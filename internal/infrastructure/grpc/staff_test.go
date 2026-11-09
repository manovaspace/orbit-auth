package grpc

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	authv1 "github.com/manovaspace/orbit-auth/api/auth/v1"
	"github.com/manovaspace/orbit-auth/internal/application"
	"github.com/manovaspace/orbit-auth/internal/domain"
	googlegrpc "google.golang.org/grpc"
)

const (
	staffTestToken       = "staff-service-token-for-tests"
	staffWriteMethod     = "/auth.v1.StaffOrgService/SuspendOrganization"
	staffReadMethod      = "/auth.v1.StaffOrgService/ListOrganizations"
	staffGetMethod       = "/auth.v1.StaffOrgService/GetOrganization"
	staffUnknownMethod   = "/auth.v1.StaffOrgService/SomeFutureWrite"
	staffOrgServiceToken = "x-orbit-service-token"
)

// staffMD builds the incoming metadata the gateway sends.
func staffMD(token, subject, reason string) context.Context {
	pairs := []string{}
	if token != "" {
		pairs = append(pairs, staffOrgServiceToken, token)
	}
	if subject != "" {
		pairs = append(pairs, "x-staff-subject", subject)
	}
	if reason != "" {
		pairs = append(pairs, "x-staff-reason", reason)
	}
	if len(pairs) == 0 {
		return context.Background()
	}
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(pairs...))
}

// runStaffInterceptor calls the interceptor and reports whether the handler ran
// and which staff actor it saw.
func runStaffInterceptor(t *testing.T, configured string, ctx context.Context, method string) (domain.StaffActor, bool, error) {
	t.Helper()
	interceptor := StaffServiceUnaryServerInterceptor(configured)
	var actor domain.StaffActor
	ran := false
	_, err := interceptor(ctx, &authv1.ListOrganizationsRequest{}, &googlegrpc.UnaryServerInfo{FullMethod: method},
		func(ctx context.Context, _ any) (any, error) {
			ran = true
			got, ok := staffActorFromContext(ctx)
			if !ok {
				t.Fatal("handler ran without a staff actor in the context")
			}
			actor = got
			return "ok", nil
		})
	return actor, ran, err
}

func TestStaffInterceptorFailsClosedWithoutConfiguredToken(t *testing.T) {
	for _, ctx := range []context.Context{
		staffMD("", "", ""),
		staffMD(staffTestToken, "staff-1", "reason"),
	} {
		_, ran, err := runStaffInterceptor(t, "", ctx, staffWriteMethod)
		assertCode(t, err, codes.Unavailable, "staff service disabled")
		if ran {
			t.Fatal("handler must not run when the staff service is disabled")
		}
	}
}

func TestStaffInterceptorRefusesMissingAndWrongToken(t *testing.T) {
	for _, ctx := range []context.Context{
		staffMD("", "staff-1", "reason"),
		staffMD("wrong-token", "staff-1", "reason"),
		staffMD(staffTestToken+"-extra", "staff-1", "reason"),
	} {
		_, ran, err := runStaffInterceptor(t, staffTestToken, ctx, staffWriteMethod)
		assertCode(t, err, codes.Unauthenticated, "invalid service token")
		if ran {
			t.Fatal("handler must not run with a wrong service token")
		}
	}
}

func TestStaffInterceptorRequiresSubject(t *testing.T) {
	_, ran, err := runStaffInterceptor(t, staffTestToken, staffMD(staffTestToken, "  ", "reason"), staffWriteMethod)
	assertCode(t, err, codes.InvalidArgument, "staff subject required")
	if ran {
		t.Fatal("handler must not run without a staff subject")
	}
	_, _, err = runStaffInterceptor(t, staffTestToken, staffMD(staffTestToken, "", "reason"), staffReadMethod)
	assertCode(t, err, codes.InvalidArgument, "staff subject required")
}

func TestStaffInterceptorRequiresReasonForWrites(t *testing.T) {
	for _, method := range []string{staffWriteMethod, staffUnknownMethod} {
		_, ran, err := runStaffInterceptor(t, staffTestToken, staffMD(staffTestToken, "staff-1", "   "), method)
		assertCode(t, err, codes.InvalidArgument, "staff reason required")
		if ran {
			t.Fatalf("%s ran without a reason", method)
		}
	}
}

