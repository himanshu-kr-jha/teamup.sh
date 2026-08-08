package calls

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teamup/teamup/internal/testsupport"
)

// newService builds a service with no LiveKit credentials, so CreateCall takes
// its stub-room path and every test exercises the database behaviour.
func newService(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	pool := testsupport.DB(t)
	return NewService(pool, Config{}), pool
}

// CreateCall used to build the LiveKit room and return a struct without ever
// writing a row, so a call left no trace at all.
func TestCreatedCallIsPersistedAndListed(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	title := "Standup"
	call, err := svc.CreateCall(ctx, org.ID, org.OwnerID, &title, "video", nil, nil)
	if err != nil {
		t.Fatalf("create call: %v", err)
	}

	loaded, err := svc.GetCall(ctx, org.ID, call.ID)
	if err != nil {
		t.Fatalf("get call: %v", err)
	}
	if loaded.Title == nil || *loaded.Title != title {
		t.Errorf("title not persisted: %+v", loaded.Title)
	}
	if loaded.Status != "active" {
		t.Errorf("a new call should be active, got %q", loaded.Status)
	}
	if loaded.StartedAt == nil {
		t.Error("startedAt should be stamped")
	}
	if loaded.CreatedByName == "" {
		t.Error("creator name should be loaded for rendering")
	}

	list, err := svc.ListCalls(ctx, org.ID, "", nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("call should appear in history, got %d", len(list))
	}
}

func TestListCallsIsEmptyForANewOrganization(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)

	list, err := svc.ListCalls(context.Background(), org.ID, "", nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected no calls, got %d", len(list))
	}
}

func TestCreatorIsRecordedAsHostAndInviteesAsParticipants(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	mate := testsupport.SeedUser(t, pool, org, "invitee@example.test", "member")

	call, err := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", nil,
		[]string{mate.String()})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	loaded, err := svc.GetCall(ctx, org.ID, call.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if loaded.ParticipantCount != 2 {
		t.Fatalf("expected host plus invitee, got %d", loaded.ParticipantCount)
	}

	var host string
	for _, p := range loaded.Participants {
		if p.UserID == org.OwnerID {
			host = p.Role
		}
	}
	if host != "host" {
		t.Errorf("creator should be host, got %q", host)
	}
}

func TestInviteeFromAnotherOrganizationIsIgnored(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedOrg(t, pool)

	call, err := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", nil,
		[]string{other.OwnerID.String()})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	loaded, _ := svc.GetCall(ctx, org.ID, call.ID)
	if loaded.ParticipantCount != 1 {
		t.Errorf("an outsider must not be added as a participant, got %d", loaded.ParticipantCount)
	}
}

