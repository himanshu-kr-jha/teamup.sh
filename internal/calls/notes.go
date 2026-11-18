package calls

// Notes on or off in a call (the owner's request of 18 November 2026): chosen
// when the call or meeting is made, and paused and resumed by anybody in it.
// Anybody, not only the host, because anybody should be able to stop being
// written down; and everybody sees who did it. Guests see it too, but only a
// member may change it.
//
// The choice is kept with the call (calls.notes_on, empty for the
// organization's default) and every change in call_notes_changes, which the
// minutes read to say where nothing was written down. The note-taker follows
// the choice (internal/notetaker/switch.go); the room is told on a topic of
// its own, so every page in it, guests' too, shows the same thing at once.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/teamup/teamup/internal/httpx"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

// MeetingNotesTopic is the LiveKit data topic that tells a room its notes were
// turned on or off, as notetaker.NotesTopic does.
const MeetingNotesTopic = "teamup.meeting-notes"

// NotesPolicy says whether the note-taker can take notes in an organization's
// calls at all, and whether it does in every call unless somebody turns them
// off (rather than only when somebody turns them on).
type NotesPolicy func(ctx context.Context, orgID uuid.UUID) (available, everyCall bool)

// errNoCall is a call that is not there, or one the caller may not see.
var errNoCall = apperrors.New(apperrors.CodeNotFound, "call not found")

// notesBurst changes in notesWindow is as often as one person may turn a
// call's notes on or off.
const (
	notesBurst  = 6
	notesWindow = time.Minute
)

// ErrNotesTooFast is more than notesBurst changes in notesWindow.
var ErrNotesTooFast = apperrors.New(apperrors.CodeRateLimited, "you have turned the notes on and off too often; wait a moment")

// ErrNotesUnavailable is turning notes on or off where there is no note-taker.
var ErrNotesUnavailable = apperrors.New(apperrors.CodeConflict, "an administrator has not set up meeting notes")

// SetNotesHooks connects the note-taker: what it can do, and what it is told
// when somebody turns a call's notes on or off.
func (s *Service) SetNotesHooks(policy NotesPolicy, changed func(ctx context.Context, orgID, callID uuid.UUID)) {
	s.notesPolicy, s.notesChanged = policy, changed
}

func (s *Service) policy(ctx context.Context, orgID uuid.UUID) (available, everyCall bool) {
	if s.notesPolicy == nil {
		return false, true
	}
	return s.notesPolicy(ctx, orgID)
}

// NotesDefault is what a page offers when a call or meeting is made.
type NotesDefault struct {
	// Available is whether there is a note-taker to turn on.
	Available bool `json:"available"`
	// EveryCall is the organization's default: notes in every call unless
	// turned off, or only when turned on.
	EveryCall bool `json:"everyCall"`
}

// NotesDefaultFor is the organization's default for a new call.
func (s *Service) NotesDefaultFor(ctx context.Context, orgID uuid.UUID) NotesDefault {
	available, every := s.policy(ctx, orgID)
	return NotesDefault{Available: available, EveryCall: every}
}

// NotesChange is one turn of a call's notes on or off.
type NotesChange struct {
	On     bool       `json:"on"`
	UserID *uuid.UUID `json:"userId,omitempty"`
	Name   string     `json:"name"`
	At     time.Time  `json:"at"`
}

// NotesState is whether a call's notes are on, and every change, latest last.
type NotesState struct {
	CallID    uuid.UUID     `json:"callId"`
	Available bool          `json:"available"`
	On        bool          `json:"on"`
	Changes   []NotesChange `json:"changes"`
}

// ChooseNotes keeps what whoever made a call or meeting chose, before it
// starts. Nil leaves the organization's default.
func (s *Service) ChooseNotes(ctx context.Context, orgID, callID uuid.UUID, on *bool) error {
	if on == nil {
		return nil
	}
	_, err := s.db.Exec(ctx, `UPDATE calls SET notes_on = $3, updated_at = NOW() WHERE id = $1 AND org_id = $2`, callID, orgID, *on)
	return err
}

