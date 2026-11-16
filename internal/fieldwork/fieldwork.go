// Package fieldwork lets somebody working away from the office count for the
// day's stand-ups: a photo taken in the app at that moment, with where their
// phone was, and an optional note. The owner's decision of 16 November 2026,
// for project managers who travel to sites.
//
// An organization turns it on; it is off until then. A check-in counts for
// every stand-up on its date (calls/attendance_rules.go), shown as field work
// rather than absent. The photo carries the time and place stamped on it by
// the page. A person sees their own check-ins; only whoever sees attendance
// (owners, admins, See or Review attendance) sees anybody else's.
package fieldwork

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	apperrors "github.com/teamup/teamup/pkg/errors"
)

// MaxPhotoBytes is the largest photo accepted. The page shrinks it to 1280
// pixels on the long side first, a few hundred kilobytes.
const MaxPhotoBytes = 2 * 1024 * 1024

// maxAccuracyM is the least precise location accepted: further off than this,
// a phone does not really know where it is.
const maxAccuracyM = 5000

const settingsKey = "field_work"

var photoTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true}

type Service struct {
	db    *pgxpool.Pool
	clock func() time.Time
	geo   *Geocoder
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db, clock: time.Now}
}

// WithGeocoder has check-ins carry the address around them as well.
func (s *Service) WithGeocoder(g *Geocoder) *Service {
	s.geo = g
	return s
}

// addressBudget is how long a check-in waits for its address before it is
// saved without one: the photo and the coordinates are what count.
const addressBudget = 4 * time.Second

// address is the address around a place, or nothing when it cannot be told.
func (s *Service) address(ctx context.Context, lat, lon float64) string {
	if s.geo == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, addressBudget)
	defer cancel()
	a, err := s.geo.Address(ctx, lat, lon)
	if err != nil {
		log.Warn().Err(err).Msg("Could not tell the address of a field work check-in")
		return ""
	}
	return a
}

// Place is the address around where somebody is, for the check-in they are
// about to send: shown before they send it, and printed on the photo.
func (s *Service) Place(ctx context.Context, orgID uuid.UUID, lat, lon float64) (string, error) {
	set, err := s.Settings(ctx, orgID)
	if err != nil {
		return "", err
	}
	if !set.Enabled {
		return "", apperrors.New(apperrors.CodeForbidden, "field work check-ins are not turned on for this organization")
	}
	if !validPlace(lat, lon) {
		return "", badRequest("that is not a place")
	}
	return s.address(ctx, lat, lon), nil
}

func validPlace(lat, lon float64) bool {
	return !math.IsNaN(lat) && !math.IsNaN(lon) && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 && (lat != 0 || lon != 0)
}

// Settings is the organization's choice.
type Settings struct {
	Enabled bool `json:"enabled"`
}

