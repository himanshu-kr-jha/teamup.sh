package notetaker

// What happened in a real meeting: TeamUp was deployed 47 minutes in, the
// server taking the notes went with it, and the notes stayed "being taken" for
// good, with nothing after that heard and no minutes sent.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func (w *world) status(t *testing.T) (status string, released bool) {
	t.Helper()
	if err := w.svc.db.QueryRow(context.Background(),
		`SELECT status, heartbeat_at = '-infinity' FROM meeting_notes WHERE call_id = $1`, w.callID).Scan(&status, &released); err != nil {
		t.Fatal(err)
	}
	return status, released
}

func TestADeployInTheMiddleOfAMeetingHandsTheNotesToTheNextServer(t *testing.T) {
	w := newWorld(t)
	srv := standIns(t)
	ctx := context.Background()
	if err := w.orgs.SetConfig(ctx, w.orgID, ConfigKey, Settings{Enabled: true, STTKey: "k", STTBaseURL: srv.URL, LLMKey: "k", LLMBaseURL: srv.URL}, true); err != nil {
		t.Fatal(err)
	}
	// The people stay in the call, so the note-taker would stay for good.
	w.svc.GracePeriod = time.Hour
	w.svc.PersonJoined(ctx, w.orgID, w.callID, "room-1")
	deadline := time.Now().Add(5 * time.Second)
	for {
		var said int
		_ = w.svc.db.QueryRow(ctx, `SELECT count(*) FROM meeting_note_segments WHERE call_id = $1`, w.callID).Scan(&said)
		if said == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("heard before the deploy: %d", said)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The old server stops: no minutes yet, the call is still going, and the
	// notes are left for the next server at once.
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	w.svc.Stop(stopCtx)
	if status, released := w.status(t); status != "taking" || !released {
		t.Fatalf("handed over: %s, released %v", status, released)
	}

	// The new server starts, finds them, and carries on in the call.
	next := NewService(w.svc.db, w.orgs, LiveKit{}, "https://teamup.example", zerolog.Nop())
	next.SetNotifier(w.sent)
	next.SetMailer(w.sent)
	next.GracePeriod = 10 * time.Millisecond
	rejoined := 0
	next.join = func(sess *session) error {
		rejoined++
		start := time.Date(2026, 11, 8, 10, 47, 0, 0, time.UTC)
		sess.clips <- Clip{Speaker: Speaker{UserID: w.bo.String(), Name: "Bo Baker"}, StartedAt: start, EndedAt: start.Add(time.Second), Audio: []byte("And after the deploy, still heard")}
		return nil
	}
	next.Resume(ctx)
	notes := w.waitFor(t, w.router(t, w.ada, "member"))
	if notes.Status != "ready" || rejoined != 1 {
		t.Fatalf("minutes after carrying on (rejoined %d): %+v", rejoined, notes)
	}
	if len(notes.Transcript) != 3 || !strings.Contains(notes.Transcript[2].Text, "after the deploy") {
		t.Errorf("before and after the deploy, in one transcript: %+v", notes.Transcript)
	}
	w.sent.mu.Lock()
	defer w.sent.mu.Unlock()
	if len(w.sent.notified) != 2 {
		t.Errorf("everybody sent the minutes once: %d", len(w.sent.notified))
	}
}

func TestNotesLeftByAServerThatDiedAreWrittenUpOnceTheCallHasEnded(t *testing.T) {
	w := newWorld(t)
	srv := standIns(t)
	ctx := context.Background()
	if err := w.orgs.SetConfig(ctx, w.orgID, ConfigKey, Settings{Enabled: true, STTKey: "k", STTBaseURL: srv.URL, LLMKey: "k", LLMBaseURL: srv.URL}, true); err != nil {
		t.Fatal(err)
	}
	pool := w.svc.db
	// What the dead server left: what it heard, and a claim it renewed a while ago.
	if _, err := pool.Exec(ctx, `INSERT INTO meeting_notes (call_id, org_id, heartbeat_at) VALUES ($1, $2, NOW() - interval '10 minutes')`, w.callID, w.orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO meeting_note_segments (call_id, user_id, speaker_name, started_at, ended_at, text) VALUES
		($1, $2, 'Ada Archer', NOW(), NOW(), 'Let us ship on Friday'), ($1, $3, 'Bo Baker', NOW() + interval '1 second', NOW() + interval '1 second', 'Agreed')`,
		w.callID, w.ada, w.bo); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE calls SET status = 'ended', ended_at = NOW() WHERE id = $1`, w.callID); err != nil {
		t.Fatal(err)
	}

	w.svc.Resume(ctx)
	notes := w.waitFor(t, w.router(t, w.ada, "member"))
	if notes.Status != "ready" || notes.Minutes.Decisions[0] != "Ship on Friday" {
		t.Fatalf("written up: %+v", notes)
	}
	if w.joins != 0 {
		t.Errorf("an ended call is not joined again: %d", w.joins)
	}
}

func TestNotesAServerIsStillTakingAreLeftAlone(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	_ = w.orgs.SetConfig(ctx, w.orgID, ConfigKey, Settings{Enabled: true, STTKey: "k", LLMKey: "k"}, true)
	if _, err := w.svc.db.Exec(ctx, `INSERT INTO meeting_notes (call_id, org_id, heartbeat_at) VALUES ($1, $2, NOW() - interval '5 seconds')`, w.callID, w.orgID); err != nil {
		t.Fatal(err)
	}
	w.svc.Resume(ctx)
	time.Sleep(100 * time.Millisecond)
	if status, _ := w.status(t); status != "taking" || w.joins != 0 {
		t.Errorf("claimed a moment ago, so somebody else's: %s, joined %d", status, w.joins)
	}
}
