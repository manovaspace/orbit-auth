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

// AddMember makes userID an active member of orgID. Adding an existing member
// leaves that membership and its status untouched.
func (s *Service) AddMember(ctx context.Context, orgID, userID string) error {
	orgID, userID = strings.TrimSpace(orgID), strings.TrimSpace(userID)
	if orgID == "" || userID == "" {
		return fmt.Errorf("org_id and user_id required")
	}
	if _, err := s.orgs.GetOrg(ctx, orgID); err != nil {
		return err
	}
	return s.orgs.AddMember(ctx, orgID, userID, domain.MembershipActive)
}

// SuspendMember suspends one active membership. Suspending an already
// non-active member is a no-op. The last active membership cannot be
// suspended.
func (s *Service) SuspendMember(ctx context.Context, orgID, userID string) error {
	orgID, userID = strings.TrimSpace(orgID), strings.TrimSpace(userID)
	if orgID == "" || userID == "" {
		return fmt.Errorf("org_id and user_id required")
	}
	membership, err := s.orgs.GetMembership(ctx, orgID, userID)
	if err != nil {
		return err
	}
	if membership.Status != domain.MembershipActive {
		return nil
	}
	return s.orgs.UpdateMemberStatus(ctx, orgID, userID, domain.MembershipSuspended)
}

// RemoveMember deletes one membership. Removing the last active membership is
// refused.
func (s *Service) RemoveMember(ctx context.Context, orgID, userID string) error {
	orgID, userID = strings.TrimSpace(orgID), strings.TrimSpace(userID)
	if orgID == "" || userID == "" {
		return fmt.Errorf("org_id and user_id required")
	}
	if _, err := s.orgs.GetMembership(ctx, orgID, userID); err != nil {
		return err
	}
	return s.orgs.RemoveMember(ctx, orgID, userID)
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
