package application

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// signupStore is both the UserStore and SignupStore used by the sign-up use
// case tests. It models the rows and the transactional side effects of the
// Postgres adapter.
type signupStore struct {
	mu        sync.Mutex
	users     map[string]domain.User
	emails    map[string]string
	createdAt map[string]time.Time
	orgs      *fakeOrgStore
	invites   *fakeInviteStore
	otp       *fakeOTP

	createCalls  int
	phoneCalls   int
	reissueCalls int
	verifyCalls  int
	purgeCalls   int
	createErr    error
	reissueErr   error
	verifyErr    error
	purgeErr     error
}

func newSignupStore() *signupStore {
	orgs := newFakeOrgStore()
	return &signupStore{
		users:     make(map[string]domain.User),
		emails:    make(map[string]string),
		createdAt: make(map[string]time.Time),
		orgs:      orgs,
		invites:   newFakeInviteStore(orgs),
		otp:       &fakeOTP{},
	}
}

func (s *signupStore) add(user domain.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[user.ID] = user
	s.emails[domain.NormalizeEmail(user.Email)] = user.ID
}

func (s *signupStore) FindOrCreateByEmail(_ context.Context, email string) (domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.emails[email]; ok {
		return s.users[id], nil
	}
	user := domain.User{ID: "u-" + email, Email: email, Status: domain.UserActive, EmailVerified: true}
	s.users[user.ID] = user
	s.emails[email] = user.ID
	return user, nil
}

func (s *signupStore) FindOrCreateByMobile(_ context.Context, mobile string) (domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.users {
		if user.Mobile == mobile {
			return user, nil
		}
	}
	user := domain.User{ID: "u-" + mobile, Mobile: mobile, Status: domain.UserActive}
	s.users[user.ID] = user
	return user, nil
}

func (s *signupStore) FindByEmail(_ context.Context, email string) (domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.emails[email]
	if !ok {
		return domain.User{}, domain.ErrUserNotFound
	}
	return s.users[id], nil
}

func (s *signupStore) FindByMobile(_ context.Context, mobile string) (domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.users {
		if user.Mobile == mobile {
			return user, nil
		}
	}
	return domain.User{}, domain.ErrUserNotFound
}

func (s *signupStore) FindByID(_ context.Context, userID string) (domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok {
		return domain.User{}, domain.ErrUserNotFound
	}
	return user, nil
}

func (s *signupStore) UpdateLastLogin(context.Context, string) error { return nil }

func (s *signupStore) MarkMobileVerified(_ context.Context, userID string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok {
		return domain.ErrUserNotFound
	}
	user.MobileVerified = true
	s.users[userID] = user
	return nil
}

func (s *signupStore) UpsertDemoUser(context.Context, string, string, string, string) error {
	return nil
}

func (s *signupStore) CreateSignupUser(ctx context.Context, user domain.User, org *domain.Organization, invite *domain.Invite, acceptedAt time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createCalls++
	if s.createErr != nil {
		return false, s.createErr
	}
	if _, ok := s.emails[user.Email]; ok {
		return false, nil
	}
	s.users[user.ID] = user
	s.emails[user.Email] = user.ID
	s.createdAt[user.ID] = acceptedAt

	if invite != nil {
		if err := s.invites.AcceptInvite(ctx, invite.ID, invite.OrgID, user.ID, invite.Role, acceptedAt); err != nil {
			delete(s.users, user.ID)
			delete(s.emails, user.Email)
			delete(s.createdAt, user.ID)
			return false, err
		}
	}
	if org != nil {
		if err := s.orgs.CreateOrg(ctx, *org, user.ID); err != nil {
			delete(s.users, user.ID)
			delete(s.emails, user.Email)
			delete(s.createdAt, user.ID)
			return false, err
		}
	}
	return true, nil
}

