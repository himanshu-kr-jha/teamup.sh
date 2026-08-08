package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/rs/zerolog"

	apperrors "github.com/teamup/teamup/pkg/errors"
)

type Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	Secure    bool
}

type Service struct {
	client     *minio.Client
	bucketName string
	db         *pgxpool.Pool
	cfg        Config
	logger     zerolog.Logger
	initErr    error
}

type UploadRequest struct {
	OrgID       string
	Category    string
	RefID       string
	Filename    string
	ContentType string
	Size        int64
	UserID      string
}

type UploadResponse struct {
	UploadURL string            `json:"uploadUrl"`
	ObjectKey string            `json:"objectKey"`
	ExpiresAt time.Time         `json:"expiresAt"`
	Headers   map[string]string `json:"headers"`
	Multipart *MultipartInfo    `json:"multipart,omitempty"`
}

type MultipartInfo struct {
	UploadID string   `json:"uploadId"`
	PartURLs []string `json:"partUrls"`
	PartSize int64    `json:"partSize"`
}

type CompletedPart struct {
	PartNumber int    `json:"partNumber"`
	ETag       string `json:"etag"`
}

func NewService(cfg Config) *Service {
	// A discarded error here left a nil client that panicked on first use;
	// the service now reports the misconfiguration when it is asked to work.
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.Secure,
	})
	if err != nil {
		client = nil
	}
	return &Service{
		client:     client,
		bucketName: cfg.Bucket,
		cfg:        cfg,
		initErr:    err,
	}
}

// SetLogger wires real logging. Left at the zero value every diagnostic in
// this package was written to a nil writer and silently discarded — including
// the fail-closed paths that swallow an error and deny access.
func (s *Service) SetLogger(l zerolog.Logger) {
	s.logger = l.With().Str("component", "files").Logger()
}

func (s *Service) SetDB(db *pgxpool.Pool) {
	s.db = db
}

func (s *Service) GenerateUploadURL(ctx context.Context, req UploadRequest) (*UploadResponse, error) {
	if s.client == nil {
		return nil, apperrors.New(apperrors.CodeUnavailable, "object storage is not configured")
	}
	if err := s.validateAccess(ctx, req); err != nil {
		return nil, err
	}

	objectKey := s.buildObjectKey(req)
	contentType := req.ContentType
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(req.Filename))
	}

	// Larger objects get a longer window; MinIO handles the multipart split
	// internally behind a presigned PUT.
	expiry := 15 * time.Minute
	if req.Size >= 50*1024*1024 {
		expiry = 2 * time.Hour
	}

	// The error has to be checked before touching the URL: minio-go returns a
	// nil *url.URL alongside it, so calling String() first panics.
	presigned, err := s.client.PresignedPutObject(ctx, s.bucketName, objectKey, expiry)
	if err != nil {
		return nil, fmt.Errorf("presign upload: %w", err)
	}
	return &UploadResponse{
		UploadURL: presigned.String(),
		ObjectKey: objectKey,
		ExpiresAt: time.Now().Add(expiry),
		Headers:   map[string]string{"Content-Type": contentType},
	}, nil
}

func (s *Service) buildObjectKey(req UploadRequest) string {
	safeName := sanitizeFilename(req.Filename)
	timestamp := time.Now().UTC().Format("20060102T150405Z")
	return fmt.Sprintf("org-%s/%s/%s/%s-%s", req.OrgID, req.Category, req.RefID, timestamp, safeName)
}

func (s *Service) CompleteMultipartUpload(ctx context.Context, orgID, userID, objectKey, uploadID string, parts []CompletedPart) error {
	// With single-part presigned PUT, multipart completion is not needed
	// The object is complete once the PUT succeeds
	if err := s.validateObjectAccess(ctx, orgID, userID, objectKey); err != nil {
		return err
	}
	// Object is already complete with single-part upload
	return nil
}

