package org

// The organization's background pictures for calls (the owner's request of 19
// November 2026): an administrator puts in a set, and those, beside the blurs
// and the company templates, are the only backgrounds anybody can choose.
//
// Each picture is brought to 16:9 from its middle when it is put in, at most
// 1920 x 1080, and kept as a JPEG with a small copy beside it for the menu. So
// whatever was uploaded, a camera only ever loads a picture of the size it
// needs, and the menu only small ones.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers WebP for image.Decode

	"github.com/teamup/teamup/internal/auth"
	"github.com/teamup/teamup/internal/httpx"
)

// MaxCallPictures is how many pictures an organization may have: enough to
// choose from, few enough that the menu stays a menu.
const MaxCallPictures = 24

// MaxCallPictureName is the longest name a picture may have.
const MaxCallPictureName = 60

// What a picture is brought to: the largest a camera sends, and the menu's tile.
const (
	callPictureWidth = 1920
	callThumbWidth   = 384
)

// maxCallPicturePixels refuses a picture that would take too much memory to
// read: its size is read from its header before any of it is decoded.
const maxCallPicturePixels = 40_000_000

// CallPictureView is one picture as the page sees it: its addresses carry its
// version, so they are fetched once and cached.
type CallPictureView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	ThumbURL string `json:"thumbUrl"`
}

func callPictureURL(id uuid.UUID, version string) string {
	return "/api/org/call-pictures/" + id.String() + "?v=" + version
}

func callThumbURL(id uuid.UUID, version string) string {
	return "/api/org/call-pictures/" + id.String() + "/thumb?v=" + version
}

// callPictures lists the organization's pictures in their order.
func (s *Service) callPictures(ctx context.Context, orgID uuid.UUID) ([]CallPictureView, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, version FROM org_call_pictures
		WHERE org_id = $1 ORDER BY position, created_at, id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []CallPictureView{}
	for rows.Next() {
		var id uuid.UUID
		var name, version string
		if err := rows.Scan(&id, &name, &version); err != nil {
			return nil, err
		}
		list = append(list, CallPictureView{ID: id.String(), Name: name, URL: callPictureURL(id, version), ThumbURL: callThumbURL(id, version)})
	}
	return list, rows.Err()
}

// respondWithBackgrounds answers a change with everything there is now, so the
// page has one list to keep, whichever was changed.
func (h *Handler) respondWithBackgrounds(c *gin.Context, orgID uuid.UUID, status int) {
	view, ok := h.callBackgroundsView(c, orgID)
	if !ok {
		return
	}
	c.JSON(status, view)
}

// AddCallPicture puts one more picture in. The picture itself is the body, as
// for a template; its name is ?name=.
func (h *Handler) AddCallPicture(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, MaxCallBackgroundBytes))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "That picture is too large: the most is 2 MB."})
		return
	}
	pic, problem := prepareCallPicture(raw)
	if problem != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": problem})
		return
	}
	name := cleanPictureName(c.Query("name"))
	if name == "" {
		name = "Background"
	}
	var createdBy *uuid.UUID
	if id, err := uuid.Parse(auth.GetUserFromContext(c).UserID); err == nil {
		createdBy = &id
	}
	ctx := c.Request.Context()
	tx, err := h.service.db.Begin(ctx)
	if err != nil {
		httpx.Error(c, err, "failed to save the picture")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// One upload at a time per organization, so two at once cannot both be the
	// one that goes over the limit.
	if _, err := tx.Exec(ctx, `SELECT id FROM organizations WHERE id = $1 FOR UPDATE`, orgID); err != nil {
		httpx.Error(c, err, "failed to save the picture")
		return
	}
	var count, last int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*), COALESCE(MAX(position), 0) FROM org_call_pictures WHERE org_id = $1`, orgID).Scan(&count, &last); err != nil {
		httpx.Error(c, err, "failed to save the picture")
		return
	}
	if count >= MaxCallPictures {
		c.JSON(http.StatusBadRequest, gin.H{"error": "There can be at most 24 background pictures. Remove one to add another."})
		return
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO org_call_pictures (id, org_id, name, position, content_type, data, thumb, width, height, version, created_by)
		VALUES ($1, $2, $3, $4, 'image/jpeg', $5, $6, $7, $8, $9, $10)`,
		uuid.New(), orgID, name, last+1, pic.data, pic.thumb, pic.width, pic.height, pic.version, createdBy); err != nil {
		httpx.Error(c, err, "failed to save the picture")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Error(c, err, "failed to save the picture")
		return
	}
	h.respondWithBackgrounds(c, orgID, http.StatusCreated)
}

// GetCallPicture serves one picture, or with /thumb its small copy, to anybody
// in the organization: everybody may wear it.
func (h *Handler) GetCallPicture(c *gin.Context) {
	h.serveCallPicture(c, "data")
}

func (h *Handler) GetCallPictureThumb(c *gin.Context) {
	h.serveCallPicture(c, "thumb")
}

