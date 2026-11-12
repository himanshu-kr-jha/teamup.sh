package calls

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/mail"
)

type sentMail struct {
	mu         sync.Mutex
	configured bool
	sent       []mail.Message
}

func (m *sentMail) Send(_ context.Context, _ uuid.UUID, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *sentMail) Configured(context.Context, uuid.UUID) bool { return m.configured }

func (m *sentMail) to(addr string) []mail.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []mail.Message
	for _, msg := range m.sent {
		if msg.To == addr {
			out = append(out, msg)
		}
	}
	return out
}

func token(path string) string { return strings.TrimPrefix(path, "/join/") }

func TestTheDirectLinkNeedsNoPasscodeAndTheOtherStillDoes(t *testing.T) {
	e := newLK(t)
	org := e.person(t, "Olga Organizer", "member")
	call := e.call(t, org)
	link, err := e.svc.SetGuestLink(context.Background(), e.org.ID, call.ID, org, false, GuestLinkChange{Enabled: true, Passcode: ptr("482913")})
	if err != nil {
		t.Fatal(err)
	}
	if link.DirectPath == "" || link.DirectPath == link.Path {
		t.Fatalf("two different addresses: %+v", link)
	}
	guest := e.guestRouter(t)
	for path, needs := range map[string]bool{link.DirectPath: false, link.Path: true} {
		var m GuestMeeting
		_ = json.Unmarshal(doJSON(t, guest, http.MethodGet, "/api/guest/"+token(path), "").Body.Bytes(), &m)
		if m.PasscodeRequired != needs {
			t.Errorf("%s asks for the passcode: %v", path, m.PasscodeRequired)
		}
	}
	wantCode(t, guestJoin(t, guest, "/api/guest/"+token(link.DirectPath), `{"name":"Pat"}`, "203.0.113.5"), http.StatusOK)
	wantCode(t, guestJoin(t, guest, "/api/guest/"+token(link.Path), `{"name":"Pat"}`, "203.0.113.5"), http.StatusUnauthorized)

	// A new link replaces both.
	fresh, _ := e.svc.SetGuestLink(context.Background(), e.org.ID, call.ID, org, false, GuestLinkChange{Enabled: true, NewLink: true})
	if fresh.DirectPath == link.DirectPath || fresh.Path == link.Path {
		t.Errorf("both replaced: %+v", fresh)
	}
	wantCode(t, guestJoin(t, guest, "/api/guest/"+token(link.DirectPath), `{"name":"Pat"}`, "203.0.113.5"), http.StatusNotFound)
}

func TestAGuestComesBackFromTheSameBrowserWithoutThePasscode(t *testing.T) {
	e := newLK(t)
	media := &fakeMedia{}
	e.svc.media = media
	org := e.person(t, "Olga Organizer", "member")
	call := e.call(t, org)
	ctx := context.Background()
	link, _ := e.svc.SetGuestLink(ctx, e.org.ID, call.ID, org, false, GuestLinkChange{Enabled: true, Passcode: ptr("482913")})
	guest := e.guestRouter(t)
	at := "/api/guest/" + token(link.Path)

	var first GuestJoin
	w := guestJoin(t, guest, at, `{"name":"Pat Partner","passcode":"482913"}`, "203.0.113.5")
	wantCode(t, w, http.StatusOK)
	_ = json.Unmarshal(w.Body.Bytes(), &first)
	if len(first.RejoinKey) < 20 {
		t.Fatalf("a key to come back with: %+v", first)
	}

	// Back, with the key and no passcode: the same guest, under the same name.
	var again GuestJoin
	w = guestJoin(t, guest, at, `{"name":"Someone Else","rejoinKey":"`+first.RejoinKey+`"}`, "203.0.113.5")
	wantCode(t, w, http.StatusOK)
	_ = json.Unmarshal(w.Body.Bytes(), &again)
	if again.Identity != first.Identity || again.Name != "Pat Partner" || again.RejoinKey != first.RejoinKey {
		t.Errorf("the same guest: %+v", again)
	}
	var rows int
	_ = e.pool.QueryRow(ctx, `SELECT COUNT(*) FROM call_guests WHERE call_id = $1`, call.ID).Scan(&rows)
	if rows != 1 {
		t.Errorf("one guest, not two: %d", rows)
	}
	var stored string
	_ = e.pool.QueryRow(ctx, `SELECT rejoin_hash FROM call_guests WHERE call_id = $1`, call.ID).Scan(&stored)
	if stored == first.RejoinKey || stored == "" {
		t.Errorf("only a hash of the key is kept")
	}

	// A made-up key is no key: the passcode is asked for.
	wantCode(t, guestJoin(t, guest, at, `{"name":"Pat","rejoinKey":"not-a-real-key"}`, "203.0.113.5"), http.StatusUnauthorized)

	// Removed, the key no longer works.
	guestID, _ := uuid.Parse(strings.TrimPrefix(first.Identity, GuestIdentityPrefix))
	if err := e.svc.RemoveGuest(ctx, e.org.ID, call.ID, org, false, guestID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, guestJoin(t, guest, at, `{"name":"Pat Partner","rejoinKey":"`+first.RejoinKey+`"}`, "203.0.113.5"), http.StatusUnauthorized)

	// Nor once the meeting is over.
	if _, err := e.pool.Exec(ctx, `UPDATE calls SET status = 'ended' WHERE id = $1`, call.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, guestJoin(t, guest, at, `{"name":"Lee","passcode":"482913"}`, "203.0.113.9"), http.StatusConflict)
}

