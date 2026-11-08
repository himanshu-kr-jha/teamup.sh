package org

// The organization's colour and logo: anybody can read them, which is what lets
// the sign-in page wear them, and only an administrator changes them.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func publicRouter(w *mailWorld) *gin.Engine {
	r := gin.New()
	RegisterPublicRoutes(r.Group("/api"), w.svc)
	return r
}

func brandingOf(t *testing.T, rec *httptest.ResponseRecorder) BrandingView {
	t.Helper()
	var v BrandingView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	return v
}

func TestTheDefaultLookIsTeamUpsOwn(t *testing.T) {
	w := newMailWorld(t)
	got := send(t, publicRouter(w), http.MethodGet, "/api/branding", nil)
	v := brandingOf(t, got)
	if got.Code != http.StatusOK || v.Colour != "" || v.LogoURL != "" || v.Name == "" {
		t.Errorf("no colour, no logo, the organization's name: %d %+v", got.Code, v)
	}
}

func TestAnAdministratorSetsAColourAndLogoAndEverybodySeesThem(t *testing.T) {
	w := newMailWorld(t)
	admin := w.as(t, "admin")
	logo := tinyPNG(t)

	saved := send(t, admin, http.MethodPut, "/api/org/branding", map[string]any{
		"color": "#0F766E", "logo": "data:image/png;base64," + base64.StdEncoding.EncodeToString(logo),
	})
	v := brandingOf(t, saved)
	if saved.Code != http.StatusOK || v.Colour != "#0f766e" || !strings.HasPrefix(v.LogoURL, "/api/branding/logo?v=") {
		t.Fatalf("saved: %d %+v", saved.Code, v)
	}

	public := publicRouter(w)
	if got := brandingOf(t, send(t, public, http.MethodGet, "/api/branding", nil)); got != v {
		t.Errorf("signed out sees the same: %+v", got)
	}
	pic := send(t, public, http.MethodGet, v.LogoURL, nil)
	if pic.Code != http.StatusOK || pic.Header().Get("Content-Type") != "image/png" || !bytes.Equal(pic.Body.Bytes(), logo) {
		t.Fatalf("the logo itself: %d %s", pic.Code, pic.Header())
	}
	if !strings.Contains(pic.Header().Get("Cache-Control"), "immutable") || !strings.Contains(pic.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("cached for good, and inert: %v", pic.Header())
	}

	// Changing only the colour keeps the logo.
	kept := brandingOf(t, send(t, admin, http.MethodPut, "/api/org/branding", map[string]any{"color": "#be123c"}))
	if kept.LogoURL != v.LogoURL || kept.Colour != "#be123c" {
		t.Errorf("logo kept: %+v", kept)
	}

	// Back to the default.
	reset := brandingOf(t, send(t, admin, http.MethodPut, "/api/org/branding", map[string]any{"color": "", "logo": ""}))
	if reset.Colour != "" || reset.LogoURL != "" {
		t.Errorf("reset: %+v", reset)
	}
	if got := send(t, public, http.MethodGet, "/api/branding/logo", nil); got.Code != http.StatusNotFound {
		t.Errorf("no logo any more: %d", got.Code)
	}
}

func TestOnlyPicturesOfASensibleSizeAreLogos(t *testing.T) {
	w := newMailWorld(t)
	admin := w.as(t, "admin")
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`
	huge := make([]byte, MaxLogoBytes+1)
	copy(huge, tinyPNG(t))
	for name, body := range map[string]map[string]any{
		"an SVG, which can carry script": {"logo": "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte(svg))},
		"not a data URL":                 {"logo": "https://example.com/logo.png"},
		"too big":                        {"logo": "data:image/png;base64," + base64.StdEncoding.EncodeToString(huge)},
		"not a colour":                   {"color": "red"},
	} {
		if got := send(t, admin, http.MethodPut, "/api/org/branding", body); got.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, got.Code, got.Body.String())
		}
	}
}

func TestOnlyAnAdministratorChangesTheLook(t *testing.T) {
	w := newMailWorld(t)
	if got := send(t, w.as(t, "member"), http.MethodPut, "/api/org/branding", map[string]any{"color": "#000000"}); got.Code != http.StatusForbidden {
		t.Errorf("member: %d", got.Code)
	}
}
