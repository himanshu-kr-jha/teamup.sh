package files

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/testsupport"
)

// accessService builds a Service with a database and a scripted object store.
// Every test here stops at the access check, except the ones that record an
// attachment: recording now measures the object rather than believing the
// client, so it needs a store that says the object is there.
func accessService(t *testing.T) *Service {
	t.Helper()
	s := &Service{store: &fakeStore{size: 1}}
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

// ── Attachment records (ticket 10) ────────────────────────────────────────
//
// Nothing ever inserted into `attachments`, so an uploaded file was
// unreachable once the browser forgot its object key.

func TestRecordedAttachmentCanBeListedAndResolved(t *testing.T) {
	svc := accessService(t)
	pool := svc.db
	ctx := context.Background()

	org := testsupport.SeedOrg(t, pool)
	channel := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)
	key := "org-" + org.ID.String() + "/chat/" + channel.String() + "/20260101T000000Z-notes.pdf"
	svc.store = &fakeStore{size: 2048}

	att, err := svc.RecordAttachment(ctx, AttachmentRecord{
		OrgID: org.ID, UploadedBy: org.OwnerID,
		Category: "chat", RefID: channel.String(), ObjectKey: key,
		Filename: "notes.pdf", ContentType: "application/pdf", SizeBytes: 2048,
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if att.ID == uuid.Nil {
		t.Fatal("attachment was not persisted")
	}

	listed, err := svc.ListAttachments(ctx, org.ID, "chat", channel.String())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 || listed[0].Filename != "notes.pdf" || listed[0].SizeBytes != 2048 {
		t.Fatalf("attachment not listed correctly: %+v", listed)
	}
}

func TestRecordingAnAttachmentTwiceUpdatesRatherThanDuplicating(t *testing.T) {
	svc := accessService(t)
	pool := svc.db
	ctx := context.Background()

	org := testsupport.SeedOrg(t, pool)
	channel := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)
	key := "org-" + org.ID.String() + "/chat/" + channel.String() + "/20260101T000000Z-notes.pdf"

	rec := AttachmentRecord{
		OrgID: org.ID, UploadedBy: org.OwnerID,
		Category: "chat", RefID: channel.String(), ObjectKey: key,
		Filename: "notes.pdf", ContentType: "application/pdf", SizeBytes: 1,
	}
	if _, err := svc.RecordAttachment(ctx, rec); err != nil {
		t.Fatalf("first: %v", err)
	}
	// The object grew in storage; re-recording must pick that up.
	svc.store = &fakeStore{size: 4096}
	if _, err := svc.RecordAttachment(ctx, rec); err != nil {
		t.Fatalf("second: %v", err)
	}

	listed, _ := svc.ListAttachments(ctx, org.ID, "chat", channel.String())
	if len(listed) != 1 {
		t.Fatalf("object key is unique, so re-recording must update: got %d", len(listed))
	}
	if listed[0].SizeBytes != 4096 {
		t.Errorf("re-recording should refresh the metadata, got %d", listed[0].SizeBytes)
	}
}

// Recording is an authorisation boundary too: the same checks as issuing the
// upload URL apply, or a caller could register a row against someone else's
// object.
func TestRecordingAnAttachmentIsAccessChecked(t *testing.T) {
	svc := accessService(t)
	pool := svc.db
	ctx := context.Background()

	org := testsupport.SeedOrg(t, pool)
	theirs := testsupport.SeedOrg(t, pool)
	theirChannel := testsupport.SeedChannel(t, pool, theirs, "theirs", theirs.OwnerID)
	theirKey := "org-" + theirs.ID.String() + "/chat/" + theirChannel.String() + "/20260101T000000Z-x.pdf"

	_, err := svc.RecordAttachment(ctx, AttachmentRecord{
		OrgID: org.ID, UploadedBy: org.OwnerID,
		Category: "chat", RefID: theirChannel.String(), ObjectKey: theirKey,
		Filename: "x.pdf", ContentType: "application/pdf", SizeBytes: 1,
	})
	if err == nil {
		t.Fatal("recording an attachment against another organization must be refused")
	}
}

func TestListAttachmentsIsScopedToTheOrganization(t *testing.T) {
	svc := accessService(t)
	pool := svc.db
	ctx := context.Background()

	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedOrg(t, pool)
	channel := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)
	key := "org-" + org.ID.String() + "/chat/" + channel.String() + "/20260101T000000Z-notes.pdf"

	if _, err := svc.RecordAttachment(ctx, AttachmentRecord{
		OrgID: org.ID, UploadedBy: org.OwnerID,
		Category: "chat", RefID: channel.String(), ObjectKey: key,
		Filename: "notes.pdf", ContentType: "application/pdf", SizeBytes: 1,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	listed, err := svc.ListAttachments(ctx, other.ID, "chat", channel.String())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 0 {
		t.Errorf("another organization must not see these attachments: %+v", listed)
	}
}

// The object key encodes the parent it was issued for. Trusting the body
// instead let a member re-file someone else's upload under a channel only
// they can read, renaming it in the process.
func TestRecordAttachmentRejectsAMismatchedParent(t *testing.T) {
	svc := accessService(t)
	pool := svc.db
	ctx := context.Background()

	org := testsupport.SeedOrg(t, pool)
	shared := testsupport.SeedChannel(t, pool, org, "shared", org.OwnerID)
	private := testsupport.SeedChannel(t, pool, org, "private", org.OwnerID)
	key := "org-" + org.ID.String() + "/chat/" + shared.String() + "/20260101T000000Z-notes.pdf"

	// Key says `shared`, body claims `private`.
	_, err := svc.RecordAttachment(ctx, AttachmentRecord{
		OrgID: org.ID, UploadedBy: org.OwnerID,
		Category: "chat", RefID: private.String(), ObjectKey: key,
		Filename: "notes.pdf", ContentType: "application/pdf", SizeBytes: 1,
	})
	if err == nil {
		t.Fatal("a parent that disagrees with the object key must be refused")
	}
}

func TestRecordAttachmentCannotHijackSomeoneElsesUpload(t *testing.T) {
	svc := accessService(t)
	pool := svc.db
	ctx := context.Background()

	org := testsupport.SeedOrg(t, pool)
	thief := testsupport.SeedUser(t, pool, org, "thief@example.test", "member")
	channel := testsupport.SeedChannel(t, pool, org, "shared", org.OwnerID, thief)
	key := "org-" + org.ID.String() + "/chat/" + channel.String() + "/20260101T000000Z-salaries.xlsx"

	rec := AttachmentRecord{
		OrgID: org.ID, UploadedBy: org.OwnerID,
		Category: "chat", RefID: channel.String(), ObjectKey: key,
		Filename: "salaries.xlsx", ContentType: "application/vnd.ms-excel", SizeBytes: 100,
	}
	if _, err := svc.RecordAttachment(ctx, rec); err != nil {
		t.Fatalf("owner records: %v", err)
	}

	// Another member of the same channel re-records the same object key.
	rec.UploadedBy = thief
	rec.Filename = "harmless.txt"
	if _, err := svc.RecordAttachment(ctx, rec); err == nil {
		t.Fatal("re-recording another member's object must be refused")
	}

	listed, _ := svc.ListAttachments(ctx, org.ID, "chat", channel.String())
	if len(listed) != 1 || listed[0].Filename != "salaries.xlsx" {
		t.Errorf("the original record must be untouched, got %+v", listed)
	}
}
