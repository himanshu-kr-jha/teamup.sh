package calls

// Failure modes and edges of attendance, knocks and standing meetings that the
// ticket tests do not reach: webhooks not arriving, rooms that are not ours, the
// clock crossing midnight, and every way to a LiveKit grant.

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"

	"github.com/teamup/teamup/internal/testsupport"
	"github.com/teamup/teamup/pkg/protocol"
)

func (e *lkEnv) rowCount(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// ── rooms and people that are not ours ────────────────────────────────────

func TestEveryWebhookForARoomThatIsNotOursIsAcceptedAndWritesNothing(t *testing.T) {
	e := newLK(t)
	person := e.person(t, "Invited Ivy", "member")
	const room = "somebody-elses-room"
	participantsBefore := e.rowCount(t, "call_participants")

	for _, ev := range []*livekit.WebhookEvent{
		{Event: "room_started", Id: "EV_1", CreatedAt: baseTime().Unix(), Room: lkRoom(room)},
		joinedEv(room, person, "PA_1", baseTime()),
		trackEv("track_published", room, person, "PA_1", livekit.TrackSource_SCREEN_SHARE, baseTime()),
		trackEv("track_unpublished", room, person, "PA_1", livekit.TrackSource_SCREEN_SHARE, baseTime()),
		leftEv(room, person, "PA_1", baseTime()),
		roomFinishedEv(room, baseTime()),
		// No room at all, which is how egress events arrive.
		{Event: "egress_started", Id: "EV_2", CreatedAt: baseTime().Unix()},
	} {
		w := e.deliver(t, ev)
		if !is2xx(w) {
			t.Errorf("%s for a room we do not know must not be retried by LiveKit, got %d", ev.Event, w.Code)
		}
	}
	for _, table := range []string{"call_events", "call_sessions", "calls"} {
		if n := e.rowCount(t, table); n != 0 {
			t.Errorf("%s: a room that is not ours writes nothing, got %d rows", table, n)
		}
	}
	if n := e.rowCount(t, "call_participants"); n != participantsBefore {
		t.Errorf("call_participants changed: %d to %d", participantsBefore, n)
	}
}

func TestAnIdentityFromAnotherOrganizationIsNotRecordedInOurRoom(t *testing.T) {
	e := newLK(t)
	call := e.call(t, e.org.OwnerID)
	other := testsupport.SeedOrg(t, e.pool)

	e.mustDeliver(t, joinedEv(call.RoomName, other.OwnerID, "PA_x", baseTime()))
	if log := getLog(t, e.asOwner(t), call.ID); len(log.Sessions) != 0 || len(log.Events) != 0 {
		t.Errorf("not one of ours, got %+v", log)
	}
	for _, table := range []string{"call_sessions", "call_events"} {
		if n := e.rowCount(t, table); n != 0 {
			t.Errorf("%s: got %d rows", table, n)
		}
	}
}

func TestAMalformedButSignedBodyIsRefusedAndRecordsNothing(t *testing.T) {
	e := newLK(t)
	body := []byte(`{"event": 7}`)
	w := rawWebhook(t, e.webhookRouter(t), body, sign(t, lkKey, lkSecret, body))
	wantCode(t, w, http.StatusBadRequest)
}

func TestAWebhookIsRefusedWhenNoKeysAreConfigured(t *testing.T) {
	e := newLK(t)
	svc := NewService(e.pool, Config{})
	e.svc = svc
	body := []byte(`{"event":"room_started"}`)
	if w := rawWebhook(t, e.webhookRouter(t), body, sign(t, lkKey, lkSecret, body)); w.Code != http.StatusUnauthorized {
		t.Errorf("an unconfigured server trusts nothing, got %d", w.Code)
	}
}

// ── when the room ends ────────────────────────────────────────────────────

func TestAFinishedRoomClosesAnySessionWhoseLeaveNeverCameAndEndsTheCall(t *testing.T) {
	e := newLK(t)
	invitee := e.person(t, "Invited Ivy", "member")
	call := e.call(t, e.org.OwnerID, invitee)
	start := baseTime()

	e.mustDeliver(t, joinedEv(call.RoomName, invitee, "PA_1", start))
	e.mustDeliver(t, roomFinishedEv(call.RoomName, start.Add(9*time.Minute)))

	log := getLog(t, e.asOwner(t), call.ID)
	ss := log.sessions(invitee)
	if len(ss) != 1 || ss[0].LeftAt == nil || !near(*ss[0].LeftAt, start.Add(9*time.Minute)) {
		t.Errorf("the session ends with the room, got %+v", ss)
	}
	if n := len(log.events("left", invitee)); n != 1 {
		t.Errorf("the trail says they left, got %d", n)
	}
	loaded, _ := e.svc.GetCall(t.Context(), e.org.ID, call.ID)
	if loaded.Status != "ended" {
		t.Errorf("an empty room means the call is over, got %q", loaded.Status)
	}

	// A leave delivered late changes nothing and adds no second line.
	e.mustDeliver(t, leftEv(call.RoomName, invitee, "PA_1", start.Add(9*time.Minute)))
	if n := len(getLog(t, e.asOwner(t), call.ID).events("left", invitee)); n != 1 {
		t.Errorf("still one leave, got %d", n)
	}
}

func TestARoomThatFilledUpAgainBeforeTheFinishArrivedIsNotEnded(t *testing.T) {
	e := newLK(t)
	invitee := e.person(t, "Invited Ivy", "member")
	call := e.call(t, e.org.OwnerID, invitee)
	start := baseTime()

	e.mustDeliver(t, joinedEv(call.RoomName, invitee, "PA_2", start.Add(10*time.Minute)))
	e.mustDeliver(t, roomFinishedEv(call.RoomName, start.Add(5*time.Minute)))

	loaded, _ := e.svc.GetCall(t.Context(), e.org.ID, call.ID)
	if loaded.Status != "active" {
		t.Errorf("somebody connected after that room finished, got %q", loaded.Status)
	}
	if ss := getLog(t, e.asOwner(t), call.ID).sessions(invitee); len(ss) != 1 || ss[0].LeftAt != nil {
		t.Errorf("their session stays open, got %+v", ss)
	}
}

func TestACallNobodyAnsweredBecomesAMissedCallWhenItsRoomFinishes(t *testing.T) {
	e := newLK(t)
	recorder := &recordedEvents{}
	e.svc.SetChatRecorder(recorder)
	callee := e.person(t, "Callee Cam", "member")
	call := e.call(t, e.org.OwnerID, callee)

	e.mustDeliver(t, roomFinishedEv(call.RoomName, baseTime()))
	if len(recorder.notes) != 1 {
		t.Errorf("the caller's browser died and nobody hung up: still a missed call, got %+v", recorder.notes)
	}
	// And a duplicate does not write it twice.
	e.mustDeliver(t, roomFinishedEv(call.RoomName, baseTime()))
	if len(recorder.notes) != 1 {
		t.Errorf("a retried finish is one note, got %+v", recorder.notes)
	}
}

func TestAFinishedRoomLeavesAMeetingThatHasNotOpenedAlone(t *testing.T) {
	e := newLK(t)
	id := uuid.New()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO calls (id, org_id, room_name, call_type, status, created_by, scheduled_start, scheduled_end)
		VALUES ($1, $2, 'call-meeting', 'video', 'scheduled', $3, NOW() + interval '1 hour', NOW() + interval '2 hours')
	`, id, e.org.ID, e.org.OwnerID); err != nil {
		t.Fatal(err)
	}
	e.mustDeliver(t, roomFinishedEv("call-meeting", baseTime()))
	loaded, _ := e.svc.GetCall(t.Context(), e.org.ID, id)
	if loaded.Status != "scheduled" {
		t.Errorf("got %q", loaded.Status)
	}
}

func TestEndingACallTwiceIsFine(t *testing.T) {
	e := newLK(t)
	call := e.call(t, e.org.OwnerID)
	r := e.asOwner(t)
	path := "/api/calls/" + call.ID.String() + "/end"
	wantCode(t, doJSON(t, r, http.MethodPost, path, ""), http.StatusOK)
	// LiveKit reporting the room gone can get there before the client does.
	wantCode(t, doJSON(t, r, http.MethodPost, path, ""), http.StatusOK)
}

// ── webhooks not arriving ─────────────────────────────────────────────────
//
// The failure mode to degrade from: LiveKit cannot reach the app, so nobody is ever
// reported as connected. Ringing and missed calls must still be right, and anything
// about attendance must say it has no data rather than that nobody came.

func TestWithoutWebhooksAPickedUpCallIsNotMissedAndDoesNotRingAgain(t *testing.T) {
	e := newLK(t)
	recorder := &recordedEvents{}
	e.svc.SetChatRecorder(recorder)
	callee := e.person(t, "Callee Cam", "member")
	other := e.person(t, "Other Oz", "member")
	call := e.call(t, e.org.OwnerID, callee, other)

	wantCode(t, doJSON(t, e.as(t, callee, "member"), http.MethodPost, "/api/calls/"+call.ID.String()+"/token", ""), http.StatusOK)

	ctx := context.Background()
	pending, err := e.svc.PendingInvitationsFor(ctx, callee)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("their socket reconnecting must not ring them for a call they picked up, got %+v", pending)
	}
	if joined, err := e.svc.SomebodyJoined(ctx, call.ID, e.org.OwnerID); err != nil || !joined {
		t.Errorf("somebody declining must not drop a call somebody picked up: %v %v", joined, err)
	}
	e.svc.NoteIfMissed(ctx, call.ID, "no_answer")
	if len(recorder.notes) != 0 {
		t.Errorf("it was answered, got %+v", recorder.notes)
	}
}

func TestWithoutWebhooksTheLogSaysThereIsNoDataRatherThanThatNobodyCame(t *testing.T) {
	e := newLK(t)
	callee := e.person(t, "Callee Cam", "member")
	call := e.call(t, e.org.OwnerID, callee)
	wantCode(t, doJSON(t, e.as(t, callee, "member"), http.MethodPost, "/api/calls/"+call.ID.String()+"/token", ""), http.StatusOK)

	read := func() (recorded bool) {
		w := doJSON(t, e.asOwner(t), http.MethodGet, "/api/calls/"+call.ID.String()+"/log", "")
		wantCode(t, w, http.StatusOK)
		var body struct {
			AttendanceRecorded bool `json:"attendanceRecorded"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.AttendanceRecorded
	}
	if read() {
		t.Error("no connection was ever reported, so this is no data")
	}
	e.mustDeliver(t, joinedEv(call.RoomName, callee, "PA_1", baseTime()))
	if !read() {
		t.Error("once LiveKit reports somebody, there is data")
	}
}

