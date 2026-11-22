package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// acceptWithGoogle runs AcceptInviteWithIdentity with a fresh candidate user ID.
func acceptWithGoogle(store *Store, id domain.GoogleIdentity, inviteID string, now time.Time, allowSignup bool) (domain.InviteAcceptance, error) {
	return store.AcceptInviteWithIdentity(context.Background(), domain.GoogleProviderName, id, inviteID, now, uuid.NewString(), allowSignup)
}

func googleIdentityCount(t *testing.T, store *Store, userID string) int {
	t.Helper()
	var n int
	if err := store.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM user_identities WHERE provider = 'google' AND user_id = $1::uuid`, userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func inviteStatusOf(t *testing.T, store *Store, inviteID string) string {
	t.Helper()
	var status string
	if err := store.pool.QueryRow(context.Background(),
		`SELECT status FROM organization_invites WHERE id = $1::uuid`, inviteID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestAcceptInviteWithGoogleCreatesAccountForVerifiedMatchingEmail(t *testing.T) {
	store, org, invite, now := emailInviteFixtureStore(t, "member@example.com", "google-new-token")

	res, err := acceptWithGoogle(store, googleIdentity("sub-new", "Member@Example.com", true), invite.ID, now, true)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if res.User.Email != "member@example.com" || !res.User.EmailVerified || res.User.Status != domain.UserActive {
		t.Fatalf("user = %+v, want an active verified account", res.User)
	}
	if res.OrgID != org.ID || res.Role != domain.RoleMember || res.AlreadyMember {
		t.Fatalf("acceptance = %+v", res)
	}
	if googleIdentityCount(t, store, res.User.ID) != 1 {
		t.Fatal("google identity row missing")
	}
	if inviteStatusOf(t, store, invite.ID) != string(domain.InviteAccepted) {
		t.Fatal("invite was not marked accepted")
	}

	// Single use: a second completion of the same invite finds nothing pending.
	if _, err := acceptWithGoogle(store, googleIdentity("sub-other", "member@example.com", true), invite.ID, now, true); !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("second accept err = %v, want ErrInviteNotFound", err)
	}
}

func TestAcceptInviteWithGoogleRefusesUnverifiedEmailAndWritesNothing(t *testing.T) {
	store, _, invite, now := emailInviteFixtureStore(t, "member@example.com", "google-unverified-token")
	ctx := context.Background()

	_, err := acceptWithGoogle(store, googleIdentity("sub-unverified", "member@example.com", false), invite.ID, now, true)
	if !errors.Is(err, domain.ErrInviteEmailMismatch) {
		t.Fatalf("err = %v, want ErrInviteEmailMismatch", err)
	}
	if _, err := store.FindByEmail(ctx, "member@example.com"); err == nil {
		t.Fatal("account created for an unverified Google email")
	}
	if inviteStatusOf(t, store, invite.ID) != string(domain.InvitePending) {
		t.Fatal("invite changed after a refused sign-in")
	}
}

func TestAcceptInviteWithGoogleRefusesNewAccountWhenSignupIsOff(t *testing.T) {
	store, _, invite, now := emailInviteFixtureStore(t, "member@example.com", "google-signup-off-token")

	_, err := acceptWithGoogle(store, googleIdentity("sub-off", "member@example.com", true), invite.ID, now, false)
	if !errors.Is(err, domain.ErrInviteSignupDisabled) {
		t.Fatalf("err = %v, want ErrInviteSignupDisabled", err)
	}
	if inviteStatusOf(t, store, invite.ID) != string(domain.InvitePending) {
		t.Fatal("invite changed although sign-up was refused")
	}
}

func TestAcceptInviteWithGoogleRefusesAnExpiredOrUnknownInvite(t *testing.T) {
	store, _, invite, now := emailInviteFixtureStore(t, "member@example.com", "google-expired-token")

	// At expires_at the invite is expired, so it is refused at the boundary.
	if _, err := acceptWithGoogle(store, googleIdentity("sub-exp", "member@example.com", true), invite.ID, invite.ExpiresAt, true); !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("expired err = %v, want ErrInviteNotFound", err)
	}
	if _, err := acceptWithGoogle(store, googleIdentity("sub-unknown", "member@example.com", true), uuid.NewString(), now, true); !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("unknown invite err = %v, want ErrInviteNotFound", err)
	}
}

func TestAcceptInviteWithGoogleTakesOverPendingSignupAccount(t *testing.T) {
	store, org, invite, now := emailInviteFixtureStore(t, "member@example.com", "google-takeover-token")
	ctx := context.Background()
	pendingID := uuid.NewString()
	if created, err := store.CreateSignupUser(ctx, domain.User{
		ID: pendingID, Email: "member@example.com", PasswordHash: "old-hash", DisplayName: "Old", Status: domain.UserSignupPending,
	}, nil, nil, now); err != nil || !created {
		t.Fatalf("pending account: created=%v err=%v", created, err)
	}

	// Taking over a signup_pending account counts as a new account for the
	// sign-up switch.
	if _, err := acceptWithGoogle(store, googleIdentity("sub-takeover", "member@example.com", true), invite.ID, now, false); !errors.Is(err, domain.ErrInviteSignupDisabled) {
		t.Fatalf("takeover with sign-up off err = %v, want ErrInviteSignupDisabled", err)
	}
	res, err := acceptWithGoogle(store, googleIdentity("sub-takeover", "member@example.com", true), invite.ID, now, true)
	if err != nil {
		t.Fatalf("takeover: %v", err)
	}
	if res.User.ID != pendingID || res.User.Status != domain.UserActive || !res.User.EmailVerified || res.OrgID != org.ID {
		t.Fatalf("acceptance = %+v, want the pending account active", res)
	}
	if googleIdentityCount(t, store, pendingID) != 1 {
		t.Fatal("google identity not linked to the taken-over account")
	}
}

func TestAcceptInviteWithGoogleLinksActiveAccountOnlyWhenAuthoritative(t *testing.T) {
	store, _, invite, now := emailInviteFixtureStore(t, "member@example.com", "google-link-token")
	ctx := context.Background()
	existing, err := store.FindOrCreateByEmail(ctx, "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertDemoUser(ctx, "member@example.com", "", "member-name", "hash", "Member"); err != nil {
		t.Fatal(err)
	}

	// A non-authoritative email (no hosted domain, not gmail) is refused.
	if _, err := acceptWithGoogle(store, googleIdentity("sub-plain", "member@example.com", true), invite.ID, now, true); !errors.Is(err, domain.ErrGoogleAccountNotAllowed) {
		t.Fatalf("non-authoritative err = %v, want ErrGoogleAccountNotAllowed", err)
	}
	if inviteStatusOf(t, store, invite.ID) != string(domain.InvitePending) {
		t.Fatal("invite changed after a refused link")
	}

	res, err := acceptWithGoogle(store, workspaceIdentity("sub-workspace", "member@example.com"), invite.ID, now, true)
	if err != nil {
		t.Fatalf("authoritative link: %v", err)
	}
	if res.User.ID != existing.ID {
		t.Fatalf("linked user = %s, want the existing account %s", res.User.ID, existing.ID)
	}
	if googleIdentityCount(t, store, existing.ID) != 1 {
		t.Fatal("google identity not linked")
	}
}

func TestAcceptInviteWithGoogleRefusesDisabledAccount(t *testing.T) {
	store, _, invite, now := emailInviteFixtureStore(t, "member@example.com", "google-disabled-token")
	ctx := context.Background()
	existing, err := store.FindOrCreateByEmail(ctx, "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DisableUser(ctx, existing.ID, domain.StaffActor{Subject: "staff-1", Reason: "test"}); err != nil {
		t.Fatal(err)
	}

	if _, err := acceptWithGoogle(store, workspaceIdentity("sub-disabled", "member@example.com"), invite.ID, now, true); !errors.Is(err, domain.ErrGoogleAccountNotAllowed) {
		t.Fatalf("disabled err = %v, want ErrGoogleAccountNotAllowed", err)
	}
	if googleIdentityCount(t, store, existing.ID) != 0 {
		t.Fatal("identity linked to a disabled account")
	}
}

func TestAcceptInviteWithGoogleKnownIdentityMustMatchTheInvitedEmail(t *testing.T) {
	store, _, invite, now := emailInviteFixtureStore(t, "member@example.com", "google-known-token")
	ctx := context.Background()
	other, err := store.SignInWithIdentity(ctx, domain.GoogleProviderName, workspaceIdentity("sub-known", "other@example.com"), now, true)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := acceptWithGoogle(store, workspaceIdentity("sub-known", "other@example.com"), invite.ID, now, true); !errors.Is(err, domain.ErrInviteEmailMismatch) {
		t.Fatalf("known mismatch err = %v, want ErrInviteEmailMismatch", err)
	}
	if inviteStatusOf(t, store, invite.ID) != string(domain.InvitePending) {
		t.Fatal("invite changed after a mismatch")
	}
	if googleIdentityCount(t, store, other.ID) != 1 {
		t.Fatal("identity row changed after a mismatch")
	}
}

// Two completions of the same invite with different Google subjects race for
// one invitation: exactly one succeeds and the other sees the invite as used.
func TestAcceptInviteWithGoogleConcurrentCompletionHasOneWinner(t *testing.T) {
	store, org, invite, now := emailInviteFixtureStore(t, "member@example.com", "google-race-token")

	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = acceptWithGoogle(store, googleIdentity("sub-race-"+string(rune('a'+i)), "member@example.com", true), invite.ID, now, true)
		}(i)
	}
	wg.Wait()

	wins := 0
	for _, err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, domain.ErrInviteNotFound):
		default:
			t.Fatalf("unexpected error from losing completion: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("winners = %d, want exactly 1 (results %v)", wins, results)
	}
	var members int
	if err := store.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM organization_memberships WHERE org_id = $1::uuid`, org.ID).Scan(&members); err != nil {
		t.Fatal(err)
	}
	if members != 2 { // the owner created by the fixture plus the single winner
		t.Fatalf("memberships = %d, want 2", members)
	}
}

