package calls

// Ticket 01: attendance and the audit trail come from LiveKit webhooks.
// Decisions 6, 10 and 11 in .scratch/calendar-and-meetings/spec.md.

import (
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"

	"github.com/teamup/teamup/internal/testsupport"
)

func baseTime() time.Time { return time.Now().Add(-2 * time.Hour).Truncate(time.Second) }

// ── signature ─────────────────────────────────────────────────────────────

func TestWebhookWithAnInvalidSignatureIsRejectedAndRecordsNothing(t *testing.T) {
	e := newLK(t)
	guest := e.person(t, "Invited Ivy", "member")
	call := e.call(t, e.org.OwnerID, guest)
	r := e.webhookRouter(t)

	body := []byte(`{"event":"participant_joined","id":"EV_x","createdAt":"` + "1" + `","room":{"name":"` + call.RoomName +
		`"},"participant":{"identity":"` + guest.String() + `","sid":"PA_1","joinedAt":"1"}}`)

	cases := map[string]string{
		"no Authorization header":       "",
		"garbage token":                 "not-a-jwt",
		"signed with the wrong secret":  sign(t, lkKey, "some-other-secret-some-other-secret-00", body),
		"signed with an unknown key":    sign(t, "otherkey", lkSecret, body),
		"signature of a different body": sign(t, lkKey, lkSecret, []byte(`{"event":"room_started"}`)),
	}
	for name, header := range cases {
		w := rawWebhook(t, r, body, header)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: want 401, got %d: %s", name, w.Code, w.Body.String())
		}
	}

	log := getLog(t, e.asOwner(t), call.ID)
	if len(log.Events) != 0 || len(log.Sessions) != 0 {
		t.Errorf("a rejected webhook must record nothing, got %+v", log)
	}
}

