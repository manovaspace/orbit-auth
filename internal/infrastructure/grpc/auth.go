package grpc

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/manovaspace/orbit-auth/internal/domain"
	orbittoken "github.com/manovaspace/orbit-auth/token"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const authorizationMetadataKey = "authorization"

// Staff service-to-service metadata. The gateway sends all three; the service
// token is the only proof that the call came from the gateway.
const (
	serviceTokenMetadataKey = "x-orbit-service-token"
	staffSubjectMetadataKey = "x-staff-subject"
	// staffReasonBinaryMetadataKey carries the reason as UTF-8 text. gRPC sends
	// -bin metadata base64-encoded and delivers the raw bytes, so a reason may be
	// written in any language. The ASCII key is still read for older callers.
	staffReasonBinaryMetadataKey = "x-staff-reason-bin"
	staffReasonMetadataKey       = "x-staff-reason"
	staffReasonMaxRunes          = 500
	staffOrgServicePrefix        = "/auth.v1.StaffOrgService/"
)

// staffReadMethods are the StaffOrgService RPCs that change nothing and so need
// no reason. Every other StaffOrgService method is a write, which is the safe
// default for a method added later.
var staffReadMethods = map[string]struct{}{
	staffOrgServicePrefix + "ListOrganizations": {},
	staffOrgServicePrefix + "GetOrganization":   {},
}

type staffActorContextKey struct{}

func withStaffActor(ctx context.Context, actor domain.StaffActor) context.Context {
	return context.WithValue(ctx, staffActorContextKey{}, actor)
}

// staffActorFromContext returns the staff actor placed there by
// StaffServiceUnaryServerInterceptor.
func staffActorFromContext(ctx context.Context) (domain.StaffActor, bool) {
	actor, ok := ctx.Value(staffActorContextKey{}).(domain.StaffActor)
	return actor, ok
}

// StaffServiceUnaryServerInterceptor guards StaffOrgService. It fails closed:
// with an empty serviceToken every StaffOrgService call is Unavailable. Otherwise
// the call needs the service token (compared in constant time), a non-empty
// staff subject, and, for every write RPC, a non-empty staff reason. The subject
// and reason go into the context. Other services pass through untouched.
func StaffServiceUnaryServerInterceptor(serviceToken string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !strings.HasPrefix(info.FullMethod, staffOrgServicePrefix) {
			return handler(ctx, req)
		}
		if serviceToken == "" {
			return nil, status.Error(codes.Unavailable, "staff service disabled")
		}
		md, _ := metadata.FromIncomingContext(ctx)
		if !serviceTokenMatches(firstMetadataValue(md, serviceTokenMetadataKey), serviceToken) {
			return nil, status.Error(codes.Unauthenticated, "invalid service token")
		}
		actor := domain.StaffActor{Subject: strings.TrimSpace(firstMetadataValue(md, staffSubjectMetadataKey))}
		if actor.Subject == "" {
			return nil, status.Error(codes.InvalidArgument, "staff subject required")
		}
		if _, isRead := staffReadMethods[info.FullMethod]; !isRead {
			actor.Reason = strings.TrimSpace(firstMetadataValue(md, staffReasonBinaryMetadataKey))
			if actor.Reason == "" {
				actor.Reason = strings.TrimSpace(firstMetadataValue(md, staffReasonMetadataKey))
			}
			if actor.Reason == "" {
				return nil, status.Error(codes.InvalidArgument, "staff reason required")
			}
			if !utf8.ValidString(actor.Reason) || utf8.RuneCountInString(actor.Reason) > staffReasonMaxRunes {
				return nil, status.Error(codes.InvalidArgument, "staff reason must be UTF-8 text of at most 500 characters")
			}
		}
		return handler(withStaffActor(ctx, actor), req)
	}
}

// serviceTokenMatches compares digests so the comparison takes the same time
// whatever the presented token's length. An empty presented token never matches.
func serviceTokenMatches(presented, configured string) bool {
	if presented == "" {
		return false
	}
	got := sha256.Sum256([]byte(presented))
	want := sha256.Sum256([]byte(configured))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

func firstMetadataValue(md metadata.MD, key string) string {
	values := md.Get(key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

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
