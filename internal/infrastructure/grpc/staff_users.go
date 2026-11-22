package grpc

import (
	"context"
	"strings"
	"time"

	authv1 "github.com/manovaspace/orbit-auth/api/auth/v1"
	"github.com/manovaspace/orbit-auth/internal/application"
	"github.com/manovaspace/orbit-auth/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StaffUserService is the port the staff user gRPC adapter needs from the
// application layer.
type StaffUserService interface {
	ListStaffUsers(ctx context.Context, in application.StaffUserListInput) (application.StaffUserPage, error)
	GetStaffUser(ctx context.Context, userID string) (domain.StaffUser, []domain.StaffUserMembership, error)
	DisableStaffUser(ctx context.Context, userID string, by domain.StaffActor) (bool, error)
	EnableStaffUser(ctx context.Context, userID string, by domain.StaffActor) (bool, error)
	ListStaffAuditEvents(ctx context.Context, in application.StaffAuditInput) (application.StaffAuditPage, error)
}

// StaffUserServer implements StaffUserService. Like StaffOrgServer it is
// reachable only through the gateway: StaffServiceUnaryServerInterceptor checks
// the service token, the staff subject and, for writes, the reason first.
type StaffUserServer struct {
	authv1.UnimplementedStaffUserServiceServer
	svc StaffUserService
}

func NewStaffUser(svc StaffUserService) *StaffUserServer {
	return &StaffUserServer{svc: svc}
}

func (s *StaffUserServer) ListUsers(ctx context.Context, req *authv1.ListUsersRequest) (*authv1.ListUsersResponse, error) {
	page, err := s.svc.ListStaffUsers(ctx, application.StaffUserListInput{
		Query:     req.GetQuery(),
		Status:    req.GetStatus(),
		PageSize:  int(req.GetPageSize()),
		PageToken: req.GetPageToken(),
	})
	if err != nil {
		return nil, staffError(err)
	}
	out := make([]*authv1.StaffUser, 0, len(page.Users))
	for _, user := range page.Users {
		out = append(out, staffUserProto(user))
	}
	return &authv1.ListUsersResponse{Users: out, NextPageToken: page.NextPageToken}, nil
}

func (s *StaffUserServer) GetUser(ctx context.Context, req *authv1.GetUserRequest) (*authv1.GetUserResponse, error) {
	userID, err := staffUserID(req.GetUserId())
	if err != nil {
		return nil, err
	}
	user, memberships, err := s.svc.GetStaffUser(ctx, userID)
	if err != nil {
		return nil, staffError(err)
	}
	out := make([]*authv1.StaffUserMembership, 0, len(memberships))
	for _, m := range memberships {
		out = append(out, staffMembershipProto(m))
	}
	return &authv1.GetUserResponse{User: staffUserProto(user), Memberships: out}, nil
}

func (s *StaffUserServer) DisableUser(ctx context.Context, req *authv1.DisableUserRequest) (*authv1.DisableUserResponse, error) {
	actor, err := staffActor(ctx)
	if err != nil {
		return nil, err
	}
	userID, err := staffUserID(req.GetUserId())
	if err != nil {
		return nil, err
	}
	changed, err := s.svc.DisableStaffUser(ctx, userID, actor)
	if err != nil {
		return nil, staffError(err)
	}
	return &authv1.DisableUserResponse{Changed: changed}, nil
}

func (s *StaffUserServer) EnableUser(ctx context.Context, req *authv1.EnableUserRequest) (*authv1.EnableUserResponse, error) {
	actor, err := staffActor(ctx)
	if err != nil {
		return nil, err
	}
	userID, err := staffUserID(req.GetUserId())
	if err != nil {
		return nil, err
	}
	changed, err := s.svc.EnableStaffUser(ctx, userID, actor)
	if err != nil {
		return nil, staffError(err)
	}
	return &authv1.EnableUserResponse{Changed: changed}, nil
}

func (s *StaffUserServer) ListAuditEvents(ctx context.Context, req *authv1.ListAuditEventsRequest) (*authv1.ListAuditEventsResponse, error) {
	in := application.StaffAuditInput{
		PageSize:  int(req.GetPageSize()),
		PageToken: req.GetPageToken(),
	}
	if raw := req.GetOrgId(); raw != "" {
		orgID, err := staffOrgID(raw)
		if err != nil {
			return nil, err
		}
		in.OrgID = orgID
	}
	if raw := req.GetUserId(); raw != "" {
		userID, err := staffUserID(raw)
		if err != nil {
			return nil, err
		}
		in.SubjectUserID = userID
	}
	page, err := s.svc.ListStaffAuditEvents(ctx, in)
	if err != nil {
		return nil, staffError(err)
	}
	out := make([]*authv1.AuditEvent, 0, len(page.Events))
	for _, event := range page.Events {
		out = append(out, auditEventProto(event))
	}
	return &authv1.ListAuditEventsResponse{Events: out, NextPageToken: page.NextPageToken}, nil
}

func staffUserID(raw string) (string, error) {
	userID := strings.TrimSpace(raw)
	if !isUUID(userID) {
		return "", status.Error(codes.InvalidArgument, "invalid user_id")
	}
	return userID, nil
}

func staffUserProto(user domain.StaffUser) *authv1.StaffUser {
	out := &authv1.StaffUser{
		Id:              user.ID,
		Email:           user.Email,
		Mobile:          user.Mobile,
		Username:        user.Username,
		DisplayName:     user.DisplayName,
		Status:          user.Status,
		EmailVerified:   user.EmailVerified,
		MobileVerified:  user.MobileVerified,
		HasPassword:     user.HasPassword,
		GoogleLinked:    user.GoogleLinked,
		CreatedAt:       user.CreatedAt.Format(time.RFC3339),
		DisabledReason:  user.DisabledReason,
		MembershipCount: int64(user.MembershipCount),
	}
	if user.LastLoginAt != nil {
		out.LastLoginAt = user.LastLoginAt.Format(time.RFC3339)
	}
	if user.DisabledAt != nil {
		out.DisabledAt = user.DisabledAt.Format(time.RFC3339)
	}
	return out
}

func staffMembershipProto(m domain.StaffUserMembership) *authv1.StaffUserMembership {
	return &authv1.StaffUserMembership{
		OrgId:     m.OrgID,
		OrgName:   m.OrgName,
		OrgSlug:   m.OrgSlug,
		OrgStatus: string(m.OrgStatus),
		Role:      string(m.Role),
		Status:    string(m.Status),
		JoinedAt:  m.JoinedAt.Format(time.RFC3339),
	}
}

func auditEventProto(e domain.AuditEvent) *authv1.AuditEvent {
	return &authv1.AuditEvent{
		Id:            e.ID,
		OccurredAt:    e.OccurredAt.Format(time.RFC3339Nano),
		Action:        e.Action,
		ActorKind:     e.ActorKind,
		ActorId:       e.ActorID,
		OrgId:         e.OrgID,
		SubjectUserId: e.SubjectUserID,
		InviteId:      e.InviteID,
		Reason:        e.Reason,
		Details:       e.Details,
	}
}