// notesOf reads a call's state. q is the pool or a transaction.
func (s *Service) notesOf(ctx context.Context, q pgx.Tx, orgID, callID uuid.UUID) (NotesState, error) {
	available, every := s.policy(ctx, orgID)
	st := NotesState{CallID: callID, Available: available, Changes: []NotesChange{}}
	var chosen *bool
	var row pgx.Row
	if q != nil {
		row = q.QueryRow(ctx, `SELECT notes_on FROM calls WHERE id = $1 AND org_id = $2 FOR UPDATE`, callID, orgID)
	} else {
		row = s.db.QueryRow(ctx, `SELECT notes_on FROM calls WHERE id = $1 AND org_id = $2`, callID, orgID)
	}
	if err := row.Scan(&chosen); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return st, errNoCall
		}
		return st, fmt.Errorf("read whether notes are on: %w", err)
	}
	st.On = every
	if chosen != nil {
		st.On = *chosen
	}
	const changes = `SELECT notes_on, user_id, user_name, changed_at FROM call_notes_changes WHERE call_id = $1 ORDER BY changed_at, id`
	var rows pgx.Rows
	var err error
	if q != nil {
		rows, err = q.Query(ctx, changes, callID)
	} else {
		rows, err = s.db.Query(ctx, changes, callID)
	}
	if err != nil {
		return st, fmt.Errorf("read the notes' changes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c NotesChange
		if err := rows.Scan(&c.On, &c.UserID, &c.Name, &c.At); err != nil {
			return st, fmt.Errorf("read the notes' changes: %w", err)
		}
		c.At = c.At.UTC()
		st.Changes = append(st.Changes, c)
	}
	return st, rows.Err()
}

// NotesFor is a call's notes state for a member who may join it.
func (s *Service) NotesFor(ctx context.Context, orgID, callID, userID uuid.UUID) (NotesState, error) {
	may, err := s.MayJoin(ctx, orgID, callID, userID)
	if err != nil {
		return NotesState{}, err
	}
	if !may {
		return NotesState{}, errNoCall
	}
	return s.notesOf(ctx, nil, orgID, callID)
}

// GuestNotes is the notes state of the meeting behind a guest's link, for a
// guest in it.
func (s *Service) GuestNotes(ctx context.Context, token, rejoinKey string) (NotesState, error) {
	call, _, _, err := s.guestInMeeting(ctx, token, rejoinKey)
	if err != nil {
		return NotesState{}, err
	}
	st, err := s.notesOf(ctx, nil, call.OrgID, call.ID)
	// Who turned them on and off is for the people in the call, guests
	// included, but their accounts are not.
	for i := range st.Changes {
		st.Changes[i].UserID = nil
	}
	return st, err
}