func TestWithoutWebhooksAScrumDayReadsUnknownAndNobodyIsMarkedAbsent(t *testing.T) {
	e := newLK(t)
	m := e.mustCreateStanding(t, "Morning scrum", "09:00", "UTC")
	ann := e.person(t, "Ann Archer", "member")
	bo := e.person(t, "Bo Baker", "member")
	// After the start, whatever time the suite runs: before it nobody is expected yet.
	afterStart := time.Now().UTC().Truncate(24 * time.Hour).Add(10 * time.Hour)
	e.svc.SetClock(func() time.Time { return afterStart })
	j := e.mustJoinStanding(t, ann, "member", m.ID)
	e.mustJoinStanding(t, bo, "member", m.ID)

	days := e.attendance(t, e.org.OwnerID, "owner", m.ID, "date="+j.Date)
	if len(days) != 1 {
		t.Fatalf("got %+v", days)
	}
	if len(days[0].People) == 0 {
		t.Fatal("everybody is still listed")
	}
	for _, p := range days[0].People {
		if p.Status != "unknown" {
			t.Errorf("people pressed join and no connection was reported: %s must be unknown, not %q", p.Name, p.Status)
		}
	}

	// A knock still reaches the people on the call, by notification and frame.
	w := newKnockWorld(t)
	req := w.mustKnock(t, w.knocker)
	got := w.frames.knockFrames(w.invitee)
	if len(got) != 1 || got[0].Payload["connected"] != false || got[0].Payload["requestId"] != req.ID {
		t.Errorf("nobody looks connected, so everybody on the call gets it marked that way, got %+v", got)
	}
	wantCode(t, w.answer(t, "admit", w.host, "member", req.ID), http.StatusOK)
	if cleared := w.frames.knockFrames(w.otherInvitee); len(cleared) != 2 || cleared[1].Type != protocol.TypeCallKnockResolved {
		t.Errorf("their prompt clears too, got %+v", cleared)
	}
}

