package files

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"

	"github.com/teamup/teamup/internal/testsupport"
)

// fakeStore stands in for the bytes in object storage. Nothing here reaches a
// real MinIO, so what the store reports about an object is scripted.
type fakeStore struct {
	size    int64
	missing bool
	removed []string
}

func (f *fakeStore) StatObject(_ context.Context, _, object string, _ minio.StatObjectOptions) (minio.ObjectInfo, error) {
	if f.missing {
		return minio.ObjectInfo{}, minio.ErrorResponse{Code: "NoSuchKey", StatusCode: 404}
	}
	return minio.ObjectInfo{Key: object, Size: f.size}, nil
}

func (f *fakeStore) RemoveObject(_ context.Context, _, object string, _ minio.RemoveObjectOptions) error {
	f.removed = append(f.removed, object)
	return nil
}

func (f *fakeStore) BucketExists(_ context.Context, _ string) (bool, error) { return true, nil }

// browserFacingConfig is the shape a production deployment has to take: the
// browser reaches object storage through the site's own TLS origin, while this
// process talks to MinIO over the compose network.
func browserFacingConfig() Config {
	return Config{
		Endpoint:         "teamup.example.test",
		Secure:           true,
		InternalEndpoint: "minio:9000",
		InternalSecure:   false,
		AccessKey:        "an-access-key",
		SecretKey:        "a-secret-key-that-is-long-enough",
		Bucket:           "teamup-files",
	}
}

// uploadService wires a real presigning client to the test database. Presigning
// touches no network, so the only live dependency is Postgres for the access
// check.
func uploadService(t *testing.T, cfg Config) (*Service, *pgxpool.Pool) {
	t.Helper()
	pool := testsupport.DB(t)
	s := NewService(cfg)
	s.SetDB(pool)
	s.store = &fakeStore{size: 4}
	return s, pool
}

// chatUpload is an upload request a channel member is entitled to make.
func chatUpload(t *testing.T, pool *pgxpool.Pool, size int64) UploadRequest {
	t.Helper()
	org := testsupport.SeedOrg(t, pool)
	channel := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)
	return UploadRequest{
		OrgID:       org.ID.String(),
		UserID:      org.OwnerID.String(),
		Category:    "chat",
		RefID:       channel.String(),
		Filename:    "notes.pdf",
		ContentType: "application/pdf",
		Size:        size,
	}
}

// ── The network path a browser actually takes ─────────────────────────────

// The presigned URL is opened by the browser, not by this process. A Docker
// hostname or a plain-http URL on an https page is not reachable at all.
func TestUploadURLIsReachableFromABrowserOverTLS(t *testing.T) {
	svc, pool := uploadService(t, browserFacingConfig())
	req := chatUpload(t, pool, 1024)

	resp, err := svc.GenerateUploadURL(context.Background(), req)
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	u, err := url.Parse(resp.UploadURL)
	if err != nil {
		t.Fatalf("parse upload url: %v", err)
	}
	if u.Scheme != "https" {
		t.Errorf("scheme is %q; an https page blocks a plain-http upload as mixed content", u.Scheme)
	}
	if u.Host != "teamup.example.test" {
		t.Errorf("host is %q, want the browser-facing origin", u.Host)
	}
	if strings.Contains(resp.UploadURL, "minio:9000") {
		t.Errorf("the internal hostname leaked into a URL the browser has to resolve: %s", resp.UploadURL)
	}
	// Path-style addressing: nginx has to route on a stable prefix, and the
	// signature covers the path, so it must be <bucket>/<key> with no rewrite.
	wantPrefix := "/" + svc.bucketName + "/"
	if !strings.HasPrefix(u.Path, wantPrefix) {
		t.Errorf("path is %q, want path-style under %q", u.Path, wantPrefix)
	}
	if !strings.HasSuffix(u.Path, "notes.pdf") {
		t.Errorf("path is %q, want it to end in the object key", u.Path)
	}
}

// Object storage calls this process makes itself should stay on the internal
// network rather than hairpinning back through the public origin.
func TestObjectStorageCallsStayOnTheInternalNetwork(t *testing.T) {
	svc := NewService(browserFacingConfig())
	if svc.internal == nil {
		t.Fatal("no internal client was built")
	}
	if got := svc.internal.EndpointURL().String(); got != "http://minio:9000" {
		t.Errorf("internal endpoint is %q, want http://minio:9000", got)
	}
}

// With no internal endpoint configured there is only one address, and it has to
// be used for both.
func TestASingleEndpointServesBothSides(t *testing.T) {
	cfg := browserFacingConfig()
	cfg.InternalEndpoint = ""
	svc := NewService(cfg)
	if got := svc.internal.EndpointURL().String(); got != "https://teamup.example.test" {
		t.Errorf("internal endpoint is %q, want it to fall back to the public one", got)
	}
}

// ── The size limit ────────────────────────────────────────────────────────

// The declared size is the only chance to refuse a file before any bytes move:
// the PUT goes straight to object storage and never passes through this app.
func TestUploadOverTheLimitIsRefused(t *testing.T) {
	cfg := browserFacingConfig()
	cfg.MaxUploadBytes = 1 << 20
	svc, pool := uploadService(t, cfg)
	req := chatUpload(t, pool, (1<<20)+1)

	resp, err := svc.GenerateUploadURL(context.Background(), req)
	if err == nil {
		t.Fatalf("a file over the limit was handed an upload URL: %s", resp.UploadURL)
	}
	if !strings.Contains(err.Error(), "1 MB") {
		t.Errorf("the message must say what the limit is, got %q", err)
	}
}

