package calls

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teamup/teamup/internal/testsupport"
	apperrors "github.com/teamup/teamup/pkg/errors"
	"github.com/teamup/teamup/pkg/protocol"
)

// newService builds a service with no LiveKit credentials, so CreateCall takes
// its stub-room path and every test exercises the database behaviour.
// markConnected puts in place what LiveKit reporting a connection leaves behind on
// call_participants, for tests about what reads it. attendance_test.go tests the
// webhook that really writes it.
func markConnected(ctx context.Context, pool *pgxpool.Pool, callID, userID uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO call_participants (call_id, user_id, role, joined_at)
		VALUES ($1, $2, 'participant', NOW())
		ON CONFLICT (call_id, user_id) DO UPDATE SET joined_at = NOW(), left_at = NULL
	`, callID, userID)
	return err
}

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
	if err := markConnected(ctx, pool, call.ID, org.OwnerID); err != nil {
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

	if err := markConnected(ctx, pool, call.ID, org.OwnerID); err != nil {
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
	pool := testsupport.DB(t)
	// A cap low enough to reach without seeding a crowd. The bound is
	// configuration, so the test sets it rather than inheriting the default.
	svc := NewService(pool, Config{MaxCallParticipants: 10})
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	members := []uuid.UUID{}
	for i := 0; i < svc.MaxChannelCallInvites()+5; i++ {
		members = append(members,
			testsupport.SeedUser(t, pool, org, fmt.Sprintf("crowd%d@example.test", i), "member"))
	}
	channelID := testsupport.SeedChannel(t, pool, org, "crowded", append([]uuid.UUID{org.OwnerID}, members...)...)

	invitees, total, err := svc.ChannelMembersFor(ctx, org.ID, channelID, org.OwnerID)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if len(invitees) != svc.MaxChannelCallInvites() {
		t.Errorf("invitees: got %d want the cap of %d", len(invitees), svc.MaxChannelCallInvites())
	}
	// The true size is reported so the caller can be told who was left out.
	if total != len(members) {
		t.Errorf("total: got %d want %d", total, len(members))
	}
	if total-len(invitees) != 5 {
		t.Errorf("uninvited: got %d want 5", total-len(invitees))
	}
}

// orgHeadcount is the size this product is built for: an all-hands at a small
// company. A call that cannot hold the company is not a call the company can
// have, so the default ceiling has to clear this with room to spare.
const orgHeadcount = 60

// The whole company in one room is the ordinary case for a startup this size,
// not an edge case. With the ceiling at 50 the invitee query truncated at 49
// and eleven people were never rung — silently, since the roster query
// succeeded and the call started.
func TestAWholeOrganizationCanBeRungIntoOneCall(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	// The owner plus everybody else makes orgHeadcount people.
	members := []uuid.UUID{}
	for i := 0; i < orgHeadcount-1; i++ {
		members = append(members,
			testsupport.SeedUser(t, pool, org, fmt.Sprintf("allhands%d@example.test", i), "member"))
	}

	// A second organization of the same size, whose members must not appear.
	theirs := testsupport.SeedOrg(t, pool)
	theirMembers := []uuid.UUID{}
	for i := 0; i < orgHeadcount-1; i++ {
		theirMembers = append(theirMembers,
			testsupport.SeedUser(t, pool, theirs, fmt.Sprintf("theirs%d@example.test", i), "member"))
	}
	testsupport.SeedChannel(t, pool, theirs, "their-all-hands",
		append([]uuid.UUID{theirs.OwnerID}, theirMembers...)...)

	channelID := testsupport.SeedChannel(t, pool, org, "all-hands",
		append([]uuid.UUID{org.OwnerID}, members...)...)

	invitees, total, err := svc.ChannelMembersFor(ctx, org.ID, channelID, org.OwnerID)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if total != orgHeadcount-1 {
		t.Errorf("total: got %d want %d", total, orgHeadcount-1)
	}
	if len(invitees) != orgHeadcount-1 {
		t.Errorf("invitees: got %d, want the whole company minus the caller (%d) — %d people were never rung",
			len(invitees), orgHeadcount-1, orgHeadcount-1-len(invitees))
	}
	// A room that cannot seat everybody it rang is the same bug one step later.
	if svc.MaxParticipants() < orgHeadcount {
		t.Errorf("room ceiling %d cannot seat an all-hands of %d", svc.MaxParticipants(), orgHeadcount)
	}

	// Nobody from the other organization, and nothing from its channel.
	foreign := map[uuid.UUID]bool{theirs.OwnerID: true}
	for _, id := range theirMembers {
		foreign[id] = true
	}
	for _, id := range invitees {
		if foreign[id] {
			t.Errorf("invitee %s belongs to another organization", id)
		}
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

	if err := markConnected(ctx, pool, call.ID, answered); err != nil {
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

// Muting somebody else can only be done by the media server, so with none
// configured the request has to fail cleanly rather than silently do nothing.
func TestMutingWithoutAMediaServerSaysSo(t *testing.T) {
	svc, _ := newService(t)
	// newService builds a service with no LiveKit client.
	err := svc.MuteParticipant(context.Background(), "call-abc", "somebody")
	if err == nil {
		t.Fatal("muting with no media server should fail")
	}
	if apperrors.HTTPStatus(err) != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", apperrors.HTTPStatus(err))
	}
}

// Somebody who was away when they were called should have their phone ring the
// moment they come back, rather than never learning it happened.
func TestACallStillRingingIsWaitingForSomebodyWhoWasAway(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	away := testsupport.SeedUser(t, pool, org, "away@example.test", "member")

	call, err := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", nil, []string{away.String()})
	if err != nil {
		t.Fatalf("create call: %v", err)
	}

	pending, err := svc.PendingInvitationsFor(ctx, away)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 1 || pending[0].CallID != call.ID {
		t.Fatalf("the call should still be waiting on them, got %+v", pending)
	}
	if pending[0].CallerID != org.OwnerID {
		t.Errorf("the invitation should name who is calling, got %v", pending[0].CallerID)
	}

	// The caller does not wait forever. Past the ringing window it is a missed
	// call, and coming online must not start a phone ringing for it.
	if _, err := pool.Exec(ctx,
		`UPDATE calls SET started_at = NOW() - INTERVAL '10 minutes' WHERE id = $1`, call.ID); err != nil {
		t.Fatalf("age the call: %v", err)
	}
	pending, err = svc.PendingInvitationsFor(ctx, away)
	if err != nil {
		t.Fatalf("pending after the window: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("a call that stopped ringing long ago should not ring now, got %d", len(pending))
	}
}

// Whoever is doing the calling is not waiting for their own phone to ring, and
// a call that was answered is over as an invitation.
func TestPendingInvitationsExcludeTheCallerAndAnsweredCalls(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	callee := testsupport.SeedUser(t, pool, org, "picks-up@example.test", "member")

	call, err := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", nil, []string{callee.String()})
	if err != nil {
		t.Fatalf("create call: %v", err)
	}

	pending, err := svc.PendingInvitationsFor(ctx, org.OwnerID)
	if err != nil {
		t.Fatalf("pending for the caller: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("the caller should not be rung by their own call, got %d", len(pending))
	}

	if err := markConnected(ctx, pool, call.ID, callee); err != nil {
		t.Fatalf("join: %v", err)
	}
	pending, err = svc.PendingInvitationsFor(ctx, callee)
	if err != nil {
		t.Fatalf("pending after joining: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("a call already picked up should not ring again, got %d", len(pending))
	}
}

// recordedEvents stands in for chat, so the test can see what would have been
// written into the conversation without depending on that package.
type recordedEvents struct {
	notes []string
}

func (r *recordedEvents) RecordCallEvent(_ context.Context, _, _, _ uuid.UUID, text string) (uuid.UUID, error) {
	r.notes = append(r.notes, text)
	return uuid.New(), nil
}

// A private call nobody picked up is the one case where the other person has
// no way of finding out unless it is written down.
func TestAPrivateCallNobodyAnsweredIsWrittenIntoTheConversation(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	away := testsupport.SeedUser(t, pool, org, "missed@example.test", "member")

	recorder := &recordedEvents{}
	svc.SetChatRecorder(recorder)

	call, err := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", nil, []string{away.String()})
	if err != nil {
		t.Fatalf("create call: %v", err)
	}

	svc.NoteIfMissed(ctx, call.ID, "no_answer")
	if len(recorder.notes) != 1 {
		t.Fatalf("a missed call should leave a note, got %+v", recorder.notes)
	}

	// Once somebody picks up it is a conversation, not a missed call, however
	// it ends afterwards.
	answered, err := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", nil, []string{away.String()})
	if err != nil {
		t.Fatalf("create second call: %v", err)
	}
	if err := svc.MarkAnswered(ctx, answered.ID); err != nil {
		t.Fatalf("mark answered: %v", err)
	}
	svc.NoteIfMissed(ctx, answered.ID, "no_answer")
	if len(recorder.notes) != 1 {
		t.Errorf("an answered call is not a missed one, got %+v", recorder.notes)
	}
}

// A call placed from a direct message carries that channel, and it is still a
// call between two people — the guard against noting channel calls must not
// swallow it.
func TestAMissedCallFromADirectMessageIsStillNoted(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "dm-missed@example.test", "member")

	dm := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO channels (id, org_id, name, type, created_by, created_at)
		VALUES ($1, $2, 'dm', 'dm', $3, NOW())
	`, dm, org.ID, org.OwnerID); err != nil {
		t.Fatalf("seed dm: %v", err)
	}

	recorder := &recordedEvents{}
	svc.SetChatRecorder(recorder)

	call, err := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", &dm, []string{other.String()})
	if err != nil {
		t.Fatalf("create call: %v", err)
	}
	svc.NoteIfMissed(ctx, call.ID, "no_answer")
	if len(recorder.notes) != 1 {
		t.Fatalf("a call from a direct message that nobody answered is a missed call, got %+v", recorder.notes)
	}
}

