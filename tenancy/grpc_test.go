package tenancy_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/manovaspace/orbit-auth/tenancy"
	"github.com/manovaspace/orbit-auth/token"
)

func grpcClaimsFrom(claims token.AccessClaims, ok bool) func(context.Context) (token.AccessClaims, bool) {
	return func(context.Context) (token.AccessClaims, bool) {
		return claims, ok
	}
}

func invoke(t *testing.T, interceptor grpc.UnaryServerInterceptor) (uuid.UUID, bool, error) {
	t.Helper()
	var (
		gotOrg uuid.UUID
		gotOK  bool
	)
	handler := func(ctx context.Context, _ any) (any, error) {
		gotOrg, gotOK = tenancy.OrgFrom(ctx)
		return "ok", nil
	}
	_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{}, handler)
	return gotOrg, gotOK, err
}

func TestUnaryServerInterceptorWithClaim(t *testing.T) {
	orgID := uuid.New()
	interceptor := tenancy.UnaryServerInterceptor(grpcClaimsFrom(token.AccessClaims{OrgID: orgID.String()}, true))

	gotOrg, ok, err := invoke(t, interceptor)
	if err != nil {
		t.Fatalf("interceptor error = %v", err)
	}
	if !ok || gotOrg != orgID {
		t.Fatalf("handler org = %v, %v, want %v, true", gotOrg, ok, orgID)
	}
}

func TestUnaryServerInterceptorWithoutClaim(t *testing.T) {
	interceptor := tenancy.UnaryServerInterceptor(grpcClaimsFrom(token.AccessClaims{}, true))

	_, _, err := invoke(t, interceptor)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code = %v, want %v", status.Code(err), codes.PermissionDenied)
	}
	if status.Convert(err).Message() != "org_required" {
		t.Fatalf("message = %q, want org_required", status.Convert(err).Message())
	}
}

func TestUnaryServerInterceptorWithFallback(t *testing.T) {
	fallback := uuid.New()
	interceptor := tenancy.UnaryServerInterceptor(grpcClaimsFrom(token.AccessClaims{}, true), tenancy.WithFallbackOrg(fallback))

	gotOrg, ok, err := invoke(t, interceptor)
	if err != nil {
		t.Fatalf("interceptor error = %v", err)
	}
	if !ok || gotOrg != fallback {
		t.Fatalf("handler org = %v, %v, want fallback %v, true", gotOrg, ok, fallback)
	}
}

func TestUnaryServerInterceptorMalformedClaim(t *testing.T) {
	interceptor := tenancy.UnaryServerInterceptor(
		grpcClaimsFrom(token.AccessClaims{OrgID: "not-a-uuid"}, true),
		tenancy.WithFallbackOrg(uuid.New()),
	)

	_, _, err := invoke(t, interceptor)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("code = %v, want %v", status.Code(err), codes.Unauthenticated)
	}
}

func TestUnaryServerInterceptorWithoutVerifiedClaims(t *testing.T) {
	interceptor := tenancy.UnaryServerInterceptor(grpcClaimsFrom(token.AccessClaims{}, false))

	_, _, err := invoke(t, interceptor)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("code = %v, want %v", status.Code(err), codes.Unauthenticated)
	}
}
