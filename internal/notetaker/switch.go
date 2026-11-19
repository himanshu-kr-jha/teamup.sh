package notetaker

// Notes on or off in one call, and paused and resumed during it (the owner's
// request of 18 November 2026).
//
// Whether a call's notes are on is kept with the call (calls.notes_on, empty
// for the organization's default), and every change in call_notes_changes;
// the calls package writes both, since it knows who is in the call. This side
// follows them: the note-taker stays in the call while notes are paused, so
// everybody still sees it there, but it stops listening to every microphone,
// so the media server sends it no audio at all. Nothing said then is kept,
// cut into clips or sent to speech to text. Resumed, it listens again.
//
// The server that is asked to pause may not be the one taking the notes, so
// it is told three ways: directly when it is the same server, by a message to
// the room (which the note-taker is in, on whichever server), and by reading
// the state again with every heartbeat, in case that message was lost. In
// every case it reads the state from the database, never from the message.

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/rtp"
)

// NotesTopic is the LiveKit data topic that tells a room its notes were paused
// or resumed, as calls.MeetingNotesTopic does.
const NotesTopic = "teamup.meeting-notes"

// Available says whether the note-taker can take notes in the organization's
// calls at all, and whether it does so in every call unless somebody turns
// them off.
func (s *Service) Available(ctx context.Context, orgID uuid.UUID) (available, everyCall bool) {
	set, err := s.Settings(ctx, orgID)
	if err != nil {
		s.logger.Warn().Err(err).Msg("notetaker: could not read the settings")
		return false, false
	}
	ready, _ := set.Ready()
	return ready, !set.OnlyWhenAsked
}

// notesOn is whether notes are to be taken in a call now: what somebody chose,
// or else the organization's default.
func (s *Service) notesOn(ctx context.Context, callID uuid.UUID, set Settings) (bool, error) {
	var chosen *bool
	if err := s.db.QueryRow(ctx, `SELECT notes_on FROM calls WHERE id = $1`, callID).Scan(&chosen); err != nil {
		return false, err
	}
	if chosen != nil {
		return *chosen, nil
	}
	return !set.OnlyWhenAsked, nil
}

// NotesChanged is told when somebody turns a call's notes on or off. If this
// server is taking them, it follows at once; every other server is told over
// Redis (switchbus.go), and the room is told too, which the note-taker hears
// on whichever server holds it.
func (s *Service) NotesChanged(ctx context.Context, _, callID uuid.UUID) {
	s.follow(callID)
	s.announceSwitch(ctx, callID)
}

// follow has this server's note-taker in a call, if it has one, read the
// switch again.
func (s *Service) follow(callID uuid.UUID) {
	s.mu.Lock()
	sess := s.sessions[callID]
	s.mu.Unlock()
	if sess != nil {
		sess.refresh()
	}
}

// refresh reads whether notes are on and pauses or resumes to match.
func (sess *session) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	on, err := sess.svc.notesOn(ctx, sess.callID, sess.settings)
	if err != nil {
		// Not knowing is not listening: paused until it can tell.
		sess.svc.logger.Warn().Err(err).Str("call", sess.callID.String()).Msg("notetaker: could not read whether notes are on; not listening")
		sess.setPaused(true)
		return
	}
	sess.setPaused(!on)
}

// heardWhilePaused says whether a clip overlaps a pause recorded for the call,
// which may be one this server has not heard about yet: the switch is written
// down before anybody is told. Not knowing counts as paused.
func (sess *session) heardWhilePaused(ctx context.Context, clip Clip) bool {
	pauses, err := sess.svc.pauses(ctx, sess.callID)
	if err != nil {
		sess.svc.logger.Warn().Err(err).Str("call", sess.callID.String()).Msg("notetaker: could not read when notes were paused; a clip is not sent")
		return true
	}
	return overlapsAny(pauses, clip.StartedAt, clip.EndedAt)
}

// hear takes one packet of somebody's microphone. One still on its way when
// notes were paused is dropped, as the recorder drops it too.
func (sess *session) hear(rec *recorder, pkt *rtp.Packet) {
	if sess.isPaused() {
		return
	}
	rec.Packet(pkt)
}

// isPaused is whether nothing is to be written down now.
func (sess *session) isPaused() bool { return sess.paused.Load() }

// setPaused pauses or resumes. Pausing cuts every clip that is open at this
// moment, so what was said before it is kept and nothing after it is.
func (sess *session) setPaused(paused bool) {
	sess.pauseMu.Lock()
	defer sess.pauseMu.Unlock()
	if sess.paused.Load() == paused {
		return
	}
	// Set first, so a packet already on its way is dropped from now on.
	sess.paused.Store(paused)
	log := sess.svc.logger.Info().Str("call", sess.callID.String())
	if paused {
		sess.mu.Lock()
		recorders := make([]*recorder, 0, len(sess.recorders))
		for _, r := range sess.recorders {
			recorders = append(recorders, r)
		}
		sess.mu.Unlock()
		for _, r := range recorders {
			r.Cut()
		}
		log.Msg("notetaker: paused; not listening to anybody")
	} else {
		sess.mu.Lock()
		for _, r := range sess.recorders {
			r.Resume()
		}
		sess.mu.Unlock()
		log.Msg("notetaker: resumed; listening again")
	}
	sess.svc.listen(sess, !paused)
}

