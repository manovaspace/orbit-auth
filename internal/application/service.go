package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// Deps are the application service's ports and runtime configuration. Every
// dependency is injected; the application layer reads no environment variable
// and imports no infrastructure package.
type Deps struct {
	OTP      domain.OTPStore
	Users    domain.UserStore
	Refresh  domain.RefreshTokenStore
	Tokens   domain.APITokenStore
	Orgs     domain.OrgStore
	Invites  domain.InviteStore
	Signup   domain.SignupStore
	Notifier domain.Notifier
	Flags    domain.Flags
	Clock    domain.Clock
	IDs      domain.IDGenerator
	Signer   domain.TokenSigner
	Hasher   domain.Hasher
	Codes    domain.CodeGenerator
	Secrets  domain.SecretGenerator

	// SignupOpen is AUTH_SIGNUP=open. The default is false, so deployments
	// that do not opt in keep self-serve registration disabled.
	SignupOpen bool

	// InviteSignupDisabled is AUTH_INVITE_SIGNUP=off or false. Invite sign-up
	// is on by default and does not depend on SignupOpen: it needs a token that
	// an owner or admin issued, not self-serve registration. The field is the
	// disabled flag so the zero value keeps invite sign-up on.
	InviteSignupDisabled bool

	// DemoMode enables seeding on start; DemoUsers is the parsed
	// AUTH_DEMO_USERS array. Both are resolved by cmd from the environment.
	DemoMode  bool
	DemoUsers []domain.DemoUser

	// AutoSelectSingleOrg is AUTH_AUTO_SELECT_SINGLE_ORG. When true, a login
	// for a user with exactly one active membership carries that organization.
	AutoSelectSingleOrg bool

	Logger func(msg string, args ...any)
}

// Service orchestrates OTP, password, refresh-token and API-token flows.
type Service struct {
	otp                 domain.OTPStore
	users               domain.UserStore
	refresh             domain.RefreshTokenStore
	tokens              domain.APITokenStore
	orgs                domain.OrgStore
	invites             domain.InviteStore
	signup              domain.SignupStore
	notifier            domain.Notifier
	flags               domain.Flags
	clock               domain.Clock
	ids                 domain.IDGenerator
	signer              domain.TokenSigner
	hasher              domain.Hasher
	codes               domain.CodeGenerator
	secrets             domain.SecretGenerator
	signupOpen          bool
	inviteSignupOff     bool
	demoMode            bool
	demoUsers           []domain.DemoUser
	autoSelectSingleOrg bool
	logger              func(msg string, args ...any)
}

// NewService builds the service from its ports.
func NewService(deps Deps) *Service {
	logger := deps.Logger
	if logger == nil {
		logger = func(string, ...any) {}
	}
	return &Service{
		otp:                 deps.OTP,
		users:               deps.Users,
		refresh:             deps.Refresh,
		tokens:              deps.Tokens,
		orgs:                deps.Orgs,
		invites:             deps.Invites,
		signup:              deps.Signup,
		notifier:            deps.Notifier,
		flags:               deps.Flags,
		clock:               deps.Clock,
		ids:                 deps.IDs,
		signer:              deps.Signer,
		hasher:              deps.Hasher,
		codes:               deps.Codes,
		secrets:             deps.Secrets,
		signupOpen:          deps.SignupOpen,
		inviteSignupOff:     deps.InviteSignupDisabled,
		demoMode:            deps.DemoMode,
		demoUsers:           deps.DemoUsers,
		autoSelectSingleOrg: deps.AutoSelectSingleOrg,
		logger:              logger,
	}
}

func (s *Service) flag(ctx context.Context, name string) bool {
	if s.flags == nil {
		return false
	}
	return s.flags.Bool(ctx, name, false)
}

// RequestOTP creates a one-time challenge and asks orbit-notifications to
// deliver it.
func (s *Service) RequestOTP(ctx context.Context, identifier, channel, correlationID string) (time.Time, error) {
	identifier = strings.TrimSpace(identifier)
	channel = strings.TrimSpace(channel)
	if identifier == "" || channel == "" {
		return time.Time{}, fmt.Errorf("identifier and channel required")
	}
	switch channel {
	case domain.ChannelEmail:
		if !s.flag(ctx, "manova.auth.email_otp") {
			return time.Time{}, ErrEmailOTPDisabled
		}
	case domain.ChannelSMS:
		if !domain.IsE164(identifier) {
			return time.Time{}, fmt.Errorf("mobile must be E.164")
		}
		if !s.flag(ctx, "manova.auth.mobile_otp") {
			return time.Time{}, ErrMobileOTPDisabled
		}
	default:
		return time.Time{}, fmt.Errorf("unsupported channel %q", channel)
	}

	ch, code, err := s.newOTPChallenge(ctx, identifier, channel)
	if err != nil {
		return time.Time{}, err
	}
	if err := s.notifier.SendOTP(ctx, channel, identifier, code, correlationID); err != nil {
		return time.Time{}, err
	}
	s.logger("otp_requested", "channel", channel, "identifier_hash", domain.HashIdentifier(identifier), "correlation_id", correlationID)
	return ch.ExpiresAt, nil
}

