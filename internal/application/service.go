package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

const otpTTL = 10 * time.Minute

var e164Pattern = regexp.MustCompile(`^\+[1-9]\d{6,14}$`)

// FlagEvaluator checks feature flags; nil-safe via Bool on concrete implementation.
type FlagEvaluator interface {
	Bool(ctx context.Context, flag string, defaultVal bool) bool
}

type Service struct {
	otp       domain.OTPRepository
	users     domain.UserRepository
	refresh   domain.RefreshTokenRepository
	tokens    domain.ApiTokenRepository
	notifier  domain.Notifier
	flags     FlagEvaluator
	jwtSecret []byte
	logger    func(msg string, args ...any)
}

func NewService(
	otp domain.OTPRepository,
	users domain.UserRepository,
	refresh domain.RefreshTokenRepository,
	tokens domain.ApiTokenRepository,
	notifier domain.Notifier,
	flags FlagEvaluator,
	logger func(string, ...any),
) (*Service, error) {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		if os.Getenv("DEPLOYMENT_ENVIRONMENT") != "dev" {
			return nil, fmt.Errorf("JWT_SECRET is required outside DEPLOYMENT_ENVIRONMENT=dev")
		}
		secret = "dev-insecure-change-me"
	}
	return &Service{
		otp:       otp,
		users:     users,
		refresh:   refresh,
		tokens:    tokens,
		notifier:  notifier,
		flags:     flags,
		jwtSecret: []byte(secret),
		logger:    logger,
	}, nil
}

func (s *Service) emailOTPEnabled(ctx context.Context) bool {
	if s.flags == nil {
		return false
	}
	return s.flags.Bool(ctx, "manova.auth.email_otp", false)
}

func (s *Service) mobileOTPEnabled(ctx context.Context) bool {
	if s.flags == nil {
		return false
	}
	return s.flags.Bool(ctx, "manova.auth.mobile_otp", false)
}

func (s *Service) RequestOTP(ctx context.Context, identifier, channel, correlationID string) (time.Time, error) {
	identifier = strings.TrimSpace(identifier)
	channel = strings.TrimSpace(channel)
	if identifier == "" || channel == "" {
		return time.Time{}, fmt.Errorf("identifier and channel required")
	}
	switch channel {
	case domain.ChannelEmail:
		if !s.emailOTPEnabled(ctx) {
			return time.Time{}, ErrEmailOTPDisabled
		}
	case domain.ChannelSMS:
		if !e164Pattern.MatchString(identifier) {
			return time.Time{}, fmt.Errorf("mobile must be E.164")
		}
		if !s.mobileOTPEnabled(ctx) {
			return time.Time{}, ErrMobileOTPDisabled
		}
	default:
		return time.Time{}, fmt.Errorf("unsupported channel %q", channel)
	}

	_ = s.otp.DeleteExpiredChallenges(ctx)
	code, err := generateOTPCode()
	if err != nil {
		return time.Time{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
	if err != nil {
		return time.Time{}, err
	}
	expires := time.Now().UTC().Add(otpTTL)
	ch := domain.OTPChallenge{
		ID:         uuid.NewString(),
		Identifier: identifier,
		Channel:    channel,
		CodeHash:   string(hash),
		ExpiresAt:  expires,
	}
	if err := s.otp.UpsertChallenge(ctx, ch); err != nil {
		return time.Time{}, err
	}
	if err := s.notifier.SendOTP(ctx, channel, identifier, code, correlationID); err != nil {
		return time.Time{}, err
	}
	s.logger("otp_requested", "channel", channel, "identifier_hash", hashIdentifier(identifier), "correlation_id", correlationID)
	return expires, nil
}

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
	if time.Now().UTC().After(ch.ExpiresAt) {
		return "", "", time.Time{}, fmt.Errorf("invalid or expired code")
	}
	if ch.Attempts >= 5 {
		return "", "", time.Time{}, fmt.Errorf("too many attempts")
	}
	if bcrypt.CompareHashAndPassword([]byte(ch.CodeHash), []byte(code)) != nil {
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

func (s *Service) Refresh(ctx context.Context, refreshToken string) (access, refresh string, expires time.Time, err error) {
	userID, err := s.refresh.FindValid(ctx, hashToken(refreshToken))
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("invalid refresh token")
	}
	_ = s.refresh.Revoke(ctx, hashToken(refreshToken))
	return s.issueTokens(ctx, userID)
}

func (s *Service) issueTokens(ctx context.Context, userID string) (access, refresh string, expires time.Time, err error) {
	expires = time.Now().UTC().Add(15 * time.Minute)
	access, err = jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"exp": expires.Unix(),
		"iat": time.Now().Unix(),
	}).SignedString(s.jwtSecret)
	if err != nil {
		return "", "", time.Time{}, err
	}
	refresh = uuid.NewString() + uuid.NewString()
	refreshExp := time.Now().UTC().Add(30 * 24 * time.Hour)
	if err := s.refresh.Store(ctx, userID, hashToken(refresh), refreshExp); err != nil {
		return "", "", time.Time{}, err
	}
	return access, refresh, expires, nil
}

func generateOTPCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func hashIdentifier(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:8])
}
