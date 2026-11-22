package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// stateHashOf returns the stored form of a state: its SHA-256 hex digest.
func stateHashOf(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}

func TestOAuthStateIsSingleUseAndExpires(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()
	now := time.Now().UTC()

	hash := stateHashOf("state-one")
	if err := store.CreateOAuthState(ctx, hash, "https://app.example.com/cb", "/inbox", now.Add(domain.OAuthStateTTL)); err != nil {
		t.Fatal(err)
	}
	redirect, returnTo, err := store.ConsumeOAuthState(ctx, hash, now)
	if err != nil || redirect != "https://app.example.com/cb" || returnTo != "/inbox" {
		t.Fatalf("consume = %q, %q, %v", redirect, returnTo, err)
	}
	if _, _, err := store.ConsumeOAuthState(ctx, hash, now); !errors.Is(err, domain.ErrInvalidGoogleState) {
		t.Fatalf("second consume err = %v, want ErrInvalidGoogleState", err)
	}

	expired := stateHashOf("state-two")
	if err := store.CreateOAuthState(ctx, expired, "https://app.example.com/cb", "/", now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ConsumeOAuthState(ctx, expired, now); !errors.Is(err, domain.ErrInvalidGoogleState) {
		t.Fatalf("expired consume err = %v, want ErrInvalidGoogleState", err)
	}
	if err := store.DeleteExpiredOAuthStates(ctx, now); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM oauth_sign_in_states`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("states left after cleanup = %d, err = %v", left, err)
	}

	// The database pins the hash to 64 lower-case hex characters.
	err = store.CreateOAuthState(ctx, "not-a-hash", "https://app.example.com/cb", "/", now.Add(time.Minute))
	if err == nil || !strings.Contains(err.Error(), "oauth_sign_in_states_state_hash_check") {
		t.Fatalf("bad hash err = %v, want the state_hash check", err)
	}
}

func googleIdentity(subject, email string, verified bool) domain.GoogleIdentity {
	return domain.GoogleIdentity{Subject: subject, Email: email, EmailVerified: verified, Name: "Ali"}
}

// workspaceIdentity is a verified identity of a Google Workspace domain, which
// makes its email authoritative.
func workspaceIdentity(subject, email string) domain.GoogleIdentity {
	return domain.GoogleIdentity{Subject: subject, Email: email, EmailVerified: true, HostedDomain: "example.com", Name: "Ali"}
}

func TestSignInWithIdentityLinkingRules(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := db.Pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Rule 3: a new authoritative email creates a verified account with the
	// email, the display name and one user.created event.
	events := count(`SELECT count(*) FROM outbox_events`)
	first, err := store.SignInWithIdentity(ctx, domain.GoogleProviderName, googleIdentity("sub-1", "Ali@Gmail.com", true), now, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if first.Email != "ali@gmail.com" || !first.EmailVerified || first.Status != domain.UserActive || first.DisplayName != "Ali" {
		t.Fatalf("created user = %+v", first)
	}
	if got := count(`SELECT count(*) FROM outbox_events`); got != events+1 {
		t.Fatalf("outbox events after create = %d, want %d", got, events+1)
	}

	// Rule 1: the known subject returns the same user, even when its email now
	// names another verified account. It is never relinked.
	other, err := store.FindOrCreateByEmail(ctx, "other@example.com")
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.SignInWithIdentity(ctx, domain.GoogleProviderName, workspaceIdentity("sub-1", "other@example.com"), now, false)
	if err != nil || again.ID != first.ID {
		t.Fatalf("known subject = %+v, err = %v; want user %s", again, err, first.ID)
	}
	if count(`SELECT count(*) FROM user_identities WHERE provider = 'google' AND user_id = $1::uuid`, other.ID) != 0 {
		t.Fatal("a known subject was relinked to another account")
	}

	// Rule 2: a verified account with no Google identity is linked.
	linked, err := store.SignInWithIdentity(ctx, domain.GoogleProviderName, workspaceIdentity("sub-2", "other@example.com"), now, false)
	if err != nil || linked.ID != other.ID {
		t.Fatalf("verified holder = %+v, err = %v; want user %s", linked, err, other.ID)
	}

	// Rule 2: the same account already has a Google identity, so another subject
	// with its email is refused.
	if _, err := store.SignInWithIdentity(ctx, domain.GoogleProviderName, workspaceIdentity("sub-3", "other@example.com"), now, true); !errors.Is(err, domain.ErrGoogleAccountNotAllowed) {
		t.Fatalf("second subject on a linked account err = %v, want ErrGoogleAccountNotAllowed", err)
	}

	// Rule 2: an existing unverified account with the email is refused, and
	// nothing is linked to it.
	if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, email, status) VALUES ($1::uuid, 'pending@example.com', 'signup_pending')`, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SignInWithIdentity(ctx, domain.GoogleProviderName, workspaceIdentity("sub-4", "pending@example.com"), now, true); !errors.Is(err, domain.ErrGoogleAccountNotAllowed) {
		t.Fatalf("unverified holder err = %v, want ErrGoogleAccountNotAllowed", err)
	}
	if count(`SELECT count(*) FROM user_identities WHERE subject = 'sub-4'`) != 0 {
		t.Fatal("an unverified account was linked")
	}

	// Rule 2 needs an authoritative email. A verified address on a domain that is
	// neither gmail.com nor the hosted domain does not link to the account.
	if _, err := store.SignInWithIdentity(ctx, domain.GoogleProviderName, googleIdentity("sub-8", "other@example.com", true), now, false); !errors.Is(err, domain.ErrGoogleAccountNotAllowed) {
		t.Fatalf("non-authoritative verified email err = %v, want ErrGoogleAccountNotAllowed", err)
	}
	if count(`SELECT count(*) FROM user_identities WHERE subject = 'sub-8'`) != 0 {
		t.Fatal("a non-authoritative email linked an account")
	}

	// Rule 3 with allowCreate=false: a new subject is refused and writes nothing.
	users := count(`SELECT count(*) FROM users`)
	if _, err := store.SignInWithIdentity(ctx, domain.GoogleProviderName, googleIdentity("sub-5", "new@example.com", true), now, false); !errors.Is(err, domain.ErrGoogleAccountNotAllowed) {
		t.Fatalf("closed sign-up err = %v, want ErrGoogleAccountNotAllowed", err)
	}
	if got := count(`SELECT count(*) FROM users`); got != users {
		t.Fatalf("users = %d, want %d: a closed sign-up created an account", got, users)
	}

	// A non-authoritative email (not verified) creates an account without the
	// email, even though an account with that email exists.
	nonAuth, err := store.SignInWithIdentity(ctx, domain.GoogleProviderName, googleIdentity("sub-6", "other@example.com", false), now, true)
	if err != nil {
		t.Fatalf("non-authoritative create: %v", err)
	}
	if nonAuth.ID == other.ID || nonAuth.Email != "" || nonAuth.EmailVerified {
		t.Fatalf("non-authoritative user = %+v; want a new account without email", nonAuth)
	}

	// Display names are trimmed and cut to 100 characters.
	long := googleIdentity("sub-7", "", false)
	long.Name = "  " + strings.Repeat("س", 150) + "  "
	named, err := store.SignInWithIdentity(ctx, domain.GoogleProviderName, long, now, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := []rune(named.DisplayName); len(got) != 100 || string(got) != strings.Repeat("س", 100) {
		t.Fatalf("display name has %d runes, want 100 trimmed", len(got))
	}
}

func TestGoogleIdentityCascadesWithUser(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()
	user, err := store.SignInWithIdentity(ctx, domain.GoogleProviderName, googleIdentity("sub-cascade", "cascade@gmail.com", true), time.Now().UTC(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, user.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM user_identities WHERE user_id = $1::uuid`, user.ID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("identities after user delete = %d, err = %v", left, err)
	}
}

func TestConcurrentFirstSignInGivesOneUser(t *testing.T) {
	cases := map[string]domain.GoogleIdentity{
		"authoritative email": googleIdentity("sub-race", "race@gmail.com", true),
		"no email":            googleIdentity("sub-race-2", "", false),
	}
	for name, id := range cases {
		t.Run(name, func(t *testing.T) {
			store, db := migrationPool(t)
			ctx := context.Background()
			const workers = 8
			var wg sync.WaitGroup
			ids := make([]string, workers)
			errs := make([]error, workers)
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					user, err := store.SignInWithIdentity(ctx, domain.GoogleProviderName, id, time.Now().UTC(), true)
					ids[i], errs[i] = user.ID, err
				}(i)
			}
			wg.Wait()
			for i := 0; i < workers; i++ {
				if errs[i] != nil {
					t.Fatalf("worker %d: %v", i, errs[i])
				}
				if ids[i] != ids[0] {
					t.Fatalf("worker %d got user %s, worker 0 got %s", i, ids[i], ids[0])
				}
			}
			var users, identities int
			if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil || users != 1 {
				t.Fatalf("users = %d, err = %v; want 1", users, err)
			}
			if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM user_identities`).Scan(&identities); err != nil || identities != 1 {
				t.Fatalf("identities = %d, err = %v; want 1", identities, err)
			}
		})
	}
}

