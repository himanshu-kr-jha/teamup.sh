package chat

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/auth"
	"github.com/teamup/teamup/internal/httpx"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func RegisterRoutes(rg *gin.RouterGroup, service *Service) {
	h := NewHandler(service)

	rg.GET("/channels", h.ListChannels)
	rg.POST("/channels", h.CreateChannel)
	rg.POST("/channels/direct", h.OpenDirectMessage)
	rg.GET("/channels/browse", h.BrowseChannels)
	rg.POST("/channels/:id/join", h.JoinChannel)
	rg.GET("/channels/:id", h.GetChannel)
	rg.PATCH("/channels/:id", h.UpdateChannel)
	rg.DELETE("/channels/:id", h.DeleteChannel)
	rg.GET("/channels/:id/members", h.ListMembers)
	rg.POST("/channels/:id/members", h.AddMember)
	rg.DELETE("/channels/:id/members/:userId", h.RemoveMember)
	rg.GET("/channels/:id/messages", h.ListMessages)
	rg.POST("/channels/:id/messages", h.SendMessage)
	rg.PATCH("/messages/:id", h.EditMessage)
	rg.DELETE("/messages/:id", h.DeleteMessage)
	rg.POST("/messages/:id/forward", h.ForwardMessage)
	rg.GET("/messages/:id/reads", h.MessageReads)
	rg.GET("/messages/:id/thread", h.GetThread)
	rg.POST("/messages/:id/thread/read", h.MarkThreadRead)
	rg.POST("/messages/:id/reactions", h.AddReaction)
	rg.DELETE("/messages/:id/reactions/:emoji", h.RemoveReaction)
	rg.POST("/channels/:id/read", h.MarkRead)
	rg.PUT("/channels/:id/notifications", h.SetNotifyLevel)
	rg.GET("/search/messages", h.SearchMessages)
	rg.GET("/search/files", h.SearchFiles)
}

// SearchMessages backs the header search box, which had no endpoint at all.
func (h *Handler) SearchMessages(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}

	q := SearchQuery{Text: c.Query("q")}
	if raw := c.Query("channelId"); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			q.ChannelID = &id
		}
	}
	if raw := c.Query("userId"); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			q.UserID = &id
		}
	}
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			q.Limit = n
		}
	}
	if raw := c.Query("offset"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			q.Offset = n
		}
	}

	results, err := h.service.SearchMessages(c.Request.Context(), cl.OrgID, cl.UserID, q)
	if err != nil {
		httpx.Error(c, err, "search failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"results": results})
}

// SearchFiles finds shared files by name, from the same box as messages.
func (h *Handler) SearchFiles(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}

	q := SearchQuery{Text: c.Query("q")}
	if raw := c.Query("channelId"); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			q.ChannelID = &id
		}
	}
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			q.Limit = n
		}
	}
	if raw := c.Query("offset"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			q.Offset = n
		}
	}

	results, err := h.service.SearchFiles(c.Request.Context(), cl.OrgID, cl.UserID, q)
	if err != nil {
		httpx.Error(c, err, "search failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"results": results})
}

type caller struct {
	OrgID  uuid.UUID
	UserID uuid.UUID
}

func callerFrom(c *gin.Context) (caller, bool) {
	u := auth.GetUserFromContext(c)
	orgID, err := uuid.Parse(u.OrgID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return caller{}, false
	}
	userID, err := uuid.Parse(u.UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return caller{}, false
	}
	return caller{OrgID: orgID, UserID: userID}, true
}

func pathUUID(c *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid " + param})
		return uuid.Nil, false
	}
	return id, true
}

// requireChannelAdmin gates the operations that affect everyone in a channel —
// renaming, archiving, and managing membership. Plain membership is not
// enough: any member could otherwise archive a channel or remove its owner.
func (h *Handler) requireChannelAdmin(c *gin.Context, cl caller, channelID uuid.UUID) bool {
	if _, err := h.service.GetChannel(c.Request.Context(), cl.OrgID, channelID); err != nil {
		httpx.Error(c, apperrors.ErrNotFound, "channel not found")
		return false
	}
	if h.service.IsChannelAdmin(c.Request.Context(), channelID, cl.UserID) {
		return true
	}
	// Organization admins can always administer a channel.
	if auth.GetUserFromContext(c).Can(auth.PermChannelsAll) {
		return true
	}
	httpx.Error(c, apperrors.New(apperrors.CodeForbidden,
		"you need to be a channel administrator"), "access denied")
	return false
}

