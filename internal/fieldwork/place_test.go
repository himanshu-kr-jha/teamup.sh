package fieldwork

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/teamup/teamup/internal/safedial"
)

// fakeOSM answers as OpenStreetMap's reverse search does, and counts the asks.
func fakeOSM(t *testing.T, answer func(w http.ResponseWriter, r *http.Request)) (*Geocoder, *atomic.Int32, *[]time.Duration) {
	t.Helper()
	t.Setenv(safedial.EnvAllow, "127.0.0.1")
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if r.URL.Path != "/reverse" || r.URL.Query().Get("format") != "jsonv2" || !strings.HasPrefix(r.Header.Get("User-Agent"), "TeamUp/") {
			t.Errorf("asked %s with %q", r.URL, r.Header.Get("User-Agent"))
		}
		answer(w, r)
	}))
	t.Cleanup(srv.Close)
	g := NewGeocoder(srv.URL, "test")
	// A clock that only moves when the geocoder waits, so the test takes no time.
	now := time.Date(2026, 11, 17, 6, 0, 0, 0, time.UTC)
	waits := []time.Duration{}
	g.now = func() time.Time { return now }
	g.sleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); now = now.Add(d); return nil }
	return g, &asked, &waits
}

func TestTheAddressIsAskedOncePerPlaceAndASecondApart(t *testing.T) {
	g, asked, waits := fakeOSM(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"display_name":"Block C, Kanke Road, Ranchi, Jharkhand, 834008, India"}`))
	})
	ctx := context.Background()
	a, err := g.Address(ctx, 23.34410, 85.30960)
	if err != nil || a != "Block C, Kanke Road, Ranchi, Jharkhand, 834008" {
		t.Fatalf("the address without the country: %q %v", a, err)
	}
	// A few metres away is the same place, and is not asked again.
	if again, _ := g.Address(ctx, 23.34412, 85.30958); again != a || asked.Load() != 1 {
		t.Errorf("remembered: %q, asked %d times", again, asked.Load())
	}
	// Somewhere else is asked, but not within a second of the last.
	if _, err := g.Address(ctx, 23.40, 85.40); err != nil || asked.Load() != 2 {
		t.Fatalf("a new place asked: %v, %d", err, asked.Load())
	}
	if len(*waits) != 1 || (*waits)[0] != time.Second {
		t.Errorf("waited a second before the second ask, got %v", *waits)
	}
}

func TestNoAddressIsNotAFailureAndOffAsksNobody(t *testing.T) {
	g, _, _ := fakeOSM(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":"Unable to geocode"}`))
	})
	if a, err := g.Address(context.Background(), 10.5, 72.1); a != "" || err != nil {
		t.Errorf("out at sea: %q %v", a, err)
	}
	if NewGeocoder("off", "test") != nil {
		t.Error("off is nobody")
	}
	var none *Geocoder
	if a, err := none.Address(context.Background(), 10.5, 72.1); a != "" || err != nil {
		t.Errorf("nobody answers nothing: %q %v", a, err)
	}
}

func TestACheckInKeepsItsAddressAndIsSavedWithoutOneWhenTheLookupFails(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	if err := w.svc.SetSettings(ctx, w.org.ID, Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	down := false
	g, _, _ := fakeOSM(t, func(rw http.ResponseWriter, r *http.Request) {
		if down {
			rw.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = rw.Write([]byte(`{"display_name":"Kanke Road, Ranchi, Jharkhand, India"}`))
	})
	w.svc.WithGeocoder(g)

	// Shown before sending, then kept with the check-in and listed.
	if a, err := w.svc.Place(ctx, w.org.ID, 23.3441, 85.3096); err != nil || a != "Kanke Road, Ranchi, Jharkhand" {
		t.Fatalf("place: %q %v", a, err)
	}
	c, err := w.svc.CheckIn(ctx, w.org.ID, w.ravi, checkin(23.3441, 85.3096, 12))
	if err != nil || c.Address != "Kanke Road, Ranchi, Jharkhand" {
		t.Fatalf("check-in: %+v %v", c, err)
	}
	all, err := w.svc.List(ctx, w.org.ID, Viewer{ID: w.mia, Sees: true}, ListInput{From: "2026-11-17", To: "2026-11-17"})
	if err != nil || len(all) != 1 || all[0].Address != "Kanke Road, Ranchi, Jharkhand" {
		t.Errorf("listed with it: %+v %v", all, err)
	}

	// The lookup down: the photo and the coordinates still count.
	down = true
	c2, err := w.svc.CheckIn(ctx, w.org.ID, w.sam, checkin(12.97, 77.59, 12))
	if err != nil || c2.Address != "" {
		t.Errorf("saved without an address: %+v %v", c2, err)
	}
	if _, err := w.svc.Place(ctx, w.org.ID, 0, 0); err == nil {
		t.Error("no place is not a place")
	}
}
