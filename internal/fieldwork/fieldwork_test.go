package fieldwork

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/tasks"
	"github.com/teamup/teamup/internal/testsupport"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

// A real JPEG's first bytes are enough for the type to be recognised.
var jpeg = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(append([]byte{0xff, 0xd8, 0xff, 0xe0, 0, 0x10, 'J', 'F', 'I', 'F', 0}, make([]byte, 200)...))

type world struct {
	svc                  *Service
	org                  *testsupport.Org
	ravi, mia, sam, hema uuid.UUID
}

func newWorld(t *testing.T) *world {
	t.Helper()
	pool := testsupport.DB(t)
	org := testsupport.SeedOrg(t, pool)
	w := &world{svc: NewService(pool, tasks.NewService(pool)), org: org}
	w.ravi = testsupport.SeedUser(t, pool, org, "ravi@example.test", "member")
	w.mia = testsupport.SeedUser(t, pool, org, "mia@example.test", "manager")
	w.sam = testsupport.SeedUser(t, pool, org, "sam@example.test", "member")
	w.hema = testsupport.SeedUser(t, pool, org, "hema@example.test", "member")
	// Mia manages the Field team, with Ravi on it; Sam is a colleague.
	testsupport.SeedTeam(t, pool, org, "Field", &w.mia, nil, w.ravi)
	w.svc.clock = func() time.Time { return time.Date(2026, 11, 17, 6, 0, 0, 0, time.UTC) }
	return w
}

func checkin(lat, lng, acc float64) Input {
	return Input{Photo: jpeg, Latitude: lat, Longitude: lng, AccuracyM: acc, Note: "Survey at block C", Today: "2026-11-17"}
}

func TestFieldWorkIsOffUntilTheOrganizationTurnsItOn(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	if _, err := w.svc.CheckIn(ctx, w.org.ID, w.ravi, checkin(23.36, 85.33, 15)); apperrors.HTTPStatus(err) != 403 {
		t.Fatalf("off by default, got %v", err)
	}
	if err := w.svc.SetSettings(ctx, w.org.ID, Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	c, err := w.svc.CheckIn(ctx, w.org.ID, w.ravi, checkin(23.36, 85.33, 15))
	if err != nil {
		t.Fatalf("on: %v", err)
	}
	if c.OnDate != "2026-11-17" || c.Note != "Survey at block C" || c.Latitude != 23.36 {
		t.Errorf("saved as sent, got %+v", c)
	}
	mine, err := w.svc.Mine(ctx, w.org.ID, w.ravi, "2026-11-17")
	if err != nil || len(mine) != 1 || mine[0].ID != c.ID {
		t.Errorf("his own day lists it, got %+v %v", mine, err)
	}
}

func TestACheckInNeedsARealLocationAndAPhoto(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	if err := w.svc.SetSettings(ctx, w.org.ID, Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]Input{
		"no location":        checkin(0, 0, 10),
		"off the map":        checkin(123, 85, 10),
		"a guess of a city":  checkin(23.36, 85.33, 9000),
		"not a photo":        {Photo: "data:text/plain;base64,aGVsbG8=", Latitude: 23.36, Longitude: 85.33, AccuracyM: 10},
		"not a data address": {Photo: "https://example.test/x.jpg", Latitude: 23.36, Longitude: 85.33, AccuracyM: 10},
	} {
		if _, err := w.svc.CheckIn(ctx, w.org.ID, w.ravi, in); apperrors.HTTPStatus(err) != 400 {
			t.Errorf("%s: want 400, got %v", name, err)
		}
	}
	// A date more than a day from the server's is not believed.
	in := checkin(23.36, 85.33, 10)
	in.Today = "2026-11-25"
	c, err := w.svc.CheckIn(ctx, w.org.ID, w.ravi, in)
	if err != nil || c.OnDate != "2026-11-17" {
		t.Errorf("a far-off date is the server's, got %+v %v", c, err)
	}
}

func TestThePhotoIsForHimHisManagerAndReviewersOnly(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	if err := w.svc.SetSettings(ctx, w.org.ID, Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	c, err := w.svc.CheckIn(ctx, w.org.ID, w.ravi, checkin(23.36, 85.33, 15))
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]Viewer{
		"himself":     {ID: w.ravi},
		"his manager": {ID: w.mia},
		"a reviewer":  {ID: w.hema, Reviewer: true},
		"an admin":    {ID: w.sam, Admin: true},
	} {
		if photo, kind, err := w.svc.Photo(ctx, w.org.ID, c.ID, v); err != nil || kind != "image/jpeg" || len(photo) == 0 {
			t.Errorf("%s should see it, got %v", name, err)
		}
	}
	if _, _, err := w.svc.Photo(ctx, w.org.ID, c.ID, Viewer{ID: w.sam}); !errors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("a colleague learns nothing, got %v", err)
	}
	other := testsupport.SeedOrg(t, testsupport.DB(t))
	if _, _, err := w.svc.Photo(ctx, other.ID, c.ID, Viewer{ID: other.OwnerID, Reviewer: true}); !errors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("another organization learns nothing, got %v", err)
	}
}

func TestACheckInMadeByMistakeIsTakenBackOnItsDayOnly(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	if err := w.svc.SetSettings(ctx, w.org.ID, Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	c, err := w.svc.CheckIn(ctx, w.org.ID, w.ravi, checkin(23.36, 85.33, 15))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Remove(ctx, w.org.ID, w.sam, c.ID, "2026-11-17"); !errors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("somebody else cannot take it back, got %v", err)
	}
	w.svc.clock = func() time.Time { return time.Date(2026, 11, 19, 6, 0, 0, 0, time.UTC) }
	if err := w.svc.Remove(ctx, w.org.ID, w.ravi, c.ID, "2026-11-19"); !errors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("not two days later, got %v", err)
	}
	w.svc.clock = func() time.Time { return time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC) }
	if err := w.svc.Remove(ctx, w.org.ID, w.ravi, c.ID, "2026-11-17"); err != nil {
		t.Errorf("on its day: %v", err)
	}
}
