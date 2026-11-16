package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/bcrypt"

	"github.com/teamup/teamup/internal/mail"
	"github.com/teamup/teamup/internal/observability"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

// Forgotten passwords. Somebody asks for a link by email address; if there is
// an account at that address, and its organization can send mail, a one-time
// link goes to it. The answer to the request is the same whatever happened, and
// comes before any of the work, so it never says whether there is an account.
// Without mail the organization's administrators are asked, in the app, to
// reset the password for them, which is what had to happen before.

const (
	// ResetLinkValidFor is how long an emailed link works.
	ResetLinkValidFor = 30 * time.Minute
	// resetRequestsAllowed is how many links one account is sent in
	// resetRequestWindow, so the form cannot fill somebody's inbox.
	resetRequestsAllowed = 3
	resetRequestWindow   = time.Hour
)

// ErrResetLinkInvalid is a link that is unknown, used, replaced or out of date.
// They are one answer, so a guessed token learns nothing about real ones.
var ErrResetLinkInvalid = apperrors.New(apperrors.CodeBadRequest,
	"this reset link has expired or has already been used; ask for a new one")

// ResetMailer sends the reset emails with an organization's own mail server.
// *mail.Service is one.
type ResetMailer interface {
	Configured(ctx context.Context, orgID uuid.UUID) bool
	Send(ctx context.Context, orgID uuid.UUID, msg mail.Message) error
	PasswordResetMessage(orgName, name, email, token string, validFor time.Duration) mail.Message
	PasswordChangedMessage(orgName, name, email string) mail.Message
}

// AdminNotifier tells people something in the app: notifications.Service.NotifyMany.
type AdminNotifier func(ctx context.Context, orgID uuid.UUID, userIDs []uuid.UUID, kind, title, body string, data map[string]any) error

// resetRequests counts reset requests per account when there is no Redis.
type resetRequests struct {
	mu    sync.Mutex
	local map[string][]time.Time
}

// SetPasswordResets gives the service what it needs to send reset links, and
// to ask administrators when it cannot.
func (s *Service) SetPasswordResets(mailer ResetMailer, notify AdminNotifier) {
	s.resetMailer = mailer
	s.notifyAdmins = notify
}

// inBackground runs reset work after the answer has gone. Tests replace it to
// run the work in place.
func (s *Service) inBackground(work func(ctx context.Context)) {
	if s.runReset != nil {
		s.runReset(work)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		work(ctx)
	}()
}

// RequestPasswordReset starts a reset for whoever has this address, if anybody
// does. It returns at once and reports nothing: what happens next is in the log.
func (s *Service) RequestPasswordReset(email, ip, userAgent string) {
	email = strings.TrimSpace(email)
	s.inBackground(func(ctx context.Context) {
		if err := s.requestPasswordReset(ctx, email, ip, userAgent); err != nil {
			s.resetLog().Error().Err(err).Msg("password reset request failed")
		}
	})
}

func (s *Service) resetLog() *zerolog.Logger {
	l := observability.Logger(context.Background()).With().Str("component", "auth").Logger()
	return &l
}

func (s *Service) requestPasswordReset(ctx context.Context, email, ip, userAgent string) error {
	log := s.resetLog()
	if !s.allowResetRequest(ctx, email) {
		log.Info().Msg("password reset not sent: too many asked for this account in the last hour")
		return nil
	}
	user, err := s.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
			log.Info().Msg("password reset not sent: no such account")
			return nil
		}
		return fmt.Errorf("find account: %w", err)
	}
	if !user.IsActive || user.PasswordHash == "" {
		log.Info().Str("user_id", user.ID.String()).Msg("password reset not sent: the account is disabled or has no password")
		return nil
	}
	orgName := s.orgName(ctx, user.OrgID)

	if s.resetMailer != nil && s.resetMailer.Configured(ctx, user.OrgID) {
		token, id, err := s.issueResetLink(ctx, user.ID, ip, userAgent)
		if err != nil {
			return err
		}
		msg := s.resetMailer.PasswordResetMessage(orgName, user.Name, user.Email, token, ResetLinkValidFor)
		if err := s.resetMailer.Send(ctx, user.OrgID, msg); err == nil {
			log.Info().Str("user_id", user.ID.String()).Msg("password reset link sent")
			return nil
		} else {
			log.Warn().Err(err).Str("user_id", user.ID.String()).Msg("could not email a password reset link; asking the administrators")
		}
		// A link that never arrived is no use to anybody.
		if _, err := s.db.Exec(ctx, `UPDATE password_resets SET used_at = NOW() WHERE id = $1`, id); err != nil {
			log.Warn().Err(err).Msg("could not retire an unsent reset link")
		}
	}
	return s.askAdminsToReset(ctx, user)
}

