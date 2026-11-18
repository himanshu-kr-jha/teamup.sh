package calls

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/livekit/protocol/auth"

	"github.com/teamup/teamup/internal/testsupport"
)

// handEnv is a call with an organizer and two members in its room, and an
// administrator who can be put in it too, against a stand-in for LiveKit.
type handEnv struct {
	*lkEnv
	media                    *fakeMedia
	call                     *Call
	organizer, asha, ben, ad uuid.UUID
}

func newHandEnv(t *testing.T) *handEnv {
	t.Helper()
	e := newLK(t)
	h := &handEnv{lkEnv: e, media: &fakeMedia{present: map[string]bool{}}}
	e.svc.media = h.media
	h.organizer = e.person(t, "Olga Organizer", "member")
	h.asha = e.person(t, "Asha Asker", "member")
	h.ben = e.person(t, "Ben Bystander", "member")
	h.ad = e.person(t, "Ada Admin", "admin")
	h.call = e.call(t, h.organizer, h.asha, h.ben)
	for _, id := range []uuid.UUID{h.organizer, h.asha, h.ben} {
		h.enter(id)
	}
	return h
}

func (h *handEnv) enter(id uuid.UUID) { h.media.present[h.call.RoomName+"/"+id.String()] = true }
func (h *handEnv) leave(id uuid.UUID) { delete(h.media.present, h.call.RoomName+"/"+id.String()) }

func (h *handEnv) hand(id uuid.UUID) string {
	h.media.mu.Lock()
	defer h.media.mu.Unlock()
	return h.media.attrs[h.call.RoomName+"/"+id.String()][HandAttribute]
}

func (h *handEnv) put(t *testing.T, who uuid.UUID, role, body string) (int, HandState) {
	t.Helper()
	w := doJSON(t, h.as(t, who, role), http.MethodPut, "/api/calls/"+h.call.ID.String()+"/hand", body)
	var state HandState
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
			t.Fatalf("decode %s: %v", w.Body.String(), err)
		}
	}
	return w.Code, state
}

func TestHandRaisedAndLoweredByItsOwner(t *testing.T) {
	h := newHandEnv(t)
	now := time.Date(2026, 11, 18, 9, 30, 15, 250_000_000, time.UTC)
	h.svc.SetClock(func() time.Time { return now })

	code, state := h.put(t, h.asha, "member", `{"raised":true}`)
	if code != http.StatusOK || !state.Raised || state.UserID != h.asha {
		t.Fatalf("raising your own hand: %d %+v", code, state)
	}
	if got := h.hand(h.asha); got != "2026-11-18T09:30:15.250Z" {
		t.Fatalf("the hand attribute should be the time it went up, got %q", got)
	}
	if _, err := time.Parse(time.RFC3339, h.hand(h.asha)); err != nil {
		t.Fatalf("the hand attribute should be RFC 3339: %v", err)
	}

	// Asking again later keeps its place in the queue.
	now = now.Add(time.Minute)
	if code, state = h.put(t, h.asha, "member", `{"raised":true}`); code != http.StatusOK || state.RaisedAt != "2026-11-18T09:30:15.250Z" {
		t.Fatalf("raising again should keep the first time: %d %+v", code, state)
	}

	if code, state = h.put(t, h.asha, "member", `{"raised":false}`); code != http.StatusOK || state.Raised {
		t.Fatalf("lowering your own hand: %d %+v", code, state)
	}
	if got := h.hand(h.asha); got != "" {
		t.Fatalf("a lowered hand should have no attribute, got %q", got)
	}
	// Lowering a hand that is down is not an error.
	if code, _ = h.put(t, h.asha, "member", `{"raised":false}`); code != http.StatusOK {
		t.Fatalf("lowering a lowered hand: %d", code)
	}
	// Naming yourself is the same as naming nobody.
	if code, _ = h.put(t, h.asha, "member", `{"raised":true,"userId":"`+h.asha.String()+`"}`); code != http.StatusOK || h.hand(h.asha) == "" {
		t.Fatalf("raising your own hand by id: %d", code)
	}
}

func TestNobodyRaisesAHandForSomebodyElse(t *testing.T) {
	h := newHandEnv(t)
	for _, who := range []struct {
		id   uuid.UUID
		role string
	}{{h.ben, "member"}, {h.organizer, "member"}, {h.ad, "admin"}} {
		h.enter(who.id)
		if code, _ := h.put(t, who.id, who.role, `{"raised":true,"userId":"`+h.asha.String()+`"}`); code != http.StatusForbidden {
			t.Fatalf("raising somebody else's hand should be refused, got %d", code)
		}
	}
	if got := h.hand(h.asha); got != "" {
		t.Fatalf("nobody should have raised Asha's hand, got %q", got)
	}
}

