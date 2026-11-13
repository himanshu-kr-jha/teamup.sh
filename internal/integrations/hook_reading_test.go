package integrations

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/teamup/teamup/internal/testsupport"
)

// countingBody is a request body that says how much of it was read.
type countingBody struct{ read int }

func (b *countingBody) Read(p []byte) (int, error) {
	if b.read >= 25<<20 {
		return 0, io.EOF
	}
	b.read += len(p)
	return len(p), nil
}

// An event for a hook nobody set up, or with no signature, is turned away
// before its body is read: otherwise anybody could make the server hold 25 MB
// in memory per request just to find it was never going to be accepted.
func TestAWebhookNobodySetUpIsRefusedBeforeItsBodyIsRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := testsupport.DB(t)
	s := NewService(pool, Config{BaseURL: "https://teamup.example", Secret: "a-server-secret-long-enough"}, nil)
	h := &handler{s: s}
	r := gin.New()
	r.POST("/github/:hookKey", h.hook)
	r.POST("/bitbucket/:hookKey", h.bitbucketHook)

	for _, tc := range []struct {
		path, header string
		want         int
	}{
		{"/github/nobody", "X-Hub-Signature-256", http.StatusNotFound},
		{"/github/nobody", "", http.StatusUnauthorized},
		{"/bitbucket/nobody", "X-Hub-Signature", http.StatusNotFound},
		{"/bitbucket/nobody", "", http.StatusUnauthorized},
	} {
		body := &countingBody{}
		req := httptest.NewRequest(http.MethodPost, tc.path, body)
		if tc.header != "" {
			req.Header.Set(tc.header, "sha256=00")
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Errorf("%s (signature %q): %d, want %d", tc.path, tc.header, w.Code, tc.want)
		}
		if body.read > 0 {
			t.Errorf("%s: read %d bytes of a request it was going to refuse", tc.path, body.read)
		}
	}
}
