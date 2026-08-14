package files

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"

	"github.com/teamup/teamup/internal/testsupport"
)

// ── A stand-in for object storage ─────────────────────────────────────────
//
// This fake models what a real MinIO was OBSERVED to do (see
// minio_integration_test.go, which runs the same flows against one): a part
// replaces any earlier part with the same number, completing needs every
// named part to be present with a matching etag, and the assembled object's
// size is the sum of its parts. It deliberately does NOT model signature
// checking — that is not something this package computes, and pretending to
// check it here would be testing the fake. The integration test covers it.
type fakeStorage struct {
	mu sync.Mutex

	uploads   map[string]map[int]minio.ObjectPart // uploadID -> partNumber -> part
	objects   map[string]int64                    // objectKey -> size
	aborted   []string
	removed   []string
	nextID    int
	failStat  bool
	statSizes map[string]int64 // objectKey -> size to report instead of the truth
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{
		uploads:   map[string]map[int]minio.ObjectPart{},
		objects:   map[string]int64{},
		statSizes: map[string]int64{},
	}
}

func (f *fakeStorage) StatObject(_ context.Context, _, object string, _ minio.StatObjectOptions) (minio.ObjectInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failStat {
		return minio.ObjectInfo{}, minio.ErrorResponse{Code: "NoSuchKey", StatusCode: 404}
	}
	if size, ok := f.statSizes[object]; ok {
		return minio.ObjectInfo{Key: object, Size: size}, nil
	}
	size, ok := f.objects[object]
	if !ok {
		return minio.ObjectInfo{}, minio.ErrorResponse{Code: "NoSuchKey", StatusCode: 404}
	}
	return minio.ObjectInfo{Key: object, Size: size}, nil
}

func (f *fakeStorage) RemoveObject(_ context.Context, _, object string, _ minio.RemoveObjectOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, object)
	delete(f.objects, object)
	return nil
}

func (f *fakeStorage) BucketExists(context.Context, string) (bool, error) { return true, nil }

func (f *fakeStorage) NewMultipartUpload(_ context.Context, _, _ string, _ minio.PutObjectOptions) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := fmt.Sprintf("storage-upload-%d", f.nextID)
	f.uploads[id] = map[int]minio.ObjectPart{}
	return id, nil
}

func (f *fakeStorage) ListObjectParts(_ context.Context, _, _, uploadID string, marker, maxParts int) (minio.ListObjectPartsResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	parts, ok := f.uploads[uploadID]
	if !ok {
		return minio.ListObjectPartsResult{}, minio.ErrorResponse{Code: "NoSuchUpload", StatusCode: 404}
	}
	numbers := make([]int, 0, len(parts))
	for n := range parts {
		if n > marker {
			numbers = append(numbers, n)
		}
	}
	sort.Ints(numbers)

	res := minio.ListObjectPartsResult{UploadID: uploadID, MaxParts: maxParts}
	for i, n := range numbers {
		if i >= maxParts {
			res.IsTruncated = true
			res.NextPartNumberMarker = res.ObjectParts[len(res.ObjectParts)-1].PartNumber
			break
		}
		res.ObjectParts = append(res.ObjectParts, parts[n])
	}
	return res, nil
}

func (f *fakeStorage) CompleteMultipartUpload(_ context.Context, _, object, uploadID string, parts []minio.CompletePart, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	have, ok := f.uploads[uploadID]
	if !ok {
		return minio.UploadInfo{}, minio.ErrorResponse{Code: "NoSuchUpload", StatusCode: 404}
	}
	var total int64
	for _, p := range parts {
		got, ok := have[p.PartNumber]
		if !ok || strings.Trim(got.ETag, `"`) != strings.Trim(p.ETag, `"`) {
			return minio.UploadInfo{}, minio.ErrorResponse{Code: "InvalidPart", StatusCode: 400}
		}
		total += got.Size
	}
	delete(f.uploads, uploadID)
	f.objects[object] = total
	// A real MinIO reports Size 0 here; the caller has to stat the object.
	return minio.UploadInfo{Key: object}, nil
}

