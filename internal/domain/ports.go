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
	FindByID(ctx context.Context, userID string) (User, error)
	UpdateLastLogin(ctx context.Context, userID string) error
	UpsertDemoUser(ctx context.Context, email, mobile, passwordHash, displayName string) error
}

// RefreshTokenStore persists refresh tokens by their hash. A stored token
// remembers the organization selected when the pair was issued; orgID is empty
// when no organization was active.
type RefreshTokenStore interface {
	Store(ctx context.Context, userID, orgID, tokenHash string, expiresAt time.Time) error
	FindValid(ctx context.Context, tokenHash string) (userID, orgID string, err error)
	Revoke(ctx context.Context, tokenHash string) error
}

// APITokenStore persists API tokens.
type APITokenStore interface {
	Create(ctx context.Context, t APIToken) error
	ListByUser(ctx context.Context, userID string) ([]APITokenInfo, error)
	RevokeToken(ctx context.Context, userID, tokenID string) error
	FindByPrefix(ctx context.Context, prefix string) (APIToken, error)
}

// OrgStore persists organizations and their memberships. Every membership
// operation is scoped by both organization and user.
type OrgStore interface {
	// CreateOrg writes the organization and the creator's active membership in
	// the same transaction.
	CreateOrg(ctx context.Context, org Organization, creatorID string) error
	GetOrg(ctx context.Context, orgID string) (Organization, error)
	ListOrgsForUser(ctx context.Context, userID string) ([]Organization, error)
	// ListMembers returns every membership of orgID with its lifecycle status.
	ListMembers(ctx context.Context, orgID string) ([]Membership, error)
	AddMember(ctx context.Context, orgID, userID string, status MembershipStatus) error
	UpdateMemberStatus(ctx context.Context, orgID, userID string, status MembershipStatus) error
	RemoveMember(ctx context.Context, orgID, userID string) error
	GetMembership(ctx context.Context, orgID, userID string) (Membership, error)
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

// TokenSigner signs an RS256 access token for subject, optionally carrying an
// active organization claim.
type TokenSigner interface {
	SignAccessToken(subject string, expires time.Time) (string, error)
	SignAccessTokenWithOrg(subject, orgID string, expires time.Time) (string, error)
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
