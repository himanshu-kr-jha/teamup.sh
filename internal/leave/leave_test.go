package leave

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teamup/teamup/internal/notifications"
	"github.com/teamup/teamup/internal/tasks"
	"github.com/teamup/teamup/internal/testsupport"
)

// ── fixture ───────────────────────────────────────────────────────────────

type env struct {
	pool *pgxpool.Pool
	org  *testsupport.Org
	svc  *Service
	// A manager, their two reports, and a colleague on another team.
	mgr, ann, bob, cat uuid.UUID
}

// now is fixed, so dates in the tests mean the same thing whenever they run.
var now = time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testsupport.DB(t)
	e := &env{pool: pool, org: testsupport.SeedOrg(t, pool)}
	e.svc = NewService(pool, tasks.NewService(pool))
	e.svc.SetNotifier(notifications.NewService(pool, nil, notifications.Config{}))
	e.svc.SetClock(func() time.Time { return now })
	e.mgr = e.person(t, "Mia Manager", "manager")
	e.ann = e.person(t, "Ann Archer", "member")
	e.bob = e.person(t, "Bob Baker", "member")
	e.cat = e.person(t, "Cat Cole", "member")
	testsupport.SeedTeam(t, pool, e.org, "Backend", &e.mgr, nil, e.ann, e.bob)
	testsupport.SeedTeam(t, pool, e.org, "Sales", nil, nil, e.cat)
	return e
}

func (e *env) person(t *testing.T, name, role string) uuid.UUID {
	t.Helper()
	id := testsupport.SeedUser(t, e.pool, e.org, strings.ToLower(strings.ReplaceAll(name, " ", "."))+"@example.test", role)
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET name = $1 WHERE id = $2`, name, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) as(user uuid.UUID, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	api.Use(func(c *gin.Context) {
		c.Set("org_id", e.org.ID.String())
		c.Set("user_id", user.String())
		c.Set("user_roles", []string{role})
		c.Next()
	})
	RegisterRoutes(api, e.svc)
	return r
}

func do(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func want(t *testing.T, w *httptest.ResponseRecorder, code int) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("want %d, got %d: %s", code, w.Code, w.Body.String())
	}
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return v
}

func str(s string) *string { return &s }

func (e *env) add(t *testing.T, as uuid.UUID, role string, body map[string]any) Leave {
	t.Helper()
	w := do(t, e.as(as, role), http.MethodPost, "/api/leave", body)
	want(t, w, http.StatusCreated)
	return decode[Leave](t, w)
}

func (e *env) list(t *testing.T, as uuid.UUID, role, query string) []Leave {
	t.Helper()
	w := do(t, e.as(as, role), http.MethodGet, "/api/leave?from=2026-01-01&to=2027-01-31"+query, nil)
	want(t, w, http.StatusOK)
	return decode[struct {
		Leave []Leave `json:"leave"`
	}](t, w).Leave
}

// ── who may write and see leave ───────────────────────────────────────────

func TestSomebodyRecordsTheirOwnLeaveAndReadsItBack(t *testing.T) {
	e := newEnv(t)
	l := e.add(t, e.ann, "member", map[string]any{"startsOn": "2026-11-18", "endsOn": "2026-11-20", "note": "Family wedding"})
	if l.UserID != e.ann || l.StartsOn != "2026-11-18" || l.EndsOn == nil || *l.EndsOn != "2026-11-20" || l.Part != "full" {
		t.Fatalf("got %+v", l)
	}
	if l.AddedBy == nil || l.AddedBy.ID != e.ann || l.AddedBy.Name != "Ann Archer" {
		t.Errorf("it says who added it, got %+v", l.AddedBy)
	}
	got := e.list(t, e.ann, "member", "")
	if len(got) != 1 || got[0].ID != l.ID || got[0].Note != "Family wedding" {
		t.Errorf("she reads her own leave back, got %+v", got)
	}
	var told int
	_ = e.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM notifications WHERE type = 'leave.changed'`).Scan(&told)
	if told != 0 {
		t.Errorf("nobody needs telling about their own leave, got %d", told)
	}
}

