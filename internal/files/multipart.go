package files

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/minio/minio-go/v7"

	apperrors "github.com/teamup/teamup/pkg/errors"
)

// Real multipart, and resumability is the whole point of it: a 2 GB screen
// recording that dies at 80% has to carry on, not start again.
//
// The shape of it, and why:
//
//   - Parts are presigned ON DEMAND, in batches, not all up front. Signing
//     every part at initiate is one round trip, but every URL then expires at
//     the same moment, so a 2 GB upload on a slow line dies wholesale an hour
//     in and cannot be resumed with what it holds. Signing one part per round
//     trip is the opposite extreme: 128 extra requests for that same file.
//     A batch of MaxPartURLBatch parts costs a couple of round trips for a
//     typical file and lets a client that has been paused for a day come back
//     and ask for fresh URLs for exactly what it still needs.
//
//   - Which parts have landed is asked of OBJECT STORAGE, never remembered
//     here. A part can arrive and its acknowledgement be lost on the way back;
//     a progress column would then be wrong in the one direction that matters,
//     and re-uploading a part is not free at 16 MiB a time. ListObjectParts is
//     authoritative and costs one call. What this service does persist is the
//     upload's identity and its plan, because those are what a reloaded
//     browser cannot reconstruct.
//
//   - The size guarantee survives. On the single-PUT path the exact
//     Content-Length is signed into the URL, so storage refuses any other body.
//     The equivalent here is that the PLAN is fixed at initiate: the part count
//     and each part's exact byte count are computed server-side from the
//     declared total, and every signed URL binds one part number and one
//     content length. Both were verified against a real MinIO — rewriting
//     partNumber on a signed URL, or sending a different number of bytes,
//     gives 403 SignatureDoesNotMatch. So the most a client can deliver is the
//     total it declared and was authorised for. Completion then re-checks each
//     landed part against the plan, and after assembly the object is STATted:
//     CompleteMultipartUpload reports Size 0 on MinIO, so the measured size can
//     only come from a stat, which is also what RecordAttachment stores.

const (
	// MinPartSize is S3's floor for every part except the last. Observed
	// against MinIO: completing an upload whose non-final part was 1 KiB
	// fails with "Your proposed upload is smaller than the minimum allowed
	// object size." The final part has no floor — a 1 KiB last part completed
	// fine.
	MinPartSize int64 = 5 << 20

	// DefaultPartSize is the part size the plan starts from. Big enough that a
	// 2 GB file is ~128 parts rather than thousands, small enough that losing
	// one to a dropped connection costs seconds, and small enough that the
	// reverse proxy in front of object storage only ever has to pass a body
	// this size.
	DefaultPartSize int64 = 16 << 20

	// MaxParts is the S3 limit on parts in one upload.
	MaxParts = 10000

	// DefaultMultipartThreshold is where a file stops being worth a single
	// PUT. Below it, multipart's initiate + sign + complete round trips cost
	// more than they save; above it, an unresumable single request is the
	// thing that keeps failing.
	DefaultMultipartThreshold int64 = 16 << 20

	// PartURLExpiry has to outlast one slow part, not the whole upload —
	// that is the point of signing them in batches. An hour is ~4.5 kB/s for
	// a 16 MiB part.
	PartURLExpiry = time.Hour

	// MaxPartURLBatch caps how many URLs one request may mint, so a single
	// call cannot be used to mint thousands of write capabilities at once.
	MaxPartURLBatch = 64

	// listPartsPage is the page size for walking what has landed.
	listPartsPage = 1000
)

// multipartStore is the part of the S3 multipart API this package calls
// server-side. *minio.Core satisfies it.
//
// Note the deliberate absence of ListMultipartUploads: in minio-go v7.0.60
// Core.ListMultipartUploads forwards its arguments to listMultipartUploadsQuery
// in the wrong order, so the caller's prefix arrives as the key marker and the
// result is silently empty. Verified against a real MinIO with an upload
// definitely in progress. In-progress uploads are tracked in the database
// instead, which is where they need to be anyway.
type multipartStore interface {
	NewMultipartUpload(ctx context.Context, bucket, object string, opts minio.PutObjectOptions) (string, error)
	ListObjectParts(ctx context.Context, bucket, object, uploadID string, partNumberMarker, maxParts int) (minio.ListObjectPartsResult, error)
	CompleteMultipartUpload(ctx context.Context, bucket, object, uploadID string, parts []minio.CompletePart, opts minio.PutObjectOptions) (minio.UploadInfo, error)
	AbortMultipartUpload(ctx context.Context, bucket, object, uploadID string) error
}

