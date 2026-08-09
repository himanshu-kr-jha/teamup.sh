package files

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teamup/teamup/internal/testsupport"
)

// newService builds a Service with a database but no MinIO client. Removing the
// stored bytes is best effort and skipped when there is no client, so the
// database side of the cleanup is what these exercise.
func newService(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	pool := testsupport.DB(t)
	s := &Service{}
	s.SetDB(pool)
	return s, pool
}

// ── Uploads that were never sent ──────────────────────────────────────────

func seedUnsentUpload(t *testing.T, pool *pgxpool.Pool, orgID, uploader uuid.UUID, channelID, filename string, age time.Duration) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO attachments (id, org_id, uploaded_by, category, ref_id, object_key,
		                         filename, content_type, size_bytes, created_at)
		VALUES (gen_random_uuid(), $1, $2, 'chat', $3, $4, $5, 'text/plain', 4, NOW() - $6::interval)
		RETURNING id
	`, orgID, uploader, channelID, "key/"+uuid.New().String(), filename, age.String()).Scan(&id); err != nil {
		t.Fatalf("seed unsent upload: %v", err)
	}
	return id
}

// Unsent uploads stay listed with the channel's files: hiding them would also
// hide every file uploaded before attachments could belong to a message. The
// sweep is what stops them accumulating.
func TestDiscardingAnUnsentUploadRemovesIt(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	channelID := uuid.New().String()

	id := seedUnsentUpload(t, pool, org.ID, org.OwnerID, channelID, "discard.txt", 0)

	if err := svc.DiscardUpload(ctx, org.ID, org.OwnerID, id); err != nil {
		t.Fatalf("discard: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM attachments WHERE id = $1`, id).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Error("the row survived being discarded")
	}
}

func TestOnlyTheUploaderCanDiscardTheirUpload(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	somebody := testsupport.SeedUser(t, pool, org, "not-yours@example.test", "member")
	channelID := uuid.New().String()

	id := seedUnsentUpload(t, pool, org.ID, org.OwnerID, channelID, "mine.txt", 0)

	if err := svc.DiscardUpload(ctx, org.ID, somebody, id); err == nil {
		t.Error("somebody else discarded an upload that was not theirs")
	}
}

func TestTheSweepOnlyTakesOldUnsentUploads(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	channelID := uuid.New().String()

	old := seedUnsentUpload(t, pool, org.ID, org.OwnerID, channelID, "old.txt", 48*time.Hour)
	fresh := seedUnsentUpload(t, pool, org.ID, org.OwnerID, channelID, "fresh.txt", time.Minute)

	// One that was actually sent, however old, is part of the conversation.
	sent := seedUnsentUpload(t, pool, org.ID, org.OwnerID, channelID, "sent.txt", 48*time.Hour)
	var messageID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO channels (id, org_id, name, type, created_by, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, 'sweep', 'public', $2, NOW(), NOW())
		RETURNING id
	`, org.ID, org.OwnerID).Scan(&messageID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	var msgID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO messages (id, org_id, channel_id, user_id, content, created_at)
		VALUES (gen_random_uuid(), $1, $2, $3, 'here', NOW()) RETURNING id
	`, org.ID, messageID, org.OwnerID).Scan(&msgID); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE attachments SET message_id = $2 WHERE id = $1`, sent, msgID); err != nil {
		t.Fatalf("attach: %v", err)
	}

	removed, err := svc.SweepAbandonedUploads(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed %d, want 1", removed)
	}

	survives := func(id uuid.UUID) bool {
		var n int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM attachments WHERE id = $1`, id).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n == 1
	}
	if survives(old) {
		t.Error("an old unsent upload survived the sweep")
	}
	if !survives(fresh) {
		t.Error("the sweep took a recent draft somebody may still be writing")
	}
	if !survives(sent) {
		t.Error("the sweep took a file that had been sent")
	}
}
