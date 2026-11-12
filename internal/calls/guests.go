package calls

// Guests: people from outside the organization let into one meeting by its
// guest link, with a passcode if the organizer set one, under the name they
// give. They have no account. What they get is a LiveKit token for that one
// room, with an identity of their own (guest-<id>) that nothing else in TeamUp
// mistakes for a member.
//
// A guest comes in only while the meeting is on, that is once somebody from the
// organization has opened it; before that the page waits. A guest put out of
// the meeting, or every guest when the link is turned off, cannot come back on
// a token they already hold: the webhook sees them arrive and puts them out.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/livekit/protocol/auth"

	apperrors "github.com/teamup/teamup/pkg/errors"
)

// GuestIdentityPrefix starts a guest's identity in a room.
const GuestIdentityPrefix = "guest-"

const (
	// guestTokenTTL is short because a token cannot be taken back; LiveKit
	// renews it for somebody who stays connected.
	guestTokenTTL = 10 * time.Minute
	// Wrong passcodes allowed in attemptWindow: from one address, and for the
	// link from everywhere, so guessing from many addresses is slow as well.
	attemptsPerAddress = 5
	attemptsPerLink    = 50
	attemptWindow      = 10 * time.Minute
	maxGuestName       = 60
	minPasscode        = 4
	maxPasscode        = 32
)

var (
	// ErrNoGuestLink is an unknown token, or a link that is turned off: the
	// same answer for both, so a token cannot be tested for having existed.
	ErrNoGuestLink = apperrors.New(apperrors.CodeNotFound, "this link does not work any more")
	// ErrWrongPasscode is a passcode that does not match.
	ErrWrongPasscode = apperrors.New(apperrors.CodeUnauthorized, "that passcode is not right")
	// ErrTooManyTries is too many wrong passcodes in a short time.
	ErrTooManyTries = apperrors.New(apperrors.CodeRateLimited, "too many wrong passcodes; try again in a few minutes")
	// ErrNotStarted is a meeting nobody from the organization has opened yet.
	ErrNotStarted = apperrors.New(apperrors.CodeConflict, "the meeting has not started yet")
)

// GuestLink is a meeting's guest link as a member of the meeting sees it.
type GuestLink struct {
	Enabled bool `json:"enabled"`
	// DirectPath lets a guest in without the passcode; Path asks for it, for
	// when the direct one cannot be used.
	DirectPath string `json:"directPath,omitempty"`
	Path       string `json:"path,omitempty"`
	Passcode   string `json:"passcode,omitempty"`
	// Invites is who from outside was invited by email.
	Invites []GuestInvite `json:"invites"`
	// MailConfigured says whether invitations can be emailed at all.
	MailConfigured bool `json:"mailConfigured"`
	// CanManage is whether the person asking may turn it on or off: the
	// meeting's organizer, or an administrator.
	CanManage bool `json:"canManage"`
}

// GuestLinkChange is what the organizer changes.
type GuestLinkChange struct {
	Enabled bool `json:"enabled"`
	// Passcode replaces the passcode; "" removes it, nil leaves it.
	Passcode *string `json:"passcode"`
	// NewLink makes a new address, so the old one stops working. Guests
	// already in the meeting stay.
	NewLink bool `json:"newLink"`
}

// GuestMeeting is what the guest page shows before anybody has a passcode.
type GuestMeeting struct {
	Title            string     `json:"title"`
	Organization     string     `json:"organization"`
	StartsAt         *time.Time `json:"startsAt,omitempty"`
	EndsAt           *time.Time `json:"endsAt,omitempty"`
	State            string     `json:"state"` // waiting, open or ended
	PasscodeRequired bool       `json:"passcodeRequired"`
}

// GuestJoin is what a guest needs to enter the room.
type GuestJoin struct {
	Token    string `json:"token"`
	RoomName string `json:"roomName"`
	Identity string `json:"identity"`
	Name     string `json:"name"`
	// RejoinKey lets this browser back in, as the same guest, without the
	// passcode, until the meeting ends or the guest is removed.
	RejoinKey string `json:"rejoinKey"`
}

// GuestInvite is somebody from outside invited to a meeting by email.
type GuestInvite struct {
	Email     string     `json:"email"`
	EmailedAt *time.Time `json:"emailedAt,omitempty"`
}

func guestPath(token string) string { return "/join/" + token }

// mayManageGuests is the organizer or an administrator.
func mayManageGuests(call *CallModel, userID uuid.UUID, admin bool) bool {
	return admin || call.CreatedBy == userID
}

