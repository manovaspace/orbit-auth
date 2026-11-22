package grpc

import (
	"context"
	"errors"
	"testing"

	authv1 "github.com/manovaspace/orbit-auth/api/auth/v1"
	"github.com/manovaspace/orbit-auth/internal/application"
	"github.com/manovaspace/orbit-auth/internal/domain"
	"google.golang.org/grpc/codes"
)

const testAccountCaller = "11111111-1111-4111-8111-111111111111"

// fakeAccountService records the caller of each call and returns err.
type fakeAccountService struct {
	err    error
	caller string
	calls  int
}

func (f *fakeAccountService) GetSignInMethods(_ context.Context, userID string) (domain.SignInMethods, error) {
	f.caller, f.calls = userID, f.calls+1
	return domain.SignInMethods{HasPassword: true}, f.err
}

func (f *fakeAccountService) SetPassword(_ context.Context, userID, _, _, _ string) error {
	f.caller, f.calls = userID, f.calls+1
	return f.err
}

func (f *fakeAccountService) StartGoogleLink(_ context.Context, userID, _, _ string) (string, string, error) {
	f.caller, f.calls = userID, f.calls+1
	return "https://accounts.example.com/auth", "state-value", f.err
}

func (f *fakeAccountService) CompleteGoogleLink(_ context.Context, userID, _, _ string) (application.GoogleLinkResult, error) {
	f.caller, f.calls = userID, f.calls+1
	return application.GoogleLinkResult{ReturnTo: "/settings", Email: "ali@example.com"}, f.err
}

func (f *fakeAccountService) UnlinkGoogle(_ context.Context, userID string) error {
	f.caller, f.calls = userID, f.calls+1
	return f.err
}

func (f *fakeAccountService) RevokeAllSessions(_ context.Context, userID, _ string) error {
	f.caller, f.calls = userID, f.calls+1
	return f.err
}

// The caller is the verified token subject. A request has no field that
// names another account, and the handler passes the subject to the service.
func TestAccountHandlersActOnTheTokenSubject(t *testing.T) {
	fake := &fakeAccountService{}
	server := NewAccount(fake)
	ctx := withCallerID(context.Background(), testAccountCaller)

	if _, err := server.GetSignInMethods(ctx, &authv1.GetSignInMethodsRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.SetPassword(ctx, &authv1.SetPasswordRequest{NewPassword: "brand-new-password"}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.StartGoogleLink(ctx, &authv1.StartGoogleLinkRequest{RedirectUri: "https://app.example.com/cb", ReturnTo: "/"}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.CompleteGoogleLink(ctx, &authv1.CompleteGoogleLinkRequest{State: "s", Code: "c"}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.UnlinkGoogle(ctx, &authv1.UnlinkGoogleRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.RevokeAllSessions(ctx, &authv1.RevokeAllSessionsRequest{}); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 6 || fake.caller != testAccountCaller {
		t.Fatalf("calls = %d, caller = %q; want 6 calls for the token subject", fake.calls, fake.caller)
	}
}

func TestAccountHandlersRefuseAMissingOrMalformedCaller(t *testing.T) {
	fake := &fakeAccountService{}
	server := NewAccount(fake)
	if _, err := server.GetSignInMethods(context.Background(), &authv1.GetSignInMethodsRequest{}); err == nil {
		t.Fatal("no caller accepted")
	}
	if _, err := server.UnlinkGoogle(withCallerID(context.Background(), "not-a-uuid"), &authv1.UnlinkGoogleRequest{}); err == nil {
		t.Fatal("malformed caller accepted")
	}
	if fake.calls != 0 {
		t.Fatal("the service ran without a valid caller")
	}
}

func TestAccountErrorsMapToFixedStatuses(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		code    codes.Code
		message string
	}{
		{"wrong current password", application.ErrInvalidCredentials, codes.Unauthenticated, "invalid credentials"},
		{"bad refresh token", application.ErrInvalidRefresh, codes.Unauthenticated, "invalid refresh token"},
		{"short password", application.ErrPasswordTooShort, codes.InvalidArgument, application.ErrPasswordTooShort.Error()},
		{"no identifier", domain.ErrNoPasswordIdentifier, codes.FailedPrecondition, domain.ErrNoPasswordIdentifier.Error()},
		{"last method", domain.ErrLastSignInMethod, codes.FailedPrecondition, domain.ErrLastSignInMethod.Error()},
		{"already linked", domain.ErrGoogleAlreadyLinked, codes.FailedPrecondition, domain.ErrGoogleAlreadyLinked.Error()},
		{"identity taken", domain.ErrGoogleIdentityTaken, codes.AlreadyExists, domain.ErrGoogleIdentityTaken.Error()},
		{"not linked", domain.ErrGoogleNotLinked, codes.NotFound, domain.ErrGoogleNotLinked.Error()},
		{"account not active", domain.ErrAccountNotActive, codes.PermissionDenied, "account is not active"},
		{"user not found", domain.ErrUserNotFound, codes.NotFound, "user not found"},
		{"link state refused", domain.ErrInvalidGoogleState, codes.Unauthenticated, "google sign-in failed"},
		{"store failure", errors.New("connection reset with secret-looking detail"), codes.Internal, "internal error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := NewAccount(&fakeAccountService{err: tc.err})
			_, err := server.SetPassword(withCallerID(context.Background(), testAccountCaller), &authv1.SetPasswordRequest{NewPassword: "brand-new-password"})
			assertCode(t, err, tc.code, tc.message)
		})
	}
}
