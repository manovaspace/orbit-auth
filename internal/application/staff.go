package application

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

const (
	// defaultStaffPageSize is the page size when a staff listing asks for none.
	defaultStaffPageSize = 25
	// maxStaffPageSize is the largest page a staff listing returns.
	maxStaffPageSize = 100
	// staffInviterName names staff in the owner invitation email, because a
	// staff member is not a user with a display name.
	staffInviterName = "Manova"
)

// StaffListInput selects one page of organizations. Status is "", "active" or
// "suspended". PageSize zero means the default; PageToken is empty for the first
// page.
type StaffListInput struct {
	Query     string
	Status    string
	PageSize  int
	PageToken string
}

// StaffOrgPage is one page of organizations. NextPageToken is empty on the last
// page.
type StaffOrgPage struct {
	Organizations []domain.StaffOrgSummary
	NextPageToken string
}

// StaffOrgDetail is one organization with its members and pending invitations.
type StaffOrgDetail struct {
	Summary        domain.StaffOrgSummary
	Members        []domain.Membership
	PendingInvites []domain.Invite
}

// ProvisionOrgInput is a provisioning request. Target is the owner's email
// address or phone number; exactly one is set.
type ProvisionOrgInput struct {
	Name           string
	Slug           string
	IdempotencyKey string
	Target         domain.InviteTarget
}

// ProvisionOrgResult is the new organization and its owner invitation. Token is
// the raw invitation token for a phone owner only; an email owner receives it
// by email.
type ProvisionOrgResult struct {
	Summary domain.StaffOrgSummary
	Invite  domain.Invite
	Token   string
}

// StaffListOrganizations lists organizations for the staff console, oldest
// first. Staff are not members, so no membership is checked.
func (s *Service) StaffListOrganizations(ctx context.Context, in StaffListInput) (StaffOrgPage, error) {
	if err := s.staffReady(); err != nil {
		return StaffOrgPage{}, err
	}
	status, err := parseStaffStatus(in.Status)
	if err != nil {
		return StaffOrgPage{}, err
	}
	limit := in.PageSize
	if limit == 0 {
		limit = defaultStaffPageSize
	}
	if limit < 1 || limit > maxStaffPageSize {
		return StaffOrgPage{}, domain.ErrInvalidPageSize
	}
	after, err := decodeStaffCursor(in.PageToken)
	if err != nil {
		return StaffOrgPage{}, err
	}
	rows, err := s.orgs.ListOrganizationsForStaff(ctx, domain.StaffOrgListQuery{
		Query:  strings.TrimSpace(in.Query),
		Status: status,
		After:  after,
		Limit:  limit + 1,
	})
	if err != nil {
		return StaffOrgPage{}, err
	}
	page := StaffOrgPage{}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1].Organization
		page.NextPageToken = encodeStaffCursor(last.CreatedAt, last.ID)
	}
	page.Organizations = rows
	return page, nil
}

// StaffGetOrganization returns one organization with its members and its
// pending invitations. Token hashes are never returned.
func (s *Service) StaffGetOrganization(ctx context.Context, orgID string) (StaffOrgDetail, error) {
	if err := s.staffReady(); err != nil {
		return StaffOrgDetail{}, err
	}
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return StaffOrgDetail{}, fmt.Errorf("org_id required")
	}
	org, err := s.orgs.GetOrg(ctx, orgID)
	if err != nil {
		return StaffOrgDetail{}, err
	}
	members, err := s.orgs.ListMembers(ctx, orgID)
	if err != nil {
		return StaffOrgDetail{}, err
	}
	invites, err := s.invites.ListInvites(ctx, orgID)
	if err != nil {
		return StaffOrgDetail{}, err
	}
	pending := make([]domain.Invite, 0, len(invites))
	for _, invite := range invites {
		if invite.Status != domain.InvitePending {
			continue
		}
		invite.TokenHash = ""
		pending = append(pending, invite)
	}
	return StaffOrgDetail{
		Summary:        staffSummary(org, members),
		Members:        members,
		PendingInvites: pending,
	}, nil
}

