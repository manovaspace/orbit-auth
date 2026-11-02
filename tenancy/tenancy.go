// Package tenancy carries the request's active organization through a Go
// context and into a Postgres transaction.
//
// It does not verify tokens and it does not resolve memberships. Products
// verify the access token with the token package (or the gateway does it) and
// hand tenancy the verified token.AccessClaims. A missing org claim means "no
// active organization"; a malformed org claim is never an authorization
// decision.
package tenancy

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/manovaspace/orbit-auth/token"
)

// ErrOrgRequired reports that a code path that needs an active organization
// ran without one in context.
var ErrOrgRequired = errors.New("tenancy: organization required")

// errOrgMalformed reports a malformed org claim. Middleware maps it to an
// authentication failure, because a token with a broken claim is not trusted.
var errOrgMalformed = errors.New("tenancy: organization claim is not a UUID")

type orgContextKey struct{}

// WithOrg returns a context carrying orgID as the active organization.
// WithOrg(ctx, uuid.Nil) leaves the context without an organization.
func WithOrg(ctx context.Context, orgID uuid.UUID) context.Context {
	if orgID == uuid.Nil {
		return context.WithValue(ctx, orgContextKey{}, uuid.Nil)
	}
	return context.WithValue(ctx, orgContextKey{}, orgID)
}

// OrgFrom returns the active organization from context. It reports ok=false
// when no organization is present.
func OrgFrom(ctx context.Context) (uuid.UUID, bool) {
	value := ctx.Value(orgContextKey{})
	orgID, ok := value.(uuid.UUID)
	if !ok || orgID == uuid.Nil {
		return uuid.Nil, false
	}
	return orgID, true
}

// MustOrg returns the active organization or panics. Use it only on code
// paths guarded by RequireOrg or UnaryServerInterceptor.
func MustOrg(ctx context.Context) uuid.UUID {
	orgID, ok := OrgFrom(ctx)
	if !ok {
		panic("tenancy: organization required in context")
	}
	return orgID
}

// Option configures the HTTP middleware and the gRPC interceptor.
type Option func(*options)

type options struct {
	fallbackOrg uuid.UUID
}

// WithFallbackOrg makes single-tenant products use orgID when the verified
// claims carry no org. The fallback is temporary: once the product issues org
// claims for every active organization, remove it. WithFallbackOrg(uuid.Nil)
// disables the fallback.
func WithFallbackOrg(orgID uuid.UUID) Option {
	return func(cfg *options) {
		cfg.fallbackOrg = orgID
	}
}

func newOptions(opts []Option) options {
	var cfg options
	for _, option := range opts {
		if option != nil {
			option(&cfg)
		}
	}
	return cfg
}

// resolveOrg applies the org claim rules shared by HTTP and gRPC: a missing
// claim may use the fallback, a malformed claim never does.
func resolveOrg(claims token.AccessClaims, fallbackOrg uuid.UUID) (uuid.UUID, error) {
	raw := strings.TrimSpace(claims.OrgID)
	if raw == "" {
		if fallbackOrg != uuid.Nil {
			return fallbackOrg, nil
		}
		return uuid.Nil, ErrOrgRequired
	}
	orgID, err := uuid.Parse(raw)
	if err != nil || orgID == uuid.Nil {
		return uuid.Nil, errOrgMalformed
	}
	return orgID, nil
}
