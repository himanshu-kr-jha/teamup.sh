package main

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/livekit/protocol/auth"
)

// The webhook has no session, and everything else on the calendar and calls side
// has to. A route registered in the wrong group fails one of these, in the real
// binary, where the handler tests cannot see it.
func TestTheLiveKitWebhookIsOutsideTheSessionAndTheCalendarIsInside(t *testing.T) {
	srv := startServer(t, serverOptions{})

	post := func(authorization string, body string) int {
		req, err := http.NewRequest(http.MethodPost, srv.baseURL+"/api/livekit/webhook", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/webhook+json")
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}

	body := `{"event":"room_started","id":"EV_1","room":{"name":"not-one-of-ours"}}`
	sum := sha256.Sum256([]byte(body))
	signed, err := auth.NewAccessToken("devkey", "devsecret").
		SetValidFor(time.Minute).
		SetSha256(base64.StdEncoding.EncodeToString(sum[:])).
		ToJWT()
	if err != nil {
		t.Fatal(err)
	}
	if got := post(signed, body); got != http.StatusOK {
		t.Errorf("signed by LiveKit, with no session: want 200, got %d", got)
	}
	if got := post("", body); got != http.StatusUnauthorized {
		t.Errorf("not signed: want 401, got %d", got)
	}
	if got := post(signed, body+" "); got != http.StatusUnauthorized {
		t.Errorf("signature of a different body: want 401, got %d", got)
	}

	for _, path := range []string{"/api/calendar", "/api/standing-meetings"} {
		if status, _ := srv.get(path); status != http.StatusUnauthorized {
			t.Errorf("%s without a session: want 401, got %d", path, status)
		}
	}
}
