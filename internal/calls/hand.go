package calls

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/calls/participant"
	"github.com/teamup/teamup/internal/httpx"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

// HandAttribute is the LiveKit participant attribute that holds a raised hand:
// the time it went up, in RFC 3339, or nothing at all when it is down.
//
// A hand is the person's, not the device's: somebody in from a laptop and a
// phone has one hand, written on every device they are in from, so either can
// lower it and it stays up while either is still there. A page counts it once
// (handQueue.ts).
//
// It lives on the media server rather than in the database because that is
// where everybody in the room already reads each other from, and a late
// joiner gets it with everything else about the people there. Only this
// server writes it: a member's token cannot change its own attributes
// (CanUpdateOwnMetadata is never granted), so nobody can raise a hand for
// somebody else, or jump the queue by backdating their own.
const HandAttribute = "hand"

// handTimeLayout is RFC 3339 to the millisecond, so two hands raised in the same
// second still queue in the order they went up.
const handTimeLayout = "2006-01-02T15:04:05.000Z07:00"

// handProofTTL is how long a hand's proof can put it back where it was. The
// browser holding a raised hand asks for a fresh one every minute, so a proof is
// only ever a minute or two old when a lost hand needs it, and an old one cannot
// be kept to jump the queue later in the meeting.
const handProofTTL = 3 * time.Minute

var (
	errNotInCall        = apperrors.New(apperrors.CodeForbidden, "you are not in this call")
	errRaiseOwnHandOnly = apperrors.New(apperrors.CodeForbidden, "you can only raise your own hand")
	errLowerOthers      = apperrors.New(apperrors.CodeForbidden, "only the organizer can lower somebody else's hand")
	errTargetNotInCall  = apperrors.New(apperrors.CodeNotFound, "that person is not in this call")
)

// HandChange is what the endpoint was asked: up or down, and for whom.
type HandChange struct {
	Raised bool
	// Target is the person whose hand it is; Nil means the caller's own.
	Target uuid.UUID
	// Restore puts a hand back up at the time it first went up, after the media
	// server lost it: a room that was started again has no attributes. It is
	// taken only with a recent proof this server signed.
	Restore *HandRestore
	// Refresh only asks for a fresh proof of a hand that is up. It never raises
	// one, so a refresh that crosses a lowering on the way cannot undo it.
	Refresh bool
}

// HandRestore is a hand's original time, and the proof this server gave for it.
type HandRestore struct {
	RaisedAt string `json:"raisedAt"`
	Proof    string `json:"proof"`
}

// HandState is the hand as it now is.
type HandState struct {
	UserID   uuid.UUID `json:"userId"`
	Raised   bool      `json:"raised"`
	RaisedAt string    `json:"raisedAt,omitempty"`
	// Proof lets the owner put the hand back at RaisedAt if it is lost; see
	// HandRestore. Only a raised hand has one.
	Proof string `json:"proof,omitempty"`
	// Restored says a restore kept the hand's original place. A restore that
	// is not, went up as a new hand at the back of the line.
	Restored bool `json:"restored,omitempty"`
}

// handProof signs a hand's time for one person in one room, at a moment.
func (s *Service) handProof(room, identity, raisedAt string, issued time.Time) string {
	if s.cfg.LiveKitAPISecret == "" {
		return ""
	}
	at := strconv.FormatInt(issued.Unix(), 10)
	return at + "." + s.handMAC(room, identity, raisedAt, at)
}