func (f *fakeStorage) AbortMultipartUpload(_ context.Context, _, _, uploadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.uploads[uploadID]; !ok {
		return minio.ErrorResponse{Code: "NoSuchUpload", StatusCode: 404}
	}
	delete(f.uploads, uploadID)
	f.aborted = append(f.aborted, uploadID)
	return nil
}

// land simulates a part arriving at object storage.
func (f *fakeStorage) land(uploadID string, partNumber int, size int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads[uploadID][partNumber] = minio.ObjectPart{
		PartNumber: partNumber,
		Size:       size,
		ETag:       fmt.Sprintf(`"etag-%s-%d"`, uploadID, partNumber),
	}
}

func (f *fakeStorage) inProgress(uploadID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.uploads[uploadID]
	return ok
}

// multipartService is the presigning service wired to a fake object store.
func multipartService(t *testing.T) (*Service, *fakeStorage, *pgxpool.Pool) {
	t.Helper()
	pool := testsupport.DB(t)
	cfg := browserFacingConfig()
	cfg.MaxUploadBytes = 2 << 30 // 2 GiB, enough for the screen recording case
	s := NewService(cfg)
	s.SetDB(pool)
	fake := newFakeStorage()
	s.store = fake
	s.multipart = fake
	return s, fake, pool
}

// bigChatUpload is a request for a file large enough to want multipart.
func bigChatUpload(t *testing.T, pool *pgxpool.Pool, size int64) UploadRequest {
	t.Helper()
	org := testsupport.SeedOrg(t, pool)
	channel := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)
	return UploadRequest{
		OrgID:       org.ID.String(),
		UserID:      org.OwnerID.String(),
		Category:    "chat",
		RefID:       channel.String(),
		Filename:    "standup.mp4",
		ContentType: "video/mp4",
		Size:        size,
	}
}

// ── The threshold: which path a file takes ────────────────────────────────

// Below the threshold the extra round trips of multipart buy nothing, so a
// small file still goes up in one PUT.
func TestASmallFileStillTakesTheSinglePutPath(t *testing.T) {
	svc, _, pool := multipartService(t)
	req := chatUpload(t, pool, 1<<20)

	resp, err := svc.GenerateUploadURL(context.Background(), req)
	if err != nil {
		t.Fatalf("a 1 MB file was refused a single PUT: %v", err)
	}
	if strings.Contains(resp.UploadURL, "uploadId=") {
		t.Errorf("a small file was sent down the multipart path: %s", resp.UploadURL)
	}
}

// Above it a single PUT is refused outright. Letting a 2 GB body go up as one
// unresumable request is the thing multipart exists to stop, and the proxy in
// front of object storage only has to be sized for one part.
func TestASinglePutIsRefusedAboveTheMultipartThreshold(t *testing.T) {
	svc, _, pool := multipartService(t)
	req := chatUpload(t, pool, DefaultMultipartThreshold+1)

	resp, err := svc.GenerateUploadURL(context.Background(), req)
	if err == nil {
		t.Fatalf("a file over the multipart threshold was handed a single-PUT URL: %s", resp.UploadURL)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "multipart") {
		t.Errorf("the refusal must tell the client to use multipart, got %q", err)
	}
}

// The client decides which path to take before it asks for anything, so it has
// to be told the same numbers the server is using.
func TestTheLimitsEndpointPublishesTheThreshold(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	RegisterRoutes(e.Group("/api"), nil)

	found := false
	for _, r := range e.Routes() {
		if r.Method == "GET" && r.Path == "/api/files/limits" {
			found = true
		}
	}
	if !found {
		t.Fatal("no route tells the client what the limits are")
	}

	svc := NewService(browserFacingConfig())
	if svc.MultipartThreshold() <= 0 {
		t.Error("the service publishes no multipart threshold")
	}
	if svc.MultipartThreshold() > svc.MaxUploadBytes() {
		t.Errorf("threshold %d is above the ceiling %d, so nothing would ever use multipart",
			svc.MultipartThreshold(), svc.MaxUploadBytes())
	}
}

// ── The part plan ─────────────────────────────────────────────────────────

