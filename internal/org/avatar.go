package org

// Profile pictures.
//
// The browser shrinks a picture to a small square before sending it, which
// also drops whatever the camera wrote into it, location included. It is kept
// in the database with the person and served from a fixed address carrying its
// version, so it can be cached for good and a new picture is a new address.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/teamup/teamup/internal/auth"
	"github.com/teamup/teamup/internal/httpx"
)

// MaxAvatarBytes is the largest picture accepted. The page sends 256 pixels
// square, a few tens of kilobytes; this leaves room and no more.
const MaxAvatarBytes = 512 * 1024

var avatarTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true}

func avatarAddress(userID uuid.UUID, data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("/api/users/%s/avatar?v=%s", userID, hex.EncodeToString(sum[:6]))
}

// mayChangeAvatar is the person themselves, or an administrator.
func mayChangeAvatar(c *gin.Context, userID uuid.UUID) bool {
	caller := auth.GetUserFromContext(c)
	if caller.UserID == userID.String() || caller.Can(auth.PermMembers) {
		return true
	}
	c.JSON(http.StatusForbidden, gin.H{"error": "you may only change your own picture"})
	return false
}

// SetAvatar stores a picture and points the person's avatar at it.
func (h *Handler) SetAvatar(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	userID, ok := pathUUID(c, "id")
	if !ok || !mayChangeAvatar(c, userID) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxAvatarBytes*2)
	var req struct {
		Image string `json:"image" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "That picture is too large, or the request is not valid."})
		return
	}
	data, kind, problem := parseAvatar(req.Image)
	if problem != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": problem})
		return
	}
	address := avatarAddress(userID, data)
	ctx := c.Request.Context()
	tx, err := h.service.db.Begin(ctx)
	if err != nil {
		httpx.Error(c, err, "failed to save the picture")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE users SET avatar_url = $3, updated_at = NOW() WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL`, userID, orgID, address)
	if err != nil {
		httpx.Error(c, err, "failed to save the picture")
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_avatars (user_id, content_type, data, updated_at) VALUES ($1, $2, $3, NOW())
		ON CONFLICT (user_id) DO UPDATE SET content_type = EXCLUDED.content_type, data = EXCLUDED.data, updated_at = NOW()
	`, userID, kind, data); err != nil {
		httpx.Error(c, err, "failed to save the picture")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Error(c, err, "failed to save the picture")
		return
	}
	c.JSON(http.StatusOK, gin.H{"avatarUrl": address})
}

// RemoveAvatar goes back to initials.
func (h *Handler) RemoveAvatar(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	userID, ok := pathUUID(c, "id")
	if !ok || !mayChangeAvatar(c, userID) {
		return
	}
	ctx := c.Request.Context()
	tag, err := h.service.db.Exec(ctx, `UPDATE users SET avatar_url = NULL, updated_at = NOW() WHERE id = $1 AND org_id = $2`, userID, orgID)
	if err != nil {
		httpx.Error(c, err, "failed to remove the picture")
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	if _, err := h.service.db.Exec(ctx, `DELETE FROM user_avatars WHERE user_id = $1`, userID); err != nil {
		httpx.Error(c, err, "failed to remove the picture")
		return
	}
	c.Status(http.StatusNoContent)
}

// GetAvatar serves a picture to anybody in the same organization.
func (h *Handler) GetAvatar(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	userID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	var kind string
	var data []byte
	err := h.service.db.QueryRow(c.Request.Context(), `
		SELECT a.content_type, a.data FROM user_avatars a JOIN users u ON u.id = a.user_id
		WHERE a.user_id = $1 AND u.org_id = $2
	`, userID, orgID).Scan(&kind, &data)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.Status(http.StatusNotFound)
			return
		}
		httpx.Error(c, err, "failed to read the picture")
		return
	}
	if c.Query("v") != "" && avatarAddress(userID, data) == c.Request.URL.RequestURI() {
		c.Header("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		c.Header("Cache-Control", "private, no-cache")
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	c.Data(http.StatusOK, kind, data)
}

func parseAvatar(dataURL string) ([]byte, string, string) {
	rest, ok := strings.CutPrefix(dataURL, "data:")
	if !ok {
		return nil, "", "The picture must be an image."
	}
	meta, payload, ok := strings.Cut(rest, ",")
	if !ok || !strings.HasSuffix(meta, ";base64") {
		return nil, "", "The picture must be an image."
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(raw) == 0 {
		return nil, "", "The picture could not be read."
	}
	if len(raw) > MaxAvatarBytes {
		return nil, "", "The picture must be 512 KB or smaller."
	}
	kind := http.DetectContentType(raw)
	if !avatarTypes[kind] {
		return nil, "", "The picture must be a PNG, JPEG or WebP image."
	}
	return raw, kind, ""
}
