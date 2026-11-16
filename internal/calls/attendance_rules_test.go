package calls

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"
	"github.com/xuri/excelize/v2"
)

// The camera rule, field work, reviewers' decisions and the export: the owner's
// decisions of 16 November 2026.

type ruledAttendee struct {
	UserID   string `json:"userId"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	CameraOn *bool  `json:"cameraOn"`
	Field    *struct {
		ID       string  `json:"id"`
		Latitude float64 `json:"latitude"`
		Note     string  `json:"note"`
	} `json:"field"`
	Review *struct {
		Decision  string `json:"decision"`
		Note      string `json:"note"`
		Was       string `json:"was"`
		DecidedBy *struct {
			Name string `json:"name"`
		} `json:"decidedBy"`
	} `json:"review"`
}

func (w *scrumWorld) ruledDay(t *testing.T, r *gin.Engine) (bool, map[uuid.UUID]ruledAttendee) {
	t.Helper()
	res := doJSON(t, r, http.MethodGet, "/api/standing-meetings/"+w.meeting.ID+"/attendance?date="+w.date, "")
	if res.Code != http.StatusOK {
		t.Fatalf("attendance: %d %s", res.Code, res.Body.String())
	}
	var body struct {
		Days []struct {
			CameraRequired bool            `json:"cameraRequired"`
			People         []ruledAttendee `json:"people"`
		} `json:"days"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || len(body.Days) != 1 {
		t.Fatalf("decode %s: %v", res.Body.String(), err)
	}
	out := map[uuid.UUID]ruledAttendee{}
	for _, p := range body.Days[0].People {
		out[uuid.MustParse(p.UserID)] = p
	}
	return body.Days[0].CameraRequired, out
}

// cameraOnFor turns somebody's camera on in the day's room, as LiveKit reports it.
func (w *scrumWorld) cameraOnFor(t *testing.T, user uuid.UUID, sid string) {
	t.Helper()
	var room string
	if err := w.pool.QueryRow(context.Background(),
		`SELECT room_name FROM calls WHERE standing_meeting_id = $1 AND occurrence_date = $2::date`, w.meeting.ID, w.date).Scan(&room); err != nil {
		t.Fatal(err)
	}
	w.mustDeliver(t, &livekit.WebhookEvent{
		Event: "track_published", Id: "EV_" + uuid.NewString(), CreatedAt: w.start.Unix(),
		Room:        lkRoom(room),
		Participant: &livekit.ParticipantInfo{Identity: user.String(), Sid: "PA_" + sid},
		Track:       &livekit.TrackInfo{Sid: "TR_" + sid, Source: livekit.TrackSource_CAMERA, Type: livekit.TrackType_VIDEO},
	})
}

func TestADayThatRequiresCamerasPutsWhoeverNeverTurnedTheirsOnInReview(t *testing.T) {
	w := newScrumWorld(t)
	if _, err := w.pool.Exec(context.Background(),
		`UPDATE calls SET camera_required = true WHERE standing_meeting_id = $1 AND occurrence_date = $2::date`, w.meeting.ID, w.date); err != nil {
		t.Fatal(err)
	}
	w.cameraOnFor(t, w.late, "lena")
	// The same track reported twice is one camera.
	w.cameraOnFor(t, w.late, "lena")

	required, people := w.ruledDay(t, w.asOwner(t))
	if !required {
		t.Error("the day says cameras were required")
	}
	if p := people[w.late]; p.Status != "present" || p.CameraOn == nil || !*p.CameraOn {
		t.Errorf("Lena turned hers on and is present, got %+v", p)
	}
	if p := people[w.early]; p.Status != "review" || p.CameraOn == nil || *p.CameraOn {
		t.Errorf("Eve never turned hers on and is in review, got %+v", p)
	}
	if p := people[w.absent]; p.Status != "absent" {
		t.Errorf("Abe never came: still absent, got %+v", p)
	}
}

func TestADayWithoutTheRuleCountsPresentWithoutACamera(t *testing.T) {
	w := newScrumWorld(t)
	_, people := w.ruledDay(t, w.asOwner(t))
	if p := people[w.early]; p.Status != "present" || p.CameraOn != nil {
		t.Errorf("no rule, no camera question: got %+v", p)
	}
}

