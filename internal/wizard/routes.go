package wizard

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// SetupGuardMiddleware blocks setup mutations once an organization exists, so a
// live instance cannot be re-provisioned by anyone who can reach the endpoint.
// It answers with JSON rather than a redirect: these are API routes, and a 302
// here made the status endpoint unreadable to the frontend.
func SetupGuardMiddleware(service *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		complete, err := service.IsSetupComplete(c.Request.Context())
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to check setup status"})
			return
		}
		if complete {
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{
				"error":    "setup is already complete",
				"complete": true,
			})
			return
		}
		c.Next()
	}
}

// maxRequestBody bounds wizard payloads. These endpoints are reachable without
// authentication before setup, and the org logo arrives inline as a base64 data
// URI, so an unbounded body would let anyone push arbitrary megabytes into a
// session held in Redis for 30 minutes.
const maxRequestBody = 2 << 20 // 2 MiB

func limitBody() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBody)
		c.Next()
	}
}

func RegisterRoutes(rg *gin.RouterGroup, service *Service) {
	h := NewHandler(service)

	// Readable at any time — the frontend uses it to decide whether to show
	// the wizard at all, so it must never be gated or redirected.
	rg.GET("/status", h.GetStatus)

	guarded := rg.Group("")
	guarded.Use(limitBody(), SetupGuardMiddleware(service), requireSetupToken(service))
	{
		guarded.POST("/step1", h.Step1Organization)
		guarded.POST("/step2", h.Step2Admin)
		guarded.POST("/step3", h.Step3Auth)
		guarded.POST("/step4", h.Step4Services)
		guarded.POST("/step5", h.Step5Complete)
		guarded.POST("/test-service", h.TestService)
	}
}

// isSecureRequest reports whether the browser reached us over HTTPS. The
// deployment terminates TLS at nginx and proxies plaintext, so c.Request.TLS is
// nil in production; without the forwarded header the session cookie would
// never be marked Secure and could leak over the pre-redirect cleartext hop.
func isSecureRequest(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	proto := c.GetHeader("X-Forwarded-Proto")
	if proto == "" {
		return false
	}
	// May be a comma-separated chain; the client-facing value is first.
	if i := strings.IndexByte(proto, ','); i >= 0 {
		proto = proto[:i]
	}
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

// loadState resolves the caller's wizard session, starting a fresh one when the
// cookie is absent, expired, or does not correspond to a session we issued.
// An unrecognised id is discarded rather than adopted, so a planted cookie
// cannot become the session the operator then fills in.
func (h *Handler) loadState(c *gin.Context) (*State, string, error) {
	id, err := c.Cookie(SessionCookie)
	if err != nil {
		id = ""
	}
	state, found, err := h.service.sessions.Load(c.Request.Context(), id)
	if err != nil {
		return nil, "", err
	}
	if !found {
		return state, "", nil
	}
	return state, id, nil
}

func (h *Handler) saveState(c *gin.Context, id string, state *State) error {
	newID, err := h.service.sessions.Save(c.Request.Context(), id, state, SessionTTL)
	if err != nil {
		return err
	}
	if newID != id {
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie(SessionCookie, newID, int(SessionTTL.Seconds()), "/", "", isSecureRequest(c), true)
	}
	return nil
}

func (h *Handler) clearState(c *gin.Context, id string) {
	if err := h.service.sessions.Delete(c.Request.Context(), id); err != nil {
		// The session still holds the admin password and service secrets, so a
		// failure to drop it early is worth recording even though setup
		// succeeded; it expires with the TTL regardless.
		h.service.logger.Warn().Err(err).Msg("failed to clear wizard session after setup")
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(SessionCookie, "", -1, "/", "", isSecureRequest(c), true)
}

// applyStep is the shared shape of steps 1-4: bind the payload, fold it into
// the session state, persist, and report progress.
func applyStep[T any](h *Handler, c *gin.Context, apply func(*State, T)) {
	var req T
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	state, id, err := h.loadState(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load setup session"})
		return
	}

	apply(state, req)

	if err := h.saveState(c, id, state); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save setup session"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"currentStep":    state.CurrentStep,
		"completedSteps": state.CompletedSteps,
		"missingSteps":   state.MissingSteps(),
	})
}