func (s *Service) Settings(ctx context.Context, orgID uuid.UUID) (Settings, error) {
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT value FROM org_configs WHERE org_id = $1 AND key = $2`, orgID, settingsKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("read field work settings: %w", err)
	}
	var set Settings
	if err := json.Unmarshal(raw, &set); err != nil {
		return Settings{}, fmt.Errorf("decode field work settings: %w", err)
	}
	return set, nil
}

func (s *Service) SetSettings(ctx context.Context, orgID uuid.UUID, set Settings) error {
	raw, _ := json.Marshal(set)
	_, err := s.db.Exec(ctx, `
		INSERT INTO org_configs (org_id, key, value, is_secret, created_at, updated_at)
		VALUES ($1, $2, $3, FALSE, NOW(), NOW())
		ON CONFLICT (org_id, key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
	`, orgID, settingsKey, raw)
	if err != nil {
		return fmt.Errorf("save field work settings: %w", err)
	}
	return nil
}

// Input is a check-in as the page sends it.
type Input struct {
	// Photo is a data: URL of the picture the page took.
	Photo     string  `json:"photo" binding:"required"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	AccuracyM float64 `json:"accuracyM"`
	Note      string  `json:"note"`
	// Today is the person's own date, which the server checks is within a day of its own.
	Today string `json:"today"`
}

// Checkin is one, without its photo, which is fetched on its own.
type Checkin struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"userId"`
	Name      string    `json:"name,omitempty"`
	OnDate    string    `json:"onDate"`
	TakenAt   time.Time `json:"takenAt"`
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	AccuracyM float64   `json:"accuracyM"`
	// Address is the address around the place, when it could be told.
	Address string `json:"address"`
	Note    string `json:"note"`
}

func badRequest(message string) error { return apperrors.New(apperrors.CodeBadRequest, message) }

func parsePhoto(dataURL string) ([]byte, string, error) {
	rest, ok := strings.CutPrefix(dataURL, "data:")
	if !ok {
		return nil, "", badRequest("the photo must be an image")
	}
	meta, payload, ok := strings.Cut(rest, ",")
	if !ok || !strings.HasSuffix(meta, ";base64") {
		return nil, "", badRequest("the photo must be an image")
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(raw) == 0 {
		return nil, "", badRequest("the photo could not be read")
	}
	if len(raw) > MaxPhotoBytes {
		return nil, "", badRequest("the photo must be 2 MB or smaller")
	}
	kind := http.DetectContentType(raw)
	if !photoTypes[kind] {
		return nil, "", badRequest("the photo must be a JPEG, PNG or WebP image")
	}
	return raw, kind, nil
}

// today is the person's date as they sent it, when it is within a day of the
// server's, or the server's.
func today(raw string, now time.Time) string {
	server := now.UTC().Format("2006-01-02")
	d, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return server
	}
	s, _ := time.Parse("2006-01-02", server)
	if diff := d.Sub(s); diff > 24*time.Hour || diff < -24*time.Hour {
		return server
	}
	return raw
}

// CheckIn records field work for somebody's day.
func (s *Service) CheckIn(ctx context.Context, orgID, userID uuid.UUID, in Input) (*Checkin, error) {
	set, err := s.Settings(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if !set.Enabled {
		return nil, apperrors.New(apperrors.CodeForbidden, "field work check-ins are not turned on for this organization")
	}
	if !validPlace(in.Latitude, in.Longitude) {
		return nil, badRequest("the location is missing; allow the app to use your location and try again")
	}
	if math.IsNaN(in.AccuracyM) || in.AccuracyM < 0 || in.AccuracyM > maxAccuracyM {
		return nil, badRequest("your phone could not tell where you are closely enough; try again outside, or with location turned on")
	}
	note := strings.TrimSpace(in.Note)
	if len(note) > 500 {
		return nil, badRequest("keep the note under 500 characters")
	}
	photo, kind, err := parsePhoto(in.Photo)
	if err != nil {
		return nil, err
	}
	c := Checkin{UserID: userID, OnDate: today(in.Today, s.clock()), Latitude: in.Latitude, Longitude: in.Longitude, AccuracyM: in.AccuracyM, Note: note}
	// The server's own lookup, which the phone's preview asked a moment ago, so
	// it is remembered; what the phone says the address is is not taken.
	c.Address = s.address(ctx, c.Latitude, c.Longitude)
	err = s.db.QueryRow(ctx, `
		INSERT INTO field_checkins (org_id, user_id, on_date, taken_at, latitude, longitude, accuracy_m, address, note, photo_type, photo)
		VALUES ($1, $2, $3::date, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, taken_at
	`, orgID, userID, c.OnDate, s.clock(), c.Latitude, c.Longitude, c.AccuracyM, c.Address, note, kind, photo).Scan(&c.ID, &c.TakenAt)
	if err != nil {
		return nil, fmt.Errorf("save field work: %w", err)
	}
	return &c, nil
}

// Mine is somebody's own check-ins on a date.
func (s *Service) Mine(ctx context.Context, orgID, userID uuid.UUID, rawToday string) ([]Checkin, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, on_date::text, taken_at, latitude, longitude, accuracy_m, address, note
		FROM field_checkins WHERE org_id = $1 AND user_id = $2 AND on_date = $3::date
		ORDER BY taken_at DESC
	`, orgID, userID, today(rawToday, s.clock()))
	if err != nil {
		return nil, fmt.Errorf("list field work: %w", err)
	}
	defer rows.Close()
	out := []Checkin{}
	for rows.Next() {
		var c Checkin
		var accuracy float32
		if err := rows.Scan(&c.ID, &c.UserID, &c.OnDate, &c.TakenAt, &c.Latitude, &c.Longitude, &accuracy, &c.Address, &c.Note); err != nil {
			return nil, fmt.Errorf("scan field work: %w", err)
		}
		c.AccuracyM = float64(accuracy)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Viewer is who is asking: Sees is whoever sees attendance (owners, admins,
// See or Review attendance), who may look at anybody's field work.
type Viewer struct {
	ID   uuid.UUID
	Sees bool
}

// Photo is a check-in's photo, for its owner and whoever sees attendance.
// Anybody else is told there is no such thing.
func (s *Service) Photo(ctx context.Context, orgID, id uuid.UUID, v Viewer) ([]byte, string, error) {
	var owner uuid.UUID
	var kind string
	var photo []byte
	err := s.db.QueryRow(ctx, `SELECT user_id, photo_type, photo FROM field_checkins WHERE id = $1 AND org_id = $2`, id, orgID).
		Scan(&owner, &kind, &photo)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", apperrors.ErrNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("read field work photo: %w", err)
	}
	if owner != v.ID && !v.Sees {
		return nil, "", apperrors.ErrNotFound
	}
	return photo, kind, nil
}

// ListInput is a range of dates, and whose: one person, or everybody.
type ListInput struct {
	From, To string
	UserID   *uuid.UUID
}

// List is check-ins in a range, newest first: somebody's own, or for whoever
// sees attendance, anybody's.
func (s *Service) List(ctx context.Context, orgID uuid.UUID, v Viewer, in ListInput) ([]Checkin, error) {
	from, err1 := time.Parse("2006-01-02", in.From)
	to, err2 := time.Parse("2006-01-02", in.To)
	if err1 != nil || err2 != nil || to.Before(from) {
		return nil, badRequest("from and to must be dates, from first")
	}
	if to.Sub(from) > 92*24*time.Hour {
		return nil, badRequest("a range can cover at most 92 days")
	}
	who := in.UserID
	if !v.Sees {
		// Anybody else's is not there to ask for.
		who = &v.ID
	}
	rows, err := s.db.Query(ctx, `
		SELECT f.id, f.user_id, u.name, f.on_date::text, f.taken_at, f.latitude, f.longitude, f.accuracy_m, f.address, f.note
		FROM field_checkins f JOIN users u ON u.id = f.user_id
		WHERE f.org_id = $1 AND f.on_date BETWEEN $2::date AND $3::date AND ($4::uuid IS NULL OR f.user_id = $4)
		ORDER BY f.taken_at DESC
		LIMIT 500
	`, orgID, in.From, in.To, who)
	if err != nil {
		return nil, fmt.Errorf("list field work: %w", err)
	}
	defer rows.Close()
	out := []Checkin{}
	for rows.Next() {
		var c Checkin
		var accuracy float32
		if err := rows.Scan(&c.ID, &c.UserID, &c.Name, &c.OnDate, &c.TakenAt, &c.Latitude, &c.Longitude, &accuracy, &c.Address, &c.Note); err != nil {
			return nil, fmt.Errorf("scan field work: %w", err)
		}
		c.AccuracyM = float64(accuracy)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Remove takes back one of somebody's own check-ins, made by mistake, on its day.
func (s *Service) Remove(ctx context.Context, orgID, userID, id uuid.UUID, rawToday string) error {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM field_checkins WHERE id = $1 AND org_id = $2 AND user_id = $3 AND on_date = $4::date`,
		id, orgID, userID, today(rawToday, s.clock()))
	if err != nil {
		return fmt.Errorf("remove field work: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperrors.ErrNotFound
	}
	return nil
}