func TestStaffInterceptorReadsNeedNoReasonAndWritesCarryTheActor(t *testing.T) {
	actor, ran, err := runStaffInterceptor(t, staffTestToken, staffMD(staffTestToken, "staff-1", ""), staffReadMethod)
	if err != nil || !ran {
		t.Fatalf("read without reason: ran=%v err=%v", ran, err)
	}
	if actor.Subject != "staff-1" || actor.Reason != "" {
		t.Fatalf("read actor = %+v", actor)
	}
	actor, ran, err = runStaffInterceptor(t, staffTestToken, staffMD(staffTestToken, " staff-2 ", " ticket 42 "), staffWriteMethod)
	if err != nil || !ran {
		t.Fatalf("write with reason: ran=%v err=%v", ran, err)
	}
	if actor.Subject != "staff-2" || actor.Reason != "ticket 42" {
		t.Fatalf("write actor = %+v", actor)
	}
	_, ran, err = runStaffInterceptor(t, staffTestToken, staffMD(staffTestToken, "staff-1", "x"), staffGetMethod)
	if err != nil || !ran {
		t.Fatalf("get: ran=%v err=%v", ran, err)
	}
}

func TestStaffInterceptorLeavesOrgAndAuthServicesAlone(t *testing.T) {
	for _, method := range []string{
		"/auth.v1.OrgService/SuspendMember",
		"/auth.v1.AuthService/RequestOTP",
		"/auth.v1.TenantService/ResolveTenant",
	} {
		interceptor := StaffServiceUnaryServerInterceptor("")
		ran := false
		_, err := interceptor(context.Background(), nil, &googlegrpc.UnaryServerInfo{FullMethod: method},
			func(context.Context, any) (any, error) {
				ran = true
				return nil, nil
			})
		if err != nil || !ran {
			t.Fatalf("%s was guarded by the staff interceptor: ran=%v err=%v", method, ran, err)
		}
	}
}

// fakeStaff records the staff calls and returns preset results.
type fakeStaff struct {
	err        error
	actor      domain.StaffActor
	orgID      string
	input      application.ProvisionOrgInput
	target     domain.InviteTarget
	page       application.StaffOrgPage
	provResult application.ProvisionOrgResult
	invite     domain.Invite
	token      string
	summary    domain.StaffOrgSummary
}

func (f *fakeStaff) StaffListOrganizations(_ context.Context, in application.StaffListInput) (application.StaffOrgPage, error) {
	f.input = application.ProvisionOrgInput{Name: in.Query, Slug: in.Status}
	return f.page, f.err
}

func (f *fakeStaff) StaffGetOrganization(_ context.Context, orgID string) (application.StaffOrgDetail, error) {
	f.orgID = orgID
	return application.StaffOrgDetail{Summary: f.summary}, f.err
}

func (f *fakeStaff) StaffProvisionOrganization(_ context.Context, actor domain.StaffActor, in application.ProvisionOrgInput) (application.ProvisionOrgResult, error) {
	f.actor = actor
	f.input = in
	return f.provResult, f.err
}

func (f *fakeStaff) StaffSuspendOrganization(_ context.Context, actor domain.StaffActor, orgID string) (domain.StaffOrgSummary, error) {
	f.actor, f.orgID = actor, orgID
	return f.summary, f.err
}

func (f *fakeStaff) StaffReactivateOrganization(_ context.Context, actor domain.StaffActor, orgID string) (domain.StaffOrgSummary, error) {
	f.actor, f.orgID = actor, orgID
	return f.summary, f.err
}

func (f *fakeStaff) StaffSetOrganizationSlug(_ context.Context, actor domain.StaffActor, orgID, slug string) (domain.StaffOrgSummary, error) {
	f.actor, f.orgID = actor, orgID
	f.input = application.ProvisionOrgInput{Slug: slug}
	return f.summary, f.err
}

func (f *fakeStaff) StaffReinviteOwner(_ context.Context, actor domain.StaffActor, orgID string, target domain.InviteTarget) (domain.Invite, string, error) {
	f.actor, f.orgID, f.target = actor, orgID, target
	return f.invite, f.token, f.err
}

// staffActorCtx is the context the interceptor produces for a write.
func staffActorCtx(actor domain.StaffActor) context.Context {
	return withStaffActor(context.Background(), actor)
}

