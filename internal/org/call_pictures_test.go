package org

// The organization's own background pictures: an administrator puts in a set,
// everybody in the organization can fetch them, each is brought to 16:9 at most
// 1920 x 1080 with a small copy for the menu, and nothing else is taken.

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func addPicture(t *testing.T, r *gin.Engine, name string, raw []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/org/call-pictures?name="+url.QueryEscape(name), bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func names(v CallBackgroundsView) []string {
	out := []string{}
	for _, p := range v.Pictures {
		out = append(out, p.Name)
	}
	return out
}

func jpegOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// The left half red and the right half blue, to see a crop from the middle.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x < w/2 {
				img.Set(x, y, color.RGBA{R: 220, A: 255})
			} else {
				img.Set(x, y, color.RGBA{B: 220, A: 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decodedSize(t *testing.T, raw []byte) (int, int) {
	t.Helper()
	cfg, kind, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || kind != "jpeg" {
		t.Fatalf("not a JPEG: %v %s", err, kind)
	}
	return cfg.Width, cfg.Height
}

// pngHeaderOf is a PNG that says it is the given size, which is all that is
// read of one before deciding whether to decode it.
func pngHeaderOf(w, h int) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(h))
	ihdr[8], ihdr[9] = 8, 2
	chunk := append([]byte("IHDR"), ihdr...)
	_ = binary.Write(&b, binary.BigEndian, uint32(13))
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return b.Bytes()
}

func TestAnAdministratorPutsInASetOfCallPicturesAndEverybodyCanUseThem(t *testing.T) {
	w := newMailWorld(t)
	admin := w.as(t, "admin")
	member := w.as(t, "member")

	if got := backgroundsOf(t, send(t, member, http.MethodGet, "/api/org/call-backgrounds", nil)); got.Pictures == nil || len(got.Pictures) != 0 {
		t.Fatalf("an empty list to begin with: %+v", got)
	}

	webp, err := os.ReadFile("testdata/backdrop-1600x900.webp")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		name string
		raw  []byte
	}{
		{"Harbour", pngOf(t, 1920, 1080, 20)},
		{"  Wide\tlobby  ", jpegOf(t, 3200, 1200)},
		{"Studio wall", webp},
		{"", jpegOf(t, 1600, 1200)},
	} {
		if got := addPicture(t, admin, p.name, p.raw); got.Code != http.StatusCreated {
			t.Fatalf("%q: %d %s", p.name, got.Code, got.Body.String())
		}
	}
	v := backgroundsOf(t, send(t, member, http.MethodGet, "/api/org/call-backgrounds", nil))
	if got := strings.Join(names(v), ", "); got != "Harbour, Wide lobby, Studio wall, Background" {
		t.Fatalf("in the order put in, names tidied: %s", got)
	}

	// Each is a 16:9 JPEG at most 1920 wide, with a small copy, cached for good.
	sizes := map[string][2]int{"Harbour": {1920, 1080}, "Wide lobby": {1920, 1080}, "Studio wall": {1600, 900}, "Background": {1600, 900}}
	for _, p := range v.Pictures {
		if !strings.HasPrefix(p.URL, "/api/org/call-pictures/"+p.ID+"?v=") || !strings.HasPrefix(p.ThumbURL, "/api/org/call-pictures/"+p.ID+"/thumb?v=") {
			t.Fatalf("addresses: %+v", p)
		}
		pic := send(t, member, http.MethodGet, p.URL, nil)
		if pic.Code != http.StatusOK || pic.Header().Get("Content-Type") != "image/jpeg" {
			t.Fatalf("%s: %d %v", p.Name, pic.Code, pic.Header())
		}
		if w, h := decodedSize(t, pic.Body.Bytes()); [2]int{w, h} != sizes[p.Name] {
			t.Errorf("%s is %dx%d", p.Name, w, h)
		}
		if cc := pic.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") || !strings.Contains(cc, "private") {
			t.Errorf("cached for good, by the browser only: %q", cc)
		}
		if !strings.Contains(pic.Header().Get("Content-Security-Policy"), "sandbox") || pic.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("served inert: %v", pic.Header())
		}
		thumb := send(t, member, http.MethodGet, p.ThumbURL, nil)
		if w, h := decodedSize(t, thumb.Body.Bytes()); w != 384 || h != 216 {
			t.Errorf("%s's small copy is %dx%d", p.Name, w, h)
		}
		if thumb.Body.Len() > 40_000 {
			t.Errorf("%s's small copy is %d bytes", p.Name, thumb.Body.Len())
		}
	}

	// The wide one was cut from its middle: red on the left, blue on the right.
	wide := send(t, member, http.MethodGet, v.Pictures[1].URL, nil)
	img, err := jpeg.Decode(bytes.NewReader(wide.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if r, _, b, _ := img.At(100, 540).RGBA(); r>>8 < 180 || b>>8 > 60 {
		t.Errorf("the left is red: %d %d", r>>8, b>>8)
	}
	if r, _, b, _ := img.At(1820, 540).RGBA(); b>>8 < 180 || r>>8 > 60 {
		t.Errorf("the right is blue: %d %d", r>>8, b>>8)
	}

	// Renamed, and put in another order.
	harbour, lobby, studio, plain := v.Pictures[0], v.Pictures[1], v.Pictures[2], v.Pictures[3]
	renamed := backgroundsOf(t, send(t, admin, http.MethodPatch, "/api/org/call-pictures/"+plain.ID, map[string]string{"name": "Meeting room"}))
	if got := strings.Join(names(renamed), ", "); got != "Harbour, Wide lobby, Studio wall, Meeting room" {
		t.Errorf("renamed: %s", got)
	}
	if got := send(t, admin, http.MethodPatch, "/api/org/call-pictures/"+plain.ID, map[string]string{"name": "   "}); got.Code != http.StatusBadRequest {
		t.Errorf("no name at all: %d", got.Code)
	}
	// Those named first; one left out keeps its place after them.
	ordered := backgroundsOf(t, send(t, admin, http.MethodPut, "/api/org/call-pictures/order", map[string][]string{"ids": {studio.ID, plain.ID, harbour.ID}}))
	if got := strings.Join(names(ordered), ", "); got != "Studio wall, Meeting room, Harbour, Wide lobby" {
		t.Errorf("ordered: %s", got)
	}

	// Removed: gone from the list and from its address.
	removed := backgroundsOf(t, send(t, admin, http.MethodDelete, "/api/org/call-pictures/"+lobby.ID, nil))
	if got := strings.Join(names(removed), ", "); got != "Studio wall, Meeting room, Harbour" {
		t.Errorf("removed: %s", got)
	}
	if got := send(t, member, http.MethodGet, lobby.URL, nil); got.Code != http.StatusNotFound {
		t.Errorf("its address: %d", got.Code)
	}
	if got := send(t, admin, http.MethodDelete, "/api/org/call-pictures/"+lobby.ID, nil); got.Code != http.StatusOK {
		t.Errorf("removing it again is not an error: %d", got.Code)
	}

	// Another organization's people cannot fetch them.
	otherOrg, err := w.svc.CreateOrganization(context.Background(), "Other Co", "other-co-"+uuid.NewString()[:8], nil)
	if err != nil {
		t.Fatal(err)
	}
	seedDefaultRoles(t, w.svc, otherOrg.ID)
	otherAdmin, _, err := w.svc.AddMember(context.Background(), otherOrg.ID, "Oscar Other", "oscar@other.test", "admin")
	if err != nil {
		t.Fatal(err)
	}
	other := withMailer(t, w.svc, otherOrg.ID.String(), otherAdmin.ID.String(), w.mailer, "admin")
	if got := send(t, other, http.MethodGet, harbour.URL, nil); got.Code != http.StatusNotFound {
		t.Errorf("from another organization: %d", got.Code)
	}
	if got := send(t, other, http.MethodDelete, "/api/org/call-pictures/"+harbour.ID, nil); got.Code != http.StatusOK {
		t.Fatalf("another organization's delete: %d", got.Code)
	}
	if got := send(t, member, http.MethodGet, harbour.URL, nil); got.Code != http.StatusOK {
		t.Errorf("still there after another organization tried to remove it: %d", got.Code)
	}

	// The templates are untouched by all of it.
	if got := backgroundsOf(t, send(t, member, http.MethodGet, "/api/org/call-backgrounds", nil)); got.Dark != "" || got.Light != "" {
		t.Errorf("templates: %+v", got)
	}
}

func TestOnlyAPictureOfTheRightKindAndSizeIsACallPicture(t *testing.T) {
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
		"smaller than the camera":          pngOf(t, 640, 360, 1),
		"too short once cut to 16:9":       pngOf(t, 1600, 700, 1),
		"too narrow once cut to 16:9":      pngOf(t, 1200, 1600, 1),
		"far too many pixels to decode":    pngHeaderOf(9000, 6000),
		"a header with nothing after it":   pngHeaderOf(1920, 1080),
		"a WebP header with no picture":    webpHeader(1920, 1080),
	} {
		if got := addPicture(t, admin, name, pic); got.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, got.Code, got.Body.String())
		}
	}
	if got := backgroundsOf(t, send(t, admin, http.MethodGet, "/api/org/call-backgrounds", nil)); len(got.Pictures) != 0 {
		t.Errorf("nothing was kept: %+v", got)
	}

	// At most 24.
	small := pngOf(t, 1280, 720, 1)
	for i := range MaxCallPictures {
		if got := addPicture(t, admin, "Picture", small); got.Code != http.StatusCreated {
			t.Fatalf("picture %d: %d %s", i+1, got.Code, got.Body.String())
		}
	}
	got := addPicture(t, admin, "One more", small)
	if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "at most 24") {
		t.Errorf("the 25th: %d %s", got.Code, got.Body.String())
	}
}

