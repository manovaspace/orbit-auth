// Package httpapi serves the HTTP surface on Fiber v3: the health probes and
// the public feature-flag read endpoint. Flag names and states are public and
// unauthenticated on purpose, because the frontend needs them before sign-in.
// Every other call stays on gRPC.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"

	"github.com/manovaspace/orbit-auth/internal/application"
	observability "github.com/manovaspace/orbit-observability"
)

// Server wraps the Fiber app so main only starts and stops it.
type Server struct {
	app *fiber.App
}

// New builds the Fiber server. The probe handlers are the same net/http
// handlers the service used before, adapted unchanged, so status codes,
// headers and bodies stay identical.
func New(flags *application.FeatureFlags) *Server {
	app := fiber.New(fiber.Config{
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	})
	// The service logs its own JSON startup lines through observability.
	app.Hooks().OnPreStartupMessage(func(data *fiber.PreStartupMessageData) error {
		data.PreventDefault = true
		return nil
	})

	mux := http.NewServeMux()
	mux.Handle("/healthz", observability.HealthHandler())
	mux.Handle("/readyz", observability.ReadinessHandler())
	mux.HandleFunc("GET /v1/flags", listFlags(flags))
	// A catch-all adapted handler keeps http.ServeMux's own 404 and method
	// behaviour instead of Fiber's defaults.
	app.Use(adaptor.HTTPHandler(observability.HTTPMiddleware(mux)))

	return &Server{app: app}
}

// Listen serves HTTP on addr until Shutdown is called.
func (s *Server) Listen(addr string) error {
	return s.app.Listen(addr)
}

// Shutdown drains the HTTP server within ctx.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.app.ShutdownWithContext(ctx)
}

type flagItem struct {
	Name        string `json:"name"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description"`
}

type flagsResponse struct {
	Version string     `json:"version"`
	Flags   []flagItem `json:"flags"`
}

// listFlags serves the public flag set. Version is the newest updated_at so a
// client can cache the whole response without pinning stale flags.
func listFlags(flags *application.FeatureFlags) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")

		listed, err := flags.List(r.Context())
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "flags unavailable"})
			return
		}

		sort.Slice(listed, func(i, j int) bool { return listed[i].Name < listed[j].Name })
		response := flagsResponse{Flags: make([]flagItem, 0, len(listed))}
		var newest time.Time
		for _, flag := range listed {
			if flag.UpdatedAt.After(newest) {
				newest = flag.UpdatedAt
			}
			response.Flags = append(response.Flags, flagItem{
				Name:        flag.Name,
				Enabled:     flag.Enabled,
				Description: flag.Description,
			})
		}
		if !newest.IsZero() {
			response.Version = newest.UTC().Format(time.RFC3339Nano)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}
}
