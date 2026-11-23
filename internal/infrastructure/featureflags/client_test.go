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

// count returns how many logged messages equal want exactly.
func (r *recorder) count(want string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, msg := range r.msgs {
		if msg == want {
			n++
		}
	}
	return n
}

// useStartupRetryDelays replaces startupRetryDelays for one test. Call it before
// newEvaluator so the restore runs after the provider has shut down.
func useStartupRetryDelays(t *testing.T, delays ...time.Duration) {
	t.Helper()
	orig := startupRetryDelays
	startupRetryDelays = delays
	t.Cleanup(func() { startupRetryDelays = orig })
}

// waitUntil polls cond until it returns true or timeout passes.
func waitUntil(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
	return true
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
	useStartupRetryDelays(t, 5*time.Millisecond, 5*time.Millisecond)

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
	// The startup retries run in the background; wait for the final log.
	if !waitUntil(time.Second, func() bool { return rec.count("feature_flags_refresh_failed") == 1 }) {
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

// TestStartupRetriesRecoverAfterFailedFetches pins the startup race fix: the
// first two fetches fail, the third succeeds, and the flag is served within a
// second without waiting for refreshInterval.
func TestStartupRetriesRecoverAfterFailedFetches(t *testing.T) {
	useStartupRetryDelays(t, 5*time.Millisecond, 5*time.Millisecond, 5*time.Millisecond)

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) <= 2 {
			http.Error(w, "boom", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"version":"2026-11-03T00:00:00Z","flags":[{"name":%q,"enabled":true,"description":"email otp"}]}`, knownFlag)
	}))
	t.Cleanup(srv.Close)

	rec := &recorder{}
	evaluator, _ := newEvaluator(t, srv.URL, rec)

	if !waitUntil(time.Second, func() bool {
		return evaluator.Bool(context.Background(), knownFlag, false)
	}) {
		t.Fatalf("Bool(%s, false) did not become true within 1s; requests=%d", knownFlag, requests.Load())
	}
	if rec.contains("feature_flags_refresh_failed") {
		t.Fatalf("feature_flags_refresh_failed logged during startup retries")
	}
	if n := rec.count("feature_flags_first_fetch_pending"); n != 1 {
		t.Fatalf("feature_flags_first_fetch_pending logged %d times, want 1", n)
	}
	if n := rec.count("feature_flags_recovered"); n != 1 {
		t.Fatalf("feature_flags_recovered logged %d times, want 1", n)
	}
}

// TestStartupRetriesExhaustedLogRefreshFailedOnce checks that an endpoint which
// never recovers gets the first fetch plus one request per retry, and that the
// startup phase logs feature_flags_refresh_failed exactly once.
func TestStartupRetriesExhaustedLogRefreshFailedOnce(t *testing.T) {
	const retries = 3
	useStartupRetryDelays(t, 5*time.Millisecond, 5*time.Millisecond, 5*time.Millisecond)

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	rec := &recorder{}
	_, _ = newEvaluator(t, srv.URL, rec)

	if !waitUntil(time.Second, func() bool {
		return rec.count("feature_flags_refresh_failed") == 1
	}) {
		t.Fatalf("feature_flags_refresh_failed not logged within 1s; requests=%d", requests.Load())
	}
	if n := rec.count("feature_flags_first_fetch_pending"); n != 1 {
		t.Fatalf("feature_flags_first_fetch_pending logged %d times, want 1", n)
	}
	if got, want := requests.Load(), int32(1+retries); got != want {
		t.Fatalf("requests = %d, want %d (first fetch plus one per retry)", got, want)
	}
}

// TestShutdownDuringStartupRetryWaitReturnsPromptly checks that Shutdown does
// not wait out a long startup retry delay.
func TestShutdownDuringStartupRetryWaitReturnsPromptly(t *testing.T) {
	useStartupRetryDelays(t, time.Minute)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	rec := &recorder{}
	provider := newPlatformProvider(srv.URL, rec.log)
	t.Cleanup(provider.Shutdown)
	if err := provider.Init(openfeature.EvaluationContext{}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	start := time.Now()
	provider.Shutdown()
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("Shutdown took %s during the retry wait, want under 1s", elapsed)
	}
	if rec.contains("feature_flags_refresh_failed") {
		t.Fatalf("Shutdown during the retry wait logged feature_flags_refresh_failed")
	}
}
