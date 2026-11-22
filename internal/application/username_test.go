package application

import (
	"context"
	"errors"
	"testing"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// takenUsers reports every username as held by another account.
type takenUsers struct {
	*fakeUsers
}

func (takenUsers) SetUsername(context.Context, string, string) error {
	return domain.ErrUsernameTaken
}

func usernameUser(t *testing.T, username, mobile string) *fakeUsers {
	t.Helper()
	users := passwordUser(t, "", mobile, domain.UserActive)
	users.user.Username = username
	return users
}

func TestLoginWithPasswordByUsername(t *testing.T) {
	svc := mustService(t, &fakeOTP{}, usernameUser(t, "chef.one", ""), &fakeNotifier{}, fakeFlags{})
	for _, identifier := range []string{"chef.one", "  Chef.One  "} {
		if _, _, _, err := svc.LoginWithPassword(context.Background(), identifier, "s3cret-pass"); err != nil {
			t.Fatalf("login by username %q: %v", identifier, err)
		}
	}
}

func TestLoginWithPasswordByUsernameRejections(t *testing.T) {
	svc := mustService(t, &fakeOTP{}, usernameUser(t, "chef.one", ""), &fakeNotifier{}, fakeFlags{})
	if _, _, _, err := svc.LoginWithPassword(context.Background(), "nobody.here", "s3cret-pass"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown username err = %v, want ErrInvalidCredentials", err)
	}
	if _, _, _, err := svc.LoginWithPassword(context.Background(), "chef.one", "wrong-pass"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password err = %v, want ErrInvalidCredentials", err)
	}
}

func TestLoginWithPasswordByLocalMobileUsesCallingCode(t *testing.T) {
	svc := mustService(t, &fakeOTP{}, passwordUser(t, "", "+989121234567", domain.UserActive), &fakeNotifier{}, fakeFlags{})
	svc.defaultCallingCode = "98"
	for _, identifier := range []string{"09121234567", "۰۹۱۲۱۲۳۴۵۶۷", "0912-123-4567"} {
		if _, _, _, err := svc.LoginWithPassword(context.Background(), identifier, "s3cret-pass"); err != nil {
			t.Fatalf("login by local mobile %q: %v", identifier, err)
		}
	}
}

func TestLoginWithPasswordLocalMobileWithoutCallingCodeFails(t *testing.T) {
	svc := mustService(t, &fakeOTP{}, passwordUser(t, "", "+989121234567", domain.UserActive), &fakeNotifier{}, fakeFlags{})
	if _, _, _, err := svc.LoginWithPassword(context.Background(), "09121234567", "s3cret-pass"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("local mobile without calling code err = %v, want ErrInvalidCredentials", err)
	}
}

func TestSetUsernameStoresNormalizedName(t *testing.T) {
	users := usernameUser(t, "", "")
	svc := mustService(t, &fakeOTP{}, users, &fakeNotifier{}, fakeFlags{})
	got, err := svc.SetUsername(context.Background(), "u1", "  Chef.One ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "chef.one" || users.user.Username != "chef.one" {
		t.Fatalf("username = %q, stored %q; want chef.one", got, users.user.Username)
	}
}

func TestSetUsernameRejections(t *testing.T) {
	svc := mustService(t, &fakeOTP{}, usernameUser(t, "", ""), &fakeNotifier{}, fakeFlags{})
	if _, err := svc.SetUsername(context.Background(), "u1", "ab"); !errors.Is(err, domain.ErrInvalidUsername) {
		t.Fatalf("invalid username err = %v, want ErrInvalidUsername", err)
	}
	if _, err := svc.SetUsername(context.Background(), "u2", "chef.one"); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("unknown user err = %v, want ErrUserNotFound", err)
	}
	if _, err := svc.SetUsername(context.Background(), "", "chef.one"); err == nil {
		t.Fatal("empty user id accepted")
	}

	taken := mustService(t, &fakeOTP{}, &takenUsers{fakeUsers: usernameUser(t, "", "")}, &fakeNotifier{}, fakeFlags{})
	if _, err := taken.SetUsername(context.Background(), "u1", "chef.one"); !errors.Is(err, domain.ErrUsernameTaken) {
		t.Fatalf("taken username err = %v, want ErrUsernameTaken", err)
	}
}

func TestSeedDemoUsernameIsNormalizedAndValidated(t *testing.T) {
	svc, store := demoService(t, true, []domain.DemoUser{
		{Email: "demo@example.com", Username: " Demo.User ", Password: "pw"},
	})
	if err := svc.SeedDemoUsers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.upserts) != 1 || store.upserts[0] != "demo@example.com||demo.user" {
		t.Fatalf("upserts = %v", store.upserts)
	}

	bad, _ := demoService(t, true, []domain.DemoUser{{Email: "demo@example.com", Username: "ab", Password: "pw"}})
	if err := bad.SeedDemoUsers(context.Background()); err == nil {
		t.Fatal("invalid demo username was accepted")
	}
}