func TestStaffServerMapsProvisionAndRequiresActor(t *testing.T) {
	orgID := uuid.NewString()
	app := &fakeStaff{provResult: application.ProvisionOrgResult{
		Summary: domain.StaffOrgSummary{Organization: domain.Organization{ID: orgID, Name: "Acme", Slug: "acme", Status: domain.OrgStatusActive}},
		Invite:  domain.Invite{ID: uuid.NewString(), Phone: "+989121234567", Role: domain.RoleOwner, ExpiresAt: time.Unix(0, 0).UTC()},
		Token:   "raw-owner-token",
	}}
	srv := NewStaffOrg(app)

	if _, err := srv.ProvisionOrganization(context.Background(), &authv1.ProvisionOrganizationRequest{Name: "Acme"}); err == nil {
		t.Fatal("provisioning without a verified staff actor must fail")
	} else {
		assertCode(t, err, codes.Unauthenticated, "staff authentication required")
	}

	actor := domain.StaffActor{Subject: "staff-1", Reason: "onboarding"}
	resp, err := srv.ProvisionOrganization(staffActorCtx(actor), &authv1.ProvisionOrganizationRequest{
		Name: "Acme", IdempotencyKey: "k", OwnerPhone: "+989121234567",
	})
	if err != nil {
		t.Fatal(err)
	}
	if app.actor != actor || app.input.Target.Phone != "+989121234567" || app.input.IdempotencyKey != "k" {
		t.Fatalf("service received actor %+v input %+v", app.actor, app.input)
	}
	if resp.GetToken() != "raw-owner-token" || resp.GetOrganization().GetId() != orgID || resp.GetOwnerInvite().GetPhone() != "+989121234567" {
		t.Fatalf("response = %+v", resp)
	}
	if resp.GetOrganization().GetStatus() != "active" || len(resp.GetOrganization().GetOwnerUserIds()) != 0 {
		t.Fatalf("organization = %+v", resp.GetOrganization())
	}

	app.err = domain.ErrIdempotencyConflict
	_, err = srv.ProvisionOrganization(staffActorCtx(actor), &authv1.ProvisionOrganizationRequest{Name: "Other", IdempotencyKey: "k", OwnerPhone: "+989121234567"})
	assertCode(t, err, codes.AlreadyExists, domain.ErrIdempotencyConflict.Error())

	app.err = domain.ErrIdempotencyKeyRequired
	_, err = srv.ProvisionOrganization(staffActorCtx(actor), &authv1.ProvisionOrganizationRequest{Name: "Other", OwnerPhone: "+989121234567"})
	assertCode(t, err, codes.InvalidArgument, domain.ErrIdempotencyKeyRequired.Error())
}

func TestStaffServerMapsListAndRejectsBadIDs(t *testing.T) {
	app := &fakeStaff{}
	srv := NewStaffOrg(app)

	app.err = domain.ErrInvalidPageToken
	_, err := srv.ListOrganizations(context.Background(), &authv1.ListOrganizationsRequest{PageToken: "x"})
	assertCode(t, err, codes.InvalidArgument, domain.ErrInvalidPageToken.Error())

	app.err = domain.ErrInvalidOrgStatus
	_, err = srv.ListOrganizations(context.Background(), &authv1.ListOrganizationsRequest{Status: "archived"})
	assertCode(t, err, codes.InvalidArgument, domain.ErrInvalidOrgStatus.Error())

	app.err = nil
	_, err = srv.GetOrganization(context.Background(), &authv1.GetOrganizationRequest{OrgId: "not-a-uuid"})
	assertCode(t, err, codes.InvalidArgument, "invalid org_id")

	actor := domain.StaffActor{Subject: "staff-1", Reason: "r"}
	_, err = srv.SuspendOrganization(staffActorCtx(actor), &authv1.SuspendOrganizationRequest{OrgId: "nope"})
	assertCode(t, err, codes.InvalidArgument, "invalid org_id")

	app.err = domain.ErrOrgNotFound
	_, err = srv.SuspendOrganization(staffActorCtx(actor), &authv1.SuspendOrganizationRequest{OrgId: uuid.NewString()})
	assertCode(t, err, codes.NotFound, "organization not found")
}

func TestStaffServerNeverReturnsTokensForEmailOwners(t *testing.T) {
	app := &fakeStaff{invite: domain.Invite{ID: uuid.NewString(), Email: "owner@example.com", Role: domain.RoleOwner}, token: ""}
	srv := NewStaffOrg(app)
	actor := domain.StaffActor{Subject: "staff-1", Reason: "r"}
	resp, err := srv.ReinviteOwner(staffActorCtx(actor), &authv1.ReinviteOwnerRequest{OrgId: uuid.NewString(), OwnerEmail: "owner@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetToken() != "" || resp.GetInvite().GetEmail() != "owner@example.com" || resp.GetInvite().GetRole() != "owner" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestOrgErrorMapsSuspendedOrganization(t *testing.T) {
	assertCode(t, orgError(domain.ErrOrgSuspended), codes.FailedPrecondition, "organization suspended")
}

func TestRefreshTokenMapsSuspendedOrganization(t *testing.T) {
	app := &fakeAuth{refreshErr: domain.ErrOrgSuspended}
	_, err := New(app).RefreshToken(context.Background(), &authv1.RefreshTokenRequest{RefreshToken: "r1"})
	assertCode(t, err, codes.FailedPrecondition, "organization suspended")
}

func TestTenantServiceReportsSuspendedFlag(t *testing.T) {
	app := &fakeOrgService{resolved: domain.Organization{ID: uuid.NewString(), Name: "Acme", Slug: "acme", Status: domain.OrgStatusSuspended}}
	resp, err := NewTenant(app).ResolveTenant(context.Background(), &authv1.ResolveTenantRequest{Slug: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.GetSuspended() {
		t.Fatal("a suspended organization must resolve with suspended set")
	}
}
