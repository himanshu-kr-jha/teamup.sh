package calls

import (
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/teamup/teamup/internal/httpx"
)

// registerGuestRoutes is what members of a meeting use to let guests in.
func registerGuestRoutes(rg *gin.RouterGroup, h *Handler) {
	rg.GET("/calls/:id/guest-link", h.getGuestLink)
	rg.PUT("/calls/:id/guest-link", h.putGuestLink)
	rg.POST("/calls/:id/guests/:guestId/remove", h.removeGuest)
	rg.POST("/calls/:id/guest-invites", h.inviteGuests)
	rg.DELETE("/calls/:id/guest-invites", h.removeGuestInvite)
}

func (h *Handler) inviteGuests(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req struct {
		Emails   []string `json:"emails"`
		TimeZone string   `json:"timeZone"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	link, err := h.service.InviteGuests(c.Request.Context(), orgID, id, userID, isAdmin(c), req.Emails, req.TimeZone)
	if err != nil {
		httpx.Error(c, err, "failed to invite the guests")
		return
	}
	c.JSON(http.StatusOK, link)
}

// removeGuestInvite takes ?email= off the invitations.
func (h *Handler) removeGuestInvite(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	link, err := h.service.RemoveGuestInvite(c.Request.Context(), orgID, id, userID, isAdmin(c), c.Query("email"))
	if err != nil {
		httpx.Error(c, err, "failed to remove the invitation")
		return
	}
	c.JSON(http.StatusOK, link)
}

// RegisterGuestRoutes is what a guest uses, with no account: outside the
// signed-in group, where the link's token is the only credential.
func RegisterGuestRoutes(rg *gin.RouterGroup, service *Service) {
	h := NewHandler(service)
	rg.GET("/guest/:token", h.guestMeeting)
	rg.POST("/guest/:token/join", h.joinAsGuest)
}

func (h *Handler) getGuestLink(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	link, err := h.service.GuestLinkFor(c.Request.Context(), orgID, id, userID, isAdmin(c))
	if err != nil {
		httpx.Error(c, err, "failed to read the guest link")
		return
	}
	c.JSON(http.StatusOK, link)
}

func (h *Handler) putGuestLink(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var change GuestLinkChange
	if err := c.ShouldBindJSON(&change); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	link, err := h.service.SetGuestLink(c.Request.Context(), orgID, id, userID, isAdmin(c), change)
	if err != nil {
		httpx.Error(c, err, "failed to change the guest link")
		return
	}
	c.JSON(http.StatusOK, link)
}

func (h *Handler) removeGuest(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	guestID, ok := pathID(c, "guestId")
	if !ok {
		return
	}
	if err := h.service.RemoveGuest(c.Request.Context(), orgID, id, userID, isAdmin(c), guestID); err != nil {
		httpx.Error(c, err, "failed to remove the guest")
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) guestMeeting(c *gin.Context) {
	m, err := h.service.GuestMeeting(c.Request.Context(), c.Param("token"))
	if err != nil {
		httpx.Error(c, err, "failed to read the meeting")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, m)
}

func (h *Handler) joinAsGuest(c *gin.Context) {
	var req struct {
		Name      string `json:"name"`
		Passcode  string `json:"passcode"`
		RejoinKey string `json:"rejoinKey"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	join, err := h.service.JoinAsGuest(c.Request.Context(), c.Param("token"), req.Name, req.Passcode, req.RejoinKey, clientAddress(c.Request))
	if err != nil {
		httpx.Error(c, err, "failed to join the meeting")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, join)
}

// clientAddress is who is asking, for counting wrong passcodes. Behind the
// bundled nginx it is X-Real-IP, which nginx sets and a visitor cannot; a
// request straight from outside cannot choose it, because a header is only
// believed from a private or loopback peer. X-Forwarded-For is never read: its
// first entry is whatever the visitor sent.
func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" && peer != nil && (peer.IsLoopback() || peer.IsPrivate()) {
		if ip := net.ParseIP(real); ip != nil {
			return ip.String()
		}
	}
	return host
}