// A meeting nobody came to is not a missed call, and must not turn up in
// somebody's private conversation.
func TestAnUnansweredChannelCallIsNotAMissedCall(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "channel-call@example.test", "member")
	ch := testsupport.SeedChannel(t, pool, org, "standup", org.OwnerID, other)

	recorder := &recordedEvents{}
	svc.SetChatRecorder(recorder)

	call, err := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", &ch, []string{other.String()})
	if err != nil {
		t.Fatalf("create call: %v", err)
	}
	svc.NoteIfMissed(ctx, call.ID, "no_answer")
	if len(recorder.notes) != 0 {
		t.Errorf("a channel call should leave nothing in a private conversation, got %+v", recorder.notes)
	}
}

// Turning somebody down and not being there are different things, and the
// conversation should not describe one as the other.
func TestADeclinedCallSaysSoRatherThanCallingItMissed(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "declined-note@example.test", "member")

	recorder := &recordedEvents{}
	svc.SetChatRecorder(recorder)

	call, err := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", nil, []string{other.String()})
	if err != nil {
		t.Fatalf("create call: %v", err)
	}
	svc.NoteIfMissed(ctx, call.ID, "declined")

	if len(recorder.notes) != 1 || recorder.notes[0] != "Call declined" {
		t.Errorf("a declined call should say so, got %+v", recorder.notes)
	}
}