func TestChangingTheRuleLaterDoesNotRewriteADayAlreadyHeld(t *testing.T) {
	w := newScrumWorld(t)
	on := true
	body, _ := json.Marshal(map[string]any{"cameraRequired": on})
	res := doJSON(t, w.asOwner(t), http.MethodPatch, "/api/standing-meetings/"+w.meeting.ID, string(body))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"cameraRequired":true`) {
		t.Fatalf("turning the rule on: %d %s", res.Code, res.Body.String())
	}
	if _, people := w.ruledDay(t, w.asOwner(t)); people[w.early].Status != "present" {
		t.Errorf("today's call was held without the rule, got %+v", people[w.early])
	}
}

func (w *scrumWorld) fieldWork(t *testing.T, user uuid.UUID, note string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := w.pool.QueryRow(context.Background(), `
		INSERT INTO field_checkins (org_id, user_id, on_date, latitude, longitude, accuracy_m, note, photo_type, photo)
		VALUES ($1, $2, $3::date, 23.36, 85.33, 12, $4, 'image/jpeg', '\xffd8ff'::bytea) RETURNING id`,
		w.org.ID, user, w.date, note).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestFieldWorkTurnsAnAbsenceIntoFieldWorkButNotAnAttendance(t *testing.T) {
	w := newScrumWorld(t)
	id := w.fieldWork(t, w.absent, "Survey at block C")
	w.fieldWork(t, w.late, "Came anyway")

	_, people := w.ruledDay(t, w.asOwner(t))
	abe := people[w.absent]
	if abe.Status != "field_work" || abe.Field == nil || abe.Field.ID != id.String() || abe.Field.Note != "Survey at block C" || abe.Field.Latitude != 23.36 {
		t.Errorf("Abe was in the field, got %+v %+v", abe, abe.Field)
	}
	if lena := people[w.late]; lena.Status != "present" || lena.Field != nil {
		t.Errorf("Lena came, so she is present, got %+v", lena)
	}
}

func decide(t *testing.T, w *scrumWorld, r *gin.Engine, who uuid.UUID, decision, note string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"date": w.date, "userId": who, "decision": decision, "note": note})
	return doJSON(t, r, http.MethodPost, "/api/standing-meetings/"+w.meeting.ID+"/attendance/review", string(body)).Code
}

func TestAReviewerDecidesADayAndTheDecisionSaysWhatItWas(t *testing.T) {
	w := newScrumWorld(t)
	if _, err := w.pool.Exec(context.Background(),
		`UPDATE calls SET camera_required = true WHERE standing_meeting_id = $1 AND occurrence_date = $2::date`, w.meeting.ID, w.date); err != nil {
		t.Fatal(err)
	}
	// A manager, without the permission, may not decide.
	if code := decide(t, w, w.as(t, w.mgr, "manager"), w.early, "present", ""); code != http.StatusForbidden {
		t.Errorf("a manager deciding: %d", code)
	}
	reviewer := w.person(t, "Hema HR", "member")
	asReviewer := withPermissions(t, w, reviewer, "attendance.review")
	if code := decide(t, w, asReviewer, w.early, "present", "Her camera was broken; she spoke throughout"); code != http.StatusOK {
		t.Fatalf("the reviewer deciding: %d", code)
	}
	if code := decide(t, w, asReviewer, w.absent, "maybe", ""); code != http.StatusBadRequest {
		t.Errorf("a decision that is neither: %d", code)
	}

	// The reviewer reads everybody's attendance, though they manage nobody.
	_, people := w.ruledDay(t, asReviewer)
	eve := people[w.early]
	if eve.Status != "present" || eve.Review == nil || eve.Review.Was != "review" || eve.Review.Decision != "present" ||
		eve.Review.DecidedBy == nil || eve.Review.DecidedBy.Name != "Hema HR" || !strings.Contains(eve.Review.Note, "camera was broken") {
		t.Errorf("Eve, decided present from review, got %+v %+v", eve, eve.Review)
	}
	if len(people) < 5 {
		t.Errorf("a reviewer reads everybody, got %d people", len(people))
	}

	// Taking it back puts the day back as it was.
	if code := decide(t, w, asReviewer, w.early, "", ""); code != http.StatusOK {
		t.Fatalf("taking it back: %d", code)
	}
	if _, people := w.ruledDay(t, asReviewer); people[w.early].Status != "review" || people[w.early].Review != nil {
		t.Errorf("back in review, got %+v", people[w.early])
	}
}

// withPermissions is somebody signed in holding exactly these permissions, as a
// role made under Roles would give them.
func withPermissions(t *testing.T, w *scrumWorld, user uuid.UUID, perms ...string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	api.Use(func(c *gin.Context) {
		c.Set("org_id", w.org.ID.String())
		c.Set("user_id", user.String())
		c.Set("user_roles", []string{"member"})
		c.Set("user_permissions", perms)
		c.Next()
	})
	RegisterRoutes(api, w.svc)
	return r
}

func TestAttendanceExportsAsCSVAndExcelWithTotals(t *testing.T) {
	w := newScrumWorld(t)
	w.fieldWork(t, w.absent, "")
	r := w.asOwner(t)
	path := "/api/standing-meetings/" + w.meeting.ID + "/attendance/export?date=" + w.date

	res := doJSON(t, r, http.MethodGet, path+"&format=csv", "")
	if res.Code != http.StatusOK || !strings.HasPrefix(res.Header().Get("Content-Type"), "text/csv") ||
		!strings.Contains(res.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("csv: %d %v", res.Code, res.Header())
	}
	text := strings.TrimPrefix(res.Body.String(), "\ufeff")
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if lines[0] != "Name,"+w.date+",Present,Absent,Leave,Holiday,Field work,In review,Unknown" {
		t.Errorf("the header is names, the date, then totals, got %q", lines[0])
	}
	var abe, lena string
	for _, l := range lines[1:] {
		switch {
		case strings.HasPrefix(l, "Absent Abe,"):
			abe = l
		case strings.HasPrefix(l, "Late Lena,"):
			lena = l
		}
	}
	if abe != "Absent Abe,Field work,0,0,0,0,1,0,0" || lena != "Late Lena,Present,1,0,0,0,0,0,0" {
		t.Errorf("rows: Abe %q, Lena %q", abe, lena)
	}

	res = doJSON(t, r, http.MethodGet, path+"&format=xlsx", "")
	if res.Code != http.StatusOK {
		t.Fatalf("xlsx: %d %s", res.Code, res.Body.String())
	}
	f, err := excelize.OpenReader(bytes.NewReader(res.Body.Bytes()))
	if err != nil {
		t.Fatalf("not a spreadsheet: %v", err)
	}
	rows, err := f.GetRows("Attendance")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 4 || rows[2][0] != "Name" || rows[2][1] != w.date {
		t.Fatalf("the sheet has a title, then the header on row 3, got %v", rows)
	}
	found := false
	for _, row := range rows[3:] {
		if row[0] == "Absent Abe" && row[1] == "Field work" {
			found = true
		}
	}
	if !found {
		t.Errorf("Abe is on the sheet as field work, got %v", rows)
	}
	if code := doJSON(t, r, http.MethodGet, path+"&format=pdf", "").Code; code != http.StatusBadRequest {
		t.Errorf("an unknown format: %d", code)
	}
}

// The window, the owner's decision of 16 November: a 9:00 stand-up counts from
// 8:30 to 9:30. Anywhere inside it is present, and not late; after it, absent,
// with when they came.
func TestJoiningCountsOnlyInsideTheDaysWindow(t *testing.T) {
	w := newScrumWorld(t)
	var room string
	if err := w.pool.QueryRow(context.Background(),
		`SELECT room_name FROM calls WHERE standing_meeting_id = $1 AND occurrence_date = $2::date`, w.meeting.ID, w.date).Scan(&room); err != nil {
		t.Fatal(err)
	}
	at := func(m int) time.Time { return w.start.Add(time.Duration(m) * time.Minute) }
	// Mia only after it closed; Max from before it opened, staying into it.
	w.mustDeliver(t, joinedEv(room, w.mgr, "PA_mia", at(45)))
	w.mustDeliver(t, leftEv(room, w.mgr, "PA_mia", at(60)))
	w.mustDeliver(t, joinedEv(room, w.mgr2, "PA_max", at(-40)))
	w.mustDeliver(t, leftEv(room, w.mgr2, "PA_max", at(10)))

	res := doJSON(t, w.asOwner(t), http.MethodGet, "/api/standing-meetings/"+w.meeting.ID+"/attendance?date="+w.date, "")
	var body struct {
		Days []struct {
			AttendFrom  *time.Time `json:"attendFrom"`
			AttendUntil *time.Time `json:"attendUntil"`
			People      []struct {
				UserID            string     `json:"userId"`
				Status            string     `json:"status"`
				MinutesLate       int        `json:"minutesLate"`
				JoinedAfterWindow *time.Time `json:"joinedAfterWindow"`
			} `json:"people"`
		} `json:"days"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || len(body.Days) != 1 {
		t.Fatalf("decode %s: %v", res.Body.String(), err)
	}
	day := body.Days[0]
	if day.AttendFrom == nil || !near(*day.AttendFrom, at(-30)) || day.AttendUntil == nil || !near(*day.AttendUntil, at(30)) {
		t.Errorf("the window is 8:30 to 9:30, got %v to %v", day.AttendFrom, day.AttendUntil)
	}
	for _, p := range day.People {
		switch p.UserID {
		case w.mgr.String():
			if p.Status != "absent" || p.JoinedAfterWindow == nil || !near(*p.JoinedAfterWindow, at(45)) {
				t.Errorf("Mia came at 9:45, after it closed: absent, with when, got %+v", p)
			}
		case w.mgr2.String():
			if p.Status != "present" || p.MinutesLate != 0 {
				t.Errorf("Max was there from 8:20 into the window: on time, got %+v", p)
			}
		case w.late.String():
			if p.Status != "present" || p.MinutesLate != 0 {
				t.Errorf("Lena at 9:07 is inside the window: present, not late, got %+v", p)
			}
		}
	}
}