// newOTPChallenge creates and stores one challenge. RequestOTP and sign-up
// registration share it so both use the pinned TTL, code format and attempt
// limits.
func (s *Service) newOTPChallenge(ctx context.Context, identifier, channel string) (domain.OTPChallenge, string, error) {
	_ = s.otp.DeleteExpiredChallenges(ctx)
	ch, code, err := s.buildOTPChallenge(identifier, channel)
	if err != nil {
		return domain.OTPChallenge{}, "", err
	}
	if err := s.otp.UpsertChallenge(ctx, ch); err != nil {
		return domain.OTPChallenge{}, "", err
	}
	return ch, code, nil
}

// buildOTPChallenge creates the challenge and code without writing them. The
// sign-up retry path writes the challenge through the signup store so the
// credential update and challenge rotation share one transaction.
func (s *Service) buildOTPChallenge(identifier, channel string) (domain.OTPChallenge, string, error) {
	code, err := s.codes.NewCode()
	if err != nil {
		return domain.OTPChallenge{}, "", err
	}
	hash, err := s.hasher.Hash(code)
	if err != nil {
		return domain.OTPChallenge{}, "", err
	}
	now := s.clock.Now()
	return domain.OTPChallenge{
		ID:         s.ids.NewID(),
		Identifier: identifier,
		Channel:    channel,
		CodeHash:   hash,
		ExpiresAt:  now.Add(domain.OTPTTL),
		CreatedAt:  now,
	}, code, nil
}

// VerifyOTP consumes a valid challenge and issues a token pair.
func (s *Service) VerifyOTP(ctx context.Context, identifier, channel, code string) (access, refresh string, expires time.Time, err error) {
	identifier = strings.TrimSpace(identifier)
	channel = strings.TrimSpace(channel)
	if identifier == "" || channel == "" || code == "" {
		return "", "", time.Time{}, ErrInvalidOTPCode
	}
	ch, err := s.otp.GetLatestChallenge(ctx, identifier, channel)
	if err != nil {
		return "", "", time.Time{}, ErrInvalidOTPCode
	}
	if s.clock.Now().After(ch.ExpiresAt) {
		return "", "", time.Time{}, ErrInvalidOTPCode
	}
	if ch.Attempts >= domain.MaxOTPAttempts {
		return "", "", time.Time{}, ErrTooManyOTPAttempts
	}
	if !s.hasher.Compare(ch.CodeHash, code) {
		_ = s.otp.IncrementAttempts(ctx, ch.ID)
		return "", "", time.Time{}, ErrInvalidOTPCode
	}
	if err := s.otp.ConsumeChallenge(ctx, ch.ID); err != nil {
		return "", "", time.Time{}, err
	}
	var user domain.User
	switch channel {
	case domain.ChannelEmail:
		user, err = s.users.FindOrCreateByEmail(ctx, identifier)
	case domain.ChannelSMS:
		user, err = s.users.FindOrCreateByMobile(ctx, identifier)
	default:
		return "", "", time.Time{}, fmt.Errorf("unsupported channel")
	}
	if err != nil {
		return "", "", time.Time{}, err
	}
	// A self-serve account stays unusable until VerifySignup activates it.
	// OTP login must not be a shortcut around that verification.
	if user.Status == domain.UserSignupPending {
		return "", "", time.Time{}, ErrInvalidOTPCode
	}
	if channel == domain.ChannelSMS {
		// The successful code is the proof that the number belongs to the
		// person. Sign-in does not depend on this record.
		if err := s.users.MarkMobileVerified(ctx, user.ID, s.clock.Now()); err != nil {
			return "", "", time.Time{}, err
		}
	}
	return s.issueTokens(ctx, user.ID)
}

