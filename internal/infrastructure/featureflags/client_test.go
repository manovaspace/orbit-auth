package featureflags

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder collects log messages emitted by NewFromEnv. NewFromEnv logs from the
// caller's goroutine only, so no locking is needed to read the result, but -race
// stays clean if that ever changes.
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

func TestNewFromEnvUnreachableFallsBackToDefaults(t *testing.T) {
	t.Setenv("UNLEASH_URL", "http://127.0.0.1:1/api/")
	t.Setenv("FEATURE_FLAGS_INIT_TIMEOUT", "200ms")

	rec := &recorder{}
	start := time.Now()
	evaluator := NewFromEnv("orbit-auth-test", rec.log)
	elapsed := time.Since(start)

	if elapsed >= 2*time.Second {
		t.Fatalf("NewFromEnv took %s, want under 2s", elapsed)
	}
	if got := evaluator.Bool(context.Background(), "x", true); got != true {
		t.Fatalf("Bool(x, true) = %v, want true", got)
	}
}

// TestInitWithTimeoutBoundsBlockingInit covers the timeout branch directly: the
// pinned unleash provider's Init returns immediately, so an unreachable URL
// cannot exercise it end to end.
func TestInitWithTimeoutBoundsBlockingInit(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	start := time.Now()
	err := initWithTimeout(func() error {
		<-release
		return nil
	}, 50*time.Millisecond)

	if elapsed := time.Since(start); elapsed >= 2*time.Second {
		t.Fatalf("initWithTimeout took %s, want under 2s", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), "init timed out after 50ms") {
		t.Fatalf("initWithTimeout error = %v, want init timed out after 50ms", err)
	}
}

func TestInitWithTimeoutReturnsInitError(t *testing.T) {
	wantErr := errors.New("boom")
	if err := initWithTimeout(func() error { return wantErr }, time.Second); !errors.Is(err, wantErr) {
		t.Fatalf("initWithTimeout error = %v, want %v", err, wantErr)
	}
}

func TestNewFromEnvDisabledURLReturnsDefaultsImmediately(t *testing.T) {
	for _, url := range []string{"off", "OFF", "disabled", "Disabled"} {
		t.Run(url, func(t *testing.T) {
			t.Setenv("UNLEASH_URL", url)

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

func TestInitTimeoutFromEnv(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{name: "unset", raw: "", want: defaultInitTimeout},
		{name: "invalid", raw: "soon", want: defaultInitTimeout},
		{name: "non-positive", raw: "-1s", want: defaultInitTimeout},
		{name: "set", raw: "250ms", want: 250 * time.Millisecond},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FEATURE_FLAGS_INIT_TIMEOUT", tc.raw)
			if got := initTimeout(); got != tc.want {
				t.Fatalf("initTimeout() = %s, want %s", got, tc.want)
			}
		})
	}
}