func (s *Service) GenerateDownloadURL(ctx context.Context, orgID, userID, objectKey, filename string, inline bool) (string, error) {
	if s.client == nil {
		return "", apperrors.New(apperrors.CodeUnavailable, "object storage is not configured")
	}
	if err := s.validateObjectAccess(ctx, orgID, userID, objectKey); err != nil {
		return "", err
	}

	reqParams := make(url.Values)
	if inline {
		reqParams.Set("response-content-disposition", fmt.Sprintf(`inline; filename="%s"`, filename))
	} else {
		reqParams.Set("response-content-disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	}

	presigned, err := s.client.PresignedGetObject(ctx, s.bucketName, objectKey, 1*time.Hour, reqParams)
	if err != nil {
		return "", fmt.Errorf("presign download: %w", err)
	}
	return presigned.String(), nil
}

// Presigned URLs bypass this application once issued, so these two checks are
// the only thing standing between an authenticated user and every file in
// every organization. They previously accepted everything unconditionally.

// allowedCategories mirrors the binding tag on the upload handler. Keeping the
// set here as well means the service is safe even if it gains another caller.
var allowedCategories = map[string]bool{
	"chat": true, "tasks": true, "recordings": true, "avatars": true, "exports": true,
}

// validateAccess authorises an upload: the caller must belong to the
// organization the object will be filed under, and must be entitled to the
// parent the attachment hangs off.
func (s *Service) validateAccess(ctx context.Context, req UploadRequest) error {
	orgID, err := uuid.Parse(req.OrgID)
	if err != nil {
		return apperrors.New(apperrors.CodeForbidden, "access denied")
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		return apperrors.New(apperrors.CodeForbidden, "access denied")
	}
	if !allowedCategories[req.Category] {
		return apperrors.New(apperrors.CodeBadRequest, "unknown attachment category")
	}
	refID, err := uuid.Parse(req.RefID)
	if err != nil {
		return apperrors.New(apperrors.CodeBadRequest, "invalid attachment reference")
	}

	if err := s.requireOrgMember(ctx, orgID, userID); err != nil {
		return err
	}
	return s.requireParentAccess(ctx, orgID, userID, req.Category, refID)
}

// validateObjectAccess authorises a download or a multipart completion. The
// object key encodes the organization and parent, so it is re-derived and
// re-checked rather than trusted.
func (s *Service) validateObjectAccess(ctx context.Context, orgID, userID, objectKey string) error {
	org, err := uuid.Parse(orgID)
	if err != nil {
		return apperrors.New(apperrors.CodeForbidden, "access denied")
	}
	user, err := uuid.Parse(userID)
	if err != nil {
		return apperrors.New(apperrors.CodeForbidden, "access denied")
	}

	category, refID, err := parseObjectKey(objectKey, org)
	if err != nil {
		// Deliberately uniform: never disclose whether the object exists or
		// which part of the key was wrong.
		return apperrors.New(apperrors.CodeForbidden, "access denied")
	}
	if err := s.requireOrgMember(ctx, org, user); err != nil {
		return err
	}
	return s.requireParentAccess(ctx, org, user, category, refID)
}

// parseObjectKey validates the key's shape and confirms it belongs to org.
// Any traversal segment or unexpected shape is rejected outright.
func parseObjectKey(objectKey string, org uuid.UUID) (category string, refID uuid.UUID, err error) {
	if objectKey == "" || strings.Contains(objectKey, "..") || strings.Contains(objectKey, "%") {
		return "", uuid.Nil, fmt.Errorf("malformed object key")
	}
	// org-<uuid>/<category>/<refId>/<file>
	parts := strings.Split(objectKey, "/")
	if len(parts) != 4 {
		return "", uuid.Nil, fmt.Errorf("malformed object key")
	}
	if parts[0] != "org-"+org.String() {
		return "", uuid.Nil, fmt.Errorf("object belongs to another organization")
	}
	if !allowedCategories[parts[1]] {
		return "", uuid.Nil, fmt.Errorf("unknown category")
	}
	refID, err = uuid.Parse(parts[2])
	if err != nil {
		return "", uuid.Nil, fmt.Errorf("malformed parent reference")
	}
	return parts[1], refID, nil
}

func (s *Service) requireOrgMember(ctx context.Context, orgID, userID uuid.UUID) error {
	if s.db == nil {
		return apperrors.New(apperrors.CodeInternal, "file access control unavailable")
	}
	var ok bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM users
			WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL AND COALESCE(is_active, true)
		)`, userID, orgID).Scan(&ok)
	if err != nil {
		return fmt.Errorf("check organization membership: %w", err)
	}
	if !ok {
		return apperrors.New(apperrors.CodeForbidden, "access denied")
	}
	return nil
}

// requireParentAccess checks the caller against whatever the attachment hangs
// off, so entitlement follows the parent rather than the file.
func (s *Service) requireParentAccess(ctx context.Context, orgID, userID uuid.UUID, category string, refID uuid.UUID) error {
	var query string
	var args []any

	switch category {
	case "chat":
		// Must be a member of the channel, and the channel must be ours.
		query = `SELECT EXISTS (
			SELECT 1 FROM channel_members cm
			JOIN channels c ON c.id = cm.channel_id
			WHERE cm.channel_id = $1 AND cm.user_id = $2 AND c.org_id = $3 AND c.deleted_at IS NULL)`
		args = []any{refID, userID, orgID}
	case "tasks":
		query = `SELECT EXISTS (
			SELECT 1 FROM tasks WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL)`
		args = []any{refID, orgID}
	case "recordings":
		query = `SELECT EXISTS (
			SELECT 1 FROM calls WHERE id = $1 AND org_id = $2)`
		args = []any{refID, orgID}
	case "avatars":
		// The reference is the user themselves; nobody may write another
		// person's avatar.
		if refID != userID {
			return apperrors.New(apperrors.CodeForbidden, "access denied")
		}
		return nil
	case "exports":
		// Exports are per-user artefacts keyed by the requester.
		if refID != userID {
			return apperrors.New(apperrors.CodeForbidden, "access denied")
		}
		return nil
	default:
		return apperrors.New(apperrors.CodeBadRequest, "unknown attachment category")
	}

	var ok bool
	if err := s.db.QueryRow(ctx, query, args...).Scan(&ok); err != nil {
		return fmt.Errorf("check %s access: %w", category, err)
	}
	if !ok {
		return apperrors.New(apperrors.CodeForbidden, "access denied")
	}
	return nil
}

func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	if len(name) > 255 {
		ext := filepath.Ext(name)
		name = name[:255-len(ext)] + ext
	}
	return name
}

func (s *Service) GenerateThumbnail(ctx context.Context, objectKey string) (string, error) {
	return "", nil
}

func (s *Service) CalculateChecksum(reader io.Reader) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, reader); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
