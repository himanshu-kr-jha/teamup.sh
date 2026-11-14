package system

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/auth"
)

// RegisterRoutes serves the System tab to whoever may change the
// organization's settings, the same people who see the Email and Appearance tabs.
func RegisterRoutes(rg *gin.RouterGroup, s *Service) {
	rg.GET("/org/system", requireSettings, s.getReport)
	rg.GET("/org/system/history", requireSettings, s.getHistory)
}

func requireSettings(c *gin.Context) {
	if !auth.GetUserFromContext(c).Can(auth.PermOrgSettings) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "administrator access required"})
		return
	}
	c.Next()
}

func (s *Service) getReport(c *gin.Context) {
	orgID, err := uuid.Parse(auth.GetUserFromContext(c).OrgID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}
	r, err := s.Report(c.Request.Context(), orgID)
	if err != nil {
		s.d.Logger.Error().Err(err).Msg("system: could not build the report")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read the system's state"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, r)
}

func (s *Service) getHistory(c *gin.Context) {
	h, err := s.History(c.Request.Context())
	if err != nil {
		s.d.Logger.Error().Err(err).Msg("system: could not read the history")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read the history"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"points": h, "stepMinutes": int(historyStep.Minutes())})
}
