package org

// The organization's call backgrounds: one picture for a dark look and one for
// a light one, with the top right left empty. Each person's browser writes their
// own name and company role there (web/src/features/calls/companyBackground.ts),
// so one upload gives everybody a background of their own.
//
// They are kept the way the logo is, in the organization's settings, but each
// under its own key: a template is a few hundred kilobytes, and the logo and
// colour are read on every page load, so they are not carried along with it.

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	_ "image/jpeg" // registers JPEG for image.DecodeConfig
	_ "image/png"  // registers PNG for image.DecodeConfig
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/httpx"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

// MaxCallBackgroundBytes is the largest template accepted, which is also the
// most any API request may carry. A 3840 x 2160 PNG of a designed background is
// well under it; a photograph should be a JPEG.
const MaxCallBackgroundBytes = 2 << 20

// minCallBackgroundWidth is the camera's own width: anything smaller would be
// stretched and blurry behind everybody.
const minCallBackgroundWidth = 1280

var callBackgroundTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true}

func callBackgroundKey(theme string) string { return "call_background_" + theme }

// CallBackground is what is stored for one theme.
type CallBackground struct {
	Type string `json:"type"`
	// Data is the picture, base64.
	Data string `json:"data"`
	// Version changes with the picture, so its address does too.
	Version string `json:"version"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
}

// CallBackgroundsView is what is shown: each template by address, empty for
// none, so the pictures are fetched once and cached; and the organization's own
// pictures (call_pictures.go), in their order.
type CallBackgroundsView struct {
	Dark     string            `json:"dark"`
	Light    string            `json:"light"`
	Pictures []CallPictureView `json:"pictures"`
}

func callBackgroundURL(theme, version string) string {
	return "/api/org/call-backgrounds/" + theme + "?v=" + version
}

func validTheme(c *gin.Context) (string, bool) {
	theme := c.Param("theme")
	if theme != "dark" && theme != "light" {
		c.JSON(http.StatusNotFound, gin.H{"error": "There is no such background."})
		return "", false
	}
	return theme, true
}

// callBackgroundsView reads only each template's version, not the pictures.
func (h *Handler) callBackgroundsView(c *gin.Context, orgID uuid.UUID) (CallBackgroundsView, bool) {
	rows, err := h.service.db.Query(c.Request.Context(),
		`SELECT key, value->>'version' FROM org_configs WHERE org_id = $1 AND key = ANY($2)`,
		orgID, []string{callBackgroundKey("dark"), callBackgroundKey("light")})
	if err != nil {
		httpx.Error(c, err, "failed to read the call backgrounds")
		return CallBackgroundsView{}, false
	}
	defer rows.Close()
	var view CallBackgroundsView
	for rows.Next() {
		var key string
		var version *string
		if err := rows.Scan(&key, &version); err != nil {
			httpx.Error(c, err, "failed to read the call backgrounds")
			return CallBackgroundsView{}, false
		}
		if version == nil || *version == "" {
			continue
		}
		switch key {
		case callBackgroundKey("dark"):
			view.Dark = callBackgroundURL("dark", *version)
		case callBackgroundKey("light"):
			view.Light = callBackgroundURL("light", *version)
		}
	}
	if err := rows.Err(); err != nil {
		httpx.Error(c, err, "failed to read the call backgrounds")
		return CallBackgroundsView{}, false
	}
	rows.Close()
	pictures, err := h.service.callPictures(c.Request.Context(), orgID)
	if err != nil {
		httpx.Error(c, err, "failed to read the call backgrounds")
		return CallBackgroundsView{}, false
	}
	view.Pictures = pictures
	return view, true
}

// ListCallBackgrounds says which templates there are, to everybody in the
// organization: everybody may use them.
func (h *Handler) ListCallBackgrounds(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	view, ok := h.callBackgroundsView(c, orgID)
	if !ok {
		return
	}
	c.Header("Cache-Control", "private, no-cache")
	c.JSON(http.StatusOK, view)
}

// GetCallBackground serves one template. The address carries its version, so a
// new picture is a new address and the old one can be cached for good.
func (h *Handler) GetCallBackground(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	theme, ok := validTheme(c)
	if !ok {
		return
	}
	var saved CallBackground
	if err := h.service.GetConfigInto(c.Request.Context(), orgID, callBackgroundKey(theme), &saved); err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			c.Status(http.StatusNotFound)
			return
		}
		httpx.Error(c, err, "failed to read the call background")
		return
	}
	raw, err := base64.StdEncoding.DecodeString(saved.Data)
	if err != nil || len(raw) == 0 {
		c.Status(http.StatusNotFound)
		return
	}
	if c.Query("v") == saved.Version {
		c.Header("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		c.Header("Cache-Control", "private, no-cache")
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	c.Data(http.StatusOK, saved.Type, raw)
}

// SetCallBackground puts in or replaces one theme's template.
func (h *Handler) SetCallBackground(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	theme, ok := validTheme(c)
	if !ok {
		return
	}
	// The picture itself is the body, not a data URL inside JSON: base64 would
	// make it a third larger, and every API body is held to 2 MB (limitBodies).
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, MaxCallBackgroundBytes))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "That picture is too large: the most is 2 MB."})
		return
	}
	bg, problem := parseCallBackground(raw)
	if problem != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": problem})
		return
	}
	if err := h.service.SetConfig(c.Request.Context(), orgID, callBackgroundKey(theme), bg, false); err != nil {
		httpx.Error(c, err, "failed to save the call background")
		return
	}
	view, ok := h.callBackgroundsView(c, orgID)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, view)
}

// RemoveCallBackground takes one theme's template away. Removing one that is
// not there is not an error: either way there is none afterwards.
func (h *Handler) RemoveCallBackground(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	theme, ok := validTheme(c)
	if !ok {
		return
	}
	if _, err := h.service.db.Exec(c.Request.Context(),
		`DELETE FROM org_configs WHERE org_id = $1 AND key = $2`, orgID, callBackgroundKey(theme)); err != nil {
		httpx.Error(c, err, "failed to remove the call background")
		return
	}
	view, ok := h.callBackgroundsView(c, orgID)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, view)
}

// parseCallBackground checks a picture is of an accepted kind, size and shape,
// by its bytes rather than by what the upload says it is.
func parseCallBackground(raw []byte) (CallBackground, string) {
	if len(raw) == 0 {
		return CallBackground{}, "Choose a picture to upload."
	}
	if len(raw) > MaxCallBackgroundBytes {
		return CallBackground{}, "That picture is too large: the most is 2 MB."
	}
	kind := http.DetectContentType(raw)
	if !callBackgroundTypes[kind] {
		return CallBackground{}, "The background must be a PNG, JPEG or WebP picture."
	}
	var width, height int
	var ok bool
	if kind == "image/webp" {
		width, height, ok = webpSize(raw)
	} else {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
		width, height, ok = cfg.Width, cfg.Height, err == nil
	}
	if !ok || width <= 0 || height <= 0 {
		return CallBackground{}, "That picture could not be read."
	}
	// 16:9, allowing for a pixel or so of rounding.
	if diff := width*9 - height*16; diff < -width/100 || diff > width/100 {
		return CallBackground{}, "The background must be 16:9, the shape of a video, such as 1920 x 1080."
	}
	if width < minCallBackgroundWidth {
		return CallBackground{}, "The background must be at least 1280 x 720."
	}
	sum := sha256.Sum256(raw)
	return CallBackground{
		Type:    kind,
		Data:    base64.StdEncoding.EncodeToString(raw),
		Version: hex.EncodeToString(sum[:6]),
		Width:   width,
		Height:  height,
	}, ""
}

// webpSize reads a WebP picture's size from its header. The standard library
// cannot decode WebP, and nothing more than the size is needed.
func webpSize(b []byte) (int, int, bool) {
	if len(b) < 30 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return 0, 0, false
	}
	switch string(b[12:16]) {
	case "VP8X":
		w := int(b[24]) | int(b[25])<<8 | int(b[26])<<16
		h := int(b[27]) | int(b[28])<<8 | int(b[29])<<16
		return w + 1, h + 1, true
	case "VP8L":
		if b[20] != 0x2f {
			return 0, 0, false
		}
		bits := binary.LittleEndian.Uint32(b[21:25])
		return int(bits&0x3fff) + 1, int((bits>>14)&0x3fff) + 1, true
	case "VP8 ":
		// A key frame: three bytes of tag, the start code, then the size.
		if b[23] != 0x9d || b[24] != 0x01 || b[25] != 0x2a {
			return 0, 0, false
		}
		w := int(binary.LittleEndian.Uint16(b[26:28]) & 0x3fff)
		h := int(binary.LittleEndian.Uint16(b[28:30]) & 0x3fff)
		return w, h, true
	}
	return 0, 0, false
}