func TestUploadAtTheLimitIsAllowed(t *testing.T) {
	cfg := browserFacingConfig()
	cfg.MaxUploadBytes = 1 << 20
	svc, pool := uploadService(t, cfg)
	req := chatUpload(t, pool, 1<<20)

	if _, err := svc.GenerateUploadURL(context.Background(), req); err != nil {
		t.Fatalf("a file exactly at the limit was refused: %v", err)
	}
}

// An unconfigured deployment still has to have a limit; "no limit" is how one
// employee fills the volume.
func TestThereIsALimitEvenWhenNoneIsConfigured(t *testing.T) {
	svc, pool := uploadService(t, browserFacingConfig())
	req := chatUpload(t, pool, 5<<30) // 5 GB

	if resp, err := svc.GenerateUploadURL(context.Background(), req); err == nil {
		t.Fatalf("5 GB was handed an upload URL with no limit configured: %s", resp.UploadURL)
	}
}

// The limit is worth nothing if the client can declare 1 byte and send five
// gigabytes. Signing the content length into the URL is what makes object
// storage itself refuse the oversized body.
func TestTheUploadURLPinsTheContentLength(t *testing.T) {
	svc, pool := uploadService(t, browserFacingConfig())
	req := chatUpload(t, pool, 4096)

	resp, err := svc.GenerateUploadURL(context.Background(), req)
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	u, err := url.Parse(resp.UploadURL)
	if err != nil {
		t.Fatalf("parse upload url: %v", err)
	}
	signed := u.Query().Get("X-Amz-SignedHeaders")
	if !strings.Contains(signed, "content-length") {
		t.Errorf("X-Amz-SignedHeaders is %q; without content-length the URL accepts a body of any size", signed)
	}
}

// Org isolation: the presign is an entitlement to write into a channel, so it
// must never be issued across an organization boundary.
func TestUploadURLIsNotIssuedForAnotherOrganizationsChannel(t *testing.T) {
	svc, pool := uploadService(t, browserFacingConfig())
	mine := testsupport.SeedOrg(t, pool)
	theirs := testsupport.SeedOrg(t, pool)
	theirChannel := testsupport.SeedChannel(t, pool, theirs, "theirs", theirs.OwnerID)

	resp, err := svc.GenerateUploadURL(context.Background(), UploadRequest{
		OrgID:    theirs.ID.String(),
		UserID:   mine.OwnerID.String(),
		Category: "chat",
		RefID:    theirChannel.String(),
		Filename: "notes.pdf",
		Size:     10,
	})
	if err == nil {
		t.Fatalf("an outsider was given a URL to write into another organization: %s", resp.UploadURL)
	}
}

// ── What actually gets stored ─────────────────────────────────────────────

// size_bytes is what quota and usage reporting are built on. Taking it from the
// request body records whatever the client felt like claiming.
func TestTheRecordedSizeComesFromStorageNotTheClient(t *testing.T) {
	svc, pool := uploadService(t, browserFacingConfig())
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	channel := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)
	key := "org-" + org.ID.String() + "/chat/" + channel.String() + "/20260101T000000Z-clip.mp4"

	svc.store = &fakeStore{size: 4096}

	att, err := svc.RecordAttachment(ctx, AttachmentRecord{
		OrgID: org.ID, UploadedBy: org.OwnerID,
		Category: "chat", RefID: channel.String(), ObjectKey: key,
		Filename: "clip.mp4", ContentType: "video/mp4",
		SizeBytes: 1, // the lie
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if att.SizeBytes != 4096 {
		t.Errorf("recorded %d bytes, want the 4096 that are actually stored", att.SizeBytes)
	}
}

// A row pointing at an object nobody ever uploaded is a broken attachment in
// the conversation for ever.
func TestRecordingAnObjectThatWasNeverUploadedIsRefused(t *testing.T) {
	svc, pool := uploadService(t, browserFacingConfig())
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	channel := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)
	key := "org-" + org.ID.String() + "/chat/" + channel.String() + "/20260101T000000Z-ghost.pdf"

	svc.store = &fakeStore{missing: true}

	if _, err := svc.RecordAttachment(ctx, AttachmentRecord{
		OrgID: org.ID, UploadedBy: org.OwnerID,
		Category: "chat", RefID: channel.String(), ObjectKey: key,
		Filename: "ghost.pdf", ContentType: "application/pdf", SizeBytes: 10,
	}); err == nil {
		t.Fatal("an attachment was recorded for an object that is not in storage")
	}
}

// ── Routes ────────────────────────────────────────────────────────────────

// The multipart pair never worked: initiate handed back an ordinary single-PUT
// URL under a multipart-shaped contract, and complete recorded nothing at all.
// With a single-PUT size limit there is nothing for them to do.
func TestMultipartEndpointsAreGone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	RegisterRoutes(e.Group("/api"), nil)

	for _, r := range e.Routes() {
		if strings.Contains(r.Path, "multipart") {
			t.Errorf("%s %s is still registered but does not do what its name says", r.Method, r.Path)
		}
	}
}

// The client cannot fail fast without knowing the limit.
func TestTheUploadLimitIsDiscoverable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	RegisterRoutes(e.Group("/api"), nil)

	for _, r := range e.Routes() {
		if r.Method == "GET" && r.Path == "/api/files/limits" {
			return
		}
	}
	t.Error("no route tells the client what the upload limit is")
}