// requireMembership keeps channel-scoped reads and writes to members.
//
// Public and announcement channels belong to the whole organization, so
// touching one joins it. Without that, everybody but the creator saw the
// channel and was refused on every write.
func (h *Handler) requireMembership(c *gin.Context, cl caller, channelID uuid.UUID, writing bool) bool {
	ctx := c.Request.Context()
	ch, err := h.service.GetChannel(ctx, cl.OrgID, channelID)
	if err != nil {
		httpx.Error(c, apperrors.ErrNotFound, "channel not found")
		return false
	}

	if isOpenToOrg(ch.Type) {
		if _, err := h.service.EnsureMember(ctx, cl.OrgID, channelID, cl.UserID); err != nil {
			httpx.Error(c, err, "failed to join channel")
			return false
		}
		// Announcements are broadcast, not conversation: everyone reads,
		// only the people running the channel post.
		if writing && ch.Type == "announcement" && !h.mayAdminister(c, cl, channelID) {
			httpx.Error(c, apperrors.New(apperrors.CodeForbidden,
				"only administrators can post in an announcement channel"), "access denied")
			return false
		}
		return true
	}

	if h.service.isMember(ctx, channelID, cl.UserID) {
		return true
	}
	httpx.Error(c, apperrors.New(apperrors.CodeForbidden, "you are not a member of this channel"), "access denied")
	return false
}

// mayAdminister answers the same question as requireChannelAdmin without
// writing a response, for callers that want to branch on it.
func (h *Handler) mayAdminister(c *gin.Context, cl caller, channelID uuid.UUID) bool {
	if h.service.IsChannelAdmin(c.Request.Context(), channelID, cl.UserID) {
		return true
	}
	return auth.GetUserFromContext(c).Can(auth.PermChannelsAll)
}

func (h *Handler) ListChannels(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	channels, err := h.service.ListChannels(c.Request.Context(), cl.OrgID, cl.UserID)
	if err != nil {
		httpx.Error(c, err, "failed to list channels")
		return
	}
	c.JSON(http.StatusOK, gin.H{"channels": channels})
}

func (h *Handler) CreateChannel(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Type        string  `json:"type" binding:"required,oneof=dm group public announcement"`
		Description *string `json:"description"`
		TeamID      *string `json:"teamId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var teamID *uuid.UUID
	if req.TeamID != nil && *req.TeamID != "" {
		id, err := uuid.Parse(*req.TeamID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid teamId"})
			return
		}
		teamID = &id
	}

	channel, err := h.service.CreateChannel(c.Request.Context(), cl.OrgID, cl.UserID,
		req.Name, req.Type, req.Description, teamID)
	if err != nil {
		httpx.Error(c, err, "failed to create channel")
		return
	}
	c.JSON(http.StatusCreated, channel)
}

// OpenDirectMessage starts, or reopens, the one-to-one conversation with
// somebody. Idempotent: asking twice returns the same conversation.
func (h *Handler) OpenDirectMessage(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	var req struct {
		UserID string `json:"userId" binding:"required,uuid"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	otherID, err := uuid.Parse(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid userId"})
		return
	}

	channel, err := h.service.OpenDirectMessage(c.Request.Context(), cl.OrgID, cl.UserID, otherID)
	if err != nil {
		httpx.Error(c, err, "failed to open that conversation")
		return
	}
	c.JSON(http.StatusOK, channel)
}

// BrowseChannels lists the org-wide channels the caller is not in, which is
// how somebody finds a channel they left.
func (h *Handler) BrowseChannels(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	channels, err := h.service.BrowsableChannels(c.Request.Context(), cl.OrgID, cl.UserID)
	if err != nil {
		httpx.Error(c, err, "failed to list channels")
		return
	}
	c.JSON(http.StatusOK, gin.H{"channels": channels})
}

// JoinChannel puts the caller back into an org-wide channel they had left.
func (h *Handler) JoinChannel(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	channelID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	if err := h.service.RejoinChannel(c.Request.Context(), cl.OrgID, channelID, cl.UserID); err != nil {
		httpx.Error(c, err, "failed to join channel")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "joined"})
}

func (h *Handler) GetChannel(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	channelID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	if !h.requireMembership(c, cl, channelID, false) {
		return
	}
	channel, err := h.service.GetChannel(c.Request.Context(), cl.OrgID, channelID)
	if err != nil {
		httpx.Error(c, err, "channel not found")
		return
	}
	c.JSON(http.StatusOK, channel)
}

