package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

const (
	// defaultSignupLocale is the locale used when a sign-up request omits one.
	defaultSignupLocale = "en"
	// signupVerificationCooldown is how long a retry keeps the previously sent
	// sign-up code instead of issuing another notification.
	signupVerificationCooldown = 60 * time.Second
)

// RegisterWithEmail creates a self-serve account. The account stays
// signup_pending until VerifySignup proves the address. A retry for an address
// that is still signup_pending replaces the credentials and sends a fresh code,
// so an attacker cannot squat on an address and block its owner. An address
// with an active/verified account receives the same successful result and is
// left untouched, so the RPC cannot be used to enumerate accounts.
//
// When invite_token is valid for the same address, the token proves control of
// the address: the account is created verified and active, the invitation is
// accepted and the user joins that organization, and no OTP is sent. An
// invalid token is the generic invalid-invite error and nothing is written.
//
// When org_name is set (and no invite is used) the organization and the
// pending user's active membership are written in the same transaction as the
// account.
func (s *Service) RegisterWithEmail(ctx context.Context, email, password, displayName, locale, orgName, inviteToken string) error {
	if !s.signupOpen {
		return ErrSignupDisabled
	}
	if s.signup == nil || s.users == nil || s.notifier == nil || s.otp == nil {
		return fmt.Errorf("sign-up is not configured")
	}

	email = domain.NormalizeEmail(email)
	if !domain.IsEmail(email) {
		return domain.ErrInvalidEmail
	}
	if strings.TrimSpace(password) == "" {
		return ErrPasswordRequired
	}
	displayName = strings.TrimSpace(displayName)
	locale = strings.TrimSpace(locale)
	if locale == "" {
		locale = defaultSignupLocale
	}

	invite, err := s.signupInvite(ctx, inviteToken, email)
	if err != nil {
		return err
	}

	hash, err := s.hasher.Hash(password)
	if err != nil {
		return err
	}

	now := s.clock.Now()
	user := domain.User{
		ID:           s.ids.NewID(),
		Email:        email,
		PasswordHash: hash,
		DisplayName:  displayName,
		Status:       domain.UserSignupPending,
	}

	var org *domain.Organization
	if invite != nil {
		user.Status = domain.UserActive
		user.EmailVerified = true
	} else if name := strings.TrimSpace(orgName); name != "" {
		org = &domain.Organization{
			ID:        s.ids.NewID(),
			Name:      name,
			CreatedAt: now,
			UpdatedAt: now,
		}
	}

	created, err := s.signup.CreateSignupUser(ctx, user, org, invite, now)
	if err != nil {
		// The invitation may have been consumed or revoked between the
		// validation above and the transaction. Keep the generic error.
		if errors.Is(err, domain.ErrInviteNotFound) {
			return domain.ErrInvalidInvite
		}
		return err
	}
	if !created {
		// The address already has an account. A pending sign-up is retried so
		// the mailbox owner can replace the squatter's credentials; an active
		// account is a silent no-op. Both return the same successful response.
		return s.retryPendingSignup(ctx, user, invite, locale, now)
	}

	if invite != nil {
		s.logger("signup_invite_accepted",
			"user_id", user.ID, "org_id", invite.OrgID, "invite_id", invite.ID)
		return nil
	}

	ch, code, err := s.newOTPChallenge(ctx, email, domain.ChannelEmail)
	if err != nil {
		return err
	}
	if err := s.notifier.SendSignupVerification(ctx, email, code, int(domain.OTPTTL.Minutes()), locale, user.ID); err != nil {
		return err
	}
	s.logger("signup_registered",
		"user_id", user.ID,
		"identifier_hash", domain.HashIdentifier(email),
		"expires_at", ch.ExpiresAt)
	return nil
}

// retryPendingSignup handles a registration for an address that already
// exists. Only a still-unverified signup_pending account is changed; anything
// else is a silent no-op so the response never reveals whether the address
// exists or has been verified.
func (s *Service) retryPendingSignup(ctx context.Context, user domain.User, invite *domain.Invite, locale string, now time.Time) error {
	if invite != nil {
		userID, reissued, err := s.signup.ReissuePendingSignup(ctx, user, nil, invite, now)
		if err != nil {
			if errors.Is(err, domain.ErrInviteNotFound) {
				return domain.ErrInvalidInvite
			}
			return err
		}
		if reissued {
			s.logger("signup_invite_accepted",
				"user_id", userID, "org_id", invite.OrgID, "invite_id", invite.ID)
		}
		return nil
	}

	// An active/verified account is a silent no-op. Checking first also avoids
	// generating an OTP hash for every registration attempt against it.
	if existing, err := s.users.FindByEmail(ctx, user.Email); err == nil &&
		(existing.Status != domain.UserSignupPending || existing.EmailVerified) {
		return nil
	}

	cooldown, err := s.signupCooldownActive(ctx, user.Email, now)
	if err != nil {
		return err
	}
	if cooldown {
		// Keep the code already in the owner's mailbox valid, but let the new
		// request's password win if that code is verified.
		_, _, err := s.signup.ReissuePendingSignup(ctx, user, nil, nil, now)
		return err
	}

	challenge, code, err := s.buildOTPChallenge(user.Email, domain.ChannelEmail)
	if err != nil {
		return err
	}
	userID, reissued, err := s.signup.ReissuePendingSignup(ctx, user, &challenge, nil, now)
	if err != nil {
		return err
	}
	if !reissued {
		return nil
	}
	if err := s.notifier.SendSignupVerification(ctx, user.Email, code, int(domain.OTPTTL.Minutes()), locale, userID); err != nil {
		return err
	}
	s.logger("signup_verification_resent",
		"user_id", userID,
		"identifier_hash", domain.HashIdentifier(user.Email),
		"expires_at", challenge.ExpiresAt)
	return nil
}

