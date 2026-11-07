package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// CreateOrg creates an organization and makes its creator an active member in
// the same store transaction.
func (s *Service) CreateOrg(ctx context.Context, creatorID, name string) (domain.Organization, error) {
	creatorID = strings.TrimSpace(creatorID)
	name = strings.TrimSpace(name)
	if creatorID == "" {
		return domain.Organization{}, fmt.Errorf("creator_id required")
	}
	if name == "" {
		return domain.Organization{}, domain.ErrInvalidOrgName
	}
	now := s.clock.Now()
	org := domain.Organization{
		ID:        s.ids.NewID(),
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.orgs.CreateOrg(ctx, org, creatorID); err != nil {
		return domain.Organization{}, err
	}
	s.logger("organization_created", "org_id", org.ID, "creator_id", creatorID)
	return org, nil
}

// ListOrgsForUser lists the organizations in which userID has an active
// membership.
func (s *Service) ListOrgsForUser(ctx context.Context, userID string) ([]domain.Organization, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("user_id required")
	}
	return s.orgs.ListOrgsForUser(ctx, userID)
}

// ListMembers returns every membership of orgID with its lifecycle status.
func (s *Service) ListMembers(ctx context.Context, orgID string) ([]domain.Membership, error) {
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return nil, fmt.Errorf("org_id required")
	}
	if _, err := s.orgs.GetOrg(ctx, orgID); err != nil {
		return nil, err
	}
	return s.orgs.ListMembers(ctx, orgID)
}

// AddMember makes userID an active plain member of orgID. Adding an existing
// member leaves that membership and its status untouched. callerID must be an
// owner or admin of the organization.
func (s *Service) AddMember(ctx context.Context, orgID, callerID, userID string) error {
	orgID, callerID, userID = strings.TrimSpace(orgID), strings.TrimSpace(callerID), strings.TrimSpace(userID)
	if orgID == "" || callerID == "" || userID == "" {
		return fmt.Errorf("org_id, caller_id and user_id required")
	}
	if err := s.authorizeMembership(ctx, orgID, callerID, domain.ActionAddMember, userID, ""); err != nil {
		return err
	}
	if err := s.requireUser(ctx, userID); err != nil {
		return err
	}
	if _, err := s.orgs.GetOrg(ctx, orgID); err != nil {
		return err
	}
	return s.orgs.AddMember(ctx, orgID, userID, domain.MembershipActive, domain.RoleMember)
}

// SuspendMember suspends one active membership. Suspending an already
// non-active member is a no-op. The last active membership and the last active
// owner cannot be suspended. callerID must be an owner or admin who outranks
// the target.
func (s *Service) SuspendMember(ctx context.Context, orgID, callerID, userID string) error {
	orgID, callerID, userID = strings.TrimSpace(orgID), strings.TrimSpace(callerID), strings.TrimSpace(userID)
	if orgID == "" || callerID == "" || userID == "" {
		return fmt.Errorf("org_id, caller_id and user_id required")
	}
	if err := s.authorizeMembership(ctx, orgID, callerID, domain.ActionSuspendMember, userID, ""); err != nil {
		return err
	}
	if err := s.requireUser(ctx, userID); err != nil {
		return err
	}
	membership, err := s.orgs.GetMembership(ctx, orgID, userID)
	if err != nil {
		return err
	}
	if membership.Status != domain.MembershipActive {
		return nil
	}
	return s.orgs.UpdateMemberStatus(ctx, orgID, userID, domain.MembershipSuspended,
		domain.MembershipActor{CallerID: callerID, Action: domain.ActionSuspendMember})
}

// RemoveMember deletes one membership. Removing the last active membership or
// the last active owner is refused. Any active member may remove themselves;
// removing someone else needs an owner or an admin who outranks the target.
func (s *Service) RemoveMember(ctx context.Context, orgID, callerID, userID string) error {
	orgID, callerID, userID = strings.TrimSpace(orgID), strings.TrimSpace(callerID), strings.TrimSpace(userID)
	if orgID == "" || callerID == "" || userID == "" {
		return fmt.Errorf("org_id, caller_id and user_id required")
	}
	if err := s.authorizeMembership(ctx, orgID, callerID, domain.ActionRemoveMember, userID, ""); err != nil {
		return err
	}
	if err := s.requireUser(ctx, userID); err != nil {
		return err
	}
	if _, err := s.orgs.GetMembership(ctx, orgID, userID); err != nil {
		return err
	}
	return s.orgs.RemoveMember(ctx, orgID, userID,
		domain.MembershipActor{CallerID: callerID, Action: domain.ActionRemoveMember})
}

// SetMemberRole changes userID's administration tier and returns the updated
// membership. Only an owner may grant owner or admin; the last active owner
// cannot be demoted.
func (s *Service) SetMemberRole(ctx context.Context, orgID, callerID, userID string, role domain.MembershipRole) (domain.Membership, error) {
	orgID, callerID, userID = strings.TrimSpace(orgID), strings.TrimSpace(callerID), strings.TrimSpace(userID)
	if orgID == "" || callerID == "" || userID == "" {
		return domain.Membership{}, fmt.Errorf("org_id, caller_id and user_id required")
	}
	if err := s.authorizeMembership(ctx, orgID, callerID, domain.ActionSetRole, userID, role); err != nil {
		return domain.Membership{}, err
	}
	if _, err := s.orgs.GetMembership(ctx, orgID, userID); err != nil {
		return domain.Membership{}, err
	}
	if err := s.orgs.UpdateMemberRole(ctx, orgID, userID, role, callerID); err != nil {
		return domain.Membership{}, err
	}
	s.logger("organization_member_role_changed", "org_id", orgID, "user_id", userID, "role", string(role), "actor_id", callerID)
	return s.orgs.GetMembership(ctx, orgID, userID)
}

// authorizeMembership loads the caller's membership and, when targetUserID is
// set and is a member, the target's, then applies the pure policy in the domain
// package. A caller who is not a member gets domain.ErrNotMember.
func (s *Service) authorizeMembership(ctx context.Context, orgID, callerID string, action domain.MembershipAction, targetUserID string, newRole domain.MembershipRole) error {
	caller, err := s.orgs.GetMembership(ctx, orgID, callerID)
	if err != nil {
		return err
	}
	var target *domain.Membership
	if targetUserID != "" {
		membership, err := s.orgs.GetMembership(ctx, orgID, targetUserID)
		switch {
		case err == nil:
			target = &membership
		case errors.Is(err, domain.ErrNotMember):
		default:
			return err
		}
	}
	return domain.AuthorizeMembershipAction(caller, action, target, newRole)
}

// requireUser turns an unknown target user id into domain.ErrUserNotFound
// before a membership write can reach the foreign key.
func (s *Service) requireUser(ctx context.Context, userID string) error {
	if s.users == nil {
		return fmt.Errorf("user store is not configured")
	}
	_, err := s.users.FindByID(ctx, userID)
	return err
}

// IsActiveMember reports whether userID is an active member of orgID. A
// non-member is false with no error.
func (s *Service) IsActiveMember(ctx context.Context, orgID, userID string) (bool, error) {
	orgID, userID = strings.TrimSpace(orgID), strings.TrimSpace(userID)
	if orgID == "" || userID == "" {
		return false, fmt.Errorf("org_id and user_id required")
	}
	membership, err := s.orgs.GetMembership(ctx, orgID, userID)
	if errors.Is(err, domain.ErrNotMember) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return membership.Status == domain.MembershipActive, nil
}
