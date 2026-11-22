package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// requireActiveUser returns domain.ErrAccountNotActive unless userID names an
// active account. A disabled account and a self-serve account that has not been
// verified are refused on every token and membership path.
func (s *Service) requireActiveUser(ctx context.Context, userID string) error {
	if s.users == nil {
		return fmt.Errorf("user store is not configured")
	}
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return err
	}
	if user.Status != domain.UserActive {
		return domain.ErrAccountNotActive
	}
	return nil
}

// generic replaces ErrAccountNotActive with the error the caller returns for a
// bad credential, so a disabled account is indistinguishable from a wrong one.
// Other errors pass through unchanged.
func generic(err, replacement error) error {
	if errors.Is(err, domain.ErrAccountNotActive) {
		return replacement
	}
	return err
}

// dummyPasswordHash is compared on every password login that finds no usable
// hash, so an unknown account costs the same as a wrong password. A failure to
// hash yields an empty hash, which still refuses the login.
func dummyPasswordHash(h domain.Hasher) string {
	if h == nil {
		return ""
	}
	hash, err := h.Hash("manova-login-dummy-password")
	if err != nil {
		return ""
	}
	return hash
}
