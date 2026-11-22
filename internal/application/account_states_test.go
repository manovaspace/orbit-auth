package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// statusUsers answers FindByID from a per-account status map, so one test can
// hold the caller and the target in different states.
type statusUsers struct {
	*fakeUsers
	statuses map[string]string
}

func (s statusUsers) FindByID(_ context.Context, id string) (domain.User, error) {
	status, ok := s.statuses[id]
	if !ok {
		return domain.User{}, domain.ErrUserNotFound
	}
	return domain.User{ID: id, Status: status}, nil
}

func TestRefreshAndSelectOrgRefuseUnusableAccounts(t *testing.T) {
	for _, status := range []string{domain.UserDisabled, domain.UserSignupPending} {
		t.Run(status, func(t *testing.T) {
			refreshStore := newFakeRefreshStore()
			users := &fakeUsers{user: domain.User{ID: "u1", Status: domain.UserActive}}
			svc := mustServiceWithRefresh(t, &fakeOTP{}, users, refreshStore, &fakeNotifier{}, fakeFlags{})
			token := "refresh-token-for-account-state-tests"
			if err := refreshStore.Store(context.Background(), "u1", "", domain.HashRefreshToken(token), time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			users.user.Status = status

			if _, _, _, err := svc.Refresh(context.Background(), token); !errors.Is(err, ErrInvalidRefresh) {
				t.Fatalf("Refresh err = %v, want ErrInvalidRefresh", err)
			}
			if err := refreshStore.Store(context.Background(), "u1", "", domain.HashRefreshToken(token), time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := svc.SelectOrg(context.Background(), "u1", token, "org-1"); !errors.Is(err, ErrInvalidRefresh) {
				t.Fatalf("SelectOrg err = %v, want ErrInvalidRefresh", err)
			}
		})
	}
}

func TestDisabledAccountCannotCreateOrgOrToken(t *testing.T) {
	users := &fakeUsers{user: domain.User{ID: "u1", Status: domain.UserDisabled}}
	svc := mustServiceWithRefresh(t, &fakeOTP{}, users, newFakeRefreshStore(), &fakeNotifier{}, fakeFlags{})

	if _, err := svc.CreateOrg(context.Background(), "u1", "Acme", ""); !errors.Is(err, domain.ErrAccountNotActive) {
		t.Fatalf("CreateOrg err = %v, want ErrAccountNotActive", err)
	}
	if _, _, _, err := svc.CreateApiToken(context.Background(), "u1", "ci", nil, nil); !errors.Is(err, domain.ErrAccountNotActive) {
		t.Fatalf("CreateApiToken err = %v, want ErrAccountNotActive", err)
	}
}

func TestDisabledCallerCannotChangeMemberships(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)
	users := statusUsers{fakeUsers: &fakeUsers{}, statuses: map[string]string{"owner": domain.UserActive, "user-2": domain.UserActive}}
	svc.users = users
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AddMember(context.Background(), org.ID, "owner", "user-2"); err != nil {
		t.Fatal(err)
	}

	users.statuses["owner"] = domain.UserDisabled
	if err := svc.AddMember(context.Background(), org.ID, "owner", "user-3"); !errors.Is(err, domain.ErrAccountNotActive) {
		t.Fatalf("AddMember err = %v, want ErrAccountNotActive", err)
	}
	if err := svc.SuspendMember(context.Background(), org.ID, "owner", "user-2"); !errors.Is(err, domain.ErrAccountNotActive) {
		t.Fatalf("SuspendMember err = %v, want ErrAccountNotActive", err)
	}
	membership, err := store.GetMembership(context.Background(), org.ID, "user-2")
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipActive {
		t.Fatalf("user-2 status = %q after refused suspend, want active", membership.Status)
	}
}

func TestReactivateMemberFollowsSuspensionRules(t *testing.T) {
	store := newFakeOrgStore()
	svc := newOrgService(t, store)
	org, err := svc.CreateOrg(context.Background(), "owner", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"member-1", "member-2"} {
		if err := svc.AddMember(context.Background(), org.ID, "owner", id); err != nil {
			t.Fatal(err)
		}
		if err := svc.SuspendMember(context.Background(), org.ID, "owner", id); err != nil {
			t.Fatal(err)
		}
	}

	if err := svc.ReactivateMember(context.Background(), org.ID, "member-1", "member-2"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member reactivate err = %v, want ErrForbidden", err)
	}
	if err := svc.ReactivateMember(context.Background(), org.ID, "owner", "member-1"); err != nil {
		t.Fatal(err)
	}
	membership, err := store.GetMembership(context.Background(), org.ID, "member-1")
	if err != nil {
		t.Fatal(err)
	}
	if membership.Status != domain.MembershipActive {
		t.Fatalf("status = %q, want active", membership.Status)
	}
	if err := svc.ReactivateMember(context.Background(), org.ID, "owner", "member-1"); err != nil {
		t.Fatalf("reactivating an active member: %v", err)
	}
}

// singleAPIToken answers FindByPrefix with one stored token.
type singleAPIToken struct {
	fakeApiTokens
	token domain.APIToken
}

func (s singleAPIToken) FindByPrefix(_ context.Context, prefix string) (domain.APIToken, error) {
	if prefix != s.token.Prefix {
		return domain.APIToken{}, errors.New("not found")
	}
	return s.token, nil
}

func TestValidateApiTokenRefusesDisabledAccount(t *testing.T) {
	full := strings.Repeat("x", domain.APITokenPrefixLength) + "payload-for-account-state"
	prefix, rest := full[:domain.APITokenPrefixLength], full[domain.APITokenPrefixLength:]
	secret := domain.APITokenScheme + prefix + "." + rest
	hash, err := testHasher{}.Hash(full)
	if err != nil {
		t.Fatal(err)
	}
	token := domain.APIToken{ID: "tok-1", UserID: "u1", Prefix: prefix, TokenHash: hash, Scopes: []string{"read"}}

	for _, tc := range []struct {
		status string
		want   error
	}{
		{domain.UserActive, nil},
		{domain.UserDisabled, ErrInvalidToken},
	} {
		svc := mustServiceWithRefresh(t, &fakeOTP{}, &fakeUsers{}, newFakeRefreshStore(), &fakeNotifier{}, fakeFlags{})
		svc.users = statusUsers{fakeUsers: &fakeUsers{}, statuses: map[string]string{"u1": tc.status}}
		svc.tokens = singleAPIToken{token: token}
		userID, _, err := svc.ValidateApiToken(context.Background(), secret)
		if tc.want == nil {
			if err != nil || userID != "u1" {
				t.Fatalf("%s: ValidateApiToken = %q, %v; want the user", tc.status, userID, err)
			}
			continue
		}
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.status, err, tc.want)
		}
	}
}

