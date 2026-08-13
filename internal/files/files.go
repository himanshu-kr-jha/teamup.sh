package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/rs/zerolog"

	apperrors "github.com/teamup/teamup/pkg/errors"
)

type Config struct {
	// Endpoint is where the BROWSER reaches object storage. Presigned URLs are
	// opened by the browser, not by this process, so a Docker-internal
	// hostname here means every upload and every inline image fails: the name
	// does not resolve, and an http:// URL on an https page is blocked as
	// mixed content before it is even tried.
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	Secure    bool

	// InternalEndpoint is where this process reaches object storage itself —
	// bucket checks, stat, delete. It defaults to Endpoint, but pointing it at
	// the container network keeps server-side calls off the public origin and
	// out of the reverse proxy. It is never handed to a browser.
	InternalEndpoint string
	InternalSecure   bool

	// Region has to match what the object store reports, because it is part of
	// the signing key. Setting it here also keeps presigning offline: with no
	// region the client asks the endpoint where the bucket lives before it can
	// sign, which on the public endpoint means a round trip back through the
	// reverse proxy.
	Region string

	// MaxUploadBytes is the largest single upload that will be signed for.
	// Zero means DefaultMaxUploadBytes; there is no "unlimited".
	MaxUploadBytes int64
}

// objectStore is the part of the MinIO API this package calls directly, as
// opposed to signs URLs for. *minio.Client satisfies it.
type objectStore interface {
	StatObject(ctx context.Context, bucket, object string, opts minio.StatObjectOptions) (minio.ObjectInfo, error)
	RemoveObject(ctx context.Context, bucket, object string, opts minio.RemoveObjectOptions) error
	BucketExists(ctx context.Context, bucket string) (bool, error)
}

type Service struct {
	// presigner signs the URLs the browser opens; internal is the same storage
	// reached over the container network. They are separate clients because
	// the host is part of an S3 signature: a URL signed for one host cannot be
	// replayed against the other.
	presigner *minio.Client
	internal  *minio.Client
	store     objectStore

	bucketName     string
	maxUploadBytes int64
	db             *pgxpool.Pool
	cfg            Config
	logger         zerolog.Logger
	initErr        error
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
}

// DefaultMaxUploadBytes is the cap applied when a deployment sets none. It is
// deliberately not "unlimited": with the PUT going straight to object storage,
// no limit means one person dragging in a video fills the volume for everyone.
const DefaultMaxUploadBytes int64 = 100 << 20 // 100 MiB

// DefaultRegion is MinIO's own default. A deployment that sets MINIO_REGION
// must set TEAMUP_MINIO_REGION to the same value or every signature is wrong.
const DefaultRegion = "us-east-1"

// withDefaults fills in what the caller left unset. It deliberately does NOT
// read the environment: cmd/teamup owns every setting and validates it at boot,
// so a second parser here would be a second source of truth — and one that
// silently bypassed the validation. It would also let the ambient environment
// leak into a Config a test constructed by hand.
func (c Config) withDefaults() Config {
	if c.Region == "" {
		c.Region = DefaultRegion
	}
	if c.MaxUploadBytes <= 0 {
		c.MaxUploadBytes = DefaultMaxUploadBytes
	}
	return c
}

func NewService(cfg Config) *Service {
	cfg = cfg.withDefaults()

	// A discarded error here left a nil client that panicked on first use;
	// the service now reports the misconfiguration when it is asked to work.
	presigner, err := newClient(cfg, cfg.Endpoint, cfg.Secure)

	// Server-side calls take the internal address when there is one. Falling
	// back to the public endpoint keeps a single-address deployment working.
	internalEndpoint, internalSecure := cfg.Endpoint, cfg.Secure
	if cfg.InternalEndpoint != "" {
		internalEndpoint, internalSecure = cfg.InternalEndpoint, cfg.InternalSecure
	}
	internal, ierr := newClient(cfg, internalEndpoint, internalSecure)
	if err == nil {
		err = ierr
	}

	s := &Service{
		presigner:      presigner,
		internal:       internal,
		bucketName:     cfg.Bucket,
		maxUploadBytes: cfg.MaxUploadBytes,
		cfg:            cfg,
		initErr:        err,
	}
	// A typed nil in an interface is not nil, so only a real client goes in.
	if internal != nil {
		s.store = internal
	}
	return s
}

