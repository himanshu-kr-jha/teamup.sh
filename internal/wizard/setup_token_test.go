package wizard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

// Before setup nobody is signed in, so on a fresh server the first stranger to
// open the wizard would make themselves owner. Every step needs the code the
// server printed in its log.
func TestSetupNeedsTheCodeFromTheServersLog(t *testing.T) {
	service := &Service{sessions: newMemoryStore(), logger: zerolog.Nop()}
	r := gin.New()
	r.POST("/api/wizard/step1", requireSetupToken(service), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	step := func(code string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/wizard/step1", nil)
		if code != "" {
			req.Header.Set(SetupTokenHeader, code)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	if got := step(""); got != http.StatusForbidden {
		t.Fatalf("with no code: %d, want 403", got)
	}
	if got := step("guess"); got != http.StatusForbidden {
		t.Fatalf("with a wrong code: %d, want 403", got)
	}
	code, err := service.SetupToken(context.Background())
	if err != nil || len(code) < 20 {
		t.Fatalf("code %q, %v", code, err)
	}
	if again, _ := service.SetupToken(context.Background()); again != code {
		t.Fatal("the code changed between asks")
	}
	if got := step(code); got != http.StatusNoContent {
		t.Fatalf("with the code: %d, want it through", got)
	}
	service.forgetSetupToken(context.Background())
	if got := step(code); got != http.StatusForbidden {
		t.Fatalf("the code still worked after setup: %d", got)
	}
}
