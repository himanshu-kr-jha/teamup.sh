package calls

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"

	"github.com/teamup/teamup/internal/testsupport"
)

// guestRouter is what somebody with no account reaches.
func (e *lkEnv) guestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterGuestRoutes(r.Group("/api"), e.svc)
	return r
}

// guestJoin asks to join from an address.
func guestJoin(t *testing.T, r *gin.Engine, path, body, from string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path+"/join", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = from + ":40000"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func guestJoinedEv(room, identity string) *livekit.WebhookEvent {
	now := time.Now()
	return &livekit.WebhookEvent{
		Event: "participant_joined", Id: "EV_" + uuid.NewString(), CreatedAt: now.Unix(),
		Room:        lkRoom(room),
		Participant: &livekit.ParticipantInfo{Identity: identity, Sid: "PA_" + uuid.NewString()[:8], JoinedAt: now.Unix()},
	}
}

func TestOnlyTheOrganizerLetsGuestsInAndTheInvitedCanPassTheLinkOn(t *testing.T) {
	e := newLK(t)
	org := e.person(t, "Olga Organizer", "member")
	inv := e.person(t, "Ivan Invited", "member")
	other := e.person(t, "Otto Other", "member")
	call := e.call(t, org, inv)
	path := "/api/calls/" + call.ID.String() + "/guest-link"

	wantCode(t, doJSON(t, e.as(t, inv, "member"), http.MethodPut, path, `{"enabled":true}`), http.StatusForbidden)
	wantCode(t, doJSON(t, e.as(t, other, "member"), http.MethodGet, path, ""), http.StatusNotFound)
	wantCode(t, doJSON(t, e.as(t, org, "member"), http.MethodPut, path, `{"enabled":true,"passcode":"12"}`), http.StatusBadRequest)

	w := doJSON(t, e.as(t, org, "member"), http.MethodPut, path, `{"enabled":true,"passcode":" 482913 "}`)
	wantCode(t, w, http.StatusOK)
	var link GuestLink
	_ = json.Unmarshal(w.Body.Bytes(), &link)
	if !link.Enabled || !strings.HasPrefix(link.Path, "/join/") || len(link.Path) < 25 || link.Passcode != "482913" || !link.CanManage {
		t.Fatalf("organizer's view: %+v", link)
	}

	var seen GuestLink
	w = doJSON(t, e.as(t, inv, "member"), http.MethodGet, path, "")
	_ = json.Unmarshal(w.Body.Bytes(), &seen)
	if seen.Path != link.Path || seen.Passcode != "482913" || seen.CanManage {
		t.Errorf("an invited member sees the link to pass on, and cannot change it: %+v", seen)
	}

	// Changing the passcode keeps the address; asking for a new one changes it.
	w = doJSON(t, e.as(t, org, "member"), http.MethodPut, path, `{"enabled":true}`)
	_ = json.Unmarshal(w.Body.Bytes(), &seen)
	if seen.Path != link.Path || seen.Passcode != "482913" {
		t.Errorf("unchanged: %+v", seen)
	}
	w = doJSON(t, e.as(t, org, "member"), http.MethodPut, path, `{"enabled":true,"newLink":true,"passcode":""}`)
	seen = GuestLink{}
	_ = json.Unmarshal(w.Body.Bytes(), &seen)
	if seen.Path == link.Path || seen.Passcode != "" {
		t.Errorf("a new address, and no passcode: %+v", seen)
	}
	wantCode(t, doJSON(t, e.guestRouter(t), http.MethodGet, "/api/guest/"+strings.TrimPrefix(link.Path, "/join/"), ""), http.StatusNotFound)

	// An administrator of another organization cannot see it at all.
	org2 := testsupport.SeedOrg(t, e.pool)
	stranger := routerFor(t, e.svc, org2.ID, org2.OwnerID, "owner")
	wantCode(t, doJSON(t, stranger, http.MethodGet, path, ""), http.StatusNotFound)
	wantCode(t, doJSON(t, stranger, http.MethodPut, path, `{"enabled":false}`), http.StatusNotFound)
}

func TestAGuestComesInByNameAndPasscodeOnlyWhileTheMeetingIsOn(t *testing.T) {
	e := newLK(t)
	media := &fakeMedia{}
	e.svc.media = media
	org := e.person(t, "Olga Organizer", "member")
	call := e.call(t, org)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `UPDATE calls SET title = 'Design review', status = 'scheduled',
		scheduled_start = NOW() + interval '1 hour', scheduled_end = NOW() + interval '2 hours' WHERE id = $1`, call.ID); err != nil {
		t.Fatal(err)
	}
	link, err := e.svc.SetGuestLink(ctx, e.org.ID, call.ID, org, false, GuestLinkChange{Enabled: true, Passcode: ptr("482913")})
	if err != nil {
		t.Fatal(err)
	}
	guest := e.guestRouter(t)
	at := "/api/guest/" + strings.TrimPrefix(link.Path, "/join/")

	var m GuestMeeting
	w := doJSON(t, guest, http.MethodGet, at, "")
	wantCode(t, w, http.StatusOK)
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	if m.Title != "Design review" || m.State != "waiting" || !m.PasscodeRequired || m.StartsAt == nil {
		t.Errorf("before it starts: %+v", m)
	}
	if strings.Contains(w.Body.String(), "482913") || strings.Contains(w.Body.String(), call.RoomName) {
		t.Errorf("the page is told neither the passcode nor the room: %s", w.Body.String())
	}

	right := `{"name":"  Pat   Partner ","passcode":"482913"}`
	wantCode(t, guestJoin(t, guest, at, right, "203.0.113.5"), http.StatusConflict) // not started
	wantCode(t, guestJoin(t, guest, at, `{"name":"","passcode":"482913"}`, "203.0.113.5"), http.StatusBadRequest)
	wantCode(t, guestJoin(t, guest, at, `{"name":"`+strings.Repeat("x", 61)+`","passcode":"482913"}`, "203.0.113.5"), http.StatusBadRequest)

	if _, err := e.pool.Exec(ctx, `UPDATE calls SET status = 'active' WHERE id = $1`, call.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, guestJoin(t, guest, at, `{"name":"Pat","passcode":"000000"}`, "203.0.113.5"), http.StatusUnauthorized)

	w = guestJoin(t, guest, at, right, "203.0.113.5")
	wantCode(t, w, http.StatusOK)
	var join GuestJoin
	_ = json.Unmarshal(w.Body.Bytes(), &join)
	if join.Name != "Pat Partner" || join.RoomName != call.RoomName || !strings.HasPrefix(join.Identity, GuestIdentityPrefix) {
		t.Fatalf("join: %+v", join)
	}
	verifier, err := auth.ParseAPIToken(join.Token)
	if err != nil {
		t.Fatal(err)
	}
	grants, err := verifier.Verify(lkSecret)
	if err != nil {
		t.Fatal(err)
	}
	if grants.Identity != join.Identity || grants.Video.Room != call.RoomName || !grants.Video.RoomJoin || grants.Name != "Pat Partner" ||
		!strings.Contains(grants.Metadata, "guest") {
		t.Errorf("a token for this room only, as a guest: %+v %+v", grants, grants.Video)
	}

	// The webhook sees them come in and leaves them be; they are not a member.
	e.mustDeliver(t, guestJoinedEv(call.RoomName, join.Identity))
	if len(media.removals()) != 0 {
		t.Errorf("a welcome guest stays: %v", media.removals())
	}
	var members int
	_ = e.pool.QueryRow(ctx, `SELECT COUNT(*) FROM call_participants WHERE call_id = $1`, call.ID).Scan(&members)
	if members != 1 {
		t.Errorf("a guest is not a participant row: %d", members)
	}

	// The organizer puts them out, and they cannot come back on the same token.
	guestID := strings.TrimPrefix(join.Identity, GuestIdentityPrefix)
	wantCode(t, doJSON(t, e.as(t, org, "member"), http.MethodPost, "/api/calls/"+call.ID.String()+"/guests/"+guestID+"/remove", ""), http.StatusNoContent)
	e.mustDeliver(t, guestJoinedEv(call.RoomName, join.Identity))
	if got := media.removals(); len(got) != 2 || got[1] != call.RoomName+"/"+join.Identity {
		t.Errorf("put out, and out again on return: %v", got)
	}
	// Somebody inventing a guest identity is put out too.
	e.mustDeliver(t, guestJoinedEv(call.RoomName, GuestIdentityPrefix+uuid.NewString()))
	if got := media.removals(); len(got) != 3 {
		t.Errorf("an unknown guest: %v", got)
	}

	// Turning the link off ends it for everybody.
	w = guestJoin(t, guest, at, right, "203.0.113.6")
	_ = json.Unmarshal(w.Body.Bytes(), &join)
	if _, err := e.svc.SetGuestLink(ctx, e.org.ID, call.ID, org, false, GuestLinkChange{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if got := media.removals(); got[len(got)-1] != call.RoomName+"/"+join.Identity {
		t.Errorf("guests put out when the link goes off: %v", got)
	}
	wantCode(t, doJSON(t, guest, http.MethodGet, at, ""), http.StatusNotFound)
	wantCode(t, guestJoin(t, guest, at, right, "203.0.113.7"), http.StatusNotFound)

	var guests int
	_ = e.pool.QueryRow(ctx, `SELECT COUNT(DISTINCT name) FROM call_guests WHERE call_id = $1 AND name = 'Pat Partner'`, call.ID).Scan(&guests)
	if guests != 1 {
		t.Errorf("the guest is on record under the name they gave: %d", guests)
	}
}

func TestGuessingAPasscodeIsStoppedByAddressAndForTheLink(t *testing.T) {
	e := newLK(t)
	org := e.person(t, "Olga Organizer", "member")
	call := e.call(t, org)
	ctx := context.Background()
	link, err := e.svc.SetGuestLink(ctx, e.org.ID, call.ID, org, false, GuestLinkChange{Enabled: true, Passcode: ptr("482913")})
	if err != nil {
		t.Fatal(err)
	}
	guest := e.guestRouter(t)
	at := "/api/guest/" + strings.TrimPrefix(link.Path, "/join/")
	right := `{"name":"Pat","passcode":"482913"}`
	for i := 0; i < attemptsPerAddress; i++ {
		wantCode(t, guestJoin(t, guest, at, `{"name":"Pat","passcode":"111111"}`, "203.0.113.5"), http.StatusUnauthorized)
	}
	wantCode(t, guestJoin(t, guest, at, right, "203.0.113.5"), http.StatusTooManyRequests)
	wantCode(t, guestJoin(t, guest, at, right, "203.0.113.9"), http.StatusOK)

	// A visitor naming another address in a header is not believed.
	req := httptest.NewRequest(http.MethodPost, at+"/join", strings.NewReader(right))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Real-IP", "198.51.100.1")
	req.RemoteAddr = "203.0.113.5:4000"
	if got := clientAddress(req); got != "203.0.113.5" {
		t.Errorf("a public peer's header is ignored: %s", got)
	}
	req.RemoteAddr = "172.18.0.4:4000" // the bundled nginx
	if got := clientAddress(req); got != "198.51.100.1" {
		t.Errorf("the proxy's header is used: %s", got)
	}

	// Many addresses together still hit the link's limit.
	if _, err := e.pool.Exec(ctx, `INSERT INTO call_guest_attempts (call_id, ip) SELECT $1, '198.18.0.' || g FROM generate_series(1, $2) g`, call.ID, attemptsPerLink); err != nil {
		t.Fatal(err)
	}
	wantCode(t, guestJoin(t, guest, at, right, "203.0.113.10"), http.StatusTooManyRequests)
	// And they age out.
	if _, err := e.pool.Exec(ctx, `UPDATE call_guest_attempts SET at = at - interval '11 minutes' WHERE call_id = $1`, call.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, guestJoin(t, guest, at, right, "203.0.113.5"), http.StatusOK)
}

func ptr(s string) *string { return &s }