// signupCooldownActive reports whether the address received a verification
// code within the cooldown window. A missing challenge is not a cooldown.
func (s *Service) signupCooldownActive(ctx context.Context, email string, now time.Time) (bool, error) {
	ch, err := s.otp.GetLatestChallenge(ctx, email, domain.ChannelEmail)
	if err != nil {
		return false, nil
	}
	if ch.CreatedAt.IsZero() {
		return false, nil
	}
	return now.Sub(ch.CreatedAt) < signupVerificationCooldown, nil
}

// signupInvite validates an optional invitation token against the sign-up
// address. A supplied but unusable token is the same generic error as any other
// invalid invitation.
func (s *Service) signupInvite(ctx context.Context, token, email string) (*domain.Invite, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, nil
	}
	if s.invites == nil {
		return nil, domain.ErrInvalidInvite
	}
	invite, err := s.invites.FindInviteByTokenHash(ctx, domain.HashInviteToken(token))
	if err != nil {
		if errors.Is(err, domain.ErrInviteNotFound) {
			return nil, domain.ErrInvalidInvite
		}
		return nil, err
	}
	now := s.clock.Now()
	if invite.Status != domain.InvitePending ||
		!now.Before(invite.ExpiresAt) ||
		!strings.EqualFold(invite.Email, email) {
		return nil, domain.ErrInvalidInvite
	}
	return &invite, nil
}

// VerifySignup consumes the sign-up OTP and the pending password, activates
// the account and issues a normal token pair through the same path as login
// (including single-organization auto-select). The code and the password must
// both match; either mismatch is the same generic invalid-code error, and a
// wrong password leaves the challenge unconsumed while counting an attempt.
func (s *Service) VerifySignup(ctx context.Context, email, code, password string) (access, refresh string, expires time.Time, err error) {
	if !s.signupOpen {
		return "", "", time.Time{}, ErrSignupDisabled
	}
	if s.signup == nil || s.users == nil || s.otp == nil {
		return "", "", time.Time{}, fmt.Errorf("sign-up is not configured")
	}

	email = domain.NormalizeEmail(email)
	code = strings.TrimSpace(code)
	if email == "" || code == "" {
		return "", "", time.Time{}, ErrInvalidOTPCode
	}
	ch, err := s.otp.GetLatestChallenge(ctx, email, domain.ChannelEmail)
	if err != nil {
		return "", "", time.Time{}, ErrInvalidOTPCode
	}
	now := s.clock.Now()
	if now.After(ch.ExpiresAt) {
		return "", "", time.Time{}, ErrInvalidOTPCode
	}
	if ch.Attempts >= domain.MaxOTPAttempts {
		return "", "", time.Time{}, ErrTooManyOTPAttempts
	}
	if !s.hasher.Compare(ch.CodeHash, code) {
		_ = s.otp.IncrementAttempts(ctx, ch.ID)
		return "", "", time.Time{}, ErrInvalidOTPCode
	}

	user, err := s.users.FindByEmail(ctx, email)
	if err != nil {
		_ = s.otp.IncrementAttempts(ctx, ch.ID)
		return "", "", time.Time{}, ErrInvalidOTPCode
	}
	if user.Status != domain.UserSignupPending ||
		user.EmailVerified ||
		user.PasswordHash == "" ||
		!s.hasher.Compare(user.PasswordHash, password) {
		_ = s.otp.IncrementAttempts(ctx, ch.ID)
		return "", "", time.Time{}, ErrInvalidOTPCode
	}
	if err := s.otp.ConsumeChallenge(ctx, ch.ID); err != nil {
		return "", "", time.Time{}, err
	}
	if err := s.signup.VerifySignup(ctx, user.ID, now); err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return "", "", time.Time{}, ErrInvalidOTPCode
		}
		return "", "", time.Time{}, err
	}
	return s.issueTokens(ctx, user.ID)
}

// PurgeUnverifiedSignups removes signup_pending accounts older than before.
// It is exposed for an operator or a future scheduler; this service does not
// schedule it.
func (s *Service) PurgeUnverifiedSignups(ctx context.Context, before time.Time) (int64, error) {
	if s.signup == nil {
		return 0, fmt.Errorf("sign-up store is not configured")
	}
	if before.IsZero() {
		return 0, fmt.Errorf("cutoff is required")
	}
	return s.signup.PurgeUnverifiedSignups(ctx, before)
}
