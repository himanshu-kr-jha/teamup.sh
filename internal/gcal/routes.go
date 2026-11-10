package gcal

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/auth"
	"github.com/teamup/teamup/internal/httpx"
)

type handler struct{ s *Service }

// RegisterRoutes adds the routes for connecting a Google Calendar. The way
// back from Google is public: the session cookie is SameSite=Strict, so a
// browser arriving from google.com does not send it. That route proves who it
// is for with the signed state and the nonce cookie instead.
func RegisterRoutes(protected, public *gin.RouterGroup, s *Service) {
	h := &handler{s: s}
	protected.GET("/calendar/google", h.status)
	protected.POST("/calendar/google/connect", h.connect)
	protected.POST("/calendar/google/sync", h.sync)
	protected.DELETE("/calendar/google", h.disconnect)
	public.GET("/calendar/google/callback", h.callback)
}

func caller(c *gin.Context) (orgID, userID uuid.UUID, ok bool) {
	user := auth.GetUserFromContext(c)
	orgID, orgErr := uuid.Parse(user.OrgID)
	userID, userErr := uuid.Parse(user.UserID)
	if orgErr != nil || userErr != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return uuid.Nil, uuid.Nil, false
	}
	return orgID, userID, true
}

func (h *handler) status(c *gin.Context) {
	_, userID, ok := caller(c)
	if !ok {
		return
	}
	st, err := h.s.Status(c.Request.Context(), userID)
	if err != nil {
		httpx.Error(c, err, "failed to read google calendar")
		return
	}
	c.JSON(http.StatusOK, st)
}

func (h *handler) connect(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	address, nonce, err := h.s.ConnectURL(orgID, userID)
	if err != nil {
		httpx.Error(c, err, "failed to start connecting google calendar")
		return
	}
	// Lax, unlike the session: it has to come back with the browser from google.com.
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     NonceCookie,
		Value:    nonce,
		Path:     CallbackPath,
		MaxAge:   int(stateLife.Seconds()),
		HttpOnly: true,
		Secure:   !h.s.cfg.InsecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	c.JSON(http.StatusOK, gin.H{"url": address})
}

func (h *handler) callback(c *gin.Context) {
	back := func(outcome string) {
		http.SetCookie(c.Writer, &http.Cookie{Name: NonceCookie, Path: CallbackPath, MaxAge: -1, HttpOnly: true,
			Secure: !h.s.cfg.InsecureCookies, SameSite: http.SameSiteLaxMode})
		c.Redirect(http.StatusFound, strings.TrimRight(h.s.cfg.BaseURL, "/")+"/calendar?google="+url.QueryEscape(outcome))
	}
	if c.Query("error") != "" {
		// access_denied: they said no on Google's page.
		back("declined")
		return
	}
	nonce, _ := c.Cookie(NonceCookie)
	if _, err := h.s.Complete(c.Request.Context(), c.Query("state"), nonce, c.Query("code")); err != nil {
		h.s.logger.Warn().Err(err).Msg("connecting google calendar failed")
		back("failed")
		return
	}
	back("connected")
}

func (h *handler) sync(c *gin.Context) {
	_, userID, ok := caller(c)
	if !ok {
		return
	}
	st, err := h.s.Status(c.Request.Context(), userID)
	if err != nil {
		httpx.Error(c, err, "failed to read google calendar")
		return
	}
	if !st.Connected {
		c.JSON(http.StatusConflict, gin.H{"error": "connect a Google Calendar first"})
		return
	}
	// A person waiting on the button is told how it went, not left to wonder.
	_ = h.s.Sync(c.Request.Context(), userID)
	st, err = h.s.Status(c.Request.Context(), userID)
	if err != nil {
		httpx.Error(c, err, "failed to read google calendar")
		return
	}
	c.JSON(http.StatusOK, st)
}

func (h *handler) disconnect(c *gin.Context) {
	_, userID, ok := caller(c)
	if !ok {
		return
	}
	if err := h.s.Disconnect(c.Request.Context(), userID); err != nil {
		httpx.Error(c, err, "failed to disconnect google calendar")
		return
	}
	c.Status(http.StatusNoContent)
}
