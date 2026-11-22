package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// minInvitePasswordRunes is the minimum password length, in Unicode characters
// after trimming, for invite sign-up.
const minInvitePasswordRunes = 8

// defaultInviteLocale is the locale an organization invitation uses when the
// caller supplies none. orbit-notifications renders the org_invite template in
// Persian by default.
const defaultInviteLocale = "fa"

// The channel an organization invitation was delivered on, logged with
// organization_invite_created. Phone invitations send nothing yet.
const (
	inviteChannelEmail = "email"
	inviteChannelPhone = "phone"
)

// InviteMember creates a pending invitation to exactly one target, an email
// address or an E.164 phone number, with the organization tier role. The
// inviter must be an active owner or admin, and an admin may invite only as a
// member: granting owner or admin needs an owner (the same policy as
// SetMemberRole). Only the token hash is stored. The raw token is returned once,
// as the second value, so the caller can build a shareable link. Email
// invitations send org_invite through orbit-notifications; phone invitations
// send nothing yet. Re-inviting the same recipient revokes the old pending
// invitation and rotates the token.
//
// acceptURL is optional (see domain.ResolveAcceptURL). It is validated before
// anything is written, for both channels. An email invitation sends the
// resolved link with the token in its fragment; a phone invitation ignores it.
func (s *Service) InviteMember(ctx context.Context, orgID, inviterID string, target domain.InviteTarget, role domain.MembershipRole, acceptURL string) (domain.Invite, string, error) {
	orgID = strings.TrimSpace(orgID)
	inviterID = strings.TrimSpace(inviterID)
	if orgID == "" || inviterID == "" {
		return domain.Invite{}, "", fmt.Errorf("org_id and inviter_id required")
	}
	target, err := domain.NormalizeInviteTarget(target)
	if err != nil {
		return domain.Invite{}, "", err
	}
	if s.invites == nil {
		return domain.Invite{}, "", fmt.Errorf("invite store is not configured")
	}
	if err := s.authorizeMembership(ctx, orgID, inviterID, domain.ActionInvite, "", role); err != nil {
		return domain.Invite{}, "", err
	}
	org, err := s.orgs.GetOrg(ctx, orgID)
	if err != nil {
		return domain.Invite{}, "", err
	}
	resolvedURL, err := domain.ResolveAcceptURL(acceptURL, org.Slug, s.inviteAcceptHosts)
	if err != nil {
		return domain.Invite{}, "", err
	}
	inviter, err := s.users.FindByID(ctx, inviterID)
	if err != nil {
		return domain.Invite{}, "", err
	}
	token, err := s.secrets.NewSecret()
	if err != nil {
		return domain.Invite{}, "", err
	}
	now := s.clock.Now()
	invite := domain.Invite{
		ID:        s.ids.NewID(),
		OrgID:     orgID,
		Email:     target.Email,
		Phone:     target.Phone,
		Role:      role,
		TokenHash: domain.HashInviteToken(token),
		InviterID: inviterID,
		Status:    domain.InvitePending,
		ExpiresAt: now.Add(domain.InviteTTL),
		CreatedAt: now,
	}
	if err := s.invites.CreateInvite(ctx, invite, domain.UserAuditActor(inviterID)); err != nil {
		return domain.Invite{}, "", err
	}
	if target.Phone != "" {
		// No SMS channel exists yet. The token is returned to the inviter, who
		// shares it themselves.
		s.logger("organization_invite_created", "org_id", orgID, "invite_id", invite.ID, "inviter_id", inviterID, "channel", inviteChannelPhone)
		invite.TokenHash = ""
		return invite, token, nil
	}
	notification := domain.InviteNotification{
		Recipient:     target.Email,
		OrgName:       org.Name,
		InviterName:   inviter.DisplayName,
		AcceptToken:   token,
		AcceptURL:     inviteAcceptLink(resolvedURL, token),
		ExpiresAt:     invite.ExpiresAt,
		Locale:        defaultInviteLocale,
		CorrelationID: invite.ID,
	}
	if notification.InviterName == "" {
		notification.InviterName = inviter.Email
	}
	if err := s.notifier.SendOrgInvite(ctx, notification); err != nil {
		// A pending invite nobody received is unusable, so revoke it. If the
		// revoke also fails, both failures are reported.
		if revokeErr := s.invites.RevokeInvite(ctx, orgID, invite.ID, domain.SystemAuditActor()); revokeErr != nil {
			return domain.Invite{}, "", fmt.Errorf("send invite: %w (revoke failed: %v)", err, revokeErr)
		}
		return domain.Invite{}, "", err
	}
	s.logger("organization_invite_created", "org_id", orgID, "invite_id", invite.ID, "inviter_id", inviterID, "channel", inviteChannelEmail)
	invite.TokenHash = ""
	return invite, token, nil
}

