// Package featureflags reads boolean feature flags from the platform flags
// endpoint (GET /v1/flags, served on this service's own HTTP port).
//
// Flags are fetched once on startup and refreshed in the background. A failed
// fetch never clears the last known values: the provider keeps the previous map
// and degrades to the caller-supplied defaults until a later refresh succeeds.
package featureflags

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/open-feature/go-sdk/openfeature"
)

const (
	defaultFlagsURL = "http://localhost:10101/v1/flags"
	refreshInterval = 30 * time.Second
	fetchTimeout    = 5 * time.Second
)

// Evaluator wraps OpenFeature for boolean flag checks.
type Evaluator struct {
	client *openfeature.Client
}

// PlatformProvider resolves flags from the platform flags endpoint. It
// implements both openfeature.FeatureProvider and openfeature.StateHandler.
type PlatformProvider struct {
	url    string
	log    func(msg string, args ...any)
	client *http.Client

	mu     sync.RWMutex
	flags  map[string]bool
	cancel context.CancelFunc
	done   chan struct{}
}

var (
	_ openfeature.FeatureProvider = (*PlatformProvider)(nil)
	_ openfeature.StateHandler    = (*PlatformProvider)(nil)
)

// flagsDocument is the payload served by the platform flags endpoint.
type flagsDocument struct {
	Version string       `json:"version"`
	Flags   []flagRecord `json:"flags"`
}

type flagRecord struct {
	Name        string `json:"name"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description"`
}

// NewFromEnv connects to the platform flags endpoint when configured. On
// failure, returns an evaluator that always returns the safe default
// (ponytail: duplicated in orbit-notifications; extract to shared module when
// a third consumer appears — no retry loop; restart to reconnect).
//
// Provider registration is non-blocking: the OpenFeature SDK initialises
// asynchronously, so evaluations return the safe default until the first fetch
// completes. An unreachable endpoint cannot delay startup.
func NewFromEnv(appName string, log func(msg string, args ...any)) *Evaluator {
	url := envOr("FLAGS_URL", defaultFlagsURL)
	if isDisabledURL(url) {
		log("feature_flags_disabled", "FLAGS_URL", url)
		return &Evaluator{}
	}

	provider := newPlatformProvider(url, log)
	if err := openfeature.SetProvider(provider); err != nil {
		log("feature_flags_init_failed", "error", err)
		return &Evaluator{}
	}

	return &Evaluator{client: openfeature.NewClient(appName)}
}

func newPlatformProvider(url string, log func(msg string, args ...any)) *PlatformProvider {
	return &PlatformProvider{
		url:    url,
		log:    log,
		client: &http.Client{Timeout: fetchTimeout},
		flags:  make(map[string]bool),
	}
}

// Metadata returns the provider name.
func (p *PlatformProvider) Metadata() openfeature.Metadata {
	return openfeature.Metadata{Name: "platform"}
}

// Init fetches the flags once and starts the background refresh loop.
func (p *PlatformProvider) Init(_ openfeature.EvaluationContext) error {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	p.mu.Lock()
	p.cancel = cancel
	p.done = done
	p.mu.Unlock()

	if err := p.refresh(ctx); err != nil {
		p.log("feature_flags_refresh_failed", "error", err)
	}

	go p.refreshLoop(ctx, done)
	return nil
}

// Shutdown stops the background refresh loop.
func (p *PlatformProvider) Shutdown() {
	p.mu.Lock()
	cancel, done := p.cancel, p.done
	p.cancel, p.done = nil, nil
	p.mu.Unlock()

	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
}

func (p *PlatformProvider) refreshLoop(ctx context.Context, done chan<- struct{}) {
	defer close(done)

	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.refresh(ctx); err != nil {
				p.log("feature_flags_refresh_failed", "error", err)
			}
		}
	}
}

// refresh fetches the document and atomically replaces the cache. On any
// failure the previous map is kept untouched.
func (p *PlatformProvider) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("flags endpoint %s returned %s", p.url, resp.Status)
	}

	var doc flagsDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return err
	}

	next := make(map[string]bool, len(doc.Flags))
	for _, flag := range doc.Flags {
		next[flag.Name] = flag.Enabled
	}

	p.mu.Lock()
	p.flags = next
	p.mu.Unlock()

	return nil
}

// isDisabledURL reports whether FLAGS_URL was explicitly set to opt out of
// flag evaluation entirely.
func isDisabledURL(url string) bool {
	url = strings.TrimSpace(url)
	return strings.EqualFold(url, "off") || strings.EqualFold(url, "disabled")
}

// Bool returns the flag value, or defaultVal when the client is unavailable.
func (e *Evaluator) Bool(ctx context.Context, flag string, defaultVal bool) bool {
	if e == nil || e.client == nil {
		return defaultVal
	}
	val, err := e.client.BooleanValue(ctx, flag, defaultVal, openfeature.EvaluationContext{})
	if err != nil {
		return defaultVal
	}
	return val
}

// BooleanEvaluation returns the cached flag value, or the default when the flag
// is unknown.
func (p *PlatformProvider) BooleanEvaluation(_ context.Context, flag string, defaultValue bool, _ openfeature.FlattenedContext) openfeature.BoolResolutionDetail {
	p.mu.RLock()
	value, ok := p.flags[flag]
	p.mu.RUnlock()

	if ok {
		return openfeature.BoolResolutionDetail{
			Value: value,
			ProviderResolutionDetail: openfeature.ProviderResolutionDetail{
				Reason: openfeature.StaticReason,
			},
		}
	}

	return openfeature.BoolResolutionDetail{
		Value: defaultValue,
		ProviderResolutionDetail: openfeature.ProviderResolutionDetail{
			Reason: openfeature.DefaultReason,
		},
	}
}

// StringEvaluation always returns the default; the platform serves booleans.
func (p *PlatformProvider) StringEvaluation(_ context.Context, _ string, defaultValue string, _ openfeature.FlattenedContext) openfeature.StringResolutionDetail {
	return openfeature.StringResolutionDetail{
		Value: defaultValue,
		ProviderResolutionDetail: openfeature.ProviderResolutionDetail{
			Reason: openfeature.DefaultReason,
		},
	}
}

// FloatEvaluation always returns the default; the platform serves booleans.
func (p *PlatformProvider) FloatEvaluation(_ context.Context, _ string, defaultValue float64, _ openfeature.FlattenedContext) openfeature.FloatResolutionDetail {
	return openfeature.FloatResolutionDetail{
		Value: defaultValue,
		ProviderResolutionDetail: openfeature.ProviderResolutionDetail{
			Reason: openfeature.DefaultReason,
		},
	}
}

// IntEvaluation always returns the default; the platform serves booleans.
func (p *PlatformProvider) IntEvaluation(_ context.Context, _ string, defaultValue int64, _ openfeature.FlattenedContext) openfeature.IntResolutionDetail {
	return openfeature.IntResolutionDetail{
		Value: defaultValue,
		ProviderResolutionDetail: openfeature.ProviderResolutionDetail{
			Reason: openfeature.DefaultReason,
		},
	}
}

// ObjectEvaluation always returns the default; the platform serves booleans.
func (p *PlatformProvider) ObjectEvaluation(_ context.Context, _ string, defaultValue any, _ openfeature.FlattenedContext) openfeature.InterfaceResolutionDetail {
	return openfeature.InterfaceResolutionDetail{
		Value: defaultValue,
		ProviderResolutionDetail: openfeature.ProviderResolutionDetail{
			Reason: openfeature.DefaultReason,
		},
	}
}

// Hooks returns no provider hooks.
func (p *PlatformProvider) Hooks() []openfeature.Hook {
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