func TestCallsDoNotCrossOrganizations(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	mine := testsupport.SeedOrg(t, pool)
	theirs := testsupport.SeedOrg(t, pool)

	call, err := svc.CreateCall(ctx, theirs.ID, theirs.OwnerID, nil, "video", nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.GetCall(ctx, mine.ID, call.ID); err == nil {
		t.Error("a call from another organization must not be readable")
	}
	list, _ := svc.ListCalls(ctx, mine.ID, "", nil)
	if len(list) != 0 {
		t.Errorf("history leaked another organization's calls: %+v", list)
	}
}

// Without this the call stayed "active" forever and never became history.
func TestEndingACallClosesTheRecord(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	call, _ := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", nil, nil)
	if err := svc.MarkParticipantJoined(ctx, call.ID, org.OwnerID); err != nil {
		t.Fatalf("join: %v", err)
	}
	time.Sleep(1100 * time.Millisecond) // so duration is measurably non-zero

	if err := svc.MarkCallEnded(ctx, org.ID, call.ID); err != nil {
		t.Fatalf("end: %v", err)
	}

	loaded, _ := svc.GetCall(ctx, org.ID, call.ID)
	if loaded.Status != "ended" {
		t.Errorf("status: got %q want ended", loaded.Status)
	}
	if loaded.EndedAt == nil {
		t.Error("endedAt should be stamped")
	}
	if loaded.DurationSeconds == nil || *loaded.DurationSeconds < 1 {
		t.Errorf("duration should be recorded, got %v", loaded.DurationSeconds)
	}
	for _, p := range loaded.Participants {
		if p.JoinedAt != nil && p.LeftAt == nil {
			t.Errorf("participant %s should have been marked as left", p.UserName)
		}
	}

	// Ending twice is refused rather than re-stamping the record.
	if err := svc.MarkCallEnded(ctx, org.ID, call.ID); err == nil {
		t.Error("ending an already-ended call should report not-found")
	}
}

func TestListCallsFiltersByStatusAndChannel(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	channel := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	inChannel, _ := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", &channel, nil)
	standalone, _ := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "audio", nil, nil)
	if err := svc.MarkCallEnded(ctx, org.ID, standalone.ID); err != nil {
		t.Fatalf("end: %v", err)
	}

	active, err := svc.ListCalls(ctx, org.ID, "active", nil)
	if err != nil {
		t.Fatalf("filter active: %v", err)
	}
	if len(active) != 1 || active[0].ID != inChannel.ID {
		t.Errorf("status filter wrong: %+v", active)
	}

	byChannel, err := svc.ListCalls(ctx, org.ID, "", &channel)
	if err != nil {
		t.Fatalf("filter channel: %v", err)
	}
	if len(byChannel) != 1 || byChannel[0].ID != inChannel.ID {
		t.Errorf("channel filter wrong: %+v", byChannel)
	}
}

func TestParticipantAttendanceIsTracked(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	call, _ := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", nil, nil)

	if err := svc.MarkParticipantJoined(ctx, call.ID, org.OwnerID); err != nil {
		t.Fatalf("join: %v", err)
	}
	loaded, _ := svc.GetCall(ctx, org.ID, call.ID)
	if loaded.Participants[0].JoinedAt == nil {
		t.Fatal("joinedAt should be recorded")
	}

	if err := svc.MarkParticipantLeft(ctx, call.ID, org.OwnerID); err != nil {
		t.Fatalf("leave: %v", err)
	}
	loaded, _ = svc.GetCall(ctx, org.ID, call.ID)
	if loaded.Participants[0].LeftAt == nil {
		t.Error("leftAt should be recorded")
	}
}

// Recording (ticket 12). Without an Egress client configured the request must
// fail clearly rather than pretend to have started.
func TestStartRecordingWithoutEgressReportsUnavailable(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	call, _ := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", nil, nil)

	if _, err := svc.StartRecording(ctx, org.ID, call.ID); err == nil {
		t.Fatal("recording must fail when it is not configured")
	}

	loaded, _ := svc.GetCall(ctx, org.ID, call.ID)
	if loaded.RecordingStatus != nil {
		t.Errorf("a failed start must leave the call untouched, got %v", *loaded.RecordingStatus)
	}
}

func TestStartRecordingOnAnEndedCallIsRefused(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	call, _ := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", nil, nil)
	if err := svc.MarkCallEnded(ctx, org.ID, call.ID); err != nil {
		t.Fatalf("end: %v", err)
	}

	if _, err := svc.StartRecording(ctx, org.ID, call.ID); err == nil {
		t.Fatal("recording an ended call must be refused")
	}
}

func TestStopRecordingIsANoOpWhenNotRecording(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	call, _ := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", nil, nil)

	if err := svc.StopRecording(ctx, org.ID, call.ID); err != nil {
		t.Fatalf("stopping a call that is not recording should be harmless: %v", err)
	}
}

func TestGetCallUnknownIsNotFound(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)

	if _, err := svc.GetCall(context.Background(), org.ID, uuid.New()); err == nil {
		t.Fatal("an unknown call must report not-found")
	}
}
