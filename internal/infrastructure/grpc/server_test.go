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
	"google.golang.org/grpc/status"
)

type fakeAuth struct {
	requestIdentifier  string
	requestChannel     string
	requestCorrelation string
	requestExpires     time.Time
	requestErr         error

	verifyIdentifier string
	verifyChannel    string
	verifyCode       string
	verifyAccess     string
	verifyRefresh    string
	verifyExpires    time.Time
	verifyErr        error

	loginIdentifier string
	loginPassword   string
	loginAccess     string
	loginRefresh    string
	loginExpires    time.Time
	loginErr        error

	refreshToken string
	refreshErr   error

	createUserID  string
	createName    string
	createScopes  []string
	createExpires *time.Time
	createID      string
	createPrefix  string
	createSecret  string
	createErr     error

	listUserID string
	listTokens []domain.APITokenInfo
	listErr    error

	revokeUserID  string
	revokeTokenID string
	revokeErr     error

	validateSecret string
	validateUserID string
	validateScopes []string
	validateErr    error
}

func (f *fakeAuth) RequestOTP(_ context.Context, identifier, channel, correlationID string) (time.Time, error) {
	f.requestIdentifier, f.requestChannel, f.requestCorrelation = identifier, channel, correlationID
	return f.requestExpires, f.requestErr
}

func (f *fakeAuth) VerifyOTP(_ context.Context, identifier, channel, code string) (string, string, time.Time, error) {
	f.verifyIdentifier, f.verifyChannel, f.verifyCode = identifier, channel, code
	return f.verifyAccess, f.verifyRefresh, f.verifyExpires, f.verifyErr
}

func (f *fakeAuth) LoginWithPassword(_ context.Context, identifier, password string) (string, string, time.Time, error) {
	f.loginIdentifier, f.loginPassword = identifier, password
	return f.loginAccess, f.loginRefresh, f.loginExpires, f.loginErr
}

func (f *fakeAuth) Refresh(_ context.Context, refreshToken string) (string, string, time.Time, error) {
	f.refreshToken = refreshToken
	return f.loginAccess, f.loginRefresh, f.loginExpires, f.refreshErr
}

func (f *fakeAuth) CreateApiToken(_ context.Context, userID, name string, scopes []string, expiresAt *time.Time) (string, string, string, error) {
	f.createUserID, f.createName, f.createScopes, f.createExpires = userID, name, scopes, expiresAt
	return f.createID, f.createPrefix, f.createSecret, f.createErr
}

func (f *fakeAuth) ListApiTokens(_ context.Context, userID string) ([]domain.APITokenInfo, error) {
	f.listUserID = userID
	return f.listTokens, f.listErr
}

func (f *fakeAuth) RevokeApiToken(_ context.Context, userID, tokenID string) error {
	f.revokeUserID, f.revokeTokenID = userID, tokenID
	return f.revokeErr
}

func (f *fakeAuth) ValidateApiToken(_ context.Context, secret string) (string, []string, error) {
	f.validateSecret = secret
	return f.validateUserID, f.validateScopes, f.validateErr
}

func assertCode(t *testing.T, err error, want codes.Code, message string) {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("err = %v, want a gRPC status", err)
	}
	if st.Code() != want {
		t.Fatalf("code = %v, want %v (%v)", st.Code(), want, err)
	}
	if message != "" && st.Message() != message {
		t.Fatalf("message = %q, want %q", st.Message(), message)
	}
}