func (h *Handler) UpdateChannel(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	channelID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	if !h.requireChannelAdmin(c, cl, channelID) {
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		IsPrivate   *bool   `json:"isPrivate"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	channel, err := h.service.UpdateChannel(c.Request.Context(), cl.OrgID, channelID,
		req.Name, req.Description, req.IsPrivate)
	if err != nil {
		httpx.Error(c, err, "failed to update channel")
		return
	}
	c.JSON(http.StatusOK, channel)
}

func (h *Handler) DeleteChannel(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	channelID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	if !h.requireChannelAdmin(c, cl, channelID) {
		return
	}
	if err := h.service.ArchiveChannel(c.Request.Context(), cl.OrgID, channelID); err != nil {
		httpx.Error(c, err, "failed to archive channel")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "archived"})
}

// ListMembers backs the channel settings panel. Readable by anyone who can
// read the channel; joining an org-wide channel happens on the way through.
func (h *Handler) ListMembers(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	channelID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	if !h.requireMembership(c, cl, channelID, false) {
		return
	}
	members, err := h.service.ListMembers(c.Request.Context(), cl.OrgID, channelID)
	if err != nil {
		httpx.Error(c, err, "failed to list members")
		return
	}
	c.JSON(http.StatusOK, gin.H{"members": members})
}

func (h *Handler) AddMember(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	channelID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	if !h.requireChannelAdmin(c, cl, channelID) {
		return
	}
	var req struct {
		UserID string `json:"userId" binding:"required,uuid"`
		Role   string `json:"role" binding:"omitempty,oneof=owner admin member"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid userId"})
		return
	}
	if err := h.service.AddMember(c.Request.Context(), cl.OrgID, channelID, userID, req.Role); err != nil {
		httpx.Error(c, err, "failed to add member")
		return
	}
	// Being put in a channel is not something you would otherwise notice.
	h.service.NotifyAddedToChannel(c.Request.Context(), cl.OrgID, channelID, userID, cl.UserID)
	c.JSON(http.StatusOK, gin.H{"message": "member added"})
}

func (h *Handler) RemoveMember(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	channelID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	userID, ok := pathUUID(c, "userId")
	if !ok {
		return
	}
	// Members may always remove themselves; removing anyone else is a channel
	// administration action.
	if userID != cl.UserID && !h.requireChannelAdmin(c, cl, channelID) {
		return
	}
	if err := h.service.RemoveMember(c.Request.Context(), cl.OrgID, channelID, userID); err != nil {
		httpx.Error(c, err, "failed to remove member")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "member removed"})
}

func (h *Handler) ListMessages(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	channelID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	if !h.requireMembership(c, cl, channelID, false) {
		return
	}

	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}
	var before *time.Time
	if b := c.Query("before"); b != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, b); err == nil {
			before = &parsed
		}
	}
	// The id half of the cursor. Optional: a client that sends only `before`
	// still pages, it just cannot separate messages sharing a timestamp.
	var beforeID *uuid.UUID
	if b := c.Query("beforeId"); b != "" {
		if parsed, err := uuid.Parse(b); err == nil {
			beforeID = &parsed
		}
	}

	// A day at a time, as the reader counts days: what the chat page asks for.
	if c.Query("unit") == "day" {
		loc, err := time.LoadLocation(c.Query("tz"))
		if err != nil || c.Query("tz") == "" {
			loc = time.UTC
		}
		messages, more, err := h.service.GetMessagesForDay(c.Request.Context(), channelID, before, beforeID, loc)
		if err != nil {
			httpx.Error(c, err, "failed to get messages")
			return
		}
		c.JSON(http.StatusOK, gin.H{"messages": messages, "hasMore": more})
		return
	}

	messages, err := h.service.GetMessages(c.Request.Context(), channelID, limit, before, beforeID)
	if err != nil {
		httpx.Error(c, err, "failed to get messages")
		return
	}
	c.JSON(http.StatusOK, gin.H{"messages": messages})
}

