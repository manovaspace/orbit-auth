package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// seedChallenge stores a challenge for identifier, channel and purpose holding
// code, created at createdAt. It uses the lowest bcrypt cost, which the test
// hasher accepts.
func seedChallenge(t *testing.T, otp *fakeOTP, id, identifier, channel string, purpose domain.OTPPurpose, code string, createdAt time.Time) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := otp.UpsertChallenge(context.Background(), domain.OTPChallenge{
		ID:         id,
		Identifier: identifier,
		Channel:    channel,
		Purpose:    purpose,
		CodeHash:   string(hash),
		ExpiresAt:  createdAt.Add(domain.OTPTTL),
		CreatedAt:  createdAt,
	}); err != nil {
		t.Fatal(err)
	}
}

// A sign-in code for the invited number is not a code for the invitation.
func TestSignInCodeCannotAcceptPhoneInvitation(t *testing.T) {
	svc, store, _, _, token := phoneInviteFixture(t, domain.RoleMember)
	ctx := context.Background()
	store.otp.deleteChallenges(invitedMobile, domain.ChannelSMS, domain.OTPPurposeInvite)
	seedChallenge(t, store.otp, "ch-sign-in-same-number", invitedMobile, domain.ChannelSMS,
		domain.OTPPurposeSignIn, invitedCode, signupNow().Add(-2*time.Minute))

	if _, err := svc.AcceptInviteWithSignup(ctx, token, "correct horse", "Sara", invitedCode); !errors.Is(err, ErrInvalidOTPCode) {
		t.Fatalf("sign-in code accepted the invitation: err = %v, want ErrInvalidOTPCode", err)
	}
	if _, ok := store.userByMobile(invitedMobile); ok {
		t.Fatal("a sign-in code wrote an account for the invitation")
	}
}

// An invitation code is not a sign-in code, and it stays usable for the
// invitation after the refused sign-in.
func TestInviteCodeCannotSignIn(t *testing.T) {
	svc, store, _, _, token := phoneInviteFixture(t, domain.RoleMember)
	ctx := context.Background()

	if _, _, _, err := svc.VerifyOTP(ctx, invitedMobile, domain.ChannelSMS, invitedCode); !errors.Is(err, ErrInvalidOTPCode) {
		t.Fatalf("invitation code signed in: err = %v, want ErrInvalidOTPCode", err)
	}
	if _, ok := store.userByMobile(invitedMobile); ok {
		t.Fatal("a refused invitation code wrote an account")
	}
	if _, err := svc.AcceptInviteWithSignup(ctx, token, "correct horse", "Sara", invitedCode); err != nil {
		t.Fatalf("invitation code after the refused sign-in: %v", err)
	}
}

// A sign-in request for the invited number neither blocks nor breaks the
// pending invitation code, and the invitation cooldown still holds for the
// invitation.
func TestSignInRequestLeavesInviteCodeUsable(t *testing.T) {
	_, store, _, _, token := phoneInviteFixture(t, domain.RoleMember)
	ctx := context.Background()
	clock := &mutableClock{at: signupNow()}
	inviteNotifier := &fakeNotifier{}
	inviteSvc := phoneCodeService(store, inviteNotifier, clock, "", true)

	// The invitation code is requested now, so the invitation cooldown is active.
	if _, err := inviteSvc.RequestInvitePhoneCode(ctx, token); err != nil {
		t.Fatalf("invitation code: %v", err)
	}
	inviteCode := inviteNotifier.otpCode

	signInNotifier := &fakeNotifier{}
	signInSvc := phoneCodeService(store, signInNotifier, clock, "", true)
	if _, err := signInSvc.RequestOTP(ctx, invitedMobile, domain.ChannelSMS, "c1"); err != nil {
		t.Fatalf("sign-in request during the invitation cooldown: %v", err)
	}
	if signInNotifier.otpCalls != 1 {
		t.Fatalf("sign-in notifier calls = %d, want 1", signInNotifier.otpCalls)
	}

	if _, err := inviteSvc.RequestInvitePhoneCode(ctx, token); !errors.Is(err, ErrOTPCooldown) {
		t.Fatalf("invitation resend after a sign-in request: err = %v, want ErrOTPCooldown", err)
	}
	if _, err := inviteSvc.AcceptInviteWithSignup(ctx, token, "correct horse", "Sara", inviteCode); err != nil {
		t.Fatalf("invitation code after a sign-in request: %v", err)
	}
}

// An email sign-up code is not a sign-in code for that email, and it still
// verifies the sign-up.
func TestSignupCodeCannotSignIn(t *testing.T) {
	store := newSignupStore()
	notifier := &fakeNotifier{}
	svc := newSignupService(t, store, notifier, true)
	ctx := context.Background()
	const email = "new@example.com"

	if err := svc.RegisterWithEmail(ctx, email, "owner-pass", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	code := notifier.signupCode

	if _, _, _, err := svc.VerifyOTP(ctx, email, domain.ChannelEmail, code); !errors.Is(err, ErrInvalidOTPCode) {
		t.Fatalf("sign-up code signed in: err = %v, want ErrInvalidOTPCode", err)
	}
	if user := store.user(t, email); user.Status != domain.UserSignupPending {
		t.Fatalf("status = %q, want signup_pending after the refused sign-in", user.Status)
	}
	if _, _, _, err := svc.VerifySignup(ctx, email, code, "owner-pass"); err != nil {
		t.Fatalf("sign-up code after the refused sign-in: %v", err)
	}
}
