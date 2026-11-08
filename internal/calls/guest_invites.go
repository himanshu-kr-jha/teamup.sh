package calls

// People from outside invited to a meeting by email. Inviting somebody turns the
// meeting's guest link on if it is off, and emails them the direct link, with
// the passcode link as a fallback. When the meeting moves or is cancelled they
// are emailed again. With no mail server set up, they are still listed, and the
// organizer sends the invitation themselves.

import (
	"context"
	"fmt"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/teamup/teamup/internal/mail"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

// maxGuestInvites is the most outside addresses one meeting may have.
const maxGuestInvites = 50

// GuestMailer sends the invitations, when the organization has mail set up.
type GuestMailer interface {
	Send(ctx context.Context, orgID uuid.UUID, msg mail.Message) error
	Configured(ctx context.Context, orgID uuid.UUID) bool
}

// SetGuestMailer lets invitations be emailed. baseURL is TeamUp's own address,
// which the links in them start with.
func (s *Service) SetGuestMailer(m GuestMailer, baseURL string) {
	s.guestMail = m
	s.baseURL = strings.TrimRight(baseURL, "/")
}

func (s *Service) guestInvites(ctx context.Context, callID uuid.UUID) ([]GuestInvite, error) {
	rows, err := s.db.Query(ctx, `SELECT email, emailed_at FROM call_guest_invites WHERE call_id = $1 ORDER BY created_at, email`, callID)
	if err != nil {
		return nil, fmt.Errorf("read invitations: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (GuestInvite, error) {
		var g GuestInvite
		return g, r.Scan(&g.Email, &g.EmailedAt)
	})
	if out == nil {
		out = []GuestInvite{}
	}
	return out, err
}

// cleanEmails reads a list of addresses, refusing any that is not one.
func cleanEmails(raw []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		addr, err := netmail.ParseAddress(r)
		if err != nil || strings.ContainsAny(addr.Address, " \r\n") || !strings.Contains(addr.Address[strings.LastIndex(addr.Address, "@")+1:], ".") {
			return nil, apperrors.New(apperrors.CodeValidation, fmt.Sprintf("%q is not an email address", r))
		}
		e := strings.ToLower(addr.Address)
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out, nil
}

// InviteGuests invites people from outside to a meeting by email: the
// organizer or an administrator may. The guest link is turned on, with a
// passcode, if it was off. Everybody named is emailed, again if they were
// invited before, when mail is set up. timeZone is the inviter's, which the
// emails give the time in.
func (s *Service) InviteGuests(ctx context.Context, orgID, callID, userID uuid.UUID, admin bool, emails []string, timeZone string) (*GuestLink, error) {
	call, err := s.GetCall(ctx, orgID, callID)
	if err != nil {
		return nil, err
	}
	if !mayManageGuests(call, userID, admin) {
		if may, _ := s.MayJoin(ctx, orgID, callID, userID); !may {
			return nil, apperrors.ErrNotFound
		}
		return nil, apperrors.New(apperrors.CodeForbidden, "only the organizer can invite guests")
	}
	if s.guestState(call) == "ended" {
		return nil, apperrors.New(apperrors.CodeConflict, "this meeting is over")
	}
	list, err := cleanEmails(emails)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, apperrors.New(apperrors.CodeValidation, "give at least one email address")
	}
	if _, err := time.LoadLocation(timeZone); err != nil || timeZone == "" {
		timeZone = "UTC"
	}
	var have int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM call_guest_invites WHERE call_id = $1 AND NOT (email = ANY($2))`, callID, list).Scan(&have); err != nil {
		return nil, err
	}
	if have+len(list) > maxGuestInvites {
		return nil, apperrors.New(apperrors.CodeValidation, fmt.Sprintf("a meeting can have at most %d guests invited by email", maxGuestInvites))
	}

	// The link is turned on, with a passcode, if it is not on already.
	link, err := s.GuestLinkFor(ctx, orgID, callID, userID, admin)
	if err != nil {
		return nil, err
	}
	if !link.Enabled {
		code, err := newPasscode()
		if err != nil {
			return nil, err
		}
		if _, err := s.SetGuestLink(ctx, orgID, callID, userID, admin, GuestLinkChange{Enabled: true, Passcode: &code}); err != nil {
			return nil, err
		}
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO call_guest_invites (call_id, email, time_zone, invited_by, created_at)
		SELECT $1, e, $3, $4, NOW() + n * interval '1 microsecond' -- listed in the order given
		FROM UNNEST($2::text[]) WITH ORDINALITY AS t(e, n)
		ON CONFLICT (call_id, email) DO UPDATE SET time_zone = EXCLUDED.time_zone`,
		callID, list, timeZone, userID); err != nil {
		return nil, fmt.Errorf("save invitations: %w", err)
	}
	s.emailGuests(ctx, orgID, callID, list, "invited")
	return s.GuestLinkFor(ctx, orgID, callID, userID, admin)
}

