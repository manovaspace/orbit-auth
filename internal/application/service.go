package application

import (
	"context"
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
	Notifier domain.Notifier
	Flags    domain.Flags
	Clock    domain.Clock
	IDs      domain.IDGenerator
	Signer   domain.TokenSigner
	Hasher   domain.Hasher
	Codes    domain.CodeGenerator
	Secrets  domain.SecretGenerator

	// DemoMode enables seeding on start; DemoUsers is the parsed
	// AUTH_DEMO_USERS array. Both are resolved by cmd from the environment.
	DemoMode  bool
	DemoUsers []domain.DemoUser

	Logger func(msg string, args ...any)
}

// Service orchestrates OTP, password, refresh-token and API-token flows.
type Service struct {
	otp       domain.OTPStore
	users     domain.UserStore
	refresh   domain.RefreshTokenStore
	tokens    domain.APITokenStore
	orgs      domain.OrgStore
	notifier  domain.Notifier
	flags     domain.Flags
	clock     domain.Clock
	ids       domain.IDGenerator
	signer    domain.TokenSigner
	hasher    domain.Hasher
	codes     domain.CodeGenerator
	secrets   domain.SecretGenerator
	demoMode  bool
	demoUsers []domain.DemoUser
	logger    func(msg string, args ...any)
}

// NewService builds the service from its ports.
func NewService(deps Deps) *Service {
	logger := deps.Logger
	if logger == nil {
		logger = func(string, ...any) {}
	}
	return &Service{
		otp:       deps.OTP,
		users:     deps.Users,
		refresh:   deps.Refresh,
		tokens:    deps.Tokens,
		orgs:      deps.Orgs,
		notifier:  deps.Notifier,
		flags:     deps.Flags,
		clock:     deps.Clock,
		ids:       deps.IDs,
		signer:    deps.Signer,
		hasher:    deps.Hasher,
		codes:     deps.Codes,
		secrets:   deps.Secrets,
		demoMode:  deps.DemoMode,
		demoUsers: deps.DemoUsers,
		logger:    logger,
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

	_ = s.otp.DeleteExpiredChallenges(ctx)
	code, err := s.codes.NewCode()
	if err != nil {
		return time.Time{}, err
	}
	hash, err := s.hasher.Hash(code)
	if err != nil {
		return time.Time{}, err
	}
	expires := s.clock.Now().Add(domain.OTPTTL)
	ch := domain.OTPChallenge{
		ID:         s.ids.NewID(),
		Identifier: identifier,
		Channel:    channel,
		CodeHash:   hash,
		ExpiresAt:  expires,
	}
	if err := s.otp.UpsertChallenge(ctx, ch); err != nil {
		return time.Time{}, err
	}
	if err := s.notifier.SendOTP(ctx, channel, identifier, code, correlationID); err != nil {
		return time.Time{}, err
	}
	s.logger("otp_requested", "channel", channel, "identifier_hash", domain.HashIdentifier(identifier), "correlation_id", correlationID)
	return expires, nil
}

// VerifyOTP consumes a valid challenge and issues a token pair.
func (s *Service) VerifyOTP(ctx context.Context, identifier, channel, code string) (access, refresh string, expires time.Time, err error) {
	identifier = strings.TrimSpace(identifier)
	channel = strings.TrimSpace(channel)
	if identifier == "" || channel == "" || code == "" {
		return "", "", time.Time{}, fmt.Errorf("invalid or expired code")
	}
	ch, err := s.otp.GetLatestChallenge(ctx, identifier, channel)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("invalid or expired code")
	}
	if s.clock.Now().After(ch.ExpiresAt) {
		return "", "", time.Time{}, fmt.Errorf("invalid or expired code")
	}
	if ch.Attempts >= domain.MaxOTPAttempts {
		return "", "", time.Time{}, fmt.Errorf("too many attempts")
	}
	if !s.hasher.Compare(ch.CodeHash, code) {
		_ = s.otp.IncrementAttempts(ctx, ch.ID)
		return "", "", time.Time{}, fmt.Errorf("invalid or expired code")
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
	return s.issueTokens(ctx, user.ID)
}

// Refresh rotates a valid refresh token.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (access, refresh string, expires time.Time, err error) {
	hash := domain.HashRefreshToken(refreshToken)
	userID, err := s.refresh.FindValid(ctx, hash)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("invalid refresh token")
	}
	_ = s.refresh.Revoke(ctx, hash)
	return s.issueTokens(ctx, userID)
}

func (s *Service) issueTokens(ctx context.Context, userID string) (access, refresh string, expires time.Time, err error) {
	now := s.clock.Now()
	expires = now.Add(domain.AccessTokenTTL)
	access, err = s.signer.SignAccessToken(userID, expires)
	if err != nil {
		return "", "", time.Time{}, err
	}
	refresh = s.ids.NewID() + s.ids.NewID()
	if err := s.refresh.Store(ctx, userID, domain.HashRefreshToken(refresh), now.Add(domain.RefreshTokenTTL)); err != nil {
		return "", "", time.Time{}, err
	}
	return access, refresh, expires, nil
}