func TestOnlyTheOrganizerOrAnAdminLowersSomebodyElsesHand(t *testing.T) {
	h := newHandEnv(t)
	raise := func() {
		t.Helper()
		if code, _ := h.put(t, h.asha, "member", `{"raised":true}`); code != http.StatusOK {
			t.Fatalf("raise: %d", code)
		}
	}
	lowerAsha := `{"raised":false,"userId":"` + h.asha.String() + `"}`

	raise()
	if code, _ := h.put(t, h.ben, "member", lowerAsha); code != http.StatusForbidden {
		t.Fatalf("another member lowering Asha's hand should be refused, got %d", code)
	}
	if h.hand(h.asha) == "" {
		t.Fatal("a refused lowering must leave the hand up")
	}

	if code, state := h.put(t, h.organizer, "member", lowerAsha); code != http.StatusOK || state.Raised || state.UserID != h.asha {
		t.Fatalf("the organizer lowering Asha's hand: %d %+v", code, state)
	}
	if h.hand(h.asha) != "" {
		t.Fatal("the organizer should have lowered Asha's hand")
	}

	raise()
	h.enter(h.ad)
	if code, _ := h.put(t, h.ad, "admin", lowerAsha); code != http.StatusOK || h.hand(h.asha) != "" {
		t.Fatalf("an administrator in the call lowering Asha's hand: %d", code)
	}
}

func TestHandsAreOnlyForPeopleInTheCall(t *testing.T) {
	h := newHandEnv(t)
	// Invited, but not connected: nothing to raise a hand in.
	h.leave(h.asha)
	if code, _ := h.put(t, h.asha, "member", `{"raised":true}`); code != http.StatusForbidden {
		t.Fatalf("raising a hand outside the room should be refused, got %d", code)
	}

	// An administrator who is not in the room cannot reach into it either.
	h.enter(h.asha)
	if code, _ := h.put(t, h.asha, "member", `{"raised":true}`); code != http.StatusOK {
		t.Fatalf("raise: %d", code)
	}
	if code, _ := h.put(t, h.ad, "admin", `{"raised":false,"userId":"`+h.asha.String()+`"}`); code != http.StatusForbidden {
		t.Fatalf("an administrator outside the call lowering a hand should be refused, got %d", code)
	}

	// The organizer lowering the hand of somebody who has gone.
	if code, _ := h.put(t, h.organizer, "member", `{"raised":false,"userId":"`+uuid.NewString()+`"}`); code != http.StatusNotFound {
		t.Fatalf("lowering the hand of somebody not in the call should be not found, got %d", code)
	}

	// A call in another organization is not found at all.
	other := testsupport.SeedOrg(t, h.pool)
	stranger := testsupport.SeedUser(t, h.pool, other, "sid.stranger@example.test", "member")
	w := doJSON(t, routerFor(t, h.svc, other.ID, stranger, "member"), http.MethodPut, "/api/calls/"+h.call.ID.String()+"/hand", `{"raised":true}`)
	wantCode(t, w, http.StatusNotFound)
}

func TestHandRequestMustSayUpOrDown(t *testing.T) {
	h := newHandEnv(t)
	for _, body := range []string{``, `{}`, `{"raised":"yes"}`, `{"raised":false,"userId":"not-an-id"}`} {
		if code, _ := h.put(t, h.asha, "member", body); code != http.StatusBadRequest {
			t.Fatalf("%q should be a bad request, got %d", body, code)
		}
	}
}

// The queue is only fair if nobody can write their own hand: a member's token
// must not let them change their own attributes.
func TestMemberTokensCannotWriteTheirOwnHand(t *testing.T) {
	h := newHandEnv(t)
	token, err := h.svc.GetToken(context.Background(), h.call.RoomName, h.asha.String(), "Asha Asker")
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := auth.ParseAPIToken(token)
	if err != nil {
		t.Fatal(err)
	}
	grants, err := verifier.Verify(lkSecret)
	if err != nil {
		t.Fatal(err)
	}
	if grants.Video.GetCanUpdateOwnMetadata() {
		t.Fatal("a member's token must not carry CanUpdateOwnMetadata")
	}
}
