package files

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/testsupport"
)

// accessService builds a Service with a database but no MinIO client: every
// test here stops at the access check, before any object storage call.
func accessService(t *testing.T) *Service {
	t.Helper()
	s := &Service{}
	s.SetDB(testsupport.DB(t))
	return s
}

func TestUploadIsRefusedForAnotherOrganization(t *testing.T) {
	svc := accessService(t)
	pool := svc.db
	ctx := context.Background()

	mine := testsupport.SeedOrg(t, pool)
	theirs := testsupport.SeedOrg(t, pool)
	channel := testsupport.SeedChannel(t, pool, theirs, "theirs", theirs.OwnerID)

	// A member of one organization naming another organization's channel.
	err := svc.validateAccess(ctx, UploadRequest{
		OrgID:    theirs.ID.String(),
		UserID:   mine.OwnerID.String(),
		Category: "chat",
		RefID:    channel.String(),
		Filename: "notes.txt",
		Size:     10,
	})
	if err == nil {
		t.Fatal("uploading into another organization must be refused")
	}
}

func TestUploadIsRefusedWithoutAccessToTheParent(t *testing.T) {
	svc := accessService(t)
	pool := svc.db
	ctx := context.Background()

	org := testsupport.SeedOrg(t, pool)
	outsider := testsupport.SeedUser(t, pool, org, "outsider@example.test", "member")
	// Channel the outsider is not a member of.
	channel := testsupport.SeedChannel(t, pool, org, "private", org.OwnerID)

	err := svc.validateAccess(ctx, UploadRequest{
		OrgID:    org.ID.String(),
		UserID:   outsider.String(),
		Category: "chat",
		RefID:    channel.String(),
		Filename: "notes.txt",
		Size:     10,
	})
	if err == nil {
		t.Fatal("a non-member must not be able to attach files to a channel")
	}
}

func TestUploadIsAllowedForAChannelMember(t *testing.T) {
	svc := accessService(t)
	pool := svc.db
	ctx := context.Background()

	org := testsupport.SeedOrg(t, pool)
	channel := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	if err := svc.validateAccess(ctx, UploadRequest{
		OrgID:    org.ID.String(),
		UserID:   org.OwnerID.String(),
		Category: "chat",
		RefID:    channel.String(),
		Filename: "notes.txt",
		Size:     10,
	}); err != nil {
		t.Fatalf("a channel member should be allowed to attach: %v", err)
	}
}

func TestUploadRejectsUnknownCategory(t *testing.T) {
	svc := accessService(t)
	org := testsupport.SeedOrg(t, svc.db)

	if err := svc.validateAccess(context.Background(), UploadRequest{
		OrgID:    org.ID.String(),
		UserID:   org.OwnerID.String(),
		Category: "../../etc",
		RefID:    uuid.New().String(),
		Filename: "x",
		Size:     1,
	}); err == nil {
		t.Fatal("an unknown category must be refused")
	}
}

func TestDownloadIsRefusedForAnotherOrganizationsObject(t *testing.T) {
	svc := accessService(t)
	pool := svc.db
	ctx := context.Background()

	mine := testsupport.SeedOrg(t, pool)
	theirs := testsupport.SeedOrg(t, pool)
	theirChannel := testsupport.SeedChannel(t, pool, theirs, "theirs", theirs.OwnerID)
	theirKey := "org-" + theirs.ID.String() + "/chat/" + theirChannel.String() + "/20260101T000000Z-secret.pdf"

	err := svc.validateObjectAccess(ctx, mine.ID.String(), mine.OwnerID.String(), theirKey)
	if err == nil {
		t.Fatal("an object key from another organization must be refused")
	}
	// The refusal must not disclose whether that object exists.
	if strings.Contains(strings.ToLower(err.Error()), "not found") {
		t.Errorf("refusal should not reveal existence, got: %v", err)
	}
}

