package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/manovaspace/orbit-auth/internal/domain"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/postgres/migrations"
	"github.com/manovaspace/orbit-auth/internal/testutil"
)

func migrationPool(t *testing.T) (*Store, *testutil.Database) {
	t.Helper()
	db := testutil.OpenPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	m, err := migrations.Open(db.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}
	return New(db.Pool), db
}

func TestOTPChallengesRoundTrip(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()

	challenge := domain.OTPChallenge{
		ID:         uuid.NewString(),
		Identifier: "person@example.com",
		Channel:    domain.ChannelEmail,
		CodeHash:   "code-hash",
		ExpiresAt:  time.Now().UTC().Add(10 * time.Minute).Truncate(time.Microsecond),
	}
	if err := store.UpsertChallenge(ctx, challenge); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetLatestChallenge(ctx, challenge.Identifier, challenge.Channel)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != challenge.ID || got.CodeHash != "code-hash" || got.Attempts != 0 {
		t.Fatalf("challenge = %+v", got)
	}
	if !got.ExpiresAt.Equal(challenge.ExpiresAt) {
		t.Fatalf("expires_at = %v, want %v", got.ExpiresAt, challenge.ExpiresAt)
	}

	if err := store.IncrementAttempts(ctx, challenge.ID); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetLatestChallenge(ctx, challenge.Identifier, challenge.Channel)
	if err != nil || got.Attempts != 1 {
		t.Fatalf("attempts = %d, err = %v", got.Attempts, err)
	}

	if err := store.ConsumeChallenge(ctx, challenge.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetLatestChallenge(ctx, challenge.Identifier, challenge.Channel); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("consumed challenge err = %v, want no rows", err)
	}

	// Expired challenges are deleted; live ones are kept.
	expired := domain.OTPChallenge{ID: uuid.NewString(), Identifier: "old@example.com", Channel: domain.ChannelEmail, CodeHash: "h", ExpiresAt: time.Now().UTC().Add(-time.Minute)}
	if err := store.UpsertChallenge(ctx, expired); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteExpiredChallenges(ctx); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM otp_challenges`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("%d challenges remain, want 0", remaining)
	}
}

func TestUsersFindOrCreateAndLastLogin(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()

	created, err := store.FindOrCreateByEmail(ctx, "person@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Email != "person@example.com" || created.Status != domain.UserActive {
		t.Fatalf("created user = %+v", created)
	}
	again, err := store.FindOrCreateByEmail(ctx, "person@example.com")
	if err != nil || again.ID != created.ID {
		t.Fatalf("second find = %+v, err = %v", again, err)
	}
	found, err := store.FindByEmail(ctx, "person@example.com")
	if err != nil || found.ID != created.ID || found.Status != domain.UserActive {
		t.Fatalf("find = %+v, err = %v", found, err)
	}
	if _, err := store.FindByEmail(ctx, "missing@example.com"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing email err = %v", err)
	}

	mobileUser, err := store.FindOrCreateByMobile(ctx, "+989121234567")
	if err != nil {
		t.Fatal(err)
	}
	again, err = store.FindOrCreateByMobile(ctx, "+989121234567")
	if err != nil || again.ID != mobileUser.ID {
		t.Fatalf("mobile find = %+v, err = %v", again, err)
	}
	if _, err := store.FindByMobile(ctx, "+989121234567"); err != nil {
		t.Fatal(err)
	}

	if err := store.UpdateLastLogin(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	var lastLogin *time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT last_login_at FROM users WHERE id = $1::uuid`, created.ID).Scan(&lastLogin); err != nil {
		t.Fatal(err)
	}
	if lastLogin == nil {
		t.Fatal("last_login_at was not set")
	}

	// A user lookup for a missing mobile is a no-row result, not an error path
	// that creates a user.
	if _, err := store.FindByMobile(ctx, "+989129999999"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing mobile err = %v", err)
	}
}