func (h *Handler) serveCallPicture(c *gin.Context, column string) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	var data []byte
	var kind, version string
	// column is one of two fixed names, never anything from the request.
	err = h.service.db.QueryRow(c.Request.Context(),
		`SELECT `+column+`, content_type, version FROM org_call_pictures WHERE id = $1 AND org_id = $2`, id, orgID).
		Scan(&data, &kind, &version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.Status(http.StatusNotFound)
			return
		}
		httpx.Error(c, err, "failed to read the picture")
		return
	}
	if c.Query("v") == version {
		c.Header("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		c.Header("Cache-Control", "private, no-cache")
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	c.Data(http.StatusOK, kind, data)
}

// RenameCallPicture changes what a picture is called in the menu.
func (h *Handler) RenameCallPicture(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	id, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	name := cleanPictureName(req.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Give the picture a name."})
		return
	}
	tag, err := h.service.db.Exec(c.Request.Context(),
		`UPDATE org_call_pictures SET name = $1 WHERE id = $2 AND org_id = $3`, name, id, orgID)
	if err != nil {
		httpx.Error(c, err, "failed to rename the picture")
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "There is no such picture."})
		return
	}
	h.respondWithBackgrounds(c, orgID, http.StatusOK)
}

// OrderCallPictures puts the pictures in the order given. Any the list leaves
// out, such as one added meanwhile, keep their place after those it names.
func (h *Handler) OrderCallPictures(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) > MaxCallPictures*2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	ids := make([]uuid.UUID, 0, len(req.IDs))
	for _, raw := range req.IDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		ids = append(ids, id)
	}
	// Those named first, in the order named; the rest after, in the order they were.
	if _, err := h.service.db.Exec(c.Request.Context(), `
		UPDATE org_call_pictures p SET position = o.n
		FROM (
			SELECT id, ROW_NUMBER() OVER (ORDER BY named.ord NULLS LAST, position, created_at, id) AS n
			FROM org_call_pictures
			LEFT JOIN UNNEST($2::uuid[]) WITH ORDINALITY AS named(pid, ord) ON named.pid = org_call_pictures.id
			WHERE org_id = $1
		) o
		WHERE p.id = o.id AND p.org_id = $1`, orgID, ids); err != nil {
		httpx.Error(c, err, "failed to order the pictures")
		return
	}
	h.respondWithBackgrounds(c, orgID, http.StatusOK)
}

// RemoveCallPicture takes one picture away. Anybody who had it on goes back to
// no background when their page next learns it is gone. Removing one that is
// not there is not an error: either way it is gone afterwards.
func (h *Handler) RemoveCallPicture(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	id, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	if _, err := h.service.db.Exec(c.Request.Context(),
		`DELETE FROM org_call_pictures WHERE id = $1 AND org_id = $2`, id, orgID); err != nil {
		httpx.Error(c, err, "failed to remove the picture")
		return
	}
	h.respondWithBackgrounds(c, orgID, http.StatusOK)
}

// cleanPictureName keeps a name to one short line of printable text.
func cleanPictureName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return ' '
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if runes := []rune(name); len(runes) > MaxCallPictureName {
		name = strings.TrimSpace(string(runes[:MaxCallPictureName]))
	}
	return name
}

// preparedPicture is a picture as it is kept.
type preparedPicture struct {
	data, thumb   []byte
	width, height int
	version       string
}

// prepareCallPicture checks a picture is of an accepted kind and size, by its
// bytes rather than by what the upload says it is, and brings it to 16:9 from
// its middle, at most 1920 x 1080, with a small copy for the menu.
func prepareCallPicture(raw []byte) (preparedPicture, string) {
	if len(raw) == 0 {
		return preparedPicture{}, "Choose a picture to upload."
	}
	if len(raw) > MaxCallBackgroundBytes {
		return preparedPicture{}, "That picture is too large: the most is 2 MB."
	}
	if !callBackgroundTypes[http.DetectContentType(raw)] {
		return preparedPicture{}, "The background must be a PNG, JPEG or WebP picture."
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return preparedPicture{}, "That picture could not be read."
	}
	if cfg.Width*cfg.Height > maxCallPicturePixels {
		return preparedPicture{}, "That picture is too large: the most is 40 megapixels."
	}
	crop := coverCrop(cfg.Width, cfg.Height)
	if crop.Dx() < minCallBackgroundWidth {
		return preparedPicture{}, "The background must be at least 1280 x 720."
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return preparedPicture{}, "That picture could not be read."
	}
	width := min(crop.Dx(), callPictureWidth)
	data, err := scaledJPEG(src, crop.Add(src.Bounds().Min), width, 88)
	if err != nil {
		return preparedPicture{}, "That picture could not be read."
	}
	thumb, err := scaledJPEG(src, crop.Add(src.Bounds().Min), callThumbWidth, 80)
	if err != nil {
		return preparedPicture{}, "That picture could not be read."
	}
	sum := sha256.Sum256(data)
	return preparedPicture{data: data, thumb: thumb, width: width, height: width * 9 / 16, version: hex.EncodeToString(sum[:6])}, ""
}

// coverCrop is the largest 16:9 part of a picture of the given size, from its middle.
func coverCrop(w, h int) image.Rectangle {
	cw, ch := w, w*9/16
	if ch > h {
		cw, ch = h*16/9, h
	}
	x, y := (w-cw)/2, (h-ch)/2
	return image.Rect(x, y, x+cw, y+ch)
}

// scaledJPEG draws part of a picture at the given width, 16:9, over a dark
// ground for any part that is see-through, as a JPEG.
func scaledJPEG(src image.Image, part image.Rectangle, width, quality int) ([]byte, error) {
	height := width * 9 / 16
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.RGBA{R: 15, G: 23, B: 42, A: 255}}, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, part, draw.Over, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
