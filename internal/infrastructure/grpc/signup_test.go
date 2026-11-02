package grpc

import (
	"context"
	"errors"
	"testing"
	"time"

	authv1 "github.com/manovaspace/orbit-auth/api/auth/v1"
	"github.com/manovaspace/orbit-auth/internal/application"
	"github.com/manovaspace/orbit-auth/internal/domain"
	"google.golang.org/grpc/codes"
)

type fakeSignup struct {
	email       string
	password    string
	displayName string
	locale      string
	orgName     string
	inviteToken string
	registerErr error

	verifyEmail    string
	verifyCode     string
	verifyPassword string
	verifyAccess   string
	verifyRefresh  string
	verifyExpires  time.Time
	verifyErr      error
}

func (f *fakeSignup) RegisterWithEmail(_ context.Context, email, password, displayName, locale, orgName, inviteToken string) error {
	f.email, f.password, f.displayName = email, password, displayName
	f.locale, f.orgName, f.inviteToken = locale, orgName, inviteToken
	return f.registerErr
}

func (f *fakeSignup) VerifySignup(_ context.Context, email, code, password string) (string, string, time.Time, error) {
	f.verifyEmail, f.verifyCode, f.verifyPassword = email, code, password
	return f.verifyAccess, f.verifyRefresh, f.verifyExpires, f.verifyErr
}

func TestSignupRegisterForwardsRequest(t *testing.T) {
	app := &fakeSignup{}
	srv := NewSignup(app)

	resp, err := srv.RegisterWithEmail(context.Background(), &authv1.RegisterWithEmailRequest{
		Email: "new@example.com", Password: "pw", DisplayName: "New", Locale: "fa", OrgName: "Acme", InviteToken: "token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("expected an empty response")
	}
	if app.email != "new@example.com" || app.password != "pw" || app.displayName != "New" ||
		app.locale != "fa" || app.orgName != "Acme" || app.inviteToken != "token" {
		t.Fatalf("request = %+v", app)
	}
}

func TestSignupRegisterMapsErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code codes.Code
		msg  string
	}{
		{"disabled", application.ErrSignupDisabled, codes.FailedPrecondition, "sign-up is disabled"},
		{"invalid email", domain.ErrInvalidEmail, codes.InvalidArgument, "invalid email"},
		{"invalid invite", domain.ErrInvalidInvite, codes.InvalidArgument, "invalid invite"},
		{"password required", application.ErrPasswordRequired, codes.InvalidArgument, "password is required"},
		{"store failure", errors.New("db down"), codes.Internal, "internal error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := NewSignup(&fakeSignup{registerErr: tc.err})
			_, err := srv.RegisterWithEmail(context.Background(), &authv1.RegisterWithEmailRequest{})
			assertCode(t, err, tc.code, tc.msg)
		})
	}
}

func TestSignupVerifyReturnsTokens(t *testing.T) {
	expires := time.Date(2026, 11, 2, 12, 15, 0, 0, time.UTC)
	app := &fakeSignup{verifyAccess: "access", verifyRefresh: "refresh", verifyExpires: expires}
	srv := NewSignup(app)

	resp, err := srv.VerifySignup(context.Background(), &authv1.VerifySignupRequest{Email: "new@example.com", Code: "123456", Password: "owner-pass"})
	if err != nil {
		t.Fatal(err)
	}
	if app.verifyEmail != "new@example.com" || app.verifyCode != "123456" || app.verifyPassword != "owner-pass" {
		t.Fatalf("verify = %q %q %q", app.verifyEmail, app.verifyCode, app.verifyPassword)
	}
	got := resp.GetTokens()
	if got.GetAccessToken() != "access" || got.GetRefreshToken() != "refresh" || got.GetExpiresAt() != "2026-11-02T12:15:00Z" {
		t.Fatalf("tokens = %+v", got)
	}
}

func TestSignupVerifyMapsErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code codes.Code
		msg  string
	}{
		{"disabled", application.ErrSignupDisabled, codes.FailedPrecondition, "sign-up is disabled"},
		{"wrong code", application.ErrInvalidOTPCode, codes.Unauthenticated, "invalid or expired code"},
		{"too many attempts", application.ErrTooManyOTPAttempts, codes.Unauthenticated, "too many attempts"},
		{"store failure", errors.New("db down"), codes.Internal, "internal error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := NewSignup(&fakeSignup{verifyErr: tc.err})
			_, err := srv.VerifySignup(context.Background(), &authv1.VerifySignupRequest{Email: "new@example.com", Code: "000000", Password: "owner-pass"})
			assertCode(t, err, tc.code, tc.msg)
		})
	}
}
