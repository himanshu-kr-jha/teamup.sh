package integrations

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/auth"
	"github.com/teamup/teamup/internal/httpx"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

func decodeJSON(res *http.Response, out any) error {
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
}

const (
	manifestCookie  = "gh_manifest"
	identityCookie  = "gh_identity"
	bitbucketCookie = "bb_state"
	githubPath      = "/api/integrations/github"
	bitbucketPath   = "/api/integrations/bitbucket"
)

type handler struct{ s *Service }

// RegisterRoutes adds the routes. GitHub's own calls, and the browser coming
// back from github.com without the SameSite=Strict session, are public; each
// proves itself with a signature or a signed state and a nonce cookie.
func RegisterRoutes(protected, public *gin.RouterGroup, s *Service) {
	h := &handler{s: s}
	protected.GET("/integrations", h.admin(h.connections))
	protected.POST("/integrations/github/manifest", h.admin(h.manifest))
	protected.POST("/integrations/:provider/sync", h.admin(h.sync))
	protected.POST("/integrations/bitbucket/connect", h.admin(h.bitbucketConnect))
	protected.POST("/integrations/bitbucket/identity", h.bitbucketIdentity)
	protected.DELETE("/integrations/:provider", h.admin(h.disconnect))
	protected.GET("/integrations/repositories", h.repositories)
	protected.PUT("/projects/:id/repositories", h.admin(h.setProjectRepositories))
	protected.GET("/integrations/me", h.identities)
	protected.POST("/integrations/github/identity", h.identityStart)
	protected.DELETE("/integrations/me/:provider", h.unlink)
	protected.GET("/tasks/:id/code", h.taskCode)
	protected.GET("/projects/:id/access", h.projectAccess)
	protected.POST("/projects/:id/access/requests", h.requestAccess)
	protected.POST("/projects/:id/access/grants", h.admin(h.grantAccess))
	protected.DELETE("/access/requests/:id", h.cancelRequest)
	protected.POST("/access/requests/:id/approve", h.admin(h.decide(true)))
	protected.POST("/access/requests/:id/decline", h.admin(h.decide(false)))
	protected.DELETE("/access/grants/:id", h.admin(h.revoke))
	protected.GET("/access/requests", h.admin(h.pendingRequests))
	protected.GET("/access/log", h.admin(h.accessLog))

	public.GET("/integrations/github/created", h.created)
	public.GET("/integrations/github/installed/:hookKey", h.installed)
	public.GET("/integrations/github/identity/callback", h.identityDone)
	public.POST("/integrations/github/hook/:hookKey", h.hook)
	public.GET("/integrations/bitbucket/callback", h.bitbucketDone)
	public.POST("/integrations/bitbucket/hook/:hookKey", h.bitbucketHook)
}

func caller(c *gin.Context) (orgID, userID uuid.UUID, ok bool) {
	u := auth.GetUserFromContext(c)
	orgID, e1 := uuid.Parse(u.OrgID)
	userID, e2 := uuid.Parse(u.UserID)
	if e1 != nil || e2 != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return uuid.Nil, uuid.Nil, false
	}
	return orgID, userID, true
}

// admin lets through only somebody who manages the organization's settings.
func (h *handler) admin(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !auth.GetUserFromContext(c).Can(auth.PermOrgSettings) {
			c.JSON(http.StatusForbidden, gin.H{"error": "only an administrator can manage integrations and projects"})
			return
		}
		next(c)
	}
}

func (h *handler) setCookie(c *gin.Context, name, value string, life int) {
	path := githubPath
	if name == bitbucketCookie {
		path = bitbucketPath
	}
	http.SetCookie(c.Writer, &http.Cookie{Name: name, Value: value, Path: path, MaxAge: life,
		HttpOnly: true, Secure: !h.s.cfg.InsecureCookies, SameSite: http.SameSiteLaxMode})
}

func (h *handler) back(c *gin.Context, page, param, outcome string) {
	c.Redirect(http.StatusFound, h.s.base()+page+"?"+param+"="+url.QueryEscape(outcome))
}

