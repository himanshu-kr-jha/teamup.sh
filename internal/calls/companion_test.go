package calls

// Companion mode (spec item 14): one person in a call from a laptop and a phone
// at once. Each device is its own identity in the room, "<userId>:<deviceId>",
// and everything that counts people counts the person once.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"

	"github.com/teamup/teamup/internal/calls/participant"
)

func deviceJoinedEv(room, identity, sid string, at time.Time) *livekit.WebhookEvent {
	return &livekit.WebhookEvent{
		Event: "participant_joined", Id: "EV_" + uuid.NewString(), CreatedAt: at.Unix(),
		Room:        lkRoom(room),
		Participant: &livekit.ParticipantInfo{Identity: identity, Sid: sid, JoinedAt: at.Unix()},
	}
}

func deviceLeftEv(room, identity, sid string, at time.Time) *livekit.WebhookEvent {
	return &livekit.WebhookEvent{
		Event: "participant_left", Id: "EV_" + uuid.NewString(), CreatedAt: at.Unix(),
		Room:        lkRoom(room),
		Participant: &livekit.ParticipantInfo{Identity: identity, Sid: sid},
	}
}

// tokenResponse is what POST /calls/:id/token answers.
type tokenResponse struct {
	Token        string        `json:"token"`
	Identity     string        `json:"identity"`
	OtherDevices []OtherDevice `json:"otherDevices"`
}

func askToken(t *testing.T, e *lkEnv, who uuid.UUID, callID uuid.UUID, body string) tokenResponse {
	t.Helper()
	w := doJSON(t, e.as(t, who, "member"), http.MethodPost, "/api/calls/"+callID.String()+"/token", body)
	wantCode(t, w, http.StatusOK)
	var out tokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return out
}

func grantsOf(t *testing.T, token string) *auth.ClaimGrants {
	t.Helper()
	v, err := auth.ParseAPIToken(token)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	grants, err := v.Verify(lkSecret)
	if err != nil {
		t.Fatalf("verify token: %v", err)
	}
	return grants
}

func TestEachDeviceJoinsAsItsOwnIdentity(t *testing.T) {
	e := newLK(t)
	media := &fakeMedia{present: map[string]bool{}}
	e.svc.media = media
	asha := e.person(t, "Asha Archer", "member")
	bilal := e.person(t, "Bilal Baker", "member")
	call := e.call(t, bilal, asha)

	// A laptop asks with its device: its own identity, and its kind on the token.
	laptop := askToken(t, e, asha, call.ID, `{"device":{"id":"lapTop01","kind":"laptop"}}`)
	if laptop.Identity != asha.String()+":lapTop01" {
		t.Fatalf("identity = %q", laptop.Identity)
	}
	g := grantsOf(t, laptop.Token)
	if g.Identity != laptop.Identity || g.Attributes[participant.DeviceAttribute] != "laptop" || g.Name != "Asha Archer" {
		t.Fatalf("token says identity %q, attributes %v, name %q", g.Identity, g.Attributes, g.Name)
	}
	if len(laptop.OtherDevices) != 0 {
		t.Fatalf("nowhere else yet, got %+v", laptop.OtherDevices)
	}

	// She is in from the laptop; Bilal is in too. Her phone asking is told where else she is.
	media.present[call.RoomName+"/"+laptop.Identity] = true
	media.attrs = map[string]map[string]string{call.RoomName + "/" + laptop.Identity: {participant.DeviceAttribute: "laptop"}}
	media.present[call.RoomName+"/"+bilal.String()+":b1"] = true
	phone := askToken(t, e, asha, call.ID, `{"device":{"id":"phone_02","kind":"phone"}}`)
	if phone.Identity != asha.String()+":phone_02" {
		t.Fatalf("phone identity = %q", phone.Identity)
	}
	if len(phone.OtherDevices) != 1 || phone.OtherDevices[0].Identity != laptop.Identity || phone.OtherDevices[0].Kind != "laptop" {
		t.Fatalf("the phone should be told of the laptop only, got %+v", phone.OtherDevices)
	}

	// The laptop again, as after a reload, is the same device and not a second one.
	again := askToken(t, e, asha, call.ID, `{"device":{"id":"lapTop01","kind":"laptop"}}`)
	if again.Identity != laptop.Identity || len(again.OtherDevices) != 0 {
		t.Fatalf("a reload is not another device: %+v", again)
	}

	// An older page says nothing, and joins as the bare user id it always did. A
	// device id that could smuggle in a separator is not taken, and a kind not
	// known is not labelled.
	old := askToken(t, e, asha, call.ID, "")
	if old.Identity != asha.String() || grantsOf(t, old.Token).Identity != asha.String() {
		t.Fatalf("an older page: %+v", old)
	}
	bad := askToken(t, e, asha, call.ID, `{"device":{"id":"x:`+bilal.String()+`","kind":"phone"}}`)
	if bad.Identity != asha.String() {
		t.Fatalf("a bad device id must not make an identity, got %q", bad.Identity)
	}
	odd := askToken(t, e, asha, call.ID, `{"device":{"id":"tv1","kind":"television"}}`)
	if g := grantsOf(t, odd.Token); g.Identity != asha.String()+":tv1" || g.Attributes[participant.DeviceAttribute] != "" {
		t.Fatalf("an unknown kind is not a label: %q %v", g.Identity, g.Attributes)
	}
}

