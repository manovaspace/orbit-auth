package tenancy

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/manovaspace/orbit-auth/token"
)

// UnaryServerInterceptor returns a gRPC interceptor that requires an active
// organization. claims must return the already-verified access claims for the
// incoming request; tenancy does not verify tokens itself.
//
// A request without verified claims is codes.Unauthenticated. A verified
// request without an org claim is codes.PermissionDenied with message
// "org_required" unless a fallback is configured. A malformed org claim is
// codes.Unauthenticated and is never replaced by the fallback.
func UnaryServerInterceptor(claims func(ctx context.Context) (token.AccessClaims, bool), opts ...Option) grpc.UnaryServerInterceptor {
	cfg := newOptions(opts)
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if claims == nil {
			return nil, status.Error(codes.Unauthenticated, "unauthenticated")
		}
		verified, ok := claims(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "unauthenticated")
		}
		orgID, err := resolveOrg(verified, cfg.fallbackOrg)
		if err != nil {
			if errors.Is(err, ErrOrgRequired) {
				return nil, status.Error(codes.PermissionDenied, "org_required")
			}
			return nil, status.Error(codes.Unauthenticated, "invalid_org")
		}
		return handler(WithOrg(ctx, orgID), req)
	}
}
