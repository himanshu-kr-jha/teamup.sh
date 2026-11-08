package notetaker

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/auth"
	"github.com/teamup/teamup/internal/httpx"
)

// RegisterRoutes serves the settings to administrators and the minutes to
// whoever was in the call.
func RegisterRoutes(rg *gin.RouterGroup, s *Service) {
	rg.GET("/org/notetaker", requireAdmin, s.getSettings)
	rg.PUT("/org/notetaker", requireAdmin, s.putSettings)
	rg.GET("/calls/:id/minutes", s.getMinutes)
	rg.POST("/calls/:id/minutes/retry", s.retryMinutes)
	rg.GET("/meeting-notes", s.listNotes)
}

// listNotes is the Meeting notes page: ?scope=all for an administrator's view
// of every meeting, ?q= to search, ?before= for older ones.
func (s *Service) listNotes(c *gin.Context) {
	orgID, userID, isAdmin, ok := caller(c)
	if !ok {
		return
	}
	q := ListQuery{All: c.Query("scope") == "all", Search: c.Query("q")}
	if b := c.Query("before"); b != "" {
		t, err := time.Parse(time.RFC3339Nano, b)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid before"})
			return
		}
		q.Before = &t
	}
	items, more, err := s.List(c.Request.Context(), orgID, userID, isAdmin, q)
	if err != nil {
		httpx.Error(c, err, "failed to list the meeting notes")
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "hasMore": more})
}

func (s *Service) retryMinutes(c *gin.Context) {
	orgID, userID, isAdmin, ok := caller(c)
	if !ok {
		return
	}
	callID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	switch err := s.Retry(c.Request.Context(), orgID, callID, userID, isAdmin); {
	case errors.Is(err, ErrNoNotes):
		c.JSON(http.StatusNotFound, gin.H{"error": "there are no minutes for this call"})
	case errors.Is(err, ErrCannotRetry):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case err != nil:
		httpx.Error(c, err, "failed to write the minutes again")
	default:
		c.JSON(http.StatusAccepted, gin.H{"status": "writing"})
	}
}

func requireAdmin(c *gin.Context) {
	if !auth.GetUserFromContext(c).HasAnyRole("owner", "admin") {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "administrator access required"})
		return
	}
	c.Next()
}

func caller(c *gin.Context) (orgID, userID uuid.UUID, isAdmin, ok bool) {
	u := auth.GetUserFromContext(c)
	o, err1 := uuid.Parse(u.OrgID)
	id, err2 := uuid.Parse(u.UserID)
	if err1 != nil || err2 != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return uuid.Nil, uuid.Nil, false, false
	}
	return o, id, u.HasAnyRole("owner", "admin"), true
}

// settingsView is the settings without the keys, which never go back to a page.
type settingsView struct {
	Enabled     bool   `json:"enabled"`
	STTProvider string `json:"sttProvider"`
	STTModel    string `json:"sttModel"`
	STTLanguage string `json:"sttLanguage"`
	STTBaseURL  string `json:"sttBaseUrl"`
	STTKeySet   bool   `json:"sttKeySet"`
	LLMProvider string `json:"llmProvider"`
	LLMModel    string `json:"llmModel"`
	LLMBaseURL  string `json:"llmBaseUrl"`
	LLMKeySet   bool   `json:"llmKeySet"`
	Ready       bool   `json:"ready"`
	NotReady    string `json:"notReady,omitempty"`
}

func viewOf(s Settings) settingsView {
	ready, why := s.Ready()
	provider := s.LLMProvider
	if provider == "" {
		provider = "anthropic"
	}
	return settingsView{
		Enabled: s.Enabled, STTProvider: "sarvam", STTModel: s.STTModel, STTLanguage: s.STTLanguage, STTBaseURL: s.STTBaseURL,
		STTKeySet: s.STTKey != "", LLMProvider: provider, LLMModel: s.LLMModel, LLMBaseURL: s.LLMBaseURL,
		LLMKeySet: s.LLMKey != "", Ready: ready, NotReady: why,
	}
}

func (s *Service) getSettings(c *gin.Context) {
	orgID, _, _, ok := caller(c)
	if !ok {
		return
	}
	set, err := s.Settings(c.Request.Context(), orgID)
	if err != nil {
		httpx.Error(c, err, "failed to read the note-taker settings")
		return
	}
	c.JSON(http.StatusOK, viewOf(set))
}

// putSettings saves them. A key left blank keeps the saved one; "clear" in a
// key's place removes it.
func (s *Service) putSettings(c *gin.Context) {
	orgID, _, _, ok := caller(c)
	if !ok {
		return
	}
	var req Settings
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the settings are not valid"})
		return
	}
	saved, err := s.Settings(c.Request.Context(), orgID)
	if err != nil {
		httpx.Error(c, err, "failed to read the note-taker settings")
		return
	}
	next := Settings{
		Enabled:     req.Enabled,
		STTProvider: "sarvam",
		STTKey:      keep(req.STTKey, saved.STTKey),
		STTModel:    strings.TrimSpace(req.STTModel),
		STTLanguage: strings.TrimSpace(req.STTLanguage),
		STTBaseURL:  strings.TrimSpace(req.STTBaseURL),
		LLMProvider: strings.TrimSpace(req.LLMProvider),
		LLMKey:      keep(req.LLMKey, saved.LLMKey),
		LLMModel:    strings.TrimSpace(req.LLMModel),
		LLMBaseURL:  strings.TrimSpace(req.LLMBaseURL),
	}
	if err := next.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := next.CheckModel(c.Request.Context()); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := s.config.SetConfig(c.Request.Context(), orgID, ConfigKey, next, true); err != nil {
		httpx.Error(c, err, "failed to save the note-taker settings")
		return
	}
	c.JSON(http.StatusOK, viewOf(next))
}

func keep(given, saved string) string {
	given = strings.TrimSpace(given)
	switch given {
	case "":
		return saved
	case "clear":
		return ""
	}
	return given
}

func (s *Service) getMinutes(c *gin.Context) {
	orgID, userID, isAdmin, ok := caller(c)
	if !ok {
		return
	}
	callID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	notes, err := s.NotesFor(c.Request.Context(), orgID, callID, userID, isAdmin)
	if errors.Is(err, ErrNoNotes) {
		c.JSON(http.StatusNotFound, gin.H{"error": "there are no minutes for this call"})
		return
	}
	if err != nil {
		httpx.Error(c, err, "failed to read the minutes")
		return
	}
	c.JSON(http.StatusOK, notes)
}