func TestDownloadIsRefusedForATraversalKey(t *testing.T) {
	svc := accessService(t)
	org := testsupport.SeedOrg(t, svc.db)

	for _, key := range []string{
		"org-" + org.ID.String() + "/chat/../../org-other/secret.pdf",
		"../org-other/secret.pdf",
		"org-" + org.ID.String() + "/chat/x/..%2f..%2fsecret",
	} {
		if err := svc.validateObjectAccess(context.Background(), org.ID.String(), org.OwnerID.String(), key); err == nil {
			t.Errorf("traversal key %q must be refused", key)
		}
	}
}

func TestDownloadIsAllowedForOwnOrganizationObject(t *testing.T) {
	svc := accessService(t)
	pool := svc.db
	ctx := context.Background()

	org := testsupport.SeedOrg(t, pool)
	channel := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)
	key := "org-" + org.ID.String() + "/chat/" + channel.String() + "/20260101T000000Z-notes.txt"

	if err := svc.validateObjectAccess(ctx, org.ID.String(), org.OwnerID.String(), key); err != nil {
		t.Fatalf("a member should be able to download from their own channel: %v", err)
	}
}

func TestDownloadIsRefusedForANonMemberOfTheParentChannel(t *testing.T) {
	svc := accessService(t)
	pool := svc.db
	ctx := context.Background()

	org := testsupport.SeedOrg(t, pool)
	outsider := testsupport.SeedUser(t, pool, org, "outsider2@example.test", "member")
	channel := testsupport.SeedChannel(t, pool, org, "private", org.OwnerID)
	key := "org-" + org.ID.String() + "/chat/" + channel.String() + "/20260101T000000Z-notes.txt"

	if err := svc.validateObjectAccess(ctx, org.ID.String(), outsider.String(), key); err == nil {
		t.Fatal("a non-member must not be able to download a channel attachment")
	}
}

// Task attachments follow the task's organization.
func TestTaskAttachmentAccessFollowsTheTask(t *testing.T) {
	svc := accessService(t)
	pool := svc.db
	ctx := context.Background()

	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedOrg(t, pool)

	taskID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO tasks (id, org_id, reporter_id, title, status, priority, created_at)
		VALUES ($1, $2, $3, 'Task', 'todo', 'medium', NOW())
	`, taskID, org.ID, org.OwnerID); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	if err := svc.validateAccess(ctx, UploadRequest{
		OrgID: org.ID.String(), UserID: org.OwnerID.String(),
		Category: "tasks", RefID: taskID.String(), Filename: "spec.pdf", Size: 5,
	}); err != nil {
		t.Errorf("a member should be able to attach to their org's task: %v", err)
	}

	if err := svc.validateAccess(ctx, UploadRequest{
		OrgID: other.ID.String(), UserID: other.OwnerID.String(),
		Category: "tasks", RefID: taskID.String(), Filename: "spec.pdf", Size: 5,
	}); err == nil {
		t.Error("attaching to another organization's task must be refused")
	}
}

// Avatars are keyed by the user themselves rather than a parent row.
func TestAvatarUploadIsLimitedToTheUploader(t *testing.T) {
	svc := accessService(t)
	pool := svc.db
	ctx := context.Background()

	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "other@example.test", "member")

	if err := svc.validateAccess(ctx, UploadRequest{
		OrgID: org.ID.String(), UserID: org.OwnerID.String(),
		Category: "avatars", RefID: org.OwnerID.String(), Filename: "me.png", Size: 5,
	}); err != nil {
		t.Errorf("a user should be able to set their own avatar: %v", err)
	}

	if err := svc.validateAccess(ctx, UploadRequest{
		OrgID: org.ID.String(), UserID: org.OwnerID.String(),
		Category: "avatars", RefID: other.String(), Filename: "them.png", Size: 5,
	}); err == nil {
		t.Error("a user must not overwrite someone else's avatar")
	}
}

func TestUploadRejectsMalformedIdentifiers(t *testing.T) {
	svc := accessService(t)
	org := testsupport.SeedOrg(t, svc.db)

	if err := svc.validateAccess(context.Background(), UploadRequest{
		OrgID: org.ID.String(), UserID: org.OwnerID.String(),
		Category: "chat", RefID: "not-a-uuid", Filename: "x", Size: 1,
	}); err == nil {
		t.Fatal("a non-uuid parent reference must be refused")
	}
}
