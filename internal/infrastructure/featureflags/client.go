package featureflags

import (
	"context"
	"net/http"
	"os"
	"time"

	unleash "github.com/Unleash/unleash-client-go/v4"
	unleashProvider "github.com/open-feature/go-sdk-contrib/providers/unleash/pkg"
	"github.com/open-feature/go-sdk/openfeature"
)

// Evaluator wraps OpenFeature for boolean flag checks.
type Evaluator struct {
	client *openfeature.Client
}

// NewFromEnv connects to Unleash when configured. On failure, returns an evaluator
// that always returns the safe default (ponytail: duplicated in orbit-notifications;
// extract to shared module when a third consumer appears — no retry loop; restart to reconnect).
func NewFromEnv(appName string, log func(msg string, args ...any)) *Evaluator {
	url := envOr("UNLEASH_URL", "http://localhost:10050/api/")
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
	if err := provider.Init(openfeature.EvaluationContext{}); err != nil {
		log("feature_flags_init_failed", "error", err)
		return &Evaluator{}
	}
	if err := openfeature.SetProvider(provider); err != nil {
		log("feature_flags_init_failed", "error", err)
		return &Evaluator{}
	}

	return &Evaluator{client: openfeature.NewClient(name)}
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
