package application

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

func passwordUser(t *testing.T, email, mobile, status string) *fakeUsers {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("s3cret-pass"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeUsers{user: domain.User{ID: "u1", Email: email, Mobile: mobile, PasswordHash: string(hash), Status: status}}
}

func TestLoginWithPassword_Mobile(t *testing.T) {
	svc := mustService(t, &fakeOTP{}, passwordUser(t, "", "+989121234567", domain.UserActive), &fakeNotifier{}, fakeFlags{})
	access, refresh, _, err := svc.LoginWithPassword(context.Background(), " +989121234567 ", "s3cret-pass")
	if err != nil {
		t.Fatal(err)
	}
	if access == "" || refresh == "" {
		t.Fatal("expected tokens")
	}
}

func TestLoginWithPassword_EmailStillWorks(t *testing.T) {
	svc := mustService(t, &fakeOTP{}, passwordUser(t, "chef@example.com", "", domain.UserActive), &fakeNotifier{}, fakeFlags{})
	if _, _, _, err := svc.LoginWithPassword(context.Background(), "Chef@Example.com", "s3cret-pass"); err != nil {
		t.Fatal(err)
	}
}

func TestLoginWithPassword_Rejections(t *testing.T) {
	cases := []struct {
		name       string
		identifier string
		password   string
		status     string
		want       error
	}{
		{"wrong password", "+989121234567", "nope", domain.UserActive, ErrInvalidCredentials},
		{"unknown mobile", "+989129999999", "s3cret-pass", domain.UserActive, ErrInvalidCredentials},
		{"local-format mobile is not a mobile identifier", "09121234567", "s3cret-pass", domain.UserActive, ErrInvalidCredentials},
		{"empty identifier", "", "s3cret-pass", domain.UserActive, ErrInvalidCredentials},
		{"disabled", "+989121234567", "s3cret-pass", domain.UserDisabled, ErrUserDisabled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := mustService(t, &fakeOTP{}, passwordUser(t, "", "+989121234567", tc.status), &fakeNotifier{}, fakeFlags{})
			_, _, _, err := svc.LoginWithPassword(context.Background(), tc.identifier, tc.password)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}
