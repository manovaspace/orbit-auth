package application

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// activeUser returns the account userID when it is active. A missing account is
// domain.ErrUserNotFound and any other state is domain.ErrAccountNotActive.
func (s *Service) activeUser(ctx context.Context, userID string) (domain.User, error) {
	if userID == "" {
		return domain.User{}, domain.ErrUserNotFound
	}
	if s.users == nil {
		return domain.User{}, fmt.Errorf("user store is not configured")
	}
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return domain.User{}, err
	}
	if user.Status != domain.UserActive {
		return domain.User{}, domain.ErrAccountNotActive
	}
	return user, nil
}

// GetSignInMethods returns how the active account userID signs in: whether it
// has a password, its identifiers and the linked Google identity. It never
// returns a password hash, a token or a Google subject.
func (s *Service) GetSignInMethods(ctx context.Context, userID string) (domain.SignInMethods, error) {
	userID = strings.TrimSpace(userID)
	user, err := s.activeUser(ctx, userID)
	if err != nil {
		return domain.SignInMethods{}, err
	}
	if s.account == nil {
		return domain.SignInMethods{}, fmt.Errorf("sign-in method store is not configured")
	}
	methods := domain.SignInMethods{
		HasPassword:    user.PasswordHash != "",
		Username:       user.Username,
		Email:          user.Email,
		EmailVerified:  user.EmailVerified,
		Mobile:         user.Mobile,
		MobileVerified: user.MobileVerified,
	}
	link, found, err := s.account.GoogleLinkForUser(ctx, domain.GoogleProviderName, userID)
	if err != nil {
		return domain.SignInMethods{}, err
	}
	if found {
		methods.Google = &link
	}
	return methods, nil
}

// SetPassword sets the password of the active account userID. When the account
// has a password, currentPassword must match it: a wrong one is
// ErrInvalidCredentials, and the comparison runs whether or not it matches. When
// it has none, currentPassword is ignored. newPassword follows the invite
// sign-up rule (at least eight characters after trimming), and the account must
// have a verified email, a mobile number or a username to sign in with
// (ErrNoPasswordIdentifier). refreshToken is the session the caller uses: it
// must be a live token of this account and it stays valid; every other live
// refresh token of the account is revoked.
func (s *Service) SetPassword(ctx context.Context, userID, currentPassword, newPassword, refreshToken string) error {
	userID = strings.TrimSpace(userID)
	user, err := s.activeUser(ctx, userID)
	if err != nil {
		return err
	}
	if s.account == nil || s.hasher == nil || s.refresh == nil {
		return fmt.Errorf("password store is not configured")
	}
	if user.PasswordHash != "" && !s.hasher.Compare(user.PasswordHash, currentPassword) {
		return ErrInvalidCredentials
	}
	trimmed := strings.TrimSpace(newPassword)
	if trimmed == "" {
		return ErrPasswordRequired
	}
	if utf8.RuneCountInString(trimmed) < minInvitePasswordRunes {
		return ErrPasswordTooShort
	}
	if !domain.PasswordIdentifierUsable(user) {
		return domain.ErrNoPasswordIdentifier
	}
	keepHash, err := s.presentedSession(ctx, userID, refreshToken)
	if err != nil {
		return err
	}
	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return err
	}
	if err := s.account.SetPassword(ctx, userID, hash, keepHash, user.PasswordHash == "", domain.UserAuditActor(userID), s.clock.Now()); err != nil {
		return err
	}
	s.logger("password_set", "user_id", userID)
	return nil
}

// UnlinkGoogle removes the Google identity of the active account userID. The
// account must keep a password that can sign it in, otherwise the call is
// domain.ErrLastSignInMethod.
func (s *Service) UnlinkGoogle(ctx context.Context, userID string) error {
	userID = strings.TrimSpace(userID)
	if _, err := s.activeUser(ctx, userID); err != nil {
		return err
	}
	if s.account == nil {
		return fmt.Errorf("sign-in method store is not configured")
	}
	if err := s.account.UnlinkGoogleIdentity(ctx, domain.GoogleProviderName, userID, domain.UserAuditActor(userID), s.clock.Now()); err != nil {
		return err
	}
	s.logger("google_unlinked", "user_id", userID)
	return nil
}

// RevokeAllSessions revokes every live refresh token of the active account
// userID. A presented refreshToken is kept, and it must be a live token of this
// account; otherwise the call is ErrInvalidRefresh and nothing is revoked.
func (s *Service) RevokeAllSessions(ctx context.Context, userID, refreshToken string) error {
	userID = strings.TrimSpace(userID)
	if _, err := s.activeUser(ctx, userID); err != nil {
		return err
	}
	if s.account == nil || s.refresh == nil {
		return fmt.Errorf("session store is not configured")
	}
	keepHash, err := s.presentedSession(ctx, userID, refreshToken)
	if err != nil {
		return err
	}
	return s.account.RevokeSessions(ctx, userID, keepHash, domain.UserAuditActor(userID), s.clock.Now())
}

// presentedSession returns the hash of a presented refresh token when it is a
// live token of userID, or "" when none is presented. A presented token that is
// not a live token of userID is ErrInvalidRefresh.
func (s *Service) presentedSession(ctx context.Context, userID, refreshToken string) (string, error) {
	token := strings.TrimSpace(refreshToken)
	if token == "" {
		return "", nil
	}
	hash := domain.HashRefreshToken(token)
	tokenUserID, _, err := s.refresh.FindValid(ctx, hash)
	if err != nil || tokenUserID != userID {
		return "", ErrInvalidRefresh
	}
	return hash, nil
}
