// Package httpapi serves the HTTP surface on Fiber v3. The service exposes only
// the health probes; every other call stays on gRPC.
package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"

	observability "github.com/manovaspace/orbit-observability"
)

// Server wraps the Fiber app so main only starts and stops it.
type Server struct {
	app *fiber.App
}

// New builds the Fiber server. The probe handlers are the same net/http
// handlers the service used before, adapted unchanged, so status codes,
// headers and bodies stay identical.
func New() *Server {
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