func TestWebhookNeedsNoSessionOnlyTheSignature(t *testing.T) {
	e := newLK(t)
	guest := e.person(t, "Invited Ivy", "member")
	call := e.call(t, e.org.OwnerID, guest)

	// webhookRouter has no session middleware at all.
	w := e.deliver(t, joinedEv(call.RoomName, guest, "PA_1", baseTime()))
	if !is2xx(w) {
		t.Fatalf("a correctly signed webhook must be accepted without a session, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWebhookForARoomOrPersonWeDoNotKnowIsAcceptedAndIgnored(t *testing.T) {
	e := newLK(t)
	guest := e.person(t, "Invited Ivy", "member")
	call := e.call(t, e.org.OwnerID, guest)

	// LiveKit retries anything that is not a 2xx, so a room that is not ours,
	// or an identity that is not a user id, must not turn into a retry storm.
	e.mustDeliver(t, joinedEv("room-we-never-made", guest, "PA_1", baseTime()))
	e.mustDeliver(t, joinedEv(call.RoomName, uuid.New(), "PA_2", baseTime())) // a user that does not exist
	ev := joinedEv(call.RoomName, guest, "PA_3", baseTime())
	ev.Participant.Identity = "not-a-user-id"
	e.mustDeliver(t, ev)

	log := getLog(t, e.asOwner(t), call.ID)
	if len(log.Sessions) != 0 {
		t.Errorf("none of those is a member of this call, got sessions %+v", log.Sessions)
	}
}

// ── a token is not attendance (decision 6) ────────────────────────────────

func TestIssuingATokenIsNotAttendance(t *testing.T) {
	e := newLK(t)
	invitee := e.person(t, "Invited Ivy", "member")
	call := e.call(t, e.org.OwnerID, invitee)

	w := doJSON(t, e.as(t, invitee, "member"), http.MethodPost, "/api/calls/"+call.ID.String()+"/token", "")
	wantCode(t, w, http.StatusOK)

	loaded, err := e.svc.GetCall(t.Context(), e.org.ID, call.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range loaded.Participants {
		if p.UserID == invitee && p.JoinedAt != nil {
			t.Errorf("a token is only permission: joinedAt must stay empty until LiveKit reports a connection, got %v", p.JoinedAt)
		}
	}
	log := getLog(t, e.asOwner(t), call.ID)
	if len(log.sessions(invitee)) != 0 || len(log.events("joined", invitee)) != 0 {
		t.Errorf("somebody who never connected was not there, got %+v", log)
	}

	// Now LiveKit says they really connected.
	e.mustDeliver(t, joinedEv(call.RoomName, invitee, "PA_1", baseTime()))
	loaded, _ = e.svc.GetCall(t.Context(), e.org.ID, call.ID)
	for _, p := range loaded.Participants {
		if p.UserID == invitee && p.JoinedAt == nil {
			t.Error("once LiveKit reports the connection, call_participants.joined_at must be set for existing readers")
		}
	}
}

// ── what a join and a leave record ────────────────────────────────────────

func TestJoinAndLeaveAreLoggedAtTheTimesLiveKitReports(t *testing.T) {
	e := newLK(t)
	invitee := e.person(t, "Invited Ivy", "member")
	call := e.call(t, e.org.OwnerID, invitee)
	start := baseTime()

	e.mustDeliver(t, joinedEv(call.RoomName, invitee, "PA_1", start))
	e.mustDeliver(t, leftEv(call.RoomName, invitee, "PA_1", start.Add(12*time.Minute)))

	log := getLog(t, e.asOwner(t), call.ID)
	joins, lefts := log.events("joined", invitee), log.events("left", invitee)
	if len(joins) != 1 || !near(joins[0].At, start) {
		t.Errorf("joined should be logged at LiveKit's time %v, got %+v", start, joins)
	}
	if len(lefts) != 1 || !near(lefts[0].At, start.Add(12*time.Minute)) {
		t.Errorf("left should be logged at LiveKit's time, got %+v", lefts)
	}
	ss := log.sessions(invitee)
	if len(ss) != 1 || !near(ss[0].JoinedAt, start) || ss[0].LeftAt == nil || !near(*ss[0].LeftAt, start.Add(12*time.Minute)) {
		t.Errorf("one closed session expected, got %+v", ss)
	}

	loaded, _ := e.svc.GetCall(t.Context(), e.org.ID, call.ID)
	for _, p := range loaded.Participants {
		if p.UserID == invitee && (p.JoinedAt == nil || p.LeftAt == nil) {
			t.Errorf("call_participants must stay correct for existing readers, got %+v", p)
		}
	}
}

func TestADuplicateWebhookIsRecordedOnce(t *testing.T) {
	e := newLK(t)
	invitee := e.person(t, "Invited Ivy", "member")
	call := e.call(t, e.org.OwnerID, invitee)
	start := baseTime()

	join := joinedEv(call.RoomName, invitee, "PA_1", start)
	e.mustDeliver(t, join)
	e.mustDeliver(t, join) // LiveKit retried: same event id
	left := leftEv(call.RoomName, invitee, "PA_1", start.Add(5*time.Minute))
	e.mustDeliver(t, left)
	e.mustDeliver(t, left)
	e.mustDeliver(t, left)

	log := getLog(t, e.asOwner(t), call.ID)
	if n := len(log.events("joined", invitee)); n != 1 {
		t.Errorf("a retried join is one join, got %d", n)
	}
	if n := len(log.events("left", invitee)); n != 1 {
		t.Errorf("a retried leave is one leave, got %d", n)
	}
	if n := len(log.sessions(invitee)); n != 1 {
		t.Errorf("a retried event must not open another session, got %d", n)
	}
}

func TestALeaveThatArrivesBeforeItsJoinStillMakesOneClosedSession(t *testing.T) {
	e := newLK(t)
	invitee := e.person(t, "Invited Ivy", "member")
	call := e.call(t, e.org.OwnerID, invitee)
	start := baseTime()

	// Webhooks are not delivered in order.
	e.mustDeliver(t, leftEv(call.RoomName, invitee, "PA_1", start.Add(10*time.Minute)))
	e.mustDeliver(t, joinedEv(call.RoomName, invitee, "PA_1", start))

	log := getLog(t, e.asOwner(t), call.ID)
	ss := log.sessions(invitee)
	if len(ss) != 1 {
		t.Fatalf("one session expected, got %+v", ss)
	}
	if !near(ss[0].JoinedAt, start) || ss[0].LeftAt == nil || !near(*ss[0].LeftAt, start.Add(10*time.Minute)) {
		t.Errorf("the session must run from the join to the leave whatever the arrival order, got %+v", ss[0])
	}
	loaded, _ := e.svc.GetCall(t.Context(), e.org.ID, call.ID)
	for _, p := range loaded.Participants {
		if p.UserID == invitee && (p.LeftAt == nil || p.JoinedAt == nil) {
			t.Errorf("the person is not still in the call, got %+v", p)
		}
	}
}

func TestARejoinCreatesASecondSessionAndKeepsTheFirst(t *testing.T) {
	e := newLK(t)
	invitee := e.person(t, "Invited Ivy", "member")
	call := e.call(t, e.org.OwnerID, invitee)
	start := baseTime()

	e.mustDeliver(t, joinedEv(call.RoomName, invitee, "PA_1", start))
	e.mustDeliver(t, leftEv(call.RoomName, invitee, "PA_1", start.Add(5*time.Minute)))
	e.mustDeliver(t, joinedEv(call.RoomName, invitee, "PA_2", start.Add(8*time.Minute)))

	log := getLog(t, e.asOwner(t), call.ID)
	ss := log.sessions(invitee)
	if len(ss) != 2 {
		t.Fatalf("a rejoin is a second session and the first is kept, got %+v", ss)
	}
	open := 0
	for _, s := range ss {
		if s.LeftAt == nil {
			open++
		}
	}
	if open != 1 {
		t.Errorf("exactly the rejoin should be open, got %+v", ss)
	}
	if n := len(log.events("joined", invitee)); n != 2 {
		t.Errorf("both joins are logged, got %d", n)
	}

	loaded, _ := e.svc.GetCall(t.Context(), e.org.ID, call.ID)
	for _, p := range loaded.Participants {
		if p.UserID == invitee && p.LeftAt != nil {
			t.Errorf("somebody who rejoined is in the call, so call_participants.left_at must be empty, got %v", p.LeftAt)
		}
	}
}

func TestOnePersonOnTwoDevicesIsPresentUntilTheLastDeviceLeaves(t *testing.T) {
	e := newLK(t)
	invitee := e.person(t, "Invited Ivy", "member")
	call := e.call(t, e.org.OwnerID, invitee)
	start := baseTime()

	e.mustDeliver(t, joinedEv(call.RoomName, invitee, "PA_phone", start))
	e.mustDeliver(t, joinedEv(call.RoomName, invitee, "PA_laptop", start.Add(time.Minute)))
	e.mustDeliver(t, leftEv(call.RoomName, invitee, "PA_phone", start.Add(5*time.Minute)))

	loaded, _ := e.svc.GetCall(t.Context(), e.org.ID, call.ID)
	for _, p := range loaded.Participants {
		if p.UserID == invitee && p.LeftAt != nil {
			t.Fatalf("the laptop is still connected, so the person has not left; got leftAt %v", p.LeftAt)
		}
	}
	if n := len(getLog(t, e.asOwner(t), call.ID).sessions(invitee)); n != 2 {
		t.Errorf("every device is its own session, got %d", n)
	}

	e.mustDeliver(t, leftEv(call.RoomName, invitee, "PA_laptop", start.Add(9*time.Minute)))
	loaded, _ = e.svc.GetCall(t.Context(), e.org.ID, call.ID)
	for _, p := range loaded.Participants {
		if p.UserID == invitee && (p.LeftAt == nil || !near(*p.LeftAt, start.Add(9*time.Minute))) {
			t.Errorf("after the last device leaves, left_at is that device's time, got %v", p.LeftAt)
		}
	}
}

func TestScreenShareStartAndStopComeFromTrackEventsAndCameraDoesNot(t *testing.T) {
	e := newLK(t)
	invitee := e.person(t, "Invited Ivy", "member")
	call := e.call(t, e.org.OwnerID, invitee)
	start := baseTime()

	e.mustDeliver(t, joinedEv(call.RoomName, invitee, "PA_1", start))
	e.mustDeliver(t, trackEv("track_published", call.RoomName, invitee, "PA_1", livekit.TrackSource_CAMERA, start.Add(time.Second)))
	e.mustDeliver(t, trackEv("track_published", call.RoomName, invitee, "PA_1", livekit.TrackSource_SCREEN_SHARE, start.Add(time.Minute)))
	e.mustDeliver(t, trackEv("track_unpublished", call.RoomName, invitee, "PA_1", livekit.TrackSource_SCREEN_SHARE, start.Add(4*time.Minute)))

	log := getLog(t, e.asOwner(t), call.ID)
	started, stopped := log.events("screen_share_started", invitee), log.events("screen_share_stopped", invitee)
	if len(started) != 1 || !near(started[0].At, start.Add(time.Minute)) {
		t.Errorf("one screen share start at LiveKit's time, got %+v", started)
	}
	if len(stopped) != 1 || !near(stopped[0].At, start.Add(4*time.Minute)) {
		t.Errorf("one screen share stop at LiveKit's time, got %+v", stopped)
	}
	for _, ev := range log.Events {
		if ev.Type != "joined" && ev.Type != "screen_share_started" && ev.Type != "screen_share_stopped" {
			t.Errorf("a camera track must not add an event, got %+v", ev)
		}
	}
}

// ── who may read the log (decision 11) ────────────────────────────────────

func TestWhoMayReadACallLog(t *testing.T) {
	e := newLK(t)
	host := e.person(t, "Host Hana", "member")
	invitee := e.person(t, "Invited Ivy", "member")
	hostsBoss := e.person(t, "Boss Of Host", "manager")
	inviteesBoss := e.person(t, "Boss Of Invitee", "manager")
	otherBoss := e.person(t, "Boss Of Somebody Else", "manager")
	admin := e.person(t, "Admin Ada", "admin")
	stranger := e.person(t, "Stranger Sam", "member")
	elsewhere := e.person(t, "Elsewhere Eli", "member")

	testsupport.SeedTeam(t, e.pool, e.org, "Hosts", &hostsBoss, nil, host)
	testsupport.SeedTeam(t, e.pool, e.org, "Invitees", &inviteesBoss, nil, invitee)
	testsupport.SeedTeam(t, e.pool, e.org, "Elsewhere", &otherBoss, nil, elsewhere)

	call := e.call(t, host, invitee)
	e.mustDeliver(t, joinedEv(call.RoomName, host, "PA_h", baseTime()))
	e.mustDeliver(t, joinedEv(call.RoomName, invitee, "PA_i", baseTime().Add(time.Minute)))

	other := testsupport.SeedOrg(t, e.pool)
	otherOrgRouter := routerFor(t, e.svc, other.ID, other.OwnerID, "owner")

	for _, c := range []struct {
		name   string
		router func() (code int)
		want   int
	}{
		{"the host", func() int { return get(t, e.as(t, host, "member"), call.ID) }, http.StatusOK},
		{"an invitee", func() int { return get(t, e.as(t, invitee, "member"), call.ID) }, http.StatusOK},
		{"the owner", func() int { return get(t, e.asOwner(t), call.ID) }, http.StatusOK},
		{"an org admin", func() int { return get(t, e.as(t, admin, "admin"), call.ID) }, http.StatusOK},
		{"a manager over the host", func() int { return get(t, e.as(t, hostsBoss, "manager"), call.ID) }, http.StatusOK},
		{"a manager over an invitee", func() int { return get(t, e.as(t, inviteesBoss, "manager"), call.ID) }, http.StatusOK},
		{"a manager over nobody in the call", func() int { return get(t, e.as(t, otherBoss, "manager"), call.ID) }, http.StatusNotFound},
		{"an unrelated member", func() int { return get(t, e.as(t, stranger, "member"), call.ID) }, http.StatusNotFound},
		{"somebody in another organization", func() int { return get(t, otherOrgRouter, call.ID) }, http.StatusNotFound},
		{"anybody, for a call that does not exist", func() int { return get(t, e.asOwner(t), uuid.New()) }, http.StatusNotFound},
	} {
		if got := c.router(); got != c.want {
			t.Errorf("%s: want %d, got %d", c.name, c.want, got)
		}
	}

	// A manager with authority over one member gets the whole log for the call.
	log := getLog(t, e.as(t, inviteesBoss, "manager"), call.ID)
	if len(log.events("joined", host)) != 1 || len(log.events("joined", invitee)) != 1 {
		t.Errorf("the manager of one member sees everyone's entries for that call, got %+v", log.Events)
	}
}

func get(t *testing.T, r *gin.Engine, callID uuid.UUID) int {
	t.Helper()
	return doJSON(t, r, http.MethodGet, "/api/calls/"+callID.String()+"/log", "").Code
}

func TestTheLogHasNoEditOrDeleteRoute(t *testing.T) {
	e := newLK(t)
	call := e.call(t, e.org.OwnerID)
	for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodPost} {
		w := doJSON(t, e.asOwner(t), m, "/api/calls/"+call.ID.String()+"/log", `{}`)
		if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /calls/:id/log must not exist, got %d", m, w.Code)
		}
	}
}