func TestAManagerRecordsLeaveForTheirReportAndTheReportIsTold(t *testing.T) {
	e := newEnv(t)
	l := e.add(t, e.mgr, "manager", map[string]any{"userId": e.ann.String(), "startsOn": "2026-11-10", "endsOn": "2026-11-10"})
	if l.UserID != e.ann || l.AddedBy == nil || l.AddedBy.ID != e.mgr {
		t.Fatalf("the manager added it for Ann, got %+v", l)
	}
	var title string
	err := e.pool.QueryRow(context.Background(),
		`SELECT title FROM notifications WHERE type = 'leave.changed' AND user_id = $1`, e.ann).Scan(&title)
	if err != nil || title != "Mia Manager added leave for you" {
		t.Errorf("Ann is told who added it, got %q (%v)", title, err)
	}

	people := decode[struct {
		People []Person `json:"people"`
	}](t, do(t, e.as(e.mgr, "manager"), http.MethodGet, "/api/leave/people", nil)).People
	if len(people) != 3 || people[0].ID != e.mgr {
		t.Errorf("the manager sees themself first, then their two reports, got %+v", people)
	}
	if got := e.list(t, e.mgr, "manager", ""); len(got) != 1 {
		t.Errorf("the manager sees their report's leave, got %+v", got)
	}
}

func TestAnAdministratorRecordsLeaveForAnybody(t *testing.T) {
	e := newEnv(t)
	admin := e.person(t, "Ada Admin", "admin")
	e.add(t, admin, "admin", map[string]any{"userId": e.cat.String(), "startsOn": "2026-11-18"})
	if got := e.list(t, admin, "admin", "&userId="+e.cat.String()); len(got) != 1 || got[0].EndsOn != nil {
		t.Errorf("an open-ended stretch for Cat, got %+v", got)
	}
}

func TestAColleagueNeitherSeesNorWritesSomebodyElsesLeave(t *testing.T) {
	e := newEnv(t)
	l := e.add(t, e.ann, "member", map[string]any{"startsOn": "2026-11-18"})
	bob := e.as(e.bob, "member")

	want(t, do(t, bob, http.MethodPost, "/api/leave", map[string]any{"userId": e.ann.String(), "startsOn": "2026-11-25"}), http.StatusNotFound)
	want(t, do(t, bob, http.MethodPatch, "/api/leave/"+l.ID.String(), map[string]any{"startsOn": "2026-11-19"}), http.StatusNotFound)
	want(t, do(t, bob, http.MethodDelete, "/api/leave/"+l.ID.String(), nil), http.StatusNotFound)
	want(t, do(t, bob, http.MethodGet, "/api/leave/"+l.ID.String()+"/history", nil), http.StatusNotFound)
	want(t, do(t, bob, http.MethodGet, "/api/leave?from=2026-01-01&to=2026-12-31&userId="+e.ann.String(), nil), http.StatusNotFound)
	if got := e.list(t, e.bob, "member", ""); len(got) != 0 {
		t.Errorf("Bob sees nobody's leave but his own, got %+v", got)
	}
	// A manager of another team is a colleague too.
	want(t, do(t, e.as(e.mgr, "manager"), http.MethodPost, "/api/leave", map[string]any{"userId": e.cat.String(), "startsOn": "2026-11-25"}), http.StatusNotFound)

	other := testsupport.SeedOrg(t, e.pool)
	want(t, do(t, e.as(e.ann, "member"), http.MethodPost, "/api/leave", map[string]any{"userId": other.OwnerID.String(), "startsOn": "2026-11-25"}), http.StatusNotFound)
}

