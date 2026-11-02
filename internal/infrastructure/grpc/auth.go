package grpc

import (
	"context"
	"errors"
	"strings"

	orbittoken "github.com/manovaspace/orbit-auth/token"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const authorizationMetadataKey = "authorization"

type callerIDContextKey struct{}

// withCallerID records the authenticated subject for the handlers that need it.
func withCallerID(ctx context.Context, callerID string) context.Context {
	return context.WithValue(ctx, callerIDContextKey{}, callerID)
}

// callerIDFromContext returns the authenticated subject placed there by
// AccessTokenUnaryServerInterceptor.
func callerIDFromContext(ctx context.Context) (string, bool) {
	callerID, ok := ctx.Value(callerIDContextKey{}).(string)
	callerID = strings.TrimSpace(callerID)
	if !ok || callerID == "" {
		return "", false
	}
	return callerID, true
}

// AccessTokenUnaryServerInterceptor verifies the bearer access token on
// OrgService calls before the handler runs. It puts the verified subject in the
// context, so a handler never reads the acting user from the request body.
// AuthService calls keep their existing unauthenticated contract.
func AccessTokenUnaryServerInterceptor(keys orbittoken.KeySet) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !strings.HasPrefix(info.FullMethod, "/auth.v1.OrgService/") {
			return handler(ctx, req)
		}
		raw, err := bearerToken(ctx)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "authentication required")
		}
		claims, err := orbittoken.ParseAccessToken(raw, keys)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "invalid or expired access token")
		}
		return handler(withCallerID(ctx, claims.Subject), req)
	}
}

func bearerToken(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", errors.New("missing metadata")
	}
	values := md.Get(authorizationMetadataKey)
	if len(values) == 0 {
		return "", errors.New("missing authorization metadata")
	}
	parts := strings.SplitN(strings.TrimSpace(values[0]), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", errors.New("authorization metadata is not a bearer token")
	}
	raw := strings.TrimSpace(parts[1])
	if raw == "" {
		return "", errors.New("bearer token is empty")
	}
	return raw, nil
}