func TestInvitingByEmailTurnsTheLinkOnAndSendsBothLinks(t *testing.T) {
	e := newLK(t)
	org := e.person(t, "Olga Organizer", "member")
	inv := e.person(t, "Ivan Invited", "member")
	mailer := &sentMail{configured: true}
	e.svc.SetGuestMailer(mailer, "https://teamup.example/")
	call := e.call(t, org, inv)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `UPDATE calls SET title = 'Pitch', status = 'scheduled',
		scheduled_start = '2026-11-09 04:30:00+00', scheduled_end = '2026-11-09 05:00:00+00' WHERE id = $1`, call.ID); err != nil {
		t.Fatal(err)
	}
	path := "/api/calls/" + call.ID.String() + "/guest-invites"

	wantCode(t, doJSON(t, e.as(t, inv, "member"), http.MethodPost, path, `{"emails":["pat@client.test"]}`), http.StatusForbidden)
	wantCode(t, doJSON(t, e.as(t, org, "member"), http.MethodPost, path, `{"emails":["pat@client.test","not an address"]}`), http.StatusBadRequest)
	if len(mailer.sent) != 0 {
		t.Fatalf("nothing sent for a refused list")
	}

	w := doJSON(t, e.as(t, org, "member"), http.MethodPost, path,
		`{"emails":["Pat Partner <Pat@Client.test>","lee@agency.test","pat@client.test"],"timeZone":"Asia/Kolkata"}`)
	wantCode(t, w, http.StatusOK)
	var link GuestLink
	_ = json.Unmarshal(w.Body.Bytes(), &link)
	if !link.Enabled || len(link.Passcode) != 6 || link.DirectPath == "" || !link.MailConfigured {
		t.Fatalf("the link is turned on, with a passcode: %+v", link)
	}
	if len(link.Invites) != 2 || link.Invites[0].Email != "pat@client.test" || link.Invites[0].EmailedAt == nil {
		t.Errorf("invited once each, and emailed: %+v", link.Invites)
	}
	got := mailer.to("pat@client.test")
	if len(got) != 1 {
		t.Fatalf("one email to Pat: %d", len(got))
	}
	body := got[0].Body
	for _, want := range []string{"Olga Organizer", "\"Pitch\"", "Monday 9 November 2026, 10:00 to 10:30 (Asia/Kolkata)",
		"https://teamup.example" + link.DirectPath, "https://teamup.example" + link.Path, "passcode " + link.Passcode} {
		if !strings.Contains(body, want) {
			t.Errorf("the invitation lacks %q:\n%s", want, body)
		}
	}
	if got[0].Subject != "Invitation: Pitch" {
		t.Errorf("subject: %q", got[0].Subject)
	}

	// Moved, then cancelled: each is told.
	e.svc.GuestsMeetingChanged(ctx, e.org.ID, call.ID, false)
	e.svc.GuestsMeetingChanged(ctx, e.org.ID, call.ID, true)
	got = mailer.to("lee@agency.test")
	if len(got) != 3 || got[1].Subject != "Updated: Pitch" || got[2].Subject != "Cancelled: Pitch" || strings.Contains(got[2].Body, "/join/") {
		t.Errorf("invited, moved, cancelled: %+v", got)
	}

	// Taken off the list, Lee hears nothing more.
	wantCode(t, doJSON(t, e.as(t, org, "member"), http.MethodDelete, path+"?email=lee@agency.test", ""), http.StatusOK)
	e.svc.GuestsMeetingChanged(ctx, e.org.ID, call.ID, true)
	if n := len(mailer.to("lee@agency.test")); n != 3 {
		t.Errorf("no more email after being taken off: %d", n)
	}
}

