package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// seedPhoneChallenge stores the SMS challenge RequestInvitePhoneCode writes for
// phone, holding code, created at createdAt. It uses the lowest bcrypt cost,
// which the test hasher accepts.
func seedPhoneChallenge(t *testing.T, otp *fakeOTP, phone, code string, createdAt time.Time) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := otp.UpsertChallenge(context.Background(), domain.OTPChallenge{
		ID:         "ch-invite-phone",
		Identifier: phone,
		Channel:    domain.ChannelSMS,
		CodeHash:   string(hash),
		ExpiresAt:  createdAt.Add(domain.OTPTTL),
		CreatedAt:  createdAt,
	}); err != nil {
		t.Fatal(err)
	}
}

// phoneCodeService builds the service over store for the phone-code tests.
// devCode is the DevSMSCode and mobileOTP the mobile_otp flag.
func phoneCodeService(store *signupStore, notifier *fakeNotifier, clock domain.Clock, devCode string, mobileOTP bool) *Service {
	return NewService(Deps{
		OTP:                 store.otp,
		Users:               store,
		Refresh:             newFakeRefreshStore(),
		Tokens:              fakeApiTokens{},
		Orgs:                store.orgs,
		Invites:             store.invites,
		Signup:              store,
		Notifier:            notifier,
		Flags:               fakeFlags{mobile: mobileOTP, email: true},
		Clock:               clock,
		IDs:                 testUUIDs{},
		Signer:              testSigner{},
		Hasher:              testHasher{},
		Codes:               testCodes{},
		Secrets:             testSecrets{},
		DevSMSCode:          devCode,
		AutoSelectSingleOrg: true,
		Logger:              func(string, ...any) {},
	})
}

