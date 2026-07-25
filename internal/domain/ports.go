package domain

import (
	"context"
	"time"
)

const (
	ChannelEmail = "email"
	ChannelSMS   = "sms"
	UserActive   = "active"
	UserDisabled = "disabled"
)

type User struct {
	ID           string
	Email        string
	Mobile       string
	PasswordHash string
	DisplayName  string
	Status       string
}

type OTPChallenge struct {
	ID         string
	Identifier string
	Channel    string
	CodeHash   string
	ExpiresAt  time.Time
	Attempts   int
}

type ApiToken struct {
	ID        string
	UserID    string
	Name      string
	Prefix    string
	TokenHash string
	Scopes    []string
	ExpiresAt *time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}

type ApiTokenInfo struct {
	ID        string
	Name      string
	Prefix    string
	Scopes    []string
	ExpiresAt *time.Time
	CreatedAt time.Time
}

type OTPRepository interface {
	UpsertChallenge(ctx context.Context, ch OTPChallenge) error
	GetLatestChallenge(ctx context.Context, identifier, channel string) (OTPChallenge, error)
	IncrementAttempts(ctx context.Context, id string) error
	ConsumeChallenge(ctx context.Context, id string) error
	DeleteExpiredChallenges(ctx context.Context) error
}

type UserRepository interface {
	FindOrCreateByEmail(ctx context.Context, email string) (User, error)
	FindOrCreateByMobile(ctx context.Context, mobile string) (User, error)
	FindByEmail(ctx context.Context, email string) (User, error)
	UpdateLastLogin(ctx context.Context, userID string) error
	UpsertDemoUser(ctx context.Context, email, passwordHash, displayName string) error
}

type RefreshTokenRepository interface {
	Store(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error
	FindValid(ctx context.Context, tokenHash string) (userID string, err error)
	Revoke(ctx context.Context, tokenHash string) error
}

type ApiTokenRepository interface {
	Create(ctx context.Context, t ApiToken) error
	ListByUser(ctx context.Context, userID string) ([]ApiTokenInfo, error)
	RevokeToken(ctx context.Context, userID, tokenID string) error
	FindByPrefix(ctx context.Context, prefix string) (ApiToken, error)
}

type Notifier interface {
	SendOTP(ctx context.Context, channel, recipient, code, correlationID string) error
}
