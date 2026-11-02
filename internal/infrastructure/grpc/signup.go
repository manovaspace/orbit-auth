package grpc

import (
	"context"
	"errors"
	"log/slog"
	"time"

	authv1 "github.com/manovaspace/orbit-auth/api/auth/v1"
	"github.com/manovaspace/orbit-auth/internal/application"
	"github.com/manovaspace/orbit-auth/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SignupService is the port the gRPC adapter needs from the application layer.
type SignupService interface {
	RegisterWithEmail(ctx context.Context, email, password, displayName, locale, orgName, inviteToken string) error
	VerifySignup(ctx context.Context, email, code, password string) (access, refresh string, expires time.Time, err error)
}

// SignupServer implements the SignupService gRPC API. Both calls are
// unauthenticated; the access-token interceptor only protects OrgService.
type SignupServer struct {
	authv1.UnimplementedSignupServiceServer
	svc SignupService
}

func NewSignup(svc SignupService) *SignupServer {
	return &SignupServer{svc: svc}
}

func (s *SignupServer) RegisterWithEmail(ctx context.Context, req *authv1.RegisterWithEmailRequest) (*authv1.RegisterWithEmailResponse, error) {
	err := s.svc.RegisterWithEmail(
		ctx,
		req.GetEmail(),
		req.GetPassword(),
		req.GetDisplayName(),
		req.GetLocale(),
		req.GetOrgName(),
		req.GetInviteToken(),
	)
	if err != nil {
		return nil, signupError(err)
	}
	return &authv1.RegisterWithEmailResponse{}, nil
}

func (s *SignupServer) VerifySignup(ctx context.Context, req *authv1.VerifySignupRequest) (*authv1.VerifySignupResponse, error) {
	access, refresh, expires, err := s.svc.VerifySignup(ctx, req.GetEmail(), req.GetCode(), req.GetPassword())
	if err != nil {
		switch {
		case errors.Is(err, application.ErrSignupDisabled):
			return nil, status.Error(codes.FailedPrecondition, "sign-up is disabled")
		case errors.Is(err, application.ErrInvalidOTPCode), errors.Is(err, application.ErrTooManyOTPAttempts):
			return nil, status.Error(codes.Unauthenticated, err.Error())
		default:
			slog.Error("sign-up verification failed", "error", err)
			return nil, status.Error(codes.Internal, "internal error")
		}
	}
	return &authv1.VerifySignupResponse{Tokens: tokenResponse(access, refresh, expires)}, nil
}

func signupError(err error) error {
	switch {
	case errors.Is(err, application.ErrSignupDisabled):
		return status.Error(codes.FailedPrecondition, "sign-up is disabled")
	case errors.Is(err, domain.ErrInvalidInvite):
		return status.Error(codes.InvalidArgument, domain.ErrInvalidInvite.Error())
	case errors.Is(err, domain.ErrInvalidEmail):
		return status.Error(codes.InvalidArgument, domain.ErrInvalidEmail.Error())
	case errors.Is(err, application.ErrPasswordRequired):
		return status.Error(codes.InvalidArgument, application.ErrPasswordRequired.Error())
	default:
		slog.Error("sign-up registration failed", "error", err)
		return status.Error(codes.Internal, "internal error")
	}
}