// MultipartUpload is an upload in progress, as handed to a client.
type MultipartUpload struct {
	ID          uuid.UUID `json:"id"`
	OrgID       uuid.UUID `json:"orgId"`
	CreatedBy   uuid.UUID `json:"createdBy"`
	Category    string    `json:"category"`
	RefID       string    `json:"refId"`
	ObjectKey   string    `json:"objectKey"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"contentType"`
	TotalBytes  int64     `json:"totalBytes"`
	PartSize    int64     `json:"partSize"`
	PartCount   int       `json:"partCount"`
	CreatedAt   time.Time `json:"createdAt"`

	// storageUploadID is the opaque id object storage gave us. It never
	// leaves this process: it is an S3-level capability, and the client
	// addresses its upload by our own row id instead.
	storageUploadID string
}

// PartURL is one presigned part upload.
type PartURL struct {
	PartNumber int    `json:"partNumber"`
	URL        string `json:"url"`
	// ContentLength is signed into the URL. The browser sets it from the slice
	// it sends, so it matches by construction and cannot be forged from script.
	ContentLength int64     `json:"contentLength"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

// UploadedPart is a part object storage says it already holds.
type UploadedPart struct {
	PartNumber int    `json:"partNumber"`
	Size       int64  `json:"size"`
	ETag       string `json:"etag"`
}

// MultipartStatus is what a resuming client needs: the plan, and what is
// already done.
type MultipartStatus struct {
	Upload   MultipartUpload `json:"upload"`
	Uploaded []UploadedPart  `json:"uploaded"`
}

// planParts turns a declared total into a fixed part plan. It is pure and
// deterministic, so the same upload replans identically after a restart.
func planParts(total int64) (partSize int64, partCount int, err error) {
	if total <= 0 {
		return 0, 0, apperrors.New(apperrors.CodeBadRequest, "an upload has to declare how large it is")
	}
	partSize = DefaultPartSize
	// Grow the part rather than the count: 10,000 is a hard S3 limit, and
	// doubling keeps every part a whole number of the base size.
	for (total+partSize-1)/partSize > int64(MaxParts) {
		partSize *= 2
	}
	return partSize, int((total + partSize - 1) / partSize), nil
}

// partLength is the exact number of bytes part n carries. Only the final part
// is short, which is the one part S3 imposes no minimum on.
func partLength(total, partSize int64, partCount, n int) int64 {
	if n < 1 || n > partCount {
		return 0
	}
	if n < partCount {
		return partSize
	}
	return total - int64(partCount-1)*partSize
}

// MultipartThreshold is the size at which a file stops going up in one PUT.
// It never exceeds the ceiling: a threshold above the largest allowed file
// would mean nothing ever qualified for multipart.
func (s *Service) MultipartThreshold() int64 {
	threshold := s.multipartThreshold
	if threshold <= 0 {
		threshold = DefaultMultipartThreshold
	}
	if limit := s.uploadLimit(); threshold > limit {
		return limit
	}
	return threshold
}

// InitiateMultipart starts a resumable upload and records its plan.
//
// It applies exactly the same authorisation as issuing a single-PUT URL —
// organization membership and access to the parent the attachment hangs off —
// because a presigned part URL is the same kind of write capability.
func (s *Service) InitiateMultipart(ctx context.Context, req UploadRequest) (*MultipartUpload, error) {
	if s.presigner == nil || s.multipart == nil {
		return nil, apperrors.New(apperrors.CodeUnavailable, "object storage is not configured")
	}
	if err := s.validateAccess(ctx, req); err != nil {
		return nil, err
	}
	if req.Size <= 0 {
		return nil, apperrors.New(apperrors.CodeBadRequest, "an upload has to declare how large it is")
	}
	if limit := s.uploadLimit(); req.Size > limit {
		return nil, apperrors.New(apperrors.CodeBadRequest, fmt.Sprintf(
			"that file is %s; the largest this server accepts is %s", humanBytes(req.Size), humanBytes(limit)))
	}
	// Deliberately no lower bound. Multipart is always correct; the threshold
	// is an optimisation the single-PUT path enforces, not a rule about what
	// may be uploaded this way. Refusing small files here would only add a
	// failure mode for a client that guessed the boundary differently.

	partSize, partCount, err := planParts(req.Size)
	if err != nil {
		return nil, err
	}

	objectKey := s.buildObjectKey(req)
	contentType := req.ContentType
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(req.Filename))
	}

	storageUploadID, err := s.multipart.NewMultipartUpload(ctx, s.bucketName, objectKey,
		minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return nil, fmt.Errorf("start multipart upload: %w", err)
	}

	up := MultipartUpload{
		OrgID:           uuid.MustParse(req.OrgID),
		CreatedBy:       uuid.MustParse(req.UserID),
		Category:        req.Category,
		RefID:           req.RefID,
		ObjectKey:       objectKey,
		Filename:        sanitizeFilename(req.Filename),
		ContentType:     contentType,
		TotalBytes:      req.Size,
		PartSize:        partSize,
		PartCount:       partCount,
		storageUploadID: storageUploadID,
	}
	err = s.db.QueryRow(ctx, `
		INSERT INTO multipart_uploads (org_id, created_by, category, ref_id, object_key,
		                               storage_upload_id, filename, content_type,
		                               total_bytes, part_size, part_count)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING id, created_at
	`, up.OrgID, up.CreatedBy, up.Category, up.RefID, up.ObjectKey, up.storageUploadID,
		up.Filename, up.ContentType, up.TotalBytes, up.PartSize, up.PartCount).
		Scan(&up.ID, &up.CreatedAt)
	if err != nil {
		// Without the row nothing can ever finish or abort this upload, so it
		// would sit in storage until the bucket's own lifecycle rule noticed.
		s.abortQuietly(ctx, objectKey, storageUploadID)
		return nil, fmt.Errorf("record multipart upload: %w", err)
	}
	return &up, nil
}

// loadUpload fetches an in-progress upload and re-authorises the caller.
//
// The entitlement is re-checked on every call rather than trusted from
// initiate: somebody removed from a channel between starting an upload and
// finishing it must not be able to carry on writing into it.
func (s *Service) loadUpload(ctx context.Context, orgID, userID, id uuid.UUID) (*MultipartUpload, error) {
	if s.db == nil {
		return nil, apperrors.New(apperrors.CodeInternal, "file access control unavailable")
	}
	var up MultipartUpload
	err := s.db.QueryRow(ctx, `
		SELECT id, org_id, created_by, category, ref_id, object_key, storage_upload_id,
		       filename, content_type, total_bytes, part_size, part_count, created_at
		FROM multipart_uploads
		WHERE id = $1 AND org_id = $2 AND created_by = $3 AND completed_at IS NULL
	`, id, orgID, userID).Scan(&up.ID, &up.OrgID, &up.CreatedBy, &up.Category, &up.RefID,
		&up.ObjectKey, &up.storageUploadID, &up.Filename, &up.ContentType,
		&up.TotalBytes, &up.PartSize, &up.PartCount, &up.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Uniform: an upload belonging to another organization, to another
			// member, or to nobody all read the same from outside.
			return nil, apperrors.New(apperrors.CodeNotFound, "no upload in progress with that id")
		}
		return nil, fmt.Errorf("load multipart upload: %w", err)
	}
	if err := s.validateObjectAccess(ctx, up.OrgID.String(), userID.String(), up.ObjectKey); err != nil {
		return nil, err
	}
	return &up, nil
}

// SignParts mints presigned URLs for specific parts. An empty list means
// "whatever is still missing", which is exactly what a resuming client wants
// and the only thing it needs to know after a reload.
func (s *Service) SignParts(ctx context.Context, orgID, userID, id uuid.UUID, partNumbers []int) ([]PartURL, error) {
	if s.presigner == nil || s.multipart == nil {
		return nil, apperrors.New(apperrors.CodeUnavailable, "object storage is not configured")
	}
	up, err := s.loadUpload(ctx, orgID, userID, id)
	if err != nil {
		return nil, err
	}

	if len(partNumbers) == 0 {
		landed, err := s.landedParts(ctx, up)
		if err != nil {
			return nil, err
		}
		have := make(map[int]bool, len(landed))
		for _, p := range landed {
			have[p.PartNumber] = true
		}
		for n := 1; n <= up.PartCount && len(partNumbers) < MaxPartURLBatch; n++ {
			if !have[n] {
				partNumbers = append(partNumbers, n)
			}
		}
	}
	if len(partNumbers) > MaxPartURLBatch {
		return nil, apperrors.New(apperrors.CodeBadRequest, fmt.Sprintf(
			"ask for at most %d part URLs at a time", MaxPartURLBatch))
	}

	seen := make(map[int]bool, len(partNumbers))
	out := make([]PartURL, 0, len(partNumbers))
	for _, n := range partNumbers {
		// The plan is the size guarantee. A part number outside it would be
		// bytes nobody accounted for, so it never gets signed.
		if n < 1 || n > up.PartCount {
			return nil, apperrors.New(apperrors.CodeBadRequest, fmt.Sprintf(
				"part %d is not in this upload, which has %d parts", n, up.PartCount))
		}
		if seen[n] {
			continue
		}
		seen[n] = true

		length := partLength(up.TotalBytes, up.PartSize, up.PartCount, n)
		q := url.Values{}
		q.Set("uploadId", up.storageUploadID)
		q.Set("partNumber", strconv.Itoa(n))
		// Both the part number and the length are inside the signature —
		// verified against a real MinIO, which answers 403
		// SignatureDoesNotMatch to a rewritten partNumber and to a body of any
		// other length.
		signedHeaders := http.Header{}
		signedHeaders.Set("Content-Length", strconv.FormatInt(length, 10))

		presigned, err := s.presigner.PresignHeader(ctx, http.MethodPut, s.bucketName, up.ObjectKey,
			PartURLExpiry, q, signedHeaders)
		if err != nil {
			return nil, fmt.Errorf("presign part %d: %w", n, err)
		}
		out = append(out, PartURL{
			PartNumber:    n,
			URL:           presigned.String(),
			ContentLength: length,
			ExpiresAt:     time.Now().Add(PartURLExpiry),
		})
	}
	return out, nil
}

// MultipartStatus reports the plan and what object storage already holds. This
// is what makes a reload survivable: the tab remembers nothing, and everything
// it needs is here.
func (s *Service) MultipartStatus(ctx context.Context, orgID, userID, id uuid.UUID) (*MultipartStatus, error) {
	up, err := s.loadUpload(ctx, orgID, userID, id)
	if err != nil {
		return nil, err
	}
	landed, err := s.landedParts(ctx, up)
	if err != nil {
		return nil, err
	}
	return &MultipartStatus{Upload: *up, Uploaded: landed}, nil
}

// landedParts asks object storage which parts it holds, paging through them.
func (s *Service) landedParts(ctx context.Context, up *MultipartUpload) ([]UploadedPart, error) {
	if s.multipart == nil {
		return nil, apperrors.New(apperrors.CodeUnavailable, "object storage is not configured")
	}
	var out []UploadedPart
	marker := 0
	for {
		res, err := s.multipart.ListObjectParts(ctx, s.bucketName, up.ObjectKey, up.storageUploadID, marker, listPartsPage)
		if err != nil {
			if isNoSuchUpload(err) {
				// Storage has forgotten it — an expired or externally aborted
				// upload. Say so plainly rather than reporting "no parts yet"
				// and letting the client upload into nothing.
				return nil, apperrors.New(apperrors.CodeNotFound, "that upload is no longer in progress; start it again")
			}
			return nil, fmt.Errorf("list uploaded parts: %w", err)
		}
		for _, p := range res.ObjectParts {
			out = append(out, UploadedPart{
				PartNumber: p.PartNumber,
				Size:       p.Size,
				// MinIO returns the etag quoted; the completion request must
				// not double-quote it.
				ETag: strings.Trim(p.ETag, `"`),
			})
		}
		if !res.IsTruncated || res.NextPartNumberMarker <= marker {
			break
		}
		marker = res.NextPartNumberMarker
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PartNumber < out[j].PartNumber })
	return out, nil
}