func TestUpsertDemoUserUpdatesByEmailAndMobile(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()

	if err := store.UpsertDemoUser(ctx, "demo@example.com", "", "hash-1", "Demo"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertDemoUser(ctx, "demo@example.com", "", "hash-2", "Demo Two"); err != nil {
		t.Fatal(err)
	}
	var count int
	var hash, name string
	if err := db.Pool.QueryRow(ctx, `SELECT count(*), max(password_hash), max(display_name) FROM users WHERE email = 'demo@example.com'`).Scan(&count, &hash, &name); err != nil {
		t.Fatal(err)
	}
	if count != 1 || hash != "hash-2" || name != "Demo Two" {
		t.Fatalf("demo user count=%d hash=%q name=%q", count, hash, name)
	}

	if err := store.UpsertDemoUser(ctx, "", "+989121234567", "hash-3", "Mobile"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertDemoUser(ctx, "", "+989121234567", "hash-4", "Mobile Two"); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*), max(password_hash) FROM users WHERE mobile = '+989121234567'`).Scan(&count, &hash); err != nil {
		t.Fatal(err)
	}
	if count != 1 || hash != "hash-4" {
		t.Fatalf("demo mobile count=%d hash=%q", count, hash)
	}
}

func TestRefreshTokensAreStoredHashedAndRevoked(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()

	user, err := store.FindOrCreateByEmail(ctx, "person@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Store(ctx, user.ID, "hash-1", time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	userID, err := store.FindValid(ctx, "hash-1")
	if err != nil || userID != user.ID {
		t.Fatalf("find = %q, %v", userID, err)
	}
	if err := store.Revoke(ctx, "hash-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FindValid(ctx, "hash-1"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("revoked token err = %v", err)
	}

	// An expired token is not valid.
	if err := store.Store(ctx, user.ID, "hash-2", time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FindValid(ctx, "hash-2"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expired token err = %v", err)
	}

	var stored string
	if err := db.Pool.QueryRow(ctx, `SELECT token_hash FROM refresh_tokens WHERE token_hash = 'hash-1'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "hash-1" {
		t.Fatalf("stored hash = %q", stored)
	}
}

func TestAPITokensCreateListRevokeAndFind(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()

	user, err := store.FindOrCreateByEmail(ctx, "person@example.com")
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Microsecond)
	token := domain.APIToken{
		ID:        uuid.NewString(),
		UserID:    user.ID,
		Name:      "deploy",
		Prefix:    "abcdefgh",
		TokenHash: "token-hash",
		Scopes:    []string{"read", "write"},
		ExpiresAt: &expires,
	}
	if err := store.Create(ctx, token); err != nil {
		t.Fatal(err)
	}

	list, err := store.ListByUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != token.ID || list[0].Prefix != "abcdefgh" || len(list[0].Scopes) != 2 {
		t.Fatalf("list = %+v", list)
	}
	if list[0].ExpiresAt == nil || !list[0].ExpiresAt.Equal(expires) {
		t.Fatalf("expires_at = %v, want %v", list[0].ExpiresAt, expires)
	}

	found, err := store.FindByPrefix(ctx, "abcdefgh")
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != token.ID || found.UserID != user.ID || found.TokenHash != "token-hash" {
		t.Fatalf("found = %+v", found)
	}
	if found.RevokedAt != nil {
		t.Fatalf("live token has revoked_at %v", found.RevokedAt)
	}

	if err := store.RevokeToken(ctx, user.ID, token.ID); err != nil {
		t.Fatal(err)
	}
	list, err = store.ListByUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("revoked token still listed: %+v", list)
	}
	if _, err := store.FindByPrefix(ctx, "abcdefgh"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("revoked token find err = %v", err)
	}
}

func TestAPITokenWithoutExpiry(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()

	user, err := store.FindOrCreateByEmail(ctx, "person@example.com")
	if err != nil {
		t.Fatal(err)
	}
	token := domain.APIToken{ID: uuid.NewString(), UserID: user.ID, Name: "ci", Prefix: "12345678", TokenHash: "h", Scopes: []string{}}
	if err := store.Create(ctx, token); err != nil {
		t.Fatal(err)
	}
	found, err := store.FindByPrefix(ctx, "12345678")
	if err != nil {
		t.Fatal(err)
	}
	if found.ExpiresAt != nil {
		t.Fatalf("expires_at = %v, want nil", found.ExpiresAt)
	}
}

func TestStoreRejectsInvalidUUIDs(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()

	if err := store.UpsertChallenge(ctx, domain.OTPChallenge{ID: "not-a-uuid"}); err == nil {
		t.Fatal("invalid challenge id was accepted")
	}
	if err := store.Create(ctx, domain.APIToken{ID: "not-a-uuid", UserID: "also-bad"}); err == nil {
		t.Fatal("invalid api token id was accepted")
	}
	if err := store.Store(ctx, "not-a-uuid", "hash", time.Now()); err == nil {
		t.Fatal("invalid user id was accepted")
	}
}