// ── concurrency ───────────────────────────────────────────────────────────

func TestWebhooksForOneCallArrivingAtOnceLeaveCallParticipantsRight(t *testing.T) {
	e := newLK(t)
	invitee := e.person(t, "Invited Ivy", "member")
	call := e.call(t, e.org.OwnerID, invitee)
	start := baseTime()
	r := e.webhookRouter(t)

	// Devices 0 to 7 connect and leave; device 8 connects and stays.
	var wg sync.WaitGroup
	deliver := func(ev *livekit.WebhookEvent) {
		defer wg.Done()
		body := marshalEvent(t, ev)
		w := rawWebhook(t, r, body, sign(t, lkKey, lkSecret, body))
		if !is2xx(w) {
			t.Errorf("%s: %d %s", ev.Event, w.Code, w.Body.String())
		}
	}
	for i := 0; i < 8; i++ {
		sid := "PA_" + string(rune('a'+i))
		wg.Add(2)
		go deliver(joinedEv(call.RoomName, invitee, sid, start))
		go deliver(leftEv(call.RoomName, invitee, sid, start.Add(time.Minute)))
	}
	wg.Add(1)
	go deliver(joinedEv(call.RoomName, invitee, "PA_stays", start))
	wg.Wait()

	loaded, _ := e.svc.GetCall(t.Context(), e.org.ID, call.ID)
	for _, p := range loaded.Participants {
		if p.UserID == invitee && (p.JoinedAt == nil || p.LeftAt != nil) {
			t.Errorf("one device is still connected whatever order those landed in, got %+v", p)
		}
	}
	if n := len(getLog(t, e.asOwner(t), call.ID).sessions(invitee)); n != 9 {
		t.Errorf("nine sessions, got %d", n)
	}
}

