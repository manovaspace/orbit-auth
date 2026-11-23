package application

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

const attemptTestCode = "123456"

// countingHasher counts compares and slows each one down, so concurrent
// verifications overlap inside the compare.
type countingHasher struct {
	testHasher
	compares atomic.Int32
}

func (h *countingHasher) Compare(hash, secret string) bool {
	h.compares.Add(1)
	time.Sleep(5 * time.Millisecond)
	return h.testHasher.Compare(hash, secret)
}

// seedAttemptChallenge stores an email challenge for code. It uses the lowest
// bcrypt cost, which the test hasher accepts.
func seedAttemptChallenge(t *testing.T, otp *fakeOTP, code string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := otp.UpsertChallenge(context.Background(), domain.OTPChallenge{
		ID:         "ch-attempts",
		Identifier: "user@example.com",
		Channel:    domain.ChannelEmail,
		CodeHash:   string(hash),
		ExpiresAt:  time.Now().UTC().Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
}

func attemptTestService(t *testing.T, otp domain.OTPStore, hasher domain.Hasher) *Service {
	t.Helper()
	return NewService(Deps{
		OTP:      otp,
		Users:    &fakeUsers{},
		Refresh:  newFakeRefreshStore(),
		Tokens:   fakeApiTokens{},
		Notifier: &fakeNotifier{},
		Flags:    fakeFlags{email: true},
		Clock:    fixedClock{at: time.Now().UTC()},
		IDs:      fixedIDs{id: "11111111-1111-1111-1111-111111111111"},
		Signer:   testSigner{},
		Hasher:   hasher,
		Codes:    testCodes{},
		Secrets:  testSecrets{},
		Logger:   func(string, ...any) {},
	})
}

func TestVerifyOTPConcurrentGuessesShareOneAttemptBudget(t *testing.T) {
	otp := &fakeOTP{}
	seedAttemptChallenge(t, otp, attemptTestCode)
	hasher := &countingHasher{}
	svc := attemptTestService(t, otp, hasher)
	ctx := context.Background()

	const guesses = 50
	errs := make([]error, guesses)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range guesses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, _, errs[i] = svc.VerifyOTP(ctx, "user@example.com", domain.ChannelEmail, "000000")
		}()
	}
	close(start)
	wg.Wait()

	if got := hasher.compares.Load(); got != domain.MaxOTPAttempts {
		t.Fatalf("compares = %d, want %d", got, domain.MaxOTPAttempts)
	}
	var invalid, limited int
	for _, err := range errs {
		switch {
		case errors.Is(err, ErrInvalidOTPCode):
			invalid++
		case errors.Is(err, ErrTooManyOTPAttempts):
			limited++
		default:
			t.Fatalf("unexpected error %v", err)
		}
	}
	if invalid != domain.MaxOTPAttempts || limited != guesses-domain.MaxOTPAttempts {
		t.Fatalf("invalid = %d, limited = %d", invalid, limited)
	}

	// Once the budget is spent, the right code is refused without a compare.
	if _, _, _, err := svc.VerifyOTP(ctx, "user@example.com", domain.ChannelEmail, attemptTestCode); !errors.Is(err, ErrTooManyOTPAttempts) {
		t.Fatalf("correct code after exhaustion err = %v, want ErrTooManyOTPAttempts", err)
	}
	if got := hasher.compares.Load(); got != domain.MaxOTPAttempts {
		t.Fatalf("compares after exhaustion = %d, want %d", got, domain.MaxOTPAttempts)
	}
}

