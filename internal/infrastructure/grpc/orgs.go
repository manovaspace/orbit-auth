package grpc

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	authv1 "github.com/manovaspace/orbit-auth/api/auth/v1"
	"github.com/manovaspace/orbit-auth/internal/application"
	"github.com/manovaspace/orbit-auth/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// OrgService is the port the gRPC adapter needs from the application layer.
type OrgService interface {
	CreateOrg(ctx context.Context, creatorID, name string) (domain.Organization, error)
	ListOrgsForUser(ctx context.Context, userID string) ([]domain.Organization, error)
	ListMembers(ctx context.Context, orgID string) ([]domain.Membership, error)
	AddMember(ctx context.Context, orgID, callerID, userID string) error
	SuspendMember(ctx context.Context, orgID, callerID, userID string) error
	RemoveMember(ctx context.Context, orgID, callerID, userID string) error
	SetMemberRole(ctx context.Context, orgID, callerID, userID string, role domain.MembershipRole) (domain.Membership, error)
	IsActiveMember(ctx context.Context, orgID, userID string) (bool, error)
	InviteMember(ctx context.Context, orgID, inviterID, email string) (domain.Invite, error)
	AcceptInvite(ctx context.Context, userID, token string) (domain.Invite, error)
	RevokeInvite(ctx context.Context, orgID, inviteID, callerID string) error
	ListInvites(ctx context.Context, orgID, callerID string) ([]domain.Invite, error)
	SelectOrg(ctx context.Context, userID, refreshToken, orgID string) (access, refresh string, expires time.Time, err error)
}

// OrgServer implements the OrgService gRPC API. The caller is always the
// verified access-token subject, never a request field.
type OrgServer struct {
	authv1.UnimplementedOrgServiceServer
	svc OrgService
}

func NewOrg(svc OrgService) *OrgServer {
	return &OrgServer{svc: svc}
}

func (s *OrgServer) CreateOrg(ctx context.Context, req *authv1.CreateOrgRequest) (*authv1.CreateOrgResponse, error) {
	callerID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	org, err := s.svc.CreateOrg(ctx, callerID, req.GetName())
	if err != nil {
		return nil, orgError(err)
	}
	return &authv1.CreateOrgResponse{Organization: organizationProto(org)}, nil
}

func (s *OrgServer) ListMyOrgs(ctx context.Context, _ *authv1.ListMyOrgsRequest) (*authv1.ListMyOrgsResponse, error) {
	callerID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	orgs, err := s.svc.ListOrgsForUser(ctx, callerID)
	if err != nil {
		return nil, orgError(err)
	}
	out := make([]*authv1.Organization, 0, len(orgs))
	for _, org := range orgs {
		out = append(out, organizationProto(org))
	}
	return &authv1.ListMyOrgsResponse{Organizations: out}, nil
}

