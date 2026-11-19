package org

// The organization's call backgrounds: an administrator puts in a dark and a
// light template, everybody in the organization can fetch them, and only
// pictures of the right kind, size and shape are taken.

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func pngOf(t *testing.T, w, h int, shade uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y += 7 {
		img.Set(w/2, y, color.RGBA{R: shade, G: 40, B: 90, A: 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// upload puts a picture in as the body of the request, as the page does.
func upload(t *testing.T, r *gin.Engine, theme string, raw []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/org/call-backgrounds/"+theme, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// webpHeader is the start of an extended WebP picture of the given size, which
// is all that is read of one.
func webpHeader(w, h int) []byte {
	b := make([]byte, 40)
	copy(b[0:4], "RIFF")
	binary.LittleEndian.PutUint32(b[4:8], 32)
	copy(b[8:12], "WEBP")
	copy(b[12:16], "VP8X")
	binary.LittleEndian.PutUint32(b[16:20], 10)
	put24 := func(at, v int) { b[at], b[at+1], b[at+2] = byte(v), byte(v>>8), byte(v>>16) }
	put24(24, w-1)
	put24(27, h-1)
	return b
}

func backgroundsOf(t *testing.T, rec *httptest.ResponseRecorder) CallBackgroundsView {
	t.Helper()
	var v CallBackgroundsView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	return v
}

func TestAnAdministratorPutsInReplacesAndRemovesTheCallBackgrounds(t *testing.T) {
	w := newMailWorld(t)
	admin := w.as(t, "admin")
	member := w.as(t, "member")

	if got := backgroundsOf(t, send(t, member, http.MethodGet, "/api/org/call-backgrounds", nil)); got.Dark != "" || got.Light != "" {
		t.Fatalf("none to begin with: %+v", got)
	}

	dark := pngOf(t, 1920, 1080, 20)
	saved := upload(t, admin, "dark", dark)
	v := backgroundsOf(t, saved)
	if saved.Code != http.StatusOK || !strings.HasPrefix(v.Dark, "/api/org/call-backgrounds/dark?v=") || v.Light != "" {
		t.Fatalf("saved: %d %+v", saved.Code, v)
	}

	// Everybody in the organization sees it and can fetch it.
	if got := backgroundsOf(t, send(t, member, http.MethodGet, "/api/org/call-backgrounds", nil)); !reflect.DeepEqual(got, v) {
		t.Errorf("a member sees the same: %+v", got)
	}
	pic := send(t, member, http.MethodGet, v.Dark, nil)
	if pic.Code != http.StatusOK || pic.Header().Get("Content-Type") != "image/png" || !bytes.Equal(pic.Body.Bytes(), dark) {
		t.Fatalf("the picture itself: %d %v", pic.Code, pic.Header())
	}
	if cc := pic.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") || !strings.Contains(cc, "private") {
		t.Errorf("cached for good, by the browser only: %q", cc)
	}
	if !strings.Contains(pic.Header().Get("Content-Security-Policy"), "sandbox") || pic.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("served inert: %v", pic.Header())
	}

	// The light one beside it.
	light := pngOf(t, 1280, 720, 240)
	both := backgroundsOf(t, upload(t, admin, "light", light))
	if both.Dark != v.Dark || !strings.HasPrefix(both.Light, "/api/org/call-backgrounds/light?v=") {
		t.Fatalf("both: %+v", both)
	}

	// Replacing the dark one gives it a new address; the old one is not cached for good.
	newer := pngOf(t, 1920, 1080, 60)
	replaced := backgroundsOf(t, upload(t, admin, "dark", newer))
	if replaced.Dark == v.Dark || replaced.Light != both.Light {
		t.Fatalf("replaced: %+v", replaced)
	}
	if got := send(t, member, http.MethodGet, v.Dark, nil); !bytes.Equal(got.Body.Bytes(), newer) || strings.Contains(got.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("an old address answers with the new picture, uncached: %v", got.Header())
	}

	// Removing one leaves the other.
	removed := backgroundsOf(t, send(t, admin, http.MethodDelete, "/api/org/call-backgrounds/dark", nil))
	if removed.Dark != "" || removed.Light != both.Light {
		t.Fatalf("removed: %+v", removed)
	}
	if got := send(t, member, http.MethodGet, "/api/org/call-backgrounds/dark", nil); got.Code != http.StatusNotFound {
		t.Errorf("no dark one any more: %d", got.Code)
	}
	if got := send(t, admin, http.MethodDelete, "/api/org/call-backgrounds/dark", nil); got.Code != http.StatusOK {
		t.Errorf("removing it again is not an error: %d", got.Code)
	}
}

func TestOnlyAPictureOfTheRightKindSizeAndShapeIsACallBackground(t *testing.T) {
	w := newMailWorld(t)
	admin := w.as(t, "admin")
	huge := make([]byte, MaxCallBackgroundBytes+1)
	copy(huge, pngOf(t, 1920, 1080, 1))
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="1920" height="1080"><script>alert(1)</script></svg>`
	for name, pic := range map[string][]byte{
		"nothing":                          nil,
		"an SVG, which can carry script":   []byte(svg),
		"a GIF":                            []byte("GIF89a\x80\x07\x38\x04"),
		"a data URL rather than a picture": []byte("data:image/png;base64,iVBORw0KGgo="),
		"too big":                          huge,
		"square":                           pngOf(t, 1440, 1440, 1),
		"a 4:3 picture":                    pngOf(t, 1600, 1200, 1),
		"smaller than the camera":          pngOf(t, 640, 360, 1),
		"a WebP of the wrong shape":        webpHeader(1600, 1200),
	} {
		if got := upload(t, admin, "dark", pic); got.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, got.Code, got.Body.String())
		}
	}
	if got := backgroundsOf(t, send(t, admin, http.MethodGet, "/api/org/call-backgrounds", nil)); got.Dark != "" {
		t.Errorf("nothing was kept: %+v", got)
	}

	// A JPEG and a WebP of the right shape are fine.
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 1366, 768)), nil); err != nil {
		t.Fatal(err)
	}
	if got := upload(t, admin, "dark", jpg.Bytes()); got.Code != http.StatusOK {
		t.Errorf("a JPEG: %d %s", got.Code, got.Body.String())
	}
	if got := upload(t, admin, "light", webpHeader(3840, 2160)); got.Code != http.StatusOK {
		t.Errorf("a WebP: %d %s", got.Code, got.Body.String())
	}

	// The Cognecto templates kept in the repository, at full size, are taken.
	for _, theme := range []string{"dark", "light"} {
		raw, err := os.ReadFile("../../docs/branding/cognecto-call-background-" + theme + ".png")
		if err != nil {
			t.Fatal(err)
		}
		if got := upload(t, admin, theme, raw); got.Code != http.StatusOK {
			t.Errorf("the Cognecto %s template: %d %s", theme, got.Code, got.Body.String())
		}
	}

	// Only dark and light.
	if got := upload(t, admin, "sepia", pngOf(t, 1280, 720, 1)); got.Code != http.StatusNotFound {
		t.Errorf("another theme: %d", got.Code)
	}
}

func TestOnlyAnAdministratorChangesTheCallBackgrounds(t *testing.T) {
	w := newMailWorld(t)
	member := w.as(t, "member")
	pic := pngOf(t, 1280, 720, 1)
	if got := upload(t, member, "dark", pic); got.Code != http.StatusForbidden {
		t.Errorf("a member puts one in: %d", got.Code)
	}
	if got := upload(t, w.as(t, "admin"), "dark", pic); got.Code != http.StatusOK {
		t.Fatalf("the administrator: %d %s", got.Code, got.Body.String())
	}
	if got := send(t, member, http.MethodDelete, "/api/org/call-backgrounds/dark", nil); got.Code != http.StatusForbidden {
		t.Errorf("a member removes one: %d", got.Code)
	}
	if got := backgroundsOf(t, send(t, member, http.MethodGet, "/api/org/call-backgrounds", nil)); got.Dark == "" {
		t.Errorf("still there: %+v", got)
	}
}

func TestTheSizeOfEachKindOfWebPIsRead(t *testing.T) {
	if w, h, ok := webpSize(webpHeader(1920, 1080)); !ok || w != 1920 || h != 1080 {
		t.Errorf("extended: %d %d %v", w, h, ok)
	}
	lossless := make([]byte, 30)
	copy(lossless, "RIFF\x00\x00\x00\x00WEBPVP8L\x00\x00\x00\x00\x2f")
	binary.LittleEndian.PutUint32(lossless[21:25], uint32(1920-1)|uint32(1080-1)<<14)
	if w, h, ok := webpSize(lossless); !ok || w != 1920 || h != 1080 {
		t.Errorf("lossless: %d %d %v", w, h, ok)
	}
	lossy := make([]byte, 30)
	copy(lossy, "RIFF\x00\x00\x00\x00WEBPVP8 \x00\x00\x00\x00\x00\x00\x00\x9d\x01\x2a")
	binary.LittleEndian.PutUint16(lossy[26:28], 1280)
	binary.LittleEndian.PutUint16(lossy[28:30], 720)
	if w, h, ok := webpSize(lossy); !ok || w != 1280 || h != 720 {
		t.Errorf("lossy: %d %d %v", w, h, ok)
	}
	if _, _, ok := webpSize([]byte("RIFF")); ok {
		t.Error("too short")
	}
}
