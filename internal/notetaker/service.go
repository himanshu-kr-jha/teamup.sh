// Package notetaker is the meeting note-taker: a visible participant called
// Notes that joins every call and meeting while an administrator has it on,
// transcribes what each person says, and when the call ends sends minutes to
// everybody who was in it.
package notetaker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/internal/mail"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

// ConfigKey is where the settings live among the organization's configuration.
const ConfigKey = "notetaker"

// IdentityPrefix starts the note-taker's identity in a room, so the page and
// the attendance record can tell it from a person.
const IdentityPrefix = "notes-"

// guestPrefix starts a guest's identity, as calls.GuestIdentityPrefix does.
const guestPrefix = "guest-"

// ConfigStore reads and writes the organization's settings.
type ConfigStore interface {
	GetConfigInto(ctx context.Context, orgID uuid.UUID, key string, dst interface{}) error
	SetConfig(ctx context.Context, orgID uuid.UUID, key string, value interface{}, isSecret bool) error
}

// Notifier tells somebody in the app and on their devices.
type Notifier interface {
	Notify(ctx context.Context, orgID, userID uuid.UUID, kind, title, body string, data map[string]any) error
}

// Mailer sends the minutes by email when a mail server is set up.
type Mailer interface {
	Send(ctx context.Context, orgID uuid.UUID, msg mail.Message) error
	Configured(ctx context.Context, orgID uuid.UUID) bool
}

// LiveKit is where the note-taker joins rooms.
type LiveKit struct {
	URL       string
	APIKey    string
	APISecret string
}

// Service runs the note-taker.
type Service struct {
	db       *pgxpool.Pool
	config   ConfigStore
	livekit  LiveKit
	notifier Notifier
	mailer   Mailer
	baseURL  string
	logger   zerolog.Logger

	// For tests: the clock, and what joins a room.
	now  func() time.Time
	join func(s *session) error

	// GracePeriod is how long it waits in an empty room for somebody to come
	// back before it writes the minutes.
	GracePeriod time.Duration
	// Heartbeat is how often a server says it is still at work on a call's
	// notes, and StaleAfter how long without that before another takes them on.
	Heartbeat  time.Duration
	StaleAfter time.Duration

	// The calls this server is taking notes in, so it never takes one twice, and
	// so it can hand them over when it stops.
	mu       sync.Mutex
	sessions map[uuid.UUID]*session
	stopping bool
	running  sync.WaitGroup
}

// NewService builds the note-taker. baseURL is TeamUp's own address, for the
// link in the email.
func NewService(db *pgxpool.Pool, config ConfigStore, lk LiveKit, baseURL string, logger zerolog.Logger) *Service {
	s := &Service{db: db, config: config, livekit: lk, baseURL: strings.TrimRight(baseURL, "/"), logger: logger,
		now: time.Now, GracePeriod: 30 * time.Second, Heartbeat: 15 * time.Second, StaleAfter: 45 * time.Second,
		sessions: map[uuid.UUID]*session{}}
	s.join = s.joinRoom
	return s
}

func (s *Service) SetNotifier(n Notifier) { s.notifier = n }
func (s *Service) SetMailer(m Mailer)     { s.mailer = m }

// Settings reads the organization's settings; none saved is off.
func (s *Service) Settings(ctx context.Context, orgID uuid.UUID) (Settings, error) {
	var set Settings
	err := s.config.GetConfigInto(ctx, orgID, ConfigKey, &set)
	if err != nil && !errors.Is(err, apperrors.ErrNotFound) {
		return Settings{}, err
	}
	return set, nil
}

// PersonJoined is called when somebody connects to a call's room. The first
// time for a call, and only on one server, the note-taker joins it.
func (s *Service) PersonJoined(ctx context.Context, orgID, callID uuid.UUID, roomName string) {
	set, err := s.Settings(ctx, orgID)
	if err != nil {
		s.logger.Warn().Err(err).Msg("notetaker: could not read the settings")
		return
	}
	if ok, _ := set.Ready(); !ok {
		return
	}
	tag, err := s.db.Exec(ctx, `INSERT INTO meeting_notes (call_id, org_id) VALUES ($1, $2) ON CONFLICT (call_id) DO NOTHING`, callID, orgID)
	if err != nil {
		s.logger.Warn().Err(err).Msg("notetaker: could not claim the call")
		return
	}
	if tag.RowsAffected() == 0 {
		return // already being taken, here or on another server
	}
	s.start(orgID, callID, roomName, set)
}

