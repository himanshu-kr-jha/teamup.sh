package files

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/auth"
	"github.com/teamup/teamup/internal/httpx"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func RegisterRoutes(rg *gin.RouterGroup, service *Service) {
	h := NewHandler(service)

	rg.POST("/files/upload-url", h.GetUploadURL)
	rg.POST("/files/multipart/initiate", h.InitiateMultipart)
	rg.POST("/files/multipart/complete", h.CompleteMultipart)
	rg.GET("/files/download-url", h.GetDownloadURL)
	rg.POST("/files/record", h.RecordAttachment)
	rg.GET("/files", h.ListAttachments)
}

// RecordAttachment is what the client calls once its upload has finished.
// Without it an uploaded file left no record and became unreachable.
func (h *Handler) RecordAttachment(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID, err := uuid.Parse(user.OrgID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}
	userID, err := uuid.Parse(user.UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}

	var req struct {
		Category    string `json:"category" binding:"required,oneof=chat tasks recordings avatars exports"`
		RefID       string `json:"refId" binding:"required"`
		ObjectKey   string `json:"objectKey" binding:"required"`
		Filename    string `json:"filename" binding:"required"`
		ContentType string `json:"contentType"`
		Size        int64  `json:"size" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	attachment, err := h.service.RecordAttachment(c.Request.Context(), AttachmentRecord{
		OrgID: orgID, UploadedBy: userID,
		Category: req.Category, RefID: req.RefID, ObjectKey: req.ObjectKey,
		Filename: req.Filename, ContentType: req.ContentType, SizeBytes: req.Size,
	})
	if err != nil {
		httpx.Error(c, err, "failed to record attachment")
		return
	}
	c.JSON(http.StatusCreated, attachment)
}

func (h *Handler) ListAttachments(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID, err := uuid.Parse(user.OrgID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}

	category := c.Query("category")
	refID := c.Query("refId")
	if category == "" || refID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "category and refId are required"})
		return
	}

	// Reading the list is gated by the same parent check as downloading.
	if err := h.service.RequireParentAccess(c.Request.Context(), orgID, user.UserID, category, refID); err != nil {
		httpx.Error(c, err, "access denied")
		return
	}

	attachments, err := h.service.ListAttachments(c.Request.Context(), orgID, category, refID)
	if err != nil {
		httpx.Error(c, err, "failed to list attachments")
		return
	}
	c.JSON(http.StatusOK, gin.H{"attachments": attachments})
}

func (h *Handler) GetUploadURL(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID := user.OrgID

	var req struct {
		Category    string `json:"category" binding:"required,oneof=chat tasks recordings avatars exports"`
		RefID       string `json:"refId" binding:"required"`
		Filename    string `json:"filename" binding:"required"`
		ContentType string `json:"contentType"`
		Size        int64  `json:"size" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.service.GenerateUploadURL(c.Request.Context(), UploadRequest{
		OrgID:       orgID,
		Category:    req.Category,
		RefID:       req.RefID,
		Filename:    req.Filename,
		ContentType: req.ContentType,
		Size:        req.Size,
		UserID:      user.UserID,
	})
	if err != nil {
		httpx.Error(c, err, "failed to generate upload URL")
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *Handler) InitiateMultipart(c *gin.Context) {
	// Similar to GetUploadURL but for large files
	h.GetUploadURL(c)
}

func (h *Handler) CompleteMultipart(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID := user.OrgID

	var req struct {
		ObjectKey string          `json:"objectKey" binding:"required"`
		UploadID  string          `json:"uploadId" binding:"required"`
		Parts     []CompletedPart `json:"parts" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.service.CompleteMultipartUpload(c.Request.Context(), orgID, user.UserID, req.ObjectKey, req.UploadID, req.Parts); err != nil {
		httpx.Error(c, err, "failed to complete upload")
		return
	}

	// Record attachment in DB
	// INSERT INTO attachments ...

	c.JSON(http.StatusOK, gin.H{"message": "upload completed"})
}

func (h *Handler) GetDownloadURL(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID := user.OrgID

	objectKey := c.Query("objectKey")
	filename := c.Query("filename")
	inline := c.Query("inline") == "true"

	url, err := h.service.GenerateDownloadURL(c.Request.Context(), orgID, user.UserID, objectKey, filename, inline)
	if err != nil {
		httpx.Error(c, err, "failed to generate download URL")
		return
	}

	c.JSON(http.StatusOK, gin.H{"downloadUrl": url})
}