// The plan is the size guarantee. Every part number that will ever be signed,
// and the exact byte count of each, is fixed here — so the total a client can
// possibly deliver is the total it declared, whatever it does afterwards.
func TestThePartPlanCoversExactlyTheDeclaredSize(t *testing.T) {
	cases := []struct {
		name  string
		total int64
	}{
		{"just over the threshold", DefaultMultipartThreshold + 1},
		{"one exact part", DefaultPartSize},
		{"one part and a byte", DefaultPartSize + 1},
		{"a screen recording", 2 << 30},
		{"the default ceiling", DefaultMaxUploadBytes},
		{"far past the part ceiling", 200 << 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			partSize, partCount, err := planParts(tc.total)
			if err != nil {
				t.Fatalf("plan %d bytes: %v", tc.total, err)
			}
			if partCount < 1 || partCount > MaxParts {
				t.Fatalf("part count %d is outside 1..%d", partCount, MaxParts)
			}
			var sum int64
			for n := 1; n <= partCount; n++ {
				length := partLength(tc.total, partSize, partCount, n)
				if length <= 0 {
					t.Fatalf("part %d has length %d", n, length)
				}
				// Object storage refuses a non-final part under 5 MiB when the
				// upload is completed — observed against a real MinIO.
				if n < partCount && length < MinPartSize {
					t.Errorf("part %d of %d is %d bytes, under the %d-byte minimum", n, partCount, length, MinPartSize)
				}
				sum += length
			}
			if sum != tc.total {
				t.Errorf("the plan covers %d bytes, want exactly %d", sum, tc.total)
			}
		})
	}
}

// ── Initiating ───────────────────────────────────────────────────────────

func TestInitiateRecordsThePlanSoItSurvivesTheBrowser(t *testing.T) {
	svc, _, pool := multipartService(t)
	ctx := context.Background()
	req := bigChatUpload(t, pool, 300<<20)

	up, err := svc.InitiateMultipart(ctx, req)
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	if up.PartCount < 2 {
		t.Fatalf("a 300 MB file planned %d parts", up.PartCount)
	}

	// Read it back the way a reloaded browser would: from the database, not
	// from anything the first page kept in memory.
	var total, partSize int64
	var partCount int
	var objectKey, storageID string
	if err := pool.QueryRow(ctx, `
		SELECT total_bytes, part_size, part_count, object_key, storage_upload_id
		FROM multipart_uploads WHERE id = $1`, up.ID).
		Scan(&total, &partSize, &partCount, &objectKey, &storageID); err != nil {
		t.Fatalf("the plan was not persisted: %v", err)
	}
	if total != 300<<20 || partSize != up.PartSize || partCount != up.PartCount {
		t.Errorf("stored plan %d/%d/%d does not match the one handed out %d/%d/%d",
			total, partSize, partCount, up.TotalBytes, up.PartSize, up.PartCount)
	}
	if objectKey != up.ObjectKey || storageID == "" {
		t.Errorf("stored key %q / storage id %q", objectKey, storageID)
	}
}

func TestInitiateRefusesAFileOverTheCeiling(t *testing.T) {
	svc, _, pool := multipartService(t)
	req := bigChatUpload(t, pool, (2<<30)+1) // one byte over the configured ceiling

	if up, err := svc.InitiateMultipart(context.Background(), req); err == nil {
		t.Fatalf("a file over the ceiling was given a multipart plan: %s", up.ObjectKey)
	}
}

// Org isolation: initiating is an entitlement to write into a channel.
func TestInitiateIsRefusedForAnotherOrganizationsChannel(t *testing.T) {
	svc, _, pool := multipartService(t)
	mine := testsupport.SeedOrg(t, pool)
	theirs := testsupport.SeedOrg(t, pool)
	theirChannel := testsupport.SeedChannel(t, pool, theirs, "theirs", theirs.OwnerID)

	up, err := svc.InitiateMultipart(context.Background(), UploadRequest{
		OrgID:    theirs.ID.String(),
		UserID:   mine.OwnerID.String(),
		Category: "chat",
		RefID:    theirChannel.String(),
		Filename: "theirs.mp4",
		Size:     300 << 20,
	})
	if err == nil {
		t.Fatalf("an outsider started an upload into another organization: %s", up.ObjectKey)
	}
}