func (s *OrgServer) ListMembers(ctx context.Context, req *authv1.ListMembersRequest) (*authv1.ListMembersResponse, error) {
	orgID, _, err := s.requireActiveMember(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	members, err := s.svc.ListMembers(ctx, orgID)
	if err != nil {
		return nil, orgError(err)
	}
	out := make([]*authv1.Membership, 0, len(members))
	for _, membership := range members {
		out = append(out, membershipProto(membership))
	}
	return &authv1.ListMembersResponse{Members: out}, nil
}

func (s *OrgServer) AddMember(ctx context.Context, req *authv1.AddMemberRequest) (*authv1.AddMemberResponse, error) {
	orgID, userID, err := s.requireActiveMember(ctx, req.GetOrgId(), req.GetUserId())
	if err != nil {
		return nil, err
	}
	callerID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.svc.AddMember(ctx, orgID, callerID, userID); err != nil {
		return nil, orgError(err)
	}
	return &authv1.AddMemberResponse{}, nil
}

func (s *OrgServer) RemoveMember(ctx context.Context, req *authv1.RemoveMemberRequest) (*authv1.RemoveMemberResponse, error) {
	orgID, userID, err := s.requireActiveMember(ctx, req.GetOrgId(), req.GetUserId())
	if err != nil {
		return nil, err
	}
	callerID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.svc.RemoveMember(ctx, orgID, callerID, userID); err != nil {
		return nil, orgError(err)
	}
	return &authv1.RemoveMemberResponse{}, nil
}

func (s *OrgServer) SuspendMember(ctx context.Context, req *authv1.SuspendMemberRequest) (*authv1.SuspendMemberResponse, error) {
	orgID, userID, err := s.requireActiveMember(ctx, req.GetOrgId(), req.GetUserId())
	if err != nil {
		return nil, err
	}
	callerID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.svc.SuspendMember(ctx, orgID, callerID, userID); err != nil {
		return nil, orgError(err)
	}
	return &authv1.SuspendMemberResponse{}, nil
}

// SetMemberRole changes a member's organization administration tier. The
// application layer decides who may grant which tier.
func (s *OrgServer) SetMemberRole(ctx context.Context, req *authv1.SetMemberRoleRequest) (*authv1.SetMemberRoleResponse, error) {
	orgID, userID, err := s.requireActiveMember(ctx, req.GetOrgId(), req.GetUserId())
	if err != nil {
		return nil, err
	}
	callerID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	role, err := domain.ParseMembershipRole(strings.TrimSpace(req.GetRole()))
	if err != nil {
		return nil, orgError(err)
	}
	membership, err := s.svc.SetMemberRole(ctx, orgID, callerID, userID, role)
	if err != nil {
		return nil, orgError(err)
	}
	return &authv1.SetMemberRoleResponse{Membership: membershipProto(membership)}, nil
}

// InviteMember creates an invitation. The application layer requires the
// caller to be an active owner or admin of the organization.
func (s *OrgServer) InviteMember(ctx context.Context, req *authv1.InviteMemberRequest) (*authv1.InviteMemberResponse, error) {
	orgID, _, err := s.requireActiveMember(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	callerID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	invite, err := s.svc.InviteMember(ctx, orgID, callerID, req.GetEmail())
	if err != nil {
		return nil, orgError(err)
	}
	return &authv1.InviteMemberResponse{Invite: inviteProto(invite)}, nil
}

// AcceptInvite works for any authenticated user, including one who is not yet a
// member of the organization. The token is the only invitation selector; a
// wrong token, wrong account, expiry or reuse all return the same error.
func (s *OrgServer) AcceptInvite(ctx context.Context, req *authv1.AcceptInviteRequest) (*authv1.AcceptInviteResponse, error) {
	callerID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	invite, err := s.svc.AcceptInvite(ctx, callerID, req.GetToken())
	if err != nil {
		return nil, orgError(err)
	}
	return &authv1.AcceptInviteResponse{Invite: inviteProto(invite)}, nil
}

func (s *OrgServer) RevokeInvite(ctx context.Context, req *authv1.RevokeInviteRequest) (*authv1.RevokeInviteResponse, error) {
	orgID, _, err := s.requireActiveMember(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	inviteID := strings.TrimSpace(req.GetInviteId())
	if !isUUID(inviteID) {
		return nil, status.Error(codes.InvalidArgument, "invalid invite_id")
	}
	callerID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.svc.RevokeInvite(ctx, orgID, inviteID, callerID); err != nil {
		return nil, orgError(err)
	}
	return &authv1.RevokeInviteResponse{}, nil
}

func (s *OrgServer) ListInvites(ctx context.Context, req *authv1.ListInvitesRequest) (*authv1.ListInvitesResponse, error) {
	orgID, _, err := s.requireActiveMember(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	callerID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	invites, err := s.svc.ListInvites(ctx, orgID, callerID)
	if err != nil {
		return nil, orgError(err)
	}
	out := make([]*authv1.Invite, 0, len(invites))
	for _, invite := range invites {
		out = append(out, inviteProto(invite))
	}
	return &authv1.ListInvitesResponse{Invites: out}, nil
}

// SelectOrg switches the caller's active organization. Membership is gated the
// same way as every other organization-scoped call; the presented refresh token
// is rotated so an old pair cannot keep selecting organizations.
func (s *OrgServer) SelectOrg(ctx context.Context, req *authv1.SelectOrgRequest) (*authv1.SelectOrgResponse, error) {
	orgID, _, err := s.requireActiveMember(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	callerID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	access, refresh, expires, err := s.svc.SelectOrg(ctx, callerID, req.GetRefreshToken(), orgID)
	if err != nil {
		if errors.Is(err, application.ErrInvalidRefresh) {
			return nil, status.Error(codes.Unauthenticated, "invalid refresh token")
		}
		return nil, orgError(err)
	}
	return &authv1.SelectOrgResponse{Tokens: tokenResponse(access, refresh, expires)}, nil
}

// caller returns the verified access-token subject. Handlers never accept the
// acting user from the request.
func caller(ctx context.Context) (string, error) {
	callerID, ok := callerIDFromContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "authentication required")
	}
	return callerID, nil
}

// requireActiveMember validates the ids, then gates the call on the caller
// being an active member. A non-member and an unknown organization both return
// NotFound so the organization's existence is not revealed.
func (s *OrgServer) requireActiveMember(ctx context.Context, rawOrgID string, rawUserIDs ...string) (string, string, error) {
	callerID, err := caller(ctx)
	if err != nil {
		return "", "", err
	}
	orgID := strings.TrimSpace(rawOrgID)
	if !isUUID(orgID) {
		return "", "", status.Error(codes.InvalidArgument, "invalid org_id")
	}
	var userID string
	if len(rawUserIDs) > 0 {
		userID = strings.TrimSpace(rawUserIDs[0])
		if !isUUID(userID) {
			return "", "", status.Error(codes.InvalidArgument, "invalid user_id")
		}
	}
	active, err := s.svc.IsActiveMember(ctx, orgID, callerID)
	if err != nil {
		return "", "", orgError(err)
	}
	if !active {
		return "", "", status.Error(codes.NotFound, "organization not found")
	}
	return orgID, userID, nil
}

func isUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func orgError(err error) error {
	switch {
	case errors.Is(err, domain.ErrForbidden):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, domain.ErrLastActiveMember), errors.Is(err, domain.ErrLastOwner):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, domain.ErrInvalidRole):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, domain.ErrUserNotFound):
		return status.Error(codes.NotFound, "user not found")
	case errors.Is(err, domain.ErrOrgNotFound), errors.Is(err, domain.ErrNotMember):
		return status.Error(codes.NotFound, "organization not found")
	case errors.Is(err, domain.ErrInvalidOrgName):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, domain.ErrInvalidInvite):
		return status.Error(codes.InvalidArgument, domain.ErrInvalidInvite.Error())
	case errors.Is(err, domain.ErrInvalidEmail):
		return status.Error(codes.InvalidArgument, domain.ErrInvalidEmail.Error())
	case errors.Is(err, domain.ErrInviteNotFound):
		return status.Error(codes.NotFound, "invite not found")
	default:
		slog.Error("organization operation failed", "error", err)
		return status.Error(codes.Internal, "internal error")
	}
}

