package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	authv1 "github.com/manovaspace/orbit-auth/api/auth/v1"
	"github.com/manovaspace/orbit-auth/internal/application"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/featureflags"
	grpchandlers "github.com/manovaspace/orbit-auth/internal/infrastructure/grpc"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/notifications"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/postgres"
	observability "github.com/manovaspace/orbit-observability"
	googlegrpc "google.golang.org/grpc"
)

func main() {
	ctx := context.Background()
	if err := observability.Configure(observability.ConfigFromEnv("orbit-auth", "0.1.0")); err != nil {
		panic(err)
	}
	log := observability.Logger()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	if err := postgres.Migrate(ctx, dsn, "migrations"); err != nil {
		log.Error("migrate failed", "error", err)
		os.Exit(1)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Error("db connect failed", "error", err)
		os.Exit(1)
	}

	if os.Getenv("JWT_PRIVATE_KEY_FILE") == "" || os.Getenv("JWT_ACTIVE_KEY_ID") == "" || os.Getenv("JWT_PUBLIC_KEYS_DIR") == "" {
		log.Error("JWT_PRIVATE_KEY_FILE, JWT_ACTIVE_KEY_ID, and JWT_PUBLIC_KEYS_DIR are required")
		os.Exit(1)
	}

	notifAddr := envOr("NOTIFICATIONS_GRPC_ADDR", "localhost:10110")
	dialOpts := append(observability.GRPCDialOptions(), observability.InternalAuthDialOption())
	notif, err := notifications.NewClient(notifAddr, dialOpts...)
	if err != nil {
		log.Error("notifications client failed", "error", err)
		os.Exit(1)
	}

	store := postgres.New(pool)
	flags := featureflags.NewFromEnv("orbit-auth", func(msg string, args ...any) {
		log.Info(msg, args...)
	})
	svc, err := application.NewService(store, store, store, store, notif, flags, func(msg string, args ...any) {
		log.Info(msg, args...)
	})
	if err != nil {
		log.Error("service init failed", "error", err)
		os.Exit(1)
	}
	if err := svc.SeedDemoUsers(ctx); err != nil {
		log.Error("demo seed failed", "error", err)
		os.Exit(1)
	}

	grpcPort := envOr("GRPC_PORT", "10100")
	lis, err := net.Listen("tcp", ":"+grpcPort)
	if err != nil {
		log.Error("listen failed", "error", err)
		os.Exit(1)
	}

	gs := googlegrpc.NewServer(observability.GRPCServerOptions()...)
	authv1.RegisterAuthServiceServer(gs, grpchandlers.New(svc))

	healthMux := http.NewServeMux()
	healthMux.Handle("/healthz", observability.HealthHandler())
	healthMux.Handle("/readyz", observability.ReadinessHandler())
	healthPort := envOr("HEALTH_PORT", "10101")
	healthServer := &http.Server{
		Addr:              ":" + healthPort,
		Handler:           observability.HTTPMiddleware(healthMux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Info("grpc listening", "port", grpcPort)
		if err := gs.Serve(lis); err != nil {
			log.Error("grpc serve failed", "error", err)
		}
	}()

	go func() {
		if err := healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("health server failed", "error", err)
		}
	}()

	observability.WaitForSignal(observability.ShutdownConfig{
		GRPCServer: gs,
		HTTPServer: healthServer,
		OnShutdown: []func(context.Context) error{
			func(context.Context) error {
				pool.Close()
				return nil
			},
		},
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