// StaffProvisionOrganization creates an organization with no members and one
// pending owner invitation. A repeated IdempotencyKey with the same name and the
// same owner target returns the organization the key created and rotates the
// owner invitation, so a phone owner's result carries the new token. A repeat
// with another name or another owner target is ErrIdempotencyConflict.
//
// The organization and the invitation commit together. An email owner gets the
// org_invite email after that; when the send fails, the invitation is revoked
// and the error returned. The organization stays, and retrying with the same
// key sends a fresh invitation.
func (s *Service) StaffProvisionOrganization(ctx context.Context, actor domain.StaffActor, in ProvisionOrgInput) (ProvisionOrgResult, error) {
	if err := s.staffReady(); err != nil {
		return ProvisionOrgResult{}, err
	}
	if err := actor.Validate(); err != nil {
		return ProvisionOrgResult{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return ProvisionOrgResult{}, domain.ErrInvalidOrgName
	}
	key := strings.TrimSpace(in.IdempotencyKey)
	if key == "" {
		return ProvisionOrgResult{}, domain.ErrIdempotencyKeyRequired
	}
	target, err := domain.NormalizeInviteTarget(in.Target)
	if err != nil {
		return ProvisionOrgResult{}, err
	}

	// A key that already created an organization is a repeat: no slug is
	// allocated, so an explicit slug of that organization is not reported taken.
	prior, found, err := s.orgs.ProvisionedOrganization(ctx, key)
	if err != nil {
		return ProvisionOrgResult{}, err
	}
	if found {
		return s.repeatProvision(ctx, actor, prior, name, target)
	}

	now := s.clock.Now()
	orgID := s.ids.NewID()
	slug, err := s.allocateSlug(ctx, in.Slug, name, orgID)
	if err != nil {
		// A concurrent attempt with this key may have just created the organization
		// that holds the slug. That attempt is a repeat, not a conflict.
		if errors.Is(err, domain.ErrSlugTaken) {
			if prior, found, findErr := s.orgs.ProvisionedOrganization(ctx, key); findErr == nil && found {
				return s.repeatProvision(ctx, actor, prior, name, target)
			}
		}
		return ProvisionOrgResult{}, err
	}
	org := domain.Organization{
		ID:        orgID,
		Name:      name,
		Slug:      slug,
		Status:    domain.OrgStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	invite, token, err := s.newStaffOwnerInvite(actor, orgID, target, now)
	if err != nil {
		return ProvisionOrgResult{}, err
	}
	created, wasCreated, err := s.orgs.ProvisionOrganization(ctx, org, invite, key)
	if err != nil {
		return ProvisionOrgResult{}, err
	}
	if !wasCreated {
		// A concurrent request with the same key created the organization first.
		return s.repeatProvision(ctx, actor, created, name, target)
	}
	if err := s.deliverStaffOwnerInvite(ctx, org, invite, token); err != nil {
		return ProvisionOrgResult{}, err
	}
	s.logger("staff_organization_provisioned", "org_id", org.ID, "invite_id", invite.ID,
		"staff_subject", actor.Subject, "reason", actor.Reason, "channel", inviteChannel(target))
	return s.provisionResult(ctx, org, invite, token, target)
}

// repeatProvision answers a provisioning call whose key already created an
// organization. Only the same name and the same owner target is a repeat: it
// rotates the owner invitation for that target. Any other call is a conflict.
func (s *Service) repeatProvision(ctx context.Context, actor domain.StaffActor, prior domain.OrganizationProvision, name string, target domain.InviteTarget) (ProvisionOrgResult, error) {
	org := prior.Organization
	if org.Name != name || prior.OwnerTarget != target {
		return ProvisionOrgResult{}, domain.ErrIdempotencyConflict
	}
	invite, token, err := s.issueStaffOwnerInvite(ctx, actor, org, target)
	if err != nil {
		return ProvisionOrgResult{}, err
	}
	s.logger("staff_organization_provision_repeated", "org_id", org.ID, "invite_id", invite.ID,
		"staff_subject", actor.Subject, "reason", actor.Reason, "channel", inviteChannel(target))
	return s.provisionResult(ctx, org, invite, token, target)
}

func (s *Service) provisionResult(ctx context.Context, org domain.Organization, invite domain.Invite, token string, target domain.InviteTarget) (ProvisionOrgResult, error) {
	summary, err := s.staffSummaryFor(ctx, org)
	if err != nil {
		return ProvisionOrgResult{}, err
	}
	invite.TokenHash = ""
	result := ProvisionOrgResult{Summary: summary, Invite: invite}
	if target.Phone != "" {
		result.Token = token
	}
	return result, nil
}

// StaffSuspendOrganization suspends one organization. Its members keep their
// memberships; selecting into it and refreshing into it are refused.
func (s *Service) StaffSuspendOrganization(ctx context.Context, actor domain.StaffActor, orgID string) (domain.StaffOrgSummary, error) {
	return s.staffSetStatus(ctx, actor, orgID, true)
}

// StaffReactivateOrganization lifts a suspension.
func (s *Service) StaffReactivateOrganization(ctx context.Context, actor domain.StaffActor, orgID string) (domain.StaffOrgSummary, error) {
	return s.staffSetStatus(ctx, actor, orgID, false)
}

func (s *Service) staffSetStatus(ctx context.Context, actor domain.StaffActor, orgID string, suspend bool) (domain.StaffOrgSummary, error) {
	if err := s.staffReady(); err != nil {
		return domain.StaffOrgSummary{}, err
	}
	if err := actor.Validate(); err != nil {
		return domain.StaffOrgSummary{}, err
	}
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return domain.StaffOrgSummary{}, fmt.Errorf("org_id required")
	}
	event := "staff_organization_reactivated"
	if suspend {
		event = "staff_organization_suspended"
		if err := s.orgs.SuspendOrganization(ctx, orgID, actor); err != nil {
			return domain.StaffOrgSummary{}, err
		}
	} else if err := s.orgs.ReactivateOrganization(ctx, orgID, actor); err != nil {
		return domain.StaffOrgSummary{}, err
	}
	org, err := s.orgs.GetOrg(ctx, orgID)
	if err != nil {
		return domain.StaffOrgSummary{}, err
	}
	s.logger(event, "org_id", orgID, "staff_subject", actor.Subject, "reason", actor.Reason)
	return s.staffSummaryFor(ctx, org)
}

// StaffSetOrganizationSlug changes an organization's slug through the owner's
// registry rules: the new slug must be valid and free, and the old one stays
// reserved for the organization.
func (s *Service) StaffSetOrganizationSlug(ctx context.Context, actor domain.StaffActor, orgID, slug string) (domain.StaffOrgSummary, error) {
	if err := s.staffReady(); err != nil {
		return domain.StaffOrgSummary{}, err
	}
	if err := actor.Validate(); err != nil {
		return domain.StaffOrgSummary{}, err
	}
	orgID = strings.TrimSpace(orgID)
	slug = strings.ToLower(strings.TrimSpace(slug))
	if orgID == "" {
		return domain.StaffOrgSummary{}, fmt.Errorf("org_id required")
	}
	if err := domain.ValidateSlug(slug); err != nil {
		return domain.StaffOrgSummary{}, err
	}
	// Staff are not members, so the store checks no membership. It names the staff
	// subject as the actor of auth.org.slug_changed and carries the reason.
	if err := s.orgs.SetOrgSlugByStaff(ctx, orgID, slug, actor); err != nil {
		return domain.StaffOrgSummary{}, err
	}
	org, err := s.orgs.GetOrg(ctx, orgID)
	if err != nil {
		return domain.StaffOrgSummary{}, err
	}
	s.logger("staff_organization_slug_changed", "org_id", orgID, "slug", slug, "staff_subject", actor.Subject, "reason", actor.Reason)
	return s.staffSummaryFor(ctx, org)
}

// StaffReinviteOwner issues a new owner invitation to target and rotates the
// pending one for the same recipient. The invitation returned carries no
// token hash; the raw token is returned only for a phone owner.
func (s *Service) StaffReinviteOwner(ctx context.Context, actor domain.StaffActor, orgID string, target domain.InviteTarget) (domain.Invite, string, error) {
	if err := s.staffReady(); err != nil {
		return domain.Invite{}, "", err
	}
	if err := actor.Validate(); err != nil {
		return domain.Invite{}, "", err
	}
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return domain.Invite{}, "", fmt.Errorf("org_id required")
	}
	target, err := domain.NormalizeInviteTarget(target)
	if err != nil {
		return domain.Invite{}, "", err
	}
	org, err := s.orgs.GetOrg(ctx, orgID)
	if err != nil {
		return domain.Invite{}, "", err
	}
	invite, token, err := s.issueStaffOwnerInvite(ctx, actor, org, target)
	if err != nil {
		return domain.Invite{}, "", err
	}
	s.logger("staff_owner_reinvited", "org_id", orgID, "invite_id", invite.ID,
		"staff_subject", actor.Subject, "reason", actor.Reason, "channel", inviteChannel(target))
	if target.Phone == "" {
		token = ""
	}
	return invite, token, nil
}