func (h *handler) connections(c *gin.Context) {
	orgID, _, ok := caller(c)
	if !ok {
		return
	}
	list, err := h.s.Connections(c.Request.Context(), orgID)
	if err != nil {
		httpx.Error(c, err, "failed to read integrations")
		return
	}
	c.JSON(http.StatusOK, gin.H{"connections": list})
}

func (h *handler) manifest(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	var req struct {
		Organization string `json:"organization"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	action, manifest, keep, err := h.s.GitHubManifest(c.Request.Context(), orgID, userID, req.Organization)
	if err != nil {
		httpx.Error(c, err, "failed to start connecting github")
		return
	}
	h.setCookie(c, manifestCookie, keep, 3600)
	c.JSON(http.StatusOK, gin.H{"action": action, "manifest": manifest})
}

func (h *handler) created(c *gin.Context) {
	kept, _ := c.Cookie(manifestCookie)
	h.setCookie(c, manifestCookie, "", -1)
	next, err := h.s.GitHubCreated(c.Request.Context(), c.Query("state"), kept, c.Query("code"))
	if err != nil {
		h.s.logger.Warn().Err(err).Bool("state_returned", c.Query("state") != "").Bool("cookie", kept != "").Msg("making the github app failed")
		// GitHub has made the app by now, so its name is taken until it is deleted there.
		h.back(c, "/organization", "github", "not-kept")
		return
	}
	c.Redirect(http.StatusFound, next)
}

func (h *handler) installed(c *gin.Context) {
	if c.Query("installation_id") == "" {
		h.back(c, "/organization", "github", "failed")
		return
	}
	if err := h.s.GitHubInstalled(c.Request.Context(), c.Param("hookKey"), c.Query("installation_id")); err != nil {
		h.s.logger.Warn().Err(err).Msg("recording the github installation failed")
		h.back(c, "/organization", "github", "failed")
		return
	}
	h.back(c, "/organization", "github", "connected")
}

func (h *handler) sync(c *gin.Context) {
	orgID, _, ok := caller(c)
	if !ok {
		return
	}
	if err := h.s.SyncNow(c.Request.Context(), orgID, c.Param("provider")); err != nil {
		httpx.Error(c, err, "failed to read repositories from github")
		return
	}
	h.connections(c)
}

func (h *handler) disconnect(c *gin.Context) {
	orgID, _, ok := caller(c)
	if !ok {
		return
	}
	if err := h.s.Disconnect(c.Request.Context(), orgID, c.Param("provider")); err != nil {
		httpx.Error(c, err, "failed to disconnect")
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) repositories(c *gin.Context) {
	orgID, _, ok := caller(c)
	if !ok {
		return
	}
	list, err := h.s.Repositories(c.Request.Context(), orgID)
	if err != nil {
		httpx.Error(c, err, "failed to list repositories")
		return
	}
	c.JSON(http.StatusOK, gin.H{"repositories": list})
}

func (h *handler) setProjectRepositories(c *gin.Context) {
	orgID, _, ok := caller(c)
	if !ok {
		return
	}
	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid project id"})
		return
	}
	var req struct {
		RepositoryIDs []uuid.UUID `json:"repositoryIds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := h.s.SetProjectRepositories(c.Request.Context(), orgID, projectID, req.RepositoryIDs); err != nil {
		httpx.Error(c, err, "failed to set the project's repositories")
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) identities(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	list, err := h.s.Identities(c.Request.Context(), orgID, userID)
	if err != nil {
		httpx.Error(c, err, "failed to read linked accounts")
		return
	}
	c.JSON(http.StatusOK, gin.H{"accounts": list})
}

func (h *handler) identityStart(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	address, nonce, err := h.s.GitHubIdentityURL(c.Request.Context(), orgID, userID)
	if err != nil {
		httpx.Error(c, err, "failed to start linking github")
		return
	}
	h.setCookie(c, identityCookie, nonce, 900)
	c.JSON(http.StatusOK, gin.H{"url": address})
}

func (h *handler) identityDone(c *gin.Context) {
	nonce, _ := c.Cookie(identityCookie)
	h.setCookie(c, identityCookie, "", -1)
	if c.Query("error") != "" {
		h.back(c, "/settings", "github", "declined")
		return
	}
	if err := h.s.GitHubIdentityDone(c.Request.Context(), c.Query("state"), nonce, c.Query("code")); err != nil {
		h.s.logger.Warn().Err(err).Msg("linking a github account failed")
		outcome := "failed"
		if apperrors.IsCode(err, apperrors.CodeConflict) {
			outcome = "taken"
		}
		h.back(c, "/settings", "github", outcome)
		return
	}
	h.back(c, "/settings", "github", "linked")
}

func (h *handler) unlink(c *gin.Context) {
	_, userID, ok := caller(c)
	if !ok {
		return
	}
	if err := h.s.Unlink(c.Request.Context(), userID, c.Param("provider")); err != nil {
		httpx.Error(c, err, "failed to unlink")
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) taskCode(c *gin.Context) {
	orgID, _, ok := caller(c)
	if !ok {
		return
	}
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid task id"})
		return
	}
	code, err := h.s.ForTask(c.Request.Context(), orgID, taskID)
	if err != nil {
		httpx.Error(c, err, "failed to read the task's code")
		return
	}
	c.JSON(http.StatusOK, code)
}

// hook takes an event from GitHub. Answered at once; GitHub retries anything
// that is not a 2xx, and a retry of one already acted on is ignored.
func (h *handler) hook(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 25<<20))
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	err = h.s.GitHubEvent(c.Request.Context(), c.Param("hookKey"), c.GetHeader("X-GitHub-Event"),
		c.GetHeader("X-GitHub-Delivery"), c.GetHeader("X-Hub-Signature-256"), body)
	switch {
	case err == nil:
		c.Status(http.StatusNoContent)
	case errors.Is(err, ErrBadSignature):
		c.Status(http.StatusUnauthorized)
	case errors.Is(err, apperrors.ErrNotFound):
		c.Status(http.StatusNotFound)
	default:
		h.s.logger.Warn().Err(err).Str("event", c.GetHeader("X-GitHub-Event")).Msg("github event failed")
		c.Status(http.StatusInternalServerError)
	}
}

