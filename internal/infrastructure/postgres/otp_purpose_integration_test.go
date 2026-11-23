package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// TestOTPChallengesAreReturnedOnlyForTheirPurpose writes a sign-in code and an
// invitation code for one number. Each lookup returns only the challenge of its
// own purpose, and a purpose with no challenge is not found.
func TestOTPChallengesAreReturnedOnlyForTheirPurpose(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()
	const phone = "+989121234567"
	now := time.Now().UTC().Truncate(time.Microsecond)

	signIn := domain.OTPChallenge{
		ID: uuid.NewString(), Identifier: phone, Channel: domain.ChannelSMS,
		Purpose: domain.OTPPurposeSignIn, CodeHash: "sign-in-hash", ExpiresAt: now.Add(domain.OTPTTL), CreatedAt: now,
	}
	invite := domain.OTPChallenge{
		ID: uuid.NewString(), Identifier: phone, Channel: domain.ChannelSMS,
		Purpose: domain.OTPPurposeInvite, CodeHash: "invite-hash", ExpiresAt: now.Add(domain.OTPTTL), CreatedAt: now.Add(time.Second),
	}
	for _, ch := range []domain.OTPChallenge{signIn, invite} {
		if err := store.UpsertChallenge(ctx, ch); err != nil {
			t.Fatalf("upsert %s: %v", ch.Purpose, err)
		}
	}

	for _, want := range []domain.OTPChallenge{signIn, invite} {
		got, err := store.GetLatestChallenge(ctx, phone, domain.ChannelSMS, want.Purpose)
		if err != nil {
			t.Fatalf("latest %s: %v", want.Purpose, err)
		}
		if got.ID != want.ID || got.Purpose != want.Purpose || got.CodeHash != want.CodeHash {
			t.Fatalf("latest %s = %+v, want challenge %s", want.Purpose, got, want.ID)
		}
	}
	if _, err := store.GetLatestChallenge(ctx, phone, domain.ChannelSMS, domain.OTPPurposeSignup); !errors.Is(err, domain.ErrChallengeNotFound) {
		t.Fatalf("signup lookup err = %v, want ErrChallengeNotFound", err)
	}
}

// TestUpsertChallengeRefusesEmptyPurpose checks that nothing relies on the
// database default: a challenge without a purpose is an error and writes no row.
func TestUpsertChallengeRefusesEmptyPurpose(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()
	const identifier = "no-purpose@example.com"
	now := time.Now().UTC().Truncate(time.Microsecond)

	err := store.UpsertChallenge(ctx, domain.OTPChallenge{
		ID: uuid.NewString(), Identifier: identifier, Channel: domain.ChannelEmail,
		CodeHash: "hash", ExpiresAt: now.Add(domain.OTPTTL), CreatedAt: now,
	})
	if err == nil {
		t.Fatal("upsert without a purpose was accepted")
	}
	var rows int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM otp_challenges WHERE identifier = $1`, identifier).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("a refused upsert wrote %d rows", rows)
	}
}

// TestReissuePendingSignupKeepsSignInChallenge checks that a sign-up reissue
// replaces only the sign-up challenge of the address. A sign-in code for the
// same address stays usable.
func TestReissuePendingSignupKeepsSignInChallenge(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	user := signupUser("keep-signin@example.com", domain.UserSignupPending)
	if created, err := store.CreateSignupUser(ctx, user, nil, nil, now); err != nil || !created {
		t.Fatalf("create pending signup = %v, %v", created, err)
	}
	signIn := domain.OTPChallenge{
		ID: uuid.NewString(), Identifier: user.Email, Channel: domain.ChannelEmail,
		Purpose: domain.OTPPurposeSignIn, CodeHash: "sign-in-hash", ExpiresAt: now.Add(domain.OTPTTL), CreatedAt: now,
	}
	if err := store.UpsertChallenge(ctx, signIn); err != nil {
		t.Fatal(err)
	}
	signup := domain.OTPChallenge{
		ID: uuid.NewString(), Identifier: user.Email, Channel: domain.ChannelEmail,
		Purpose: domain.OTPPurposeSignup, CodeHash: "signup-hash", ExpiresAt: now.Add(domain.OTPTTL), CreatedAt: now.Add(time.Minute),
	}
	updated := domain.User{Email: user.Email, PasswordHash: "owner-hash", DisplayName: "Owner"}
	if _, reissued, err := store.ReissuePendingSignup(ctx, updated, &signup, nil, now.Add(time.Minute)); err != nil || !reissued {
		t.Fatalf("reissue = %v, %v", reissued, err)
	}

	got, err := store.GetLatestChallenge(ctx, user.Email, domain.ChannelEmail, domain.OTPPurposeSignIn)
	if err != nil || got.ID != signIn.ID {
		t.Fatalf("sign-in challenge after reissue = %+v, %v; want %s", got, err, signIn.ID)
	}
	if _, err := store.GetLatestChallenge(ctx, user.Email, domain.ChannelEmail, domain.OTPPurposeSignup); err != nil {
		t.Fatalf("sign-up challenge after reissue: %v", err)
	}

	noPurpose := signup
	noPurpose.ID = uuid.NewString()
	noPurpose.Purpose = ""
	if _, _, err := store.ReissuePendingSignup(ctx, updated, &noPurpose, nil, now.Add(2*time.Minute)); err == nil {
		t.Fatal("reissue with a challenge without a purpose was accepted")
	}
	if got, err := store.GetLatestChallenge(ctx, user.Email, domain.ChannelEmail, domain.OTPPurposeSignup); err != nil || got.ID != signup.ID {
		t.Fatalf("refused reissue changed the sign-up challenge: %+v, %v", got, err)
	}
}