func TestLeaveIsValidated(t *testing.T) {
	e := newEnv(t)
	r := e.as(e.ann, "member")
	for name, body := range map[string]map[string]any{
		"no first day":              {"endsOn": "2026-11-20"},
		"not a date":                {"startsOn": "18/11/2026"},
		"last before first":         {"startsOn": "2026-11-20", "endsOn": "2026-11-18"},
		"half a day over two dates": {"startsOn": "2026-11-18", "endsOn": "2026-11-19", "part": "morning"},
		"half a day with no end":    {"startsOn": "2026-11-18", "part": "afternoon"},
		"an unknown part":           {"startsOn": "2026-11-18", "endsOn": "2026-11-18", "part": "evening"},
		"over a year":               {"startsOn": "2026-11-18", "endsOn": "2027-11-19"},
		"years ago":                 {"startsOn": "2020-01-01", "endsOn": "2020-01-02"},
		"a note that is an essay":   {"startsOn": "2026-11-18", "note": strings.Repeat("x", 201)},
	} {
		if w := do(t, r, http.MethodPost, "/api/leave", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d: %s", name, w.Code, w.Body.String())
		}
	}
	if got := e.list(t, e.ann, "member", ""); len(got) != 0 {
		t.Errorf("nothing was recorded, got %+v", got)
	}
}

func TestOverlappingLeaveIsRefusedButTwoHalvesOfOneDayAreNot(t *testing.T) {
	e := newEnv(t)
	r := e.as(e.ann, "member")
	e.add(t, e.ann, "member", map[string]any{"startsOn": "2026-11-18", "endsOn": "2026-11-20"})
	want(t, do(t, r, http.MethodPost, "/api/leave", map[string]any{"startsOn": "2026-11-20", "endsOn": "2026-11-22"}), http.StatusConflict)
	want(t, do(t, r, http.MethodPost, "/api/leave", map[string]any{"startsOn": "2026-11-10"}), http.StatusConflict)

	e.add(t, e.ann, "member", map[string]any{"startsOn": "2026-11-25", "endsOn": "2026-11-25", "part": "morning"})
	e.add(t, e.ann, "member", map[string]any{"startsOn": "2026-11-25", "endsOn": "2026-11-25", "part": "afternoon"})
	want(t, do(t, r, http.MethodPost, "/api/leave", map[string]any{"startsOn": "2026-11-25", "endsOn": "2026-11-25", "part": "morning"}), http.StatusConflict)
	want(t, do(t, r, http.MethodPost, "/api/leave", map[string]any{"startsOn": "2026-11-25", "endsOn": "2026-11-25"}), http.StatusConflict)

	// Somebody else's leave never clashes with Ann's.
	e.add(t, e.bob, "member", map[string]any{"startsOn": "2026-11-18", "endsOn": "2026-11-20"})
}

func TestEveryChangeIsKeptWithWhoMadeIt(t *testing.T) {
	e := newEnv(t)
	l := e.add(t, e.ann, "member", map[string]any{"startsOn": "2026-11-18", "endsOn": "2026-11-18"})
	mgr := e.as(e.mgr, "manager")
	w := do(t, mgr, http.MethodPatch, "/api/leave/"+l.ID.String(), map[string]any{"startsOn": "2026-11-18", "endsOn": "2026-11-19", "note": "Two days"})
	want(t, w, http.StatusOK)
	changed := decode[Leave](t, w)
	if changed.ChangedBy == nil || changed.ChangedBy.ID != e.mgr || changed.AddedBy.ID != e.ann || changed.UserID != e.ann {
		t.Errorf("still Ann's, added by Ann, changed by the manager, got %+v", changed)
	}
	want(t, do(t, mgr, http.MethodDelete, "/api/leave/"+l.ID.String(), nil), http.StatusNoContent)
	if got := e.list(t, e.ann, "member", ""); len(got) != 0 {
		t.Errorf("removed leave is no longer listed, got %+v", got)
	}

	w = do(t, e.as(e.ann, "member"), http.MethodGet, "/api/leave/"+l.ID.String()+"/history", nil)
	want(t, w, http.StatusOK)
	changes := decode[struct {
		Changes []Change `json:"changes"`
	}](t, w).Changes
	if len(changes) != 3 {
		t.Fatalf("added, changed, removed, got %+v", changes)
	}
	for i, c := range []struct {
		action string
		actor  uuid.UUID
		end    string
	}{{"added", e.ann, "2026-11-18"}, {"changed", e.mgr, "2026-11-19"}, {"removed", e.mgr, "2026-11-19"}} {
		got := changes[i]
		if got.Action != c.action || got.Actor == nil || got.Actor.ID != c.actor || got.EndsOn == nil || *got.EndsOn != c.end {
			t.Errorf("change %d: want %s by %s to %s, got %+v", i, c.action, c.actor, c.end, got)
		}
	}
}