func newClient(cfg Config, endpoint string, secure bool) (*minio.Client, error) {
	c, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: secure,
		Region: cfg.Region,
		// Path-style addressing, explicitly. The browser reaches storage
		// through the site's own origin, so the bucket cannot be a DNS label;
		// it has to be the first path segment for nginx to route on, and the
		// signature covers that path.
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

// internalAddr is the address this process itself dials, which is the one worth
// naming when a server-side call fails.
func (s *Service) internalAddr() string {
	if s.internal != nil {
		return s.internal.EndpointURL().String()
	}
	return s.cfg.Endpoint
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

// Ping reports whether object storage can actually serve this instance's
// uploads: the endpoint is reachable, the credentials are accepted, and the
// configured bucket is there. It backs the readiness probe, which is what
// keeps a replica that cannot reach MinIO out of the load balancer instead of
// letting it answer 500 to every upload.
func (s *Service) Ping(ctx context.Context) error {
	if s.initErr != nil {
		return fmt.Errorf("object storage is misconfigured: %w", s.initErr)
	}
	if s.store == nil {
		return errors.New("object storage is not configured")
	}
	exists, err := s.store.BucketExists(ctx, s.bucketName)
	if err != nil {
		return fmt.Errorf("object storage at %s is unreachable: %w", s.internalAddr(), err)
	}
	if !exists {
		return fmt.Errorf("bucket %q does not exist at %s", s.bucketName, s.internalAddr())
	}
	return nil
}

func (s *Service) GenerateUploadURL(ctx context.Context, req UploadRequest) (*UploadResponse, error) {
	if s.presigner == nil {
		return nil, apperrors.New(apperrors.CodeUnavailable, "object storage is not configured")
	}
	if err := s.validateAccess(ctx, req); err != nil {
		return nil, err
	}
	if req.Size <= 0 {
		return nil, apperrors.New(apperrors.CodeBadRequest, "an upload has to declare how large it is")
	}
	// The only chance to refuse a file. The PUT goes straight to object
	// storage, so nothing downstream of this ever sees the bytes.
	if limit := s.uploadLimit(); req.Size > limit {
		return nil, apperrors.New(apperrors.CodeBadRequest, fmt.Sprintf(
			"that file is %s; the largest this server accepts is %s", humanBytes(req.Size), humanBytes(limit)))
	}

	objectKey := s.buildObjectKey(req)
	contentType := req.ContentType
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(req.Filename))
	}

	// One PUT carries the whole object — the cap above is far below the 5 GiB
	// a single PUT can hold. A bigger file just gets longer to finish on a
	// slow connection before the URL expires.
	expiry := 15 * time.Minute
	if req.Size >= 50*1024*1024 {
		expiry = 2 * time.Hour
	}

	// The declared size is signed into the URL, which is what turns the limit
	// from a request the client may ignore into one object storage enforces:
	// a body of any other length fails the signature check and never lands.
	// The browser sets Content-Length itself from the file, so it matches by
	// construction and cannot be forged from script.
	signedHeaders := http.Header{}
	signedHeaders.Set("Content-Length", strconv.FormatInt(req.Size, 10))

	// The error has to be checked before touching the URL: minio-go returns a
	// nil *url.URL alongside it, so calling String() first panics.
	presigned, err := s.presigner.PresignHeader(ctx, http.MethodPut, s.bucketName, objectKey, expiry, nil, signedHeaders)
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

// uploadLimit never returns zero: an unconfigured service is capped, not open.
func (s *Service) uploadLimit() int64 {
	if s.maxUploadBytes > 0 {
		return s.maxUploadBytes
	}
	return DefaultMaxUploadBytes
}

// MaxUploadBytes is what the client is told so it can refuse a file before
// starting an upload that is going to be rejected.
func (s *Service) MaxUploadBytes() int64 { return s.uploadLimit() }

// humanBytes renders a size the way the message needs to read: "100 MB", not
// "104857600 bytes".
func humanBytes(n int64) string {
	for _, u := range []struct {
		size int64
		name string
	}{{1 << 30, "GB"}, {1 << 20, "MB"}, {1 << 10, "KB"}} {
		if n >= u.size {
			v := float64(n) / float64(u.size)
			if v >= 10 {
				return fmt.Sprintf("%.0f %s", v, u.name)
			}
			return strings.TrimSuffix(strconv.FormatFloat(v, 'f', 1, 64), ".0") + " " + u.name
		}
	}
	return fmt.Sprintf("%d bytes", n)
}

func (s *Service) buildObjectKey(req UploadRequest) string {
	safeName := sanitizeFilename(req.Filename)
	timestamp := time.Now().UTC().Format("20060102T150405Z")
	return fmt.Sprintf("org-%s/%s/%s/%s-%s", req.OrgID, req.Category, req.RefID, timestamp, safeName)
}

func (s *Service) GenerateDownloadURL(ctx context.Context, orgID, userID, objectKey, filename string, inline bool) (string, error) {
	if s.presigner == nil {
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

	presigned, err := s.presigner.PresignedGetObject(ctx, s.bucketName, objectKey, 1*time.Hour, reqParams)
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

// ── Attachment records ────────────────────────────────────────────────────
//
// Issuing a presigned URL was only half the job: nothing recorded that the
// upload happened, so a file became unreachable the moment the browser forgot
// its object key, and history never returned attachments at all.

// AttachmentRecord is what the client reports once its upload has completed.
type AttachmentRecord struct {
	OrgID       uuid.UUID `json:"orgId"`
	UploadedBy  uuid.UUID `json:"uploadedBy"`
	Category    string    `json:"category"`
	RefID       string    `json:"refId"`
	ObjectKey   string    `json:"objectKey"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"contentType"`
	SizeBytes   int64     `json:"sizeBytes"`
}

// Attachment is a stored file as it is handed back to a client.
type Attachment struct {
	ID          uuid.UUID `json:"id"`
	OrgID       uuid.UUID `json:"orgId"`
	UploadedBy  uuid.UUID `json:"uploadedBy"`
	Category    string    `json:"category"`
	RefID       string    `json:"refId"`
	ObjectKey   string    `json:"objectKey"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"contentType"`
	SizeBytes   int64     `json:"sizeBytes"`
	CreatedAt   time.Time `json:"createdAt"`
}

// RecordAttachment registers a completed upload. It re-runs the same access
// check as issuing the URL, so a caller cannot register a row against an
// object they were never entitled to write.
func (s *Service) RecordAttachment(ctx context.Context, rec AttachmentRecord) (*Attachment, error) {
	if err := s.validateAccess(ctx, UploadRequest{
		OrgID:    rec.OrgID.String(),
		UserID:   rec.UploadedBy.String(),
		Category: rec.Category,
		RefID:    rec.RefID,
		Filename: rec.Filename,
		Size:     rec.SizeBytes,
	}); err != nil {
		return nil, err
	}
	// The key encodes the organization, category and parent it was issued for.
	// Trusting the body's category/refId instead would let a caller re-file
	// somebody else's object under a parent only they can read.
	keyCategory, keyRef, err := parseObjectKey(rec.ObjectKey, rec.OrgID)
	if err != nil {
		return nil, apperrors.New(apperrors.CodeForbidden, "access denied")
	}
	if keyCategory != rec.Category || keyRef.String() != rec.RefID {
		return nil, apperrors.New(apperrors.CodeForbidden, "access denied")
	}

	// The size in the body is a claim, not a measurement — this server never
	// saw the bytes. Asking storage how big the object actually is makes
	// size_bytes true, and is also the only proof the upload happened at all.
	if s.store == nil {
		return nil, apperrors.New(apperrors.CodeUnavailable, "object storage is not configured")
	}
	info, err := s.store.StatObject(ctx, s.bucketName, rec.ObjectKey, minio.StatObjectOptions{})
	if err != nil {
		s.logger.Warn().Err(err).Str("object", rec.ObjectKey).Msg("recording an object that is not in storage")
		return nil, apperrors.New(apperrors.CodeBadRequest, "that upload is not in storage")
	}
	if limit := s.uploadLimit(); info.Size > limit {
		// Belt and braces: the signed content length should already have kept
		// this out, but if it is here it does not get to stay.
		s.removeObject(ctx, rec.ObjectKey)
		return nil, apperrors.New(apperrors.CodeBadRequest, fmt.Sprintf(
			"that file is %s; the largest this server accepts is %s", humanBytes(info.Size), humanBytes(limit)))
	}
	rec.SizeBytes = info.Size

	var a Attachment
	err = s.db.QueryRow(ctx, `
		INSERT INTO attachments (id, org_id, uploaded_by, category, ref_id, object_key,
		                         filename, content_type, size_bytes, created_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8, NOW())
		ON CONFLICT (object_key) DO UPDATE
		SET filename = EXCLUDED.filename, content_type = EXCLUDED.content_type,
		    size_bytes = EXCLUDED.size_bytes
		-- Only the original uploader may refresh their own record. Without
		-- this, re-recording an object key seen in a listing let anyone rename
		-- another member's file or move it out of the channel it belongs to.
		WHERE attachments.org_id = EXCLUDED.org_id
		  AND attachments.uploaded_by = EXCLUDED.uploaded_by
		  AND attachments.deleted_at IS NULL
		RETURNING id, org_id, uploaded_by, category, ref_id, object_key,
		          filename, content_type, size_bytes, created_at
	`, rec.OrgID, rec.UploadedBy, rec.Category, rec.RefID, rec.ObjectKey,
		rec.Filename, rec.ContentType, rec.SizeBytes).Scan(
		&a.ID, &a.OrgID, &a.UploadedBy, &a.Category, &a.RefID, &a.ObjectKey,
		&a.Filename, &a.ContentType, &a.SizeBytes, &a.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			// The conflicting row belongs to somebody else.
			return nil, apperrors.New(apperrors.CodeForbidden, "access denied")
		}
		return nil, fmt.Errorf("record attachment: %w", err)
	}
	return &a, nil
}

// ListAttachments returns the files hanging off one parent.
func (s *Service) ListAttachments(ctx context.Context, orgID uuid.UUID, category, refID string) ([]Attachment, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, org_id, uploaded_by, category, ref_id, object_key,
		       filename, content_type, size_bytes, created_at
		FROM attachments
		WHERE org_id = $1 AND category = $2 AND ref_id = $3 AND deleted_at IS NULL
		ORDER BY created_at
	`, orgID, category, refID)
	if err != nil {
		return nil, fmt.Errorf("list attachments: %w", err)
	}
	defer rows.Close()

	out := []Attachment{}
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.OrgID, &a.UploadedBy, &a.Category, &a.RefID, &a.ObjectKey,
			&a.Filename, &a.ContentType, &a.SizeBytes, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan attachment: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DiscardUpload removes an upload that was never sent, both the row and the
// object. Only the person who uploaded it can discard it, and only while it is
// still unattached — once it belongs to a message it is part of the
// conversation and this refuses.
func (s *Service) DiscardUpload(ctx context.Context, orgID, userID, attachmentID uuid.UUID) error {
	var objectKey string
	err := s.db.QueryRow(ctx, `
		DELETE FROM attachments
		WHERE id = $1 AND org_id = $2 AND uploaded_by = $3 AND message_id IS NULL
		RETURNING object_key
	`, attachmentID, orgID, userID).Scan(&objectKey)
	if err != nil {
		if err == pgx.ErrNoRows {
			return apperrors.New(apperrors.CodeNotFound, "no unsent upload with that id")
		}
		return fmt.Errorf("discard upload: %w", err)
	}
	s.removeObject(ctx, objectKey)
	return nil
}

// SweepAbandonedUploads deletes chat uploads that were never sent with a
// message. Staging an attachment and then navigating away leaves the bytes in
// object storage and a row pointing at them for ever; nothing else ever
// reclaims them.
//
// Returns how many were removed.
func (s *Service) SweepAbandonedUploads(ctx context.Context, olderThan time.Duration) (int, error) {
	rows, err := s.db.Query(ctx, `
		DELETE FROM attachments
		WHERE category = 'chat'
		  AND message_id IS NULL
		  AND created_at < NOW() - $1::interval
		RETURNING object_key
	`, olderThan.String())
	if err != nil {
		return 0, fmt.Errorf("sweep abandoned uploads: %w", err)
	}
	defer rows.Close()

	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return 0, fmt.Errorf("scan abandoned upload: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, key := range keys {
		s.removeObject(ctx, key)
	}
	return len(keys), nil
}

// removeObject deletes the stored bytes. Best effort: the row is already gone,
// and a leftover object is wasted space rather than a correctness problem.
func (s *Service) removeObject(ctx context.Context, objectKey string) {
	if s.store == nil {
		return
	}
	if err := s.store.RemoveObject(ctx, s.bucketName, objectKey, minio.RemoveObjectOptions{}); err != nil {
		s.logger.Warn().Err(err).Str("object", objectKey).Msg("could not remove stored object")
	}
}

// RequireParentAccess exposes the parent entitlement check for callers that
// need it without issuing a URL, such as listing a parent's attachments.
func (s *Service) RequireParentAccess(ctx context.Context, orgID uuid.UUID, userID, category, refID string) error {
	user, err := uuid.Parse(userID)
	if err != nil {
		return apperrors.New(apperrors.CodeForbidden, "access denied")
	}
	if !allowedCategories[category] {
		return apperrors.New(apperrors.CodeBadRequest, "unknown attachment category")
	}
	ref, err := uuid.Parse(refID)
	if err != nil {
		return apperrors.New(apperrors.CodeBadRequest, "invalid attachment reference")
	}
	if err := s.requireOrgMember(ctx, orgID, user); err != nil {
		return err
	}
	return s.requireParentAccess(ctx, orgID, user, category, ref)
}
