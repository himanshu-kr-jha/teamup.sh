package calls

// Ticket 02: knock, admit and deny for private calls.
// Decisions 7, 8, 9 and 10 in .scratch/calendar-and-meetings/spec.md.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/testsupport"
)

type knockRequest struct {
	ID     string `json:"id"`
	UserID string `json:"userId"`
	Status string `json:"status"`
}

type knockWorld struct {
	*lkEnv
	host, invitee, otherInvitee, knocker, bystander uuid.UUID
	call                                            *Call
}

// newKnockWorld is a private call with a host and two invitees, and two
// organization members who are not part of it.
func newKnockWorld(t *testing.T) *knockWorld {
	t.Helper()
	e := newLK(t)
	w := &knockWorld{lkEnv: e}
	w.host = e.person(t, "Host Hana", "member")
	w.invitee = e.person(t, "Invited Ivy", "member")
	w.otherInvitee = e.person(t, "Invited Ian", "member")
	w.knocker = e.person(t, "Knocking Kim", "member")
	w.bystander = e.person(t, "Bystander Bo", "member")
	w.call = e.call(t, w.host, w.invitee, w.otherInvitee)
	return w
}

func (w *knockWorld) path(suffix string) string { return "/api/calls/" + w.call.ID.String() + suffix }

func (w *knockWorld) token(t *testing.T, who uuid.UUID, role string) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, w.as(t, who, role), http.MethodPost, w.path("/token"), "")
}

// knock returns the response and, when it succeeded, the request it made.
func (w *knockWorld) knock(t *testing.T, who uuid.UUID, role string) (*httptest.ResponseRecorder, knockRequest) {
	t.Helper()
	res := doJSON(t, w.as(t, who, role), http.MethodPost, w.path("/knock"), "")
	var req knockRequest
	if is2xx(res) {
		_ = json.Unmarshal(inner(res.Body.Bytes(), "request"), &req)
	}
	return res, req
}

func (w *knockWorld) mustKnock(t *testing.T, who uuid.UUID) knockRequest {
	t.Helper()
	res, req := w.knock(t, who, "member")
	if !is2xx(res) || req.ID == "" {
		t.Fatalf("knock should make a request, got %d: %s", res.Code, res.Body.String())
	}
	if req.Status != "pending" {
		t.Fatalf("a new request is pending, got %q", req.Status)
	}
	return req
}

func (w *knockWorld) requests(t *testing.T, who uuid.UUID, role string) (int, []knockRequest) {
	t.Helper()
	res := doJSON(t, w.as(t, who, role), http.MethodGet, w.path("/requests"), "")
	var body struct {
		Requests []knockRequest `json:"requests"`
	}
	if res.Code == http.StatusOK {
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode requests %q: %v", res.Body.String(), err)
		}
	}
	return res.Code, body.Requests
}

func (w *knockWorld) pending(t *testing.T, asMember uuid.UUID) []knockRequest {
	t.Helper()
	code, all := w.requests(t, asMember, "member")
	if code != http.StatusOK {
		t.Fatalf("a member must be able to list the requests, got %d", code)
	}
	out := []knockRequest{}
	for _, r := range all {
		if r.Status == "pending" {
			out = append(out, r)
		}
	}
	return out
}

func (w *knockWorld) answer(t *testing.T, verb string, who uuid.UUID, role, requestID string) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, w.as(t, who, role), http.MethodPost, w.path("/requests/"+requestID+"/"+verb), "")
}