// Refresh rotates a valid refresh token. The new pair keeps the remembered
// organization while the user is still an active member; if the membership was
// suspended or removed, the pair is issued without an org claim instead of
// failing the refresh.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (access, refresh string, expires time.Time, err error) {
	hash := domain.HashRefreshToken(refreshToken)
	userID, orgID, err := s.refresh.Consume(ctx, hash)
	if err != nil {
		return "", "", time.Time{}, ErrInvalidRefresh
	}
	return s.issueTokensForOrg(ctx, userID, s.activeOrg(ctx, userID, orgID))
}

// SelectOrg rotates the presented refresh token and issues a new pair carrying
// orgID. The caller must still be an active member of orgID, and the refresh
// token must belong to the caller, so a caller can neither switch another
// user's session nor select an organization they cannot access.
func (s *Service) SelectOrg(ctx context.Context, userID, refreshToken, orgID string) (access, refresh string, expires time.Time, err error) {
	userID = strings.TrimSpace(userID)
	orgID = strings.TrimSpace(orgID)
	if userID == "" || orgID == "" {
		return "", "", time.Time{}, ErrInvalidRefresh
	}
	if s.orgs == nil {
		return "", "", time.Time{}, domain.ErrNotMember
	}
	active, err := s.IsActiveMember(ctx, orgID, userID)
	if err != nil {
		return "", "", time.Time{}, err
	}
	if !active {
		return "", "", time.Time{}, domain.ErrNotMember
	}
	hash := domain.HashRefreshToken(refreshToken)
	tokenUserID, _, err := s.refresh.FindValid(ctx, hash)
	if err != nil || tokenUserID != userID {
		return "", "", time.Time{}, ErrInvalidRefresh
	}
	// Consume, not Revoke: a concurrent redemption that wins the race must
	// make this one fail.
	if _, _, err := s.refresh.Consume(ctx, hash); err != nil {
		return "", "", time.Time{}, ErrInvalidRefresh
	}
	return s.issueTokensForOrg(ctx, userID, orgID)
}

// RevokeRefresh revokes the presented refresh token so it can no longer be
// redeemed. It is idempotent: an empty, unknown, revoked or expired token
// returns nil, so logout does not reveal whether the token was valid. Any
// other store error is returned.
func (s *Service) RevokeRefresh(ctx context.Context, refreshToken string) error {
	if strings.TrimSpace(refreshToken) == "" {
		return nil
	}
	_, _, err := s.refresh.Consume(ctx, domain.HashRefreshToken(refreshToken))
	if err != nil && !errors.Is(err, domain.ErrRefreshTokenNotFound) {
		return err
	}
	return nil
}

// issueTokens issues a pair for a login, applying single-organization
// auto-select when enabled.
func (s *Service) issueTokens(ctx context.Context, userID string) (access, refresh string, expires time.Time, err error) {
	orgID := ""
	if s.autoSelectSingleOrg {
		orgID = s.singleActiveOrg(ctx, userID)
	}
	return s.issueTokensForOrg(ctx, userID, orgID)
}

// singleActiveOrg returns the only active organization of userID, or an empty
// string for zero or many memberships. A store failure is treated as no
// organization so an optional convenience claim never blocks login.
func (s *Service) singleActiveOrg(ctx context.Context, userID string) string {
	if s.orgs == nil {
		return ""
	}
	orgs, err := s.orgs.ListOrgsForUser(ctx, userID)
	if err != nil || len(orgs) != 1 {
		return ""
	}
	return orgs[0].ID
}

// activeOrg keeps orgID only while the user still has an active membership.
func (s *Service) activeOrg(ctx context.Context, userID, orgID string) string {
	if orgID == "" || s.orgs == nil {
		return ""
	}
	active, err := s.IsActiveMember(ctx, orgID, userID)
	if err != nil || !active {
		return ""
	}
	return orgID
}

func (s *Service) issueTokensForOrg(ctx context.Context, userID, orgID string) (access, refresh string, expires time.Time, err error) {
	now := s.clock.Now()
	expires = now.Add(domain.AccessTokenTTL)
	if orgID == "" {
		access, err = s.signer.SignAccessToken(userID, expires)
	} else {
		access, err = s.signer.SignAccessTokenWithOrg(userID, orgID, expires)
	}
	if err != nil {
		return "", "", time.Time{}, err
	}
	refresh = s.ids.NewID() + s.ids.NewID()
	if err := s.refresh.Store(ctx, userID, orgID, domain.HashRefreshToken(refresh), now.Add(domain.RefreshTokenTTL)); err != nil {
		return "", "", time.Time{}, err
	}
	return access, refresh, expires, nil
}