// GuestLinkFor returns a meeting's guest link to somebody who may be in the
// meeting, or who administers the organization.
func (s *Service) GuestLinkFor(ctx context.Context, orgID, callID, userID uuid.UUID, admin bool) (*GuestLink, error) {
	call, err := s.GetCall(ctx, orgID, callID)
	if err != nil {
		return nil, err
	}
	if !admin {
		may, err := s.MayJoin(ctx, orgID, callID, userID)
		if err != nil {
			return nil, err
		}
		if !may {
			return nil, apperrors.ErrNotFound
		}
	}
	link := &GuestLink{CanManage: mayManageGuests(call, userID, admin), Invites: []GuestInvite{},
		MailConfigured: s.guestMail != nil && s.guestMail.Configured(ctx, orgID)}
	if link.Invites, err = s.guestInvites(ctx, callID); err != nil {
		return nil, err
	}
	var token string
	var direct, passcode *string
	err = s.db.QueryRow(ctx, `SELECT token, direct_token, passcode, enabled FROM call_guest_links WHERE call_id = $1`, callID).
		Scan(&token, &direct, &passcode, &link.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return link, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read guest link: %w", err)
	}
	if link.Enabled {
		// A link made before direct links existed gets one now.
		if direct == nil {
			fresh, err := newGuestToken()
			if err != nil {
				return nil, err
			}
			if err := s.db.QueryRow(ctx, `UPDATE call_guest_links SET direct_token = COALESCE(direct_token, $2) WHERE call_id = $1 RETURNING direct_token`,
				callID, fresh).Scan(&direct); err != nil {
				return nil, fmt.Errorf("make direct link: %w", err)
			}
		}
		link.Path, link.DirectPath = guestPath(token), guestPath(*direct)
		if passcode != nil {
			link.Passcode = *passcode
		}
	}
	return link, nil
}

// SetGuestLink turns a meeting's guest link on or off, sets its passcode, or
// gives it a new address. Turning it off puts out any guest in the meeting.
func (s *Service) SetGuestLink(ctx context.Context, orgID, callID, userID uuid.UUID, admin bool, change GuestLinkChange) (*GuestLink, error) {
	call, err := s.GetCall(ctx, orgID, callID)
	if err != nil {
		return nil, err
	}
	if !mayManageGuests(call, userID, admin) {
		if may, _ := s.MayJoin(ctx, orgID, callID, userID); !may {
			return nil, apperrors.ErrNotFound
		}
		return nil, apperrors.New(apperrors.CodeForbidden, "only the organizer can let guests in")
	}
	if change.Enabled && (call.Status == "ended" && call.ScheduledEnd == nil || call.Status == "cancelled") {
		return nil, apperrors.New(apperrors.CodeConflict, "this meeting is over")
	}
	var passcode *string
	if change.Passcode != nil {
		p := strings.TrimSpace(*change.Passcode)
		if p != "" {
			if n := len([]rune(p)); n < minPasscode || n > maxPasscode {
				return nil, apperrors.New(apperrors.CodeValidation, fmt.Sprintf("a passcode is %d to %d characters", minPasscode, maxPasscode))
			}
			passcode = &p
		}
	}

	token, err := newGuestToken()
	if err != nil {
		return nil, err
	}
	direct, err := newGuestToken()
	if err != nil {
		return nil, err
	}
	// The addresses are kept unless new ones are asked for; the passcode unless
	// a new one is given.
	if _, err := s.db.Exec(ctx, `
		INSERT INTO call_guest_links (call_id, org_id, token, direct_token, passcode, enabled, created_by)
		VALUES ($1, $2, $3, $9, $4, $5, $6)
		ON CONFLICT (call_id) DO UPDATE SET
		    enabled = EXCLUDED.enabled,
		    token = CASE WHEN $7 THEN EXCLUDED.token ELSE call_guest_links.token END,
		    direct_token = CASE WHEN $7 OR call_guest_links.direct_token IS NULL THEN EXCLUDED.direct_token ELSE call_guest_links.direct_token END,
		    passcode = CASE WHEN $8 THEN EXCLUDED.passcode ELSE call_guest_links.passcode END,
		    updated_at = NOW()`,
		callID, orgID, token, passcode, change.Enabled, userID, change.NewLink, change.Passcode != nil, direct); err != nil {
		return nil, fmt.Errorf("save guest link: %w", err)
	}
	if !change.Enabled {
		s.putOutGuests(ctx, call)
	}
	return s.GuestLinkFor(ctx, orgID, callID, userID, admin)
}

// putOutGuests takes every guest out of the meeting's room, and keeps them out.
func (s *Service) putOutGuests(ctx context.Context, call *CallModel) {
	rows, err := s.db.Query(ctx, `UPDATE call_guests SET removed_at = NOW() WHERE call_id = $1 AND removed_at IS NULL RETURNING id`, call.ID)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not mark the guests removed")
		return
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not read the guests")
		return
	}
	for _, id := range ids {
		if err := s.mediaOf().RemoveParticipant(ctx, call.RoomName, GuestIdentityPrefix+id.String()); err != nil {
			s.logger.Warn().Err(err).Msg("could not take a guest out of a meeting")
		}
	}
}

