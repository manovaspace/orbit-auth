package application

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

type fakeOTP struct {
	mu       sync.RWMutex
	ch       domain.OTPChallenge
	consumed bool
	attempts int
}

func (f *fakeOTP) UpsertChallenge(_ context.Context, ch domain.OTPChallenge) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ch = ch
	f.consumed = false
	f.attempts = 0
	return nil
}

func (f *fakeOTP) GetLatestChallenge(_ context.Context, identifier, channel string) (domain.OTPChallenge, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.consumed {
		return domain.OTPChallenge{}, fmt.Errorf("challenge already consumed")
	}
	if f.ch.Identifier == identifier && f.ch.Channel == channel {
		return f.ch, nil
	}
	return domain.OTPChallenge{}, fmt.Errorf("challenge not found")
}

func (f *fakeOTP) IncrementAttempts(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ch.ID == id {
		f.attempts++
		f.ch.Attempts = f.attempts
	}
	return nil
}

func (f *fakeOTP) ConsumeChallenge(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ch.ID == id {
		f.consumed = true
	}
	return nil
}

func (f *fakeOTP) DeleteExpiredChallenges(context.Context) error { return nil }

type fakeUsers struct {
	mu   sync.RWMutex
	user domain.User
}

func (f *fakeUsers) FindOrCreateByEmail(_ context.Context, email string) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.user.Email == "" {
		f.user = domain.User{ID: "u1", Email: email}
	}
	return f.user, nil
}

func (f *fakeUsers) FindOrCreateByMobile(_ context.Context, mobile string) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.user.Mobile == "" {
		f.user = domain.User{ID: "u1", Mobile: mobile}
	}
	return f.user, nil
}

func (f *fakeUsers) FindByEmail(_ context.Context, email string) (domain.User, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.user, nil
}

func (f *fakeUsers) UpdateLastLogin(context.Context, string) error { return nil }

func (f *fakeUsers) UpsertDemoUser(context.Context, string, string, string) error { return nil }

type fakeApiTokens struct{}

func (fakeApiTokens) Create(context.Context, domain.ApiToken) error { return nil }
func (fakeApiTokens) ListByUser(context.Context, string) ([]domain.ApiTokenInfo, error) {
	return nil, nil
}
func (fakeApiTokens) RevokeToken(context.Context, string, string) error { return nil }
func (fakeApiTokens) FindByPrefix(context.Context, string) (domain.ApiToken, error) {
	return domain.ApiToken{}, context.Canceled
}

type fakeRefreshStore struct {
	mu     sync.RWMutex
	tokens map[string]string // tokenHash -> userID
}

func newFakeRefreshStore() *fakeRefreshStore {
	return &fakeRefreshStore{
		tokens: make(map[string]string),
	}
}

func (f *fakeRefreshStore) Store(_ context.Context, userID, tokenHash string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tokens == nil {
		f.tokens = make(map[string]string)
	}
	f.tokens[tokenHash] = userID
	return nil
}

func (f *fakeRefreshStore) FindValid(_ context.Context, tokenHash string) (string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.tokens == nil {
		return "", fmt.Errorf("refresh token not found")
	}
	userID, ok := f.tokens[tokenHash]
	if !ok {
		return "", fmt.Errorf("refresh token not found")
	}
	return userID, nil
}

func (f *fakeRefreshStore) Revoke(_ context.Context, tokenHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tokens != nil {
		delete(f.tokens, tokenHash)
	}
	return nil
}

type fakeNotifier struct {
	mu            sync.RWMutex
	lastChannel   string
	lastRecipient string
}

func (f *fakeNotifier) SendOTP(_ context.Context, channel, recipient, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastChannel = channel
	f.lastRecipient = recipient
	return nil
}

type fakeFlags struct{ mobile, email bool }

func (f fakeFlags) Bool(_ context.Context, flag string, _ bool) bool {
	switch flag {
	case "manova.auth.mobile_otp":
		return f.mobile
	case "manova.auth.email_otp":
		return f.email
	default:
		return false
	}
}

func mustService(t *testing.T, otp domain.OTPRepository, users domain.UserRepository, n domain.Notifier, flags FlagEvaluator) *Service {
	return mustServiceWithRefresh(t, otp, users, newFakeRefreshStore(), n, flags)
}