func TestRequestInvitePhoneCodeDevCodeSendsNothing(t *testing.T) {
	_, store, _, _, token := phoneInviteFixture(t, domain.RoleMember)
	notifier := &fakeNotifier{}
	svc := phoneCodeService(store, notifier, fixedClock{at: signupNow()}, "123456", false)
	ctx := context.Background()

	expires, err := svc.RequestInvitePhoneCode(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if !expires.Equal(signupNow().Add(domain.OTPTTL)) {
		t.Fatalf("expires = %v, want %v", expires, signupNow().Add(domain.OTPTTL))
	}
	if notifier.otpCalls != 0 {
		t.Fatalf("dev code: the notifier was called %d times", notifier.otpCalls)
	}

	// The challenge holds the dev code, so accepting with it succeeds without
	// the mobile OTP flag.
	if _, err := svc.AcceptInviteWithSignup(ctx, token, "correct horse", "Sara", "123456"); err != nil {
		t.Fatalf("accept with the dev code: %v", err)
	}
	user, ok := store.userByMobile(invitedMobile)
	if !ok || !user.MobileVerified {
		t.Fatalf("user = %+v, ok = %v; want a verified mobile", user, ok)
	}
}

func TestRequestInvitePhoneCodeSendsCodeThroughNotifier(t *testing.T) {
	_, store, _, _, token := phoneInviteFixture(t, domain.RoleMember)
	notifier := &fakeNotifier{}
	svc := phoneCodeService(store, notifier, fixedClock{at: signupNow()}, "", true)
	ctx := context.Background()

	if _, err := svc.RequestInvitePhoneCode(ctx, token); err != nil {
		t.Fatal(err)
	}
	if notifier.otpCalls != 1 || notifier.lastChannel != domain.ChannelSMS || notifier.lastRecipient != invitedMobile {
		t.Fatalf("notifier calls = %d, channel = %q, recipient = %q", notifier.otpCalls, notifier.lastChannel, notifier.lastRecipient)
	}
	if _, err := svc.AcceptInviteWithSignup(ctx, token, "correct horse", "Sara", notifier.otpCode); err != nil {
		t.Fatalf("accept with the sent code: %v", err)
	}
}

func TestRequestInvitePhoneCodeNeedsMobileFlagWithoutDevCode(t *testing.T) {
	_, store, _, _, token := phoneInviteFixture(t, domain.RoleMember)
	notifier := &fakeNotifier{}
	ctx := context.Background()

	svc := phoneCodeService(store, notifier, fixedClock{at: signupNow()}, "", false)
	if _, err := svc.RequestInvitePhoneCode(ctx, token); !errors.Is(err, ErrMobileOTPDisabled) {
		t.Fatalf("err = %v, want ErrMobileOTPDisabled", err)
	}
	if notifier.otpCalls != 0 {
		t.Fatal("a disabled mobile OTP still sent a code")
	}

	// A dev code needs no flag.
	devSvc := phoneCodeService(store, notifier, fixedClock{at: signupNow()}, "123456", false)
	if _, err := devSvc.RequestInvitePhoneCode(ctx, token); err != nil {
		t.Fatalf("dev code with the flag off: %v", err)
	}
}

func TestRequestInvitePhoneCodeRefusesEmailInvite(t *testing.T) {
	store := newSignupStore()
	store.add(domain.User{ID: "owner", Email: "owner@example.com", DisplayName: "Owner", Status: domain.UserActive, EmailVerified: true})
	notifier := &fakeNotifier{}
	owner := newSignupService(t, store, notifier, true)
	ctx := context.Background()
	org, err := owner.CreateOrg(ctx, "owner", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := owner.InviteMember(ctx, org.ID, "owner", domain.InviteTarget{Email: "guest@example.com"}, domain.RoleMember, "")
	if err != nil {
		t.Fatal(err)
	}

	svc := phoneCodeService(store, notifier, fixedClock{at: signupNow()}, "123456", true)
	if _, err := svc.RequestInvitePhoneCode(ctx, token); !errors.Is(err, domain.ErrInvalidInvite) {
		t.Fatalf("err = %v, want ErrInvalidInvite", err)
	}
	if notifier.otpCalls != 0 {
		t.Fatal("an email invitation sent an SMS code")
	}
}

func TestRequestInvitePhoneCodeRefusedWhenInviteSignupIsOff(t *testing.T) {
	_, store, _, _, token := phoneInviteFixture(t, domain.RoleMember)
	svc := newInviteSignupService(store, true, true)

	if _, err := svc.RequestInvitePhoneCode(context.Background(), token); !errors.Is(err, ErrInviteSignupDisabled) {
		t.Fatalf("err = %v, want ErrInviteSignupDisabled", err)
	}
}

func TestRequestInvitePhoneCodeRefusesExistingAccount(t *testing.T) {
	_, store, _, _, token := phoneInviteFixture(t, domain.RoleMember)
	store.add(domain.User{ID: "existing", Mobile: invitedMobile, Status: domain.UserActive})
	notifier := &fakeNotifier{}
	svc := phoneCodeService(store, notifier, fixedClock{at: signupNow()}, "123456", true)

	if _, err := svc.RequestInvitePhoneCode(context.Background(), token); !errors.Is(err, ErrAccountExists) {
		t.Fatalf("err = %v, want ErrAccountExists", err)
	}
	if notifier.otpCalls != 0 {
		t.Fatal("an existing number was sent a code")
	}
}

func TestRequestInvitePhoneCodeCooldownIsSixtySeconds(t *testing.T) {
	_, store, _, _, token := phoneInviteFixture(t, domain.RoleMember)
	notifier := &fakeNotifier{}
	clock := &mutableClock{at: signupNow()}
	svc := phoneCodeService(store, notifier, clock, "", true)
	ctx := context.Background()

	if _, err := svc.RequestInvitePhoneCode(ctx, token); err != nil {
		t.Fatal(err)
	}
	clock.Advance(59 * time.Second)
	if _, err := svc.RequestInvitePhoneCode(ctx, token); !errors.Is(err, ErrOTPCooldown) {
		t.Fatalf("at 59 s: err = %v, want ErrOTPCooldown", err)
	}
	if notifier.otpCalls != 1 {
		t.Fatalf("during the cooldown the notifier was called %d times in total, want 1", notifier.otpCalls)
	}
	clock.Advance(time.Second)
	if _, err := svc.RequestInvitePhoneCode(ctx, token); err != nil {
		t.Fatalf("at 60 s: %v", err)
	}
	if notifier.otpCalls != 2 {
		t.Fatalf("after the cooldown the notifier was called %d times in total, want 2", notifier.otpCalls)
	}
}

// wrongGuess spends one attempt against the newest challenge for phone.
func wrongGuess(t *testing.T, svc *Service, token string) {
	t.Helper()
	if _, err := svc.AcceptInviteWithSignup(context.Background(), token, "correct horse", "Sara", "000000"); !errors.Is(err, ErrInvalidOTPCode) {
		t.Fatalf("wrong guess: err = %v, want ErrInvalidOTPCode", err)
	}
}

func TestOTPResendRefusedAfterWrongGuessUntilExpiry(t *testing.T) {
	_, store, _, _, token := phoneInviteFixture(t, domain.RoleMember)
	notifier := &fakeNotifier{}
	clock := &mutableClock{at: signupNow()}
	svc := phoneCodeService(store, notifier, clock, "", true)
	ctx := context.Background()

	// A fresh code, then one guess against it.
	if _, err := svc.RequestInvitePhoneCode(ctx, token); err != nil {
		t.Fatal(err)
	}
	clock.Advance(60 * time.Second)
	wrongGuess(t, svc, token)

	// Past the 60 s cooldown but with a failed attempt: still refused, until
	// the code expires.
	clock.Advance(8 * time.Minute)
	if _, err := svc.RequestInvitePhoneCode(ctx, token); !errors.Is(err, ErrOTPCooldown) {
		t.Fatalf("after a guess, before expiry: err = %v, want ErrOTPCooldown", err)
	}
	if notifier.otpCalls != 1 {
		t.Fatalf("notifier calls = %d, want 1", notifier.otpCalls)
	}

	// At expiry the code is spent, so a new one is allowed.
	clock.Advance(2 * time.Minute)
	if _, err := svc.RequestInvitePhoneCode(ctx, token); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
	if notifier.otpCalls != 2 {
		t.Fatalf("notifier calls = %d, want 2", notifier.otpCalls)
	}
}

func TestOTPResendAllowedAfterCooldownWithoutGuesses(t *testing.T) {
	_, store, _, _, token := phoneInviteFixture(t, domain.RoleMember)
	notifier := &fakeNotifier{}
	clock := &mutableClock{at: signupNow()}
	svc := phoneCodeService(store, notifier, clock, "", true)
	ctx := context.Background()

	if _, err := svc.RequestInvitePhoneCode(ctx, token); err != nil {
		t.Fatal(err)
	}
	clock.Advance(60 * time.Second)
	if _, err := svc.RequestInvitePhoneCode(ctx, token); err != nil {
		t.Fatalf("resend at 60 s with no guesses: %v", err)
	}
}

func TestRequestOTPTwiceWithinCooldownIsRefused(t *testing.T) {
	store := newSignupStore()
	notifier := &fakeNotifier{}
	clock := &mutableClock{at: signupNow()}
	svc := phoneCodeService(store, notifier, clock, "", true)
	ctx := context.Background()

	if _, err := svc.RequestOTP(ctx, "person@example.com", domain.ChannelEmail, ""); err != nil {
		t.Fatal(err)
	}
	clock.Advance(59 * time.Second)
	if _, err := svc.RequestOTP(ctx, "person@example.com", domain.ChannelEmail, ""); !errors.Is(err, ErrOTPCooldown) {
		t.Fatalf("second request at 59 s: err = %v, want ErrOTPCooldown", err)
	}
	if notifier.otpCalls != 1 {
		t.Fatalf("refused request sent a code: calls = %d", notifier.otpCalls)
	}
	clock.Advance(time.Second)
	if _, err := svc.RequestOTP(ctx, "person@example.com", domain.ChannelEmail, ""); err != nil {
		t.Fatalf("request at 60 s: %v", err)
	}
}

// erroringOTP makes GetLatestChallenge fail with a store error.
type erroringOTP struct {
	*fakeOTP
}

func (e erroringOTP) GetLatestChallenge(context.Context, string, string) (domain.OTPChallenge, error) {
	return domain.OTPChallenge{}, errors.New("database down")
}

func TestOTPResendStoreFailureFailsOpenAndLogs(t *testing.T) {
	_, store, _, _, token := phoneInviteFixture(t, domain.RoleMember)
	notifier := &fakeNotifier{}
	svc := phoneCodeService(store, notifier, fixedClock{at: signupNow()}, "", true)
	svc.otp = erroringOTP{store.otp}
	var logged []string
	svc.logger = func(msg string, args ...any) { logged = append(logged, fmt.Sprint(msg, args)) }

	if _, err := svc.RequestInvitePhoneCode(context.Background(), token); err != nil {
		t.Fatalf("store failure blocked the resend: %v", err)
	}
	if len(logged) == 0 || !strings.Contains(logged[0], "otp_resend_check_failed") {
		t.Fatalf("logs = %q, want otp_resend_check_failed", logged)
	}
}

func TestRequestInvitePhoneCodeLogsWithoutRawNumber(t *testing.T) {
	_, store, _, invite, token := phoneInviteFixture(t, domain.RoleMember)
	svc := phoneCodeService(store, &fakeNotifier{}, fixedClock{at: signupNow()}, "", true)
	var entries []string
	svc.logger = func(msg string, args ...any) {
		entries = append(entries, fmt.Sprint(msg, args))
	}

	if _, err := svc.RequestInvitePhoneCode(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(entries))
	}
	entry := entries[0]
	if !strings.Contains(entry, "invite_phone_code_requested") || !strings.Contains(entry, invite.ID) ||
		!strings.Contains(entry, domain.HashIdentifier(invitedMobile)) {
		t.Fatalf("log entry = %q", entry)
	}
	if strings.Contains(entry, invitedMobile) {
		t.Fatalf("log entry carries the raw number: %q", entry)
	}
}

func TestAcceptPhoneInviteRequiresCode(t *testing.T) {
	svc, store, _, invite, token := phoneInviteFixture(t, domain.RoleMember)
	ctx := context.Background()

	if _, err := svc.AcceptInviteWithSignup(ctx, token, "correct horse", "Sara", "  "); !errors.Is(err, ErrPhoneCodeRequired) {
		t.Fatalf("blank code: err = %v, want ErrPhoneCodeRequired", err)
	}
	if _, err := svc.AcceptInviteWithSignup(ctx, token, "correct horse", "Sara", "000000"); !errors.Is(err, ErrInvalidOTPCode) {
		t.Fatalf("wrong code: err = %v, want ErrInvalidOTPCode", err)
	}
	if store.otp.attempts != 1 {
		t.Fatalf("attempts = %d, want the wrong guess counted once", store.otp.attempts)
	}
	if _, ok := store.userByMobile(invitedMobile); ok {
		t.Fatal("a refused code wrote an account")
	}
	if got := store.invites.get(t, invite.ID); got.Status != domain.InvitePending {
		t.Fatalf("invite status = %q, want pending", got.Status)
	}
}

func TestAcceptPhoneInviteRejectsExpiredCode(t *testing.T) {
	_, store, _, _, token := phoneInviteFixture(t, domain.RoleMember)
	// Created eleven minutes before now: past the ten-minute TTL.
	seedPhoneChallenge(t, store.otp, invitedMobile, invitedCode, signupNow().Add(-11*time.Minute))
	svc := phoneCodeService(store, &fakeNotifier{}, fixedClock{at: signupNow()}, "", true)

	if _, err := svc.AcceptInviteWithSignup(context.Background(), token, "correct horse", "Sara", invitedCode); !errors.Is(err, ErrInvalidOTPCode) {
		t.Fatalf("err = %v, want ErrInvalidOTPCode", err)
	}
	if _, ok := store.userByMobile(invitedMobile); ok {
		t.Fatal("an expired code wrote an account")
	}
}

func TestAcceptPhoneInviteStopsAfterMaxAttempts(t *testing.T) {
	svc, store, _, _, token := phoneInviteFixture(t, domain.RoleMember)
	ctx := context.Background()

	for i := range domain.MaxOTPAttempts {
		if _, err := svc.AcceptInviteWithSignup(ctx, token, "correct horse", "Sara", "000000"); !errors.Is(err, ErrInvalidOTPCode) {
			t.Fatalf("attempt %d: err = %v, want ErrInvalidOTPCode", i+1, err)
		}
	}
	// The right code is refused once the budget is spent.
	if _, err := svc.AcceptInviteWithSignup(ctx, token, "correct horse", "Sara", invitedCode); !errors.Is(err, ErrTooManyOTPAttempts) {
		t.Fatalf("correct code after the limit: err = %v, want ErrTooManyOTPAttempts", err)
	}
	if _, ok := store.userByMobile(invitedMobile); ok {
		t.Fatal("an exhausted challenge wrote an account")
	}
}

func TestAcceptPhoneInviteWithCodeVerifiesMobileOnce(t *testing.T) {
	svc, store, _, _, token := phoneInviteFixture(t, domain.RoleMember)
	ctx := context.Background()

	result, err := svc.AcceptInviteWithSignup(ctx, token, "correct horse", "Sara", invitedCode)
	if err != nil {
		t.Fatal(err)
	}
	user, ok := store.userByMobile(invitedMobile)
	if !ok || user.ID != result.UserID || !user.MobileVerified {
		t.Fatalf("user = %+v, ok = %v; want the new account with a verified mobile", user, ok)
	}

	// The proof is spent: the same challenge cannot be consumed again.
	if err := svc.consumeOTPChallenge(ctx, "ch-invite-phone"); !errors.Is(err, ErrInvalidOTPCode) {
		t.Fatalf("second consume: err = %v, want ErrInvalidOTPCode", err)
	}
	// The invitation is accepted, so the same token is now refused.
	if _, err := svc.AcceptInviteWithSignup(ctx, token, "correct horse", "Sara", invitedCode); !errors.Is(err, domain.ErrInvalidInvite) {
		t.Fatalf("second accept: err = %v, want ErrInvalidInvite", err)
	}
}

func TestAcceptEmailInviteIgnoresPhoneCode(t *testing.T) {
	store := newSignupStore()
	store.add(domain.User{ID: "owner", Email: "owner@example.com", DisplayName: "Owner", Status: domain.UserActive, EmailVerified: true})
	svc := newSignupService(t, store, &fakeNotifier{}, true)
	ctx := context.Background()
	org, err := svc.CreateOrg(ctx, "owner", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := svc.InviteMember(ctx, org.ID, "owner", domain.InviteTarget{Email: "guest@example.com"}, domain.RoleMember, "")
	if err != nil {
		t.Fatal(err)
	}

	result, err := svc.AcceptInviteWithSignup(ctx, token, "correct horse", "Guest", "")
	if err != nil {
		t.Fatalf("email invite with no phone code: %v", err)
	}
	user, ok := store.users[result.UserID]
	if !ok || !user.EmailVerified || user.MobileVerified {
		t.Fatalf("user = %+v, ok = %v; want a verified email and no mobile", user, ok)
	}
}

func TestInviteRecipientMatchesRequiresVerifiedMobileForPhone(t *testing.T) {
	invite := domain.Invite{Phone: invitedMobile}
	user := domain.User{ID: "u1", Status: domain.UserActive, Mobile: invitedMobile}

	if inviteRecipientMatches(user, invite) {
		t.Fatal("an unverified mobile matched the phone invitation")
	}
	user.MobileVerified = true
	if !inviteRecipientMatches(user, invite) {
		t.Fatal("a verified mobile equal to the invited number did not match")
	}
	user.Mobile = "+989120000000"
	if inviteRecipientMatches(user, invite) {
		t.Fatal("a different verified number matched")
	}
}

func TestAcceptInviteRefusesUnverifiedMobile(t *testing.T) {
	svc, store, _, _, token := phoneInviteFixture(t, domain.RoleMember)
	store.add(domain.User{ID: "existing", Mobile: invitedMobile, Status: domain.UserActive})

	if _, err := svc.AcceptInvite(context.Background(), "existing", token); !errors.Is(err, domain.ErrInvalidInvite) {
		t.Fatalf("err = %v, want ErrInvalidInvite for an unverified mobile", err)
	}
}