// ── Signing parts ─────────────────────────────────────────────────────────

func startUpload(t *testing.T, svc *Service, pool *pgxpool.Pool, size int64) (*MultipartUpload, UploadRequest) {
	t.Helper()
	req := bigChatUpload(t, pool, size)
	up, err := svc.InitiateMultipart(context.Background(), req)
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	return up, req
}

// Each URL is signed for one part number and one content length, exactly as
// the single-PUT path signs the whole body's length. Neither can be changed
// without invalidating the signature.
func TestEachPartURLIsSignedForOnePartAndOneLength(t *testing.T) {
	svc, _, pool := multipartService(t)
	up, req := startUpload(t, svc, pool, 300<<20)

	parts, err := svc.SignParts(context.Background(), uuid.MustParse(req.OrgID), uuid.MustParse(req.UserID), up.ID, []int{1, 2})
	if err != nil {
		t.Fatalf("sign parts: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("asked for 2 part URLs, got %d", len(parts))
	}
	for _, p := range parts {
		u, err := url.Parse(p.URL)
		if err != nil {
			t.Fatalf("parse part url: %v", err)
		}
		q := u.Query()
		if q.Get("partNumber") != strconv.Itoa(p.PartNumber) {
			t.Errorf("part %d: url carries partNumber=%q", p.PartNumber, q.Get("partNumber"))
		}
		if q.Get("uploadId") == "" {
			t.Errorf("part %d: url carries no uploadId", p.PartNumber)
		}
		if signed := q.Get("X-Amz-SignedHeaders"); !strings.Contains(signed, "content-length") {
			t.Errorf("part %d: SignedHeaders is %q; without content-length the part accepts any number of bytes",
				p.PartNumber, signed)
		}
		if p.ContentLength != up.PartSize {
			t.Errorf("part %d is signed for %d bytes, want the planned %d", p.PartNumber, p.ContentLength, up.PartSize)
		}
		if u.Host != "teamup.example.test" {
			t.Errorf("part %d points at %q, which the browser cannot reach", p.PartNumber, u.Host)
		}
	}
}

// A part number outside the plan would be bytes nobody accounted for.
func TestPartNumbersOutsideThePlanAreRefused(t *testing.T) {
	svc, _, pool := multipartService(t)
	up, req := startUpload(t, svc, pool, 300<<20)
	org, user := uuid.MustParse(req.OrgID), uuid.MustParse(req.UserID)

	for _, n := range []int{0, -1, up.PartCount + 1, MaxParts + 1} {
		if _, err := svc.SignParts(context.Background(), org, user, up.ID, []int{n}); err == nil {
			t.Errorf("part number %d was signed for, but the plan has %d parts", n, up.PartCount)
		}
	}
}

// Asking for nothing in particular means "whatever is still missing" — which
// is what a resuming browser wants and is the only thing it needs to know.
func TestSigningWithNoPartNumbersOffersTheMissingOnes(t *testing.T) {
	svc, fake, pool := multipartService(t)
	up, req := startUpload(t, svc, pool, 300<<20)
	storageID := storageUploadID(t, pool, up.ID)
	fake.land(storageID, 1, up.PartSize)
	fake.land(storageID, 2, up.PartSize)

	parts, err := svc.SignParts(context.Background(), uuid.MustParse(req.OrgID), uuid.MustParse(req.UserID), up.ID, nil)
	if err != nil {
		t.Fatalf("sign parts: %v", err)
	}
	if len(parts) == 0 {
		t.Fatal("nothing was offered although parts are still missing")
	}
	for _, p := range parts {
		if p.PartNumber == 1 || p.PartNumber == 2 {
			t.Errorf("part %d already landed but was offered again", p.PartNumber)
		}
	}
}

func TestOnlyTheCreatorCanAddPartsToAnUpload(t *testing.T) {
	svc, _, pool := multipartService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	channel := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)
	colleague := testsupport.SeedUser(t, pool, org, "colleague@example.test", "member")
	if _, err := pool.Exec(ctx, `
		INSERT INTO channel_members (channel_id, user_id, role, joined_at)
		VALUES ($1, $2, 'member', NOW()) ON CONFLICT DO NOTHING`, channel, colleague); err != nil {
		t.Fatalf("add colleague to the channel: %v", err)
	}

	up, err := svc.InitiateMultipart(ctx, UploadRequest{
		OrgID: org.ID.String(), UserID: org.OwnerID.String(),
		Category: "chat", RefID: channel.String(), Filename: "mine.mp4", Size: 300 << 20,
	})
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}

	// A colleague with full access to the same channel still may not write
	// into somebody else's half-finished object.
	if _, err := svc.SignParts(ctx, org.ID, colleague, up.ID, []int{1}); err == nil {
		t.Error("a colleague was given a part URL for another member's upload")
	}
}

