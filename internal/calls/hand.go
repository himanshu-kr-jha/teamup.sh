package calls

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/httpx"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

// HandAttribute is the LiveKit participant attribute that holds a raised hand:
// the time it went up, in RFC 3339, or nothing at all when it is down.
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
}

// HandState is the hand as it now is.
type HandState struct {
	UserID   uuid.UUID `json:"userId"`
	Raised   bool      `json:"raised"`
	RaisedAt string    `json:"raisedAt,omitempty"`
}

// SetHand raises or lowers a hand in a call.
//
// The caller has to be in the room now, not merely entitled to it. Anybody may
// raise or lower their own hand. Lowering somebody else's is for whoever runs
// the meeting, its organizer or an administrator, the same people who manage
// its guests; nobody can raise a hand for anybody else.
//
// Raising a hand that is already up keeps the time it first went up, so asking
// twice does not send somebody to the back of the queue.
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
	if err != nil {
		return nil, apperrors.New(apperrors.CodeNotFound, "call not found")
	}
	if !self && !mayManageGuests(call, actor, admin) {
		return nil, errLowerOthers
	}

	media := s.mediaOf()
	mine, present, err := media.Attributes(ctx, call.RoomName, actor.String())
	if errors.Is(err, errNoMedia) {
		return nil, apperrors.New(apperrors.CodeBadRequest, "calls are not configured on this installation")
	}
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, errNotInCall
	}
	theirs := mine
	if !self {
		theirs, present, err = media.Attributes(ctx, call.RoomName, target.String())
		if err != nil {
			return nil, err
		}
		if !present {
			return nil, errTargetNotInCall
		}
	}

	state := &HandState{UserID: target, Raised: change.Raised}
	current := theirs[HandAttribute]
	switch {
	case change.Raised && current != "":
		// Already up: keep its place.
		state.RaisedAt = current
		return state, nil
	case !change.Raised && current == "":
		// Already down. Nothing to tell anybody.
		return state, nil
	case change.Raised:
		state.RaisedAt = s.clock().UTC().Format(handTimeLayout)
	}
	// An empty value is how LiveKit is told to remove an attribute.
	if err := media.SetAttributes(ctx, call.RoomName, target.String(), map[string]string{HandAttribute: state.RaisedAt}); err != nil {
		return nil, err
	}
	return state, nil
}

// putHand is PUT /calls/:id/hand with {"raised": bool, "userId"?: string}.
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
		Raised *bool  `json:"raised"`
		UserID string `json:"userId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Raised == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "say whether the hand is raised"})
		return
	}
	change := HandChange{Raised: *req.Raised}
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
