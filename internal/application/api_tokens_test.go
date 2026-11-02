package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

func serviceWithTokens(t *testing.T, tokens *charTokenStore) *Service {
	t.Helper()
	return charBuildService(t, &fakeOTP{}, newFakeRefreshStore(), tokens)
}

func TestCreateApiTokenValidatesInput(t *testing.T) {
	svc := serviceWithTokens(t, newCharTokenStore())
	if _, _, _, err := svc.CreateApiToken(context.Background(), "", "name", nil, nil); err == nil {
		t.Fatal("empty user id was accepted")
	}
	if _, _, _, err := svc.CreateApiToken(context.Background(), "u1", "  ", nil, nil); err == nil {
		t.Fatal("empty name was accepted")
	}
}

func TestListAndRevokeApiTokenValidateInput(t *testing.T) {
	svc := serviceWithTokens(t, newCharTokenStore())
	if _, err := svc.ListApiTokens(context.Background(), ""); err == nil {
		t.Fatal("empty user id was accepted")
	}
	if err := svc.RevokeApiToken(context.Background(), "", "t1"); err == nil {
		t.Fatal("empty user id was accepted")
	}
	if err := svc.RevokeApiToken(context.Background(), "u1", ""); err == nil {
		t.Fatal("empty token id was accepted")
	}
}

func TestValidateApiTokenRejectsExpiredAndUnknown(t *testing.T) {
	tokens := newCharTokenStore()
	svc := serviceWithTokens(t, tokens)

	// Unknown prefix.
	if _, _, err := svc.ValidateApiToken(context.Background(), "oat_abcdefgh.tail"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("unknown prefix err = %v", err)
	}

	// Expired token.
	expired := time.Date(2026, 11, 2, 11, 0, 0, 0, time.UTC)
	hash, err := testHasher{}.Hash("abcdefgh" + "tail")
	if err != nil {
		t.Fatal(err)
	}
	tokens.byPrefix["abcdefgh"] = domain.APIToken{
		ID: "t1", UserID: "u1", Prefix: "abcdefgh", TokenHash: hash, ExpiresAt: &expired,
	}
	if _, _, err := svc.ValidateApiToken(context.Background(), "oat_abcdefgh.tail"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired token err = %v", err)
	}

	// Revoked token.
	revoked := time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC)
	tokens.byPrefix["abcdefgh"] = domain.APIToken{
		ID: "t1", UserID: "u1", Prefix: "abcdefgh", TokenHash: hash, RevokedAt: &revoked,
	}
	if _, _, err := svc.ValidateApiToken(context.Background(), "oat_abcdefgh.tail"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("revoked token err = %v", err)
	}

	// Wrong secret for a live token.
	tokens.byPrefix["abcdefgh"] = domain.APIToken{ID: "t1", UserID: "u1", Prefix: "abcdefgh", TokenHash: hash}
	if _, _, err := svc.ValidateApiToken(context.Background(), "oat_abcdefgh.wrong"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong secret err = %v", err)
	}
}

type demoUsersStore struct {
	fakeUsers
	upserts []string
}

func (s *demoUsersStore) UpsertDemoUser(_ context.Context, email, mobile, _, _ string) error {
	s.upserts = append(s.upserts, email+"|"+mobile)
	return nil
}

func demoService(t *testing.T, mode bool, users []domain.DemoUser) (*Service, *demoUsersStore) {
	t.Helper()
	store := &demoUsersStore{}
	svc := NewService(Deps{
		Users:     store,
		Clock:     fixedClock{at: time.Now()},
		IDs:       testUUIDs{},
		Signer:    testSigner{},
		Hasher:    testHasher{},
		Codes:     testCodes{},
		Secrets:   testSecrets{},
		DemoMode:  mode,
		DemoUsers: users,
		Logger:    func(string, ...any) {},
	})
	return svc, store
}

func TestSeedDemoUsersNeedsDemoMode(t *testing.T) {
	svc, store := demoService(t, false, []domain.DemoUser{{Email: "demo@example.com", Password: "pw"}})
	if err := svc.SeedDemoUsers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.upserts) != 0 {
		t.Fatalf("seeded %d users with demo mode off", len(store.upserts))
	}
}

func TestSeedDemoUsersValidatesAndNormalises(t *testing.T) {
	svc, store := demoService(t, true, []domain.DemoUser{
		{Email: "  Demo@Example.com ", Password: "pw", DisplayName: "Demo"},
		{Mobile: "+989121234567", Password: "pw"},
	})
	if err := svc.SeedDemoUsers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.upserts) != 2 || store.upserts[0] != "demo@example.com|" || store.upserts[1] != "|+989121234567" {
		t.Fatalf("upserts = %v", store.upserts)
	}
}

func TestSeedDemoUsersRejectsInvalidEntries(t *testing.T) {
	tests := []struct {
		name  string
		users []domain.DemoUser
		want  string
	}{
		{"no identifier", []domain.DemoUser{{Password: "pw"}}, "demo user requires email or mobile"},
		{"bad mobile", []domain.DemoUser{{Mobile: "09121234567", Password: "pw"}}, "demo user mobile must be E.164"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := demoService(t, true, tc.users)
			err := svc.SeedDemoUsers(context.Background())
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
