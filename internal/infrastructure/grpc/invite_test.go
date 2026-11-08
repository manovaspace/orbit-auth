package grpc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	authv1 "github.com/manovaspace/orbit-auth/api/auth/v1"
	"github.com/manovaspace/orbit-auth/internal/application"
	"github.com/manovaspace/orbit-auth/internal/domain"
)

type fakeInviteService struct {
	preview    domain.InvitePreview
	previewErr error
	previewTok string

	acceptToken    string
	acceptPassword string
	acceptName     string
	acceptUserID   string
	acceptOrgID    string
	acceptErr      error
}

func (f *fakeInviteService) GetInvitePreview(_ context.Context, token string) (domain.InvitePreview, error) {
	f.previewTok = token
	return f.preview, f.previewErr
}

func (f *fakeInviteService) AcceptInviteWithSignup(_ context.Context, token, password, displayName string) (string, string, error) {
	f.acceptToken, f.acceptPassword, f.acceptName = token, password, displayName
	return f.acceptUserID, f.acceptOrgID, f.acceptErr
}

func TestInviteMemberPhoneInviteMapsRoleAndReturnsToken(t *testing.T) {
	orgID := uuid.NewString()
	app := &fakeOrgService{active: true, inviteToken: "raw-token", inviteResult: domain.Invite{
		ID: uuid.NewString(), OrgID: orgID, Phone: "+989121234567", Role: domain.RoleAdmin, Status: domain.InvitePending,
		ExpiresAt: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC),
	}}
	srv := NewOrg(app)

	resp, err := srv.InviteMember(authenticatedOrgContext("caller-1"), &authv1.InviteMemberRequest{
		OrgId: orgID, Phone: "+989121234567", Role: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if app.invitePhone != "+989121234567" || app.inviteEmail != "" || app.inviteRole != domain.RoleAdmin {
		t.Fatalf("target = phone %q email %q role %q", app.invitePhone, app.inviteEmail, app.inviteRole)
	}
	if resp.GetToken() != "raw-token" {
		t.Fatalf("token = %q, want the raw token for a phone invite", resp.GetToken())
	}
	if resp.GetInvite().GetPhone() != "+989121234567" || resp.GetInvite().GetRole() != "admin" {
		t.Fatalf("invite = %+v", resp.GetInvite())
	}
}

func TestInviteMemberEmailInviteNeverReturnsToken(t *testing.T) {
	orgID := uuid.NewString()
	app := &fakeOrgService{active: true, inviteToken: "raw-token", inviteResult: domain.Invite{
		ID: uuid.NewString(), OrgID: orgID, Email: "member@example.com", Role: domain.RoleMember,
	}}
	srv := NewOrg(app)

	resp, err := srv.InviteMember(authenticatedOrgContext("caller-1"), &authv1.InviteMemberRequest{
		OrgId: orgID, Email: "member@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetToken() != "" {
		t.Fatal("an email invitation returned the raw token to the inviter")
	}
	if app.inviteRole != domain.RoleMember {
		t.Fatalf("empty role mapped to %q, want member", app.inviteRole)
	}
}

func TestInviteMemberInviteErrorCodes(t *testing.T) {
	orgID := uuid.NewString()
	app := &fakeOrgService{active: true}
	srv := NewOrg(app)
	ctx := authenticatedOrgContext("caller-1")

	_, err := srv.InviteMember(ctx, &authv1.InviteMemberRequest{OrgId: orgID, Role: "owner-ish"})
	assertCode(t, err, codes.InvalidArgument, domain.ErrInvalidRole.Error())
	if app.inviteOrgID != "" {
		t.Fatal("an invalid role reached the use case")
	}

	app.inviteErr = domain.ErrInvalidPhone
	_, err = srv.InviteMember(ctx, &authv1.InviteMemberRequest{OrgId: orgID, Phone: "0912"})
	assertCode(t, err, codes.InvalidArgument, domain.ErrInvalidPhone.Error())

	app.inviteErr = domain.ErrInvalidInviteTarget
	_, err = srv.InviteMember(ctx, &authv1.InviteMemberRequest{OrgId: orgID, Email: "a@example.com", Phone: "+989121234567"})
	assertCode(t, err, codes.InvalidArgument, domain.ErrInvalidInviteTarget.Error())

	app.inviteErr = domain.ErrForbidden
	_, err = srv.InviteMember(ctx, &authv1.InviteMemberRequest{OrgId: orgID, Phone: "+989121234567"})
	assertCode(t, err, codes.PermissionDenied, domain.ErrForbidden.Error())
}

func TestInviteServicePreviewMapsGrantAndHidesRecipient(t *testing.T) {
	expires := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	app := &fakeInviteService{preview: domain.InvitePreview{
		OrgID: "org-1", OrgName: "Acme", OrgSlug: "acme", Role: domain.RoleAdmin,
		Status: domain.InvitePending, ExpiresAt: expires,
	}}
	srv := NewInvite(app)

	resp, err := srv.GetInvitePreview(context.Background(), &authv1.GetInvitePreviewRequest{Token: "raw-token"})
	if err != nil {
		t.Fatal(err)
	}
	if app.previewTok != "raw-token" {
		t.Fatalf("token = %q", app.previewTok)
	}
	if resp.GetOrgId() != "org-1" || resp.GetOrgName() != "Acme" || resp.GetOrgSlug() != "acme" ||
		resp.GetRole() != "admin" || resp.GetExpiresAt() != expires.Format(time.RFC3339) {
		t.Fatalf("preview = %+v", resp)
	}

	fields := resp.ProtoReflect().Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		name := string(fields.Get(i).Name())
		if strings.Contains(name, "email") || strings.Contains(name, "phone") || strings.Contains(name, "mobile") {
			t.Fatalf("InvitePreview exposes recipient field %q", name)
		}
	}
}

func TestInviteServicePreviewErrorCodes(t *testing.T) {
	app := &fakeInviteService{previewErr: domain.ErrInvalidInvite}
	srv := NewInvite(app)

	_, err := srv.GetInvitePreview(context.Background(), &authv1.GetInvitePreviewRequest{Token: "bad"})
	assertCode(t, err, codes.NotFound, "invite not found")

	app.previewErr = errors.New("database down")
	_, err = srv.GetInvitePreview(context.Background(), &authv1.GetInvitePreviewRequest{Token: "bad"})
	assertCode(t, err, codes.Internal, "internal error")
}

func TestInviteServiceAcceptWithSignupReturnsIDsAndMapsErrors(t *testing.T) {
	app := &fakeInviteService{acceptUserID: "user-1", acceptOrgID: "org-1"}
	srv := NewInvite(app)

	resp, err := srv.AcceptInviteWithSignup(context.Background(), &authv1.AcceptInviteWithSignupRequest{
		Token: "raw-token", Password: "correct horse", DisplayName: "Sara",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetUserId() != "user-1" || resp.GetOrgId() != "org-1" {
		t.Fatalf("response = %+v", resp)
	}
	if app.acceptToken != "raw-token" || app.acceptPassword != "correct horse" || app.acceptName != "Sara" {
		t.Fatalf("forwarded = %q %q %q", app.acceptToken, app.acceptPassword, app.acceptName)
	}

	cases := []struct {
		err     error
		code    codes.Code
		message string
	}{
		{application.ErrAccountExists, codes.AlreadyExists, "account exists: sign in to accept the invitation"},
		{application.ErrInviteSignupDisabled, codes.FailedPrecondition, "invite sign-up is disabled"},
		{application.ErrPasswordRequired, codes.InvalidArgument, application.ErrPasswordRequired.Error()},
		{application.ErrPasswordTooShort, codes.InvalidArgument, "password must be at least 8 characters"},
		{domain.ErrInvalidInvite, codes.NotFound, "invite not found"},
		{errors.New("database down"), codes.Internal, "internal error"},
	}
	for _, tc := range cases {
		app.acceptErr = tc.err
		_, err := srv.AcceptInviteWithSignup(context.Background(), &authv1.AcceptInviteWithSignupRequest{Token: "t"})
		assertCode(t, err, tc.code, tc.message)
	}
}

func TestInviteServiceIsPublicToTheInterceptor(t *testing.T) {
	keys, _ := orgTestKeys(t)
	interceptor := AccessTokenUnaryServerInterceptor(keys)
	for _, method := range []string{
		"/auth.v1.InviteService/GetInvitePreview",
		"/auth.v1.InviteService/AcceptInviteWithSignup",
	} {
		called := false
		handler := func(context.Context, any) (any, error) {
			called = true
			return nil, nil
		}
		info := &grpc.UnaryServerInfo{FullMethod: method}
		if _, err := interceptor(context.Background(), nil, info, handler); err != nil {
			t.Fatalf("%s without a token: %v", method, err)
		}
		if !called {
			t.Fatalf("%s did not reach its handler without a token", method)
		}
	}
}