// issueStaffOwnerInvite creates the owner invitation, revokes the pending one
// for the same recipient, and delivers it.
func (s *Service) issueStaffOwnerInvite(ctx context.Context, actor domain.StaffActor, org domain.Organization, target domain.InviteTarget) (domain.Invite, string, error) {
	invite, token, err := s.newStaffOwnerInvite(actor, org.ID, target, s.clock.Now())
	if err != nil {
		return domain.Invite{}, "", err
	}
	if err := s.invites.CreateInvite(ctx, invite); err != nil {
		return domain.Invite{}, "", err
	}
	if err := s.deliverStaffOwnerInvite(ctx, org, invite, token); err != nil {
		return domain.Invite{}, "", err
	}
	invite.TokenHash = ""
	return invite, token, nil
}

// newStaffOwnerInvite builds a pending owner-tier invitation issued by staff and
// its raw token. Only the token hash is set on the invitation.
func (s *Service) newStaffOwnerInvite(actor domain.StaffActor, orgID string, target domain.InviteTarget, now time.Time) (domain.Invite, string, error) {
	token, err := s.secrets.NewSecret()
	if err != nil {
		return domain.Invite{}, "", err
	}
	return domain.Invite{
		ID:                  s.ids.NewID(),
		OrgID:               orgID,
		Email:               target.Email,
		Phone:               target.Phone,
		Role:                domain.RoleOwner,
		TokenHash:           domain.HashInviteToken(token),
		InviterStaffSubject: actor.Subject,
		Status:              domain.InvitePending,
		ExpiresAt:           now.Add(domain.InviteTTL),
		CreatedAt:           now,
	}, token, nil
}

