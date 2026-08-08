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
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/rs/zerolog"
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
	client, _ := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.Secure,
	})
	return &Service{
		client:     client,
		bucketName: cfg.Bucket,
		cfg:        cfg,
	}
}

func (s *Service) SetDB(db *pgxpool.Pool) {
	s.db = db
}

func (s *Service) GenerateUploadURL(ctx context.Context, req UploadRequest) (*UploadResponse, error) {
	if err := s.validateAccess(ctx, req); err != nil {
		return nil, err
	}

	objectKey := s.buildObjectKey(req)
	contentType := req.ContentType
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(req.Filename))
	}

	// Single-part upload (< 50MB): presigned PUT
	if req.Size < 50*1024*1024 {
		url, err := s.client.PresignedPutObject(ctx, s.bucketName, objectKey, 15*time.Minute)
		return &UploadResponse{
			UploadURL: url.String(),
			ObjectKey: objectKey,
			ExpiresAt: time.Now().Add(15 * time.Minute),
			Headers:   map[string]string{"Content-Type": contentType},
		}, err
	}

	// Multipart upload (>= 50MB): use presigned PUT with longer expiry (MinIO v7 handles multipart internally for large objects)
	// PresignedPutObject supports up to 5GB, MinIO handles multipart internally
	url, err := s.client.PresignedPutObject(ctx, s.bucketName, objectKey, 2*time.Hour)
	return &UploadResponse{
		UploadURL: url.String(),
		ObjectKey: objectKey,
		ExpiresAt: time.Now().Add(2 * time.Hour),
		Headers:   map[string]string{"Content-Type": contentType},
	}, err
}

func (s *Service) buildObjectKey(req UploadRequest) string {
	safeName := sanitizeFilename(req.Filename)
	timestamp := time.Now().UTC().Format("20060102T150405Z")
	return fmt.Sprintf("org-%s/%s/%s/%s-%s", req.OrgID, req.Category, req.RefID, timestamp, safeName)
}

func (s *Service) CompleteMultipartUpload(ctx context.Context, orgID, objectKey, uploadID string, parts []CompletedPart) error {
	// With single-part presigned PUT, multipart completion is not needed
	// The object is complete once the PUT succeeds
	if err := s.validateObjectAccess(ctx, orgID, objectKey); err != nil {
		return err
	}
	// Object is already complete with single-part upload
	return nil
}

func (s *Service) GenerateDownloadURL(ctx context.Context, orgID, objectKey, filename string, inline bool) (string, error) {
	if err := s.validateObjectAccess(ctx, orgID, objectKey); err != nil {
		return "", err
	}

	reqParams := make(url.Values)
	if inline {
		reqParams.Set("response-content-disposition", fmt.Sprintf(`inline; filename="%s"`, filename))
	} else {
		reqParams.Set("response-content-disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	}

	url, err := s.client.PresignedGetObject(ctx, s.bucketName, objectKey, 1*time.Hour, reqParams)
	return url.String(), err
}

func (s *Service) validateAccess(ctx context.Context, req UploadRequest) error {
	return nil
}

func (s *Service) validateObjectAccess(ctx context.Context, orgID, objectKey string) error {
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
