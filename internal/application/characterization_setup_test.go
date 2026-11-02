package application

// Setup indirection for the characterization tests. When the implementation
// moves a helper into another layer this file follows it; the expected values
// in characterization_test.go stay untouched.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/manovaspace/orbit-auth/internal/domain"
	orbittoken "github.com/manovaspace/orbit-auth/token"
)

func charHashToken(token string) string           { return domain.HashRefreshToken(token) }
func charHashIdentifier(identifier string) string { return domain.HashIdentifier(identifier) }
func charOTPCode() (string, error)                { return testCodes{}.NewCode() }
func charAPISecret() (string, error)              { return testSecrets{}.NewSecret() }
func charIsE164(value string) bool                { return domain.IsE164(value) }
func charBcryptCost() int                         { return bcrypt.DefaultCost }
func charOTPTTL() time.Duration                   { return domain.OTPTTL }
func charMaxOTPAttempts() int                     { return domain.MaxOTPAttempts }
func charAccessTokenTTL() time.Duration           { return domain.AccessTokenTTL }
func charRefreshTokenTTL() time.Duration          { return domain.RefreshTokenTTL }

var errCharTokenNotFound = errors.New("token not found")

type charTokenStore struct {
	created  []domain.APIToken
	byPrefix map[string]domain.APIToken
}

func newCharTokenStore() *charTokenStore {
	return &charTokenStore{byPrefix: make(map[string]domain.APIToken)}
}

func (s *charTokenStore) Create(_ context.Context, token domain.APIToken) error {
	s.created = append(s.created, token)
	s.byPrefix[token.Prefix] = token
	return nil
}

func (s *charTokenStore) ListByUser(context.Context, string) ([]domain.APITokenInfo, error) {
	return nil, nil
}

func (s *charTokenStore) RevokeToken(context.Context, string, string) error { return nil }

func (s *charTokenStore) FindByPrefix(_ context.Context, prefix string) (domain.APIToken, error) {
	token, ok := s.byPrefix[prefix]
	if !ok {
		return domain.APIToken{}, errCharTokenNotFound
	}
	return token, nil
}

func charServiceWithTokenStore(t *testing.T) (*Service, *charTokenStore) {
	t.Helper()
	tokens := newCharTokenStore()
	return charBuildService(t, &fakeOTP{}, newFakeRefreshStore(), tokens), tokens
}

func charServiceForTokens(t *testing.T) (*Service, *fakeRefreshStore) {
	t.Helper()
	refresh := newFakeRefreshStore()
	return charBuildService(t, &fakeOTP{}, refresh, newCharTokenStore()), refresh
}

func charBuildService(t *testing.T, otp domain.OTPStore, refresh domain.RefreshTokenStore, tokens domain.APITokenStore) *Service {
	t.Helper()
	return NewService(Deps{
		OTP:      otp,
		Users:    &fakeUsers{},
		Refresh:  refresh,
		Tokens:   tokens,
		Notifier: &fakeNotifier{},
		Flags:    fakeFlags{},
		Clock:    fixedClock{at: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)},
		IDs:      testUUIDs{},
		Signer:   testSigner{},
		Hasher:   testHasher{},
		Codes:    testCodes{},
		Secrets:  testSecrets{},
		Logger:   func(string, ...any) {},
	})
}

var testJWTKeyOnce sync.Once

var testJWTPrivateKey *rsa.PrivateKey
var testJWTKeyErr error

func charSignAccessToken(t *testing.T, subject string, expires time.Time) string {
	t.Helper()
	testJWTKeyOnce.Do(func() {
		testJWTPrivateKey, testJWTKeyErr = rsa.GenerateKey(rand.Reader, 2048)
	})
	if testJWTKeyErr != nil {
		t.Fatal(testJWTKeyErr)
	}
	raw, err := orbittoken.SignAccessToken(testJWTPrivateKey, "char-key", subject, expires)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func charParseUnverified(t *testing.T, raw string) (map[string]any, map[string]any) {
	t.Helper()
	claims := jwt.MapClaims{}
	parsed, _, err := new(jwt.Parser).ParseUnverified(raw, claims)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Header, claims
}
