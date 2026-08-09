package calls

import (
	"context"
	"fmt"
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

	list, err := svc.ListCalls(ctx, org.ID, org.OwnerID, "", nil)
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

	list, err := svc.ListCalls(context.Background(), org.ID, org.OwnerID, "", nil)
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
	list, _ := svc.ListCalls(ctx, mine.ID, mine.OwnerID, "", nil)
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

	active, err := svc.ListCalls(ctx, org.ID, org.OwnerID, "active", nil)
	if err != nil {
		t.Fatalf("filter active: %v", err)
	}
	if len(active) != 1 || active[0].ID != inChannel.ID {
		t.Errorf("status filter wrong: %+v", active)
	}

	byChannel, err := svc.ListCalls(ctx, org.ID, org.OwnerID, "", &channel)
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

// A call between two people must not be joinable by everyone who can list it.
func TestOnlyInviteesOrChannelMembersMayJoin(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	invitee := testsupport.SeedUser(t, pool, org, "invited@example.test", "member")
	outsider := testsupport.SeedUser(t, pool, org, "nosy@example.test", "member")

	call, err := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", nil, []string{invitee.String()})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	for _, who := range []struct {
		name string
		id   uuid.UUID
		want bool
	}{
		{"host", org.OwnerID, true},
		{"invitee", invitee, true},
		{"uninvited member of the same org", outsider, false},
	} {
		ok, err := svc.MayJoin(ctx, org.ID, call.ID, who.id)
		if err != nil {
			t.Fatalf("%s: %v", who.name, err)
		}
		if ok != who.want {
			t.Errorf("%s: MayJoin = %v, want %v", who.name, ok, who.want)
		}
	}
}

// A call hung off a channel is open to that channel, so "join the standup"
// works without inviting people one by one.
func TestChannelMembersMayJoinAChannelCall(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	member := testsupport.SeedUser(t, pool, org, "teammate@example.test", "member")
	outsider := testsupport.SeedUser(t, pool, org, "elsewhere@example.test", "member")
	channel := testsupport.SeedChannel(t, pool, org, "standup", org.OwnerID, member)

	call, err := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", &channel, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	ok, err := svc.MayJoin(ctx, org.ID, call.ID, member)
	if err != nil {
		t.Fatalf("member: %v", err)
	}
	if !ok {
		t.Error("a channel member should be able to join the channel's call")
	}

	ok, err = svc.MayJoin(ctx, org.ID, call.ID, outsider)
	if err != nil {
		t.Fatalf("outsider: %v", err)
	}
	if ok {
		t.Error("someone outside the channel must not join its call")
	}
}

// ── Calls started from a channel ──────────────────────────────────────────

func TestOnlyAChannelsMembersCanStartItsCall(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	outsider := testsupport.SeedUser(t, pool, org, "call-outsider@example.test", "member")

	channelID := testsupport.SeedChannel(t, pool, org, "private-call", org.OwnerID)

	member, err := svc.IsChannelMember(ctx, org.ID, channelID, org.OwnerID)
	if err != nil {
		t.Fatalf("member check: %v", err)
	}
	if !member {
		t.Error("the channel's own member was refused")
	}

	stranger, err := svc.IsChannelMember(ctx, org.ID, channelID, outsider)
	if err != nil {
		t.Fatalf("outsider check: %v", err)
	}
	if stranger {
		t.Error("somebody outside a private channel could start its call")
	}
}

func TestAChannelCallReachesEverybodyElseInTheChannel(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	second := testsupport.SeedUser(t, pool, org, "call-b@example.test", "member")
	third := testsupport.SeedUser(t, pool, org, "call-c@example.test", "member")

	channelID := testsupport.SeedChannel(t, pool, org, "group-call", org.OwnerID, second, third)

	invitees, _, err := svc.ChannelMembersFor(ctx, org.ID, channelID, org.OwnerID)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if len(invitees) != 2 {
		t.Fatalf("invitees: got %d want 2", len(invitees))
	}
	for _, id := range invitees {
		if id == org.OwnerID {
			t.Error("the caller was invited to their own call")
		}
	}
}

func TestAChannelRosterIsScopedToTheOrganization(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	theirs := testsupport.SeedOrg(t, pool)

	channelID := testsupport.SeedChannel(t, pool, org, "ours-call", org.OwnerID)

	invitees, _, err := svc.ChannelMembersFor(ctx, theirs.ID, channelID, theirs.OwnerID)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if len(invitees) != 0 {
		t.Errorf("another organization read our channel roster: %d", len(invitees))
	}
}

// A public channel holds the whole organization, so a call started in one must
// not try to ring everybody: a room only holds so many, and each invite is a
// row and a frame.
func TestAChannelCallInviteListIsBounded(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	members := []uuid.UUID{}
	for i := 0; i < MaxChannelCallInvites+5; i++ {
		members = append(members,
			testsupport.SeedUser(t, pool, org, fmt.Sprintf("crowd%d@example.test", i), "member"))
	}
	channelID := testsupport.SeedChannel(t, pool, org, "crowded", append([]uuid.UUID{org.OwnerID}, members...)...)

	invitees, total, err := svc.ChannelMembersFor(ctx, org.ID, channelID, org.OwnerID)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if len(invitees) != MaxChannelCallInvites {
		t.Errorf("invitees: got %d want the cap of %d", len(invitees), MaxChannelCallInvites)
	}
	// The true size is reported so the caller can be told who was left out.
	if total != len(members) {
		t.Errorf("total: got %d want %d", total, len(members))
	}
	if total-len(invitees) != 5 {
		t.Errorf("uninvited: got %d want 5", total-len(invitees))
	}
}