func organizationProto(org domain.Organization) *authv1.Organization {
	return &authv1.Organization{
		Id:        org.ID,
		Name:      org.Name,
		CreatedAt: org.CreatedAt.Format(time.RFC3339),
		UpdatedAt: org.UpdatedAt.Format(time.RFC3339),
	}
}

func membershipProto(membership domain.Membership) *authv1.Membership {
	return &authv1.Membership{
		OrgId:     membership.OrgID,
		UserId:    membership.UserID,
		Status:    string(membership.Status),
		Role:      string(membership.Role),
		CreatedAt: membership.CreatedAt.Format(time.RFC3339),
		UpdatedAt: membership.UpdatedAt.Format(time.RFC3339),
	}
}

// inviteProto renders an invitation for the wire. The token hash is not part of
// the message, so no handler can return it.
func inviteProto(invite domain.Invite) *authv1.Invite {
	out := &authv1.Invite{
		Id:        invite.ID,
		OrgId:     invite.OrgID,
		Email:     invite.Email,
		InviterId: invite.InviterID,
		Status:    string(invite.Status),
		ExpiresAt: invite.ExpiresAt.Format(time.RFC3339),
		CreatedAt: invite.CreatedAt.Format(time.RFC3339),
	}
	if invite.AcceptedAt != nil {
		out.AcceptedAt = invite.AcceptedAt.Format(time.RFC3339)
	}
	return out
}
