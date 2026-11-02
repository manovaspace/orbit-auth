package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

var (
	ErrInvalidCredentials = fmt.Errorf("invalid credentials")
	ErrUserDisabled       = fmt.Errorf("user disabled")
	ErrInvalidToken       = fmt.Errorf("invalid api token")
	ErrInvalidRefresh     = fmt.Errorf("invalid refresh token")
)

// LoginWithPassword authenticates by email or E.164 mobile number plus password.
func (s *Service) LoginWithPassword(ctx context.Context, identifier, password string) (access, refresh string, expires time.Time, err error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" || password == "" {
		return "", "", time.Time{}, ErrInvalidCredentials
	}
	var user domain.User
	if domain.IsE164(identifier) {
		user, err = s.users.FindByMobile(ctx, identifier)
	} else {
		user, err = s.users.FindByEmail(ctx, strings.ToLower(identifier))
	}
	if err != nil {
		return "", "", time.Time{}, ErrInvalidCredentials
	}
	if user.Status == domain.UserDisabled {
		return "", "", time.Time{}, ErrUserDisabled
	}
	if user.PasswordHash == "" || !s.hasher.Compare(user.PasswordHash, password) {
		return "", "", time.Time{}, ErrInvalidCredentials
	}
	_ = s.users.UpdateLastLogin(ctx, user.ID)
	s.logger("password_login", "user_id", user.ID)
	return s.issueTokens(ctx, user.ID)
}

// CreateApiToken returns the plaintext token exactly once.
func (s *Service) CreateApiToken(ctx context.Context, userID, name string, scopes []string, expiresAt *time.Time) (tokenID, prefix, secret string, err error) {
	if userID == "" || strings.TrimSpace(name) == "" {
		return "", "", "", fmt.Errorf("user_id and name required")
	}
	raw, err := s.secrets.NewSecret()
	if err != nil {
		return "", "", "", err
	}
	prefix = raw[:domain.APITokenPrefixLength]
	hash, err := s.hasher.Hash(raw)
	if err != nil {
		return "", "", "", err
	}
	tokenID = s.ids.NewID()
	t := domain.APIToken{
		ID:        tokenID,
		UserID:    userID,
		Name:      name,
		Prefix:    prefix,
		TokenHash: hash,
		Scopes:    scopes,
		ExpiresAt: expiresAt,
	}
	if err := s.tokens.Create(ctx, t); err != nil {
		return "", "", "", err
	}
	return tokenID, prefix, domain.APITokenScheme + prefix + "." + raw[domain.APITokenPrefixLength:], nil
}

// ListApiTokens lists a user's active tokens.
func (s *Service) ListApiTokens(ctx context.Context, userID string) ([]domain.APITokenInfo, error) {
	if userID == "" {
		return nil, fmt.Errorf("user_id required")
	}
	return s.tokens.ListByUser(ctx, userID)
}

// RevokeApiToken revokes one token owned by userID.
func (s *Service) RevokeApiToken(ctx context.Context, userID, tokenID string) error {
	if userID == "" || tokenID == "" {
		return fmt.Errorf("user_id and token_id required")
	}
	return s.tokens.RevokeToken(ctx, userID, tokenID)
}

// ValidateApiToken verifies the documented oat_<prefix>.<secret> format and
// returns the owning user and scopes.
func (s *Service) ValidateApiToken(ctx context.Context, secret string) (userID string, scopes []string, err error) {
	secret = strings.TrimPrefix(strings.TrimSpace(secret), domain.APITokenScheme)
	parts := strings.SplitN(secret, ".", 2)
	if len(parts) != 2 || len(parts[0]) != domain.APITokenPrefixLength || parts[1] == "" {
		return "", nil, ErrInvalidToken
	}
	prefix := parts[0]
	full := prefix + parts[1]
	t, err := s.tokens.FindByPrefix(ctx, prefix)
	if err != nil {
		return "", nil, ErrInvalidToken
	}
	if t.RevokedAt != nil {
		return "", nil, ErrInvalidToken
	}
	if t.ExpiresAt != nil && s.clock.Now().After(*t.ExpiresAt) {
		return "", nil, ErrInvalidToken
	}
	if !s.hasher.Compare(t.TokenHash, full) {
		return "", nil, ErrInvalidToken
	}
	return t.UserID, t.Scopes, nil
}

// SeedDemoUsers seeds AUTH_DEMO_USERS. cmd passes the parsed array only when
// DEMO_MODE=true and DEPLOYMENT_ENVIRONMENT=dev.
func (s *Service) SeedDemoUsers(ctx context.Context) error {
	if !s.demoMode {
		return nil
	}
	for _, u := range s.demoUsers {
		hash, err := s.hasher.Hash(u.Password)
		if err != nil {
			return err
		}
		email := strings.ToLower(strings.TrimSpace(u.Email))
		mobile := strings.TrimSpace(u.Mobile)
		if email == "" && mobile == "" {
			return fmt.Errorf("demo user requires email or mobile")
		}
		if mobile != "" && !domain.IsE164(mobile) {
			return fmt.Errorf("demo user mobile must be E.164")
		}
		if err := s.users.UpsertDemoUser(ctx, email, mobile, hash, u.DisplayName); err != nil {
			return err
		}
	}
	s.logger("demo_users_seeded", "count", len(s.demoUsers))
	return nil
}
