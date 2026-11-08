// Package domain holds the authentication entities, the rules that do not
// need a port, and the ports every other layer depends on. It imports no
// infrastructure package.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"time"
)

// Channels, user statuses and token rules are part of the wire and storage
// contract. Characterization tests pin the durations and hash encodings.
const (
	ChannelEmail = "email"
	ChannelSMS   = "sms"

	UserActive        = "active"
	UserDisabled      = "disabled"
	UserSignupPending = "signup_pending"

	// OTPTTL is how long a requested OTP challenge stays valid.
	OTPTTL = 10 * time.Minute
	// MaxOTPAttempts is the number of wrong codes a challenge tolerates.
	MaxOTPAttempts = 5
	// AccessTokenTTL is the lifetime of a signed access token.
	AccessTokenTTL = 15 * time.Minute
	// RefreshTokenTTL is the lifetime of a stored refresh token.
	RefreshTokenTTL = 30 * 24 * time.Hour
	// APITokenPrefixLength is the number of leading secret characters stored
	// in clear for lookup.
	APITokenPrefixLength = 8
	// APITokenScheme prefixes every API token.
	APITokenScheme = "oat_"
)

var e164Pattern = regexp.MustCompile(`^\+[1-9]\d{6,14}$`)

// IsE164 reports whether value is an E.164 mobile number. The pattern is part
// of the login contract and is pinned by a characterization test.
func IsE164(value string) bool {
	return e164Pattern.MatchString(value)
}

// HashRefreshToken returns the SHA-256 hex digest stored in refresh_tokens.
func HashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// HashIdentifier returns the short SHA-256 digest written to logs instead of a
// raw email address or mobile number.
func HashIdentifier(identifier string) string {
	sum := sha256.Sum256([]byte(identifier))
	return hex.EncodeToString(sum[:8])
}

// User is a product user.
type User struct {
	ID            string
	Email         string
	Mobile        string
	PasswordHash  string
	DisplayName   string
	Status        string
	EmailVerified bool
	// MobileVerified is true when a mobile OTP for Mobile succeeded at some
	// point, or the account was seeded. An account created by accepting a phone
	// invitation with a password starts unverified. Sign-in does not depend on it.
	MobileVerified bool
}

// OTPChallenge is a pending one-time code.
type OTPChallenge struct {
	ID         string
	Identifier string
	Channel    string
	CodeHash   string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	Attempts   int
}

// APIToken is a stored API token. Only the hash of the secret is persisted.
type APIToken struct {
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

// APITokenInfo is the safe listing shape returned to a caller.
type APITokenInfo struct {
	ID        string
	Name      string
	Prefix    string
	Scopes    []string
	ExpiresAt *time.Time
	CreatedAt time.Time
}

// DemoUser is one entry of AUTH_DEMO_USERS, parsed by cmd and seeded by the
// application layer in dev.
type DemoUser struct {
	Email       string `json:"email"`
	Mobile      string `json:"mobile"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}