func (s *signupStore) ReissuePendingSignup(ctx context.Context, user domain.User, challenge *domain.OTPChallenge, invite *domain.Invite, acceptedAt time.Time) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reissueCalls++
	if s.reissueErr != nil {
		return "", false, s.reissueErr
	}
	id, ok := s.emails[user.Email]
	if !ok {
		return "", false, nil
	}
	existing := s.users[id]
	if existing.Status != domain.UserSignupPending || existing.EmailVerified {
		return "", false, nil
	}
	existing.PasswordHash = user.PasswordHash
	existing.DisplayName = user.DisplayName
	s.users[id] = existing

	if challenge != nil {
		s.otp.deleteChallenges(challenge.Identifier, challenge.Channel)
		if err := s.otp.UpsertChallenge(ctx, *challenge); err != nil {
			return "", false, err
		}
	}
	if invite != nil {
		if err := s.invites.AcceptInvite(ctx, invite.ID, invite.OrgID, id, invite.Role, acceptedAt); err != nil {
			return "", false, err
		}
		existing.Status = domain.UserActive
		existing.EmailVerified = true
		s.users[id] = existing
	}
	return id, true, nil
}

// CreatePhoneInviteSignup models the store transaction: the user row, the
// accepted invitation and the membership are written together or not at all.
func (s *signupStore) CreatePhoneInviteSignup(ctx context.Context, user domain.User, invite domain.Invite, acceptedAt time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phoneCalls++
	for _, existing := range s.users {
		if existing.Mobile != "" && existing.Mobile == user.Mobile {
			return false, nil
		}
	}
	s.users[user.ID] = user
	if err := s.invites.AcceptInvite(ctx, invite.ID, invite.OrgID, user.ID, invite.Role, acceptedAt); err != nil {
		delete(s.users, user.ID)
		return false, err
	}
	return true, nil
}

func (s *signupStore) VerifySignup(_ context.Context, userID string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verifyCalls++
	if s.verifyErr != nil {
		return s.verifyErr
	}
	user, ok := s.users[userID]
	if !ok {
		return domain.ErrUserNotFound
	}
	user.Status = domain.UserActive
	user.EmailVerified = true
	s.users[userID] = user
	return nil
}

func (s *signupStore) PurgeUnverifiedSignups(_ context.Context, before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeCalls++
	if s.purgeErr != nil {
		return 0, s.purgeErr
	}
	var removed int64
	for id, user := range s.users {
		if user.Status != domain.UserSignupPending || user.EmailVerified {
			continue
		}
		if s.createdAt[id].Before(before) {
			delete(s.users, id)
			delete(s.emails, user.Email)
			delete(s.createdAt, id)
			removed++
		}
	}
	return removed, nil
}

func (s *signupStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.users)
}

func (s *signupStore) user(t *testing.T, email string) domain.User {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.emails[email]
	if !ok {
		t.Fatalf("user %q not found", email)
	}
	return s.users[id]
}

func signupNow() time.Time {
	return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
}

func newSignupService(t *testing.T, store *signupStore, notifier *fakeNotifier, open bool) *Service {
	t.Helper()
	return newSignupServiceWithClock(t, store, notifier, open, fixedClock{at: signupNow()})
}

func newSignupServiceWithClock(t *testing.T, store *signupStore, notifier *fakeNotifier, open bool, clock domain.Clock) *Service {
	t.Helper()
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
		Clock:               clock,
		IDs:                 testUUIDs{},
		Signer:              testSigner{},
		Hasher:              testHasher{},
		Codes:               testCodes{},
		Secrets:             testSecrets{},
		SignupOpen:          open,
		AutoSelectSingleOrg: true,
		Logger:              func(string, ...any) {},
	})
}

// mutableClock lets the sign-up tests move past the resend cooldown.
type mutableClock struct{ at time.Time }

func (c *mutableClock) Now() time.Time { return c.at }
func (c *mutableClock) Advance(d time.Duration) {
	c.at = c.at.Add(d)
}

func TestRegisterWithEmailDisabledWritesNothing(t *testing.T) {
	store := newSignupStore()
	notifier := &fakeNotifier{}
	svc := newSignupService(t, store, notifier, false)

	err := svc.RegisterWithEmail(context.Background(), "new@example.com", "s3cret-pass", "New", "en", "Acme", "")
	if !errors.Is(err, ErrSignupDisabled) {
		t.Fatalf("err = %v, want ErrSignupDisabled", err)
	}
	if store.createCalls != 0 || store.count() != 0 {
		t.Fatal("a disabled sign-up reached the store")
	}
	if notifier.signupCalls != 0 {
		t.Fatal("a disabled sign-up sent a notification")
	}
	if _, _, _, err := svc.VerifySignup(context.Background(), "new@example.com", "123456", "s3cret-pass"); !errors.Is(err, ErrSignupDisabled) {
		t.Fatalf("verify err = %v, want ErrSignupDisabled", err)
	}
}

