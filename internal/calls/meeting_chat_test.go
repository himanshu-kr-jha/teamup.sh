package calls

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// chatsIn reads a meeting's chat through the route a page uses.
func chatsIn(t *testing.T, r *gin.Engine, path string, headers ...string) ([]ChatMessage, int) {
	t.Helper()
	w := guestRequest(t, r, http.MethodGet, path, "", headers...)
	var body struct {
		Messages []ChatMessage `json:"messages"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return body.Messages, w.Code
}

// guestRequest is a request with headers given as name, value pairs.
func guestRequest(t *testing.T, r *gin.Engine, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestPeopleInAMeetingChatAndOnlyTheyCanReadItAfterwards(t *testing.T) {
	e := newLK(t)
	org := e.person(t, "Olga Organizer", "member")
	inv := e.person(t, "Ivan Invited", "member")
	away := e.person(t, "Alma Away", "member")
	other := e.person(t, "Otto Other", "member")
	call := e.call(t, org, inv, away)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `UPDATE calls SET status = 'active' WHERE id = $1`, call.ID); err != nil {
		t.Fatal(err)
	}
	media := &fakeMedia{present: map[string]bool{
		call.RoomName + "/" + org.String(): true,
		call.RoomName + "/" + inv.String(): true,
	}}
	e.svc.media = media
	path := "/api/calls/" + call.ID.String() + "/chat"

	w := doJSON(t, e.as(t, org, "member"), http.MethodPost, path, `{"content":"  Slides: https://example.test/deck  "}`)
	wantCode(t, w, http.StatusCreated)
	var sent ChatMessage
	_ = json.Unmarshal(w.Body.Bytes(), &sent)
	if sent.Content != "Slides: https://example.test/deck" || sent.SenderID != org.String() || sent.SenderName != "Olga Organizer" || sent.Guest {
		t.Errorf("the message as kept: %+v", sent)
	}

	// Everybody in the room is sent it at once, on the chat's own topic.
	if len(media.sent) != 1 || media.sent[0].room != call.RoomName || media.sent[0].topic != MeetingChatTopic {
		t.Fatalf("delivered to the room: %+v", media.sent)
	}
	var delivered ChatMessage
	if err := json.Unmarshal(media.sent[0].data, &delivered); err != nil || delivered.ID != sent.ID || delivered.Content != sent.Content {
		t.Errorf("what the room got: %s (%v)", media.sent[0].data, err)
	}

	wantCode(t, doJSON(t, e.as(t, inv, "member"), http.MethodPost, path, `{"content":"Thanks"}`), http.StatusCreated)

	// Somebody invited but not in the room reads along, and cannot write until they join.
	got, code := chatsIn(t, e.as(t, away, "member"), path)
	if code != http.StatusOK || len(got) != 2 || got[0].Content != sent.Content || got[1].SenderName != "Ivan Invited" {
		t.Errorf("an invited member reads the chat in order: %d %+v", code, got)
	}
	wantCode(t, doJSON(t, e.as(t, away, "member"), http.MethodPost, path, `{"content":"hello?"}`), http.StatusForbidden)

	// Somebody with no business in the meeting cannot tell that it has a chat.
	_, code = chatsIn(t, e.as(t, other, "member"), path)
	wantCode(t, &httptest.ResponseRecorder{Code: code}, http.StatusNotFound)
	wantCode(t, doJSON(t, e.as(t, other, "member"), http.MethodPost, path, `{"content":"hi"}`), http.StatusNotFound)

	wantCode(t, doJSON(t, e.as(t, org, "member"), http.MethodPost, path, `{"content":"   "}`), http.StatusBadRequest)
	wantCode(t, doJSON(t, e.as(t, org, "member"), http.MethodPost, path, `{"content":"`+strings.Repeat("é", maxChatMessage+1)+`"}`), http.StatusBadRequest)
	wantCode(t, doJSON(t, e.as(t, org, "member"), http.MethodPost, path, `{"content":"`+strings.Repeat("é", maxChatMessage)+`"}`), http.StatusCreated)

	// Once it is over nobody writes, and the people who were in it still read it.
	if _, err := e.pool.Exec(ctx, `UPDATE calls SET status = 'ended', ended_at = NOW() WHERE id = $1`, call.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, doJSON(t, e.as(t, org, "member"), http.MethodPost, path, `{"content":"late"}`), http.StatusConflict)
	if got, code := chatsIn(t, e.as(t, inv, "member"), path); code != http.StatusOK || len(got) != 3 {
		t.Errorf("afterwards the chat stays with the call: %d %d messages", code, len(got))
	}
	_, code = chatsIn(t, e.as(t, other, "member"), path)
	wantCode(t, &httptest.ResponseRecorder{Code: code}, http.StatusNotFound)
}

func TestAGuestChatsWithTheirKeyUntilTheyArePutOut(t *testing.T) {
	e := newLK(t)
	org := e.person(t, "Olga Organizer", "member")
	call := e.call(t, org)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `UPDATE calls SET status = 'active' WHERE id = $1`, call.ID); err != nil {
		t.Fatal(err)
	}
	media := &fakeMedia{present: map[string]bool{call.RoomName + "/" + org.String(): true}}
	e.svc.media = media
	link, err := e.svc.SetGuestLink(ctx, e.org.ID, call.ID, org, false, GuestLinkChange{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	guest := e.guestRouter(t)
	at := "/api/guest/" + strings.TrimPrefix(link.Path, "/join/")
	w := guestJoin(t, guest, at, `{"name":"Pat Partner"}`, "203.0.113.5")
	wantCode(t, w, http.StatusOK)
	var join GuestJoin
	_ = json.Unmarshal(w.Body.Bytes(), &join)
	media.present[call.RoomName+"/"+join.Identity] = true

	wantCode(t, guestRequest(t, guest, http.MethodPost, at+"/chat", `{"content":"Hello all"}`), http.StatusForbidden)
	wantCode(t, guestRequest(t, guest, http.MethodPost, at+"/chat", `{"content":"Hello all"}`, GuestKeyHeader, "not-the-key"), http.StatusForbidden)
	w = guestRequest(t, guest, http.MethodPost, at+"/chat", `{"content":"Hello all"}`, GuestKeyHeader, join.RejoinKey)
	wantCode(t, w, http.StatusCreated)
	var sent ChatMessage
	_ = json.Unmarshal(w.Body.Bytes(), &sent)
	if sent.SenderID != join.Identity || sent.SenderName != "Pat Partner" || !sent.Guest {
		t.Errorf("a guest's message says it is a guest's: %+v", sent)
	}

	wantCode(t, doJSON(t, e.as(t, org, "member"), http.MethodPost, "/api/calls/"+call.ID.String()+"/chat", `{"content":"Welcome Pat"}`), http.StatusCreated)
	got, code := chatsIn(t, guest, at+"/chat", GuestKeyHeader, join.RejoinKey)
	if code != http.StatusOK || len(got) != 2 || got[1].SenderName != "Olga Organizer" {
		t.Errorf("the guest reads the whole chat: %d %+v", code, got)
	}

	// Put out of the meeting, a guest neither reads nor writes.
	guestID := strings.TrimPrefix(join.Identity, GuestIdentityPrefix)
	if _, err := e.pool.Exec(ctx, `UPDATE call_guests SET removed_at = NOW() WHERE id = $1`, guestID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, guestRequest(t, guest, http.MethodPost, at+"/chat", `{"content":"still here"}`, GuestKeyHeader, join.RejoinKey), http.StatusForbidden)
	_, code = chatsIn(t, guest, at+"/chat", GuestKeyHeader, join.RejoinKey)
	wantCode(t, &httptest.ResponseRecorder{Code: code}, http.StatusForbidden)
}

func TestAGuestHasNoChatOnceTheMeetingIsOver(t *testing.T) {
	e := newLK(t)
	org := e.person(t, "Olga Organizer", "member")
	call := e.call(t, org)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `UPDATE calls SET status = 'active' WHERE id = $1`, call.ID); err != nil {
		t.Fatal(err)
	}
	e.svc.media = &fakeMedia{present: map[string]bool{}}
	link, err := e.svc.SetGuestLink(ctx, e.org.ID, call.ID, org, false, GuestLinkChange{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	guest := e.guestRouter(t)
	at := "/api/guest/" + strings.TrimPrefix(link.Path, "/join/")
	w := guestJoin(t, guest, at, `{"name":"Pat Partner"}`, "203.0.113.5")
	wantCode(t, w, http.StatusOK)
	var join GuestJoin
	_ = json.Unmarshal(w.Body.Bytes(), &join)

	// Let in but not connected yet: nothing to write in.
	wantCode(t, guestRequest(t, guest, http.MethodPost, at+"/chat", `{"content":"hi"}`, GuestKeyHeader, join.RejoinKey), http.StatusForbidden)

	if _, err := e.pool.Exec(ctx, `UPDATE calls SET status = 'ended', ended_at = NOW() WHERE id = $1`, call.ID); err != nil {
		t.Fatal(err)
	}
	_, code := chatsIn(t, guest, at+"/chat", GuestKeyHeader, join.RejoinKey)
	wantCode(t, &httptest.ResponseRecorder{Code: code}, http.StatusConflict)
}

func TestNobodyFloodsAMeetingChat(t *testing.T) {
	e := newLK(t)
	org := e.person(t, "Olga Organizer", "member")
	call := e.call(t, org)
	if _, err := e.pool.Exec(context.Background(), `UPDATE calls SET status = 'active' WHERE id = $1`, call.ID); err != nil {
		t.Fatal(err)
	}
	e.svc.media = &fakeMedia{present: map[string]bool{call.RoomName + "/" + org.String(): true}}
	path := "/api/calls/" + call.ID.String() + "/chat"
	r := e.as(t, org, "member")
	for i := 0; i < chatBurst; i++ {
		wantCode(t, doJSON(t, r, http.MethodPost, path, `{"content":"line"}`), http.StatusCreated)
	}
	wantCode(t, doJSON(t, r, http.MethodPost, path, `{"content":"one too many"}`), http.StatusTooManyRequests)
}