// CompleteMultipart assembles the object and records the attachment.
//
// The client sends no part list. The etags come from object storage, so a
// client cannot name a part it did not upload, and a browser that reloaded
// mid-upload does not have to have remembered anything.
func (s *Service) CompleteMultipart(ctx context.Context, orgID, userID, id uuid.UUID) (*Attachment, error) {
	up, err := s.loadUpload(ctx, orgID, userID, id)
	if err != nil {
		return nil, err
	}
	landed, err := s.landedParts(ctx, up)
	if err != nil {
		return nil, err
	}

	byNumber := make(map[int]UploadedPart, len(landed))
	for _, p := range landed {
		byNumber[p.PartNumber] = p
	}

	parts := make([]minio.CompletePart, 0, up.PartCount)
	for n := 1; n <= up.PartCount; n++ {
		p, ok := byNumber[n]
		if !ok {
			// Not an error to recover from by throwing the upload away — the
			// client can still send this part. Leave everything where it is.
			return nil, apperrors.New(apperrors.CodeBadRequest, fmt.Sprintf(
				"part %d of %d has not been uploaded yet", n, up.PartCount))
		}
		if want := partLength(up.TotalBytes, up.PartSize, up.PartCount, n); p.Size != want {
			// Unreachable through a presigned URL, which binds the length.
			// Checked anyway: it is the only thing standing between the plan
			// and the object's real size if it ever became reachable. The part
			// can be re-uploaded over itself, so nothing is discarded.
			return nil, apperrors.New(apperrors.CodeBadRequest, fmt.Sprintf(
				"part %d is %d bytes but was signed for %d", n, p.Size, want))
		}
		parts = append(parts, minio.CompletePart{PartNumber: n, ETag: p.ETag})
	}
	if len(landed) > up.PartCount {
		return nil, apperrors.New(apperrors.CodeBadRequest, "this upload holds parts that are not in its plan")
	}

	if _, err := s.multipart.CompleteMultipartUpload(ctx, s.bucketName, up.ObjectKey, up.storageUploadID,
		parts, minio.PutObjectOptions{}); err != nil {
		return nil, fmt.Errorf("assemble multipart upload: %w", err)
	}

	// CompleteMultipartUpload reports Size 0 on MinIO — observed — so the
	// assembled size can only come from a stat. Anything other than the total
	// that was declared, planned and authorised does not get to stay.
	if s.store == nil {
		return nil, apperrors.New(apperrors.CodeUnavailable, "object storage is not configured")
	}
	info, err := s.store.StatObject(ctx, s.bucketName, up.ObjectKey, minio.StatObjectOptions{})
	if err != nil {
		s.logger.Warn().Err(err).Str("object", up.ObjectKey).Msg("assembled object is not in storage")
		return nil, apperrors.New(apperrors.CodeInternal, "that upload could not be assembled")
	}
	if info.Size != up.TotalBytes {
		s.logger.Warn().Int64("measured", info.Size).Int64("declared", up.TotalBytes).
			Str("object", up.ObjectKey).Msg("assembled object is not the size that was planned")
		s.removeObject(ctx, up.ObjectKey)
		s.forgetUpload(ctx, up.ID)
		return nil, apperrors.New(apperrors.CodeBadRequest, "that upload did not assemble to the size it declared")
	}

	// One writer of attachment rows, so the measured-size and
	// entitlement-re-checked properties hold identically on both paths. It
	// stats a second time; one HEAD against the internal endpoint is a fair
	// price for not having a second way to create an attachment.
	att, err := s.RecordAttachment(ctx, AttachmentRecord{
		OrgID:       up.OrgID,
		UploadedBy:  up.CreatedBy,
		Category:    up.Category,
		RefID:       up.RefID,
		ObjectKey:   up.ObjectKey,
		Filename:    up.Filename,
		ContentType: up.ContentType,
		SizeBytes:   info.Size,
	})
	if err != nil {
		// An object with no row is unreachable and would never be reclaimed.
		s.removeObject(ctx, up.ObjectKey)
		s.forgetUpload(ctx, up.ID)
		return nil, err
	}

	if _, err := s.db.Exec(ctx,
		`UPDATE multipart_uploads SET completed_at = NOW() WHERE id = $1`, up.ID); err != nil {
		// The attachment exists and is correct; a stale row only means the
		// sweep tidies it later.
		s.logger.Warn().Err(err).Str("upload", up.ID.String()).Msg("could not mark multipart upload complete")
	}
	return att, nil
}