func TestRegisterWithEmailCreatesPendingUserAndSendsOneOTP(t *testing.T) {
	store := newSignupStore()
	notifier := &fakeNotifier{}
	svc := newSignupService(t, store, notifier, true)
	ctx := context.Background()

	if err := svc.RegisterWithEmail(ctx, "  New@Example.com ", "s3cret-pass", "  New User  ", "", "", ""); err != nil {
		t.Fatal(err)
	}
	user := store.user(t, "new@example.com")
	if user.Status != domain.UserSignupPending || user.EmailVerified {
		t.Fatalf("user = %+v, want signup_pending and unverified", user)
	}
	if user.DisplayName != "New User" {
		t.Fatalf("display name = %q", user.DisplayName)
	}
	if !(testHasher{}).Compare(user.PasswordHash, "s3cret-pass") {
		t.Fatal("stored password hash does not verify the sign-up password")
	}
	if store.createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", store.createCalls)
	}
	if notifier.signupCalls != 1 {
		t.Fatalf("signup notifications = %d, want 1", notifier.signupCalls)
	}
	if notifier.signupRecipient != "new@example.com" || notifier.signupExpires != 10 || notifier.signupLocale != "en" {
		t.Fatalf("notification = %+v", notifier)
	}
	if notifier.signupCode == "" || notifier.signupCorrelation != user.ID {
		t.Fatalf("notification code/correlation = %q/%q", notifier.signupCode, notifier.signupCorrelation)
	}
}

func TestRegisterWithEmailDuplicateIsIndistinguishable(t *testing.T) {
	store := newSignupStore()
	store.add(domain.User{ID: "existing", Email: "taken@example.com", Status: domain.UserActive, EmailVerified: true})
	notifier := &fakeNotifier{}
	svc := newSignupService(t, store, notifier, true)

	if err := svc.RegisterWithEmail(context.Background(), "taken@example.com", "s3cret-pass", "", "", "", ""); err != nil {
		t.Fatalf("duplicate registration returned %v, want success", err)
	}
	if store.createCalls != 1 {
		t.Fatalf("create calls = %d, want the single duplicate attempt", store.createCalls)
	}
	if store.count() != 1 {
		t.Fatalf("users = %d, want the existing one only", store.count())
	}
	if notifier.signupCalls != 0 {
		t.Fatal("a duplicate registration sent a notification")
	}
}

func TestSignupCannotLoginBeforeVerification(t *testing.T) {
	t.Run("password", func(t *testing.T) {
		store := newSignupStore()
		notifier := &fakeNotifier{}
		svc := newSignupService(t, store, notifier, true)
		ctx := context.Background()
		if err := svc.RegisterWithEmail(ctx, "new@example.com", "s3cret-pass", "", "", "", ""); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := svc.LoginWithPassword(ctx, "new@example.com", "s3cret-pass"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("err = %v, want ErrInvalidCredentials", err)
		}
	})

	t.Run("otp", func(t *testing.T) {
		store := newSignupStore()
		notifier := &fakeNotifier{}
		svc := newSignupService(t, store, notifier, true)
		ctx := context.Background()
		if err := svc.RegisterWithEmail(ctx, "new@example.com", "s3cret-pass", "", "", "", ""); err != nil {
			t.Fatal(err)
		}
		access, refresh, _, err := svc.VerifyOTP(ctx, "new@example.com", domain.ChannelEmail, notifier.signupCode)
		if !errors.Is(err, ErrInvalidOTPCode) {
			t.Fatalf("err = %v, want ErrInvalidOTPCode", err)
		}
		if access != "" || refresh != "" {
			t.Fatal("a pending sign-up received tokens")
		}
	})
}

