package calls

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	apperrors "github.com/teamup/teamup/pkg/errors"
)

// A hand the media server lost (a room started again has no attributes) can be
// put back where it was, but only with a recent proof this server gave its owner.
func TestALostHandGoesBackInItsPlace(t *testing.T) {
	h := newHandEnv(t)
	now := time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC)
	h.svc.SetClock(func() time.Time { return now })

	code, up := h.put(t, h.asha, "member", `{"raised":true}`)
	if code != http.StatusOK || up.Proof == "" {
		t.Fatalf("a raised hand should come with a proof: %d %+v", code, up)
	}

	// Asking again while it is up gives the same time and a fresh proof.
	now = now.Add(time.Minute)
	_, again := h.put(t, h.asha, "member", `{"raised":true}`)
	if again.RaisedAt != up.RaisedAt || again.Proof == "" || again.Proof == up.Proof {
		t.Fatalf("a refreshed proof for the same time: %+v then %+v", up, again)
	}

	restore := func(raisedAt, proof string) HandState {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"raised": true, "restore": map[string]string{"raisedAt": raisedAt, "proof": proof}})
		code, state := h.put(t, h.asha, "member", string(body))
		if code != http.StatusOK {
			t.Fatalf("restore: %d", code)
		}
		return state
	}
	lose := func() {
		h.media.mu.Lock()
		delete(h.media.attrs, h.call.RoomName+"/"+h.asha.String())
		h.media.mu.Unlock()
	}

	// Lost, and put back at its first time.
	lose()
	now = now.Add(30 * time.Second)
	if got := restore(again.RaisedAt, again.Proof); !got.Restored || got.RaisedAt != up.RaisedAt || h.hand(h.asha) != up.RaisedAt {
		t.Fatalf("a valid proof should put the hand back at %s: %+v, attribute %q", up.RaisedAt, got, h.hand(h.asha))
	}

	// A proof that was tampered with, made up, or for an earlier time than it
	// says, puts the hand up as new.
	for _, bad := range []struct{ name, at, proof string }{
		{"altered time", "2026-11-18T09:00:00.000Z", again.Proof},
		{"made up", up.RaisedAt, "1794000000.abc"},
		{"no proof", up.RaisedAt, ""},
	} {
		lose()
		got := restore(bad.at, bad.proof)
		if got.Restored || got.RaisedAt == bad.at || got.RaisedAt == up.RaisedAt {
			t.Fatalf("%s: should go up as a new hand, got %+v", bad.name, got)
		}
	}

	// Somebody else's proof is no good for Asha.
	h.enter(h.ben)
	_, bens := h.put(t, h.ben, "member", `{"raised":true}`)
	lose()
	if got := restore(bens.RaisedAt, bens.Proof); got.Restored {
		t.Fatalf("Ben's proof restored Asha's hand: %+v", got)
	}

	// A proof kept too long is no good either.
	_, fresh := h.put(t, h.asha, "member", `{"raised":true}`)
	lose()
	now = now.Add(handProofTTL + time.Second)
	if got := restore(fresh.RaisedAt, fresh.Proof); got.Restored {
		t.Fatalf("an expired proof restored the hand: %+v", got)
	}
}

// The browser refreshes its proof every minute; a refresh that arrives after the
// hand came down must not put it back up.
func TestARefreshNeverRaisesAHand(t *testing.T) {
	h := newHandEnv(t)
	code, state := h.put(t, h.asha, "member", `{"raised":true,"refresh":true}`)
	if code != http.StatusOK || state.Raised || h.hand(h.asha) != "" {
		t.Fatalf("a refresh with the hand down: %d %+v, attribute %q", code, state, h.hand(h.asha))
	}
	_, up := h.put(t, h.asha, "member", `{"raised":true}`)
	code, state = h.put(t, h.asha, "member", `{"raised":true,"refresh":true}`)
	if code != http.StatusOK || !state.Raised || state.RaisedAt != up.RaisedAt || state.Proof == "" {
		t.Fatalf("a refresh with the hand up: %d %+v", code, state)
	}
	if code, _ = h.put(t, h.organizer, "member", `{"raised":true,"refresh":true,"userId":"`+h.asha.String()+`"}`); code != http.StatusForbidden {
		t.Fatalf("a refresh of somebody else's hand should be refused, got %d", code)
	}
}

func TestHandOnACallThatCannotBeReadIsAServerError(t *testing.T) {
	h := newHandEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := h.svc.SetHand(ctx, h.org.ID, h.call.ID, h.asha, false, HandChange{Raised: true})
	if err == nil {
		t.Fatal("reading the call with a cancelled context should fail")
	}
	if got := apperrors.HTTPStatus(err); got != http.StatusInternalServerError {
		t.Fatalf("a failure to read the call is not a missing call: got %d (%v)", got, err)
	}
}