func (h *Handler) SendMessage(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	channelID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	var req struct {
		// Not required: an attachment on its own is a message.
		Content       string   `json:"content"`
		ReplyTo       *string  `json:"replyTo"`
		AttachmentIDs []string `json:"attachmentIds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var replyTo *uuid.UUID
	if req.ReplyTo != nil && *req.ReplyTo != "" {
		id, err := uuid.Parse(*req.ReplyTo)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid replyTo"})
			return
		}
		replyTo = &id
	}

	attachmentIDs := make([]uuid.UUID, 0, len(req.AttachmentIDs))
	for _, raw := range req.AttachmentIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid attachmentIds"})
			return
		}
		attachmentIDs = append(attachmentIDs, id)
	}

	isOrgAdmin := auth.GetUserFromContext(c).Can(auth.PermChannelsAll)
	msg, err := h.service.SendMessage(c.Request.Context(), cl.OrgID, channelID, cl.UserID,
		req.Content, replyTo, attachmentIDs, isOrgAdmin)
	if err != nil {
		httpx.Error(c, err, "failed to send message")
		return
	}
	c.JSON(http.StatusCreated, msg)
}

func (h *Handler) EditMessage(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	messageID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	var req struct {
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.service.EditMessage(c.Request.Context(), messageID, cl.UserID, req.Content); err != nil {
		httpx.Error(c, err, "failed to edit message")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "updated"})
}

func (h *Handler) DeleteMessage(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	messageID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	if err := h.service.DeleteMessage(c.Request.Context(), messageID, cl.UserID); err != nil {
		httpx.Error(c, err, "failed to delete message")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// MessageReads answers who has read a message. Anybody in the channel can ask;
// it is the same information they could work out by watching the read markers.
func (h *Handler) MessageReads(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	messageID, ok := pathUUID(c, "id")
	if !ok {
		return
	}

	channelID, err := h.service.messageChannel(c.Request.Context(), messageID)
	if err != nil {
		httpx.Error(c, err, "message not found")
		return
	}
	if !h.requireMembership(c, cl, channelID, false) {
		return
	}

	receipts, err := h.service.ReadBy(c.Request.Context(), cl.OrgID, messageID)
	if err != nil {
		httpx.Error(c, err, "failed to list who has read this")
		return
	}
	c.JSON(http.StatusOK, gin.H{"reads": receipts})
}

// GetThread reads one thread — a message and the replies to it — identified by
// any message in it.
//
// Gated on exactly the same question as reading the channel it is in. A route
// that turns a message id into messages is a second way in, and without this
// it would be a way to read a private channel one id at a time. The channel is
// resolved and the caller checked before any message body is loaded.
func (h *Handler) GetThread(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	messageID, ok := pathUUID(c, "id")
	if !ok {
		return
	}

	_, channelID, err := h.service.ThreadOf(c.Request.Context(), cl.OrgID, messageID)
	if err != nil {
		httpx.Error(c, err, "message not found")
		return
	}
	if !h.requireMembership(c, cl, channelID, false) {
		return
	}

	thread, err := h.service.GetThread(c.Request.Context(), cl.OrgID, messageID)
	if err != nil {
		httpx.Error(c, err, "failed to read that thread")
		return
	}
	c.JSON(http.StatusOK, thread)
}

func (h *Handler) AddReaction(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	messageID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	var req struct {
		Emoji string `json:"emoji" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.service.AddReaction(c.Request.Context(), messageID, cl.UserID, req.Emoji); err != nil {
		httpx.Error(c, err, "failed to add reaction")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "reaction added"})
}

func (h *Handler) RemoveReaction(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	messageID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	if err := h.service.RemoveReaction(c.Request.Context(), messageID, cl.UserID, c.Param("emoji")); err != nil {
		httpx.Error(c, err, "failed to remove reaction")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "reaction removed"})
}

func (h *Handler) MarkRead(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	channelID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	var req struct {
		MessageID string `json:"messageId" binding:"required,uuid"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	messageID, err := uuid.Parse(req.MessageID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid messageId"})
		return
	}
	if err := h.service.MarkRead(c.Request.Context(), channelID, cl.UserID, messageID); err != nil {
		httpx.Error(c, err, "failed to mark read")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "read"})
}

// MarkThreadRead records that the caller has read a thread as far as the
// message named, which is the newest reply they have seen.
func (h *Handler) MarkThreadRead(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	messageID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	_, channelID, err := h.service.ThreadOf(c.Request.Context(), cl.OrgID, messageID)
	if err != nil {
		httpx.Error(c, err, "message not found")
		return
	}
	if !h.requireMembership(c, cl, channelID, false) {
		return
	}
	if err := h.service.MarkThreadRead(c.Request.Context(), cl.OrgID, cl.UserID, messageID); err != nil {
		httpx.Error(c, err, "failed to mark read")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "read"})
}

// SetNotifyLevel is the bell in a conversation's header: what the caller hears
// about from it.
func (h *Handler) SetNotifyLevel(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	channelID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	var req struct {
		Level string `json:"level" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.service.SetNotifyLevel(c.Request.Context(), channelID, cl.UserID, req.Level); err != nil {
		httpx.Error(c, err, "failed to change notifications")
		return
	}
	c.JSON(http.StatusOK, gin.H{"notifyLevel": req.Level})
}

// ForwardMessage passes a message on to other conversations.
func (h *Handler) ForwardMessage(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	messageID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	var req struct {
		ChannelIDs []string `json:"channelIds" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "choose where to forward it"})
		return
	}
	targets := make([]uuid.UUID, 0, len(req.ChannelIDs))
	for _, raw := range req.ChannelIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid channelIds"})
			return
		}
		targets = append(targets, id)
	}
	isOrgAdmin := auth.GetUserFromContext(c).Can(auth.PermChannelsAll)
	sent, err := h.service.ForwardMessage(c.Request.Context(), cl.OrgID, messageID, cl.UserID, targets, isOrgAdmin)
	if err != nil {
		httpx.Error(c, err, "failed to forward the message")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"messages": sent})
}