// ── every way to a grant ──────────────────────────────────────────────────

func grantOf(t *testing.T, token string) *auth.VideoGrant {
	t.Helper()
	verifier, err := auth.ParseAPIToken(token)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifier.Verify(lkSecret)
	if err != nil {
		t.Fatal(err)
	}
	return claims.Video
}

// Screen share: nothing in the grant restricts sources, so anybody with a token can
// publish one. The frontend ticket can rely on this.
func TestEveryTokenLetsItsHolderPublishAScreen(t *testing.T) {
	e := newLK(t)
	invitee := e.person(t, "Invited Ivy", "member")
	call := e.call(t, e.org.OwnerID, invitee)
	m := e.mustCreateStanding(t, "Morning scrum", "09:00", "UTC")

	w := doJSON(t, e.as(t, invitee, "member"), http.MethodPost, "/api/calls/"+call.ID.String()+"/token", "")
	wantCode(t, w, http.StatusOK)
	var token struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{
		"somebody invited, not the organizer": token.Token,
		"a standing meeting":                  e.mustJoinStanding(t, invitee, "member", m.ID).Token,
	} {
		grant := grantOf(t, raw)
		if grant.GetCanPublish() != true {
			t.Errorf("%s: must be able to publish", name)
		}
		if len(grant.GetCanPublishSources()) != 0 {
			t.Errorf("%s: a source list would stop somebody sharing their screen, got %v", name, grant.GetCanPublishSources())
		}
	}
}

func TestAcceptingOverTheSocketFollowsTheSameRulesAsTheToken(t *testing.T) {
	e := newLK(t)
	sockets := newRecordingBroadcaster()
	e.svc.SetBroadcaster(sockets)
	invitee := e.person(t, "Invited Ivy", "member")
	knocker := e.person(t, "Knocking Kim", "member")
	call := e.call(t, e.org.OwnerID, invitee)
	m := e.mustCreateStanding(t, "Morning scrum", "09:00", "UTC")
	occurrence := e.mustJoinStanding(t, invitee, "member", m.ID)

	meeting := uuid.New()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO calls (id, org_id, room_name, call_type, status, created_by, scheduled_start, scheduled_end)
		VALUES ($1, $2, 'call-later', 'video', 'scheduled', $3, NOW() + interval '1 hour', NOW() + interval '2 hours')
	`, meeting, e.org.ID, e.org.OwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO call_participants (call_id, user_id) VALUES ($1, $2)`, meeting, invitee); err != nil {
		t.Fatal(err)
	}

	accept := func(who uuid.UUID, room string) int {
		sockets.mu.Lock()
		delete(sockets.toUser, who.String())
		sockets.mu.Unlock()
		raw, _ := json.Marshal(protocol.CallAcceptPayload{RoomName: room})
		conn := &wsConn{userID: who.String(), orgID: e.org.ID.String()}
		e.svc.HandleCallAccept(context.Background(), conn, &protocol.Message{Type: protocol.TypeCallAccept, Payload: raw})
		sockets.mu.Lock()
		defer sockets.mu.Unlock()
		return len(sockets.toUser[who.String()])
	}

	if n := accept(knocker, call.RoomName); n != 0 {
		t.Errorf("no grant for somebody who has to knock, got %d frames", n)
	}
	if n := accept(invitee, "call-later"); n != 0 {
		t.Errorf("a meeting does not ring, so there is nothing to accept, got %d frames", n)
	}
	if n := accept(knocker, occurrence.RoomName); n != 1 {
		t.Errorf("a standing meeting is open to everybody, got %d frames", n)
	}

	// Admitted, the same person now can.
	w := &knockWorld{lkEnv: e, call: call}
	e.svc.SetBroadcaster(e.frames)
	req := w.mustKnock(t, knocker)
	wantCode(t, w.answer(t, "admit", invitee, "member", req.ID), http.StatusOK)
	e.svc.SetBroadcaster(sockets)
	if n := accept(knocker, call.RoomName); n != 1 {
		t.Errorf("somebody admitted is a member, got %d frames", n)
	}
}