// rungDevices records what would have been put on somebody's lock screen.
type rungDevices struct {
	payloads []protocol.Push
}

func (r *rungDevices) PushToUsers(_ context.Context, _ []uuid.UUID, payload protocol.Push) {
	r.payloads = append(r.payloads, payload)
}

func (r *rungDevices) Notify(context.Context, uuid.UUID, uuid.UUID, string, string, string, map[string]any) error {
	return nil
}

// A call is the one thing that is worthless late, so it has to reach the phone
// itself rather than only a browser tab that happens to still be open.
func TestACallRingsTheDeviceAndStopsWhenItIsOver(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	callee := testsupport.SeedUser(t, pool, org, "rings@example.test", "member")

	rung := &rungDevices{}
	svc.SetNotifier(rung)

	call, err := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", nil, []string{callee.String()})
	if err != nil {
		t.Fatalf("create call: %v", err)
	}
	svc.Ring(ctx, call, org.OwnerID.String(), "QA Alice", []uuid.UUID{callee})

	if len(rung.payloads) != 1 {
		t.Fatalf("the phone should have rung, got %+v", rung.payloads)
	}
	ringing := rung.payloads[0]
	if ringing.Title != "QA Alice" {
		t.Errorf("it should say who is calling, got %q", ringing.Title)
	}
	// It waits to be answered rather than sliding away unseen.
	if !ringing.RequireInteraction {
		t.Error("a call should hold the screen until it is dealt with")
	}
	if ringing.Data["url"] != "/calls/"+call.ID.String() {
		t.Errorf("answering it should open the call, got %v", ringing.Data["url"])
	}

	// And it has to be taken back, or a phone in a pocket shows somebody
	// calling long after they gave up.
	loaded, err := svc.GetCall(ctx, org.ID, call.ID)
	if err != nil {
		t.Fatalf("get call: %v", err)
	}
	svc.AnnounceCallOver(ctx, loaded, "no_answer", "", []string{callee.String()})

	if len(rung.payloads) != 2 {
		t.Fatalf("the ringing should have been called off, got %+v", rung.payloads)
	}
	cancelled := rung.payloads[1]
	if cancelled.Tag != ringing.Tag {
		t.Errorf("it has to name the same notification to replace it: %q vs %q",
			cancelled.Tag, ringing.Tag)
	}
	if cancelled.Data["kind"] != "call.cancelled" {
		t.Errorf("the device is told to take it away, got %v", cancelled.Data["kind"])
	}
}

