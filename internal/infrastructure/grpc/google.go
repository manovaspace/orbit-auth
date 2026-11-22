package grpc

import (
	"context"
	"errors"
	"log/slog"

	authv1 "github.com/manovaspace/orbit-auth/api/auth/v1"
	"github.com/manovaspace/orbit-auth/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StartGoogleSignIn returns the Google authorization URL and the state. Both
// RPCs run before any sign-in, so neither needs a bearer token.
func (s *Server) StartGoogleSignIn(ctx context.Context, req *authv1.StartGoogleSignInRequest) (*authv1.StartGoogleSignInResponse, error) {
	authorizationURL, state, err := s.svc.StartGoogleSignIn(ctx, req.GetRedirectUri(), req.GetReturnTo(), req.GetInviteToken())
	if err != nil {
		return nil, googleError(err)
	}
	return &authv1.StartGoogleSignInResponse{AuthorizationUrl: authorizationURL, State: state}, nil
}

// CompleteGoogleSignIn exchanges the code and returns the token pair with the
// return path, and for an invitation sign-in the invitation it accepted. A bad
// state, a failed exchange and a refused account are fixed statuses that do not
// say which check failed.
func (s *Server) CompleteGoogleSignIn(ctx context.Context, req *authv1.CompleteGoogleSignInRequest) (*authv1.CompleteGoogleSignInResponse, error) {
	res, err := s.svc.CompleteGoogleSignIn(ctx, req.GetState(), req.GetCode())
	if err != nil {
		return nil, googleError(err)
	}
	return &authv1.CompleteGoogleSignInResponse{
		Tokens:        tokenResponse(res.Access, res.Refresh, res.Expires),
		ReturnTo:      res.ReturnTo,
		InviteId:      res.InviteID,
		OrgId:         res.OrgID,
		OrgSlug:       res.OrgSlug,
		Role:          string(res.Role),
		AlreadyMember: res.AlreadyMember,
	}, nil
}

// googleError maps Google sign-in errors to gRPC statuses. Only the messages
// named here reach a client.
func googleError(err error) error {
	switch {
	case errors.Is(err, domain.ErrGoogleDisabled):
		return status.Error(codes.FailedPrecondition, domain.ErrGoogleDisabled.Error())
	case errors.Is(err, domain.ErrInvalidRedirectURI), errors.Is(err, domain.ErrInvalidReturnTo):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, domain.ErrInvalidGoogleState), errors.Is(err, domain.ErrGoogleExchangeFailed):
		return status.Error(codes.Unauthenticated, "google sign-in failed")
	case errors.Is(err, domain.ErrGoogleAccountNotAllowed):
		return status.Error(codes.PermissionDenied, "account not allowed")
	case errors.Is(err, domain.ErrInvalidInvite):
		return status.Error(codes.InvalidArgument, domain.ErrInvalidInvite.Error())
	case errors.Is(err, domain.ErrInviteNeedsPassword):
		return status.Error(codes.FailedPrecondition, domain.ErrInviteNeedsPassword.Error())
	case errors.Is(err, domain.ErrInviteEmailMismatch):
		return status.Error(codes.FailedPrecondition, domain.ErrInviteEmailMismatch.Error())
	case errors.Is(err, domain.ErrInviteSignupDisabled):
		return status.Error(codes.FailedPrecondition, domain.ErrInviteSignupDisabled.Error())
	default:
		slog.Error("google sign-in failed", "error", err)
		return status.Error(codes.Internal, "internal error")
	}
}
