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
	authorizationURL, state, err := s.svc.StartGoogleSignIn(ctx, req.GetRedirectUri(), req.GetReturnTo())
	if err != nil {
		return nil, googleError(err)
	}
	return &authv1.StartGoogleSignInResponse{AuthorizationUrl: authorizationURL, State: state}, nil
}

// CompleteGoogleSignIn exchanges the code and returns the token pair with the
// return path. A bad state, a failed exchange and a refused account are fixed
// statuses that do not say which check failed.
func (s *Server) CompleteGoogleSignIn(ctx context.Context, req *authv1.CompleteGoogleSignInRequest) (*authv1.CompleteGoogleSignInResponse, error) {
	access, refresh, exp, returnTo, err := s.svc.CompleteGoogleSignIn(ctx, req.GetState(), req.GetCode())
	if err != nil {
		return nil, googleError(err)
	}
	return &authv1.CompleteGoogleSignInResponse{
		Tokens:   tokenResponse(access, refresh, exp),
		ReturnTo: returnTo,
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
	default:
		slog.Error("google sign-in failed", "error", err)
		return status.Error(codes.Internal, "internal error")
	}
}
