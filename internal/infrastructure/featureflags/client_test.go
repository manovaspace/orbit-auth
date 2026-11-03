package featureflags

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-feature/go-sdk/openfeature"
)

const knownFlag = "manova.auth.email_otp"

// recorder collects log messages emitted by NewFromEnv and the provider.
type recorder struct {
	mu   sync.Mutex
	msgs []string
}

func (r *recorder) log(msg string, _ ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, msg)
}

func (r *recorder) contains(want string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, msg := range r.msgs {
		if strings.Contains(msg, want) {
			return true
		}
	}
	return false
}

// newEvaluator registers a provider pointing at url and returns an Evaluator
// backed by the OpenFeature client. SetProviderAndWait makes the initial fetch
// deterministic.
func newEvaluator(t *testing.T, url string, rec *recorder) (*Evaluator, *PlatformProvider) {
	t.Helper()

	provider := newPlatformProvider(url, rec.log)
	if err := openfeature.SetProviderAndWait(provider); err != nil {
		t.Fatalf("SetProviderAndWait: %v", err)
	}
	t.Cleanup(openfeature.Shutdown)

	return &Evaluator{client: openfeature.NewClient("orbit-auth-test")}, provider
}

func TestBoolUsesServedFlags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"version":"2026-11-03T00:00:00Z","flags":[{"name":%q,"enabled":true,"description":"email otp"}]}`, knownFlag)
	}))
	t.Cleanup(srv.Close)

	evaluator, _ := newEvaluator(t, srv.URL, &recorder{})

	if got := evaluator.Bool(context.Background(), knownFlag, false); got != true {
		t.Fatalf("Bool(%s, false) = %v, want true", knownFlag, got)
	}
	if got := evaluator.Bool(context.Background(), "unknown.flag", true); got != true {
		t.Fatalf("Bool(unknown.flag, true) = %v, want true", got)
	}
	if got := evaluator.Bool(context.Background(), "unknown.flag", false); got != false {
		t.Fatalf("Bool(unknown.flag, false) = %v, want false", got)
	}
}

func TestBoolFallsBackToDefaultWhenFetchFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	rec := &recorder{}
	evaluator, _ := newEvaluator(t, srv.URL, rec)

	if got := evaluator.Bool(context.Background(), knownFlag, true); got != true {
		t.Fatalf("Bool(%s, true) = %v, want true", knownFlag, got)
	}
	if got := evaluator.Bool(context.Background(), knownFlag, false); got != false {
		t.Fatalf("Bool(%s, false) = %v, want false", knownFlag, got)
	}
	if !rec.contains("feature_flags_refresh_failed") {
		t.Fatalf("expected feature_flags_refresh_failed log, got %v", rec.msgs)
	}
}

func TestRefreshUpdatesAndFailedRefreshKeepsCache(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	var failing atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if failing.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"version":"2026-11-03T00:00:00Z","flags":[{"name":%q,"enabled":%t,"description":"email otp"}]}`, knownFlag, enabled.Load())
	}))
	t.Cleanup(srv.Close)

	rec := &recorder{}
	evaluator, provider := newEvaluator(t, srv.URL, rec)

	if got := evaluator.Bool(context.Background(), knownFlag, false); got != true {
		t.Fatalf("initial Bool(%s, false) = %v, want true", knownFlag, got)
	}

	enabled.Store(false)
	if err := provider.refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := evaluator.Bool(context.Background(), knownFlag, true); got != false {
		t.Fatalf("refreshed Bool(%s, true) = %v, want false", knownFlag, got)
	}

	failing.Store(true)
	if err := provider.refresh(context.Background()); err == nil {
		t.Fatal("refresh: want error, got nil")
	}
	if got := evaluator.Bool(context.Background(), knownFlag, true); got != false {
		t.Fatalf("after failed refresh Bool(%s, true) = %v, want cached false", knownFlag, got)
	}
}

// TestNewFromEnvNonBlockingWithUnreachableEndpoint pins the startup contract:
// registering the provider must not wait for the flag endpoint, and
// evaluations return the caller's default until the provider is ready.
func TestNewFromEnvNonBlockingWithUnreachableEndpoint(t *testing.T) {
	t.Setenv("FLAGS_URL", "http://10.255.255.1:1/v1/flags")
	t.Cleanup(openfeature.Shutdown)

	rec := &recorder{}
	start := time.Now()
	evaluator := NewFromEnv("orbit-auth-test", rec.log)
	elapsed := time.Since(start)

	if elapsed >= time.Second {
		t.Fatalf("NewFromEnv took %s, want under 1s", elapsed)
	}
	if got := evaluator.Bool(context.Background(), "x", true); got != true {
		t.Fatalf("Bool(x, true) = %v, want true", got)
	}
}

func TestNewFromEnvDisabledURLReturnsDefaultsImmediately(t *testing.T) {
	t.Cleanup(openfeature.Shutdown)

	for _, url := range []string{"off", "OFF", "disabled", "Disabled"} {
		t.Run(url, func(t *testing.T) {
			t.Setenv("FLAGS_URL", url)

			rec := &recorder{}
			start := time.Now()
			evaluator := NewFromEnv("orbit-auth-test", rec.log)
			elapsed := time.Since(start)

			if elapsed >= time.Second {
				t.Fatalf("NewFromEnv took %s, want immediate return", elapsed)
			}
			if got := evaluator.Bool(context.Background(), "x", true); got != true {
				t.Fatalf("Bool(x, true) = %v, want true", got)
			}
			if !rec.contains("feature_flags_disabled") {
				t.Fatalf("expected feature_flags_disabled log, got %v", rec.msgs)
			}
		})
	}
}
