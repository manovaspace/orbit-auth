package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// defaultInviteLocale is the locale an organization invitation uses when the
// caller supplies none. orbit-notifications renders the org_invite template in
// Persian by default.
const defaultInviteLocale = "fa"

// InviteMember creates a pending invitation and sends it through
// orbit-notifications. Only an active member of the organization may invite,
// and only the token hash is stored. Re-inviting an address that already has a
// pending invitation revokes the old one and rotates the token.
func (s *Service) InviteMember(ctx context.Context, orgID, inviterID, email string) (domain.Invite, error) {
	orgID = strings.TrimSpace(orgID)
	inviterID = strings.TrimSpace(inviterID)
	if orgID == "" || inviterID == "" {
		return domain.Invite{}, fmt.Errorf("org_id and inviter_id required")
	}
	email = domain.NormalizeEmail(email)
	if !domain.IsEmail(email) {
		return domain.Invite{}, domain.ErrInvalidEmail
	}
	if s.invites == nil {
		return domain.Invite{}, fmt.Errorf("invite store is not configured")
	}
	active, err := s.IsActiveMember(ctx, orgID, inviterID)
	if err != nil {
		return domain.Invite{}, err
	}
	if !active {
		return domain.Invite{}, domain.ErrNotMember
	}
	org, err := s.orgs.GetOrg(ctx, orgID)
	if err != nil {
		return domain.Invite{}, err
	}
	inviter, err := s.users.FindByID(ctx, inviterID)
	if err != nil {
		return domain.Invite{}, err
	}
	token, err := s.secrets.NewSecret()
	if err != nil {
		return domain.Invite{}, err
	}
	now := s.clock.Now()
	invite := domain.Invite{
		ID:        s.ids.NewID(),
		OrgID:     orgID,
		Email:     email,
		TokenHash: domain.HashInviteToken(token),
		InviterID: inviterID,
		Status:    domain.InvitePending,
		ExpiresAt: now.Add(domain.InviteTTL),
		CreatedAt: now,
	}
	if err := s.invites.CreateInvite(ctx, invite); err != nil {
		return domain.Invite{}, err
	}
	notification := domain.InviteNotification{
		Recipient:     email,
		OrgName:       org.Name,
		InviterName:   inviter.DisplayName,
		AcceptToken:   token,
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
		if revokeErr := s.invites.RevokeInvite(ctx, orgID, invite.ID); revokeErr != nil {
			return domain.Invite{}, fmt.Errorf("send invite: %w (revoke failed: %v)", err, revokeErr)
		}
		return domain.Invite{}, err
	}
	s.logger("organization_invite_created", "org_id", orgID, "invite_id", invite.ID, "inviter_id", inviterID)
	invite.TokenHash = ""
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
	if s.invites == nil {
		return domain.Invite{}, domain.ErrInvalidInvite
	}
	invite, err := s.invites.FindInviteByTokenHash(ctx, domain.HashInviteToken(token))
	if err != nil {
		if errors.Is(err, domain.ErrInviteNotFound) {
			return domain.Invite{}, domain.ErrInvalidInvite
		}
		return domain.Invite{}, err
	}
	now := s.clock.Now()
	if invite.Status != domain.InvitePending || !now.Before(invite.ExpiresAt) {
		return domain.Invite{}, domain.ErrInvalidInvite
	}
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return domain.Invite{}, domain.ErrInvalidInvite
		}
		return domain.Invite{}, err
	}
	if !inviteRecipientMatches(user, invite.Email) {
		return domain.Invite{}, domain.ErrInvalidInvite
	}
	if err := s.invites.AcceptInvite(ctx, invite.ID, invite.OrgID, userID, now); err != nil {
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

// RevokeInvite revokes one pending invitation. Only an active member of the
// organization may revoke; an unknown or already consumed invite is
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
	active, err := s.IsActiveMember(ctx, orgID, callerID)
	if err != nil {
		return err
	}
	if !active {
		return domain.ErrNotMember
	}
	return s.invites.RevokeInvite(ctx, orgID, inviteID)
}

// ListInvites returns every invitation of orgID. The token hash is never
// returned.
func (s *Service) ListInvites(ctx context.Context, orgID string) ([]domain.Invite, error) {
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return nil, fmt.Errorf("org_id required")
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
// invitation. The users table has no separate email_verified flag yet: an
// account email is only set by email OTP (which proves control of the address)
// or by a trusted admin or demo seed, so a non-empty email on a non-disabled
// account is the verified identity. A5 adds explicit sign-up verification.
func inviteRecipientMatches(user domain.User, inviteEmail string) bool {
	return user.Email != "" &&
		user.Status != domain.UserDisabled &&
		strings.EqualFold(user.Email, inviteEmail)
}
