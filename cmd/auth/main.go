package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	authv1 "github.com/manovaspace/orbit-auth/api/auth/v1"
	"github.com/manovaspace/orbit-auth/internal/application"
	"github.com/manovaspace/orbit-auth/internal/domain"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/clock"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/credentials"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/featureflags"
	grpchandlers "github.com/manovaspace/orbit-auth/internal/infrastructure/grpc"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/httpapi"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/ids"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/jwt"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/notifications"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/postgres"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/postgres/migrations"
	messagebus "github.com/manovaspace/orbit-message-bus"
	"github.com/manovaspace/orbit-message-bus/outbox"
	"github.com/manovaspace/orbit-message-bus/transport"
	observability "github.com/manovaspace/orbit-observability"
	googlegrpc "google.golang.org/grpc"
)

// version is the service version reported to observability. Release builds
// override it with -X main.version.
var version = "dev"

func main() {
	ctx := context.Background()
	if err := observability.Configure(observability.ConfigFromEnv("orbit-auth", version)); err != nil {
		panic(err)
	}
	log := observability.Logger()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Error("DATABASE_URL is required")
		os.Exit(1)
	}

	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		runMigrate(ctx, dsn, os.Args[2:])
		return
	}

	// Auto-migrate on start keeps the previous deployment behaviour. Set
	// MIGRATE_ON_START=false when a deploy runs `migrate` explicitly.
	if !strings.EqualFold(envOr("MIGRATE_ON_START", "true"), "false") {
		if err := migrateUp(ctx, dsn); err != nil {
			log.Error("migrate failed", "error", err)
			os.Exit(1)
		}
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
	signer, err := jwt.NewFromEnv()
	if err != nil {
		log.Error("jwt config failed", "error", err)
		os.Exit(1)
	}

	notifAddr := envOr("NOTIFICATIONS_GRPC_ADDR", "localhost:10110")
	dialOpts := append(observability.GRPCDialOptions(), observability.InternalAuthDialOption())
	notif, err := notifications.NewClient(notifAddr, dialOpts...)
	if err != nil {
		log.Error("notifications client failed", "error", err)
		os.Exit(1)
	}

	demoUsers, err := demoUsersFromEnv()
	if err != nil {
		log.Error("demo mode config failed", "error", err)
		os.Exit(1)
	}

	store := postgres.New(pool)
	// Platform flags are read over HTTP before sign-in; writes arrive with the
	// admin surface later.
	flagService := application.NewFeatureFlags(store)

	// Lifecycle events go through the Postgres outbox. The transport is
	// selected from BUS_TRANSPORT (default inproc); only cmd reads it.
	bus, err := transport.FromEnv(ctx, messagebus.Config{
		Source:  domain.EventSource,
		Version: domain.EventVersion,
	})
	if err != nil {
		log.Error("message bus config failed", "error", err)
		os.Exit(1)
	}
	relay := &outbox.Relay{Pool: pool, Bus: bus}
	relayCtx, stopRelay := context.WithCancel(ctx)
	relayDone := make(chan struct{})
	go func() {
		defer close(relayDone)
		runEventRelay(relayCtx, relay,
			func(msg string, args ...any) { log.Info(msg, args...) },
			func(msg string, args ...any) { log.Error(msg, args...) },
		)
	}()

	flags := featureflags.NewFromEnv("orbit-auth", func(msg string, args ...any) {
		log.Info(msg, args...)
	})
	hasher := credentials.NewHasher()
	svc := application.NewService(application.Deps{
		OTP:       store,
		Users:     store,
		Refresh:   store,
		Tokens:    store,
		Orgs:      store,
		Invites:   store,
		Signup:    store,
		Notifier:  notif,
		Flags:     flags,
		Clock:     clock.New(),
		IDs:       ids.New(),
		Signer:    signer,
		Hasher:    hasher,
		Codes:     credentials.NewCodes(),
		Secrets:   credentials.NewSecrets(),
		DemoMode:  os.Getenv("DEMO_MODE") == "true",
		DemoUsers: demoUsers,
		// AUTH_SIGNUP defaults to off; only the literal open (case-insensitive)
		// enables self-serve registration.
		SignupOpen: strings.EqualFold(strings.TrimSpace(os.Getenv("AUTH_SIGNUP")), "open"),
		// AUTH_AUTO_SELECT_SINGLE_ORG defaults to true; only an explicit
		// false disables it.
		AutoSelectSingleOrg: !strings.EqualFold(envOr("AUTH_AUTO_SELECT_SINGLE_ORG", "true"), "false"),
		Logger:              func(msg string, args ...any) { log.Info(msg, args...) },
	})
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

	gs := googlegrpc.NewServer(serverOptions(signer)...)
	authv1.RegisterAuthServiceServer(gs, grpchandlers.New(svc))
	authv1.RegisterOrgServiceServer(gs, grpchandlers.NewOrg(svc))
	authv1.RegisterSignupServiceServer(gs, grpchandlers.NewSignup(svc))

	health := httpapi.New(flagService)
	healthPort := envOr("HEALTH_PORT", "10101")

	go func() {
		log.Info("grpc listening", "port", grpcPort)
		if err := gs.Serve(lis); err != nil {
			log.Error("grpc serve failed", "error", err)
		}
	}()

	go func() {
		log.Info("http listening", "port", healthPort)
		if err := health.Listen(":" + healthPort); err != nil {
			log.Error("http server failed", "error", err)
		}
	}()

	observability.WaitForSignal(observability.ShutdownConfig{
		GRPCServer: gs,
		OnShutdown: []func(context.Context) error{
			func(ctx context.Context) error {
				stopRelay()
				select {
				case <-relayDone:
				case <-ctx.Done():
				}
				return nil
			},
			func(context.Context) error { return bus.Close() },
			func(ctx context.Context) error { return health.Shutdown(ctx) },
			func(context.Context) error {
				pool.Close()
				return nil
			},
		},
	})
}