func mustServiceWithRefresh(t *testing.T, otp domain.OTPRepository, users domain.UserRepository, refresh domain.RefreshTokenRepository, n domain.Notifier, flags FlagEvaluator) *Service {
	t.Helper()
	t.Setenv("DEPLOYMENT_ENVIRONMENT", "dev")
	t.Setenv("JWT_SECRET", "test-secret")
	svc, err := NewService(otp, users, refresh, fakeApiTokens{}, n, flags, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestRequestOTP_MobileRequiresE164(t *testing.T) {
	svc := mustService(t, &fakeOTP{}, &fakeUsers{}, &fakeNotifier{}, fakeFlags{mobile: true})
	_, err := svc.RequestOTP(context.Background(), "09121234567", domain.ChannelSMS, "c1")
	if err == nil {
		t.Fatal("expected E.164 error")
	}
}

func TestRequestOTP_MobileHappyPath(t *testing.T) {
	otp := &fakeOTP{}
	notifier := &fakeNotifier{}
	svc := mustService(t, otp, &fakeUsers{}, notifier, fakeFlags{mobile: true})
	_, err := svc.RequestOTP(context.Background(), "+989121234567", domain.ChannelSMS, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if notifier.lastChannel != domain.ChannelSMS {
		t.Fatalf("channel %q", notifier.lastChannel)
	}
	if otp.ch.Channel != domain.ChannelSMS {
		t.Fatalf("stored channel %q", otp.ch.Channel)
	}
}

func TestVerifyOTP_ValidCodeAndSingleUse(t *testing.T) {
	t.Run("email channel", func(t *testing.T) {
		code := "123456"
		hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		otp := &fakeOTP{}
		_ = otp.UpsertChallenge(context.Background(), domain.OTPChallenge{
			ID:         "ch-email-1",
			Identifier: "test@example.com",
			Channel:    domain.ChannelEmail,
			CodeHash:   string(hash),
			ExpiresAt:  time.Now().UTC().Add(10 * time.Minute),
		})
		refreshStore := newFakeRefreshStore()
		users := &fakeUsers{}
		svc := mustServiceWithRefresh(t, otp, users, refreshStore, &fakeNotifier{}, fakeFlags{email: true})

		access, refresh, expires, err := svc.VerifyOTP(context.Background(), "test@example.com", domain.ChannelEmail, code)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if access == "" {
			t.Fatal("expected non-empty access token")
		}
		if refresh == "" {
			t.Fatal("expected non-empty refresh token")
		}
		if expires.Before(time.Now().UTC()) {
			t.Fatalf("expected future expiration, got %v", expires)
		}
		if !otp.consumed {
			t.Fatal("expected challenge to be marked consumed")
		}
		uid, err := refreshStore.FindValid(context.Background(), hashToken(refresh))
		if err != nil || uid != "u1" {
			t.Fatalf("expected stored refresh token for user u1, got uid=%q err=%v", uid, err)
		}

		// Single-use verification: second attempt with the same OTP must fail
		_, _, _, err = svc.VerifyOTP(context.Background(), "test@example.com", domain.ChannelEmail, code)
		if err == nil {
			t.Fatal("expected error on reusing consumed OTP challenge")
		}
	})

	t.Run("mobile channel", func(t *testing.T) {
		code := "654321"
		hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		otp := &fakeOTP{}
		_ = otp.UpsertChallenge(context.Background(), domain.OTPChallenge{
			ID:         "ch-mobile-1",
			Identifier: "+989121234567",
			Channel:    domain.ChannelSMS,
			CodeHash:   string(hash),
			ExpiresAt:  time.Now().UTC().Add(10 * time.Minute),
		})
		refreshStore := newFakeRefreshStore()
		users := &fakeUsers{}
		svc := mustServiceWithRefresh(t, otp, users, refreshStore, &fakeNotifier{}, fakeFlags{mobile: true})

		access, refresh, expires, err := svc.VerifyOTP(context.Background(), "+989121234567", domain.ChannelSMS, code)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if access == "" || refresh == "" || expires.IsZero() {
			t.Fatal("expected valid tokens and expiry")
		}
		if !otp.consumed {
			t.Fatal("expected challenge to be marked consumed")
		}

		// Single-use verification: second attempt with the same OTP must fail
		_, _, _, err = svc.VerifyOTP(context.Background(), "+989121234567", domain.ChannelSMS, code)
		if err == nil {
			t.Fatal("expected error on reusing consumed OTP challenge")
		}
	})
}

func TestVerifyOTP_InvalidCode(t *testing.T) {
	code := "123456"
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	otp := &fakeOTP{}
	_ = otp.UpsertChallenge(context.Background(), domain.OTPChallenge{
		ID:         "ch-invalid-1",
		Identifier: "user@example.com",
		Channel:    domain.ChannelEmail,
		CodeHash:   string(hash),
		ExpiresAt:  time.Now().UTC().Add(10 * time.Minute),
	})
	refreshStore := newFakeRefreshStore()
	svc := mustServiceWithRefresh(t, otp, &fakeUsers{}, refreshStore, &fakeNotifier{}, fakeFlags{email: true})

	access, refresh, _, err := svc.VerifyOTP(context.Background(), "user@example.com", domain.ChannelEmail, "999999")
	if err == nil {
		t.Fatal("expected error for invalid OTP code")
	}
	if access != "" || refresh != "" {
		t.Fatalf("expected empty tokens, got access=%q refresh=%q", access, refresh)
	}
	if otp.consumed {
		t.Fatal("expected challenge NOT to be consumed on invalid code")
	}
	if otp.attempts != 1 {
		t.Fatalf("expected attempts to be incremented to 1, got %d", otp.attempts)
	}

	t.Run("too many attempts", func(t *testing.T) {
		otpTooMany := &fakeOTP{}
		_ = otpTooMany.UpsertChallenge(context.Background(), domain.OTPChallenge{
			ID:         "ch-too-many-attempts",
			Identifier: "user@example.com",
			Channel:    domain.ChannelEmail,
			CodeHash:   string(hash),
			ExpiresAt:  time.Now().UTC().Add(10 * time.Minute),
			Attempts:   5,
		})
		svcTooMany := mustService(t, otpTooMany, &fakeUsers{}, &fakeNotifier{}, fakeFlags{email: true})
		_, _, _, err := svcTooMany.VerifyOTP(context.Background(), "user@example.com", domain.ChannelEmail, code)
		if err == nil || err.Error() != "too many attempts" {
			t.Fatalf("expected 'too many attempts' error, got %v", err)
		}
	})
}

func TestVerifyOTP_ExpiredChallenge(t *testing.T) {
	code := "123456"
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	otp := &fakeOTP{}
	_ = otp.UpsertChallenge(context.Background(), domain.OTPChallenge{
		ID:         "ch-expired-1",
		Identifier: "user@example.com",
		Channel:    domain.ChannelEmail,
		CodeHash:   string(hash),
		ExpiresAt:  time.Now().UTC().Add(-1 * time.Minute),
	})
	svc := mustService(t, otp, &fakeUsers{}, &fakeNotifier{}, fakeFlags{email: true})

	access, refresh, _, err := svc.VerifyOTP(context.Background(), "user@example.com", domain.ChannelEmail, code)
	if err == nil {
		t.Fatal("expected error for expired OTP challenge")
	}
	if access != "" || refresh != "" {
		t.Fatalf("expected empty tokens, got access=%q refresh=%q", access, refresh)
	}
	if otp.consumed {
		t.Fatal("expected expired challenge NOT to be consumed")
	}
}

func TestVerifyOTP_EmptyParameters(t *testing.T) {
	svc := mustService(t, &fakeOTP{}, &fakeUsers{}, &fakeNotifier{}, fakeFlags{email: true})
	tests := []struct {
		name       string
		identifier string
		channel    string
		code       string
	}{
		{"empty identifier", "", domain.ChannelEmail, "123456"},
		{"empty channel", "user@example.com", "", "123456"},
		{"empty code", "user@example.com", domain.ChannelEmail, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := svc.VerifyOTP(context.Background(), tc.identifier, tc.channel, tc.code)
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestRefresh_ValidRotation(t *testing.T) {
	refreshStore := newFakeRefreshStore()
	svc := mustServiceWithRefresh(t, &fakeOTP{}, &fakeUsers{}, refreshStore, &fakeNotifier{}, fakeFlags{})

	initialRefreshToken := "valid-refresh-token-value-12345"
	userID := "user-42"
	err := refreshStore.Store(context.Background(), userID, hashToken(initialRefreshToken), time.Now().UTC().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	newAccess, newRefresh, expires, err := svc.Refresh(context.Background(), initialRefreshToken)
	if err != nil {
		t.Fatalf("unexpected refresh error: %v", err)
	}
	if newAccess == "" {
		t.Fatal("expected non-empty new access token")
	}
	if newRefresh == "" {
		t.Fatal("expected non-empty new refresh token")
	}
	if newRefresh == initialRefreshToken {
		t.Fatal("expected rotated refresh token to be different from initial refresh token")
	}
	if expires.Before(time.Now().UTC()) {
		t.Fatalf("expected future expiry time, got %v", expires)
	}

	// Verify old refresh token is revoked / deleted
	_, err = refreshStore.FindValid(context.Background(), hashToken(initialRefreshToken))
	if err == nil {
		t.Fatal("expected old refresh token to be revoked/invalidated")
	}

	// Verify new refresh token is stored and maps to user
	storedUser, err := refreshStore.FindValid(context.Background(), hashToken(newRefresh))
	if err != nil {
		t.Fatalf("expected new refresh token to be valid in store: %v", err)
	}
	if storedUser != userID {
		t.Fatalf("expected stored user %q, got %q", userID, storedUser)
	}

	// Verify attempting to reuse old refresh token fails
	_, _, _, err = svc.Refresh(context.Background(), initialRefreshToken)
	if err == nil {
		t.Fatal("expected error when attempting to reuse revoked refresh token")
	}
}

func TestRefresh_RevokedOrNonExistent(t *testing.T) {
	refreshStore := newFakeRefreshStore()
	svc := mustServiceWithRefresh(t, &fakeOTP{}, &fakeUsers{}, refreshStore, &fakeNotifier{}, fakeFlags{})

	t.Run("non-existent refresh token", func(t *testing.T) {
		_, _, _, err := svc.Refresh(context.Background(), "unknown-refresh-token")
		if err == nil {
			t.Fatal("expected error for non-existent refresh token")
		}
	})

	t.Run("revoked refresh token", func(t *testing.T) {
		token := "active-then-revoked-token"
		_ = refreshStore.Store(context.Background(), "user-99", hashToken(token), time.Now().UTC().Add(time.Hour))
		// Revoke the token
		_ = refreshStore.Revoke(context.Background(), hashToken(token))

		_, _, _, err := svc.Refresh(context.Background(), token)
		if err == nil {
			t.Fatal("expected error for revoked refresh token")
		}
	})
}
