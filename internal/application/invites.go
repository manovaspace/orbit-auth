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
func (s *Service) InviteMember(ctx context.Context, orgID, inviterID string, target domain.InviteTarget, role domain.MembershipRole) (domain.Invite, string, error) {
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
	if err := s.invites.CreateInvite(ctx, invite); err != nil {
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
	return domain.InvitePreview{
		OrgID:     org.ID,
		OrgName:   org.Name,
		OrgSlug:   org.Slug,
		Role:      invite.Role,
		Status:    invite.Status,
		ExpiresAt: invite.ExpiresAt,
	}, nil
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
	return s.invites.RevokeInvite(ctx, orgID, inviteID)
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