// RemoveGuest takes one guest out of a meeting for good. The organizer or an
// administrator may.
func (s *Service) RemoveGuest(ctx context.Context, orgID, callID, userID uuid.UUID, admin bool, guestID uuid.UUID) error {
	call, err := s.GetCall(ctx, orgID, callID)
	if err != nil {
		return err
	}
	if !mayManageGuests(call, userID, admin) {
		return apperrors.New(apperrors.CodeForbidden, "only the organizer can remove a guest")
	}
	tag, err := s.db.Exec(ctx, `UPDATE call_guests SET removed_at = COALESCE(removed_at, NOW()) WHERE id = $1 AND call_id = $2`, guestID, callID)
	if err != nil {
		return fmt.Errorf("remove guest: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperrors.ErrNotFound
	}
	return s.mediaOf().RemoveParticipant(ctx, call.RoomName, GuestIdentityPrefix+guestID.String())
}

type guestLinkRow struct {
	callID   uuid.UUID
	orgID    uuid.UUID
	passcode *string
	// direct is the address that needs no passcode.
	direct bool
}

func (s *Service) guestLink(ctx context.Context, token string) (*guestLinkRow, error) {
	if token == "" || len(token) > 64 {
		return nil, ErrNoGuestLink
	}
	var r guestLinkRow
	err := s.db.QueryRow(ctx, `
		SELECT call_id, org_id, passcode, direct_token IS NOT DISTINCT FROM $1
		FROM call_guest_links WHERE (token = $1 OR direct_token = $1) AND enabled`, token).
		Scan(&r.callID, &r.orgID, &r.passcode, &r.direct)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoGuestLink
	}
	if err != nil {
		return nil, fmt.Errorf("read guest link: %w", err)
	}
	return &r, nil
}

func (s *Service) guestState(call *CallModel) string {
	switch {
	case call.Status == "active":
		return "open"
	case call.Status == "cancelled", call.Status == "ended" && (call.ScheduledEnd == nil || !call.ScheduledEnd.After(s.clock())):
		return "ended"
	}
	return "waiting"
}

// GuestMeeting describes the meeting behind a guest link, for the page a guest
// opens. It says nothing more than the invitation would.
func (s *Service) GuestMeeting(ctx context.Context, token string) (*GuestMeeting, error) {
	link, err := s.guestLink(ctx, token)
	if err != nil {
		return nil, err
	}
	call, err := s.GetCall(ctx, link.orgID, link.callID)
	if err != nil {
		return nil, ErrNoGuestLink
	}
	m := &GuestMeeting{Title: "Meeting", StartsAt: call.ScheduledStart, EndsAt: call.ScheduledEnd,
		State: s.guestState(call), PasscodeRequired: link.passcode != nil && !link.direct}
	if call.Title != nil && *call.Title != "" {
		m.Title = *call.Title
	}
	_ = s.db.QueryRow(ctx, `SELECT name FROM organizations WHERE id = $1`, link.orgID).Scan(&m.Organization)
	return m, nil
}

// JoinAsGuest lets somebody into the meeting behind a guest link under the name
// they give, if the passcode is right or the link is the direct one, and the
// meeting is on. A rejoin key from an earlier join in the same browser lets the
// same guest back in without the passcode.
func (s *Service) JoinAsGuest(ctx context.Context, token, name, passcode, rejoinKey, ip string) (*GuestJoin, error) {
	link, err := s.guestLink(ctx, token)
	if err != nil {
		return nil, err
	}

	var id uuid.UUID
	if rejoinKey != "" {
		var known string
		err := s.db.QueryRow(ctx, `
			SELECT id, name FROM call_guests
			WHERE call_id = $1 AND rejoin_hash = $2 AND removed_at IS NULL
			ORDER BY created_at DESC LIMIT 1`, link.callID, hashKey(rejoinKey)).Scan(&id, &known)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("find guest: %w", err)
		}
		if err == nil {
			name = known
		}
	}
	rejoining := id != uuid.Nil
	if !rejoining {
		if name, err = cleanGuestName(name); err != nil {
			return nil, err
		}
		if link.passcode != nil && !link.direct {
			if err := s.checkPasscode(ctx, link, passcode, ip); err != nil {
				return nil, err
			}
		}
	}

	call, err := s.GetCall(ctx, link.orgID, link.callID)
	if err != nil {
		return nil, ErrNoGuestLink
	}
	switch s.guestState(call) {
	case "ended":
		return nil, ErrCallOver
	case "waiting":
		return nil, ErrNotStarted
	}

	if !rejoining {
		id = uuid.New()
		if rejoinKey, err = newGuestToken(); err != nil {
			return nil, err
		}
		if _, err := s.db.Exec(ctx, `INSERT INTO call_guests (id, call_id, name, created_at, rejoin_hash) VALUES ($1, $2, $3, $4, $5)`,
			id, call.ID, name, s.clock(), hashKey(rejoinKey)); err != nil {
			return nil, fmt.Errorf("record guest: %w", err)
		}
	}
	identity := GuestIdentityPrefix + id.String()
	lkToken, err := s.guestToken(call.RoomName, identity, name)
	if err != nil {
		return nil, err
	}
	return &GuestJoin{Token: lkToken, RoomName: call.RoomName, Identity: identity, Name: name, RejoinKey: rejoinKey}, nil
}