func TestOnlyAnAdministratorChangesTheCallPictures(t *testing.T) {
	w := newMailWorld(t)
	member := w.as(t, "member")
	admin := w.as(t, "admin")
	pic := pngOf(t, 1280, 720, 1)
	if got := addPicture(t, member, "Mine", pic); got.Code != http.StatusForbidden {
		t.Errorf("a member puts one in: %d", got.Code)
	}
	saved := addPicture(t, admin, "Ours", pic)
	if saved.Code != http.StatusCreated {
		t.Fatalf("the administrator: %d %s", saved.Code, saved.Body.String())
	}
	id := backgroundsOf(t, saved).Pictures[0].ID
	for _, req := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPatch, "/api/org/call-pictures/" + id, map[string]string{"name": "Mine now"}},
		{http.MethodPut, "/api/org/call-pictures/order", map[string][]string{"ids": {id}}},
		{http.MethodDelete, "/api/org/call-pictures/" + id, nil},
	} {
		if got := send(t, member, req.method, req.path, req.body); got.Code != http.StatusForbidden {
			t.Errorf("a member %s %s: %d", req.method, req.path, got.Code)
		}
	}
	if got := backgroundsOf(t, send(t, member, http.MethodGet, "/api/org/call-backgrounds", nil)); len(got.Pictures) != 1 || got.Pictures[0].Name != "Ours" {
		t.Errorf("still there as it was: %+v", got)
	}
}

func TestAPictureNameIsOneShortLine(t *testing.T) {
	for in, want := range map[string]string{
		"  Harbour  ":             "Harbour",
		"Two\nlines\r\nhere":      "Two lines here",
		"tab\tbetween":            "tab between",
		strings.Repeat("é", 70):   strings.Repeat("é", 60),
		"separator in the middle": "separator in the middle",
		"":                        "",
	} {
		if got := cleanPictureName(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}