func TestVerifyOTPFourWrongThenRightSucceeds(t *testing.T) {
	otp := &fakeOTP{}
	seedAttemptChallenge(t, otp, attemptTestCode)
	svc := attemptTestService(t, otp, testHasher{})
	ctx := context.Background()

	for i := range domain.MaxOTPAttempts - 1 {
		if _, _, _, err := svc.VerifyOTP(ctx, "user@example.com", domain.ChannelEmail, "000000"); !errors.Is(err, ErrInvalidOTPCode) {
			t.Fatalf("wrong guess %d err = %v, want ErrInvalidOTPCode", i+1, err)
		}
	}
	access, refresh, _, err := svc.VerifyOTP(ctx, "user@example.com", domain.ChannelEmail, attemptTestCode)
	if err != nil {
		t.Fatalf("right code after four wrong guesses: %v", err)
	}
	if access == "" || refresh == "" {
		t.Fatalf("empty tokens: access=%q refresh=%q", access, refresh)
	}
}

func TestVerifyOTPFiveWrongGuessesExhaustChallenge(t *testing.T) {
	otp := &fakeOTP{}
	seedAttemptChallenge(t, otp, attemptTestCode)
	svc := attemptTestService(t, otp, testHasher{})
	ctx := context.Background()

	for i := range domain.MaxOTPAttempts {
		if _, _, _, err := svc.VerifyOTP(ctx, "user@example.com", domain.ChannelEmail, "000000"); !errors.Is(err, ErrInvalidOTPCode) {
			t.Fatalf("wrong guess %d err = %v, want ErrInvalidOTPCode", i+1, err)
		}
	}
	if _, _, _, err := svc.VerifyOTP(ctx, "user@example.com", domain.ChannelEmail, attemptTestCode); !errors.Is(err, ErrTooManyOTPAttempts) {
		t.Fatalf("right code after five wrong guesses err = %v, want ErrTooManyOTPAttempts", err)
	}
}

// signupAttemptService builds the sign-up service with the counting hasher. It
// matches newSignupServiceWithClock except for the hasher.
func signupAttemptService(store *signupStore, notifier *fakeNotifier, hasher domain.Hasher) *Service {
	return NewService(Deps{
		OTP:                 store.otp,
		Users:               store,
		Refresh:             newFakeRefreshStore(),
		Tokens:              fakeApiTokens{},
		Orgs:                store.orgs,
		Invites:             store.invites,
		Signup:              store,
		Notifier:            notifier,
		Flags:               fakeFlags{email: true},
		Clock:               fixedClock{at: signupNow()},
		IDs:                 testUUIDs{},
		Signer:              testSigner{},
		Hasher:              hasher,
		Codes:               testCodes{},
		Secrets:             testSecrets{},
		SignupOpen:          true,
		AutoSelectSingleOrg: true,
		Logger:              func(string, ...any) {},
	})
}

func TestVerifySignupConcurrentGuessesShareOneAttemptBudget(t *testing.T) {
	store := newSignupStore()
	notifier := &fakeNotifier{}
	hasher := &countingHasher{}
	svc := signupAttemptService(store, notifier, hasher)
	ctx := context.Background()
	if err := svc.RegisterWithEmail(ctx, "victim@example.com", "owner-pass", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	code := notifier.signupCode
	hasher.compares.Store(0)

	const guesses = 50
	errs := make([]error, guesses)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range guesses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, _, errs[i] = svc.VerifySignup(ctx, "victim@example.com", "000000", "owner-pass")
		}()
	}
	close(start)
	wg.Wait()

	if got := hasher.compares.Load(); got != domain.MaxOTPAttempts {
		t.Fatalf("compares = %d, want %d", got, domain.MaxOTPAttempts)
	}
	for _, err := range errs {
		if !errors.Is(err, ErrInvalidOTPCode) && !errors.Is(err, ErrTooManyOTPAttempts) {
			t.Fatalf("unexpected error %v", err)
		}
	}
	if _, _, _, err := svc.VerifySignup(ctx, "victim@example.com", code, "owner-pass"); !errors.Is(err, ErrTooManyOTPAttempts) {
		t.Fatalf("correct code after exhaustion err = %v, want ErrTooManyOTPAttempts", err)
	}
	if user := store.user(t, "victim@example.com"); user.Status != domain.UserSignupPending {
		t.Fatalf("status = %q, want signup_pending", user.Status)
	}
}