func (h *Handler) GetStatus(c *gin.Context) {
	complete, err := h.service.IsSetupComplete(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check setup status"})
		return
	}

	resp := gin.H{"complete": complete}
	if state, _, err := h.loadState(c); err == nil {
		resp["currentStep"] = state.CurrentStep
		resp["completedSteps"] = state.CompletedSteps
		resp["missingSteps"] = state.MissingSteps()
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) Step1Organization(c *gin.Context) {
	applyStep(h, c, func(s *State, in OrganizationStep) { s.ApplyOrganization(in) })
}

func (h *Handler) Step2Admin(c *gin.Context) {
	applyStep(h, c, func(s *State, in AdminStep) { s.ApplyAdmin(in) })
}

func (h *Handler) Step3Auth(c *gin.Context) {
	applyStep(h, c, func(s *State, in AuthStep) { s.ApplyAuth(in) })
}

func (h *Handler) Step4Services(c *gin.Context) {
	applyStep(h, c, func(s *State, in ServicesStep) { s.ApplyServices(in) })
}

func (h *Handler) Step5Complete(c *gin.Context) {
	state, id, err := h.loadState(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load setup session"})
		return
	}

	// Refuse before any database work: finalizing a partial session is what
	// created a blank organization and locked the instance out of setup.
	if missing := state.MissingSteps(); len(missing) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":        "setup is incomplete; finish the remaining steps first",
			"missingSteps": missing,
		})
		return
	}
	if err := state.Data.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.service.FinalizeSetup(c.Request.Context(), state.Data); err != nil {
		// These endpoints are reachable unauthenticated before setup, so the
		// raw error (a wrapped pgx failure naming constraints and SQL) stays in
		// the log rather than going back over the wire.
		h.service.logger.Error().Err(err).Msg("setup finalization failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not complete setup; check the server logs"})
		return
	}

	h.clearState(c, id)
	h.service.forgetSetupToken(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"message": "setup complete", "redirect": "/"})
}

// TestService probes one dependency with the credentials entered so far. It
// previously reported success without connecting to anything.
func (h *Handler) TestService(c *gin.Context) {
	// The client posts the whole form, so the connection details for the
	// service under test arrive alongside the service name. Bound in one pass
	// because the request body can only be read once.
	var req struct {
		Service string `json:"service" binding:"required,oneof=minio livekit smtp"`

		MinIOEndpoint    string `json:"minioEndpoint"`
		MinIOAccessKey   string `json:"minioAccessKey"`
		MinIOSecretKey   string `json:"minioSecretKey"`
		MinIOBucket      string `json:"minioBucket"`
		LiveKitURL       string `json:"livekitUrl"`
		LiveKitAPIKey    string `json:"livekitApiKey"`
		LiveKitAPISecret string `json:"livekitApiSecret"`
		SMTPHost         string `json:"smtpHost"`
		SMTPPort         int    `json:"smtpPort"`
		SMTPUser         string `json:"smtpUser"`
		SMTPPass         string `json:"smtpPass"`
		SMTPFrom         string `json:"smtpFrom"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	creds := ServicesStep{
		MinIOEndpoint: req.MinIOEndpoint, MinIOAccessKey: req.MinIOAccessKey,
		MinIOSecretKey: req.MinIOSecretKey, MinIOBucket: req.MinIOBucket,
		LiveKitURL: req.LiveKitURL, LiveKitAPIKey: req.LiveKitAPIKey,
		LiveKitAPISecret: req.LiveKitAPISecret,
		SMTPHost:         req.SMTPHost, SMTPPort: req.SMTPPort,
		SMTPUser: req.SMTPUser, SMTPPass: req.SMTPPass, SMTPFrom: req.SMTPFrom,
	}

	state, _, err := h.loadState(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load setup session"})
		return
	}
	data := state.Data
	mergeServiceCredentials(&data, creds)

	if err := h.service.TestService(c.Request.Context(), req.Service, data); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "connection successful"})
}

// mergeServiceCredentials prefers values supplied with the test request but
// falls back to whatever the session already holds.
func mergeServiceCredentials(dst *Data, in ServicesStep) {
	set := func(target *string, value string) {
		if value != "" {
			*target = value
		}
	}
	set(&dst.MinIOEndpoint, in.MinIOEndpoint)
	set(&dst.MinIOAccessKey, in.MinIOAccessKey)
	set(&dst.MinIOSecretKey, in.MinIOSecretKey)
	set(&dst.MinIOBucket, in.MinIOBucket)
	set(&dst.LiveKitURL, in.LiveKitURL)
	set(&dst.LiveKitAPIKey, in.LiveKitAPIKey)
	set(&dst.LiveKitAPISecret, in.LiveKitAPISecret)
	set(&dst.SMTPHost, in.SMTPHost)
	set(&dst.SMTPUser, in.SMTPUser)
	set(&dst.SMTPPass, in.SMTPPass)
	set(&dst.SMTPFrom, in.SMTPFrom)
	if in.SMTPPort != 0 {
		dst.SMTPPort = in.SMTPPort
	}
}