// ── holidays ──────────────────────────────────────────────────────────────

func TestOnlyAnAdministratorKeepsHolidaysAndEverybodyReadsThem(t *testing.T) {
	e := newEnv(t)
	body := map[string]any{"date": "2026-11-08", "name": "Diwali"}
	want(t, do(t, e.as(e.mgr, "manager"), http.MethodPost, "/api/holidays", body), http.StatusForbidden)
	w := do(t, e.as(e.org.OwnerID, "owner"), http.MethodPost, "/api/holidays", body)
	want(t, w, http.StatusCreated)
	h := decode[Holiday](t, w)
	want(t, do(t, e.as(e.org.OwnerID, "owner"), http.MethodPost, "/api/holidays", map[string]any{"date": "2026-11-08", "name": "Again"}), http.StatusConflict)
	want(t, do(t, e.as(e.org.OwnerID, "owner"), http.MethodPost, "/api/holidays", map[string]any{"date": "2026-11-09", "name": " "}), http.StatusBadRequest)

	w = do(t, e.as(e.ann, "member"), http.MethodGet, "/api/holidays?from=2026-01-01&to=2026-12-31", nil)
	want(t, w, http.StatusOK)
	list := decode[struct {
		Holidays []Holiday `json:"holidays"`
	}](t, w).Holidays
	if len(list) != 1 || list[0].Name != "Diwali" || list[0].Date != "2026-11-08" {
		t.Errorf("everybody reads the holidays, got %+v", list)
	}

	want(t, do(t, e.as(e.ann, "member"), http.MethodDelete, "/api/holidays/"+h.ID.String(), nil), http.StatusForbidden)
	want(t, do(t, e.as(e.org.OwnerID, "owner"), http.MethodDelete, "/api/holidays/"+h.ID.String(), nil), http.StatusNoContent)
	other := testsupport.SeedOrg(t, e.pool)
	otherHoliday, err := e.svc.AddHoliday(context.Background(), other.ID, other.OwnerID, "2026-12-25", "Christmas")
	if err != nil {
		t.Fatal(err)
	}
	want(t, do(t, e.as(e.org.OwnerID, "owner"), http.MethodDelete, "/api/holidays/"+otherHoliday.ID.String(), nil), http.StatusNotFound)
}

// ── the availability picker ───────────────────────────────────────────────