func TestPartsCannotBeSignedAcrossAnOrganizationBoundary(t *testing.T) {
	svc, _, pool := multipartService(t)
	ctx := context.Background()
	theirs := testsupport.SeedOrg(t, pool)
	theirChannel := testsupport.SeedChannel(t, pool, theirs, "theirs", theirs.OwnerID)
	mine := testsupport.SeedOrg(t, pool)

	up, err := svc.InitiateMultipart(ctx, UploadRequest{
		OrgID: theirs.ID.String(), UserID: theirs.OwnerID.String(),
		Category: "chat", RefID: theirChannel.String(), Filename: "theirs.mp4", Size: 300 << 20,
	})
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}

	if _, err := svc.SignParts(ctx, mine.ID, mine.OwnerID, up.ID, []int{1}); err == nil {
		t.Error("an outsider was given a part URL for another organization's upload")
	}
	if _, err := svc.MultipartStatus(ctx, mine.ID, mine.OwnerID, up.ID); err == nil {
		t.Error("an outsider read the progress of another organization's upload")
	}
	if _, err := svc.CompleteMultipart(ctx, mine.ID, mine.OwnerID, up.ID); err == nil {
		t.Error("an outsider completed another organization's upload")
	}
	if err := svc.AbortMultipart(ctx, mine.ID, mine.OwnerID, up.ID); err == nil {
		t.Error("an outsider aborted another organization's upload")
	}
}

// ── Resuming ──────────────────────────────────────────────────────────────

// What has landed is asked of object storage, never remembered here: a part
// can arrive and its acknowledgement be lost, and the store is the only thing
// that cannot disagree with itself.
func TestStatusReportsWhichPartsAlreadyLanded(t *testing.T) {
	svc, fake, pool := multipartService(t)
	up, req := startUpload(t, svc, pool, 300<<20)
	storageID := storageUploadID(t, pool, up.ID)
	fake.land(storageID, 1, up.PartSize)
	fake.land(storageID, 3, up.PartSize)

	st, err := svc.MultipartStatus(context.Background(), uuid.MustParse(req.OrgID), uuid.MustParse(req.UserID), up.ID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	got := map[int]int64{}
	for _, p := range st.Uploaded {
		got[p.PartNumber] = p.Size
	}
	if len(got) != 2 || got[1] != up.PartSize || got[3] != up.PartSize {
		t.Errorf("status reports %v, want parts 1 and 3 of %d bytes", got, up.PartSize)
	}
	if st.Upload.PartCount != up.PartCount {
		t.Errorf("status reports %d parts, want %d", st.Upload.PartCount, up.PartCount)
	}
}

// After a reload the tab knows nothing. The server has to be able to hand the
// upload back, or there is no resuming.
func TestAnInterruptedUploadIsOfferedBackToItsOwner(t *testing.T) {
	svc, _, pool := multipartService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	channel := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)
	other := testsupport.SeedOrg(t, pool)

	up, err := svc.InitiateMultipart(ctx, UploadRequest{
		OrgID: org.ID.String(), UserID: org.OwnerID.String(),
		Category: "chat", RefID: channel.String(), Filename: "standup.mp4", Size: 300 << 20,
	})
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}

	mine, err := svc.ListResumableUploads(ctx, org.ID, org.OwnerID, "chat", channel.String())
	if err != nil {
		t.Fatalf("list resumable: %v", err)
	}
	if len(mine) != 1 || mine[0].ID != up.ID || mine[0].Filename != "standup.mp4" {
		t.Fatalf("the interrupted upload was not offered back: %+v", mine)
	}

	// Not to anybody else, and not across an organization boundary.
	stranger := testsupport.SeedUser(t, pool, org, "stranger@example.test", "member")
	if got, _ := svc.ListResumableUploads(ctx, org.ID, stranger, "chat", channel.String()); len(got) != 0 {
		t.Errorf("somebody else's half-finished upload was offered to a stranger: %+v", got)
	}
	if got, err := svc.ListResumableUploads(ctx, other.ID, other.OwnerID, "chat", channel.String()); err == nil && len(got) != 0 {
		t.Errorf("another organization was shown this upload: %+v", got)
	}
}

