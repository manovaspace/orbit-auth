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

type Server struct {
	authv1.UnimplementedAuthServiceServer
	svc *application.Service
}

func New(svc *application.Service) *Server {
	return &Server{svc: svc}
}

func (s *Server) RequestOTP(ctx context.Context, req *authv1.RequestOTPRequest) (*authv1.RequestOTPResponse, error) {
	//nolint:staticcheck // SA1019: email kept for proto wire compatibility
	identifier, channel := resolveIdentifierChannel(req.GetIdentifier(), req.GetChannel(), req.GetEmail())
	expires, err := s.svc.RequestOTP(ctx, identifier, channel, req.GetCorrelationId())
	if err != nil {
		if errors.Is(err, application.ErrEmailOTPDisabled) || errors.Is(err, application.ErrMobileOTPDisabled) {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, err
	}
	return &authv1.RequestOTPResponse{ExpiresAt: expires.Format(time.RFC3339)}, nil
}

func (s *Server) VerifyOTP(ctx context.Context, req *authv1.VerifyOTPRequest) (*authv1.VerifyOTPResponse, error) {
	//nolint:staticcheck // SA1019: email kept for proto wire compatibility
	identifier, channel := resolveIdentifierChannel(req.GetIdentifier(), req.GetChannel(), req.GetEmail())
	access, refresh, exp, err := s.svc.VerifyOTP(ctx, identifier, channel, req.GetCode())
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, err.Error())
	}
	return &authv1.VerifyOTPResponse{Tokens: tokenResponse(access, refresh, exp)}, nil
}

func (s *Server) LoginWithPassword(ctx context.Context, req *authv1.LoginWithPasswordRequest) (*authv1.TokenResponse, error) {
	access, refresh, exp, err := s.svc.LoginWithPassword(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		if errors.Is(err, application.ErrInvalidCredentials) {
			return nil, status.Error(codes.Unauthenticated, "invalid credentials")
		}
		if errors.Is(err, application.ErrUserDisabled) {
			return nil, status.Error(codes.PermissionDenied, "user disabled")
		}
		return nil, err
	}
	return tokenResponse(access, refresh, exp), nil
}

func (s *Server) RefreshToken(ctx context.Context, req *authv1.RefreshTokenRequest) (*authv1.TokenResponse, error) {
	access, refresh, exp, err := s.svc.Refresh(ctx, req.GetRefreshToken())
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, err.Error())
	}
	return tokenResponse(access, refresh, exp), nil
}

func (s *Server) CreateApiToken(ctx context.Context, req *authv1.CreateApiTokenRequest) (*authv1.CreateApiTokenResponse, error) {
	var exp *time.Time
	if req.GetExpiresAt() != "" {
		t, err := time.Parse(time.RFC3339, req.GetExpiresAt())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid expires_at")
		}
		exp = &t
	}
	id, prefix, secret, err := s.svc.CreateApiToken(ctx, req.GetUserId(), req.GetName(), req.GetScopes(), exp)
	if err != nil {
		return nil, err
	}
	return &authv1.CreateApiTokenResponse{TokenId: id, Prefix: prefix, Secret: secret}, nil
}

func (s *Server) ListApiTokens(ctx context.Context, req *authv1.ListApiTokensRequest) (*authv1.ListApiTokensResponse, error) {
	list, err := s.svc.ListApiTokens(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	out := make([]*authv1.ApiTokenInfo, 0, len(list))
	for _, t := range list {
		info := &authv1.ApiTokenInfo{
			TokenId: t.ID,
			Name:    t.Name,
			Prefix:  t.Prefix,
			Scopes:  t.Scopes,
			CreatedAt: t.CreatedAt.Format(time.RFC3339),
		}
		if t.ExpiresAt != nil {
			info.ExpiresAt = t.ExpiresAt.Format(time.RFC3339)
		}
		out = append(out, info)
	}
	return &authv1.ListApiTokensResponse{Tokens: out}, nil
}

func (s *Server) RevokeApiToken(ctx context.Context, req *authv1.RevokeApiTokenRequest) (*authv1.RevokeApiTokenResponse, error) {
	if err := s.svc.RevokeApiToken(ctx, req.GetUserId(), req.GetTokenId()); err != nil {
		return nil, err
	}
	return &authv1.RevokeApiTokenResponse{}, nil
}

func (s *Server) ValidateApiToken(ctx context.Context, req *authv1.ValidateApiTokenRequest) (*authv1.ValidateApiTokenResponse, error) {
	userID, scopes, err := s.svc.ValidateApiToken(ctx, req.GetSecret())
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}
	return &authv1.ValidateApiTokenResponse{UserId: userID, Scopes: scopes}, nil
}

func tokenResponse(access, refresh string, exp time.Time) *authv1.TokenResponse {
	return &authv1.TokenResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    exp.Format(time.RFC3339),
	}
}

func resolveIdentifierChannel(identifier, channel, legacyEmail string) (string, string) {
	identifier = strings.TrimSpace(identifier)
	channel = strings.TrimSpace(channel)
	if identifier == "" && legacyEmail != "" {
		identifier = strings.TrimSpace(legacyEmail)
	}
	if channel == "" && identifier != "" {
		if strings.Contains(identifier, "@") {
			channel = domain.ChannelEmail
		} else {
			channel = domain.ChannelSMS
		}
	}
	return identifier, channel
}