func TestSomebodyWithALinkLearnsOnlyEnoughToKnock(t *testing.T) {
	e := newLK(t)
	title := "Layoff planning"
	invitee := e.person(t, "Invited Ivy", "member")
	stranger := e.person(t, "Stranger Sam", "member")
	call, err := e.svc.CreateCall(context.Background(), e.org.ID, e.org.OwnerID, &title, "video", nil, []string{invitee.String()})
	if err != nil {
		t.Fatal(err)
	}

	w := doJSON(t, e.as(t, stranger, "member"), http.MethodGet, "/api/calls/"+call.ID.String(), "")
	wantCode(t, w, http.StatusOK)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["canKnock"] != true || body["status"] != "active" {
		t.Errorf("enough to show a knock screen, got %v", body)
	}
	for _, private := range []string{"title", "participants", "roomName", "createdByName"} {
		if _, leaked := body[private]; leaked {
			t.Errorf("%s is the members' business: %v", private, body)
		}
	}

	full := doJSON(t, e.as(t, invitee, "member"), http.MethodGet, "/api/calls/"+call.ID.String(), "")
	wantCode(t, full, http.StatusOK)
	var memberView CallModel
	if err := json.Unmarshal(full.Body.Bytes(), &memberView); err != nil {
		t.Fatal(err)
	}
	if memberView.Title == nil || *memberView.Title != title || len(memberView.Participants) != 2 {
		t.Errorf("a member sees it all, got %+v", memberView)
	}
}

func TestAKnockerIsToldWhenTheCallEndsWithThemStillWaiting(t *testing.T) {
	w := newKnockWorld(t)
	req := w.mustKnock(t, w.knocker)
	w.frames.reset()
	wantCode(t, doJSON(t, w.as(t, w.host, "member"), http.MethodPost, w.path("/end"), ""), http.StatusOK)

	got := w.frames.knockFrames(w.knocker)
	if len(got) != 1 || got[0].Payload["requestId"] != req.ID || got[0].Payload["status"] != "ended" {
		t.Errorf("their knock screen has to stop waiting, got %+v", got)
	}
}

func TestSomebodyWhoMayAlreadyJoinIsNotAKnocker(t *testing.T) {
	w := newKnockWorld(t)
	res, _ := w.knock(t, w.invitee, "member")
	wantCode(t, res, http.StatusConflict)
}

// ── who reads a standing meeting's log ────────────────────────────────────

func TestAttendingAStandingMeetingDoesNotOpenItsLogToYou(t *testing.T) {
	e := newLK(t)
	m := e.mustCreateStanding(t, "Morning scrum", "09:00", "UTC")
	ann := e.person(t, "Ann Archer", "member")
	boss := e.person(t, "Boss Bea", "manager")
	bystander := e.person(t, "Bystander Bo", "manager")
	testsupport.SeedTeam(t, e.pool, e.org, "Anns", &boss, nil, ann)

	j := e.mustJoinStanding(t, ann, "member", m.ID)
	e.mustDeliver(t, joinedEv(j.RoomName, ann, "PA_1", baseTime()))
	id := uuid.MustParse(j.CallID)

	if got := get(t, e.as(t, ann, "member"), id); got != http.StatusNotFound {
		t.Errorf("an employee sees only themself in attendance, so not the log, got %d", got)
	}
	if got := get(t, e.as(t, boss, "manager"), id); got != http.StatusOK {
		t.Errorf("a manager over somebody who came reads it, got %d", got)
	}
	if got := get(t, e.as(t, bystander, "manager"), id); got != http.StatusNotFound {
		t.Errorf("a manager over nobody there does not, got %d", got)
	}
	if got := get(t, e.asOwner(t), id); got != http.StatusOK {
		t.Errorf("the owner reads it, got %d", got)
	}
}

