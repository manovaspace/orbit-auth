package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	observability "github.com/manovaspace/orbit-observability"
)

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
			srv := New()
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
