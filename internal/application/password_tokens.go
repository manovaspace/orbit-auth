package application

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

var (
	ErrInvalidCredentials = fmt.Errorf("invalid credentials")
	ErrUserDisabled       = fmt.Errorf("user disabled")
	ErrInvalidToken       = fmt.Errorf("invalid api token")
)

type demoUser struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

func (s *Service) LoginWithPassword(ctx context.Context, email, password string) (access, refresh string, expires time.Time, err error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || password == "" {
		return "", "", time.Time{}, ErrInvalidCredentials
	}
	user, err := s.users.FindByEmail(ctx, email)
	if err != nil {
		return "", "", time.Time{}, ErrInvalidCredentials
	}
	if user.Status == domain.UserDisabled {
		return "", "", time.Time{}, ErrUserDisabled
	}
	if user.PasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return "", "", time.Time{}, ErrInvalidCredentials
	}
	_ = s.users.UpdateLastLogin(ctx, user.ID)
	s.logger("password_login", "user_id", user.ID)
	return s.issueTokens(ctx, user.ID)
}

func (s *Service) CreateApiToken(ctx context.Context, userID, name string, scopes []string, expiresAt *time.Time) (tokenID, prefix, secret string, err error) {
	if userID == "" || strings.TrimSpace(name) == "" {
		return "", "", "", fmt.Errorf("user_id and name required")
	}
	secret, err = generateAPISecret()
	if err != nil {
		return "", "", "", err
	}
	prefix = secret[:8]
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return "", "", "", err
	}
	tokenID = uuid.NewString()
	t := domain.ApiToken{
		ID:        tokenID,
		UserID:    userID,
		Name:      name,
		Prefix:    prefix,
		TokenHash: string(hash),
		Scopes:    scopes,
		ExpiresAt: expiresAt,
	}
	if err := s.tokens.Create(ctx, t); err != nil {
		return "", "", "", err
	}
	return tokenID, prefix, "oat_" + prefix + "." + secret[8:], nil
}

func (s *Service) ListApiTokens(ctx context.Context, userID string) ([]domain.ApiTokenInfo, error) {
	if userID == "" {
		return nil, fmt.Errorf("user_id required")
	}
	return s.tokens.ListByUser(ctx, userID)
}

func (s *Service) RevokeApiToken(ctx context.Context, userID, tokenID string) error {
	if userID == "" || tokenID == "" {
		return fmt.Errorf("user_id and token_id required")
	}
	return s.tokens.RevokeToken(ctx, userID, tokenID)
}

func (s *Service) ValidateApiToken(ctx context.Context, secret string) (userID string, scopes []string, err error) {
	secret = strings.TrimPrefix(strings.TrimSpace(secret), "oat_")
	parts := strings.SplitN(secret, ".", 2)
	if len(parts) != 2 || len(parts[0]) != 8 || parts[1] == "" {
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
	if t.ExpiresAt != nil && time.Now().UTC().After(*t.ExpiresAt) {
		return "", nil, ErrInvalidToken
	}
	if bcrypt.CompareHashAndPassword([]byte(t.TokenHash), []byte(full)) != nil {
		return "", nil, ErrInvalidToken
	}
	return t.UserID, t.Scopes, nil
}

func (s *Service) SeedDemoUsers(ctx context.Context) error {
	if os.Getenv("DEMO_MODE") != "true" {
		return nil
	}
	if os.Getenv("DEPLOYMENT_ENVIRONMENT") != "dev" {
		return fmt.Errorf("DEMO_MODE is only allowed when DEPLOYMENT_ENVIRONMENT=dev")
	}
	raw := os.Getenv("AUTH_DEMO_USERS")
	if raw == "" {
		return fmt.Errorf("AUTH_DEMO_USERS is required when DEMO_MODE=true")
	}
	var users []demoUser
	if err := json.Unmarshal([]byte(raw), &users); err != nil {
		return err
	}
	for _, u := range users {
		hash, err := bcrypt.GenerateFromPassword([]byte(u.Password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		if err := s.users.UpsertDemoUser(ctx, strings.ToLower(strings.TrimSpace(u.Email)), string(hash), u.DisplayName); err != nil {
			return err
		}
	}
	s.logger("demo_users_seeded", "count", len(users))
	return nil
}

func generateAPISecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