// ── Completing ────────────────────────────────────────────────────────────

func landAllParts(t *testing.T, fake *fakeStorage, pool *pgxpool.Pool, up *MultipartUpload) {
	t.Helper()
	storageID := storageUploadID(t, pool, up.ID)
	for n := 1; n <= up.PartCount; n++ {
		fake.land(storageID, n, partLength(up.TotalBytes, up.PartSize, up.PartCount, n))
	}
}

func storageUploadID(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(),
		`SELECT storage_upload_id FROM multipart_uploads WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatalf("read storage upload id: %v", err)
	}
	return s
}

// The measured-size property the single-PUT path has must survive multipart:
// the row records what storage says is there, never what the client claimed.
func TestCompletingRecordsTheAttachmentWithTheMeasuredSize(t *testing.T) {
	svc, fake, pool := multipartService(t)
	ctx := context.Background()
	// Small enough to keep the fake cheap, large enough to need two parts.
	const total = DefaultPartSize + 1024
	up, req := startUpload(t, svc, pool, total)
	landAllParts(t, fake, pool, up)

	att, err := svc.CompleteMultipart(ctx, uuid.MustParse(req.OrgID), uuid.MustParse(req.UserID), up.ID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if att.SizeBytes != total {
		t.Errorf("recorded %d bytes, want the %d that are actually stored", att.SizeBytes, total)
	}
	if att.ObjectKey != up.ObjectKey {
		t.Errorf("recorded object key %q, want %q", att.ObjectKey, up.ObjectKey)
	}

	// The row is real, and the upload is finished rather than left resumable.
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM attachments WHERE object_key = $1`, up.ObjectKey).Scan(&n); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if n != 1 {
		t.Errorf("complete wrote %d attachment rows, want 1", n)
	}
	left, _ := svc.ListResumableUploads(ctx, uuid.MustParse(req.OrgID), uuid.MustParse(req.UserID), req.Category, req.RefID)
	if len(left) != 0 {
		t.Errorf("a finished upload is still being offered as resumable: %+v", left)
	}
}

func TestCompletingIsRefusedWhileAPartIsMissing(t *testing.T) {
	svc, fake, pool := multipartService(t)
	ctx := context.Background()
	up, req := startUpload(t, svc, pool, DefaultPartSize+1024)
	storageID := storageUploadID(t, pool, up.ID)
	fake.land(storageID, 1, up.PartSize) // part 2 never arrives

	if _, err := svc.CompleteMultipart(ctx, uuid.MustParse(req.OrgID), uuid.MustParse(req.UserID), up.ID); err == nil {
		t.Fatal("an upload was completed with a part missing")
	}
	if !fake.inProgress(storageID) {
		t.Error("the refusal threw away the parts that had landed, so it cannot be resumed")
	}
}

// A part that is not the length it was signed for cannot happen through a
// presigned URL — object storage refuses it, observed. Checking anyway is what
// keeps the total honest if it ever could.
func TestCompletingIsRefusedWhenAPartIsNotItsPlannedSize(t *testing.T) {
	svc, fake, pool := multipartService(t)
	ctx := context.Background()
	up, req := startUpload(t, svc, pool, DefaultPartSize+1024)
	storageID := storageUploadID(t, pool, up.ID)
	fake.land(storageID, 1, up.PartSize)
	fake.land(storageID, 2, 999999) // not the 1024 the plan says

	if _, err := svc.CompleteMultipart(ctx, uuid.MustParse(req.OrgID), uuid.MustParse(req.UserID), up.ID); err == nil {
		t.Fatal("an upload completed with a part that was not the size it was signed for")
	}
}