// A channel filter that could not be read used to be dropped, which listed
// every call the person could see instead of the ones they asked about.
func TestAMalformedChannelFilterIsRefused(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	api.Use(func(c *gin.Context) {
		c.Set("org_id", org.ID.String())
		c.Set("user_id", org.OwnerID.String())
		c.Set("user_roles", []string{"owner"})
		c.Next()
	})
	RegisterRoutes(api, svc)

	for query, want := range map[string]int{
		"?channelId=not-a-uuid":          http.StatusBadRequest,
		"":                               http.StatusOK,
		"?channelId=" + uuid.NewString(): http.StatusOK,
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/calls"+query, nil))
		if w.Code != want {
			t.Errorf("GET /calls%s: want %d, got %d: %s", query, want, w.Code, w.Body.String())
		}
	}
}

func TestTheCallListKeepsOnlyTheNewest(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedOrg(t, pool)
	channel := testsupport.SeedChannel(t, pool, org, "standup", org.OwnerID)

	if _, err := pool.Exec(ctx, `
		INSERT INTO calls (org_id, channel_id, room_name, title, call_type, status, created_by, created_at)
		SELECT $1, $2, 'call-' || i, CASE WHEN i > $4::int THEN 'oldest' ELSE 'newer' END,
		       'video', 'ended', $3, NOW() - (i * interval '1 minute')
		FROM generate_series(1, $4::int + 5) i
	`, org.ID, channel, org.OwnerID, maxListedCalls); err != nil {
		t.Fatalf("seed calls: %v", err)
	}

	list, err := svc.ListCalls(ctx, org.ID, org.OwnerID, "", nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != maxListedCalls {
		t.Fatalf("listed %d calls, want the cap of %d", len(list), maxListedCalls)
	}
	for _, call := range list {
		if call.Title == nil || *call.Title != "newer" {
			t.Fatalf("the cap kept an old call over a newer one: %+v", call.Title)
		}
	}

	foreign, err := svc.ListCalls(ctx, other.ID, other.OwnerID, "", nil)
	if err != nil {
		t.Fatalf("list other: %v", err)
	}
	if len(foreign) != 0 {
		t.Errorf("another organization listed %d of these calls", len(foreign))
	}
}
