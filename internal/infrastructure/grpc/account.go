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

// AccountService is the port the AccountService adapter needs from the
// application layer. Every method acts on the account of the caller.
type AccountService interface {
	GetSignInMethods(ctx context.Context, userID string) (domain.SignInMethods, error)
	SetPassword(ctx context.Context, userID, currentPassword, newPassword, refreshToken string) error
	StartGoogleLink(ctx context.Context, userID, redirectURI, returnTo string) (authorizationURL, state string, err error)
	CompleteGoogleLink(ctx context.Context, userID, state, code string) (application.GoogleLinkResult, error)
	UnlinkGoogle(ctx context.Context, userID string) error
	RevokeAllSessions(ctx context.Context, userID, refreshToken string) error
}

// AccountServer implements the AccountService gRPC API. The caller is the
// subject of the verified access token, so a request never names the account.
type AccountServer struct {
	authv1.UnimplementedAccountServiceServer
	svc AccountService
}

// NewAccount returns the AccountService handler over svc.
func NewAccount(svc AccountService) *AccountServer {
	return &AccountServer{svc: svc}
}

// GetSignInMethods returns how the caller signs in.
func (s *AccountServer) GetSignInMethods(ctx context.Context, _ *authv1.GetSignInMethodsRequest) (*authv1.GetSignInMethodsResponse, error) {
	userID, err := callerAccount(ctx)
	if err != nil {
		return nil, err
	}
	methods, err := s.svc.GetSignInMethods(ctx, userID)
	if err != nil {
		return nil, accountError(err)
	}
	out := &authv1.GetSignInMethodsResponse{
		HasPassword:    methods.HasPassword,
		Username:       methods.Username,
		Email:          methods.Email,
		EmailVerified:  methods.EmailVerified,
		Mobile:         methods.Mobile,
		MobileVerified: methods.MobileVerified,
	}
	if methods.Google != nil {
		out.Google = []*authv1.GoogleLink{{
			Email:       methods.Google.Email,
			LinkedAt:    methods.Google.LinkedAt.Format(time.RFC3339),
			LastLoginAt: methods.Google.LastLoginAt.Format(time.RFC3339),
		}}
	}
	return out, nil
}

// SetPassword sets or changes the caller's password.
func (s *AccountServer) SetPassword(ctx context.Context, req *authv1.SetPasswordRequest) (*authv1.SetPasswordResponse, error) {
	userID, err := callerAccount(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.svc.SetPassword(ctx, userID, req.GetCurrentPassword(), req.GetNewPassword(), req.GetRefreshToken()); err != nil {
		return nil, accountError(err)
	}
	return &authv1.SetPasswordResponse{}, nil
}

// StartGoogleLink starts linking a Google account to the caller.
func (s *AccountServer) StartGoogleLink(ctx context.Context, req *authv1.StartGoogleLinkRequest) (*authv1.StartGoogleLinkResponse, error) {
	userID, err := callerAccount(ctx)
	if err != nil {
		return nil, err
	}
	authorizationURL, state, err := s.svc.StartGoogleLink(ctx, userID, req.GetRedirectUri(), req.GetReturnTo())
	if err != nil {
		return nil, accountError(err)
	}
	return &authv1.StartGoogleLinkResponse{AuthorizationUrl: authorizationURL, State: state}, nil
}

// CompleteGoogleLink links the Google account of the code to the caller.
func (s *AccountServer) CompleteGoogleLink(ctx context.Context, req *authv1.CompleteGoogleLinkRequest) (*authv1.CompleteGoogleLinkResponse, error) {
	userID, err := callerAccount(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.svc.CompleteGoogleLink(ctx, userID, req.GetState(), req.GetCode())
	if err != nil {
		return nil, accountError(err)
	}
	return &authv1.CompleteGoogleLinkResponse{ReturnTo: res.ReturnTo, Email: res.Email}, nil
}

// UnlinkGoogle removes the caller's Google account.
func (s *AccountServer) UnlinkGoogle(ctx context.Context, _ *authv1.UnlinkGoogleRequest) (*authv1.UnlinkGoogleResponse, error) {
	userID, err := callerAccount(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.svc.UnlinkGoogle(ctx, userID); err != nil {
		return nil, accountError(err)
	}
	return &authv1.UnlinkGoogleResponse{}, nil
}

// RevokeAllSessions revokes the caller's sessions, keeping the one presented.
func (s *AccountServer) RevokeAllSessions(ctx context.Context, req *authv1.RevokeAllSessionsRequest) (*authv1.RevokeAllSessionsResponse, error) {
	userID, err := callerAccount(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.svc.RevokeAllSessions(ctx, userID, req.GetRefreshToken()); err != nil {
		return nil, accountError(err)
	}
	return &authv1.RevokeAllSessionsResponse{}, nil
}

// callerAccount returns the verified subject, or Unauthenticated when none is
// present. The interceptor rejects a missing token before a handler runs.
func callerAccount(ctx context.Context) (string, error) {
	userID, err := caller(ctx)
	if err != nil {
		return "", err
	}
	if !isUUID(userID) {
		return "", status.Error(codes.Unauthenticated, "authentication required")
	}
	return userID, nil
}

// accountError maps AccountService errors to gRPC statuses. Google sign-in
// errors use googleError. Only the messages named here reach a client.
func accountError(err error) error {
	switch {
	case errors.Is(err, application.ErrInvalidCredentials):
		return status.Error(codes.Unauthenticated, "invalid credentials")
	case errors.Is(err, application.ErrInvalidRefresh):
		return status.Error(codes.Unauthenticated, "invalid refresh token")
	case errors.Is(err, application.ErrPasswordRequired), errors.Is(err, application.ErrPasswordTooShort):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, domain.ErrNoPasswordIdentifier), errors.Is(err, domain.ErrLastSignInMethod):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, domain.ErrGoogleAlreadyLinked):
		return status.Error(codes.FailedPrecondition, domain.ErrGoogleAlreadyLinked.Error())
	case errors.Is(err, domain.ErrGoogleIdentityTaken):
		return status.Error(codes.AlreadyExists, domain.ErrGoogleIdentityTaken.Error())
	case errors.Is(err, domain.ErrGoogleNotLinked):
		return status.Error(codes.NotFound, domain.ErrGoogleNotLinked.Error())
	case errors.Is(err, domain.ErrAccountNotActive):
		return status.Error(codes.PermissionDenied, "account is not active")
	case errors.Is(err, domain.ErrUserNotFound):
		return status.Error(codes.NotFound, "user not found")
	case errors.Is(err, domain.ErrGoogleDisabled), errors.Is(err, domain.ErrInvalidRedirectURI), errors.Is(err, domain.ErrInvalidReturnTo),
		errors.Is(err, domain.ErrInvalidGoogleState), errors.Is(err, domain.ErrGoogleExchangeFailed), errors.Is(err, domain.ErrGoogleAccountNotAllowed):
		return googleError(err)
	default:
		slog.Error("account operation failed", "error", err)
		return status.Error(codes.Internal, "internal error")
	}
}