// If the assembled object somehow is not the size that was declared and
// planned, nothing gets recorded and the bytes do not get to stay.
func TestAnAssembledObjectOfTheWrongSizeIsNotKept(t *testing.T) {
	svc, fake, pool := multipartService(t)
	ctx := context.Background()
	up, req := startUpload(t, svc, pool, DefaultPartSize+1024)
	landAllParts(t, fake, pool, up)
	fake.statSizes[up.ObjectKey] = 9 << 30 // storage reports something else entirely

	if _, err := svc.CompleteMultipart(ctx, uuid.MustParse(req.OrgID), uuid.MustParse(req.UserID), up.ID); err == nil {
		t.Fatal("an object of an unplanned size was recorded")
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM attachments WHERE object_key = $1`, up.ObjectKey).Scan(&n); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if n != 0 {
		t.Errorf("%d attachment rows were written for an object of the wrong size", n)
	}
	found := false
	for _, k := range fake.removed {
		if k == up.ObjectKey {
			found = true
		}
	}
	if !found {
		t.Error("the mis-sized object was left in storage")
	}
}

// ── Aborting and sweeping ─────────────────────────────────────────────────

func TestAbortingDropsThePartsAndTheRow(t *testing.T) {
	svc, fake, pool := multipartService(t)
	ctx := context.Background()
	up, req := startUpload(t, svc, pool, 300<<20)
	storageID := storageUploadID(t, pool, up.ID)
	fake.land(storageID, 1, up.PartSize)

	if err := svc.AbortMultipart(ctx, uuid.MustParse(req.OrgID), uuid.MustParse(req.UserID), up.ID); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if fake.inProgress(storageID) {
		t.Error("the parts are still consuming storage after an abort")
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM multipart_uploads WHERE id = $1`, up.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Error("the row survived the abort")
	}
}

func TestOnlyTheCreatorCanAbortTheirUpload(t *testing.T) {
	svc, _, pool := multipartService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	channel := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)
	somebody := testsupport.SeedUser(t, pool, org, "somebody@example.test", "member")

	up, err := svc.InitiateMultipart(ctx, UploadRequest{
		OrgID: org.ID.String(), UserID: org.OwnerID.String(),
		Category: "chat", RefID: channel.String(), Filename: "mine.mp4", Size: 300 << 20,
	})
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	if err := svc.AbortMultipart(ctx, org.ID, somebody, up.ID); err == nil {
		t.Error("somebody else aborted an upload that was not theirs")
	}
}