// GetInvitePreview resolves an invitation token to what the holder may see
// before accepting: the organization, the invited tier and the expiry. It never
// returns the recipient's email address or phone number. Any token that is not
// a pending, unexpired invitation to an existing organization is
// ErrInvalidInvite.
func (s *Service) GetInvitePreview(ctx context.Context, token string) (domain.InvitePreview, error) {
	token = strings.TrimSpace(token)
	if token == "" || s.invites == nil {
		return domain.InvitePreview{}, domain.ErrInvalidInvite
	}
	invite, err := s.invites.FindInviteByTokenHash(ctx, domain.HashInviteToken(token))
	if err != nil {
		if errors.Is(err, domain.ErrInviteNotFound) {
			return domain.InvitePreview{}, domain.ErrInvalidInvite
		}
		return domain.InvitePreview{}, err
	}
	now := s.clock.Now()
	if invite.Status != domain.InvitePending || !now.Before(invite.ExpiresAt) {
		return domain.InvitePreview{}, domain.ErrInvalidInvite
	}
	org, err := s.orgs.GetOrg(ctx, invite.OrgID)
	if err != nil {
		if errors.Is(err, domain.ErrOrgNotFound) {
			return domain.InvitePreview{}, domain.ErrInvalidInvite
		}
		return domain.InvitePreview{}, err
	}
	accountExists, err := s.inviteAccountExists(ctx, invite)
	if err != nil {
		return domain.InvitePreview{}, err
	}
	channel := inviteChannelEmail
	if invite.Phone != "" {
		channel = inviteChannelPhone
	}
	return domain.InvitePreview{
		OrgID:         org.ID,
		OrgName:       org.Name,
		OrgSlug:       org.Slug,
		Role:          invite.Role,
		Status:        invite.Status,
		ExpiresAt:     invite.ExpiresAt,
		Channel:       channel,
		AccountExists: accountExists,
	}, nil
}

// inviteAccountExists reports whether an active account already uses the
// invited phone number or email. A missing account is false; any other lookup
// error is returned.
func (s *Service) inviteAccountExists(ctx context.Context, invite domain.Invite) (bool, error) {
	var (
		user domain.User
		err  error
	)
	if invite.Phone != "" {
		user, err = s.users.FindByMobile(ctx, invite.Phone)
	} else {
		user, err = s.users.FindByEmail(ctx, invite.Email)
	}
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return false, nil
		}
		return false, err
	}
	return user.Status == domain.UserActive, nil
}

// inviteAcceptLink is the link an email invitation shows: the resolved
// accept_url with the raw token in its fragment. An empty accept_url gives an
// empty link, and the email carries only the code.
func inviteAcceptLink(resolvedURL, token string) string {
	if resolvedURL == "" {
		return ""
	}
	return resolvedURL + "#" + token
}

// pendingInvite resolves an invitation token to its invite. A token that is
// unknown, not pending or expired at now is the generic ErrInvalidInvite.
func (s *Service) pendingInvite(ctx context.Context, token string, now time.Time) (domain.Invite, error) {
	token = strings.TrimSpace(token)
	if token == "" || s.invites == nil {
		return domain.Invite{}, domain.ErrInvalidInvite
	}
	invite, err := s.invites.FindInviteByTokenHash(ctx, domain.HashInviteToken(token))
	if err != nil {
		if errors.Is(err, domain.ErrInviteNotFound) {
			return domain.Invite{}, domain.ErrInvalidInvite
		}
		return domain.Invite{}, err
	}
	if invite.Status != domain.InvitePending || !now.Before(invite.ExpiresAt) {
		return domain.Invite{}, domain.ErrInvalidInvite
	}
	return invite, nil
}