// disabledOTPUsers returns the account with a fixed status on every sign-in.
type disabledOTPUsers struct {
	*fakeUsers
	status string
}

func (u disabledOTPUsers) FindOrCreateByEmail(_ context.Context, email string) (domain.User, error) {
	return domain.User{ID: "u1", Email: email, Status: u.status}, nil
}

func TestVerifyOTPRefusesDisabledAccountLikeWrongCode(t *testing.T) {
	const code = "123456"
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	build := func(status string) (*Service, *fakeRefreshStore) {
		otp := &fakeOTP{}
		_ = otp.UpsertChallenge(context.Background(), domain.OTPChallenge{
			ID: "ch-1", Identifier: "u@example.com", Channel: domain.ChannelEmail,
			CodeHash: string(hash), ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		})
		refresh := newFakeRefreshStore()
		svc := mustServiceWithRefresh(t, otp, disabledOTPUsers{fakeUsers: &fakeUsers{}, status: status}, refresh, &fakeNotifier{}, fakeFlags{email: true})
		return svc, refresh
	}

	svc, refresh := build(domain.UserDisabled)
	_, _, _, disabledErr := svc.VerifyOTP(context.Background(), "u@example.com", domain.ChannelEmail, code)
	if !errors.Is(disabledErr, ErrInvalidOTPCode) {
		t.Fatalf("disabled err = %v, want ErrInvalidOTPCode", disabledErr)
	}
	if len(refresh.tokens) != 0 {
		t.Fatalf("a disabled account received %d refresh tokens", len(refresh.tokens))
	}

	svc, _ = build(domain.UserActive)
	_, _, _, wrongErr := svc.VerifyOTP(context.Background(), "u@example.com", domain.ChannelEmail, "000000")
	if disabledErr.Error() != wrongErr.Error() || !errors.Is(wrongErr, ErrInvalidOTPCode) {
		t.Fatalf("disabled err %q differs from wrong code err %q", disabledErr, wrongErr)
	}
}