func TestChoosingOnLeaveRecordsItAndComingBackEndsIt(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	today := "2026-11-16"

	if err := e.svc.StartToday(ctx, e.org.ID, e.ann, today, str("2026-11-19")); err != nil {
		t.Fatal(err)
	}
	got := e.list(t, e.ann, "member", "")
	if len(got) != 1 || got[0].StartsOn != today || got[0].EndsOn == nil || *got[0].EndsOn != "2026-11-18" {
		t.Fatalf("on leave today until the day before she is back, got %+v", got)
	}
	away, err := e.svc.OnLeaveToday(ctx, e.org.ID, today)
	if err != nil || away[e.ann] == nil || *away[e.ann] != "2026-11-19" {
		t.Errorf("she shows as on leave, back on the 19th, got %v (%v)", away, err)
	}

	// Choosing it again with a later return moves the end, and adds nothing.
	if err := e.svc.StartToday(ctx, e.org.ID, e.ann, today, str("2026-11-21")); err != nil {
		t.Fatal(err)
	}
	if got = e.list(t, e.ann, "member", ""); len(got) != 1 || *got[0].EndsOn != "2026-11-20" {
		t.Fatalf("one stretch, now to the 20th, got %+v", got)
	}

	// Back the next day: the stretch ends yesterday, so the day she was away stays.
	if err := e.svc.EndToday(ctx, e.org.ID, e.ann, "2026-11-17"); err != nil {
		t.Fatal(err)
	}
	if got = e.list(t, e.ann, "member", ""); len(got) != 1 || *got[0].EndsOn != "2026-11-16" {
		t.Fatalf("leave on the 16th alone, got %+v", got)
	}

	// Back the day it started: there was no leave after all.
	if err := e.svc.StartToday(ctx, e.org.ID, e.bob, today, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.EndToday(ctx, e.org.ID, e.bob, today); err != nil {
		t.Fatal(err)
	}
	if got = e.list(t, e.bob, "member", ""); len(got) != 0 {
		t.Errorf("nothing left, got %+v", got)
	}
	if err := e.svc.StartToday(ctx, e.org.ID, e.bob, today, str(today)); err == nil {
		t.Error("back today is not leave")
	}
}

func TestTodayIsTheCallersDateOnlyWhenItIsBelievable(t *testing.T) {
	at := time.Date(2026, 11, 16, 20, 0, 0, 0, time.UTC)
	for raw, want := range map[string]string{
		"2026-11-17": "2026-11-17", // already tomorrow in Kolkata
		"2026-11-15": "2026-11-15",
		"2026-11-19": "2026-11-16",
		"rubbish":    "2026-11-16",
		"":           "2026-11-16",
	} {
		if got := Today(raw, at); got != want {
			t.Errorf("%q: want %s, got %s", raw, want, got)
		}
	}
}

// ── what attendance reads ─────────────────────────────────────────────────

func TestADateCountsAsLeaveFromTheChangeThatTookItIn(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	l, err := e.svc.Add(ctx, e.org.ID, Actor{ID: e.ann}, Input{UserID: e.ann, StartsOn: "2026-11-10", EndsOn: str("2026-11-10")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE leave_changes SET created_at = '2026-11-01T09:00:00Z' WHERE leave_id = $1`, l.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Change(ctx, e.org.ID, Actor{ID: e.mgr}, l.ID, Input{StartsOn: "2026-11-10", EndsOn: str("2026-11-11")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddHoliday(ctx, e.org.ID, e.org.OwnerID, "2026-11-12", "Founders' day"); err != nil {
		t.Fatal(err)
	}

	book, err := Load(ctx, e.pool, e.org.ID, []uuid.UUID{e.ann, e.bob}, "2026-11-09", "2026-11-12")
	if err != nil {
		t.Fatal(err)
	}
	slot := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	first, ok := book.On(e.ann, "2026-11-10", slot)
	if !ok || !first.RecordedAt.Equal(time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)) || first.RecordedBy.ID != e.ann {
		t.Errorf("the 10th was planned on the 1st by Ann, got %+v (%v)", first, ok)
	}
	second, ok := book.On(e.ann, "2026-11-11", slot.AddDate(0, 0, 1))
	if !ok || second.RecordedBy.ID != e.mgr || second.RecordedAt.Before(time.Now().Add(-time.Minute)) {
		t.Errorf("the 11th came in with the manager's change just now, got %+v (%v)", second, ok)
	}
	if _, ok := book.On(e.ann, "2026-11-09", slot); ok {
		t.Error("the 9th is not leave")
	}
	if _, ok := book.On(e.bob, "2026-11-10", slot); ok {
		t.Error("Bob has no leave")
	}
	if name, ok := book.Holiday("2026-11-12"); !ok || name != "Founders' day" {
		t.Errorf("the 12th is a holiday, got %q", name)
	}
}

func TestHalfADayCoversTheMeetingInThatHalf(t *testing.T) {
	morning := Day{Part: PartMorning}
	afternoon := Day{Part: PartAfternoon}
	nine := time.Date(2026, 11, 16, 9, 30, 0, 0, time.UTC)
	two := time.Date(2026, 11, 16, 14, 0, 0, 0, time.UTC)
	if !morning.Covers(nine) || morning.Covers(two) || afternoon.Covers(nine) || !afternoon.Covers(two) {
		t.Error("a morning covers 09:30 and not 14:00, an afternoon the other way round")
	}
	if !(Day{Part: PartFull}).Covers(two) {
		t.Error("a full day covers everything")
	}
}
