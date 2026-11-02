package featureflags

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	unleash "github.com/Unleash/unleash-client-go/v4"
	unleashProvider "github.com/open-feature/go-sdk-contrib/providers/unleash/pkg"
	"github.com/open-feature/go-sdk/openfeature"
)

const defaultInitTimeout = 3 * time.Second

// Evaluator wraps OpenFeature for boolean flag checks.
type Evaluator struct {
	client *openfeature.Client
}

// NewFromEnv connects to Unleash when configured. On failure, returns an evaluator
// that always returns the safe default (ponytail: duplicated in orbit-notifications;
// extract to shared module when a third consumer appears — no retry loop; restart to reconnect).
func NewFromEnv(appName string, log func(msg string, args ...any)) *Evaluator {
	url := envOr("UNLEASH_URL", "http://localhost:10050/api/")
	if isDisabledURL(url) {
		log("feature_flags_disabled", "UNLEASH_URL", url)
		return &Evaluator{}
	}
	token := os.Getenv("UNLEASH_API_TOKEN")
	if token == "" {
		token = "default:development.unleash-insecure-api-token" // Unleash requires a token string; override in prod
	}
	name := envOr("UNLEASH_APP_NAME", appName)

	providerConfig := unleashProvider.ProviderConfig{
		Options: []unleash.ConfigOption{
			unleash.WithUrl(url),
			unleash.WithAppName(name),
			unleash.WithCustomHeaders(http.Header{
				"Authorization": {token},
			}),
			unleash.WithRefreshInterval(15 * time.Second),
		},
	}

	provider, err := unleashProvider.NewProvider(providerConfig)
	if err != nil {
		log("feature_flags_init_failed", "error", err)
		return &Evaluator{}
	}
	if err := initWithTimeout(func() error {
		return provider.Init(openfeature.EvaluationContext{})
	}, initTimeout()); err != nil {
		log("feature_flags_init_failed", "error", err)
		return &Evaluator{}
	}
	if err := openfeature.SetProvider(provider); err != nil {
		log("feature_flags_init_failed", "error", err)
		return &Evaluator{}
	}

	return &Evaluator{client: openfeature.NewClient(name)}
}

// initWithTimeout bounds the provider handshake: Unleash being unreachable must
// not block startup, so a timed-out Init falls back to flag defaults.
func initWithTimeout(init func() error, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() {
		done <- init()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-done:
		return err
	case <-timer.C:
		return fmt.Errorf("init timed out after %s", timeout)
	}
}

// isDisabledURL reports whether UNLEASH_URL was explicitly set to opt out of
// Unleash entirely.
func isDisabledURL(url string) bool {
	url = strings.TrimSpace(url)
	return strings.EqualFold(url, "off") || strings.EqualFold(url, "disabled")
}

// initTimeout reads FEATURE_FLAGS_INIT_TIMEOUT as a Go duration, defaulting to 3s.
func initTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("FEATURE_FLAGS_INIT_TIMEOUT"))
	if raw == "" {
		return defaultInitTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return defaultInitTimeout
	}
	return d
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

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