// An interrupted multipart upload keeps its parts for ever otherwise. This is
// the same hourly sweep that reclaims staged-but-never-sent single uploads.
func TestTheSweepAbortsAbandonedMultipartUploads(t *testing.T) {
	svc, fake, pool := multipartService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	channel := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	start := func(name string, age time.Duration) *MultipartUpload {
		up, err := svc.InitiateMultipart(ctx, UploadRequest{
			OrgID: org.ID.String(), UserID: org.OwnerID.String(),
			Category: "chat", RefID: channel.String(), Filename: name, Size: 300 << 20,
		})
		if err != nil {
			t.Fatalf("initiate %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx,
			`UPDATE multipart_uploads SET created_at = NOW() - $2::interval WHERE id = $1`, up.ID, age.String()); err != nil {
			t.Fatalf("age %s: %v", name, err)
		}
		return up
	}

	abandoned := start("abandoned.mp4", 48*time.Hour)
	paused := start("paused.mp4", time.Minute)
	abandonedStorageID := storageUploadID(t, pool, abandoned.ID)
	pausedStorageID := storageUploadID(t, pool, paused.ID)
	fake.land(abandonedStorageID, 1, abandoned.PartSize)
	fake.land(pausedStorageID, 1, paused.PartSize)

	swept, err := svc.SweepAbandonedMultipartUploads(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if swept != 1 {
		t.Errorf("swept %d, want 1", swept)
	}
	if fake.inProgress(abandonedStorageID) {
		t.Error("the abandoned upload's parts are still consuming storage")
	}
	if !fake.inProgress(pausedStorageID) {
		t.Error("the sweep threw away an upload somebody paused a minute ago")
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM multipart_uploads WHERE id = $1`, abandoned.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Error("the abandoned row survived the sweep")
	}
}

// The hourly job calls one thing. Multipart leftovers have to be reclaimed by
// it too, or nothing ever reclaims them.
func TestTheHourlySweepReachesMultipartLeftovers(t *testing.T) {
	svc, fake, pool := multipartService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	channel := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	up, err := svc.InitiateMultipart(ctx, UploadRequest{
		OrgID: org.ID.String(), UserID: org.OwnerID.String(),
		Category: "chat", RefID: channel.String(), Filename: "gone.mp4", Size: 300 << 20,
	})
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE multipart_uploads SET created_at = NOW() - INTERVAL '48 hours' WHERE id = $1`, up.ID); err != nil {
		t.Fatalf("age: %v", err)
	}
	storageID := storageUploadID(t, pool, up.ID)
	fake.land(storageID, 1, up.PartSize)

	if _, err := svc.SweepAbandonedUploads(ctx, 24*time.Hour); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if fake.inProgress(storageID) {
		t.Error("the hourly sweep left an abandoned multipart upload's parts in storage")
	}
}

// ── Routes ────────────────────────────────────────────────────────────────

// Multipart was deleted once because it was a lie: initiate returned an
// ordinary single-PUT URL and complete wrote no row. These are the endpoints
// the real thing needs; the behaviour behind them is covered above.
func TestTheMultipartEndpointsAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	RegisterRoutes(e.Group("/api"), nil)

	want := map[string]string{
		"POST /api/files/multipart":              "start a resumable upload",
		"GET /api/files/multipart":               "offer interrupted uploads back after a reload",
		"GET /api/files/multipart/:id":           "report which parts already landed",
		"POST /api/files/multipart/:id/parts":    "sign the next parts",
		"POST /api/files/multipart/:id/complete": "assemble and record",
		"DELETE /api/files/multipart/:id":        "give up and reclaim the parts",
	}
	have := map[string]bool{}
	for _, r := range e.Routes() {
		have[r.Method+" "+r.Path] = true
	}
	for route, why := range want {
		if !have[route] {
			t.Errorf("no route to %s: %s is missing", why, route)
		}
	}
}

// The multipart paths sit underneath /files/:id, which already matches a
// wildcard in that position. Registering both is one thing; having a request
// reach the handler its path names is another, and getting it wrong means
// cancelling an upload silently tries to discard an attachment called
// "multipart" instead.
func TestMultipartRequestsReachTheirOwnHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	// Capture which route gin resolved to and stop before the handler, which
	// would need a service and a session.
	var resolved string
	e.Use(func(c *gin.Context) {
		resolved = c.FullPath()
		c.AbortWithStatus(http.StatusTeapot)
	})
	RegisterRoutes(e.Group("/api"), nil)

	id := uuid.New().String()
	cases := []struct {
		method, path, want string
	}{
		{"POST", "/api/files/multipart", "/api/files/multipart"},
		{"GET", "/api/files/multipart", "/api/files/multipart"},
		{"GET", "/api/files/multipart/" + id, "/api/files/multipart/:id"},
		{"POST", "/api/files/multipart/" + id + "/parts", "/api/files/multipart/:id/parts"},
		{"POST", "/api/files/multipart/" + id + "/complete", "/api/files/multipart/:id/complete"},
		{"DELETE", "/api/files/multipart/" + id, "/api/files/multipart/:id"},
		// And the pre-existing route it sits underneath still works.
		{"DELETE", "/api/files/" + id, "/api/files/:id"},
		{"GET", "/api/files", "/api/files"},
	}
	for _, tc := range cases {
		resolved = ""
		req := httptest.NewRequest(tc.method, tc.path, nil)
		e.ServeHTTP(httptest.NewRecorder(), req)
		if resolved != tc.want {
			t.Errorf("%s %s resolved to %q, want %q", tc.method, tc.path, resolved, tc.want)
		}
	}
}
