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
	rg.GET("/files/limits", h.UploadLimits)
	rg.GET("/files/download-url", h.GetDownloadURL)
	rg.POST("/files/record", h.RecordAttachment)
	rg.GET("/files", h.ListAttachments)
	rg.DELETE("/files/:id", h.DiscardUpload)

	// Resumable uploads. Every one of these re-runs the same organization and
	// parent checks as issuing a single-PUT URL — a presigned part is the same
	// write capability, and an upload started yesterday must not outlive the
	// caller's access to the channel it is going into.
	rg.POST("/files/multipart", h.InitiateMultipart)
	rg.GET("/files/multipart", h.ListResumableUploads)
	rg.GET("/files/multipart/:id", h.MultipartStatus)
	rg.POST("/files/multipart/:id/parts", h.SignParts)
	rg.POST("/files/multipart/:id/complete", h.CompleteMultipart)
	rg.DELETE("/files/multipart/:id", h.AbortMultipart)
}

// caller pulls the organization and user out of the session, which is the only
// place either is ever taken from — never from the request body.
func caller(c *gin.Context) (orgID, userID uuid.UUID, ok bool) {
	user := auth.GetUserFromContext(c)
	orgID, err := uuid.Parse(user.OrgID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return uuid.Nil, uuid.Nil, false
	}
	userID, err = uuid.Parse(user.UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return uuid.Nil, uuid.Nil, false
	}
	return orgID, userID, true
}

// InitiateMultipart starts a resumable upload and hands back its plan.
func (h *Handler) InitiateMultipart(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
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

	up, err := h.service.InitiateMultipart(c.Request.Context(), UploadRequest{
		OrgID:       orgID.String(),
		UserID:      userID.String(),
		Category:    req.Category,
		RefID:       req.RefID,
		Filename:    req.Filename,
		ContentType: req.ContentType,
		Size:        req.Size,
	})
	if err != nil {
		httpx.Error(c, err, "failed to start upload")
		return
	}
	c.JSON(http.StatusCreated, up)
}

// ListResumableUploads is where resuming starts after a reload: the tab
// remembers nothing, so it asks what it left unfinished here.
func (h *Handler) ListResumableUploads(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	category, refID := c.Query("category"), c.Query("refId")
	if category == "" || refID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "category and refId are required"})
		return
	}

	uploads, err := h.service.ListResumableUploads(c.Request.Context(), orgID, userID, category, refID)
	if err != nil {
		httpx.Error(c, err, "failed to list uploads in progress")
		return
	}
	c.JSON(http.StatusOK, gin.H{"uploads": uploads})
}

// MultipartStatus reports the plan and which parts object storage already
// holds, which is everything a resuming client needs.
func (h *Handler) MultipartStatus(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	status, err := h.service.MultipartStatus(c.Request.Context(), orgID, userID, id)
	if err != nil {
		httpx.Error(c, err, "failed to read upload progress")
		return
	}
	c.JSON(http.StatusOK, status)
}

// SignParts mints URLs for the next batch of parts. An empty partNumbers list
// means "whatever is still missing".
func (h *Handler) SignParts(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req struct {
		PartNumbers []int `json:"partNumbers"`
	}
	// A body is optional here: no body at all means the same as an empty list.
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	parts, err := h.service.SignParts(c.Request.Context(), orgID, userID, id, req.PartNumbers)
	if err != nil {
		httpx.Error(c, err, "failed to sign upload parts")
		return
	}
	c.JSON(http.StatusOK, gin.H{"parts": parts})
}

// CompleteMultipart assembles the object and records the attachment. The
// client sends no part list: the etags come from object storage, so it cannot
// name a part it never uploaded and does not have to have remembered any.
func (h *Handler) CompleteMultipart(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	attachment, err := h.service.CompleteMultipart(c.Request.Context(), orgID, userID, id)
	if err != nil {
		httpx.Error(c, err, "failed to finish upload")
		return
	}
	c.JSON(http.StatusCreated, attachment)
}

// AbortMultipart gives up on an upload and reclaims its parts now, rather than
// leaving them for the hourly sweep.
func (h *Handler) AbortMultipart(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if err := h.service.AbortMultipart(c.Request.Context(), orgID, userID, id); err != nil {
		httpx.Error(c, err, "failed to cancel upload")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "cancelled"})
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

// DiscardUpload throws away an upload that was staged and then removed before
// it was sent, so it does not sit in storage for ever.
func (h *Handler) DiscardUpload(c *gin.Context) {
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
	attachmentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if err := h.service.DiscardUpload(c.Request.Context(), orgID, userID, attachmentID); err != nil {
		httpx.Error(c, err, "failed to discard upload")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "discarded"})
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

// UploadLimits lets the client refuse an over-sized file in the browser rather
// than after the bytes have crossed the network, and pick the same upload path
// the server would. Neither number is the enforcement — that is the signed
// content length on every URL — but the client-side check has to be the same
// arithmetic or a file lands in the gap between them.
func (h *Handler) UploadLimits(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"maxUploadBytes":          h.service.MaxUploadBytes(),
		"multipartThresholdBytes": h.service.MultipartThreshold(),
	})
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