func TestGoogleSignInLinksMixedCaseEmailWithoutDuplicate(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()
	existing := uuid.NewString()
	if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, email, status, email_verified_at) VALUES ($1::uuid, 'Mixed@Example.com', 'active', NOW())`, existing); err != nil {
		t.Fatal(err)
	}

	user, err := store.SignInWithIdentity(ctx, domain.GoogleProviderName, workspaceIdentity("sub-mixed", "mixed@example.com"), time.Now().UTC(), true)
	if err != nil || user.ID != existing {
		t.Fatalf("mixed-case holder = %+v, err = %v; want user %s", user, err, existing)
	}
	var users, identities int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil || users != 1 {
		t.Fatalf("users = %d, err = %v; want 1: sign-in duplicated the account", users, err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM user_identities WHERE provider = 'google' AND subject = 'sub-mixed' AND user_id = $1::uuid`, existing).Scan(&identities); err != nil || identities != 1 {
		t.Fatalf("google identities on the holder = %d, err = %v; want 1", identities, err)
	}
}

func TestGoogleSignInRefusesCaseOnlyDuplicateEmails(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()
	for _, email := range []string{"Dup@Example.com", "dup@example.com"} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, email, status, email_verified_at) VALUES ($1::uuid, $2, 'active', NOW())`, uuid.NewString(), email); err != nil {
			t.Fatal(err)
		}
	}

	_, err := store.SignInWithIdentity(ctx, domain.GoogleProviderName, workspaceIdentity("sub-dup", "dup@example.com"), time.Now().UTC(), true)
	if !errors.Is(err, domain.ErrGoogleAccountNotAllowed) {
		t.Fatalf("case-only duplicates err = %v, want ErrGoogleAccountNotAllowed", err)
	}
	var identities, users int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM user_identities WHERE subject = 'sub-dup'`).Scan(&identities); err != nil || identities != 0 {
		t.Fatalf("identities for refused sign-in = %d, err = %v; want 0", identities, err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil || users != 2 {
		t.Fatalf("users = %d, err = %v; want 2 untouched", users, err)
	}
}