// deliverStaffOwnerInvite sends the org_invite email for an email owner. A phone
// owner has no channel yet, so the token goes back to the staff caller. A failed
// send revokes the invitation.
func (s *Service) deliverStaffOwnerInvite(ctx context.Context, org domain.Organization, invite domain.Invite, token string) error {
	if invite.Phone != "" {
		return nil
	}
	notification := domain.InviteNotification{
		Recipient:     invite.Email,
		OrgName:       org.Name,
		InviterName:   staffInviterName,
		AcceptToken:   token,
		ExpiresAt:     invite.ExpiresAt,
		Locale:        defaultInviteLocale,
		CorrelationID: invite.ID,
	}
	if err := s.notifier.SendOrgInvite(ctx, notification); err != nil {
		if revokeErr := s.invites.RevokeInvite(ctx, org.ID, invite.ID); revokeErr != nil {
			return fmt.Errorf("send invite: %w (revoke failed: %v)", err, revokeErr)
		}
		return err
	}
	return nil
}

// staffReady reports whether the stores and ports the staff operations need are
// configured.
func (s *Service) staffReady() error {
	if s.orgs == nil || s.invites == nil || s.notifier == nil || s.secrets == nil || s.ids == nil || s.clock == nil {
		return fmt.Errorf("staff organization operations are not configured")
	}
	return nil
}

// staffSummaryFor loads the members of org and returns its staff summary.
func (s *Service) staffSummaryFor(ctx context.Context, org domain.Organization) (domain.StaffOrgSummary, error) {
	members, err := s.orgs.ListMembers(ctx, org.ID)
	if err != nil {
		return domain.StaffOrgSummary{}, err
	}
	return staffSummary(org, members), nil
}

// staffSummary counts every membership and lists the active owners.
func staffSummary(org domain.Organization, members []domain.Membership) domain.StaffOrgSummary {
	owners := make([]string, 0)
	for _, member := range members {
		if member.Status == domain.MembershipActive && member.Role == domain.RoleOwner {
			owners = append(owners, member.UserID)
		}
	}
	return domain.StaffOrgSummary{Organization: org, MemberCount: len(members), OwnerUserIDs: owners}
}

func parseStaffStatus(raw string) (domain.OrganizationStatus, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "", nil
	case string(domain.OrgStatusActive):
		return domain.OrgStatusActive, nil
	case string(domain.OrgStatusSuspended):
		return domain.OrgStatusSuspended, nil
	default:
		return "", domain.ErrInvalidOrgStatus
	}
}

// encodeStaffCursor encodes the keyset position after a row as an opaque token.
func encodeStaffCursor(createdAt time.Time, id string) string {
	raw := createdAt.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeStaffCursor reads a token from encodeStaffCursor. An empty token is the
// first page and returns nil.
func decodeStaffCursor(token string) (*domain.StaffOrgCursor, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, domain.ErrInvalidPageToken
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return nil, domain.ErrInvalidPageToken
	}
	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return nil, domain.ErrInvalidPageToken
	}
	if _, err := uuid.Parse(parts[1]); err != nil {
		return nil, domain.ErrInvalidPageToken
	}
	return &domain.StaffOrgCursor{CreatedAt: createdAt, ID: parts[1]}, nil
}

// inviteChannel names the channel an invitation target uses, for logs.
func inviteChannel(target domain.InviteTarget) string {
	if target.Phone != "" {
		return inviteChannelPhone
	}
	return inviteChannelEmail
}