// ── midnight, with a real clock ───────────────────────────────────────────

func TestCrossingMidnightInTheMeetingsZoneStartsANewOccurrence(t *testing.T) {
	e := newLK(t)
	m := e.mustCreateStanding(t, "Night shift", "23:30", "Asia/Kolkata")
	ann := e.person(t, "Ann Archer", "member")

	// 23:59 in Kolkata is 18:29 UTC, the middle of the server's day.
	now := time.Date(2026, 11, 6, 18, 29, 0, 0, time.UTC)
	e.svc.SetClock(func() time.Time { return now })
	before := e.mustJoinStanding(t, ann, "member", m.ID)
	if before.Date != "2026-11-06" {
		t.Fatalf("the date in Kolkata is the 6th, got %s", before.Date)
	}

	// Half a minute on, still the same day there.
	now = now.Add(30 * time.Second)
	if same := e.mustJoinStanding(t, ann, "member", m.ID); same.CallID != before.CallID {
		t.Errorf("23:59:30 is still the 6th there, got a new occurrence %s", same.Date)
	}

	// 00:01 in Kolkata, while it is still the 6th at the server.
	now = time.Date(2026, 11, 6, 18, 31, 0, 0, time.UTC)
	after := e.mustJoinStanding(t, ann, "member", m.ID)
	if after.Date != "2026-11-07" {
		t.Errorf("past midnight there it is the 7th, got %s", after.Date)
	}
	if after.CallID == before.CallID || after.RoomName == before.RoomName {
		t.Error("a new day is a new occurrence, in a room of its own")
	}
	if n := e.callCount(t); n != 2 {
		t.Errorf("one row per day, got %d", n)
	}
}

// DST is why the start is worked out per date rather than from a fixed offset.
func TestTheScheduledStartFollowsDaylightSavingInTheMeetingsZone(t *testing.T) {
	e := newLK(t)
	m := e.mustCreateStanding(t, "Morning scrum", "09:00", "America/New_York")
	ann := e.person(t, "Ann Archer", "member")

	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC) // EDT, UTC-4
	e.svc.SetClock(func() time.Time { return now })
	j := e.mustJoinStanding(t, ann, "member", m.ID)
	e.mustDeliver(t, joinedEv(j.RoomName, ann, "PA_1", time.Date(2026, 7, 1, 13, 5, 0, 0, time.UTC)))
	day := e.attendance(t, e.org.OwnerID, "owner", m.ID, "date=2026-07-01")[0]
	if want := time.Date(2026, 7, 1, 13, 0, 0, 0, time.UTC); !day.ScheduledStart.Equal(want) {
		t.Errorf("09:00 in summer is 13:00 UTC, got %v", day.ScheduledStart)
	}

	now = time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC) // EST, UTC-5
	j = e.mustJoinStanding(t, ann, "member", m.ID)
	e.mustDeliver(t, joinedEv(j.RoomName, ann, "PA_2", time.Date(2026, 12, 1, 14, 5, 0, 0, time.UTC)))
	day = e.attendance(t, e.org.OwnerID, "owner", m.ID, "date=2026-12-01")[0]
	if want := time.Date(2026, 12, 1, 14, 0, 0, 0, time.UTC); !day.ScheduledStart.Equal(want) {
		t.Errorf("09:00 in winter is 14:00 UTC, got %v", day.ScheduledStart)
	}
	if p, ok := day.person(ann); !ok || p.MinutesLate != 5 {
		t.Errorf("five minutes late, got %+v", p)
	}
}

// ── attendance edges ──────────────────────────────────────────────────────

func TestChangingTheTimeDoesNotMakeAnybodyLateInRetrospect(t *testing.T) {
	e := newLK(t)
	m := e.mustCreateStanding(t, "Morning scrum", "09:00", "UTC")
	ann := e.person(t, "Ann Archer", "member")

	now := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	e.svc.SetClock(func() time.Time { return now })
	j := e.mustJoinStanding(t, ann, "member", m.ID)
	e.mustDeliver(t, joinedEv(j.RoomName, ann, "PA_1", time.Date(2026, 3, 2, 9, 2, 0, 0, time.UTC)))

	wantCode(t, doJSON(t, e.asOwner(t), http.MethodPatch, "/api/standing-meetings/"+m.ID, `{"timeOfDay":"08:00"}`), http.StatusOK)

	day := e.attendance(t, e.org.OwnerID, "owner", m.ID, "date=2026-03-02")[0]
	if p, _ := day.person(ann); p.MinutesLate != 2 {
		t.Errorf("that day started at 09:00, got %d minutes late", p.MinutesLate)
	}
}

