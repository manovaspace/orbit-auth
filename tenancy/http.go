package tenancy

import (
	"errors"
	"net/http"

	"github.com/manovaspace/orbit-auth/token"
)

// ClaimsFunc returns the already-verified access claims for a request. The
// product's authentication middleware is responsible for verifying the token
// and putting the claims where ClaimsFunc can find them. Returning ok=false
// means the request is unauthenticated.
type ClaimsFunc func(*http.Request) (token.AccessClaims, bool)

// RequireOrg is net/http middleware that requires an active organization. It
// reads verified claims through claims and adds the organization to the
// request context for the next handler.
//
// A request without verified claims is 401 {"error":"unauthenticated"}. A
// verified request without an org claim is 403 {"error":"org_required"} unless
// a fallback is configured. A malformed org claim is 401
// {"error":"invalid_org"} and is never replaced by the fallback.
func RequireOrg(claims ClaimsFunc, opts ...Option) func(http.Handler) http.Handler {
	cfg := newOptions(opts)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if claims == nil {
				writeJSONError(w, http.StatusUnauthorized, "unauthenticated")
				return
			}
			verified, ok := claims(r)
			if !ok {
				writeJSONError(w, http.StatusUnauthorized, "unauthenticated")
				return
			}
			orgID, err := resolveOrg(verified, cfg.fallbackOrg)
			if err != nil {
				if errors.Is(err, ErrOrgRequired) {
					writeJSONError(w, http.StatusForbidden, "org_required")
					return
				}
				writeJSONError(w, http.StatusUnauthorized, "invalid_org")
				return
			}
			next.ServeHTTP(w, r.WithContext(WithOrg(r.Context(), orgID)))
		})
	}
}

func writeJSONError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + code + `"}`))
}