// runEventRelay polls the outbox until ctx is cancelled. Publish and database
// failures are logged and retried with exponential backoff; they never stop the
// gRPC server.
func runEventRelay(ctx context.Context, relay *outbox.Relay, logInfo, logError func(msg string, args ...any)) {
	const (
		idleInterval = 500 * time.Millisecond
		minBackoff   = time.Second
		maxBackoff   = 30 * time.Second
	)
	backoff := minBackoff
	for {
		published, err := relay.RunOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		wait := idleInterval
		switch {
		case err != nil:
			logError("outbox relay failed", "error", err)
			wait = backoff
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		default:
			backoff = minBackoff
			if published > 0 {
				logInfo("outbox events relayed", "count", published)
				continue
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// serverOptions keeps the observability chain and adds access-token validation
// for OrgService. The access interceptor runs after internal auth and before
// every OrgService handler. AuthService keeps its existing unauthenticated
// contract.
func serverOptions(signer *jwt.Signer) []googlegrpc.ServerOption {
	opts := observability.GRPCServerOptions()
	return append(opts, googlegrpc.ChainUnaryInterceptor(
		grpchandlers.AccessTokenUnaryServerInterceptor(signer.KeySet()),
	))
}

// demoUsersFromEnv parses AUTH_DEMO_USERS. DEMO_MODE=true outside dev fails
// closed before the service starts.
func demoUsersFromEnv() ([]domain.DemoUser, error) {
	if os.Getenv("DEMO_MODE") != "true" {
		return nil, nil
	}
	if os.Getenv("DEPLOYMENT_ENVIRONMENT") != "dev" {
		return nil, fmt.Errorf("DEMO_MODE is only allowed when DEPLOYMENT_ENVIRONMENT=dev")
	}
	raw := os.Getenv("AUTH_DEMO_USERS")
	if raw == "" {
		return nil, fmt.Errorf("AUTH_DEMO_USERS is required when DEMO_MODE=true")
	}
	var users []domain.DemoUser
	if err := json.Unmarshal([]byte(raw), &users); err != nil {
		return nil, err
	}
	return users, nil
}

// runMigrate executes the explicit migrate subcommand:
//
//	auth migrate          apply all pending migrations
//	auth migrate up       apply all pending migrations
//	auth migrate down     roll back the most recent migration
//	auth migrate status   print every embedded migration
func runMigrate(ctx context.Context, dsn string, args []string) {
	action := "up"
	if len(args) > 0 {
		action = args[0]
	}
	var err error
	switch action {
	case "up":
		err = migrateUp(ctx, dsn)
	case "down":
		err = migrateDown(ctx, dsn)
	case "status":
		err = migrateStatus(ctx, dsn)
	default:
		err = fmt.Errorf("unknown migrate action %q (want up, down or status)", action)
	}
	if err != nil {
		observability.Logger().Error("migrate failed", "error", err)
		os.Exit(1)
	}
}

func migrateUp(ctx context.Context, dsn string) error {
	m, err := migrations.Open(dsn)
	if err != nil {
		return err
	}
	defer func() { _ = m.Close() }()

	results, err := m.Up(ctx)
	if err != nil {
		return err
	}
	for _, result := range results {
		observability.Logger().Info("migration applied",
			"version", result.Source.Version, "file", result.Source.Path)
	}
	return nil
}

func migrateDown(ctx context.Context, dsn string) error {
	m, err := migrations.Open(dsn)
	if err != nil {
		return err
	}
	defer func() { _ = m.Close() }()

	result, err := m.Down(ctx)
	if err != nil {
		return err
	}
	observability.Logger().Info("migration rolled back",
		"version", result.Source.Version, "file", result.Source.Path)
	return nil
}

func migrateStatus(ctx context.Context, dsn string) error {
	m, err := migrations.Open(dsn)
	if err != nil {
		return err
	}
	defer func() { _ = m.Close() }()

	statuses, err := m.Status(ctx)
	if err != nil {
		return err
	}
	for _, status := range statuses {
		observability.Logger().Info("migration",
			"version", status.Source.Version,
			"file", status.Source.Path,
			"applied", status.State == "applied")
	}
	current, target, err := m.Version(ctx)
	if err != nil {
		return err
	}
	observability.Logger().Info("migration version", "current", current, "latest", target)
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
