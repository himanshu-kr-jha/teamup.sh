package notetaker

// Notes on or off in one call, and paused and resumed during it. The room is
// replaced by a recorder fed packets by hand, speech to text and the model by
// local stand-ins that count what they are sent.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pion/rtp"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// lockedClock is a clock the test moves, read from the session's goroutines.
type lockedClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *lockedClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *lockedClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// counting stands in for Sarvam, numbering what it hears, and for Claude,
// keeping what it was asked.
type counting struct {
	heard atomic.Int32
	mu    sync.Mutex
	asked []string
}

func (c *counting) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/speech-to-text":
			n := c.heard.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]string{"transcript": fmt.Sprintf("Clip %d", n)})
		case "/v1/messages":
			var body struct {
				Messages []struct{ Content string } `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			c.mu.Lock()
			c.asked = append(c.asked, body.Messages[0].Content)
			c.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"content": []map[string]string{{"type": "text",
				"text": `{"summary": "They talked.", "decisions": [], "actionItems": [], "openQuestions": []}`}}})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (c *counting) prompt(t *testing.T) string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.asked) == 0 {
		t.Fatal("the model was never asked")
	}
	return c.asked[len(c.asked)-1]
}

func (c *counting) waitHeard(t *testing.T, want int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.heard.Load() < want && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := c.heard.Load(); got != want {
		t.Fatalf("speech to text heard %d clips, want %d", got, want)
	}
}

// turn is somebody in the call turning the notes on or off, as the calls
// package records it.
func (w *world) turn(t *testing.T, on bool, by string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := w.svc.db.Exec(ctx, `UPDATE calls SET notes_on = $2 WHERE id = $1`, w.callID, on); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.db.Exec(ctx, `INSERT INTO call_notes_changes (call_id, notes_on, user_id, user_name, changed_at) VALUES ($1, $2, $3, $4, $5)`,
		w.callID, on, w.bo, by, at); err != nil {
		t.Fatal(err)
	}
}

func (w *world) chat(t *testing.T, at time.Time, text string) {
	t.Helper()
	if _, err := w.svc.db.Exec(context.Background(), `INSERT INTO call_messages (id, call_id, sender_user_id, sender_name, content, created_at) VALUES ($1, $2, $3, 'Ada Archer', $4, $5)`,
		uuid.New(), w.callID, w.ada, text, at); err != nil {
		t.Fatal(err)
	}
}

// speak is somebody talking for d into the note-taker, as packets from the room.
func speak(sess *session, rec *recorder, clock *lockedClock, seq *uint16, d time.Duration) {
	rec.Speaking(true)
	for elapsed := time.Duration(0); elapsed < d; elapsed += 20 * time.Millisecond {
		*seq++
		sess.hear(rec, &rtp.Packet{Header: rtp.Header{SequenceNumber: *seq, Timestamp: uint32(*seq) * 960}, Payload: []byte{0xfc, 0xff, 0xfe}})
		clock.advance(20 * time.Millisecond)
	}
}

// quiet is them stopping, and the moment after, when their clip closes.
func quiet(rec *recorder, clock *lockedClock) {
	rec.Speaking(false)
	clock.advance(2 * time.Second)
	rec.Tick()
}

type listens struct {
	mu  sync.Mutex
	ons []bool
}

func (l *listens) record(_ *session, on bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ons = append(l.ons, on)
}

func (l *listens) all() []bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]bool{}, l.ons...)
}

func TestPausingStopsTheClipsAndSpeechToTextAndResumingStartsThemAgain(t *testing.T) {
	w := newWorld(t)
	w.mayReadOwn(t, w.ada)
	stand := &counting{}
	srv := stand.server(t)
	ctx := context.Background()
	if err := w.orgs.SetConfig(ctx, w.orgID, ConfigKey, Settings{Enabled: true, STTKey: "k", STTBaseURL: srv.URL, LLMKey: "k", LLMBaseURL: srv.URL}, true); err != nil {
		t.Fatal(err)
	}
	clock := &lockedClock{t: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)}
	w.svc.now = clock.now
	w.svc.GracePeriod = time.Hour
	listened := &listens{}
	w.svc.listen = listened.record
	joined := make(chan *session, 1)
	w.svc.join = func(sess *session) error {
		joined <- sess
		return nil
	}
	w.svc.PersonJoined(ctx, w.orgID, w.callID, "room-1")
	sess := <-joined
	rec := sess.recorderFor(w.ada.String(), "Ada Archer")
	var seq uint16

	// Notes on: what Ada says is a clip, sent to speech to text.
	w.chat(t, clock.now(), "The deck is in the drive")
	speak(sess, rec, clock, &seq, 2*time.Second)
	quiet(rec, clock)
	stand.waitHeard(t, 1)

	// Ada is mid-sentence when Bo pauses the notes: that clip is cut there and
	// kept, and the note-taker stops listening to everybody.
	speak(sess, rec, clock, &seq, 1500*time.Millisecond)
	pausedAt := clock.now()
	w.turn(t, false, "Bo Baker", pausedAt)
	w.svc.NotesChanged(ctx, w.orgID, w.callID)
	if !sess.isPaused() {
		t.Fatal("not paused")
	}
	// It started paused, listened once it read the notes were on, and stopped.
	if got := listened.all(); len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("stopped listening to the room: %v", got)
	}
	stand.waitHeard(t, 2)
	var cutAt time.Time
	if err := w.svc.db.QueryRow(ctx, `SELECT MAX(ended_at) FROM meeting_note_segments WHERE call_id = $1`, w.callID).Scan(&cutAt); err != nil {
		t.Fatal(err)
	}
	if cutAt.After(pausedAt) {
		t.Errorf("the open clip ends at the pause, %v, not after it: %v", pausedAt, cutAt)
	}

	// While paused, nothing is kept: no clip, no speech to text, not even
	// what reaches the recorder directly.
	clock.advance(time.Minute)
	w.chat(t, clock.now(), "Salary figures, off the record")
	speak(sess, rec, clock, &seq, 3*time.Second)
	rec.Packet(&rtp.Packet{Header: rtp.Header{SequenceNumber: seq + 1, Timestamp: uint32(seq+1) * 960}, Payload: []byte{0xfc}})
	quiet(rec, clock)
	// Somebody who first speaks while paused is not heard either.
	late := sess.recorderFor(w.carol.String(), "Carol")
	speak(sess, late, clock, &seq, 2*time.Second)
	quiet(late, clock)
	time.Sleep(200 * time.Millisecond)
	if got := stand.heard.Load(); got != 2 {
		t.Fatalf("speech to text was sent %d clips while notes were paused", got-2)
	}
	late.Packet(&rtp.Packet{})
	late.mu.Lock()
	opened := late.w != nil
	late.mu.Unlock()
	if opened {
		t.Error("a recorder made while paused opened a clip")
	}

	// Resumed: listening again, and heard again.
	clock.advance(time.Minute)
	resumedAt := clock.now()
	w.turn(t, true, "Ada Archer", resumedAt)
	w.svc.NotesChanged(ctx, w.orgID, w.callID)
	if got := listened.all(); len(got) != 3 || !got[2] {
		t.Fatalf("listening again: %v", got)
	}
	clock.advance(time.Second)
	w.chat(t, clock.now(), "Back on the record")
	speak(sess, rec, clock, &seq, 2*time.Second)
	quiet(rec, clock)
	stand.waitHeard(t, 3)

	sess.finish()
	notes := w.waitFor(t, w.reader(t, w.ada))
	if notes.Status != "ready" || len(notes.Transcript) != 3 {
		t.Fatalf("minutes from the three clips heard: %+v", notes)
	}
	prompt := stand.prompt(t)
	for _, want := range []string{"Clip 1", "Clip 2", "Clip 3", "The deck is in the drive", "Back on the record",
		"TeamUp: Notes were paused from 10:00 to 10:02 by Bo Baker. Nothing said then was written down."} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the model was not given %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "Salary") {
		t.Errorf("the chat while paused went to the model:\n%s", prompt)
	}
	if p := notes.Minutes.Pauses; len(p) != 1 || !p[0].From.Equal(pausedAt) || !p[0].To.Equal(resumedAt) || p[0].By != "Bo Baker" || p[0].ResumedBy != "Ada Archer" {
		t.Errorf("the minutes say where the gap was: %+v", p)
	}
	if len(notes.Pauses) != 1 {
		t.Errorf("and so does the page: %+v", notes.Pauses)
	}
	if text := notes.Minutes.Text(time.UTC); !strings.Contains(text, "Not written down\n- Notes were paused from 10:00 to 10:02 by Bo Baker.") {
		t.Errorf("the email says so too:\n%s", text)
	}
}

func TestNotesOffFromTheStartAreNotTakenUntilSomebodyTurnsThemOn(t *testing.T) {
	ctx := context.Background()
	// The organization takes notes only when somebody asks.
	w := newWorld(t)
	srv := standIns(t)
	if err := w.orgs.SetConfig(ctx, w.orgID, ConfigKey, Settings{Enabled: true, OnlyWhenAsked: true, STTKey: "k", STTBaseURL: srv.URL, LLMKey: "k", LLMBaseURL: srv.URL}, true); err != nil {
		t.Fatal(err)
	}
	if available, every := w.svc.Available(ctx, w.orgID); !available || every {
		t.Errorf("available, only when asked: %v %v", available, every)
	}
	w.svc.GracePeriod = time.Hour
	w.svc.PersonJoined(ctx, w.orgID, w.callID, "room-1")
	time.Sleep(50 * time.Millisecond)
	var rows int
	_ = w.svc.db.QueryRow(ctx, `SELECT count(*) FROM meeting_notes WHERE call_id = $1`, w.callID).Scan(&rows)
	if w.joins != 0 || rows != 0 {
		t.Fatalf("off until asked: joined %d, %d notes", w.joins, rows)
	}
	// Somebody turns them on: the next word that the call is a meeting brings it in.
	w.turn(t, true, "Bo Baker", time.Now())
	w.svc.PersonJoined(ctx, w.orgID, w.callID, "room-1")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		n := w.joins
		w.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	w.mu.Lock()
	if w.joins != 1 {
		t.Errorf("joined once turned on: %d", w.joins)
	}
	w.mu.Unlock()
	w.svc.Stop(ctx)
}

func TestACallMadeWithNotesOffIsNotJoinedWhereEveryCallHasThem(t *testing.T) {
	ctx := context.Background()
	other := newWorld(t)
	if err := other.orgs.SetConfig(ctx, other.orgID, ConfigKey, Settings{Enabled: true, STTKey: "k", LLMKey: "k"}, true); err != nil {
		t.Fatal(err)
	}
	if _, every := other.svc.Available(ctx, other.orgID); !every {
		t.Error("every call is the default, as it always was")
	}
	if _, err := other.svc.db.Exec(ctx, `UPDATE calls SET notes_on = false WHERE id = $1`, other.callID); err != nil {
		t.Fatal(err)
	}
	other.svc.PersonJoined(ctx, other.orgID, other.callID, "room-1")
	time.Sleep(50 * time.Millisecond)
	if other.joins != 0 {
		t.Errorf("a call made with notes off: joined %d", other.joins)
	}
}

func TestPausedNotesStayPausedWhenAnotherServerCarriesOn(t *testing.T) {
	w := newWorld(t)
	srv := standIns(t)
	ctx := context.Background()
	if err := w.orgs.SetConfig(ctx, w.orgID, ConfigKey, Settings{Enabled: true, STTKey: "k", STTBaseURL: srv.URL, LLMKey: "k", LLMBaseURL: srv.URL}, true); err != nil {
		t.Fatal(err)
	}
	w.svc.GracePeriod = time.Hour
	w.svc.listen = func(*session, bool) {}
	w.svc.PersonJoined(ctx, w.orgID, w.callID, "room-1")
	// Paused, through the room's message, which reaches whichever server it is.
	w.turn(t, false, "Bo Baker", time.Now())
	w.svc.NotesChanged(ctx, w.orgID, w.callID)
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	w.svc.Stop(stopCtx)

	next := NewService(w.svc.db, w.orgs, LiveKit{}, "https://teamup.example", zerolog.Nop())
	next.GracePeriod = time.Hour
	listened := &listens{}
	next.listen = listened.record
	joined := make(chan bool, 1)
	next.join = func(sess *session) error {
		joined <- sess.isPaused()
		return nil
	}
	next.Resume(ctx)
	select {
	case paused := <-joined:
		if !paused {
			t.Fatal("the next server joined listening, though the notes were paused")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the next server did not carry on")
	}
	if got := listened.all(); len(got) != 0 {
		t.Errorf("it never listened: %v", got)
	}
	// Somebody resumes them, and the next server hears it on its next look.
	w.turn(t, true, "Ada Archer", time.Now())
	next.NotesChanged(ctx, w.orgID, w.callID)
	if got := listened.all(); len(got) != 1 || !got[0] {
		t.Errorf("listening again on the next server: %v", got)
	}
	next.Stop(stopCtx)
}

func TestMinutesWrittenAgainStillLeaveOutWhatWasSaidWhilePaused(t *testing.T) {
	w := newWorld(t)
	stand := &counting{}
	srv := stand.server(t)
	ctx := context.Background()
	if err := w.orgs.SetConfig(ctx, w.orgID, ConfigKey, Settings{Enabled: true, STTKey: "k", STTBaseURL: srv.URL, LLMKey: "k", LLMBaseURL: srv.URL}, true); err != nil {
		t.Fatal(err)
	}
	at := func(hh, mm int) time.Time { return time.Date(2026, 11, 18, hh, mm, 0, 0, time.UTC) }
	pool := w.svc.db
	if _, err := pool.Exec(ctx, `INSERT INTO meeting_notes (call_id, org_id, status, error) VALUES ($1, $2, 'failed', 'the model was down')`, w.callID, w.orgID); err != nil {
		t.Fatal(err)
	}
	for _, s := range []struct {
		at   time.Time
		text string
	}{{at(10, 1), "Before the pause"}, {at(10, 7), "Somehow kept while paused"}, {at(10, 4).Add(50 * time.Second), "Ran on into the pause"}, {at(10, 15), "After it"}} {
		if _, err := pool.Exec(ctx, `INSERT INTO meeting_note_segments (call_id, user_id, speaker_name, started_at, ended_at, text) VALUES ($1, $2, 'Ada Archer', $3, $4, $5)`,
			w.callID, w.ada, s.at, s.at.Add(20*time.Second), s.text); err != nil {
			t.Fatal(err)
		}
	}
	w.turn(t, false, "Ravi Rao", at(10, 5))
	w.turn(t, true, "Ravi Rao", at(10, 12))
	w.chat(t, at(10, 6), "Typed while paused")

	if err := w.svc.Retry(ctx, w.orgID, w.callID, w.ada, true); err != nil {
		t.Fatal(err)
	}
	notes := w.waitFor(t, w.router(t, w.ada, "admin"))
	if notes.Status != "ready" {
		t.Fatalf("written again: %+v", notes)
	}
	prompt := stand.prompt(t)
	if !strings.Contains(prompt, "[10:05] TeamUp: Notes were paused from 10:05 to 10:12 by Ravi Rao.") ||
		!strings.Contains(prompt, "Before the pause") || !strings.Contains(prompt, "After it") {
		t.Errorf("the recorded parts and the gap:\n%s", prompt)
	}
	if strings.Contains(prompt, "while paused") || strings.Contains(prompt, "into the pause") {
		t.Errorf("nothing from the paused stretch, nor what ran into it:\n%s", prompt)
	}
	// Nor in the transcript the minutes page shows.
	if len(notes.Transcript) != 2 || notes.Transcript[0].Text != "Before the pause" || notes.Transcript[1].Text != "After it" {
		t.Errorf("the transcript shown: %+v", notes.Transcript)
	}
}

func TestPausesAreReadFromTheChanges(t *testing.T) {
	at := func(mm int) time.Time { return time.Date(2026, 11, 18, 10, mm, 0, 0, time.UTC) }
	cases := []struct {
		name    string
		changes []notesChange
		want    []string
	}{
		{"never changed", nil, nil},
		{"paused and resumed", []notesChange{{false, "Ravi", at(5)}, {true, "Mia", at(12)}},
			[]string{"Notes were paused from 10:05 to 10:12 by Ravi."}},
		{"paused to the end", []notesChange{{false, "Ravi", at(5)}},
			[]string{"Notes were paused at 10:05 by Ravi, until the end of the call."}},
		{"off from the start", []notesChange{{true, "Mia", at(3)}, {false, "Ravi", at(20)}, {true, "Ravi", at(25)}},
			[]string{"Notes were off until 10:03, when Mia started them.", "Notes were paused from 10:20 to 10:25 by Ravi."}},
	}
	for _, c := range cases {
		var got []string
		for _, p := range pausesOf(c.changes) {
			got = append(got, p.Text(time.UTC))
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	pauses := pausesOf(cases[3].changes)
	kept := outside([]Segment{{StartedAt: at(1)}, {StartedAt: at(3)}, {StartedAt: at(21)}, {StartedAt: at(25)}}, pauses)
	if len(kept) != 2 || !kept[0].StartedAt.Equal(at(3)) || !kept[1].StartedAt.Equal(at(25)) {
		t.Errorf("kept only what was said while on: %+v", kept)
	}
}

// The room's word that notes were paused can be lost, or reach the server
// taking them late: whatever it hears meanwhile is checked against the switch
// as written down, and never sent to speech to text or kept.
func TestWhatIsHeardBeforeThePauseArrivesIsNeverSentOrKept(t *testing.T) {
	w := newWorld(t)
	stand := &counting{}
	srv := stand.server(t)
	ctx := context.Background()
	if err := w.orgs.SetConfig(ctx, w.orgID, ConfigKey, Settings{Enabled: true, STTKey: "k", STTBaseURL: srv.URL, LLMKey: "k", LLMBaseURL: srv.URL}, true); err != nil {
		t.Fatal(err)
	}
	w.svc.GracePeriod = time.Hour
	w.svc.listen = func(*session, bool) {}
	joined := make(chan *session, 1)
	w.svc.join = func(sess *session) error {
		joined <- sess
		return nil
	}
	w.svc.PersonJoined(ctx, w.orgID, w.callID, "room-1")
	sess := <-joined
	start := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	clip := func(from, to time.Duration, said string) Clip {
		return Clip{Speaker: Speaker{UserID: w.ada.String(), Name: "Ada Archer"}, StartedAt: start.Add(from), EndedAt: start.Add(to), Audio: []byte(said)}
	}
	sess.clips <- clip(0, 3*time.Second, "before")
	stand.waitHeard(t, 1)

	// Paused at 10:00:10, and this server is never told.
	w.turn(t, false, "Bo Baker", start.Add(10*time.Second))
	sess.clips <- clip(8*time.Second, 12*time.Second, "ran into it")
	sess.clips <- clip(20*time.Second, 25*time.Second, "after it")
	time.Sleep(300 * time.Millisecond)
	if got := stand.heard.Load(); got != 1 {
		t.Errorf("%d clips from the pause went to speech to text", got-1)
	}
	var saved int
	_ = w.svc.db.QueryRow(ctx, `SELECT count(*) FROM meeting_note_segments WHERE call_id = $1`, w.callID).Scan(&saved)
	if saved != 1 {
		t.Errorf("kept %d stretches, want only the one before the pause", saved)
	}
	w.svc.Stop(ctx)
}

// Not knowing whether notes are on is not listening.
func TestANoteTakerThatCannotReadTheSwitchDoesNotListen(t *testing.T) {
	w := newWorld(t)
	listened := &listens{}
	w.svc.listen = listened.record
	sess := &session{svc: w.svc, callID: uuid.New(), recorders: map[string]*recorder{}, listening: map[string]bool{}}
	sess.paused.Store(false)
	sess.refresh() // no such call: the read fails
	if !sess.isPaused() {
		t.Error("listening without knowing whether notes are on")
	}
	if got := listened.all(); len(got) != 1 || got[0] {
		t.Errorf("stopped listening: %v", got)
	}
}

// A pause that lands while a microphone is being asked for: the microphone is
// let go after it, and asked for again on resume, so nobody goes unheard.
func TestAPauseInTheMiddleOfSubscribingIsUndoneAndResumeSubscribesAgain(t *testing.T) {
	w := newWorld(t)
	sess := &session{svc: w.svc, callID: w.callID, recorders: map[string]*recorder{}, listening: map[string]bool{}}
	w.svc.listen = func(s *session, on bool) {
		if !on {
			_ = s.want("TR_ada", false, func(bool) error { return nil })
		}
	}
	var mu sync.Mutex
	var asked []bool
	inside := make(chan struct{})
	release := make(chan struct{})
	set := func(on bool) error {
		mu.Lock()
		asked = append(asked, on)
		mu.Unlock()
		if on && len(asked) == 1 {
			close(inside)
			<-release
		}
		return nil
	}
	done := make(chan struct{})
	go func() {
		_ = sess.want("TR_ada", true, set)
		close(done)
	}()
	<-inside
	paused := make(chan struct{})
	go func() {
		sess.paused.Store(true)
		_ = sess.want("TR_ada", false, set)
		close(paused)
	}()
	time.Sleep(50 * time.Millisecond)
	close(release)
	<-done
	<-paused
	sess.paused.Store(false)
	if err := sess.want("TR_ada", true, set); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(asked) != 3 || !asked[0] || asked[1] || !asked[2] {
		t.Errorf("asked, let go, asked again: %v", asked)
	}
	if !sess.listening["TR_ada"] {
		t.Error("not listening after the resume")
	}
}

// Somebody pressing the button over and over makes one gap, not dozens, and a
// call with many real pauses names at most maxPauseLines of them to the model.
func TestQuickTogglesAreOneGapAndTheModelIsNotFlooded(t *testing.T) {
	at := func(s int) time.Time {
		return time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC).Add(time.Duration(s) * time.Second)
	}
	var changes []notesChange
	for i := 0; i < 20; i++ {
		changes = append(changes, notesChange{false, "Ravi", at(60 + 2*i)}, notesChange{true, "Ravi", at(61 + 2*i)})
	}
	got := pausesOf(changes)
	if len(got) != 1 || !got[0].From.Equal(at(60)) || !got[0].To.Equal(at(99)) {
		t.Fatalf("one gap: %+v", got)
	}

	changes = nil
	for i := 0; i < 30; i++ {
		changes = append(changes, notesChange{false, "Ravi", at(60 * i)}, notesChange{true, "Ravi", at(60*i + 30)})
	}
	pauses := pausesOf(changes)
	if len(pauses) != 30 {
		t.Fatalf("thirty real pauses: %d", len(pauses))
	}
	marks := 0
	for _, s := range withPauses(nil, pauses, time.UTC) {
		if s.Speaker == pauseSpeaker {
			marks++
		}
	}
	if marks != maxPauseLines+1 {
		t.Errorf("%d lines about gaps, want %d and one for the rest", marks, maxPauseLines+1)
	}
}

// A switch on one server reaches the note-taker on another over Redis at
// once, without waiting for the room's message or the heartbeat.
func TestTheSwitchReachesTheNoteTakerOnAnotherServerAtOnce(t *testing.T) {
	url := os.Getenv("TEAMUP_TEST_REDIS_URL")
	if url == "" {
		t.Skip("no test Redis")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("no test Redis: %v", err)
	}

	w := newWorld(t)
	srv := standIns(t)
	if err := w.orgs.SetConfig(ctx, w.orgID, ConfigKey, Settings{Enabled: true, STTKey: "k", STTBaseURL: srv.URL, LLMKey: "k", LLMBaseURL: srv.URL}, true); err != nil {
		t.Fatal(err)
	}
	// The server taking the notes.
	w.svc.GracePeriod = time.Hour
	w.svc.Heartbeat = time.Hour
	w.svc.listen = func(*session, bool) {}
	w.svc.UseRedis(rdb)
	go w.svc.ListenForSwitches(ctx)
	joined := make(chan *session, 1)
	w.svc.join = func(sess *session) error {
		joined <- sess
		return nil
	}
	w.svc.PersonJoined(ctx, w.orgID, w.callID, "room-1")
	sess := <-joined
	// The subscription takes a moment to be in place.
	time.Sleep(200 * time.Millisecond)

	// Another server, where Bo pressed Pause.
	other := NewService(w.svc.db, w.orgs, LiveKit{}, "", zerolog.Nop())
	other.UseRedis(rdb)
	w.turn(t, false, "Bo Baker", time.Now())
	other.NotesChanged(ctx, w.orgID, w.callID)
	deadline := time.Now().Add(2 * time.Second)
	for !sess.isPaused() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !sess.isPaused() {
		t.Fatal("the note-taker on the other server was not told")
	}
	stopCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	w.svc.Stop(stopCtx)
}