// listenAll subscribes to every microphone in the room, or unsubscribes from
// all of them, so that while notes are paused the media server sends none.
func (s *Service) listenAll(sess *session, on bool) {
	sess.mu.Lock()
	room := sess.room
	sess.mu.Unlock()
	if room == nil {
		return
	}
	for _, rp := range room.GetRemoteParticipants() {
		for _, tp := range rp.TrackPublications() {
			pub, ok := tp.(*lksdk.RemoteTrackPublication)
			if !ok {
				continue
			}
			if on {
				sess.listenTo(pub, rp)
				continue
			}
			sess.stopListening(pub, rp)
		}
	}
}

// Pause is a stretch of the call when notes were not taken. From is nil when
// they were off from the start, To nil when they stayed off to the end.
type Pause struct {
	From      *time.Time `json:"from,omitempty"`
	To        *time.Time `json:"to,omitempty"`
	By        string     `json:"by,omitempty"`
	ResumedBy string     `json:"resumedBy,omitempty"`
}

// Overlaps says whether anything from start to end was said or written while
// notes were off. A clip that began before a pause and ran into it overlaps
// it, and is left out whole: its words cannot be split at the pause.
func (p Pause) Overlaps(start, end time.Time) bool {
	if end.Before(start) {
		end = start
	}
	return (p.From == nil || !end.Before(*p.From)) && (p.To == nil || start.Before(*p.To))
}

// overlapsAny says whether start to end overlaps any of the pauses.
func overlapsAny(pauses []Pause, start, end time.Time) bool {
	for _, p := range pauses {
		if p.Overlaps(start, end) {
			return true
		}
	}
	return false
}

// Text says the pause plainly, at the meeting's clock.
func (p Pause) Text(loc *time.Location) string {
	at := func(t *time.Time) string { return t.In(loc).Format("15:04") }
	switch {
	case p.From != nil && p.To != nil:
		return fmt.Sprintf("Notes were paused from %s to %s by %s.", at(p.From), at(p.To), p.By)
	case p.From != nil:
		return fmt.Sprintf("Notes were paused at %s by %s, until the end of the call.", at(p.From), p.By)
	case p.To != nil:
		return fmt.Sprintf("Notes were off until %s, when %s started them.", at(p.To), p.ResumedBy)
	}
	return "Notes were off for the whole call."
}

// notesChange is one turn of the switch.
type notesChange struct {
	On bool
	By string
	At time.Time
}

// briefOn is the shortest time notes may be back on between two pauses and
// count as on: anything shorter is one pause, so that somebody pressing the
// button over and over cannot fill the minutes with gaps, and nothing said in
// a moment's resumption is kept.
const briefOn = 5 * time.Second

// pausesOf is the stretches when notes were off, from the changes in order.
// Before the first change notes were the other way round.
func pausesOf(changes []notesChange) []Pause {
	if len(changes) == 0 {
		return nil
	}
	var out []Pause
	var open *Pause
	if changes[0].On {
		open = &Pause{}
	}
	for _, c := range changes {
		at := c.At
		switch {
		case !c.On && open == nil:
			if n := len(out); n > 0 && out[n-1].To != nil && at.Sub(*out[n-1].To) < briefOn {
				last := out[n-1]
				out = out[:n-1]
				last.To, last.ResumedBy = nil, ""
				open = &last
				continue
			}
			open = &Pause{From: &at, By: c.By}
		case c.On && open != nil:
			open.To, open.ResumedBy = &at, c.By
			out = append(out, *open)
			open = nil
		}
	}
	if open != nil {
		out = append(out, *open)
	}
	return out
}

// pauses is when a call's notes were off, from its record of changes.
func (s *Service) pauses(ctx context.Context, callID uuid.UUID) ([]Pause, error) {
	rows, err := s.db.Query(ctx, `SELECT notes_on, user_name, changed_at FROM call_notes_changes WHERE call_id = $1 ORDER BY changed_at, id`, callID)
	if err != nil {
		return nil, err
	}
	changes, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (notesChange, error) {
		var c notesChange
		err := row.Scan(&c.On, &c.By, &c.At)
		return c, err
	})
	if err != nil {
		return nil, err
	}
	return pausesOf(changes), nil
}

// outside is what was said or written while notes were on.
func outside(segments []Segment, pauses []Pause) []Segment {
	if len(pauses) == 0 {
		return segments
	}
	out := make([]Segment, 0, len(segments))
	for _, seg := range segments {
		if !overlapsAny(pauses, seg.StartedAt, seg.EndedAt) {
			out = append(out, seg)
		}
	}
	return out
}

// maxPauseLines is the most gaps named one by one in what the model reads.
const maxPauseLines = 20

// pauseSpeaker is who says, in the transcript the model reads, that notes
// were paused.
const pauseSpeaker = "TeamUp"

// withPauses puts a line where each pause began (or, off from the start, where
// notes began), so the model knows there is a gap and does not fill it.
func withPauses(segments []Segment, pauses []Pause, loc *time.Location) []Segment {
	if len(pauses) == 0 {
		return segments
	}
	marks := make([]Segment, 0, len(pauses))
	for i, p := range pauses {
		if i == maxPauseLines {
			// The rest in one line, so the model is not handed page after page of gaps.
			last := pauses[len(pauses)-1]
			at := last.To
			if last.From != nil {
				at = last.From
			}
			if at != nil {
				marks = append(marks, Segment{Speaker: pauseSpeaker, StartedAt: *at,
					Text: fmt.Sprintf("Notes were paused %d more times after this. Nothing said then was written down.", len(pauses)-maxPauseLines)})
			}
			break
		}
		at := p.To
		if p.From != nil {
			at = p.From
		}
		if at == nil {
			continue
		}
		marks = append(marks, Segment{Speaker: pauseSpeaker, StartedAt: *at, Text: p.Text(loc) + " Nothing said then was written down."})
	}
	return withChat(segments, marks)
}
