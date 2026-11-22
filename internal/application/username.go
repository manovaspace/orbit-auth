package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// SetUsername stores username for userID and returns its normalized form. An
// invalid name is domain.ErrInvalidUsername, a name another account holds is
// domain.ErrUsernameTaken, and an unknown user is domain.ErrUserNotFound. The
// username is never logged.
func (s *Service) SetUsername(ctx context.Context, userID, username string) (string, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "", fmt.Errorf("user_id required")
	}
	normalized, ok := domain.NormalizeUsername(username)
	if !ok {
		return "", domain.ErrInvalidUsername
	}
	if err := s.users.SetUsername(ctx, userID, normalized); err != nil {
		return "", err
	}
	s.logger("username_set", "user_id", userID)
	return normalized, nil
}
