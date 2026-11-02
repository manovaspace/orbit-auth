package tenancy_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/manovaspace/orbit-auth/tenancy"
	"github.com/manovaspace/orbit-auth/token"
)

func claimsFrom(claims token.AccessClaims, ok bool) tenancy.ClaimsFunc {
	return func(*http.Request) (token.AccessClaims, bool) {
		return claims, ok
	}
}

func handlerForOrg(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		orgID, ok := tenancy.OrgFrom(r.Context())
		if !ok {
			t.Error("handler has no organization in context")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("X-Org-ID", orgID.String())
		w.WriteHeader(http.StatusNoContent)
	})
}

func TestRequireOrgWithClaim(t *testing.T) {
	orgID := uuid.New()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)

	tenancy.RequireOrg(claimsFrom(token.AccessClaims{OrgID: orgID.String()}, true))(handlerForOrg(t)).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if got := recorder.Header().Get("X-Org-ID"); got != orgID.String() {
		t.Fatalf("handler org = %q, want %q", got, orgID)
	}
}

func TestRequireOrgWithoutClaim(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)

	tenancy.RequireOrg(claimsFrom(token.AccessClaims{}, true))(handlerForOrg(t)).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
	if body := recorder.Body.String(); body != `{"error":"org_required"}` {
		t.Fatalf("body = %q", body)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q", contentType)
	}
}

func TestRequireOrgWithoutVerifiedClaims(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)

	tenancy.RequireOrg(claimsFrom(token.AccessClaims{}, false))(handlerForOrg(t)).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestRequireOrgWithFallback(t *testing.T) {
	fallback := uuid.New()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)

	tenancy.RequireOrg(claimsFrom(token.AccessClaims{}, true), tenancy.WithFallbackOrg(fallback))(handlerForOrg(t)).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if got := recorder.Header().Get("X-Org-ID"); got != fallback.String() {
		t.Fatalf("handler org = %q, want fallback %q", got, fallback)
	}
}

func TestRequireOrgFallbackDoesNotOverrideClaim(t *testing.T) {
	orgID := uuid.New()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)

	tenancy.RequireOrg(claimsFrom(token.AccessClaims{OrgID: orgID.String()}, true), tenancy.WithFallbackOrg(uuid.New()))(handlerForOrg(t)).ServeHTTP(recorder, request)

	if got := recorder.Header().Get("X-Org-ID"); got != orgID.String() {
		t.Fatalf("handler org = %q, want claim %q", got, orgID)
	}
}

func TestRequireOrgMalformedClaim(t *testing.T) {
	for _, claim := range []string{"not-a-uuid", uuid.Nil.String(), "   "} {
		t.Run(claim, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/", nil)

			tenancy.RequireOrg(
				claimsFrom(token.AccessClaims{OrgID: claim}, true),
				tenancy.WithFallbackOrg(uuid.New()),
			)(handlerForOrg(t)).ServeHTTP(recorder, request)

			// Blank is a missing claim, not a malformed one, so it falls back.
			if claim == "   " {
				if recorder.Code != http.StatusNoContent {
					t.Fatalf("blank claim status = %d, want %d", recorder.Code, http.StatusNoContent)
				}
				return
			}
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestRequireOrgNilClaimsFunc(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)

	tenancy.RequireOrg(nil)(handlerForOrg(t)).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestRequireOrgPassesRequestContext(t *testing.T) {
	orgID := uuid.New()
	key := struct{}{}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request = request.WithContext(context.WithValue(request.Context(), key, "kept"))

	var sawValue bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawValue = r.Context().Value(key) == "kept"
	})
	recorder := httptest.NewRecorder()

	tenancy.RequireOrg(claimsFrom(token.AccessClaims{OrgID: orgID.String()}, true))(next).ServeHTTP(recorder, request)

	if !sawValue {
		t.Fatal("middleware dropped the existing request context")
	}
}