// issueResetLink makes a new link for somebody, retiring any they had: only the
// latest link works.
func (s *Service) issueResetLink(ctx context.Context, userID uuid.UUID, ip, userAgent string) (string, uuid.UUID, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", uuid.Nil, fmt.Errorf("make reset token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`UPDATE password_resets SET used_at = NOW() WHERE user_id = $1 AND used_at IS NULL`, userID); err != nil {
		return "", uuid.Nil, fmt.Errorf("retire old reset links: %w", err)
	}
	var addr *string
	if ip != "" {
		addr = &ip
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO password_resets (user_id, token_hash, expires_at, requested_ip, requested_ua)
		VALUES ($1, $2, NOW() + $3::interval, $4::inet, NULLIF($5, ''))
		RETURNING id`,
		userID, tokenHash(token), fmt.Sprintf("%d seconds", int(ResetLinkValidFor.Seconds())), addr, userAgent,
	).Scan(&id); err != nil {
		return "", uuid.Nil, fmt.Errorf("store reset link: %w", err)
	}
	return token, id, tx.Commit(ctx)
}

// askAdminsToReset is the way back in when no email can be sent: the people
// who can reset passwords are told who asked.
func (s *Service) askAdminsToReset(ctx context.Context, user *User) error {
	if s.notifyAdmins == nil {
		s.resetLog().Warn().Str("user_id", user.ID.String()).Msg("password reset asked for, but there is neither mail nor anybody to tell")
		return nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT u.id FROM users u
		JOIN user_roles ur ON ur.user_id = u.id
		JOIN roles r ON r.id = ur.role_id
		WHERE u.org_id = $1 AND u.deleted_at IS NULL AND COALESCE(u.is_active, true)
		  AND u.id <> $2 AND r.permissions ?| array['*', 'members.manage']`, user.OrgID, user.ID)
	if err != nil {
		return fmt.Errorf("find administrators: %w", err)
	}
	admins, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return fmt.Errorf("read administrators: %w", err)
	}
	if len(admins) == 0 {
		s.resetLog().Warn().Str("user_id", user.ID.String()).Msg("password reset asked for, but mail is not set up and there is no other administrator")
		return nil
	}
	if err := s.notifyAdmins(ctx, user.OrgID, admins, "password.reset_requested",
		fmt.Sprintf("%s forgot their password", user.Name),
		"Mail is not set up, so no reset link could be emailed. Reset their password from Members and give them the temporary one.",
		map[string]any{"userId": user.ID.String()}); err != nil {
		return fmt.Errorf("tell the administrators: %w", err)
	}
	s.resetLog().Info().Str("user_id", user.ID.String()).Int("administrators", len(admins)).Msg("password reset asked for without mail; administrators told")
	return nil
}

// CheckPasswordReset says whose a working link is, so the page can say so, or
// that it has expired, before anybody types a new password.
func (s *Service) CheckPasswordReset(ctx context.Context, token string) (string, error) {
	var email string
	err := s.db.QueryRow(ctx, `
		SELECT u.email FROM password_resets pr JOIN users u ON u.id = pr.user_id
		WHERE pr.token_hash = $1 AND pr.used_at IS NULL AND pr.expires_at > NOW()
		  AND u.deleted_at IS NULL AND COALESCE(u.is_active, true)`, tokenHash(token)).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrResetLinkInvalid
	}
	if err != nil {
		return "", fmt.Errorf("check reset link: %w", err)
	}
	return email, nil
}