// start takes notes in a call, newly or carrying on from where another server
// left off: what was said before is already saved, and this adds to it.
func (s *Service) start(orgID, callID uuid.UUID, roomName string, set Settings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions[callID] != nil {
		return
	}
	if s.stopping {
		go s.release(callID)
		return
	}
	sess := &session{svc: s, orgID: orgID, callID: callID, roomName: roomName, settings: set,
		recorders: map[string]*recorder{}, clips: make(chan Clip, 512), done: make(chan struct{})}
	s.sessions[callID] = sess
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		defer func() {
			s.mu.Lock()
			delete(s.sessions, callID)
			s.mu.Unlock()
		}()
		sess.run()
	}()
}

// session is the note-taker in one call.
type session struct {
	svc      *Service
	orgID    uuid.UUID
	callID   uuid.UUID
	roomName string
	settings Settings

	mu        sync.Mutex
	room      *lksdk.Room
	recorders map[string]*recorder
	grace     *time.Timer
	clips     chan Clip
	failures  int
	clipCount int
	lastError string

	finishOnce sync.Once
	done       chan struct{}
	// Set when the server is stopping rather than the call ending: what was
	// heard is saved and the notes are left for the next server to carry on.
	handingOver bool
}

func (sess *session) run() {
	log := sess.svc.logger.With().Str("call", sess.callID.String()).Logger()
	if err := sess.svc.join(sess); err != nil {
		log.Warn().Err(err).Msg("notetaker: could not join the call")
		sess.svc.markFailed(sess.callID, "The note-taker could not join the call: "+err.Error())
		return
	}

	// Transcribing runs alongside the meeting, three clips at a time, so the
	// minutes are ready soon after it ends.
	var workers sync.WaitGroup
	stt := sess.settings.transcriber()
	for i := 0; i < 3; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for clip := range sess.clips {
				sess.transcribe(stt, clip)
			}
		}()
	}

	tick := time.NewTicker(300 * time.Millisecond)
	beat := time.NewTicker(sess.svc.Heartbeat)
	limit := time.NewTimer(10 * time.Hour)
	defer tick.Stop()
	defer beat.Stop()
	defer limit.Stop()
	sess.checkEmpty()
loop:
	for {
		select {
		case <-tick.C:
			sess.mu.Lock()
			for _, r := range sess.recorders {
				r.Tick()
			}
			sess.mu.Unlock()
		case <-beat.C:
			sess.svc.beat(sess.callID)
		case <-limit.C:
			sess.finish()
		case <-sess.done:
			break loop
		}
	}

	sess.mu.Lock()
	for _, r := range sess.recorders {
		r.Flush()
	}
	sess.mu.Unlock()
	close(sess.clips)
	workers.Wait()

	sess.mu.Lock()
	handingOver := sess.handingOver
	sess.mu.Unlock()
	if handingOver {
		sess.svc.release(sess.callID)
		log.Info().Msg("notetaker: handed over, for the next server to carry on")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), composeTimeout)
	defer cancel()
	stop := sess.svc.keepAlive(sess.callID)
	defer stop()
	sess.svc.writeMinutes(ctx, sess)
}