func TestRegisterWithEmailSquattedAddressCanBeRecovered(t *testing.T) {
	store := newSignupStore()
	notifier := &fakeNotifier{}
	clock := &mutableClock{at: signupNow()}
	svc := newSignupServiceWithClock(t, store, notifier, true, clock)
	ctx := context.Background()

	// The attacker registers the victim's address first.
	if err := svc.RegisterWithEmail(ctx, "victim@example.com", "squatter-pass", "Squatter", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if notifier.signupCalls != 1 {
		t.Fatalf("squatter notifications = %d, want 1", notifier.signupCalls)
	}

	// The mailbox owner re-registers after the cooldown and takes the account
	// back with their own password.
	clock.Advance(2 * time.Minute)
	if err := svc.RegisterWithEmail(ctx, "victim@example.com", "owner-pass", "Owner", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if notifier.signupCalls != 2 {
		t.Fatalf("owner notifications = %d, want 2", notifier.signupCalls)
	}
	user := store.user(t, "victim@example.com")
	if user.Status != domain.UserSignupPending || user.DisplayName != "Owner" {
		t.Fatalf("user = %+v, want pending with the owner's display name", user)
	}
	if !(testHasher{}).Compare(user.PasswordHash, "owner-pass") {
		t.Fatal("owner re-registration did not replace the password hash")
	}
	if (testHasher{}).Compare(user.PasswordHash, "squatter-pass") {
		t.Fatal("squatter password still verifies")
	}

	// Only the owner has the new code; verification activates the account with
	// the owner's password.
	if _, _, _, err := svc.VerifySignup(ctx, "victim@example.com", notifier.signupCode, "owner-pass"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.LoginWithPassword(ctx, "victim@example.com", "owner-pass"); err != nil {
		t.Fatalf("owner login: %v", err)
	}
	if _, _, _, err := svc.LoginWithPassword(ctx, "victim@example.com", "squatter-pass"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("squatter login err = %v, want ErrInvalidCredentials", err)
	}
}

func TestRegisterWithEmailCooldownKeepsExistingCode(t *testing.T) {
	store := newSignupStore()
	notifier := &fakeNotifier{}
	clock := &mutableClock{at: signupNow()}
	svc := newSignupServiceWithClock(t, store, notifier, true, clock)
	ctx := context.Background()

	if err := svc.RegisterWithEmail(ctx, "victim@example.com", "squatter-pass", "Squatter", "", "", ""); err != nil {
		t.Fatal(err)
	}
	firstCode := notifier.signupCode
	if firstCode == "" {
		t.Fatal("no verification code was sent")
	}

	// A retry inside 60 seconds updates the credentials but must not invalidate
	// or resend the code already in the owner's mailbox.
	clock.Advance(30 * time.Second)
	if err := svc.RegisterWithEmail(ctx, "victim@example.com", "owner-pass", "Owner", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if notifier.signupCalls != 1 {
		t.Fatalf("cooldown sent %d notifications, want 1", notifier.signupCalls)
	}
	user := store.user(t, "victim@example.com")
	if !(testHasher{}).Compare(user.PasswordHash, "owner-pass") || user.DisplayName != "Owner" {
		t.Fatalf("cooldown did not update credentials: %+v", user)
	}

	if _, _, _, err := svc.VerifySignup(ctx, "victim@example.com", firstCode, "owner-pass"); err != nil {
		t.Fatalf("the previously sent code stopped working: %v", err)
	}
	if _, _, _, err := svc.LoginWithPassword(ctx, "victim@example.com", "owner-pass"); err != nil {
		t.Fatalf("owner login: %v", err)
	}
}

func TestRegisterWithEmailVerifiedAccountUntouched(t *testing.T) {
	store := newSignupStore()
	notifier := &fakeNotifier{}
	svc := newSignupService(t, store, notifier, true)
	ctx := context.Background()
	if err := svc.RegisterWithEmail(ctx, "victim@example.com", "owner-pass", "Owner", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.VerifySignup(ctx, "victim@example.com", notifier.signupCode, "owner-pass"); err != nil {
		t.Fatal(err)
	}
	before := store.user(t, "victim@example.com")
	notifications := notifier.signupCalls

	if err := svc.RegisterWithEmail(ctx, "victim@example.com", "attacker-pass", "Attacker", "", "", ""); err != nil {
		t.Fatal(err)
	}
	after := store.user(t, "victim@example.com")
	if after.PasswordHash != before.PasswordHash || after.DisplayName != before.DisplayName ||
		after.Status != domain.UserActive || !after.EmailVerified {
		t.Fatalf("verified account changed: before=%+v after=%+v", before, after)
	}
	if notifier.signupCalls != notifications {
		t.Fatalf("verified account received %d extra notifications", notifier.signupCalls-notifications)
	}
	if (testHasher{}).Compare(after.PasswordHash, "attacker-pass") {
		t.Fatal("attacker password replaced a verified account password")
	}
}

func TestVerifySignupActivatesAndIssuesTokens(t *testing.T) {
	store := newSignupStore()
	notifier := &fakeNotifier{}
	svc := newSignupService(t, store, notifier, true)
	ctx := context.Background()
	if err := svc.RegisterWithEmail(ctx, "new@example.com", "s3cret-pass", "", "fa", "", ""); err != nil {
		t.Fatal(err)
	}

	access, refresh, expires, err := svc.VerifySignup(ctx, "NEW@example.com", notifier.signupCode, "s3cret-pass")
	if err != nil {
		t.Fatal(err)
	}
	if access == "" || refresh == "" || expires.Before(signupNow()) {
		t.Fatalf("tokens = %q/%q/%v", access, refresh, expires)
	}
	user := store.user(t, "new@example.com")
	if user.Status != domain.UserActive || !user.EmailVerified {
		t.Fatalf("user = %+v, want active and verified", user)
	}
	if store.verifyCalls != 1 {
		t.Fatalf("verify calls = %d, want 1", store.verifyCalls)
	}
	if _, _, _, err := svc.LoginWithPassword(ctx, "new@example.com", "s3cret-pass"); err != nil {
		t.Fatalf("login after verification: %v", err)
	}
}

func TestVerifySignupWrongPasswordDoesNotConsumeChallenge(t *testing.T) {
	store := newSignupStore()
	notifier := &fakeNotifier{}
	svc := newSignupService(t, store, notifier, true)
	ctx := context.Background()
	if err := svc.RegisterWithEmail(ctx, "victim@example.com", "owner-pass", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	code := notifier.signupCode

	if _, _, _, err := svc.VerifySignup(ctx, "victim@example.com", code, "wrong-pass"); !errors.Is(err, ErrInvalidOTPCode) {
		t.Fatalf("wrong password err = %v, want ErrInvalidOTPCode", err)
	}
	if store.otp.consumed {
		t.Fatal("a wrong password consumed the challenge")
	}
	if store.otp.attempts != 1 {
		t.Fatalf("attempts = %d, want 1", store.otp.attempts)
	}
	if store.verifyCalls != 0 {
		t.Fatal("a wrong password activated the account")
	}
	if user := store.user(t, "victim@example.com"); user.Status != domain.UserSignupPending {
		t.Fatalf("status = %q, want signup_pending", user.Status)
	}

	// The same challenge still works with the right password.
	if _, _, _, err := svc.VerifySignup(ctx, "victim@example.com", code, "owner-pass"); err != nil {
		t.Fatalf("correct password after a wrong one: %v", err)
	}
}

func TestVerifySignupPreHijackRequiresCodeAndPassword(t *testing.T) {
	store := newSignupStore()
	notifier := &fakeNotifier{}
	clock := &mutableClock{at: signupNow()}
	svc := newSignupServiceWithClock(t, store, notifier, true, clock)
	ctx := context.Background()

	// The attacker registers the victim's address first.
	if err := svc.RegisterWithEmail(ctx, "victim@example.com", "attacker-pass", "Attacker", "", "", ""); err != nil {
		t.Fatal(err)
	}
	// The owner re-registers with their own password, then the attacker
	// overwrites the pending credentials again. The owner still has the latest
	// mailbox code, but the pending password is the attacker's.
	clock.Advance(2 * time.Minute)
	if err := svc.RegisterWithEmail(ctx, "victim@example.com", "owner-pass", "Owner", "", "", ""); err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Minute)
	if err := svc.RegisterWithEmail(ctx, "victim@example.com", "attacker-pass", "Attacker", "", "", ""); err != nil {
		t.Fatal(err)
	}
	code := notifier.signupCode

	// The owner's password no longer matches the pending account.
	if _, _, _, err := svc.VerifySignup(ctx, "victim@example.com", code, "owner-pass"); !errors.Is(err, ErrInvalidOTPCode) {
		t.Fatalf("owner verify err = %v, want ErrInvalidOTPCode", err)
	}
	if store.otp.consumed {
		t.Fatal("a mismatched password consumed the challenge")
	}
	if store.otp.attempts != 1 {
		t.Fatalf("attempts = %d, want 1", store.otp.attempts)
	}

	// The attacker knows their own password but not the code.
	if _, _, _, err := svc.VerifySignup(ctx, "victim@example.com", "000000", "attacker-pass"); !errors.Is(err, ErrInvalidOTPCode) {
		t.Fatalf("attacker verify err = %v, want ErrInvalidOTPCode", err)
	}
	if user := store.user(t, "victim@example.com"); user.Status != domain.UserSignupPending {
		t.Fatalf("status = %q, want signup_pending", user.Status)
	}

	// The owner re-registers with their own password and verifies the new code.
	clock.Advance(2 * time.Minute)
	if err := svc.RegisterWithEmail(ctx, "victim@example.com", "owner-pass", "Owner", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.VerifySignup(ctx, "victim@example.com", notifier.signupCode, "owner-pass"); err != nil {
		t.Fatalf("owner recovery: %v", err)
	}
	if _, _, _, err := svc.LoginWithPassword(ctx, "victim@example.com", "owner-pass"); err != nil {
		t.Fatalf("owner login: %v", err)
	}
	if _, _, _, err := svc.LoginWithPassword(ctx, "victim@example.com", "attacker-pass"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("attacker login err = %v, want ErrInvalidCredentials", err)
	}
}

func TestVerifySignupWrongCode(t *testing.T) {
	store := newSignupStore()
	notifier := &fakeNotifier{}
	svc := newSignupService(t, store, notifier, true)
	ctx := context.Background()
	if err := svc.RegisterWithEmail(ctx, "new@example.com", "s3cret-pass", "", "", "", ""); err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := svc.VerifySignup(ctx, "new@example.com", "000000", "s3cret-pass"); !errors.Is(err, ErrInvalidOTPCode) {
		t.Fatalf("err = %v, want ErrInvalidOTPCode", err)
	}
	if store.verifyCalls != 0 {
		t.Fatal("a wrong code activated the account")
	}
	if user := store.user(t, "new@example.com"); user.Status != domain.UserSignupPending {
		t.Fatalf("status = %q, want signup_pending", user.Status)
	}
}

func TestRegisterWithEmailInviteCreatesVerifiedMember(t *testing.T) {
	store := newSignupStore()
	now := signupNow()
	org := domain.Organization{ID: "org-1", Name: "Acme", CreatedAt: now, UpdatedAt: now}
	store.add(domain.User{ID: "inviter", Email: "owner@example.com", Status: domain.UserActive, EmailVerified: true})
	if err := store.orgs.CreateOrg(context.Background(), org, "inviter"); err != nil {
		t.Fatal(err)
	}
	invite := domain.Invite{
		ID:        "invite-1",
		OrgID:     org.ID,
		Email:     "new@example.com",
		Role:      domain.RoleMember,
		TokenHash: domain.HashInviteToken("invite-token"),
		InviterID: "inviter",
		Status:    domain.InvitePending,
		ExpiresAt: now.Add(time.Hour),
		CreatedAt: now,
	}
	if err := store.invites.CreateInvite(context.Background(), invite); err != nil {
		t.Fatal(err)
	}

	notifier := &fakeNotifier{}
	svc := newSignupService(t, store, notifier, true)
	if err := svc.RegisterWithEmail(context.Background(), "new@example.com", "s3cret-pass", "", "", "", "invite-token"); err != nil {
		t.Fatal(err)
	}
	user := store.user(t, "new@example.com")
	if user.Status != domain.UserActive || !user.EmailVerified {
		t.Fatalf("user = %+v, want active and verified", user)
	}
	if notifier.signupCalls != 0 {
		t.Fatal("an invited sign-up sent an OTP")
	}
	stored := store.invites.get(t, invite.ID)
	if stored.Status != domain.InviteAccepted {
		t.Fatalf("invite status = %q, want accepted", stored.Status)
	}
	membership, err := store.orgs.GetMembership(context.Background(), org.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipActive {
		t.Fatalf("membership = %q, want active", membership.Status)
	}
}

func TestRegisterWithEmailInvalidInviteCreatesNothing(t *testing.T) {
	store := newSignupStore()
	notifier := &fakeNotifier{}
	svc := newSignupService(t, store, notifier, true)

	err := svc.RegisterWithEmail(context.Background(), "new@example.com", "s3cret-pass", "", "", "", "bad-token")
	if !errors.Is(err, domain.ErrInvalidInvite) {
		t.Fatalf("err = %v, want ErrInvalidInvite", err)
	}
	if store.createCalls != 0 || store.count() != 0 || notifier.signupCalls != 0 {
		t.Fatal("an invalid invite wrote or sent something")
	}
}

func TestRegisterWithEmailInviteEmailMustMatch(t *testing.T) {
	store := newSignupStore()
	now := signupNow()
	org := domain.Organization{ID: "org-1", Name: "Acme", CreatedAt: now, UpdatedAt: now}
	store.add(domain.User{ID: "inviter", Email: "owner@example.com", Status: domain.UserActive, EmailVerified: true})
	if err := store.orgs.CreateOrg(context.Background(), org, "inviter"); err != nil {
		t.Fatal(err)
	}
	invite := domain.Invite{
		ID:        "invite-1",
		OrgID:     org.ID,
		Email:     "other@example.com",
		Role:      domain.RoleMember,
		TokenHash: domain.HashInviteToken("invite-token"),
		InviterID: "inviter",
		Status:    domain.InvitePending,
		ExpiresAt: now.Add(time.Hour),
		CreatedAt: now,
	}
	if err := store.invites.CreateInvite(context.Background(), invite); err != nil {
		t.Fatal(err)
	}

	svc := newSignupService(t, store, &fakeNotifier{}, true)
	err := svc.RegisterWithEmail(context.Background(), "new@example.com", "s3cret-pass", "", "", "", "invite-token")
	if !errors.Is(err, domain.ErrInvalidInvite) {
		t.Fatalf("err = %v, want ErrInvalidInvite", err)
	}
	if store.createCalls != 0 || store.count() != 1 {
		t.Fatal("a mismatched invitation created an account")
	}
}

func TestRegisterWithEmailCreatesOrganizationAndOwner(t *testing.T) {
	store := newSignupStore()
	notifier := &fakeNotifier{}
	svc := newSignupService(t, store, notifier, true)

	if err := svc.RegisterWithEmail(context.Background(), "new@example.com", "s3cret-pass", "", "", "  Acme  ", ""); err != nil {
		t.Fatal(err)
	}
	user := store.user(t, "new@example.com")
	if user.Status != domain.UserSignupPending {
		t.Fatalf("status = %q, want signup_pending", user.Status)
	}
	orgs, err := store.orgs.ListOrgsForUser(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(orgs) != 1 || orgs[0].Name != "Acme" {
		t.Fatalf("orgs = %+v, want Acme", orgs)
	}
	membership, err := store.orgs.GetMembership(context.Background(), orgs[0].ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipActive {
		t.Fatalf("membership = %q, want active", membership.Status)
	}
}

func TestPurgeUnverifiedSignups(t *testing.T) {
	store := newSignupStore()
	notifier := &fakeNotifier{}
	svc := newSignupService(t, store, notifier, true)
	ctx := context.Background()
	if err := svc.RegisterWithEmail(ctx, "old@example.com", "s3cret-pass", "", "", "", ""); err != nil {
		t.Fatal(err)
	}

	removed, err := svc.PurgeUnverifiedSignups(ctx, signupNow().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 || store.count() != 1 {
		t.Fatalf("removed = %d, users = %d, want nothing purged yet", removed, store.count())
	}

	removed, err = svc.PurgeUnverifiedSignups(ctx, signupNow().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || store.count() != 0 {
		t.Fatalf("removed = %d, users = %d, want one purged", removed, store.count())
	}
}

func TestPurgeUnverifiedSignupsKeepsVerifiedAccounts(t *testing.T) {
	store := newSignupStore()
	store.add(domain.User{ID: "active", Email: "active@example.com", Status: domain.UserActive, EmailVerified: true})
	svc := newSignupService(t, store, &fakeNotifier{}, true)

	removed, err := svc.PurgeUnverifiedSignups(context.Background(), signupNow().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 || store.count() != 1 {
		t.Fatalf("removed = %d, users = %d, want the verified account kept", removed, store.count())
	}
}