func TestComingOnADayItDoesNotRunIsRecordedAndNobodyIsAbsent(t *testing.T) {
	e := newLK(t)
	_, m := e.createStanding(t, e.org.OwnerID, "owner", standingBody("Weekday scrum", "09:00", "UTC", 1, 2, 3, 4, 5))
	ann := e.person(t, "Ann Archer", "member")
	stayed := e.person(t, "Stayed Home", "member")

	saturday := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	e.svc.SetClock(func() time.Time { return saturday })
	j := e.mustJoinStanding(t, ann, "member", m.ID)
	e.mustDeliver(t, joinedEv(j.RoomName, ann, "PA_1", saturday))

	days := e.attendance(t, e.org.OwnerID, "owner", m.ID, "date=2026-11-07")
	if len(days) != 1 {
		t.Fatalf("got %+v", days)
	}
	if _, ok := days[0].person(ann); !ok {
		t.Error("she was there, so it says so")
	}
	if _, ok := days[0].person(stayed); ok {
		t.Error("nobody is absent from a day it does not run")
	}
}

func TestADayNobodyJoinedListsEverybodyAbsentOnceItIsDue(t *testing.T) {
	e := newLK(t)
	m := e.mustCreateStanding(t, "Morning scrum", "09:00", "UTC")
	ann := e.person(t, "Ann Archer", "member")
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE standing_meetings SET created_at = '2026-01-01' WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE users SET created_at = '2026-01-01' WHERE org_id = $1`, e.org.ID); err != nil {
		t.Fatal(err)
	}

	before := time.Date(2026, 3, 2, 8, 59, 0, 0, time.UTC)
	e.svc.SetClock(func() time.Time { return before })
	if days := e.attendance(t, e.org.OwnerID, "owner", m.ID, "date=2026-03-02"); len(days) != 0 {
		t.Errorf("it has not happened yet, got %+v", days)
	}

	after := before.Add(2 * time.Minute)
	e.svc.SetClock(func() time.Time { return after })
	days := e.attendance(t, e.org.OwnerID, "owner", m.ID, "date=2026-03-02")
	if len(days) != 1 {
		t.Fatalf("got %+v", days)
	}
	if p, ok := days[0].person(ann); !ok || p.Status != "absent" {
		t.Errorf("no webhook could have been missed, because nobody asked to join, got %+v", p)
	}

	// Before the meeting existed nobody could have been absent from it.
	if days := e.attendance(t, e.org.OwnerID, "owner", m.ID, "date=2025-12-31"); len(days) != 0 {
		t.Errorf("got %+v", days)
	}
}

func TestALeaveThatNeverArrivedDoesNotMakeSomebodyPresentForever(t *testing.T) {
	e := newLK(t)
	m := e.mustCreateStanding(t, "Morning scrum", "09:00", "UTC")
	ann := e.person(t, "Ann Archer", "member")

	day := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	e.svc.SetClock(func() time.Time { return day })
	j := e.mustJoinStanding(t, ann, "member", m.ID)
	e.mustDeliver(t, joinedEv(j.RoomName, ann, "PA_1", day))

	// A week later the leave and the room's finish are still lost.
	later := day.AddDate(0, 0, 7)
	e.svc.SetClock(func() time.Time { return later })
	got := e.attendance(t, e.org.OwnerID, "owner", m.ID, "date=2026-03-02")[0]
	p, _ := got.person(ann)
	if p.MinutesPresent != 15*60 {
		t.Errorf("counted to the end of that day at the most (15 hours), got %d minutes", p.MinutesPresent)
	}
	if p.LastLeaveAt != nil {
		t.Errorf("nobody knows when she left, got %v", p.LastLeaveAt)
	}
}

func TestAnAttendanceRangeIsValidated(t *testing.T) {
	e := newLK(t)
	m := e.mustCreateStanding(t, "Morning scrum", "09:00", "UTC")
	for name, q := range map[string]string{
		"not a date":    "date=soon",
		"reversed":      "from=2026-03-02&to=2026-03-01",
		"half a year":   "from=2026-01-01&to=2026-07-01",
		"a bad to":      "from=2026-01-01&to=later",
		"a bad team id": "date=2026-03-02&teamId=nope",
	} {
		w := doJSON(t, e.asOwner(t), http.MethodGet, "/api/standing-meetings/"+m.ID+"/attendance?"+q, "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d", name, w.Code)
		}
	}
}

func TestAManagerSeesTheirOwnAttendanceWithoutATeamFilter(t *testing.T) {
	w := newScrumWorld(t)
	d := w.day(t, w.mgr, "manager", "")
	if _, ok := d.person(w.mgr); !ok {
		t.Error("nobody needs permission to see themself")
	}
	d = w.day(t, w.mgr, "manager", "&teamId="+w.backend.String())
	if _, ok := d.person(w.mgr); ok {
		t.Error("a team filter asks for that team and nothing else")
	}
}

func TestSomebodyWhoCameAndHasSinceLeftIsStillOnTheOrganizationsRecord(t *testing.T) {
	w := newScrumWorld(t)
	if _, err := w.pool.Exec(context.Background(), `UPDATE users SET is_active = false WHERE id = $1`, w.late); err != nil {
		t.Fatal(err)
	}
	if p, ok := w.day(t, w.org.OwnerID, "owner", "").person(w.late); !ok || p.Status != "present" {
		t.Errorf("she came, whatever happened afterwards, got %+v", p)
	}
}

// ── a connection reopening what it finds closed ───────────────────────────

func TestAConnectionReopensAnOccurrenceButARetriedOldJoinDoesNot(t *testing.T) {
	e := newLK(t)
	m := e.mustCreateStanding(t, "Morning scrum", "09:00", "UTC")
	ann := e.person(t, "Ann Archer", "member")
	j := e.mustJoinStanding(t, ann, "member", m.ID)
	id := uuid.MustParse(j.CallID)
	status := func() string {
		call, err := e.svc.GetCall(context.Background(), e.org.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		return call.Status
	}

	start := time.Now().Add(-time.Minute).Truncate(time.Second)
	join := joinedEv(j.RoomName, ann, "PA_1", start)
	e.mustDeliver(t, join)
	e.mustDeliver(t, roomFinishedEv(j.RoomName, start.Add(30*time.Second)))
	if got := status(); got != "ended" {
		t.Fatalf("the room finished, got %q", got)
	}

	// LiveKit retries the old join after the room is gone: that session is over.
	e.mustDeliver(t, join)
	if got := status(); got != "ended" {
		t.Errorf("a retry of a finished session must not bring the call back, got %q", got)
	}

	// Somebody with a token from a moment ago really connects after the finish.
	e.mustDeliver(t, joinedEv(j.RoomName, ann, "PA_2", time.Now().Add(time.Minute)))
	if got := status(); got != "active" {
		t.Errorf("somebody is in the room, so the record says so, got %q", got)
	}
}

func TestAConnectionDoesNotReopenAnOrdinaryCall(t *testing.T) {
	e := newLK(t)
	call := e.call(t, e.org.OwnerID)
	wantCode(t, doJSON(t, e.asOwner(t), http.MethodPost, "/api/calls/"+call.ID.String()+"/end", ""), http.StatusOK)

	e.mustDeliver(t, joinedEv(call.RoomName, e.org.OwnerID, "PA_1", time.Now().Add(time.Minute)))
	loaded, _ := e.svc.GetCall(t.Context(), e.org.ID, call.ID)
	if loaded.Status != "ended" {
		t.Errorf("a call that was hung up stays hung up, got %q", loaded.Status)
	}
	if n := len(getLog(t, e.asOwner(t), call.ID).sessions(e.org.OwnerID)); n != 1 {
		t.Errorf("but LiveKit said they connected, so that is on the record, got %d sessions", n)
	}
}

// Two people read a meeting as not yet open, and one of them opens it first. The
// other is not being told the call has ended.
func TestOpeningAMeetingSomebodyJustOpenedIsNotARefusal(t *testing.T) {
	e := newLK(t)
	id := uuid.New()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO calls (id, org_id, room_name, call_type, status, created_by, scheduled_start, scheduled_end)
		VALUES ($1, $2, 'call-meeting', 'video', 'scheduled', $3, NOW() + interval '1 hour', NOW() + interval '2 hours')
	`, id, e.org.ID, e.org.OwnerID); err != nil {
		t.Fatal(err)
	}
	stale, err := e.svc.GetCall(context.Background(), e.org.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.openForJoin(context.Background(), stale); err != nil {
		t.Fatalf("the first opens it: %v", err)
	}
	if err := e.svc.openForJoin(context.Background(), stale); err != nil {
		t.Errorf("the second arrived at the same moment and must get in: %v", err)
	}
}