func TestAttendanceCountsAPersonOnceAcrossDevices(t *testing.T) {
	e := newLK(t)
	asha := e.person(t, "Asha Archer", "member")
	bilal := e.person(t, "Bilal Baker", "member")
	call := e.call(t, bilal, asha)
	t0 := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	laptop, phone := asha.String()+":lap1", asha.String()+":ph1"

	e.mustDeliver(t, joinedEv(call.RoomName, bilal, "PA_b", t0))
	e.mustDeliver(t, deviceJoinedEv(call.RoomName, laptop, "PA_lap", t0.Add(time.Minute)))
	e.mustDeliver(t, deviceJoinedEv(call.RoomName, phone, "PA_ph", t0.Add(2*time.Minute)))
	// The laptop leaves; she is still in from her phone.
	e.mustDeliver(t, deviceLeftEv(call.RoomName, laptop, "PA_lap", t0.Add(3*time.Minute)))

	log := getLog(t, e.asOwner(t), call.ID)
	if got := len(log.sessions(asha)); got != 2 {
		t.Fatalf("each device is a session of hers: %d", got)
	}
	var leftAt *time.Time
	var rows int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT left_at, (SELECT COUNT(*) FROM call_participants WHERE call_id = $1 AND user_id = $2)
		 FROM call_participants WHERE call_id = $1 AND user_id = $2`, call.ID, asha).Scan(&leftAt, &rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || leftAt != nil {
		t.Fatalf("one participant row, still in while the phone is: rows %d, left %v", rows, leftAt)
	}
	n, err := e.svc.peopleInRoom(context.Background(), call.ID, call.RoomName)
	if err != nil || n != 2 {
		t.Fatalf("Asha and Bilal are two people, got %d (%v)", n, err)
	}

	// Present for as long as either device was, not the two added up.
	spans := [][2]time.Time{{t0.Add(time.Minute), t0.Add(3 * time.Minute)}, {t0.Add(2 * time.Minute), t0.Add(5 * time.Minute)}}
	if got := mergedDuration(spans); got != 4*time.Minute {
		t.Fatalf("merged = %v, want 4m", got)
	}
}

func TestOnePersonWithTwoDevicesIsNotAMeeting(t *testing.T) {
	e := newLK(t)
	asha := e.person(t, "Asha Archer", "member")
	bilal := e.person(t, "Bilal Baker", "member")
	call := e.call(t, asha, bilal)
	var told int
	e.svc.joined = func(context.Context, uuid.UUID, uuid.UUID, string) { told++ }
	t0 := time.Now().Add(-time.Minute)

	e.mustDeliver(t, deviceJoinedEv(call.RoomName, asha.String()+":lap1", "PA_1", t0))
	e.mustDeliver(t, deviceJoinedEv(call.RoomName, asha.String()+":ph1", "PA_2", t0))
	if told != 0 {
		t.Fatal("one person waiting from two devices is not two people: no note-taker")
	}
	e.mustDeliver(t, deviceJoinedEv(call.RoomName, bilal.String()+":lap2", "PA_3", t0))
	if told == 0 {
		t.Fatal("two people are a meeting")
	}
}

func TestAHandIsThePersonsOnEveryDevice(t *testing.T) {
	h := newHandEnv(t)
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	h.svc.SetClock(func() time.Time { return now })
	laptop, phone := h.asha.String()+":lap1", h.asha.String()+":ph1"
	h.leave(h.asha)
	h.media.present[h.call.RoomName+"/"+laptop] = true
	h.media.present[h.call.RoomName+"/"+phone] = true
	attr := func(identity string) string {
		h.media.mu.Lock()
		defer h.media.mu.Unlock()
		return h.media.attrs[h.call.RoomName+"/"+identity][HandAttribute]
	}

	code, state := h.put(t, h.asha, "member", `{"raised":true}`)
	if code != http.StatusOK || !state.Raised || state.UserID != h.asha {
		t.Fatalf("raise: %d %+v", code, state)
	}
	if attr(laptop) == "" || attr(laptop) != attr(phone) {
		t.Fatalf("one hand, on both devices: %q %q", attr(laptop), attr(phone))
	}

	// Raised again from the other device: still the same hand, in the same place.
	now = now.Add(time.Minute)
	if _, again := h.put(t, h.asha, "member", `{"raised":true}`); again.RaisedAt != state.RaisedAt {
		t.Fatalf("raising again moved the hand: %q, was %q", again.RaisedAt, state.RaisedAt)
	}

	// A device that lost it (its connection came back) gets it again on refresh.
	h.media.mu.Lock()
	delete(h.media.attrs[h.call.RoomName+"/"+phone], HandAttribute)
	h.media.mu.Unlock()
	if _, r := h.put(t, h.asha, "member", `{"raised":false,"refresh":true}`); !r.Raised || attr(phone) != state.RaisedAt {
		t.Fatalf("refresh should put the hand back on the phone: %+v, phone %q", r, attr(phone))
	}

	// The organizer lowers it, by the person: gone from both.
	if code, _ := h.put(t, h.organizer, "member", `{"raised":false,"userId":"`+h.asha.String()+`"}`); code != http.StatusOK {
		t.Fatalf("organizer lowering: %d", code)
	}
	if attr(laptop) != "" || attr(phone) != "" {
		t.Fatalf("lowered on both devices: %q %q", attr(laptop), attr(phone))
	}
}

func TestADeviceJoiningGetsTheHandAlreadyUp(t *testing.T) {
	h := newHandEnv(t)
	laptop, phone := h.asha.String()+":lap1", h.asha.String()+":ph1"
	h.leave(h.asha)
	h.media.present[h.call.RoomName+"/"+laptop] = true
	if code, _ := h.put(t, h.asha, "member", `{"raised":true}`); code != http.StatusOK {
		t.Fatalf("raise: %d", code)
	}
	raised := h.media.attrs[h.call.RoomName+"/"+laptop][HandAttribute]

	// Her phone connects: LiveKit says so, and the phone has her hand too, so it
	// stays up when the laptop goes.
	h.media.present[h.call.RoomName+"/"+phone] = true
	h.mustDeliver(t, deviceJoinedEv(h.call.RoomName, phone, "PA_ph", time.Now()))
	h.media.mu.Lock()
	got := h.media.attrs[h.call.RoomName+"/"+phone][HandAttribute]
	h.media.mu.Unlock()
	if got != raised {
		t.Fatalf("the phone should have the hand %q, has %q", raised, got)
	}
}

func TestPersonLevelChecksSeeEveryDevice(t *testing.T) {
	e := newLK(t)
	media := &fakeMedia{present: map[string]bool{}}
	e.svc.media = media
	asha := e.person(t, "Asha Archer", "member")
	bilal := e.person(t, "Bilal Baker", "member")
	call := e.call(t, bilal, asha)
	phone := asha.String() + ":ph1"
	media.present[call.RoomName+"/"+phone] = true

	// In from her phone only, she is in the call: she can chat, and is not rung.
	in, err := media.InRoom(context.Background(), call.RoomName, asha.String())
	if err != nil || !in {
		t.Fatalf("in from her phone is in: %v %v", in, err)
	}
	if _, err := e.svc.SendChatMessage(context.Background(), e.org.ID, call.ID, asha, "From my phone"); err != nil {
		t.Fatalf("chat from a device: %v", err)
	}
	model, err := e.svc.GetCall(context.Background(), e.org.ID, call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inCall, err := e.svc.inCallNow(context.Background(), model, asha); err != nil || !inCall {
		t.Fatalf("somebody in from a phone is in the call, so not rung: %v %v", inCall, err)
	}
	// A guest is only ever themselves.
	if in, _ := media.InRoom(context.Background(), call.RoomName, participant.GuestPrefix+asha.String()); in {
		t.Fatal("a guest identity with her id is not her")
	}

	// Taking somebody out takes out every device.
	media.present[call.RoomName+"/"+asha.String()+":lap1"] = true
	e.svc.putOut(context.Background(), call.RoomName, asha)
	removed := map[string]bool{}
	for _, r := range media.removals() {
		removed[r] = true
	}
	if !removed[call.RoomName+"/"+phone] || !removed[call.RoomName+"/"+asha.String()+":lap1"] || len(removed) != 2 {
		t.Fatalf("both devices removed, nobody else: %v", media.removals())
	}
}
