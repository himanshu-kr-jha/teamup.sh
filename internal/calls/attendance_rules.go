package calls

// Attendance rules, the owner's decisions of 16 November 2026:
//
//   - A stand-up may require cameras. Whoever joined that day and never turned
//     their camera on is "review", not present, until a reviewer decides.
//   - Somebody working in the field sends a photo taken in the app, with where
//     their phone was, and is "field_work" that day rather than absent.
//   - A reviewer (attendance.review) decides a day for somebody, present or
//     absent, with a note. The decision is kept with who made it, and what the
//     day said before.

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/leave"
)

// AttendeeField is the field work that counts for somebody on a day.
type AttendeeField struct {
	ID        uuid.UUID `json:"id"`
	TakenAt   time.Time `json:"takenAt"`
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	AccuracyM float64   `json:"accuracyM"`
	Note      string    `json:"note,omitempty"`
}

// AttendeeReview is a reviewer's decision about somebody's day.
type AttendeeReview struct {
	Decision  string        `json:"decision"`
	Note      string        `json:"note,omitempty"`
	DecidedBy *leave.Person `json:"decidedBy"`
	DecidedAt time.Time     `json:"decidedAt"`
	// Was is what the day said before the decision: review, absent, field_work...
	Was string `json:"was"`
}

// attendanceExtras is what the rules read for a range of days.
type attendanceExtras struct {
	cameraOn map[string]map[uuid.UUID]bool
	field    map[string]map[uuid.UUID]AttendeeField
	reviews  map[string]map[uuid.UUID]AttendeeReview
}

func (s *Service) loadAttendanceExtras(ctx context.Context, orgID, meetingID uuid.UUID, people []uuid.UUID, from, to time.Time) (*attendanceExtras, error) {
	first, last := from.Format("2006-01-02"), to.Format("2006-01-02")
	x := &attendanceExtras{
		cameraOn: map[string]map[uuid.UUID]bool{},
		field:    map[string]map[uuid.UUID]AttendeeField{},
		reviews:  map[string]map[uuid.UUID]AttendeeReview{},
	}

	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT c.occurrence_date::text, e.user_id
		FROM calls c JOIN call_events e ON e.call_id = c.id AND e.type = 'camera_on'
		WHERE c.standing_meeting_id = $1 AND c.occurrence_date BETWEEN $2::date AND $3::date AND e.user_id IS NOT NULL
	`, meetingID, first, last)
	if err != nil {
		return nil, fmt.Errorf("list cameras turned on: %w", err)
	}
	for rows.Next() {
		var date string
		var user uuid.UUID
		if err := rows.Scan(&date, &user); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan camera: %w", err)
		}
		if x.cameraOn[date] == nil {
			x.cameraOn[date] = map[uuid.UUID]bool{}
		}
		x.cameraOn[date][user] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// The latest check-in of each person's day is the one shown.
	rows, err = s.db.Query(ctx, `
		SELECT DISTINCT ON (user_id, on_date) on_date::text, user_id, id, taken_at, latitude, longitude, accuracy_m, note
		FROM field_checkins
		WHERE org_id = $1 AND user_id = ANY($2) AND on_date BETWEEN $3::date AND $4::date
		ORDER BY user_id, on_date, taken_at DESC
	`, orgID, people, first, last)
	if err != nil {
		return nil, fmt.Errorf("list field work: %w", err)
	}
	for rows.Next() {
		var date string
		var user uuid.UUID
		var f AttendeeField
		var accuracy float32
		if err := rows.Scan(&date, &user, &f.ID, &f.TakenAt, &f.Latitude, &f.Longitude, &accuracy, &f.Note); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan field work: %w", err)
		}
		f.AccuracyM = float64(accuracy)
		if x.field[date] == nil {
			x.field[date] = map[uuid.UUID]AttendeeField{}
		}
		x.field[date][user] = f
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.Query(ctx, `
		SELECT r.on_date::text, r.user_id, r.decision, r.note, r.decided_at, r.decided_by, COALESCE(u.name, '')
		FROM attendance_reviews r LEFT JOIN users u ON u.id = r.decided_by
		WHERE r.standing_meeting_id = $1 AND r.on_date BETWEEN $2::date AND $3::date
	`, meetingID, first, last)
	if err != nil {
		return nil, fmt.Errorf("list attendance reviews: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var date, name string
		var user uuid.UUID
		var by *uuid.UUID
		var r AttendeeReview
		if err := rows.Scan(&date, &user, &r.Decision, &r.Note, &r.DecidedAt, &by, &name); err != nil {
			return nil, fmt.Errorf("scan attendance review: %w", err)
		}
		if by != nil {
			r.DecidedBy = &leave.Person{ID: *by, Name: name}
		}
		if x.reviews[date] == nil {
			x.reviews[date] = map[uuid.UUID]AttendeeReview{}
		}
		x.reviews[date][user] = r
	}
	return x, rows.Err()
}

// apply turns a day's attendees into what the rules say, in order: the camera
// rule, then field work, then a reviewer's decision, which has the last word.
func (x *attendanceExtras) apply(day *AttendanceDay) {
	for i := range day.People {
		a := &day.People[i]
		if a.Status == "present" && day.CameraRequired {
			on := x.cameraOn[day.Date][a.UserID]
			a.CameraOn = &on
			if !on {
				a.Status = "review"
			}
		}
		if a.Status == "absent" || a.Status == "unknown" {
			if f, ok := x.field[day.Date][a.UserID]; ok {
				field := f
				a.Status = "field_work"
				a.Field = &field
			}
		}
		if r, ok := x.reviews[day.Date][a.UserID]; ok {
			review := r
			review.Was = a.Status
			a.Status = r.Decision
			a.Review = &review
		}
	}
}
