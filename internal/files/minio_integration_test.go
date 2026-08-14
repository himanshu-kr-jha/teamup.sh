package files

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/teamup/teamup/internal/testsupport"
)

// These run the multipart flow against a REAL object store, because the whole
// size guarantee rests on what an S3 signature does and does not cover — and
// that is not something a fake can tell you. Everything asserted here was
// observed against MinIO before it was written down.
//
// Point TEAMUP_TEST_MINIO_ENDPOINT at one to run them, e.g.
//
//	docker run -d --name minio -p 9401:9000 \
//	  -e MINIO_ROOT_USER=minioadmin -e MINIO_ROOT_PASSWORD=minioadmin123 \
//	  minio/minio server /data
//	TEAMUP_TEST_MINIO_ENDPOINT=localhost:9401 \
//	TEAMUP_TEST_MINIO_ACCESS_KEY=minioadmin \
//	TEAMUP_TEST_MINIO_SECRET_KEY=minioadmin123 go test ./internal/files/...
//
// Without it they skip, and a skipped test proves nothing: the fake-backed
// tests in multipart_test.go cover this package's own logic, not the store's.

const envMinIOEndpoint = "TEAMUP_TEST_MINIO_ENDPOINT"

// liveService wires the service to a real MinIO. The browser-facing and
// internal endpoints are the same address here, which is the single-address
// deployment shape; the two-client split is covered in presign_test.go.
func liveService(t *testing.T) (*Service, *minio.Core) {
	t.Helper()
	endpoint := os.Getenv(envMinIOEndpoint)
	if endpoint == "" {
		t.Skipf("set %s to run the object storage integration tests", envMinIOEndpoint)
	}
	access := envOr("TEAMUP_TEST_MINIO_ACCESS_KEY", "minioadmin")
	secret := envOr("TEAMUP_TEST_MINIO_SECRET_KEY", "minioadmin123")
	bucket := envOr("TEAMUP_TEST_MINIO_BUCKET", "teamup-files-test")

	cfg := Config{
		Endpoint:  endpoint,
		AccessKey: access,
		SecretKey: secret,
		Bucket:    bucket,
		Secure:    false,
		Region:    DefaultRegion,
		// Big enough for the fixtures below without waiting on real bytes.
		MaxUploadBytes: 1 << 30,
	}
	svc := NewService(cfg)
	svc.SetDB(testsupport.DB(t))

	core, err := minio.NewCore(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(access, secret, ""),
		Secure:       false,
		Region:       DefaultRegion,
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		t.Fatalf("connect to %s: %v", endpoint, err)
	}
	ctx := context.Background()
	exists, err := core.BucketExists(ctx, bucket)
	if err != nil {
		t.Fatalf("reach object storage at %s: %v", endpoint, err)
	}
	if !exists {
		if err := core.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: DefaultRegion}); err != nil {
			t.Fatalf("create bucket %s: %v", bucket, err)
		}
	}
	return svc, core
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// putPart sends a body to a presigned part URL exactly as a browser would.
func putPart(t *testing.T, rawurl string, body []byte) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, rawurl, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.ContentLength = int64(len(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT part: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// liveChatUpload seeds an organization and a channel and returns an upload
// request for a file big enough to need two real parts.
func liveChatUpload(t *testing.T, svc *Service, size int64) (UploadRequest, uuid.UUID, uuid.UUID) {
	t.Helper()
	org := testsupport.SeedOrg(t, svc.db)
	channel := testsupport.SeedChannel(t, svc.db, org, "general", org.OwnerID)
	return UploadRequest{
		OrgID:       org.ID.String(),
		UserID:      org.OwnerID.String(),
		Category:    "chat",
		RefID:       channel.String(),
		Filename:    "standup.bin",
		ContentType: "application/octet-stream",
		Size:        size,
	}, org.ID, org.OwnerID
}

// The whole thing, end to end, with the interruption that is the point of it:
// one part lands, completion is refused, the second part lands, completion
// succeeds, and the recorded size is what storage measured.
func TestLiveMultipartUploadResumesAndRecordsTheMeasuredSize(t *testing.T) {
	svc, core := liveService(t)
	ctx := context.Background()

	const total = DefaultPartSize + 4096 // two parts: one full, one short
	req, orgID, userID := liveChatUpload(t, svc, total)

	up, err := svc.InitiateMultipart(ctx, req)
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	if up.PartCount != 2 {
		t.Fatalf("planned %d parts for %d bytes, want 2", up.PartCount, total)
	}
	t.Cleanup(func() { _ = core.RemoveObject(ctx, svc.bucketName, up.ObjectKey, minio.RemoveObjectOptions{}) })

	// Distinguishable bytes, so assembly order is checked rather than assumed.
	partOne := bytes.Repeat([]byte("A"), int(up.PartSize))
	partTwo := bytes.Repeat([]byte("B"), 4096)

	urls, err := svc.SignParts(ctx, orgID, userID, up.ID, []int{1, 2})
	if err != nil {
		t.Fatalf("sign parts: %v", err)
	}
	byNumber := map[int]PartURL{}
	for _, p := range urls {
		byNumber[p.PartNumber] = p
	}
	if byNumber[1].ContentLength != up.PartSize || byNumber[2].ContentLength != 4096 {
		t.Fatalf("signed lengths %d/%d, want %d/4096", byNumber[1].ContentLength, byNumber[2].ContentLength, up.PartSize)
	}

	// ── the signature really does bind the length ─────────────────────────
	if code, body := putPart(t, byNumber[2].URL, bytes.Repeat([]byte("B"), 8192)); code != http.StatusForbidden {
		t.Errorf("a part URL signed for 4096 bytes accepted 8192: %d %s", code, body)
	} else if !strings.Contains(body, "SignatureDoesNotMatch") {
		t.Errorf("refused with %q, want SignatureDoesNotMatch", body)
	}
	if code, body := putPart(t, byNumber[2].URL, []byte("B")); code != http.StatusForbidden {
		t.Errorf("a part URL signed for 4096 bytes accepted 1 byte: %d %s", code, body)
	}

	// ── and the part number ───────────────────────────────────────────────
	rewritten := strings.Replace(byNumber[1].URL, "partNumber=1", "partNumber=9", 1)
	if code, body := putPart(t, rewritten, partOne); code != http.StatusForbidden {
		t.Errorf("a part URL signed for part 1 wrote part 9: %d %s", code, body)
	} else if !strings.Contains(body, "SignatureDoesNotMatch") {
		t.Errorf("refused with %q, want SignatureDoesNotMatch", body)
	}

	// ── part 1 lands, then the connection dies ────────────────────────────
	if code, body := putPart(t, byNumber[1].URL, partOne); code != http.StatusOK {
		t.Fatalf("PUT part 1: %d %s", code, body)
	}

	status, err := svc.MultipartStatus(ctx, orgID, userID, up.ID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(status.Uploaded) != 1 || status.Uploaded[0].PartNumber != 1 || status.Uploaded[0].Size != up.PartSize {
		t.Fatalf("storage reports %+v, want part 1 of %d bytes", status.Uploaded, up.PartSize)
	}

	if _, err := svc.CompleteMultipart(ctx, orgID, userID, up.ID); err == nil {
		t.Fatal("an upload completed with part 2 missing")
	}

	// ── resuming: ask for what is left, without naming it ─────────────────
	missing, err := svc.SignParts(ctx, orgID, userID, up.ID, nil)
	if err != nil {
		t.Fatalf("sign missing parts: %v", err)
	}
	if len(missing) != 1 || missing[0].PartNumber != 2 {
		t.Fatalf("resuming offered %+v, want only part 2", missing)
	}
	if code, body := putPart(t, missing[0].URL, partTwo); code != http.StatusOK {
		t.Fatalf("PUT part 2: %d %s", code, body)
	}

	att, err := svc.CompleteMultipart(ctx, orgID, userID, up.ID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if att.SizeBytes != total {
		t.Errorf("recorded %d bytes, want %d", att.SizeBytes, total)
	}

	// Independently of anything this package returned: what is actually there?
	info, err := core.StatObject(ctx, svc.bucketName, up.ObjectKey, minio.StatObjectOptions{})
	if err != nil {
		t.Fatalf("stat the assembled object: %v", err)
	}
	if info.Size != total {
		t.Errorf("storage holds %d bytes, want %d", info.Size, total)
	}

	// And in the right order, which is the one thing part numbers are for.
	obj, err := core.Client.GetObject(ctx, svc.bucketName, up.ObjectKey, minio.GetObjectOptions{})
	if err != nil {
		t.Fatalf("get the assembled object: %v", err)
	}
	defer obj.Close()
	got, err := io.ReadAll(obj)
	if err != nil {
		t.Fatalf("read the assembled object: %v", err)
	}
	want := append(append([]byte{}, partOne...), partTwo...)
	if !bytes.Equal(got, want) {
		t.Errorf("the object did not assemble in part order (first mismatch at %d)", firstDiff(got, want))
	}
}

// An interrupted upload has to be reclaimable, or its parts sit in storage for
// ever without ever appearing as an object.
func TestLiveAbortReclaimsTheParts(t *testing.T) {
	svc, core := liveService(t)
	ctx := context.Background()

	const total = DefaultPartSize + 4096
	req, orgID, userID := liveChatUpload(t, svc, total)

	up, err := svc.InitiateMultipart(ctx, req)
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	urls, err := svc.SignParts(ctx, orgID, userID, up.ID, []int{1})
	if err != nil {
		t.Fatalf("sign parts: %v", err)
	}
	if code, body := putPart(t, urls[0].URL, bytes.Repeat([]byte("A"), int(up.PartSize))); code != http.StatusOK {
		t.Fatalf("PUT part 1: %d %s", code, body)
	}
	storageID := storageUploadID(t, svc.db, up.ID)

	if err := svc.AbortMultipart(ctx, orgID, userID, up.ID); err != nil {
		t.Fatalf("abort: %v", err)
	}

	if _, err := core.ListObjectParts(ctx, svc.bucketName, up.ObjectKey, storageID, 0, 100); err == nil {
		t.Error("the parts are still in storage after an abort")
	}
	if _, err := core.StatObject(ctx, svc.bucketName, up.ObjectKey, minio.StatObjectOptions{}); err == nil {
		t.Error("aborting produced an object")
		_ = core.RemoveObject(ctx, svc.bucketName, up.ObjectKey, minio.RemoveObjectOptions{})
	}
}

// The hourly sweep is the only thing that frees the parts of an upload nobody
// came back to. Against a real store, because "the parts are gone" is a claim
// about the store.
func TestLiveSweepReclaimsAbandonedParts(t *testing.T) {
	svc, core := liveService(t)
	ctx := context.Background()

	const total = DefaultPartSize + 4096
	req, orgID, userID := liveChatUpload(t, svc, total)

	up, err := svc.InitiateMultipart(ctx, req)
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	urls, err := svc.SignParts(ctx, orgID, userID, up.ID, []int{1})
	if err != nil {
		t.Fatalf("sign parts: %v", err)
	}
	if code, body := putPart(t, urls[0].URL, bytes.Repeat([]byte("A"), int(up.PartSize))); code != http.StatusOK {
		t.Fatalf("PUT part 1: %d %s", code, body)
	}
	storageID := storageUploadID(t, svc.db, up.ID)
	if _, err := svc.db.Exec(ctx,
		`UPDATE multipart_uploads SET created_at = NOW() - INTERVAL '48 hours' WHERE id = $1`, up.ID); err != nil {
		t.Fatalf("age the upload: %v", err)
	}

	if _, err := svc.SweepAbandonedUploads(ctx, 24*time.Hour); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if _, err := core.ListObjectParts(ctx, svc.bucketName, up.ObjectKey, storageID, 0, 100); err == nil {
		t.Error("the sweep left an abandoned upload's parts consuming storage")
	}
}

// A single PUT still has to work, and its content length still has to bind —
// the multipart work must not have loosened the path most files take.
func TestLiveSinglePutStillPinsItsContentLength(t *testing.T) {
	svc, core := liveService(t)
	ctx := context.Background()

	req, _, _ := liveChatUpload(t, svc, 4096)
	resp, err := svc.GenerateUploadURL(ctx, req)
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	t.Cleanup(func() { _ = core.RemoveObject(ctx, svc.bucketName, resp.ObjectKey, minio.RemoveObjectOptions{}) })

	if code, body := putPart(t, resp.UploadURL, bytes.Repeat([]byte("x"), 8192)); code != http.StatusForbidden {
		t.Errorf("a URL signed for 4096 bytes accepted 8192: %d %s", code, body)
	}
	if code, body := putPart(t, resp.UploadURL, bytes.Repeat([]byte("x"), 4096)); code != http.StatusOK {
		t.Fatalf("PUT the exact 4096 bytes: %d %s", code, body)
	}
	info, err := core.StatObject(ctx, svc.bucketName, resp.ObjectKey, minio.StatObjectOptions{})
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size != 4096 {
		t.Errorf("storage holds %d bytes, want 4096", info.Size)
	}
}

// firstDiff reports where two byte slices part company, for a readable failure.
func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}
