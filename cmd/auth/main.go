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
	"github.com/manovaspace/orbit-auth/internal/infrastructure/google"
	grpchandlers "github.com/manovaspace/orbit-auth/internal/infrastructure/grpc"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/httpapi"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/ids"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/jwt"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/notifications"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/postgres"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/postgres/migrations"
	messagebus "github.com/manovaspace/orbit-kit/messagebus"
	"github.com/manovaspace/orbit-kit/messagebus/outbox"
	"github.com/manovaspace/orbit-kit/messagebus/transport"
	observability "github.com/manovaspace/orbit-kit/observability"
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

	notifAddr := envOr("NOTIFICATIONS_GRPC_ADDR", "localhost:10012")
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

	defaultCallingCode, err := defaultCallingCodeFromEnv()
	if err != nil {
		log.Error("calling code config failed", "error", err)
		os.Exit(1)
	}

	store := postgres.New(pool)
	// Google sign-in is off unless all three GOOGLE_* settings are present. The
	// state key is derived once here from the client secret.
	googleCfg, googleOn, err := googleConfigFromEnv()
	if err != nil {
		log.Error("google sign-in config failed", "error", err)
		os.Exit(1)
	}
	var googleSignIn *application.GoogleSignIn
	if googleOn {
		googleSignIn = &application.GoogleSignIn{
			Provider:     google.NewProvider(googleCfg.clientID, googleCfg.clientSecret),
			States:       store,
			RedirectURIs: googleCfg.redirectURIs,
			StateKey:     application.DeriveGoogleStateKey(googleCfg.clientSecret),
		}
	} else {
		log.Info("google sign-in disabled", "reason", "GOOGLE_CLIENT_ID is empty")
	}
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
		// AUTH_DEFAULT_CALLING_CODE completes local mobile numbers at sign-in.
		DefaultCallingCode: defaultCallingCode,
		// AUTH_SIGNUP defaults to off; only the literal open (case-insensitive)
		// enables self-serve registration.
		SignupOpen: strings.EqualFold(strings.TrimSpace(os.Getenv("AUTH_SIGNUP")), "open"),
		// AUTH_INVITE_SIGNUP defaults to on; only an explicit off or false
		// disables invite sign-up.
		InviteSignupDisabled: strings.EqualFold(strings.TrimSpace(os.Getenv("AUTH_INVITE_SIGNUP")), "off") ||
			strings.EqualFold(strings.TrimSpace(os.Getenv("AUTH_INVITE_SIGNUP")), "false"),
		// AUTH_INVITE_ACCEPT_HOSTS lists the hosts an invitation accept_url may use.
		InviteAcceptHosts: inviteAcceptHostsFromEnv(),
		// AUTH_AUTO_SELECT_SINGLE_ORG defaults to true; only an explicit
		// false disables it.
		AutoSelectSingleOrg: !strings.EqualFold(envOr("AUTH_AUTO_SELECT_SINGLE_ORG", "true"), "false"),
		// Google sign-in: nil when off.
		Google: googleSignIn,
		Logger: func(msg string, args ...any) { log.Info(msg, args...) },
	})
	if err := svc.SeedDemoUsers(ctx); err != nil {
		log.Error("demo seed failed", "error", err)
		os.Exit(1)
	}

	grpcPort := envOr("GRPC_PORT", "10010")
	lis, err := net.Listen("tcp", ":"+grpcPort)
	if err != nil {
		log.Error("listen failed", "error", err)
		os.Exit(1)
	}

	// AUTH_STAFF_SERVICE_TOKEN is the shared secret the gateway presents on every
	// StaffOrgService call. Empty disables the staff surface: every call is
	// refused. The value is never logged.
	staffServiceToken := os.Getenv("AUTH_STAFF_SERVICE_TOKEN")
	if staffServiceToken == "" {
		log.Info("staff service disabled", "reason", "AUTH_STAFF_SERVICE_TOKEN is empty")
	}

	gs := googlegrpc.NewServer(serverOptions(signer, staffServiceToken)...)
	authv1.RegisterAuthServiceServer(gs, grpchandlers.New(svc))
	authv1.RegisterOrgServiceServer(gs, grpchandlers.NewOrg(svc))
	authv1.RegisterTenantServiceServer(gs, grpchandlers.NewTenant(svc))
	authv1.RegisterInviteServiceServer(gs, grpchandlers.NewInvite(svc))
	authv1.RegisterSignupServiceServer(gs, grpchandlers.NewSignup(svc))
	authv1.RegisterStaffOrgServiceServer(gs, grpchandlers.NewStaffOrg(svc))

	health := httpapi.New(flagService)
	healthPort := envOr("HEALTH_PORT", "10011")

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
// for OrgService and for AuthService.SetUsername, plus the service-token guard
// for StaffOrgService. The access interceptor runs after internal auth and
// before those handlers. The other AuthService RPCs keep their unauthenticated
// contract.
func serverOptions(signer *jwt.Signer, staffServiceToken string) []googlegrpc.ServerOption {
	opts := observability.GRPCServerOptions()
	return append(opts, googlegrpc.ChainUnaryInterceptor(
		grpchandlers.AccessTokenUnaryServerInterceptor(signer.KeySet()),
		grpchandlers.StaffServiceUnaryServerInterceptor(staffServiceToken),
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

// inviteAcceptHostsFromEnv reads AUTH_INVITE_ACCEPT_HOSTS, a comma-separated
// list of hosts. Entries are trimmed and lower-cased, and empty entries are
// dropped. Empty means no invitation accept_url is accepted.
func inviteAcceptHostsFromEnv() []string {
	var hosts []string
	for _, raw := range strings.Split(os.Getenv("AUTH_INVITE_ACCEPT_HOSTS"), ",") {
		if host := strings.ToLower(strings.TrimSpace(raw)); host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// defaultCallingCodeFromEnv reads AUTH_DEFAULT_CALLING_CODE: the country
// calling code in digits only, without "+". Empty means local mobile numbers
// are not accepted at sign-in. Any other value fails before the service starts.
func defaultCallingCodeFromEnv() (string, error) {
	code := strings.TrimSpace(os.Getenv("AUTH_DEFAULT_CALLING_CODE"))
	for _, r := range code {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("AUTH_DEFAULT_CALLING_CODE must contain digits only, without \"+\"")
		}
	}
	return code, nil
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