func (s *Service) handMAC(room, identity, raisedAt, issued string) string {
	mac := hmac.New(sha256.New, []byte("teamup-hand|"+s.cfg.LiveKitAPISecret))
	mac.Write([]byte(strings.Join([]string{room, identity, raisedAt, issued}, "|")))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// validHandProof says whether a restore was signed here, for this person and
// room, recently, and for a time that is not in the future.
func (s *Service) validHandProof(room, identity string, r *HandRestore, now time.Time) bool {
	if r == nil || s.cfg.LiveKitAPISecret == "" {
		return false
	}
	issued, mac, ok := strings.Cut(r.Proof, ".")
	if !ok {
		return false
	}
	secs, err := strconv.ParseInt(issued, 10, 64)
	if err != nil {
		return false
	}
	age := now.Sub(time.Unix(secs, 0))
	if age < -time.Minute || age > handProofTTL {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, r.RaisedAt)
	if err != nil || at.After(now) {
		return false
	}
	return hmac.Equal([]byte(mac), []byte(s.handMAC(room, identity, r.RaisedAt, issued)))
}

// SetHand raises or lowers a hand in a call.
//
// The caller has to be in the room now, not merely entitled to it. Anybody may
// raise or lower their own hand. Lowering somebody else's is for whoever runs
// the meeting, its organizer or an administrator, the same people who manage
// its guests; nobody can raise a hand for anybody else.
//
// Raising a hand that is already up keeps the time it first went up, so asking
// twice does not send somebody to the back of the queue. Raising with a valid
// Restore puts a hand the media server lost back at its original time.
func (s *Service) SetHand(ctx context.Context, orgID, callID, actor uuid.UUID, admin bool, change HandChange) (*HandState, error) {
	target := change.Target
	if target == uuid.Nil {
		target = actor
	}
	self := target == actor
	if change.Raised && !self {
		return nil, errRaiseOwnHandOnly
	}

	call, err := s.GetCall(ctx, orgID, callID)
	if errors.Is(err, apperrors.ErrNotFound) {
		return nil, apperrors.New(apperrors.CodeNotFound, "call not found")
	}
	if err != nil {
		return nil, fmt.Errorf("load call: %w", err)
	}
	if !self && !mayManageGuests(call, actor, admin) {
		return nil, errLowerOthers
	}

	media := s.mediaOf()
	conns, err := media.Connections(ctx, call.RoomName)
	if errors.Is(err, errNoMedia) {
		return nil, apperrors.New(apperrors.CodeBadRequest, "calls are not configured on this installation")
	}
	if err != nil {
		return nil, err
	}
	if len(devicesOf(conns, actor)) == 0 {
		return nil, errNotInCall
	}
	theirs := devicesOf(conns, target)
	if len(theirs) == 0 {
		return nil, errTargetNotInCall
	}

	now := s.clock()
	state := &HandState{UserID: target, Raised: change.Raised}
	current := handOf(theirs)
	switch {
	case change.Refresh && current == "":
		state.Raised = false
		return state, nil
	case change.Raised && current != "":
		// Already up: keep its place, with a fresh proof of it. A device that
		// lost it, as one whose connection came back does, gets it again.
		if err := setHandOn(ctx, media, call.RoomName, theirs, current); err != nil {
			return nil, err
		}
		state.RaisedAt = current
		state.Restored = change.Restore != nil && change.Restore.RaisedAt == current
		state.Proof = s.handProof(call.RoomName, target.String(), current, now)
		return state, nil
	case !change.Raised && current == "":
		// Already down. Nothing to tell anybody.
		return state, nil
	case change.Raised && s.validHandProof(call.RoomName, target.String(), change.Restore, now):
		state.RaisedAt = change.Restore.RaisedAt
		state.Restored = true
	case change.Raised:
		state.RaisedAt = now.UTC().Format(handTimeLayout)
	}
	// An empty value is how LiveKit is told to remove an attribute.
	if err := setHandOn(ctx, media, call.RoomName, theirs, state.RaisedAt); err != nil {
		return nil, err
	}
	if state.Raised {
		state.Proof = s.handProof(call.RoomName, target.String(), state.RaisedAt, now)
	}
	return state, nil
}

// devicesOf is a member's connections to a room, from any device.
func devicesOf(conns []Connection, userID uuid.UUID) []Connection {
	var out []Connection
	for _, c := range conns {
		if participant.IsMember(c.Identity, userID) {
			out = append(out, c)
		}
	}
	return out
}

// handOf is when a person's hand went up, as any of their devices has it: the
// earliest, should two disagree.
func handOf(devices []Connection) string {
	earliest, at := "", time.Time{}
	for _, d := range devices {
		v := d.Attributes[HandAttribute]
		if v == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			continue
		}
		if earliest == "" || t.Before(at) {
			earliest, at = v, t
		}
	}
	return earliest
}

// setHandOn writes a hand (or, with no time, its coming down) on each of a
// person's devices that does not have it so already.
func setHandOn(ctx context.Context, media mediaServer, room string, devices []Connection, raisedAt string) error {
	for _, d := range devices {
		if d.Attributes[HandAttribute] == raisedAt {
			continue
		}
		if err := media.SetAttributes(ctx, room, d.Identity, map[string]string{HandAttribute: raisedAt}); err != nil {
			return err
		}
	}
	return nil
}

// shareHand gives a device that has just joined the hand its person already has
// up on another device. Best effort: the hand is still up on the other one.
func (s *Service) shareHand(ctx context.Context, room string, userID uuid.UUID, identity string) {
	media := s.mediaOf()
	conns, err := media.Connections(ctx, room)
	if err != nil {
		return
	}
	devices := devicesOf(conns, userID)
	raised := handOf(devices)
	if raised == "" {
		return
	}
	for _, d := range devices {
		if d.Identity == identity && d.Attributes[HandAttribute] == "" {
			if err := media.SetAttributes(ctx, room, identity, map[string]string{HandAttribute: raised}); err != nil {
				s.logger.Warn().Err(err).Msg("could not give a device that joined the hand its person has up")
			}
		}
	}
}

// putHand is PUT /calls/:id/hand with {"raised": bool, "userId"?: string,
// "restore"?: {"raisedAt": string, "proof": string}, "refresh"?: bool}.
func (h *Handler) putHand(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req struct {
		Raised  *bool        `json:"raised"`
		UserID  string       `json:"userId"`
		Restore *HandRestore `json:"restore"`
		Refresh bool         `json:"refresh"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Raised == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "say whether the hand is raised"})
		return
	}
	change := HandChange{Raised: *req.Raised || req.Refresh, Restore: req.Restore, Refresh: req.Refresh}
	if req.UserID != "" {
		target, err := uuid.Parse(req.UserID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid userId"})
			return
		}
		change.Target = target
	}
	state, err := h.service.SetHand(c.Request.Context(), orgID, id, userID, isAdmin(c), change)
	if err != nil {
		httpx.Error(c, err, "failed to change the hand")
		return
	}
	c.JSON(http.StatusOK, state)
}