// ── Who can see which calls ───────────────────────────────────────────────

// A one-to-one call between two people used to be listed to the whole
// organization: who called whom, and when.
func TestAPrivateCallIsNotListedToEverybody(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "callee@example.test", "member")
	bystander := testsupport.SeedUser(t, pool, org, "bystander@example.test", "member")

	call, err := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", nil, []string{other.String()})
	if err != nil {
		t.Fatalf("create call: %v", err)
	}

	for _, who := range []struct {
		name string
		id   uuid.UUID
	}{{"the caller", org.OwnerID}, {"the person called", other}} {
		list, err := svc.ListCalls(ctx, org.ID, who.id, "", nil)
		if err != nil {
			t.Fatalf("list for %s: %v", who.name, err)
		}
		if !containsCall(list, call.ID) {
			t.Errorf("%s cannot see their own call", who.name)
		}
	}

	list, err := svc.ListCalls(ctx, org.ID, bystander, "", nil)
	if err != nil {
		t.Fatalf("list for bystander: %v", err)
	}
	if containsCall(list, call.ID) {
		t.Error("somebody uninvolved can see a private call")
	}
}

// A call started in a channel belongs to that channel's members.
func TestAChannelCallIsVisibleToThatChannel(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	member := testsupport.SeedUser(t, pool, org, "in-channel@example.test", "member")
	outsider := testsupport.SeedUser(t, pool, org, "out-of-channel@example.test", "member")

	channelID := testsupport.SeedChannel(t, pool, org, "standup", org.OwnerID, member)

	call, err := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", &channelID, nil)
	if err != nil {
		t.Fatalf("create call: %v", err)
	}

	list, err := svc.ListCalls(ctx, org.ID, member, "", nil)
	if err != nil {
		t.Fatalf("list for member: %v", err)
	}
	if !containsCall(list, call.ID) {
		t.Error("a channel member cannot see their channel's call")
	}

	hidden, err := svc.ListCalls(ctx, org.ID, outsider, "", nil)
	if err != nil {
		t.Fatalf("list for outsider: %v", err)
	}
	if containsCall(hidden, call.ID) {
		t.Error("somebody outside the channel can see its call")
	}
}

func containsCall(calls []CallModel, id uuid.UUID) bool {
	for _, c := range calls {
		if c.ID == id {
			return true
		}
	}
	return false
}

// The caller needs to know whether a call was turned down or simply rang out,
// and a call nobody picked up should not leave them in an empty room.
func TestACallWithNobodyLeftToAnswerIsOver(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "declines@example.test", "member")

	call, err := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", nil, []string{other.String()})
	if err != nil {
		t.Fatalf("create call: %v", err)
	}

	waiting, err := svc.AnybodyLeftToAnswer(ctx, call.ID, org.OwnerID)
	if err != nil {
		t.Fatalf("outstanding: %v", err)
	}
	if !waiting {
		t.Fatal("somebody was called, so the call is still waiting on them")
	}

	if err := svc.MarkParticipantLeft(ctx, call.ID, other); err != nil {
		t.Fatalf("decline: %v", err)
	}

	waiting, err = svc.AnybodyLeftToAnswer(ctx, call.ID, org.OwnerID)
	if err != nil {
		t.Fatalf("outstanding after decline: %v", err)
	}
	if waiting {
		t.Error("the only person called has gone, so nothing is left to wait for")
	}

	joined, err := svc.SomebodyJoined(ctx, call.ID, org.OwnerID)
	if err != nil {
		t.Fatalf("joined: %v", err)
	}
	if joined {
		t.Error("nobody picked up, so this was never a conversation")
	}
}

// A call somebody actually joined must not be torn down when a second person
// declines it.
func TestACallSomebodyJoinedIsNotDroppedByAnotherDecline(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	answered := testsupport.SeedUser(t, pool, org, "answers@example.test", "member")
	declined := testsupport.SeedUser(t, pool, org, "busy@example.test", "member")

	call, err := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", nil,
		[]string{answered.String(), declined.String()})
	if err != nil {
		t.Fatalf("create call: %v", err)
	}

	if err := svc.MarkParticipantJoined(ctx, call.ID, answered); err != nil {
		t.Fatalf("join: %v", err)
	}
	if err := svc.MarkParticipantLeft(ctx, call.ID, declined); err != nil {
		t.Fatalf("decline: %v", err)
	}

	joined, err := svc.SomebodyJoined(ctx, call.ID, org.OwnerID)
	if err != nil {
		t.Fatalf("joined: %v", err)
	}
	if !joined {
		t.Error("somebody picked up, so the call is real and must not be dropped")
	}
}
