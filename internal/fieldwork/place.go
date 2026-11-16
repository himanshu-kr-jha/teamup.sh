package fieldwork

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/teamup/teamup/internal/safedial"
)

// DefaultGeocoder is OpenStreetMap's own address search, which needs no key.
// Its rules allow one request a second from an application that names itself,
// which a check-in a day per person stays far within.
const DefaultGeocoder = "https://nominatim.openstreetmap.org"

// Geocoder turns where a phone is into the address around it, so a check-in
// says "near Kanke Road, Ranchi" as well as the coordinates (the owner's
// request of 16 November 2026). The server asks, not the phone, so one
// address is kept with the check-in and the same place is asked once.
type Geocoder struct {
	base      string
	userAgent string
	client    *http.Client
	now       func() time.Time
	sleep     func(context.Context, time.Duration) error

	mu    sync.Mutex
	last  time.Time
	cache map[string]string
	order []string
}

// cacheSize is how many places are remembered; a team's sites repeat.
const cacheSize = 2000

// NewGeocoder asks base, or OpenStreetMap when base is empty. "off" asks
// nobody, and check-ins carry coordinates only.
func NewGeocoder(base, version string) *Geocoder {
	switch strings.TrimSpace(base) {
	case "off":
		return nil
	case "":
		base = DefaultGeocoder
	}
	return &Geocoder{
		base:      strings.TrimRight(base, "/"),
		userAgent: "TeamUp/" + version + " (field work check-ins; https://github.com/himanshu-kr-jha/teamup.sh)",
		client:    safedial.NewClient(6 * time.Second),
		now:       time.Now,
		sleep: func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		},
		cache: map[string]string{},
	}
}

// placeKey is a place to about 11 metres, close enough to share an address.
func placeKey(lat, lon float64) string { return fmt.Sprintf("%.4f,%.4f", lat, lon) }

// Address is the address around a place, or an error when it cannot be told.
func (g *Geocoder) Address(ctx context.Context, lat, lon float64) (string, error) {
	if g == nil {
		return "", nil
	}
	key := placeKey(lat, lon)
	g.mu.Lock()
	if a, ok := g.cache[key]; ok {
		g.mu.Unlock()
		return a, nil
	}
	// One at a time, a second apart, as OpenStreetMap asks.
	wait := g.last.Add(time.Second).Sub(g.now())
	if wait > 0 {
		if err := g.sleep(ctx, wait); err != nil {
			g.mu.Unlock()
			return "", err
		}
	}
	g.last = g.now()
	address, err := g.ask(ctx, lat, lon)
	if err == nil && address != "" {
		if len(g.order) >= cacheSize {
			delete(g.cache, g.order[0])
			g.order = g.order[1:]
		}
		g.cache[key] = address
		g.order = append(g.order, key)
	}
	g.mu.Unlock()
	return address, err
}

func (g *Geocoder) ask(ctx context.Context, lat, lon float64) (string, error) {
	q := url.Values{}
	q.Set("format", "jsonv2")
	q.Set("lat", strconv.FormatFloat(lat, 'f', 6, 64))
	q.Set("lon", strconv.FormatFloat(lon, 'f', 6, 64))
	q.Set("zoom", "18")
	q.Set("accept-language", "en")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.base+"/reverse?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", g.userAgent)
	req.Header.Set("Accept", "application/json")
	res, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ask for the address: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ask for the address: %s", res.Status)
	}
	var body struct {
		DisplayName string `json:"display_name"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, res.Body, 64<<10)).Decode(&body); err != nil {
		return "", fmt.Errorf("read the address: %w", err)
	}
	if body.Error != "" {
		// Somewhere with no address, out at sea or in a forest: not a failure.
		return "", nil
	}
	return shortAddress(body.DisplayName), nil
}

// shortAddress keeps an address to what reads on a phone: OpenStreetMap's
// names run from the building out to the country, and the country is dropped
// when there is plenty before it.
func shortAddress(full string) string {
	parts := []string{}
	for _, p := range strings.Split(full, ",") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) > 3 {
		parts = parts[:len(parts)-1]
	}
	out := strings.Join(parts, ", ")
	if r := []rune(out); len(r) > 200 {
		out = string(r[:199]) + "…"
	}
	return out
}
