package org

import (
	"net/http"
	"strings"
	"testing"
)

// avatarUrl is drawn as an <img src> beside the person's name for everybody in
// the organization, so it may only be TeamUp's own picture address or an https
// one. Anything else (javascript:, data:, http:, another site by
// protocol-relative path, a megabyte of text) is refused.
func TestAnAvatarAddressIsOnlyOursOrHTTPS(t *testing.T) {
	w := newMailWorld(t)
	r := w.as(t, "admin")
	path := "/api/users/" + w.admin.ID.String()

	for _, ok := range []string{
		"",
		"/api/users/" + w.admin.ID.String() + "/avatar?v=0a1b2c3d4e5f",
		"https://images.example.com/ada.png",
	} {
		if got := send(t, r, http.MethodPatch, path, map[string]string{"avatarUrl": ok}); got.Code != http.StatusOK {
			t.Errorf("%q should be accepted: %d %s", ok, got.Code, got.Body.String())
		}
	}
	for _, bad := range []string{
		"javascript:alert(1)",
		"data:image/svg+xml;base64,PHN2Zz4=",
		"http://images.example.com/ada.png",
		"//evil.example.com/a.png",
		"/api/users/../../admin",
		"/somewhere/else.png",
		"https://",
		"https://" + strings.Repeat("a", 2048) + ".example.com/a.png",
		" https://images.example.com/ada.png",
	} {
		if got := send(t, r, http.MethodPatch, path, map[string]string{"avatarUrl": bad}); got.Code != http.StatusBadRequest {
			t.Errorf("%q should be refused: %d %s", bad, got.Code, got.Body.String())
		}
	}
	// A name change alone still works.
	if got := send(t, r, http.MethodPatch, path, map[string]string{"name": "Ada A."}); got.Code != http.StatusOK {
		t.Errorf("name only: %d %s", got.Code, got.Body.String())
	}
}
