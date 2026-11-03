package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manovaspace/orbit-auth/internal/application"
	"github.com/manovaspace/orbit-auth/internal/domain"
	observability "github.com/manovaspace/orbit-observability"
)

type fakeFlagStore struct {
	flags []domain.FeatureFlag
	err   error
}

func (f fakeFlagStore) ListFeatureFlags(context.Context) ([]domain.FeatureFlag, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.flags, nil
}

func newTestServer(store domain.FeatureFlagStore) *Server {
	return New(application.NewFeatureFlags(store))
}

// These assertions are the "after" proof of the Fiber v3 migration. The same
// requests were captured against the previous net/http server before the
// migration:
//
//	GET  /healthz  200 application/json                     {"status":"ok"}\n
//	GET  /readyz   200 application/json                     {"status":"ok"}\n
//	GET  /missing  404 text/plain; charset=utf-8 nosniff   404 page not found\n
//	POST /healthz  200 application/json                     {"status":"ok"}\n
//	GET  /readyz   503 application/json (not ready)         {"message":"db down","status":"not_ready"}\n
func TestHealthRoutesMatchTheNetHTTPServer(t *testing.T) {
	observability.SetReadiness(true, "")
	t.Cleanup(func() { observability.SetReadiness(true, "") })

	tests := []struct {
		name        string
		method      string
		path        string
		notReady    bool
		wantStatus  int
		wantType    string
		wantNosniff string
		wantBody    string
	}{
		{
			name: "live", method: http.MethodGet, path: "/healthz",
			wantStatus: http.StatusOK, wantType: "application/json",
			wantBody: `{"status":"ok"}` + "\n",
		},
		{
			name: "ready", method: http.MethodGet, path: "/readyz",
			wantStatus: http.StatusOK, wantType: "application/json",
			wantBody: `{"status":"ok"}` + "\n",
		},
		{
			name: "missing", method: http.MethodGet, path: "/missing",
			wantStatus: http.StatusNotFound, wantType: "text/plain; charset=utf-8", wantNosniff: "nosniff",
			wantBody: "404 page not found\n",
		},
		{
			name: "post healthz still matches", method: http.MethodPost, path: "/healthz",
			wantStatus: http.StatusOK, wantType: "application/json",
			wantBody: `{"status":"ok"}` + "\n",
		},
		{
			name: "not ready", method: http.MethodGet, path: "/readyz", notReady: true,
			wantStatus: http.StatusServiceUnavailable, wantType: "application/json",
			wantBody: `{"message":"db down","status":"not_ready"}` + "\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.notReady {
				observability.SetReadiness(false, "db down")
				t.Cleanup(func() { observability.SetReadiness(true, "") })
			}
			srv := newTestServer(fakeFlagStore{})
			resp, err := srv.app.Test(httptest.NewRequest(tc.method, tc.path, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			if got := resp.Header.Get("Content-Type"); got != tc.wantType {
				t.Fatalf("content-type = %q, want %q", got, tc.wantType)
			}
			if got := resp.Header.Get("X-Content-Type-Options"); got != tc.wantNosniff {
				t.Fatalf("x-content-type-options = %q, want %q", got, tc.wantNosniff)
			}
			if string(body) != tc.wantBody {
				t.Fatalf("body = %q, want %q", body, tc.wantBody)
			}
		})
	}
}

func TestFlagsReturnsTheSeededShape(t *testing.T) {
	updatedAt := time.Date(2026, 11, 3, 12, 0, 0, 123456789, time.UTC)
	srv := newTestServer(fakeFlagStore{flags: []domain.FeatureFlag{{
		Name:        "manova.frontend.locale_switcher",
		Enabled:     true,
		Description: "Locale switcher in the Orbit frontend template",
		UpdatedAt:   updatedAt,
	}}})

	resp, body := getFlags(t, srv)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q, want application/json", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("cache-control = %q, want no-cache", got)
	}
	want := `{"version":"2026-11-03T12:00:00.123456789Z","flags":[{"name":"manova.frontend.locale_switcher","enabled":true,"description":"Locale switcher in the Orbit frontend template"}]}` + "\n"
	if body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

func TestFlagsAreSortedByName(t *testing.T) {
	srv := newTestServer(fakeFlagStore{flags: []domain.FeatureFlag{
		{Name: "zeta.flag", Enabled: false, Description: "last", UpdatedAt: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)},
		{Name: "alpha.flag", Enabled: true, Description: "first", UpdatedAt: time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC)},
	}})

	resp, body := getFlags(t, srv)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got flagsResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Flags) != 2 {
		t.Fatalf("flags = %+v, want 2", got.Flags)
	}
	if got.Flags[0].Name != "alpha.flag" || got.Flags[1].Name != "zeta.flag" {
		t.Fatalf("flags out of order: %+v", got.Flags)
	}
	// The newest updated_at wins regardless of the list order.
	if got.Version != "2026-11-03T11:00:00Z" {
		t.Fatalf("version = %q, want newest timestamp", got.Version)
	}
}

func TestFlagsEmptyVersion(t *testing.T) {
	srv := newTestServer(fakeFlagStore{})

	resp, body := getFlags(t, srv)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if body != `{"version":"","flags":[]}`+"\n" {
		t.Fatalf("body = %q, want an empty flag list", body)
	}
}

func TestFlagsUnavailable(t *testing.T) {
	srv := newTestServer(fakeFlagStore{err: errors.New("relation feature_flags does not exist")})

	resp, body := getFlags(t, srv)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q, want application/json", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("cache-control = %q, want no-cache", got)
	}
	if body != `{"error":"flags unavailable"}`+"\n" {
		t.Fatalf("body = %q", body)
	}
	if strings.Contains(body, "relation") {
		t.Fatalf("body leaked the store error: %q", body)
	}
}

func getFlags(t *testing.T, srv *Server) (*http.Response, string) {
	t.Helper()
	resp, err := srv.app.Test(httptest.NewRequest(http.MethodGet, "/v1/flags", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}