func TestOAuthStatePurposeRoundTripsAndIsChecked(t *testing.T) {
	store, _, invite, now := emailInviteFixtureStore(t, "member@example.com", "google-state-token")
	ctx := context.Background()
	user, err := store.FindOrCreateByEmail(ctx, "state-link@example.com")
	if err != nil {
		t.Fatal(err)
	}
	expires := now.Add(domain.OAuthStateTTL)

	inviteHash := stateHashOf("state-invite")
	if err := store.CreateOAuthState(ctx, inviteHash, domain.OAuthState{RedirectURI: "https://app.example.com/cb", ReturnTo: "/", Purpose: domain.OAuthPurposeInvite, InviteID: invite.ID}, expires); err != nil {
		t.Fatal(err)
	}
	got, err := store.ConsumeOAuthState(ctx, inviteHash, now)
	if err != nil || got.Purpose != domain.OAuthPurposeInvite || got.InviteID != invite.ID || got.UserID != "" {
		t.Fatalf("invite state = %+v, err = %v", got, err)
	}

	linkHash := stateHashOf("state-link")
	if err := store.CreateOAuthState(ctx, linkHash, domain.OAuthState{RedirectURI: "https://app.example.com/cb", ReturnTo: "/settings", Purpose: domain.OAuthPurposeLink, UserID: user.ID}, expires); err != nil {
		t.Fatal(err)
	}
	got, err = store.ConsumeOAuthState(ctx, linkHash, now)
	if err != nil || got.Purpose != domain.OAuthPurposeLink || got.UserID != user.ID || got.InviteID != "" {
		t.Fatalf("link state = %+v, err = %v", got, err)
	}

	// The shape check: an invite state needs an invite and no user, a link
	// state needs a user and no invite.
	bad := []domain.OAuthState{
		{RedirectURI: "https://app.example.com/cb", Purpose: domain.OAuthPurposeInvite},
		{RedirectURI: "https://app.example.com/cb", Purpose: domain.OAuthPurposeLink, UserID: user.ID, InviteID: invite.ID},
		{RedirectURI: "https://app.example.com/cb", Purpose: domain.OAuthPurposeSignIn, UserID: user.ID},
	}
	for i, st := range bad {
		err := store.CreateOAuthState(ctx, stateHashOf("bad-"+string(rune('a'+i))), st, expires)
		if err == nil {
			t.Fatalf("bad state %d stored: %+v", i, st)
		}
	}
}