func TestWithNoMailTheInvitationIsStillKeptToSendByHand(t *testing.T) {
	e := newLK(t)
	org := e.person(t, "Olga Organizer", "member")
	mailer := &sentMail{configured: false}
	e.svc.SetGuestMailer(mailer, "https://teamup.example")
	call := e.call(t, org)
	link, err := e.svc.InviteGuests(context.Background(), e.org.ID, call.ID, org, false, []string{"pat@client.test"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if link.MailConfigured || len(link.Invites) != 1 || link.Invites[0].EmailedAt != nil || len(mailer.sent) != 0 {
		t.Errorf("listed, not emailed: %+v", link)
	}
}

// Asking again re-sends an invitation, but not over and over: the
// organization's mail is not a way to send the same people anything at will.
func TestInvitingTheSameListAgainAndAgainDoesNotFloodThem(t *testing.T) {
	e := newLK(t)
	org := e.person(t, "Olga Organizer", "member")
	mailer := &sentMail{configured: true}
	e.svc.SetGuestMailer(mailer, "https://teamup.example/")
	call := e.call(t, org)
	ctx := context.Background()
	list := []string{"a@client.test", "b@client.test"}
	for i := 0; i < 5; i++ {
		if _, err := e.svc.InviteGuests(ctx, e.org.ID, call.ID, org, false, list, ""); err != nil {
			t.Fatalf("invite %d: %v", i, err)
		}
	}
	if len(mailer.sent) != 2 {
		t.Errorf("asked five times in a row, emailed %d times instead of once each", len(mailer.sent))
	}
	// Once the last one is a while old, asking again does send it again.
	if _, err := e.pool.Exec(ctx, `UPDATE call_guest_invites SET last_emailed_at = NOW() - interval '1 hour' WHERE call_id = $1`, call.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.InviteGuests(ctx, e.org.ID, call.ID, org, false, list[:1], ""); err != nil {
		t.Fatal(err)
	}
	if len(mailer.sent) != 3 {
		t.Errorf("a re-send after a while: %d emails", len(mailer.sent))
	}
}

// Guesses at a passcode are limited per address. Sent all at once they must be
// limited just the same, not each counted against a total none has added to yet.
func TestPasscodeGuessesSentAllAtOnceAreStillLimited(t *testing.T) {
	e := newLK(t)
	org := e.person(t, "Olga Organizer", "member")
	call := e.call(t, org)
	ctx := context.Background()
	link, err := e.svc.SetGuestLink(ctx, e.org.ID, call.ID, org, false, GuestLinkChange{Enabled: true, Passcode: ptr("482913")})
	if err != nil {
		t.Fatal(err)
	}
	const tries = 40
	var wrong atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < tries; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := e.svc.JoinAsGuest(ctx, token(link.Path), "Mallory", fmt.Sprintf("%06d", i), "", "198.51.100.7")
			if errors.Is(err, ErrWrongPasscode) {
				wrong.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if got := wrong.Load(); got > attemptsPerAddress {
		t.Errorf("%d guesses were checked from one address; the limit is %d", got, attemptsPerAddress)
	}
}