// ── Bitbucket ─────────────────────────────────────────────────────────────

func (h *handler) bitbucketConnect(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	var req struct {
		Workspace string `json:"workspace"`
		Key       string `json:"key"`
		Secret    string `json:"secret"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	address, nonce, err := h.s.BitbucketStart(c.Request.Context(), orgID, userID, req.Workspace, req.Key, req.Secret)
	if err != nil {
		httpx.Error(c, err, "failed to start connecting bitbucket")
		return
	}
	h.setCookie(c, bitbucketCookie, nonce, 900)
	c.JSON(http.StatusOK, gin.H{"url": address})
}

func (h *handler) bitbucketIdentity(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	address, nonce, err := h.s.BitbucketIdentityURL(c.Request.Context(), orgID, userID)
	if err != nil {
		httpx.Error(c, err, "failed to start linking bitbucket")
		return
	}
	h.setCookie(c, bitbucketCookie, nonce, 900)
	c.JSON(http.StatusOK, gin.H{"url": address})
}

// bitbucketDone is the one address the consumer sends everybody back to: the
// owner connecting the workspace, and anybody linking their own account.
func (h *handler) bitbucketDone(c *gin.Context) {
	nonce, _ := c.Cookie(bitbucketCookie)
	h.setCookie(c, bitbucketCookie, "", -1)
	if c.Query("error") != "" {
		// Which of the two it was is in the state, which a refusal still carries.
		var st bitbucketState
		page := "/settings"
		if h.s.box != nil && h.s.box.ReadToken(c.Query("state"), &st) == nil && st.Purpose == "connect" {
			page = "/organization"
		}
		h.back(c, page, "bitbucket", "declined")
		return
	}
	purpose, err := h.s.BitbucketDone(c.Request.Context(), c.Query("state"), nonce, c.Query("code"))
	page, ok := "/settings", "linked"
	if purpose == "connect" {
		page, ok = "/organization", "connected"
	}
	if err != nil {
		h.s.logger.Warn().Err(err).Str("purpose", purpose).Msg("bitbucket sign-in failed")
		outcome := "failed"
		switch {
		case apperrors.IsCode(err, apperrors.CodeConflict):
			outcome = "taken"
		case apperrors.IsCode(err, apperrors.CodeForbidden):
			outcome = "not-owner"
		}
		h.back(c, page, "bitbucket", outcome)
		return
	}
	h.back(c, page, "bitbucket", ok)
}

func (h *handler) bitbucketHook(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 25<<20))
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	delivery := c.GetHeader("X-Request-UUID")
	if delivery != "" {
		delivery = c.GetHeader("X-Hook-UUID") + "/" + delivery
	}
	err = h.s.BitbucketEvent(c.Request.Context(), c.Param("hookKey"), c.GetHeader("X-Event-Key"), delivery, c.GetHeader("X-Hub-Signature"), body)
	switch {
	case err == nil:
		c.Status(http.StatusNoContent)
	case errors.Is(err, ErrBadSignature):
		c.Status(http.StatusUnauthorized)
	case errors.Is(err, apperrors.ErrNotFound):
		c.Status(http.StatusNotFound)
	default:
		h.s.logger.Warn().Err(err).Str("event", c.GetHeader("X-Event-Key")).Msg("bitbucket event failed")
		c.Status(http.StatusInternalServerError)
	}
}

// ── access to projects ────────────────────────────────────────────────────

func idParam(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return uuid.Nil, false
	}
	return id, true
}

func (h *handler) projectAccess(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	view, err := h.s.ProjectAccess(c.Request.Context(), orgID, id, userID, auth.GetUserFromContext(c).Can(auth.PermOrgSettings))
	if err != nil {
		httpx.Error(c, err, "failed to read access")
		return
	}
	c.JSON(http.StatusOK, view)
}

func (h *handler) requestAccess(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		Level  string `json:"level"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	r, err := h.s.RequestAccess(c.Request.Context(), orgID, id, userID, req.Level, req.Reason)
	if err != nil {
		httpx.Error(c, err, "failed to request access")
		return
	}
	c.JSON(http.StatusCreated, r)
}

func (h *handler) grantAccess(c *gin.Context) {
	orgID, adminID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		UserID uuid.UUID `json:"userId"`
		Level  string    `json:"level"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	g, err := h.s.Grant(c.Request.Context(), orgID, id, req.UserID, adminID, req.Level)
	if err != nil {
		httpx.Error(c, err, "failed to give access")
		return
	}
	c.JSON(http.StatusOK, g)
}

func (h *handler) cancelRequest(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := h.s.CancelRequest(c.Request.Context(), orgID, id, userID); err != nil {
		httpx.Error(c, err, "failed to withdraw the request")
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) decide(approve bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		orgID, adminID, ok := caller(c)
		if !ok {
			return
		}
		id, ok := idParam(c)
		if !ok {
			return
		}
		var req struct {
			Note string `json:"note"`
		}
		_ = c.ShouldBindJSON(&req)
		g, err := h.s.Decide(c.Request.Context(), orgID, id, adminID, approve, req.Note)
		if err != nil {
			httpx.Error(c, err, "failed to answer the request")
			return
		}
		if g == nil {
			c.Status(http.StatusNoContent)
			return
		}
		c.JSON(http.StatusOK, g)
	}
}

func (h *handler) revoke(c *gin.Context) {
	orgID, adminID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := h.s.Revoke(c.Request.Context(), orgID, id, adminID); err != nil {
		httpx.Error(c, err, "failed to remove access")
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) pendingRequests(c *gin.Context) {
	orgID, _, ok := caller(c)
	if !ok {
		return
	}
	list, err := h.s.PendingRequests(c.Request.Context(), orgID)
	if err != nil {
		httpx.Error(c, err, "failed to list requests")
		return
	}
	c.JSON(http.StatusOK, gin.H{"requests": list})
}

func (h *handler) accessLog(c *gin.Context) {
	orgID, _, ok := caller(c)
	if !ok {
		return
	}
	var projectID *uuid.UUID
	if raw := c.Query("projectId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid project id"})
			return
		}
		projectID = &id
	}
	list, err := h.s.AccessLog(c.Request.Context(), orgID, projectID, 200)
	if err != nil {
		httpx.Error(c, err, "failed to read the access log")
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": list})
}