// AbortMultipart gives up on an upload and reclaims its parts.
func (s *Service) AbortMultipart(ctx context.Context, orgID, userID, id uuid.UUID) error {
	up, err := s.loadUpload(ctx, orgID, userID, id)
	if err != nil {
		return err
	}
	s.abortQuietly(ctx, up.ObjectKey, up.storageUploadID)
	return s.forgetUpload(ctx, up.ID)
}

// ListResumableUploads offers somebody their own interrupted uploads back.
// After a reload the tab knows nothing, so this is where resuming starts.
func (s *Service) ListResumableUploads(ctx context.Context, orgID, userID uuid.UUID, category, refID string) ([]MultipartUpload, error) {
	if err := s.RequireParentAccess(ctx, orgID, userID.String(), category, refID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, org_id, created_by, category, ref_id, object_key, filename, content_type,
		       total_bytes, part_size, part_count, created_at
		FROM multipart_uploads
		WHERE org_id = $1 AND created_by = $2 AND category = $3 AND ref_id = $4
		  AND completed_at IS NULL
		ORDER BY created_at
	`, orgID, userID, category, refID)
	if err != nil {
		return nil, fmt.Errorf("list resumable uploads: %w", err)
	}
	defer rows.Close()

	out := []MultipartUpload{}
	for rows.Next() {
		var up MultipartUpload
		if err := rows.Scan(&up.ID, &up.OrgID, &up.CreatedBy, &up.Category, &up.RefID,
			&up.ObjectKey, &up.Filename, &up.ContentType, &up.TotalBytes,
			&up.PartSize, &up.PartCount, &up.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan resumable upload: %w", err)
		}
		out = append(out, up)
	}
	return out, rows.Err()
}

// SweepAbandonedMultipartUploads reclaims uploads nobody came back to.
//
// An interrupted multipart upload holds its parts indefinitely: they are not
// an object, so nothing that walks the bucket sees them, and no lifecycle rule
// is configured by default. This is the only thing that frees them.
//
// The retention is the same as for staged-but-never-sent single uploads, which
// is what makes resuming-tomorrow work: a day is long enough to come back to a
// paused 2 GB upload.
//
// Returns how many rows were reclaimed.
func (s *Service) SweepAbandonedMultipartUploads(ctx context.Context, olderThan time.Duration) (int, error) {
	if s.db == nil {
		return 0, nil
	}
	if s.multipart == nil {
		// Deleting the rows without being able to abort the uploads would
		// orphan the parts with nothing left pointing at them.
		return 0, nil
	}
	rows, err := s.db.Query(ctx, `
		DELETE FROM multipart_uploads
		WHERE created_at < NOW() - $1::interval
		RETURNING object_key, storage_upload_id, completed_at IS NULL
	`, olderThan.String())
	if err != nil {
		return 0, fmt.Errorf("sweep abandoned multipart uploads: %w", err)
	}
	defer rows.Close()

	type leftover struct {
		objectKey, uploadID string
		unfinished          bool
	}
	var found []leftover
	for rows.Next() {
		var l leftover
		if err := rows.Scan(&l.objectKey, &l.uploadID, &l.unfinished); err != nil {
			return 0, fmt.Errorf("scan abandoned multipart upload: %w", err)
		}
		found = append(found, l)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, l := range found {
		// A finished upload's parts are already gone; its object belongs to an
		// attachment row and must not be touched.
		if l.unfinished {
			s.abortQuietly(ctx, l.objectKey, l.uploadID)
		}
	}
	return len(found), nil
}

// abortQuietly reclaims parts, best effort. An upload storage has already
// forgotten is the outcome we wanted anyway.
func (s *Service) abortQuietly(ctx context.Context, objectKey, storageUploadID string) {
	if s.multipart == nil {
		return
	}
	if err := s.multipart.AbortMultipartUpload(ctx, s.bucketName, objectKey, storageUploadID); err != nil && !isNoSuchUpload(err) {
		s.logger.Warn().Err(err).Str("object", objectKey).Msg("could not abort multipart upload")
	}
}

func (s *Service) forgetUpload(ctx context.Context, id uuid.UUID) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM multipart_uploads WHERE id = $1`, id); err != nil {
		return fmt.Errorf("forget multipart upload: %w", err)
	}
	return nil
}

// isNoSuchUpload recognises storage having forgotten an upload — expired,
// aborted elsewhere, or already completed.
func isNoSuchUpload(err error) bool {
	var resp minio.ErrorResponse
	if errors.As(err, &resp) {
		return resp.Code == "NoSuchUpload"
	}
	return false
}