// joinRoom connects to LiveKit as a visible agent participant.
func (s *Service) joinRoom(sess *session) error {
	cb := lksdk.NewRoomCallback()
	cb.OnTrackSubscribed = func(track *webrtc.TrackRemote, _ *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
		if track.Kind() != webrtc.RTPCodecTypeAudio || rp.Kind() == lksdk.ParticipantAgent {
			return
		}
		rec := sess.recorderFor(rp.Identity(), rp.Name())
		s.logger.Info().Str("call", sess.callID.String()).Str("who", rp.Name()).Str("codec", track.Codec().MimeType).Msg("notetaker: listening")
		go func() {
			for {
				pkt, _, err := track.ReadRTP()
				if err != nil {
					rec.Flush()
					return
				}
				rec.Packet(pkt)
			}
		}()
	}
	// Somebody can be heard speaking before their audio is subscribed here, so
	// the recorder is made by whichever comes first, and neither is lost.
	cb.OnIsSpeakingChanged = func(p lksdk.Participant) {
		// Itself, or another agent, is not somebody to write down.
		if strings.HasPrefix(p.Identity(), IdentityPrefix) {
			return
		}
		// A member, or a guest let in by the meeting's link.
		if _, err := uuid.Parse(strings.TrimPrefix(p.Identity(), guestPrefix)); err != nil {
			return
		}
		s.logger.Debug().Str("who", p.Name()).Bool("speaking", p.IsSpeaking()).Msg("notetaker: speaking")
		sess.recorderFor(p.Identity(), p.Name()).Speaking(p.IsSpeaking())
	}
	cb.OnParticipantConnected = func(*lksdk.RemoteParticipant) { sess.checkEmpty() }
	cb.OnParticipantDisconnected = func(*lksdk.RemoteParticipant) { sess.checkEmpty() }
	cb.OnDisconnected = func() { sess.finish() }

	url := strings.Replace(strings.Replace(s.livekit.URL, "https://", "wss://", 1), "http://", "ws://", 1)
	room, err := lksdk.ConnectToRoom(url, lksdk.ConnectInfo{
		APIKey:              s.livekit.APIKey,
		APISecret:           s.livekit.APISecret,
		RoomName:            sess.roomName,
		ParticipantIdentity: IdentityPrefix + sess.callID.String(),
		ParticipantName:     "Notes",
		ParticipantKind:     lksdk.ParticipantAgent,
		ParticipantMetadata: `{"role":"notetaker"}`,
	}, cb, lksdk.WithAutoSubscribe(true))
	if err != nil {
		return err
	}
	sess.mu.Lock()
	sess.room = room
	sess.mu.Unlock()
	return nil
}

func (sess *session) recorderFor(identity, name string) *recorder {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if r, ok := sess.recorders[identity]; ok {
		return r
	}
	if name == "" {
		name = "Somebody"
	}
	r := newRecorder(Speaker{UserID: identity, Name: name}, func(c Clip) {
		select {
		case sess.clips <- c:
		default:
			// Far behind: dropping a clip loses a sentence, blocking would lose
			// the call's audio.
			sess.svc.logger.Warn().Msg("notetaker: transcription is behind; a clip was dropped")
		}
	}, sess.svc.now)
	sess.recorders[identity] = r
	return r
}

// checkEmpty starts the wait for an empty room, or calls it off when somebody
// is there.
func (sess *session) checkEmpty() {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	people := 0
	if sess.room != nil {
		for _, p := range sess.room.GetRemoteParticipants() {
			if p.Kind() != lksdk.ParticipantAgent {
				people++
			}
		}
	}
	if people > 0 {
		if sess.grace != nil {
			sess.grace.Stop()
			sess.grace = nil
		}
		return
	}
	if sess.grace == nil {
		sess.grace = time.AfterFunc(sess.svc.GracePeriod, sess.finish)
	}
}

// handOver stops listening because the server is stopping, not because the
// call has ended.
func (sess *session) handOver() {
	sess.mu.Lock()
	sess.handingOver = true
	sess.mu.Unlock()
	sess.finish()
}

func (sess *session) finish() {
	sess.finishOnce.Do(func() {
		sess.mu.Lock()
		room := sess.room
		sess.mu.Unlock()
		if room != nil {
			room.Disconnect()
		}
		close(sess.done)
	})
}