// AcceptInvite consumes one invitation token for the authenticated user. The
// account email must equal the invitation address case-insensitively, the
// account must be usable, and the invite must still be pending and unexpired.
// Every failure is the same generic error so the reason and the target
// organization are not revealed. Accepting an invite for an address without an
// account is out of scope until sign-up lands.
func (s *Service) AcceptInvite(ctx context.Context, userID, token string) (domain.Invite, error) {
	userID = strings.TrimSpace(userID)
	token = strings.TrimSpace(token)
	if userID == "" || token == "" {
		return domain.Invite{}, domain.ErrInvalidInvite
	}
	now := s.clock.Now()
	invite, err := s.pendingInvite(ctx, token, now)
	if err != nil {
		return domain.Invite{}, err
	}
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return domain.Invite{}, domain.ErrInvalidInvite
		}
		return domain.Invite{}, err
	}
	if !inviteRecipientMatches(user, invite) {
		return domain.Invite{}, domain.ErrInvalidInvite
	}
	if err := s.invites.AcceptInvite(ctx, invite.ID, invite.OrgID, userID, invite.Role, now); err != nil {
		if errors.Is(err, domain.ErrInviteNotFound) {
			return domain.Invite{}, domain.ErrInvalidInvite
		}
		return domain.Invite{}, err
	}
	invite.Status = domain.InviteAccepted
	invite.AcceptedAt = &now
	invite.TokenHash = ""
	s.logger("organization_invite_accepted", "org_id", invite.OrgID, "invite_id", invite.ID, "user_id", userID)
	return invite, nil
}

// InviteSignupResult is the outcome of AcceptInviteWithSignup. Identifier is
// what the new account signs in with: its E.164 phone number for a phone
// invitation, or its email address for an email invitation.
type InviteSignupResult struct {
	UserID     string
	OrgID      string
	InviteID   string
	Role       domain.MembershipRole
	Identifier string
}

// AcceptInviteWithSignup creates the account for an invitation and accepts it.
// The token must resolve to a pending, unexpired invitation (anything else is
// ErrInvalidInvite). Invite sign-up is switched by AUTH_INVITE_SIGNUP, default
// on; off is ErrInviteSignupDisabled. It does not depend on AUTH_SIGNUP.
//
// A phone invitation writes an account with the invited number, left unverified
// (mobile_verified_at NULL). If an account already uses the number the call is
// ErrAccountExists and nothing is written.
//
// An email invitation writes an account with the invited email and
// email_verified_at set, because the token reached only that mailbox. An active
// account with the email is ErrAccountExists and nothing is written. A
// signup_pending account with the email is taken over: its password and display
// name are replaced and it becomes active (see SignupStore.CreateInviteSignup).
//
// The password must be at least eight characters after trimming
// (ErrPasswordTooShort), and present (ErrPasswordRequired) when empty. The
// account, the accepted invitation, the membership with the invited tier and
// the events commit in one transaction.
func (s *Service) AcceptInviteWithSignup(ctx context.Context, token, password, displayName string) (InviteSignupResult, error) {
	if s.inviteSignupOff {
		return InviteSignupResult{}, ErrInviteSignupDisabled
	}
	if s.signup == nil || s.users == nil || s.hasher == nil || s.ids == nil {
		return InviteSignupResult{}, fmt.Errorf("invite sign-up is not configured")
	}
	now := s.clock.Now()
	invite, err := s.pendingInvite(ctx, token, now)
	if err != nil {
		return InviteSignupResult{}, err
	}
	if invite.Phone == "" && invite.Email == "" {
		return InviteSignupResult{}, domain.ErrInvalidInvite
	}
	if err := s.refuseExistingInviteAccount(ctx, invite); err != nil {
		return InviteSignupResult{}, err
	}
	trimmed := strings.TrimSpace(password)
	if trimmed == "" {
		return InviteSignupResult{}, ErrPasswordRequired
	}
	if utf8.RuneCountInString(trimmed) < minInvitePasswordRunes {
		return InviteSignupResult{}, ErrPasswordTooShort
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return InviteSignupResult{}, err
	}
	user := domain.User{
		ID:           s.ids.NewID(),
		PasswordHash: hash,
		DisplayName:  strings.TrimSpace(displayName),
		Status:       domain.UserActive,
	}
	channel := inviteChannelEmail
	identifier := invite.Email
	if invite.Phone != "" {
		channel = inviteChannelPhone
		identifier = invite.Phone
		user.Mobile = invite.Phone
	} else {
		user.Email = invite.Email
	}
	userID, created, err := s.signup.CreateInviteSignup(ctx, user, invite, now)
	if err != nil {
		if errors.Is(err, domain.ErrInviteNotFound) {
			return InviteSignupResult{}, domain.ErrInvalidInvite
		}
		return InviteSignupResult{}, err
	}
	if !created {
		// A concurrent sign-up took the number or an active email between the
		// check above and the transaction.
		return InviteSignupResult{}, ErrAccountExists
	}
	s.logger("organization_invite_accepted",
		"org_id", invite.OrgID, "invite_id", invite.ID, "user_id", userID,
		"channel", channel, "signup", true)
	return InviteSignupResult{
		UserID:     userID,
		OrgID:      invite.OrgID,
		InviteID:   invite.ID,
		Role:       invite.Role,
		Identifier: identifier,
	}, nil
}