func (w *knockWorld) isMember(t *testing.T, who uuid.UUID) bool {
	t.Helper()
	ok, err := w.svc.MayJoin(context.Background(), w.org.ID, w.call.ID, who)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// ── something actionable instead of a bare 403 ────────────────────────────

func TestANonMemberIsToldTheyCanKnockRatherThanGettingABare403(t *testing.T) {
	w := newKnockWorld(t)
	res := w.token(t, w.knocker, "member")
	if res.Code != http.StatusForbidden {
		t.Fatalf("a non-member gets no token, want 403, got %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Error    string `json:"error"`
		Code     string `json:"code"`
		CanKnock bool   `json:"canKnock"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &body)
	if body.Code != "knock_required" || !body.CanKnock {
		t.Errorf(`the client needs something to act on: want code "knock_required" and canKnock true, got %s`, res.Body.String())
	}
	if body.Error == "" {
		t.Error("keep a human readable error too")
	}
}

func TestAnOrgAdminOwnerAndManagerKnockLikeAnybodyElse(t *testing.T) {
	w := newKnockWorld(t)
	admin := w.person(t, "Admin Ada", "admin")
	manager := w.person(t, "Manager Max", "manager")
	testsupport.SeedTeam(t, w.pool, w.org, "Hosts", &manager, nil, w.host)

	for _, c := range []struct {
		name string
		id   uuid.UUID
		role string
	}{{"owner", w.org.OwnerID, "owner"}, {"admin", admin, "admin"}, {"manager", manager, "manager"}} {
		res := w.token(t, c.id, c.role)
		if res.Code != http.StatusForbidden {
			t.Errorf("%s: being able to read logs is not the same as walking into a private call, got %d", c.name, res.Code)
		}
		if k, _ := w.knock(t, c.id, c.role); !is2xx(k) {
			t.Errorf("%s: should be able to knock, got %d: %s", c.name, k.Code, k.Body.String())
		}
	}
}

func TestSomebodyInAnotherOrganizationCannotKnockAtAll(t *testing.T) {
	w := newKnockWorld(t)
	req := w.mustKnock(t, w.knocker)
	other := testsupport.SeedOrg(t, w.pool)
	r := routerFor(t, w.svc, other.ID, other.OwnerID, "owner")

	for name, c := range map[string][2]string{
		"token": {http.MethodPost, w.path("/token")},
		"knock": {http.MethodPost, w.path("/knock")},
		"list":  {http.MethodGet, w.path("/requests")},
		"admit": {http.MethodPost, w.path("/requests/" + req.ID + "/admit")},
		"deny":  {http.MethodPost, w.path("/requests/" + req.ID + "/deny")},
	} {
		if got := doJSON(t, r, c[0], c[1], "").Code; got != http.StatusNotFound {
			t.Errorf("%s from another organization: want 404, got %d", name, got)
		}
	}
	if len(w.pending(t, w.invitee)) != 1 {
		t.Error("the foreign attempts must not have touched the request")
	}
}

// ── knocking, admitting, denying ──────────────────────────────────────────

func TestAKnockMakesOnePendingRequestMembersCanSee(t *testing.T) {
	w := newKnockWorld(t)
	first := w.mustKnock(t, w.knocker)
	if first.UserID != w.knocker.String() {
		t.Errorf("the request is the knocker's, got %+v", first)
	}
	// A double click is not two requests.
	w.knock(t, w.knocker, "member")

	for _, m := range []uuid.UUID{w.host, w.invitee, w.otherInvitee} {
		p := w.pending(t, m)
		if len(p) != 1 || p[0].ID != first.ID {
			t.Errorf("every member sees the one pending request, got %+v", p)
		}
	}
	if code, _ := w.requests(t, w.knocker, "member"); code != http.StatusForbidden && code != http.StatusNotFound {
		t.Errorf("the knocker cannot read the requests list, got %d", code)
	}
	if code, _ := w.requests(t, w.bystander, "member"); code != http.StatusForbidden && code != http.StatusNotFound {
		t.Errorf("a non-member cannot read the requests list, got %d", code)
	}
}

func TestAnyMemberAdmitsNotJustTheHostAndTheAdmittedPersonReconnectsWithoutKnocking(t *testing.T) {
	w := newKnockWorld(t)
	req := w.mustKnock(t, w.knocker)

	res := w.answer(t, "admit", w.otherInvitee, "member", req.ID)
	if !is2xx(res) {
		t.Fatalf("any member may admit, got %d: %s", res.Code, res.Body.String())
	}
	if !w.isMember(t, w.knocker) {
		t.Fatal("an admitted person becomes a member")
	}

	first := w.token(t, w.knocker, "member")
	wantCode(t, first, http.StatusOK)
	var tok struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &tok)
	if tok.Token == "" {
		t.Error("admitting must lead to a join token")
	}
	// Reconnecting later does not knock again.
	wantCode(t, w.token(t, w.knocker, "member"), http.StatusOK)
	if len(w.pending(t, w.host)) != 0 {
		t.Error("nothing is left pending after an admit")
	}

	log := getLog(t, w.asOwner(t), w.call.ID)
	knocked, admitted := log.events("knocked", w.knocker), log.events("admitted", w.knocker)
	if len(knocked) != 1 || len(admitted) != 1 {
		t.Fatalf("knocked and admitted are both on the trail, got %+v", log.Events)
	}
	if admitted[0].ActorID != w.otherInvitee.String() {
		t.Errorf("the trail says who admitted, want %s got %+v", w.otherInvitee, admitted[0])
	}
}

func TestAMemberOfTheCallsChannelCanAdmitToo(t *testing.T) {
	e := newLK(t)
	insider := e.person(t, "Channel Cleo", "member")
	knocker := e.person(t, "Knocking Kim", "member")
	channel := testsupport.SeedChannel(t, e.pool, e.org, "private-room", e.org.OwnerID, insider)
	call, err := e.svc.CreateCall(context.Background(), e.org.ID, e.org.OwnerID, nil, "video", &channel, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A private channel's call is private to that channel.
	w := &knockWorld{lkEnv: e, host: e.org.OwnerID, invitee: insider, knocker: knocker, call: call}
	req := w.mustKnock(t, knocker)
	if res := w.answer(t, "admit", insider, "member", req.ID); !is2xx(res) {
		t.Fatalf("a channel member is a member of the call and may admit, got %d: %s", res.Code, res.Body.String())
	}
	wantCode(t, w.token(t, knocker, "member"), http.StatusOK)
}

func TestSomebodyWhoIsNotAMemberCannotAdmitOrDenyAndNeitherCanTheKnocker(t *testing.T) {
	w := newKnockWorld(t)
	req := w.mustKnock(t, w.knocker)

	for name, who := range map[string]uuid.UUID{"the knocker": w.knocker, "a bystander": w.bystander} {
		for _, verb := range []string{"admit", "deny"} {
			res := w.answer(t, verb, who, "member", req.ID)
			if res.Code != http.StatusForbidden && res.Code != http.StatusNotFound {
				t.Errorf("%s must not %s, got %d", name, verb, res.Code)
			}
		}
	}
	if w.isMember(t, w.knocker) || w.isMember(t, w.bystander) {
		t.Error("nobody became a member")
	}
	if len(w.pending(t, w.host)) != 1 {
		t.Error("the request is still waiting")
	}
	// An org admin who is not in the call cannot admit either.
	admin := w.person(t, "Admin Ada", "admin")
	if res := w.answer(t, "admit", admin, "admin", req.ID); res.Code != http.StatusForbidden && res.Code != http.StatusNotFound {
		t.Errorf("an admin who is not in the call is not a member of it, got %d", res.Code)
	}
}

func TestADeniedKnockerStaysOutAndTheDenialIsOnTheTrail(t *testing.T) {
	w := newKnockWorld(t)
	req := w.mustKnock(t, w.knocker)
	if res := w.answer(t, "deny", w.invitee, "member", req.ID); !is2xx(res) {
		t.Fatalf("a member may deny, got %d: %s", res.Code, res.Body.String())
	}
	if w.isMember(t, w.knocker) {
		t.Error("denied is not admitted")
	}
	if w.token(t, w.knocker, "member").Code != http.StatusForbidden {
		t.Error("a denied person still gets no token")
	}
	// Answering twice is not allowed to flip the outcome.
	if res := w.answer(t, "admit", w.host, "member", req.ID); is2xx(res) {
		t.Errorf("an answered request cannot be answered again, got %d", res.Code)
	}
	if w.isMember(t, w.knocker) {
		t.Error("a second answer changed the outcome")
	}
	denied := getLog(t, w.asOwner(t), w.call.ID).events("denied", w.knocker)
	if len(denied) != 1 || denied[0].ActorID != w.invitee.String() {
		t.Errorf("the denial and who made it are on the trail, got %+v", denied)
	}
}

func TestADeniedKnockerMayTryAgainOnlyAfterAMinute(t *testing.T) {
	w := newKnockWorld(t)
	req := w.mustKnock(t, w.knocker)
	wantCode(t, w.answer(t, "deny", w.invitee, "member", req.ID), http.StatusOK, http.StatusNoContent)

	res, _ := w.knock(t, w.knocker, "member")
	if res.Code != http.StatusTooManyRequests {
		t.Fatalf("knocking again straight after a denial: want 429, got %d: %s", res.Code, res.Body.String())
	}
	if res.Header().Get("Retry-After") == "" {
		t.Error("say when to try again with a Retry-After header")
	}

	ageCall(t, w.pool, w.call.ID, 61*time.Second)
	if res, again := w.knock(t, w.knocker, "member"); !is2xx(res) || again.Status != "pending" || again.ID == req.ID {
		t.Errorf("after a minute a new request is allowed, got %d %+v", res.Code, again)
	}
}

// ── requests end with the call ────────────────────────────────────────────

func TestPendingRequestsEndWhenTheCallEnds(t *testing.T) {
	w := newKnockWorld(t)
	req := w.mustKnock(t, w.knocker)

	wantCode(t, doJSON(t, w.as(t, w.host, "member"), http.MethodPost, w.path("/end"), ""), http.StatusOK)

	if p := w.pending(t, w.invitee); len(p) != 0 {
		t.Errorf("a request cannot outlive its call, got %+v", p)
	}
	if res := w.answer(t, "admit", w.invitee, "member", req.ID); is2xx(res) {
		t.Errorf("admitting into an ended call must fail, got %d", res.Code)
	}
	if w.isMember(t, w.knocker) {
		t.Error("nobody is admitted into a call that is over")
	}
	if res, _ := w.knock(t, w.bystander, "member"); res.Code != http.StatusConflict && res.Code != http.StatusGone {
		t.Errorf("knocking on a call that ended: want 409 or 410, got %d", res.Code)
	}
}

// ── who is told, and how ──────────────────────────────────────────────────

func TestOnlyMembersConnectedRightNowAreRungInTheCallAndTheyAreToldWhenItIsAnswered(t *testing.T) {
	w := newKnockWorld(t)
	// Ivy is connected (LiveKit says so). Ian and the host are not.
	w.mustDeliver(t, joinedEv(w.call.RoomName, w.invitee, "PA_ivy", baseTime()))
	w.frames.reset()

	req := w.mustKnock(t, w.knocker)

	if got := w.frames.knockFrames(w.invitee); len(got) != 1 || got[0].Payload["requestId"] != req.ID {
		t.Fatalf("the connected member is pinged with the request id, got %+v", got)
	}
	for name, who := range map[string]uuid.UUID{"the host": w.host, "Ian": w.otherInvitee, "the knocker": w.knocker} {
		if got := w.frames.knockFrames(who); len(got) != 0 {
			t.Errorf("%s is not connected, so is not pinged in the call, got %+v", name, got)
		}
	}

	// Ivy is connected, so nobody is pushed a notification instead.
	w.push.mu.Lock()
	pushed := len(w.push.pushed)
	w.push.mu.Unlock()
	if pushed != 0 {
		t.Errorf("somebody was connected, so there is no fallback notification, got %v", w.push.pushed)
	}

	// A second connected member sees the prompt, then it clears when Ivy answers.
	w.mustDeliver(t, joinedEv(w.call.RoomName, w.host, "PA_host", baseTime()))
	w.frames.reset()
	req2 := w.mustKnock(t, w.bystander)
	if len(w.frames.knockFrames(w.host)) != 1 || len(w.frames.knockFrames(w.invitee)) != 1 {
		t.Fatalf("both connected members are pinged")
	}
	w.frames.reset()
	wantCode(t, w.answer(t, "admit", w.invitee, "member", req2.ID), http.StatusOK, http.StatusNoContent)

	cleared := w.frames.knockFrames(w.host)
	if len(cleared) != 1 || cleared[0].Payload["requestId"] != req2.ID {
		t.Errorf("the other connected member's prompt is cleared once somebody answers, got %+v", cleared)
	}
	result := w.frames.knockFrames(w.bystander)
	if len(result) != 1 || result[0].Payload["requestId"] != req2.ID || result[0].Payload["status"] != "admitted" {
		t.Errorf("the knocker is told the result (status admitted), got %+v", result)
	}
}

func TestWithNobodyConnectedTheOrganizerAndInviteesAreNotifiedAndTheRequestWaits(t *testing.T) {
	w := newKnockWorld(t)
	req := w.mustKnock(t, w.knocker)

	for name, who := range map[string]uuid.UUID{"the organizer": w.host, "Ivy": w.invitee, "Ian": w.otherInvitee} {
		if !w.notified(t, who) {
			t.Errorf("%s should have been notified that somebody is at the door", name)
		}
	}
	for name, who := range map[string]uuid.UUID{"the knocker": w.knocker, "a bystander": w.bystander} {
		if w.notified(t, who) {
			t.Errorf("%s is not a member and must not be notified", name)
		}
	}
	// It waits, and is still answerable later.
	if p := w.pending(t, w.invitee); len(p) != 1 || p[0].ID != req.ID {
		t.Errorf("the request waits for somebody to come online, got %+v", p)
	}
	wantCode(t, w.answer(t, "admit", w.invitee, "member", req.ID), http.StatusOK, http.StatusNoContent)
}

// notified is true if the person was pushed, or has a bell entry.
func (w *knockWorld) notified(t *testing.T, who uuid.UUID) bool {
	t.Helper()
	w.push.mu.Lock()
	n := w.push.pushed[who]
	w.push.mu.Unlock()
	if n > 0 {
		return true
	}
	var rows int
	if err := w.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM notifications WHERE user_id = $1`, who).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows > 0
}

// Whoever declined is named to the caller, in a toast, so it has to be the name
// they go by and not their login.
func TestTheCallerIsToldTheNameOfWhoeverDeclinedNotTheirEmail(t *testing.T) {
	w := newKnockWorld(t)
	w.frames.reset()
	wantCode(t, doJSON(t, w.as(t, w.invitee, "member"), http.MethodPost, w.path("/decline"), `{"reason":"declined"}`), http.StatusOK)

	var named string
	for i := 0; i < 50 && named == ""; i++ {
		w.frames.mu.Lock()
		for _, fr := range w.frames.frames {
			if fr.To == w.host.String() && fr.Type == "call.ended" {
				named, _ = fr.Payload["declinedBy"].(string)
			}
		}
		w.frames.mu.Unlock()
		if named == "" {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if named != "Invited Ivy" {
		t.Errorf("the caller should be told %q, got %q", "Invited Ivy", named)
	}
}