// RemoveGuestInvite takes somebody off a meeting's email invitations. It does
// not stop the link working for them; a new link does that.
func (s *Service) RemoveGuestInvite(ctx context.Context, orgID, callID, userID uuid.UUID, admin bool, email string) (*GuestLink, error) {
	call, err := s.GetCall(ctx, orgID, callID)
	if err != nil {
		return nil, err
	}
	if !mayManageGuests(call, userID, admin) {
		return nil, apperrors.New(apperrors.CodeForbidden, "only the organizer can change who is invited")
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM call_guest_invites WHERE call_id = $1 AND email = $2`, callID, strings.ToLower(strings.TrimSpace(email))); err != nil {
		return nil, fmt.Errorf("remove invitation: %w", err)
	}
	return s.GuestLinkFor(ctx, orgID, callID, userID, admin)
}

// GuestsMeetingChanged emails everybody invited from outside that a meeting
// moved or was cancelled. The calendar calls it.
func (s *Service) GuestsMeetingChanged(ctx context.Context, orgID, callID uuid.UUID, cancelled bool) {
	rows, err := s.db.Query(ctx, `SELECT email FROM call_guest_invites WHERE call_id = $1`, callID)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not read a meeting's guests to tell them")
		return
	}
	emails, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(emails) == 0 {
		return
	}
	kind := "moved"
	if cancelled {
		kind = "cancelled"
	}
	s.emailGuests(ctx, orgID, callID, emails, kind)
}

// emailGuests sends one kind of email to each address, when mail is set up. A
// failure is logged and does not undo anything.
func (s *Service) emailGuests(ctx context.Context, orgID, callID uuid.UUID, emails []string, kind string) {
	if s.guestMail == nil || !s.guestMail.Configured(ctx, orgID) || len(emails) == 0 {
		return
	}
	var title, org, organizer, token string
	var direct, passcode *string
	var start, end *time.Time
	if err := s.db.QueryRow(ctx, `
		SELECT COALESCE(NULLIF(c.title, ''), 'a meeting'), o.name, u.name, c.scheduled_start, c.scheduled_end,
		       COALESCE(l.token, ''), l.direct_token, l.passcode
		FROM calls c JOIN organizations o ON o.id = c.org_id JOIN users u ON u.id = c.created_by
		LEFT JOIN call_guest_links l ON l.call_id = c.id AND l.enabled
		WHERE c.id = $1 AND c.org_id = $2`, callID, orgID).
		Scan(&title, &org, &organizer, &start, &end, &token, &direct, &passcode); err != nil {
		s.logger.Warn().Err(err).Msg("could not read a meeting to email its guests")
		return
	}
	for _, email := range emails {
		var zone string
		_ = s.db.QueryRow(ctx, `SELECT time_zone FROM call_guest_invites WHERE call_id = $1 AND email = $2`, callID, email).Scan(&zone)
		msg := mail.Message{To: email}
		when := meetingTime(start, end, zone)
		switch kind {
		case "cancelled":
			msg.Subject = "Cancelled: " + title
			msg.Body = fmt.Sprintf("%s has cancelled %q%s.\n\nThe link you were sent no longer matters.\n", organizer, title, prefixed(" (", when, ")"))
		default:
			if token == "" {
				continue // the link was turned off: nothing to join with
			}
			msg.Subject = "Invitation: " + title
			lead := fmt.Sprintf("%s from %s invites you to %q.", organizer, org, title)
			if kind == "moved" {
				msg.Subject = "Updated: " + title
				lead = fmt.Sprintf("%q has a new time.", title)
			}
			var b strings.Builder
			fmt.Fprintf(&b, "%s\n\n", lead)
			if when != "" {
				fmt.Fprintf(&b, "When: %s\n\n", when)
			}
			if direct != nil {
				fmt.Fprintf(&b, "Join from your browser, no account needed:\n%s\n\n", s.baseURL+guestPath(*direct))
			}
			if passcode != nil {
				fmt.Fprintf(&b, "If that link does not work, open\n%s\nand enter the passcode %s.\n\n", s.baseURL+guestPath(token), *passcode)
			} else if direct == nil {
				fmt.Fprintf(&b, "Join from your browser, no account needed:\n%s\n\n", s.baseURL+guestPath(token))
			}
			b.WriteString("You can join once the meeting has started.\n")
			msg.Body = b.String()
		}
		if err := s.guestMail.Send(ctx, orgID, msg); err != nil {
			s.logger.Warn().Err(err).Msg("could not email a guest")
			continue
		}
		if kind != "cancelled" {
			_, _ = s.db.Exec(ctx, `UPDATE call_guest_invites SET emailed_at = $3 WHERE call_id = $1 AND email = $2`, callID, email, s.clock())
		}
	}
}

// meetingTime is "Monday 9 November 2026, 10:00 to 10:30 (Asia/Kolkata)".
func meetingTime(start, end *time.Time, zone string) string {
	if start == nil {
		return ""
	}
	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "" {
		loc, zone = time.UTC, "UTC"
	}
	out := start.In(loc).Format("Monday 2 January 2006, 15:04")
	if end != nil {
		if end.In(loc).YearDay() == start.In(loc).YearDay() {
			out += " to " + end.In(loc).Format("15:04")
		} else {
			out += " to " + end.In(loc).Format("Monday 2 January 2006, 15:04")
		}
	}
	return out + " (" + zone + ")"
}

func prefixed(before, s, after string) string {
	if s == "" {
		return ""
	}
	return before + s + after
}