// refuseExistingInviteAccount returns ErrAccountExists when an account already
// holds the invitation's target. A phone target is refused for any account, as
// before. An email target is refused only for an active account: a
// signup_pending account is taken over by the store. A failed lookup for a phone
// target falls through to the transaction, which refuses a duplicate number.
func (s *Service) refuseExistingInviteAccount(ctx context.Context, invite domain.Invite) error {
	if invite.Phone != "" {
		if _, err := s.users.FindByMobile(ctx, invite.Phone); err == nil {
			return ErrAccountExists
		}
		return nil
	}
	user, err := s.users.FindByEmail(ctx, invite.Email)
	if errors.Is(err, domain.ErrUserNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if user.Status == domain.UserActive {
		return ErrAccountExists
	}
	return nil
}

// RevokeInvite revokes one pending invitation. Only an active owner or admin of
// the organization may revoke; an unknown or already consumed invite is
// ErrInviteNotFound.
func (s *Service) RevokeInvite(ctx context.Context, orgID, inviteID, callerID string) error {
	orgID = strings.TrimSpace(orgID)
	inviteID = strings.TrimSpace(inviteID)
	callerID = strings.TrimSpace(callerID)
	if orgID == "" || inviteID == "" || callerID == "" {
		return fmt.Errorf("org_id, invite_id and caller_id required")
	}
	if s.invites == nil {
		return fmt.Errorf("invite store is not configured")
	}
	if err := s.authorizeMembership(ctx, orgID, callerID, domain.ActionRevokeInvite, "", ""); err != nil {
		return err
	}
	return s.invites.RevokeInvite(ctx, orgID, inviteID, domain.UserAuditActor(callerID))
}

// ListInvites returns every invitation of orgID to an owner or admin. The token
// hash is never returned.
func (s *Service) ListInvites(ctx context.Context, orgID, callerID string) ([]domain.Invite, error) {
	orgID, callerID = strings.TrimSpace(orgID), strings.TrimSpace(callerID)
	if orgID == "" || callerID == "" {
		return nil, fmt.Errorf("org_id and caller_id required")
	}
	if err := s.authorizeMembership(ctx, orgID, callerID, domain.ActionListInvites, "", ""); err != nil {
		return nil, err
	}
	if s.invites == nil {
		return nil, fmt.Errorf("invite store is not configured")
	}
	if _, err := s.orgs.GetOrg(ctx, orgID); err != nil {
		return nil, err
	}
	invites, err := s.invites.ListInvites(ctx, orgID)
	if err != nil {
		return nil, err
	}
	for i := range invites {
		invites[i].TokenHash = ""
	}
	return invites, nil
}

// inviteRecipientMatches decides whether an authenticated account may accept an
// invitation. The account must be active. An email invitation needs the account
// email to match and to have been proven: email OTP, an admin/demo seed, a
// backfilled pre-sign-up account or a completed sign-up all set
// email_verified_at. A phone invitation needs the account mobile number to equal
// the invited E.164 number. A signup_pending account is not active and therefore
// cannot accept an invitation.
func inviteRecipientMatches(user domain.User, invite domain.Invite) bool {
	if user.Status != domain.UserActive {
		return false
	}
	if invite.Phone != "" {
		mobile := strings.TrimSpace(user.Mobile)
		return mobile != "" && mobile == invite.Phone
	}
	return user.Email != "" &&
		user.EmailVerified &&
		strings.EqualFold(user.Email, invite.Email)
}
