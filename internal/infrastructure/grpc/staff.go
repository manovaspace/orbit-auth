package grpc

import (
	"context"
	"errors"
	"strings"
	"time"

	authv1 "github.com/manovaspace/orbit-auth/api/auth/v1"
	"github.com/manovaspace/orbit-auth/internal/application"
	"github.com/manovaspace/orbit-auth/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StaffService is the port the staff gRPC adapter needs from the application
// layer.
type StaffService interface {
	StaffListOrganizations(ctx context.Context, in application.StaffListInput) (application.StaffOrgPage, error)
	StaffGetOrganization(ctx context.Context, orgID string) (application.StaffOrgDetail, error)
	StaffProvisionOrganization(ctx context.Context, actor domain.StaffActor, in application.ProvisionOrgInput) (application.ProvisionOrgResult, error)
	StaffSuspendOrganization(ctx context.Context, actor domain.StaffActor, orgID string) (domain.StaffOrgSummary, error)
	StaffReactivateOrganization(ctx context.Context, actor domain.StaffActor, orgID string) (domain.StaffOrgSummary, error)
	StaffSetOrganizationSlug(ctx context.Context, actor domain.StaffActor, orgID, slug string) (domain.StaffOrgSummary, error)
	StaffReinviteOwner(ctx context.Context, actor domain.StaffActor, orgID string, target domain.InviteTarget) (domain.Invite, string, error)
}

// StaffOrgServer implements StaffOrgService. It is reachable only through the
// gateway: StaffServiceUnaryServerInterceptor checks the service token, the
// staff subject and, for writes, the reason before any handler runs.
type StaffOrgServer struct {
	authv1.UnimplementedStaffOrgServiceServer
	svc StaffService
}

func NewStaffOrg(svc StaffService) *StaffOrgServer {
	return &StaffOrgServer{svc: svc}
}

func (s *StaffOrgServer) ListOrganizations(ctx context.Context, req *authv1.ListOrganizationsRequest) (*authv1.ListOrganizationsResponse, error) {
	page, err := s.svc.StaffListOrganizations(ctx, application.StaffListInput{
		Query:     req.GetQuery(),
		Status:    req.GetStatus(),
		PageSize:  int(req.GetPageSize()),
		PageToken: req.GetPageToken(),
	})
	if err != nil {
		return nil, staffError(err)
	}
	out := make([]*authv1.StaffOrganization, 0, len(page.Organizations))
	for _, summary := range page.Organizations {
		out = append(out, staffOrganizationProto(summary))
	}
	return &authv1.ListOrganizationsResponse{Organizations: out, NextPageToken: page.NextPageToken}, nil
}

func (s *StaffOrgServer) GetOrganization(ctx context.Context, req *authv1.GetOrganizationRequest) (*authv1.GetOrganizationResponse, error) {
	orgID, err := staffOrgID(req.GetOrgId())
	if err != nil {
		return nil, err
	}
	detail, err := s.svc.StaffGetOrganization(ctx, orgID)
	if err != nil {
		return nil, staffError(err)
	}
	members := make([]*authv1.StaffMember, 0, len(detail.Members))
	for _, member := range detail.Members {
		members = append(members, &authv1.StaffMember{
			UserId:   member.UserID,
			Role:     string(member.Role),
			Status:   string(member.Status),
			JoinedAt: member.CreatedAt.Format(time.RFC3339),
		})
	}
	invites := make([]*authv1.StaffInvite, 0, len(detail.PendingInvites))
	for _, invite := range detail.PendingInvites {
		invites = append(invites, staffInviteProto(invite))
	}
	return &authv1.GetOrganizationResponse{
		Organization:   staffOrganizationProto(detail.Summary),
		Members:        members,
		PendingInvites: invites,
	}, nil
}

func (s *StaffOrgServer) ProvisionOrganization(ctx context.Context, req *authv1.ProvisionOrganizationRequest) (*authv1.ProvisionOrganizationResponse, error) {
	actor, err := staffActor(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.svc.StaffProvisionOrganization(ctx, actor, application.ProvisionOrgInput{
		Name:           req.GetName(),
		Slug:           req.GetSlug(),
		IdempotencyKey: req.GetIdempotencyKey(),
		Target:         domain.InviteTarget{Email: req.GetOwnerEmail(), Phone: req.GetOwnerPhone()},
	})
	if err != nil {
		return nil, staffError(err)
	}
	return &authv1.ProvisionOrganizationResponse{
		Organization: staffOrganizationProto(result.Summary),
		OwnerInvite:  staffInviteProto(result.Invite),
		Token:        result.Token,
	}, nil
}

func (s *StaffOrgServer) SuspendOrganization(ctx context.Context, req *authv1.SuspendOrganizationRequest) (*authv1.SuspendOrganizationResponse, error) {
	actor, err := staffActor(ctx)
	if err != nil {
		return nil, err
	}
	orgID, err := staffOrgID(req.GetOrgId())
	if err != nil {
		return nil, err
	}
	summary, err := s.svc.StaffSuspendOrganization(ctx, actor, orgID)
	if err != nil {
		return nil, staffError(err)
	}
	return &authv1.SuspendOrganizationResponse{Organization: staffOrganizationProto(summary)}, nil
}

func (s *StaffOrgServer) ReactivateOrganization(ctx context.Context, req *authv1.ReactivateOrganizationRequest) (*authv1.ReactivateOrganizationResponse, error) {
	actor, err := staffActor(ctx)
	if err != nil {
		return nil, err
	}
	orgID, err := staffOrgID(req.GetOrgId())
	if err != nil {
		return nil, err
	}
	summary, err := s.svc.StaffReactivateOrganization(ctx, actor, orgID)
	if err != nil {
		return nil, staffError(err)
	}
	return &authv1.ReactivateOrganizationResponse{Organization: staffOrganizationProto(summary)}, nil
}

func (s *StaffOrgServer) SetOrganizationSlug(ctx context.Context, req *authv1.SetOrganizationSlugRequest) (*authv1.SetOrganizationSlugResponse, error) {
	actor, err := staffActor(ctx)
	if err != nil {
		return nil, err
	}
	orgID, err := staffOrgID(req.GetOrgId())
	if err != nil {
		return nil, err
	}
	summary, err := s.svc.StaffSetOrganizationSlug(ctx, actor, orgID, req.GetSlug())
	if err != nil {
		return nil, staffError(err)
	}
	return &authv1.SetOrganizationSlugResponse{Organization: staffOrganizationProto(summary)}, nil
}

func (s *StaffOrgServer) ReinviteOwner(ctx context.Context, req *authv1.ReinviteOwnerRequest) (*authv1.ReinviteOwnerResponse, error) {
	actor, err := staffActor(ctx)
	if err != nil {
		return nil, err
	}
	orgID, err := staffOrgID(req.GetOrgId())
	if err != nil {
		return nil, err
	}
	invite, token, err := s.svc.StaffReinviteOwner(ctx, actor, orgID, domain.InviteTarget{Email: req.GetOwnerEmail(), Phone: req.GetOwnerPhone()})
	if err != nil {
		return nil, staffError(err)
	}
	return &authv1.ReinviteOwnerResponse{Invite: staffInviteProto(invite), Token: token}, nil
}

// staffActor returns the actor the interceptor verified.
func staffActor(ctx context.Context) (domain.StaffActor, error) {
	actor, ok := staffActorFromContext(ctx)
	if !ok {
		return domain.StaffActor{}, status.Error(codes.Unauthenticated, "staff authentication required")
	}
	return actor, nil
}

func staffOrgID(raw string) (string, error) {
	orgID := strings.TrimSpace(raw)
	if !isUUID(orgID) {
		return "", status.Error(codes.InvalidArgument, "invalid org_id")
	}
	return orgID, nil
}

// staffError maps staff-specific failures and falls back to the organization
// mapping for everything else.
func staffError(err error) error {
	switch {
	case errors.Is(err, domain.ErrStaffSubjectRequired):
		return status.Error(codes.InvalidArgument, "staff subject required")
	case errors.Is(err, domain.ErrStaffReasonRequired):
		return status.Error(codes.InvalidArgument, "staff reason required")
	case errors.Is(err, domain.ErrIdempotencyKeyRequired),
		errors.Is(err, domain.ErrInvalidOrgStatus),
		errors.Is(err, domain.ErrInvalidPageSize),
		errors.Is(err, domain.ErrInvalidPageToken):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, domain.ErrIdempotencyConflict):
		return status.Error(codes.AlreadyExists, err.Error())
	default:
		return orgError(err)
	}
}

func staffOrganizationProto(summary domain.StaffOrgSummary) *authv1.StaffOrganization {
	org := summary.Organization
	out := &authv1.StaffOrganization{
		Id:              org.ID,
		Name:            org.Name,
		Slug:            org.Slug,
		Status:          string(org.Status),
		CreatedAt:       org.CreatedAt.Format(time.RFC3339),
		MemberCount:     int64(summary.MemberCount),
		OwnerUserIds:    summary.OwnerUserIDs,
		SuspendedReason: org.SuspendedReason,
	}
	if out.OwnerUserIds == nil {
		out.OwnerUserIds = []string{}
	}
	if org.SuspendedAt != nil {
		out.SuspendedAt = org.SuspendedAt.Format(time.RFC3339)
	}
	return out
}

// staffInviteProto renders a pending invitation without its token hash.
func staffInviteProto(invite domain.Invite) *authv1.StaffInvite {
	return &authv1.StaffInvite{
		Id:        invite.ID,
		Email:     invite.Email,
		Phone:     invite.Phone,
		Role:      string(invite.Role),
		ExpiresAt: invite.ExpiresAt.Format(time.RFC3339),
	}
}
