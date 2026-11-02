package domain

import (
	"context"
	"time"
)

// OTPStore persists one-time challenges.
type OTPStore interface {
	UpsertChallenge(ctx context.Context, ch OTPChallenge) error
	GetLatestChallenge(ctx context.Context, identifier, channel string) (OTPChallenge, error)
	IncrementAttempts(ctx context.Context, id string) error
	ConsumeChallenge(ctx context.Context, id string) error
	DeleteExpiredChallenges(ctx context.Context) error
}

// UserStore reads and creates users.
type UserStore interface {
	FindOrCreateByEmail(ctx context.Context, email string) (User, error)
	FindOrCreateByMobile(ctx context.Context, mobile string) (User, error)
	FindByEmail(ctx context.Context, email string) (User, error)
	FindByMobile(ctx context.Context, mobile string) (User, error)
	UpdateLastLogin(ctx context.Context, userID string) error
	UpsertDemoUser(ctx context.Context, email, mobile, passwordHash, displayName string) error
}

// RefreshTokenStore persists refresh tokens by their hash.
type RefreshTokenStore interface {
	Store(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error
	FindValid(ctx context.Context, tokenHash string) (userID string, err error)
	Revoke(ctx context.Context, tokenHash string) error
}

// APITokenStore persists API tokens.
type APITokenStore interface {
	Create(ctx context.Context, t APIToken) error
	ListByUser(ctx context.Context, userID string) ([]APITokenInfo, error)
	RevokeToken(ctx context.Context, userID, tokenID string) error
	FindByPrefix(ctx context.Context, prefix string) (APIToken, error)
}

// Notifier sends an OTP through orbit-notifications. The service never talks
// to a mail or SMS provider directly.
type Notifier interface {
	SendOTP(ctx context.Context, channel, recipient, code, correlationID string) error
}

// Flags evaluates boolean feature flags.
type Flags interface {
	Bool(ctx context.Context, flag string, defaultVal bool) bool
}

// Clock supplies the current time.
type Clock interface {
	Now() time.Time
}

// IDGenerator supplies UUID strings for challenges, users, refresh tokens and
// API tokens.
type IDGenerator interface {
	NewID() string
}

// TokenSigner signs an RS256 access token for subject.
type TokenSigner interface {
	SignAccessToken(subject string, expires time.Time) (string, error)
}

// Hasher hashes and verifies passwords and API-token secrets. The production
// adapter is bcrypt at bcrypt.DefaultCost.
type Hasher interface {
	Hash(secret string) (string, error)
	Compare(hash, secret string) bool
}

// CodeGenerator generates a numeric OTP code.
type CodeGenerator interface {
	NewCode() (string, error)
}

// SecretGenerator generates an opaque API-token secret.
type SecretGenerator interface {
	NewSecret() (string, error)
}