func TestAReviewerSetsAnyDayToAnyStatus(t *testing.T) {
	w := newScrumWorld(t)
	asReviewer := withPermissions(t, w, w.person(t, "Hema HR", "member"), "attendance.review")
	late := func(who uuid.UUID, minutes int) int {
		body, _ := json.Marshal(map[string]any{"date": w.date, "userId": who, "decision": "late", "lateMinutes": minutes})
		return doJSON(t, asReviewer, http.MethodPost, "/api/standing-meetings/"+w.meeting.ID+"/attendance/review", string(body)).Code
	}
	if code := late(w.absent, 12); code != http.StatusOK {
		t.Fatalf("late by 12: %d", code)
	}
	if code := late(w.absent, 0); code != http.StatusBadRequest {
		t.Errorf("late by nothing: %d", code)
	}
	if code := decide(t, w, asReviewer, w.early, "on_leave", "Forgot to record it"); code != http.StatusOK {
		t.Fatalf("on leave: %d", code)
	}
	if code := decide(t, w, asReviewer, w.late, "field_work", ""); code != http.StatusOK {
		t.Fatalf("field work: %d", code)
	}
	res := doJSON(t, asReviewer, http.MethodGet, "/api/standing-meetings/"+w.meeting.ID+"/attendance?date="+w.date, "")
	var body struct {
		Days []struct {
			People []struct {
				UserID      string `json:"userId"`
				Status      string `json:"status"`
				MinutesLate int    `json:"minutesLate"`
				Review      *struct {
					Decision string `json:"decision"`
					Was      string `json:"was"`
				} `json:"review"`
			} `json:"people"`
		} `json:"days"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, p := range body.Days[0].People {
		switch p.UserID {
		case w.absent.String():
			if p.Status != "present" || p.MinutesLate != 12 || p.Review == nil || p.Review.Decision != "late" || p.Review.Was != "absent" {
				t.Errorf("Abe, decided late by 12, got %+v", p)
			}
		case w.early.String():
			if p.Status != "on_leave" || p.Review == nil || p.Review.Was != "present" {
				t.Errorf("Eve, decided on leave, got %+v", p)
			}
		case w.late.String():
			if p.Status != "field_work" {
				t.Errorf("Lena, decided field work, got %+v", p)
			}
		}
	}
}