func (sess *session) transcribe(stt Transcriber, clip Clip) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sess.mu.Lock()
	sess.clipCount++
	sess.mu.Unlock()
	text, err := stt.Transcribe(ctx, clip.Audio, "clip.ogg")
	if err != nil {
		sess.mu.Lock()
		sess.failures++
		sess.lastError = err.Error()
		sess.mu.Unlock()
		sess.svc.logger.Warn().Err(err).Msg("notetaker: a clip could not be transcribed")
		return
	}
	if text == "" {
		return
	}
	var userID *uuid.UUID
	if id, err := uuid.Parse(clip.Speaker.UserID); err == nil {
		userID = &id
	}
	if _, err := sess.svc.db.Exec(ctx, `
		INSERT INTO meeting_note_segments (call_id, user_id, speaker_name, started_at, ended_at, text)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		sess.callID, userID, clip.Speaker.Name, clip.StartedAt, clip.EndedAt, text); err != nil {
		sess.svc.logger.Warn().Err(err).Msg("notetaker: could not save what was said")
	}
}

func (s *Service) markFailed(callID uuid.UUID, why string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = s.db.Exec(ctx, `UPDATE meeting_notes SET status = 'failed', error = $2, finished_at = NOW() WHERE call_id = $1`, callID, why)
}

// writeMinutes turns what was said into minutes and sends them to everybody
// who was there.
func (s *Service) writeMinutes(ctx context.Context, sess *session) {
	sttProblem := ""
	sess.mu.Lock()
	if sess.failures > 0 {
		sttProblem = sess.lastError
	}
	sess.mu.Unlock()
	s.compose(ctx, sess.orgID, sess.callID, sess.settings, sttProblem)
}

// compose writes a call's minutes from its saved transcript and delivers them,
// or records why it could not and tells the people who were there, who can
// try again from the minutes page once the reason is put right.
func (s *Service) compose(ctx context.Context, orgID, callID uuid.UUID, set Settings, sttProblem string) {
	_, _ = s.db.Exec(ctx, `UPDATE meeting_notes SET status = 'writing', error = NULL WHERE call_id = $1`, callID)
	segments, err := s.segments(ctx, callID)
	if err != nil {
		s.markFailed(callID, "Could not read the transcript.")
		return
	}
	if len(segments) == 0 {
		why := "Nothing was said that could be transcribed."
		if sttProblem != "" {
			why = "Speech to text did not work: " + sttProblem
		}
		_, _ = s.db.Exec(ctx, `UPDATE meeting_notes SET status = 'empty', error = $2, finished_at = NOW() WHERE call_id = $1`, callID, why)
		return
	}

	title, startedAt, attendees, err := s.meeting(ctx, callID)
	if err != nil {
		s.markFailed(callID, "Could not read the meeting.")
		return
	}
	names := make([]string, 0, len(attendees))
	for _, a := range attendees {
		names = append(names, a.name)
	}
	// Guests are in the minutes, though they have no account to receive them.
	guests, err := s.guestNames(ctx, callID)
	if err != nil {
		s.logger.Warn().Err(err).Msg("notetaker: could not read the guests")
	}
	for _, g := range guests {
		names = append(names, g+" (guest)")
	}
	minutes, err := Write(ctx, set.writer(), title, startedAt, names, segments, time.UTC)
	if err != nil {
		log := s.logger.Warn().Err(err).Str("call", callID.String())
		// What the model said instead, so the next failure can be understood.
		var unusable *UnusableAnswer
		if errors.As(err, &unusable) {
			log = log.Str("answer", unusable.Sample)
		}
		log.Msg("notetaker: the minutes could not be written")
		s.markFailed(callID, err.Error())
		s.notify(ctx, orgID, callID, attendees, "minutes.failed", "Minutes could not be written: "+title,
			"The transcript is kept. Open it to see why, and to try again.")
		return
	}
	raw, _ := json.Marshal(minutes)
	if _, err := s.db.Exec(ctx, `UPDATE meeting_notes SET status = 'ready', minutes = $2, error = NULL, finished_at = NOW() WHERE call_id = $1`, callID, raw); err != nil {
		s.logger.Warn().Err(err).Msg("notetaker: could not save the minutes")
		return
	}
	s.deliver(ctx, orgID, callID, minutes, attendees)
}

func (s *Service) notify(ctx context.Context, orgID, callID uuid.UUID, attendees []attendee, kind, title, body string) {
	if s.notifier == nil {
		return
	}
	link := "/calls/" + callID.String() + "/minutes"
	for _, a := range attendees {
		if err := s.notifier.Notify(ctx, orgID, a.id, kind, title, body,
			map[string]any{"callId": callID.String(), "url": link}); err != nil {
			s.logger.Warn().Err(err).Msg("notetaker: could not notify somebody")
		}
	}
}

type attendee struct {
	id    uuid.UUID
	name  string
	email string
}

func (s *Service) meeting(ctx context.Context, callID uuid.UUID) (string, time.Time, []attendee, error) {
	var title string
	var started *time.Time
	var created time.Time
	if err := s.db.QueryRow(ctx, `
		SELECT COALESCE(NULLIF(c.title, ''), ch.name, 'Call'), c.started_at, c.created_at
		FROM calls c LEFT JOIN channels ch ON ch.id = c.channel_id WHERE c.id = $1`, callID).Scan(&title, &started, &created); err != nil {
		return "", time.Time{}, nil, err
	}
	when := created
	if started != nil {
		when = *started
	}
	rows, err := s.db.Query(ctx, `
		SELECT u.id, u.name, u.email FROM call_participants p JOIN users u ON u.id = p.user_id
		WHERE p.call_id = $1 AND p.joined_at IS NOT NULL ORDER BY p.joined_at`, callID)
	if err != nil {
		return "", time.Time{}, nil, err
	}
	defer rows.Close()
	var out []attendee
	for rows.Next() {
		var a attendee
		if err := rows.Scan(&a.id, &a.name, &a.email); err != nil {
			return "", time.Time{}, nil, err
		}
		out = append(out, a)
	}
	return title, when, out, rows.Err()
}

func (s *Service) guestNames(ctx context.Context, callID uuid.UUID) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT name FROM call_guests WHERE call_id = $1 GROUP BY name ORDER BY MIN(created_at)`, callID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func (s *Service) segments(ctx context.Context, callID uuid.UUID) ([]Segment, error) {
	rows, err := s.db.Query(ctx, `SELECT speaker_name, started_at, text FROM meeting_note_segments WHERE call_id = $1 ORDER BY started_at, id`, callID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Segment
	for rows.Next() {
		var seg Segment
		if err := rows.Scan(&seg.Speaker, &seg.StartedAt, &seg.Text); err != nil {
			return nil, err
		}
		out = append(out, seg)
	}
	return out, rows.Err()
}

func (s *Service) deliver(ctx context.Context, orgID, callID uuid.UUID, m Minutes, attendees []attendee) {
	link := "/calls/" + callID.String() + "/minutes"
	summary := m.Summary
	if r := []rune(summary); len(r) > 140 {
		summary = string(r[:140]) + "…"
	}
	s.notify(ctx, orgID, callID, attendees, "minutes.ready", "Minutes: "+m.Title, summary)
	if s.mailer == nil || !s.mailer.Configured(ctx, orgID) {
		return
	}
	for _, a := range attendees {
		if a.email == "" {
			continue
		}
		body := m.Text(time.UTC)
		if s.baseURL != "" {
			body += "\nRead them, with the transcript: " + s.baseURL + link + "\n"
		}
		if err := s.mailer.Send(ctx, orgID, mail.Message{To: a.email, Subject: "Minutes: " + m.Title, Body: body}); err != nil {
			s.logger.Warn().Err(err).Msg("notetaker: could not email the minutes")
		}
	}
}

// Notes is what the minutes page shows.
type Notes struct {
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	Minutes    *Minutes  `json:"minutes,omitempty"`
	Transcript []Segment `json:"transcript"`
}

// mayRead says whether somebody may read a call's notes: they were in it, or
// they administer the organization.
func (s *Service) mayRead(ctx context.Context, callID, userID uuid.UUID, isAdmin bool) (bool, error) {
	if isAdmin {
		return true, nil
	}
	var was bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM call_participants WHERE call_id = $1 AND user_id = $2 AND joined_at IS NOT NULL)`, callID, userID).Scan(&was)
	return was, err
}

// ErrNoNotes is a call with no notes, or one the caller may not read.
var ErrNoNotes = apperrors.ErrNotFound

// NotesFor returns a call's notes to somebody who was in it, or to an
// administrator.
func (s *Service) NotesFor(ctx context.Context, orgID, callID, userID uuid.UUID, isAdmin bool) (*Notes, error) {
	var n Notes
	var raw []byte
	var errText *string
	err := s.db.QueryRow(ctx, `SELECT status, minutes, error FROM meeting_notes WHERE call_id = $1 AND org_id = $2`, callID, orgID).Scan(&n.Status, &raw, &errText)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoNotes
	}
	if err != nil {
		return nil, fmt.Errorf("read notes: %w", err)
	}
	if ok, err := s.mayRead(ctx, callID, userID, isAdmin); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrNoNotes
	}
	if errText != nil {
		n.Error = *errText
	}
	if len(raw) > 0 {
		var m Minutes
		if err := json.Unmarshal(raw, &m); err == nil {
			n.Minutes = &m
		}
	}
	n.Transcript, err = s.segments(ctx, callID)
	if n.Transcript == nil {
		n.Transcript = []Segment{}
	}
	return &n, err
}