// ResetPassword sets a new password with a link, once. Everywhere the person
// was signed in is signed out, as changing a password does.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	if len(newPassword) < MinPasswordLength {
		return apperrors.New(apperrors.CodeBadRequest,
			fmt.Sprintf("the new password must be at least %d characters", MinPasswordLength))
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID, orgID uuid.UUID
	var email, name string
	// FOR UPDATE: two uses of one link at once, and only the first wins.
	err = tx.QueryRow(ctx, `
		SELECT u.id, u.org_id, u.email, u.name FROM password_resets pr JOIN users u ON u.id = pr.user_id
		WHERE pr.token_hash = $1 AND pr.used_at IS NULL AND pr.expires_at > NOW()
		  AND u.deleted_at IS NULL AND COALESCE(u.is_active, true)
		FOR UPDATE OF pr`, tokenHash(token)).Scan(&userID, &orgID, &email, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrResetLinkInvalid
	}
	if err != nil {
		return fmt.Errorf("check reset link: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users SET password_hash = $2, must_change_password = FALSE, updated_at = NOW()
		WHERE id = $1`, userID, hash); err != nil {
		return fmt.Errorf("store new password: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE password_resets SET used_at = NOW() WHERE user_id = $1 AND used_at IS NULL`, userID); err != nil {
		return fmt.Errorf("retire reset links: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	s.sessionsEnded(userID.String())
	// The wrong guesses before somebody remembered they had forgotten no longer count.
	s.clearFailures(ctx, email)
	s.resetLog().Info().Str("user_id", userID.String()).Msg("password reset with an emailed link")

	if s.resetMailer != nil {
		orgName := s.orgName(ctx, orgID)
		s.inBackground(func(ctx context.Context) {
			if err := s.resetMailer.Send(ctx, orgID, s.resetMailer.PasswordChangedMessage(orgName, name, email)); err != nil {
				s.resetLog().Warn().Err(err).Str("user_id", userID.String()).Msg("could not email that the password was changed")
			}
		})
	}
	return nil
}

// retireResetLinks makes somebody's outstanding links worthless, when their
// password changes some other way.
func (s *Service) retireResetLinks(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE password_resets SET used_at = NOW() WHERE user_id = $1 AND used_at IS NULL`, userID)
	return err
}

func (s *Service) orgName(ctx context.Context, orgID uuid.UUID) string {
	var name string
	if err := s.db.QueryRow(ctx, `SELECT name FROM organizations WHERE id = $1`, orgID).Scan(&name); err != nil || name == "" {
		return "your organization"
	}
	return name
}

func tokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// allowResetRequest counts one more request for this account and says whether
// it is within the limit.
func (s *Service) allowResetRequest(ctx context.Context, email string) bool {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	key := "reset-requests:" + hex.EncodeToString(sum[:16])
	if s.redis != nil {
		pipe := s.redis.TxPipeline()
		n := pipe.Incr(ctx, key)
		pipe.ExpireNX(ctx, key, resetRequestWindow)
		if _, err := pipe.Exec(ctx); err == nil {
			return n.Val() <= resetRequestsAllowed
		}
		// Unable to count is no reason to leave somebody locked out.
		return true
	}
	s.resets.mu.Lock()
	defer s.resets.mu.Unlock()
	if s.resets.local == nil {
		s.resets.local = map[string][]time.Time{}
	}
	var keep []time.Time
	for _, at := range s.resets.local[key] {
		if time.Since(at) < resetRequestWindow {
			keep = append(keep, at)
		}
	}
	keep = append(keep, time.Now())
	s.resets.local[key] = keep
	return len(keep) <= resetRequestsAllowed
}
