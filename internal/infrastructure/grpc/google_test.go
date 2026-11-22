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

// The Google sign-in calls on the shared fake. Tests that do not reach them
// never see these zero values.
func (f *fakeAuth) StartGoogleSignIn(context.Context, string, string, string) (string, string, error) {
	return "", "", nil
}

func (f *fakeAuth) CompleteGoogleSignIn(context.Context, string, string) (application.GoogleSignInResult, error) {
	return application.GoogleSignInResult{}, nil
}

// googleAuth overrides the two Google calls and records their arguments.
type googleAuth struct {
	*fakeAuth
	startRedirectURI string
	startReturnTo    string
	startInviteToken string
	startURL         string
	startState       string
	startErr         error

	completeState    string
	completeCode     string
	completeAccess   string
	completeRefresh  string
	completeExpires  time.Time
	completeReturnTo string
	completeErr      error
}

func (f *googleAuth) StartGoogleSignIn(_ context.Context, redirectURI, returnTo, inviteToken string) (string, string, error) {
	f.startRedirectURI, f.startReturnTo, f.startInviteToken = redirectURI, returnTo, inviteToken
	return f.startURL, f.startState, f.startErr
}

func (f *googleAuth) CompleteGoogleSignIn(_ context.Context, state, code string) (application.GoogleSignInResult, error) {
	f.completeState, f.completeCode = state, code
	return application.GoogleSignInResult{
		Access:   f.completeAccess,
		Refresh:  f.completeRefresh,
		Expires:  f.completeExpires,
		ReturnTo: f.completeReturnTo,
	}, f.completeErr
}

func TestStartGoogleSignInPassesArgumentsAndReturnsURLAndState(t *testing.T) {
	fake := &googleAuth{fakeAuth: &fakeAuth{}, startURL: "https://accounts.example.com/auth?x=1", startState: "state-value"}
	server := New(fake)

	resp, err := server.StartGoogleSignIn(context.Background(), &authv1.StartGoogleSignInRequest{
		RedirectUri: "https://app.example.com/cb",
		ReturnTo:    "/home",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.startRedirectURI != "https://app.example.com/cb" || fake.startReturnTo != "/home" {
		t.Fatalf("service got redirect %q and return_to %q", fake.startRedirectURI, fake.startReturnTo)
	}
	if resp.GetAuthorizationUrl() != fake.startURL || resp.GetState() != "state-value" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestCompleteGoogleSignInReturnsTokensAndReturnTo(t *testing.T) {
	exp := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	fake := &googleAuth{
		fakeAuth:         &fakeAuth{},
		completeAccess:   "access-token",
		completeRefresh:  "refresh-token",
		completeExpires:  exp,
		completeReturnTo: "/inbox",
	}
	server := New(fake)

	resp, err := server.CompleteGoogleSignIn(context.Background(), &authv1.CompleteGoogleSignInRequest{
		State: "state-value",
		Code:  "code-value",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.completeState != "state-value" || fake.completeCode != "code-value" {
		t.Fatalf("service got state %q and code %q", fake.completeState, fake.completeCode)
	}
	tokens := resp.GetTokens()
	if tokens.GetAccessToken() != "access-token" || tokens.GetRefreshToken() != "refresh-token" || tokens.GetExpiresAt() != exp.Format(time.RFC3339) {
		t.Fatalf("tokens = %+v", tokens)
	}
	if resp.GetReturnTo() != "/inbox" {
		t.Fatalf("return_to = %q, want /inbox", resp.GetReturnTo())
	}
}

func TestGoogleErrorsMapToFixedStatuses(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		code    codes.Code
		message string
	}{
		{"disabled", domain.ErrGoogleDisabled, codes.FailedPrecondition, "google sign-in is off"},
		{"redirect not allowed", domain.ErrInvalidRedirectURI, codes.InvalidArgument, domain.ErrInvalidRedirectURI.Error()},
		{"return_to invalid", domain.ErrInvalidReturnTo, codes.InvalidArgument, domain.ErrInvalidReturnTo.Error()},
		{"state unknown or expired", domain.ErrInvalidGoogleState, codes.Unauthenticated, "google sign-in failed"},
		{"exchange failed", domain.ErrGoogleExchangeFailed, codes.Unauthenticated, "google sign-in failed"},
		{"account not allowed", domain.ErrGoogleAccountNotAllowed, codes.PermissionDenied, "account not allowed"},
		{"invite unknown or expired", domain.ErrInvalidInvite, codes.InvalidArgument, domain.ErrInvalidInvite.Error()},
		{"phone invite", domain.ErrInviteNeedsPassword, codes.FailedPrecondition, domain.ErrInviteNeedsPassword.Error()},
		{"invite email mismatch", domain.ErrInviteEmailMismatch, codes.FailedPrecondition, domain.ErrInviteEmailMismatch.Error()},
		{"invite sign-up off", domain.ErrInviteSignupDisabled, codes.FailedPrecondition, domain.ErrInviteSignupDisabled.Error()},
		{"store failure", errors.New("connection reset with secret-looking detail"), codes.Internal, "internal error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := New(&googleAuth{fakeAuth: &fakeAuth{}, startErr: tc.err, completeErr: tc.err})
			_, err := server.StartGoogleSignIn(context.Background(), &authv1.StartGoogleSignInRequest{})
			assertCode(t, err, tc.code, tc.message)
			_, err = server.CompleteGoogleSignIn(context.Background(), &authv1.CompleteGoogleSignInRequest{})
			assertCode(t, err, tc.code, tc.message)
		})
	}
}

// The Google RPCs run before any sign-in, so the access-token interceptor must
// not guard them.
func TestGoogleRPCsAreNotAuthenticatedMethods(t *testing.T) {
	for _, method := range []string{"StartGoogleSignIn", "CompleteGoogleSignIn"} {
		if _, guarded := authenticatedAuthMethods["/auth.v1.AuthService/"+method]; guarded {
			t.Fatalf("%s must stay outside authenticatedAuthMethods", method)
		}
	}
}