// checkPasscode compares the passcode, counting wrong ones by address and for
// the link, and refusing once there have been too many.
func (s *Service) checkPasscode(ctx context.Context, link *guestLinkRow, passcode, ip string) error {
	// Counting, comparing and recording a wrong guess happen as one, a link at
	// a time: otherwise guesses sent all at once each find the count still
	// under the limit, and the limit stops none of them.
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("check passcode: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "guest-passcode:"+link.callID.String()); err != nil {
		return fmt.Errorf("check passcode: %w", err)
	}
	var mine, all int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE ip = $2), COUNT(*)
		FROM call_guest_attempts WHERE call_id = $1 AND at > $3`,
		link.callID, ip, s.clock().Add(-attemptWindow)).Scan(&mine, &all); err != nil {
		return fmt.Errorf("count attempts: %w", err)
	}
	if mine >= attemptsPerAddress || all >= attemptsPerLink {
		return ErrTooManyTries
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(passcode)), []byte(*link.passcode)) != 1 {
		if _, err := tx.Exec(ctx, `INSERT INTO call_guest_attempts (call_id, ip, at) VALUES ($1, $2, $3)`, link.callID, ip, s.clock()); err != nil {
			return fmt.Errorf("record attempt: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("record attempt: %w", err)
		}
		return ErrWrongPasscode
	}
	return nil
}

func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func (s *Service) guestToken(room, identity, name string) (string, error) {
	if s.cfg.LiveKitAPIKey == "" || s.cfg.LiveKitAPISecret == "" {
		return "", fmt.Errorf("LiveKit credentials not configured")
	}
	yes, no := true, false
	meta, _ := json.Marshal(map[string]any{"role": "guest"})
	at := auth.NewAccessToken(s.cfg.LiveKitAPIKey, s.cfg.LiveKitAPISecret)
	at.SetIdentity(identity).SetName(name).SetMetadata(string(meta)).SetValidFor(guestTokenTTL).
		SetVideoGrant(&auth.VideoGrant{
			RoomJoin: true, Room: room, CanPublish: &yes, CanSubscribe: &yes, CanPublishData: &yes,
			// A guest cannot rename themselves into somebody else.
			CanUpdateOwnMetadata: &no,
		})
	return at.ToJWT()
}

// guestArrived is the webhook seeing a guest connect: one who was put out, or
// whose link is off, is put out again.
func (s *Service) guestArrived(ctx context.Context, call *webhookCall, roomName, identity string) {
	id, err := uuid.Parse(strings.TrimPrefix(identity, GuestIdentityPrefix))
	welcome := false
	if err == nil {
		if qerr := s.db.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM call_guests g JOIN call_guest_links l ON l.call_id = g.call_id
			               WHERE g.id = $1 AND g.call_id = $2 AND g.removed_at IS NULL AND l.enabled)`,
			id, call.ID).Scan(&welcome); qerr != nil {
			s.logger.Warn().Err(qerr).Msg("could not check a guest")
			return
		}
	}
	if !welcome {
		if err := s.mediaOf().RemoveParticipant(ctx, roomName, identity); err != nil {
			s.logger.Warn().Err(err).Msg("could not put out a guest who may not be there")
		}
	}
}

func cleanGuestName(name string) (string, error) {
	name = strings.Join(strings.FieldsFunc(name, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	if name == "" {
		return "", apperrors.New(apperrors.CodeValidation, "say what your name is")
	}
	if len([]rune(name)) > maxGuestName {
		return "", apperrors.New(apperrors.CodeValidation, fmt.Sprintf("a name is at most %d characters", maxGuestName))
	}
	return name, nil
}

// newGuestToken is 128 random bits, as an address-safe string.
func newGuestToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("make guest link: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// newPasscode is six digits: easy to read out on the phone, and the limits on
// wrong ones make guessing hopeless.
func newPasscode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}