// SetNotes turns a call's notes on or off, for a member who is in it. The
// same answer twice is not a change.
func (s *Service) SetNotes(ctx context.Context, orgID, callID, userID uuid.UUID, on bool) (NotesState, error) {
	call, err := s.GetCall(ctx, orgID, callID)
	if err != nil {
		return NotesState{}, err
	}
	may, err := s.MayJoin(ctx, orgID, callID, userID)
	if err != nil {
		return NotesState{}, err
	}
	if !may {
		return NotesState{}, errNoCall
	}
	if call.Status != "active" {
		return NotesState{}, ErrCallOver
	}
	// In the call, not only allowed into it. Without a media server there is
	// nobody to ask, and the rule above stands.
	in, err := s.mediaOf().InRoom(ctx, call.RoomName, userID.String())
	switch {
	case errors.Is(err, errNoMedia):
	case err != nil:
		return NotesState{}, fmt.Errorf("ask the media server who is in the meeting: %w", err)
	case !in:
		return NotesState{}, ErrNotInMeeting
	}
	if available, _ := s.policy(ctx, orgID); !available {
		return NotesState{}, ErrNotesUnavailable
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return NotesState{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	st, err := s.notesOf(ctx, tx, orgID, callID)
	if err != nil {
		return NotesState{}, err
	}
	if st.On == on {
		return st, nil
	}
	// A person pressing it over and over is not taking part in a meeting.
	var recent int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM call_notes_changes WHERE call_id = $1 AND user_id = $2 AND changed_at > $3`,
		callID, userID, s.clock().Add(-notesWindow)).Scan(&recent); err != nil {
		return NotesState{}, fmt.Errorf("count recent changes: %w", err)
	}
	if recent >= notesBurst {
		return NotesState{}, ErrNotesTooFast
	}
	name := s.DisplayName(ctx, userID)
	at := s.clock().UTC().Truncate(time.Microsecond)
	if _, err := tx.Exec(ctx, `UPDATE calls SET notes_on = $2, updated_at = NOW() WHERE id = $1`, callID, on); err != nil {
		return NotesState{}, fmt.Errorf("turn notes on or off: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO call_notes_changes (call_id, notes_on, user_id, user_name, changed_at) VALUES ($1, $2, $3, $4, $5)`,
		callID, on, userID, truncateRunes(name, 120), at); err != nil {
		return NotesState{}, fmt.Errorf("record the change: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return NotesState{}, err
	}
	st.On = on
	st.Changes = append(st.Changes, NotesChange{On: on, UserID: &userID, Name: name, At: at})
	s.logger.Info().Str("call", callID.String()).Bool("on", on).Msg("meeting notes turned on or off")

	// The note-taker first, if it is on this server, so it stops listening
	// before anybody is told it has.
	detached := context.WithoutCancel(ctx)
	if s.notesChanged != nil {
		s.notesChanged(detached, orgID, callID)
	}
	s.tellRoomAboutNotes(detached, call.RoomName, st)
	if on {
		// Off from the start, so nobody has joined to take them: it joins now,
		// if this is already a meeting of two or more.
		s.tellJoined(detached, &webhookCall{ID: call.ID, OrgID: orgID, Status: call.Status}, call.RoomName)
	}
	return st, nil
}

// tellRoomAboutNotes sends everybody in the room, and the note-taker, the new
// state. Guests are not given anybody's account.
func (s *Service) tellRoomAboutNotes(ctx context.Context, room string, st NotesState) {
	sent := st
	sent.Changes = make([]NotesChange, len(st.Changes))
	for i, c := range st.Changes {
		c.UserID = nil
		sent.Changes[i] = c
	}
	payload, _ := json.Marshal(sent)
	if err := s.mediaOf().SendData(ctx, room, MeetingNotesTopic, payload); err != nil && !errors.Is(err, errNoMedia) {
		s.logger.Warn().Err(err).Str("room", room).Msg("could not tell a room its notes were turned on or off")
	}
}

func (h *Handler) notesDefault(c *gin.Context) {
	orgID, _, ok := caller(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, h.service.NotesDefaultFor(c.Request.Context(), orgID))
}

func (h *Handler) getNotes(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	st, err := h.service.NotesFor(c.Request.Context(), orgID, id, userID)
	if err != nil {
		httpx.Error(c, err, "failed to read whether notes are on")
		return
	}
	c.JSON(http.StatusOK, st)
}

func (h *Handler) putNotes(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req struct {
		On *bool `json:"on"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.On == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "say whether notes are on"})
		return
	}
	st, err := h.service.SetNotes(c.Request.Context(), orgID, id, userID, *req.On)
	if err != nil {
		httpx.Error(c, err, "failed to turn notes on or off")
		return
	}
	c.JSON(http.StatusOK, st)
}

func (h *Handler) getGuestNotes(c *gin.Context) {
	st, err := h.service.GuestNotes(c.Request.Context(), c.Param("token"), c.GetHeader(GuestKeyHeader))
	if err != nil {
		httpx.Error(c, err, "failed to read whether notes are on")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, st)
}