func TestRequestOTPMapsLegacyEmailAndResponse(t *testing.T) {
	expires := time.Date(2026, 11, 2, 12, 10, 0, 0, time.UTC)
	app := &fakeAuth{requestExpires: expires}
	srv := New(app)

	resp, err := srv.RequestOTP(context.Background(), &authv1.RequestOTPRequest{
		//nolint:staticcheck // legacy field is part of the wire contract
		Email:         "person@example.com",
		CorrelationId: "corr-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if app.requestIdentifier != "person@example.com" || app.requestChannel != domain.ChannelEmail || app.requestCorrelation != "corr-1" {
		t.Fatalf("request = %q %q %q", app.requestIdentifier, app.requestChannel, app.requestCorrelation)
	}
	if resp.GetExpiresAt() != "2026-11-02T12:10:00Z" {
		t.Fatalf("expires_at = %q", resp.GetExpiresAt())
	}
}

func TestRequestOTPInfersChannelAndMapsDisabledFlag(t *testing.T) {
	app := &fakeAuth{}
	srv := New(app)
	if _, err := srv.RequestOTP(context.Background(), &authv1.RequestOTPRequest{Identifier: "+989121234567"}); err != nil {
		t.Fatal(err)
	}
	if app.requestChannel != domain.ChannelSMS {
		t.Fatalf("channel = %q, want sms", app.requestChannel)
	}

	app.requestErr = application.ErrEmailOTPDisabled
	_, err := srv.RequestOTP(context.Background(), &authv1.RequestOTPRequest{Identifier: "person@example.com", Channel: "email"})
	assertCode(t, err, codes.FailedPrecondition, "email OTP is disabled")

	app.requestErr = application.ErrMobileOTPDisabled
	_, err = srv.RequestOTP(context.Background(), &authv1.RequestOTPRequest{Identifier: "+989121234567", Channel: "sms"})
	assertCode(t, err, codes.FailedPrecondition, "mobile OTP is disabled")

	app.requestErr = errors.New("notifier down")
	_, err = srv.RequestOTP(context.Background(), &authv1.RequestOTPRequest{Identifier: "person@example.com", Channel: "email"})
	if err == nil || status.Code(err) != codes.Unknown {
		t.Fatalf("unexpected non-status error mapping: %v", err)
	}
}

func TestVerifyOTPMapsTokensAndError(t *testing.T) {
	app := &fakeAuth{
		verifyAccess:  "access-1",
		verifyRefresh: "refresh-1",
		verifyExpires: time.Date(2026, 11, 2, 12, 15, 0, 0, time.UTC),
	}
	srv := New(app)
	resp, err := srv.VerifyOTP(context.Background(), &authv1.VerifyOTPRequest{
		Identifier: "person@example.com", Channel: "email", Code: "123456",
	})
	if err != nil {
		t.Fatal(err)
	}
	if app.verifyIdentifier != "person@example.com" || app.verifyChannel != "email" || app.verifyCode != "123456" {
		t.Fatalf("request = %q %q %q", app.verifyIdentifier, app.verifyChannel, app.verifyCode)
	}
	if resp.GetTokens().GetAccessToken() != "access-1" || resp.GetTokens().GetRefreshToken() != "refresh-1" ||
		resp.GetTokens().GetExpiresAt() != "2026-11-02T12:15:00Z" {
		t.Fatalf("response = %+v", resp.GetTokens())
	}

	app.verifyErr = errors.New("invalid or expired code")
	_, err = srv.VerifyOTP(context.Background(), &authv1.VerifyOTPRequest{Identifier: "person@example.com", Channel: "email", Code: "000000"})
	assertCode(t, err, codes.Unauthenticated, "invalid or expired code")
}

func TestLoginWithPasswordMapsErrors(t *testing.T) {
	app := &fakeAuth{loginAccess: "a", loginRefresh: "r", loginExpires: time.Unix(0, 0).UTC()}
	srv := New(app)

	//nolint:staticcheck // legacy field is part of the wire contract
	if _, err := srv.LoginWithPassword(context.Background(), &authv1.LoginWithPasswordRequest{Email: "person@example.com", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	if app.loginIdentifier != "person@example.com" || app.loginPassword != "pw" {
		t.Fatalf("login = %q %q", app.loginIdentifier, app.loginPassword)
	}

	app.loginErr = application.ErrInvalidCredentials
	_, err := srv.LoginWithPassword(context.Background(), &authv1.LoginWithPasswordRequest{Identifier: "person@example.com"})
	assertCode(t, err, codes.Unauthenticated, "invalid credentials")

	app.loginErr = application.ErrUserDisabled
	_, err = srv.LoginWithPassword(context.Background(), &authv1.LoginWithPasswordRequest{Identifier: "person@example.com"})
	assertCode(t, err, codes.PermissionDenied, "user disabled")
}

func TestRefreshTokenMapsError(t *testing.T) {
	app := &fakeAuth{loginAccess: "a2", loginRefresh: "r2", loginExpires: time.Unix(0, 0).UTC()}
	srv := New(app)
	if _, err := srv.RefreshToken(context.Background(), &authv1.RefreshTokenRequest{RefreshToken: "r1"}); err != nil {
		t.Fatal(err)
	}
	if app.refreshToken != "r1" {
		t.Fatalf("refresh token = %q", app.refreshToken)
	}
	app.refreshErr = errors.New("invalid refresh token")
	_, err := srv.RefreshToken(context.Background(), &authv1.RefreshTokenRequest{RefreshToken: "r1"})
	assertCode(t, err, codes.Unauthenticated, "invalid refresh token")
}

func TestCreateApiTokenParsesExpiry(t *testing.T) {
	app := &fakeAuth{createID: "t1", createPrefix: "abcdefgh", createSecret: "secret"}
	srv := New(app)
	resp, err := srv.CreateApiToken(context.Background(), &authv1.CreateApiTokenRequest{
		UserId: "u1", Name: "deploy", Scopes: []string{"read"}, ExpiresAt: "2026-11-03T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetTokenId() != "t1" || resp.GetPrefix() != "abcdefgh" || resp.GetSecret() != "secret" {
		t.Fatalf("response = %+v", resp)
	}
	if app.createUserID != "u1" || app.createName != "deploy" || len(app.createScopes) != 1 {
		t.Fatalf("request = %+v", app)
	}
	if app.createExpires == nil || app.createExpires.Format(time.RFC3339) != "2026-11-03T12:00:00Z" {
		t.Fatalf("expires = %v", app.createExpires)
	}

	_, err = srv.CreateApiToken(context.Background(), &authv1.CreateApiTokenRequest{ExpiresAt: "not-a-time"})
	assertCode(t, err, codes.InvalidArgument, "invalid expires_at")
}

func TestListApiTokensMapsRecordsAndKeepsEmptyNonNil(t *testing.T) {
	created := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	expires := created.Add(time.Hour)
	app := &fakeAuth{listTokens: []domain.APITokenInfo{{
		ID: "t1", Name: "deploy", Prefix: "abcdefgh", Scopes: []string{"read"}, ExpiresAt: &expires, CreatedAt: created,
	}}}
	srv := New(app)
	resp, err := srv.ListApiTokens(context.Background(), &authv1.ListApiTokensRequest{UserId: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if app.listUserID != "u1" || len(resp.GetTokens()) != 1 {
		t.Fatalf("list = %+v", resp)
	}
	got := resp.GetTokens()[0]
	if got.GetTokenId() != "t1" || got.GetName() != "deploy" || got.GetPrefix() != "abcdefgh" ||
		got.GetCreatedAt() != "2026-11-02T12:00:00Z" || got.GetExpiresAt() != "2026-11-02T13:00:00Z" {
		t.Fatalf("token = %+v", got)
	}

	app.listTokens = nil
	resp, err = srv.ListApiTokens(context.Background(), &authv1.ListApiTokensRequest{UserId: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetTokens() == nil {
		t.Fatal("empty list must stay an empty slice, as before")
	}
}

func TestRevokeApiTokenAndValidate(t *testing.T) {
	app := &fakeAuth{validateUserID: "u1", validateScopes: []string{"read"}}
	srv := New(app)
	if _, err := srv.RevokeApiToken(context.Background(), &authv1.RevokeApiTokenRequest{UserId: "u1", TokenId: "t1"}); err != nil {
		t.Fatal(err)
	}
	if app.revokeUserID != "u1" || app.revokeTokenID != "t1" {
		t.Fatalf("revoke = %q %q", app.revokeUserID, app.revokeTokenID)
	}

	resp, err := srv.ValidateApiToken(context.Background(), &authv1.ValidateApiTokenRequest{Secret: "oat_abcdefgh.tail"})
	if err != nil {
		t.Fatal(err)
	}
	if app.validateSecret != "oat_abcdefgh.tail" || resp.GetUserId() != "u1" || len(resp.GetScopes()) != 1 {
		t.Fatalf("validate = %+v", resp)
	}

	app.validateErr = application.ErrInvalidToken
	_, err = srv.ValidateApiToken(context.Background(), &authv1.ValidateApiTokenRequest{Secret: "bad"})
	assertCode(t, err, codes.Unauthenticated, "invalid token")
}

func TestResolveIdentifierChannel(t *testing.T) {
	tests := []struct {
		identifier, channel, legacyEmail string
		wantIdentifier, wantChannel      string
	}{
		{"person@example.com", "", "", "person@example.com", domain.ChannelEmail},
		{"+989121234567", "", "", "+989121234567", domain.ChannelSMS},
		{"", "", "legacy@example.com", "legacy@example.com", domain.ChannelEmail},
		{"person@example.com", "sms", "", "person@example.com", "sms"},
	}
	for _, tc := range tests {
		identifier, channel := resolveIdentifierChannel(tc.identifier, tc.channel, tc.legacyEmail)
		if identifier != tc.wantIdentifier || channel != tc.wantChannel {
			t.Fatalf("resolve(%q,%q,%q) = %q,%q, want %q,%q", tc.identifier, tc.channel, tc.legacyEmail, identifier, channel, tc.wantIdentifier, tc.wantChannel)
		}
	}
}
