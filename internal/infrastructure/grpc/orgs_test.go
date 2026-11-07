package grpc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	authv1 "github.com/manovaspace/orbit-auth/api/auth/v1"
	"github.com/manovaspace/orbit-auth/internal/application"
	"github.com/manovaspace/orbit-auth/internal/domain"
	orbittoken "github.com/manovaspace/orbit-auth/token"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type fakeOrgService struct {
	createCreator string
	createName    string
	createSlug    string
	created       domain.Organization
	createErr     error

	slugOrgID  string
	slugCaller string
	slugValue  string
	slugResult domain.Organization
	slugErr    error

	resolveSlug string
	resolved    domain.Organization
	resolveErr  error

	listUserID  string
	orgs        []domain.Organization
	listOrgsErr error

	listMembersOrgID string
	members          []domain.Membership
	listMembersErr   error

	activeCalls []activeCall
	active      bool
	activeErr   error

	addOrgID  string
	addCaller string
	addUserID string
	addErr    error

	removeOrgID  string
	removeCaller string
	removeUserID string
	removeErr    error

	suspendOrgID  string
	suspendCaller string
	suspendUserID string
	suspendErr    error

	roleOrgID  string
	roleCaller string
	roleUserID string
	roleValue  domain.MembershipRole
	roleResult domain.Membership
	roleErr    error

	selectCalls   []selectCall
	selectAccess  string
	selectRefresh string
	selectExpires time.Time
	selectErr     error

	inviteOrgID   string
	inviteInviter string
	inviteEmail   string
	inviteResult  domain.Invite
	inviteErr     error

	acceptUserID string
	acceptToken  string
	acceptResult domain.Invite
	acceptErr    error

	revokeInviteOrgID  string
	revokeInviteID     string
	revokeInviteCaller string
	revokeInviteErr    error

	listInvitesOrgID  string
	listInvitesCaller string
	invites           []domain.Invite
	listInvitesErr    error
}

type selectCall struct {
	userID       string
	refreshToken string
	orgID        string
}

type activeCall struct {
	orgID  string
	userID string
}

func (f *fakeOrgService) CreateOrg(_ context.Context, creatorID, name, slug string) (domain.Organization, error) {
	f.createCreator, f.createName, f.createSlug = creatorID, name, slug
	return f.created, f.createErr
}

func (f *fakeOrgService) SetOrgSlug(_ context.Context, orgID, callerID, slug string) (domain.Organization, error) {
	f.slugOrgID, f.slugCaller, f.slugValue = orgID, callerID, slug
	return f.slugResult, f.slugErr
}

func (f *fakeOrgService) ResolveOrg(_ context.Context, slug string) (domain.Organization, error) {
	f.resolveSlug = slug
	return f.resolved, f.resolveErr
}

func (f *fakeOrgService) ListOrgsForUser(_ context.Context, userID string) ([]domain.Organization, error) {
	f.listUserID = userID
	return f.orgs, f.listOrgsErr
}

func (f *fakeOrgService) ListMembers(_ context.Context, orgID string) ([]domain.Membership, error) {
	f.listMembersOrgID = orgID
	return f.members, f.listMembersErr
}

func (f *fakeOrgService) AddMember(_ context.Context, orgID, callerID, userID string) error {
	f.addOrgID, f.addCaller, f.addUserID = orgID, callerID, userID
	return f.addErr
}

func (f *fakeOrgService) RemoveMember(_ context.Context, orgID, callerID, userID string) error {
	f.removeOrgID, f.removeCaller, f.removeUserID = orgID, callerID, userID
	return f.removeErr
}

func (f *fakeOrgService) SuspendMember(_ context.Context, orgID, callerID, userID string) error {
	f.suspendOrgID, f.suspendCaller, f.suspendUserID = orgID, callerID, userID
	return f.suspendErr
}

func (f *fakeOrgService) SetMemberRole(_ context.Context, orgID, callerID, userID string, role domain.MembershipRole) (domain.Membership, error) {
	f.roleOrgID, f.roleCaller, f.roleUserID, f.roleValue = orgID, callerID, userID, role
	return f.roleResult, f.roleErr
}

func (f *fakeOrgService) IsActiveMember(_ context.Context, orgID, userID string) (bool, error) {
	f.activeCalls = append(f.activeCalls, activeCall{orgID: orgID, userID: userID})
	return f.active, f.activeErr
}

func (f *fakeOrgService) SelectOrg(_ context.Context, userID, refreshToken, orgID string) (string, string, time.Time, error) {
	f.selectCalls = append(f.selectCalls, selectCall{userID: userID, refreshToken: refreshToken, orgID: orgID})
	return f.selectAccess, f.selectRefresh, f.selectExpires, f.selectErr
}

func (f *fakeOrgService) InviteMember(_ context.Context, orgID, inviterID, email string) (domain.Invite, error) {
	f.inviteOrgID, f.inviteInviter, f.inviteEmail = orgID, inviterID, email
	return f.inviteResult, f.inviteErr
}

func (f *fakeOrgService) AcceptInvite(_ context.Context, userID, token string) (domain.Invite, error) {
	f.acceptUserID, f.acceptToken = userID, token
	return f.acceptResult, f.acceptErr
}

func (f *fakeOrgService) RevokeInvite(_ context.Context, orgID, inviteID, callerID string) error {
	f.revokeInviteOrgID, f.revokeInviteID, f.revokeInviteCaller = orgID, inviteID, callerID
	return f.revokeInviteErr
}

func (f *fakeOrgService) ListInvites(_ context.Context, orgID, callerID string) ([]domain.Invite, error) {
	f.listInvitesOrgID, f.listInvitesCaller = orgID, callerID
	return f.invites, f.listInvitesErr
}

func authenticatedOrgContext(callerID string) context.Context {
	return withCallerID(context.Background(), callerID)
}

func TestOrgServiceCreateOrgUsesTokenCaller(t *testing.T) {
	created := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	app := &fakeOrgService{created: domain.Organization{
		ID: uuid.NewString(), Name: "Acme", CreatedAt: created, UpdatedAt: created,
	}}
	srv := NewOrg(app)

	resp, err := srv.CreateOrg(authenticatedOrgContext("caller-1"), &authv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	if app.createCreator != "caller-1" || app.createName != "Acme" {
		t.Fatalf("create = %q %q", app.createCreator, app.createName)
	}
	if resp.GetOrganization().GetId() != app.created.ID || resp.GetOrganization().GetName() != "Acme" ||
		resp.GetOrganization().GetCreatedAt() != "2026-11-02T12:00:00Z" {
		t.Fatalf("response = %+v", resp.GetOrganization())
	}

	app.createErr = domain.ErrInvalidOrgName
	_, err = srv.CreateOrg(authenticatedOrgContext("caller-1"), &authv1.CreateOrgRequest{})
	assertCode(t, err, codes.InvalidArgument, domain.ErrInvalidOrgName.Error())
}

func TestOrgServiceListMyOrgsUsesTokenCaller(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	app := &fakeOrgService{orgs: []domain.Organization{
		{ID: uuid.NewString(), Name: "One", CreatedAt: now, UpdatedAt: now},
		{ID: uuid.NewString(), Name: "Two", CreatedAt: now, UpdatedAt: now},
	}}
	srv := NewOrg(app)

	resp, err := srv.ListMyOrgs(authenticatedOrgContext("caller-1"), &authv1.ListMyOrgsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if app.listUserID != "caller-1" {
		t.Fatalf("list user = %q, want caller-1", app.listUserID)
	}
	if len(resp.GetOrganizations()) != 2 || resp.GetOrganizations()[0].GetName() != "One" {
		t.Fatalf("response = %+v", resp.GetOrganizations())
	}

	app.orgs = nil
	resp, err = srv.ListMyOrgs(authenticatedOrgContext("caller-1"), &authv1.ListMyOrgsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetOrganizations() == nil {
		t.Fatal("empty list must stay an empty slice")
	}
}

func TestOrgServiceListMembersGatesOnActiveMembership(t *testing.T) {
	orgID := uuid.NewString()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	app := &fakeOrgService{
		active: true,
		members: []domain.Membership{
			{OrgID: orgID, UserID: uuid.NewString(), Status: domain.MembershipActive, CreatedAt: now, UpdatedAt: now},
			{OrgID: orgID, UserID: uuid.NewString(), Status: domain.MembershipSuspended, CreatedAt: now, UpdatedAt: now},
		},
	}
	srv := NewOrg(app)

	resp, err := srv.ListMembers(authenticatedOrgContext("caller-1"), &authv1.ListMembersRequest{OrgId: orgID})
	if err != nil {
		t.Fatal(err)
	}
	if len(app.activeCalls) != 1 || app.activeCalls[0] != (activeCall{orgID: orgID, userID: "caller-1"}) {
		t.Fatalf("active calls = %+v", app.activeCalls)
	}
	if app.listMembersOrgID != orgID {
		t.Fatalf("list org = %q, want %q", app.listMembersOrgID, orgID)
	}
	if len(resp.GetMembers()) != 2 || resp.GetMembers()[1].GetStatus() != string(domain.MembershipSuspended) {
		t.Fatalf("response = %+v", resp.GetMembers())
	}
}

func TestOrgServiceAddRemoveAndSuspendMember(t *testing.T) {
	orgID := uuid.NewString()
	targetID := uuid.NewString()
	app := &fakeOrgService{active: true}
	srv := NewOrg(app)
	ctx := authenticatedOrgContext("caller-1")

	if _, err := srv.AddMember(ctx, &authv1.AddMemberRequest{OrgId: orgID, UserId: targetID}); err != nil {
		t.Fatal(err)
	}
	if app.addOrgID != orgID || app.addUserID != targetID {
		t.Fatalf("add = %q %q", app.addOrgID, app.addUserID)
	}

	if _, err := srv.RemoveMember(ctx, &authv1.RemoveMemberRequest{OrgId: orgID, UserId: targetID}); err != nil {
		t.Fatal(err)
	}
	if app.removeOrgID != orgID || app.removeUserID != targetID {
		t.Fatalf("remove = %q %q", app.removeOrgID, app.removeUserID)
	}

	if _, err := srv.SuspendMember(ctx, &authv1.SuspendMemberRequest{OrgId: orgID, UserId: targetID}); err != nil {
		t.Fatal(err)
	}
	if app.suspendOrgID != orgID || app.suspendUserID != targetID {
		t.Fatalf("suspend = %q %q", app.suspendOrgID, app.suspendUserID)
	}
}

func TestOrgServiceUnauthenticated(t *testing.T) {
	orgID := uuid.NewString()
	userID := uuid.NewString()
	app := &fakeOrgService{active: true}
	srv := NewOrg(app)
	ctx := context.Background()

	calls := []func() error{
		func() error { _, err := srv.CreateOrg(ctx, &authv1.CreateOrgRequest{Name: "Acme"}); return err },
		func() error { _, err := srv.ListMyOrgs(ctx, &authv1.ListMyOrgsRequest{}); return err },
		func() error { _, err := srv.ListMembers(ctx, &authv1.ListMembersRequest{OrgId: orgID}); return err },
		func() error {
			_, err := srv.AddMember(ctx, &authv1.AddMemberRequest{OrgId: orgID, UserId: userID})
			return err
		},
		func() error {
			_, err := srv.RemoveMember(ctx, &authv1.RemoveMemberRequest{OrgId: orgID, UserId: userID})
			return err
		},
		func() error {
			_, err := srv.SuspendMember(ctx, &authv1.SuspendMemberRequest{OrgId: orgID, UserId: userID})
			return err
		},
		func() error {
			_, err := srv.InviteMember(ctx, &authv1.InviteMemberRequest{OrgId: orgID, Email: "member@example.com"})
			return err
		},
		func() error {
			_, err := srv.AcceptInvite(ctx, &authv1.AcceptInviteRequest{Token: "invite-token"})
			return err
		},
		func() error {
			_, err := srv.RevokeInvite(ctx, &authv1.RevokeInviteRequest{OrgId: orgID, InviteId: userID})
			return err
		},
		func() error {
			_, err := srv.ListInvites(ctx, &authv1.ListInvitesRequest{OrgId: orgID})
			return err
		},
		func() error {
			_, err := srv.SelectOrg(ctx, &authv1.SelectOrgRequest{OrgId: orgID, RefreshToken: "r1"})
			return err
		},
	}
	for _, call := range calls {
		assertCode(t, call(), codes.Unauthenticated, "authentication required")
	}
	if len(app.activeCalls) != 0 || app.listMembersOrgID != "" || len(app.selectCalls) != 0 {
		t.Fatal("an unauthenticated call reached the service")
	}
}

func TestOrgServiceNonMemberGetsNotFound(t *testing.T) {
	orgID := uuid.NewString()
	userID := uuid.NewString()
	app := &fakeOrgService{active: false}
	srv := NewOrg(app)
	ctx := authenticatedOrgContext("stranger")

	_, err := srv.ListMembers(ctx, &authv1.ListMembersRequest{OrgId: orgID})
	assertCode(t, err, codes.NotFound, "organization not found")
	_, err = srv.AddMember(ctx, &authv1.AddMemberRequest{OrgId: orgID, UserId: userID})
	assertCode(t, err, codes.NotFound, "organization not found")
	_, err = srv.RemoveMember(ctx, &authv1.RemoveMemberRequest{OrgId: orgID, UserId: userID})
	assertCode(t, err, codes.NotFound, "organization not found")
	_, err = srv.SuspendMember(ctx, &authv1.SuspendMemberRequest{OrgId: orgID, UserId: userID})
	assertCode(t, err, codes.NotFound, "organization not found")
	_, err = srv.InviteMember(ctx, &authv1.InviteMemberRequest{OrgId: orgID, Email: "member@example.com"})
	assertCode(t, err, codes.NotFound, "organization not found")
	_, err = srv.RevokeInvite(ctx, &authv1.RevokeInviteRequest{OrgId: orgID, InviteId: userID})
	assertCode(t, err, codes.NotFound, "organization not found")
	_, err = srv.ListInvites(ctx, &authv1.ListInvitesRequest{OrgId: orgID})
	assertCode(t, err, codes.NotFound, "organization not found")
	if app.listMembersOrgID != "" || app.addOrgID != "" || app.removeOrgID != "" || app.suspendOrgID != "" ||
		app.inviteOrgID != "" || app.revokeInviteID != "" || app.listInvitesOrgID != "" {
		t.Fatal("a non-member reached the organization use case")
	}
}

func TestOrgServiceSelectOrgAsMember(t *testing.T) {
	orgID := uuid.NewString()
	expires := time.Date(2026, 11, 2, 12, 15, 0, 0, time.UTC)
	app := &fakeOrgService{
		active:        true,
		selectAccess:  "access-with-org",
		selectRefresh: "refresh-with-org",
		selectExpires: expires,
	}
	srv := NewOrg(app)

	resp, err := srv.SelectOrg(authenticatedOrgContext("caller-1"), &authv1.SelectOrgRequest{
		OrgId: orgID, RefreshToken: "presented-refresh",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(app.activeCalls) != 1 || app.activeCalls[0] != (activeCall{orgID: orgID, userID: "caller-1"}) {
		t.Fatalf("active calls = %+v", app.activeCalls)
	}
	if len(app.selectCalls) != 1 || app.selectCalls[0] != (selectCall{
		userID: "caller-1", refreshToken: "presented-refresh", orgID: orgID,
	}) {
		t.Fatalf("select calls = %+v", app.selectCalls)
	}
	if got := resp.GetTokens(); got.GetAccessToken() != "access-with-org" ||
		got.GetRefreshToken() != "refresh-with-org" || got.GetExpiresAt() != "2026-11-02T12:15:00Z" {
		t.Fatalf("response = %+v", got)
	}
}

func TestOrgServiceSelectOrgNonMemberGetsNotFound(t *testing.T) {
	orgID := uuid.NewString()
	app := &fakeOrgService{active: false}
	srv := NewOrg(app)

	_, err := srv.SelectOrg(authenticatedOrgContext("stranger"), &authv1.SelectOrgRequest{
		OrgId: orgID, RefreshToken: "presented-refresh",
	})
	assertCode(t, err, codes.NotFound, "organization not found")
	if len(app.selectCalls) != 0 {
		t.Fatal("a non-member selected an organization")
	}
}

func TestOrgServiceSelectOrgInvalidRefreshToken(t *testing.T) {
	orgID := uuid.NewString()
	app := &fakeOrgService{active: true, selectErr: application.ErrInvalidRefresh}
	srv := NewOrg(app)

	_, err := srv.SelectOrg(authenticatedOrgContext("caller-1"), &authv1.SelectOrgRequest{
		OrgId: orgID, RefreshToken: "bad-refresh",
	})
	assertCode(t, err, codes.Unauthenticated, "invalid refresh token")
}

func TestOrgServiceLastActiveMemberIsFailedPrecondition(t *testing.T) {
	orgID := uuid.NewString()
	userID := uuid.NewString()
	app := &fakeOrgService{active: true, removeErr: domain.ErrLastActiveMember, suspendErr: domain.ErrLastActiveMember}
	srv := NewOrg(app)
	ctx := authenticatedOrgContext("caller-1")

	_, err := srv.RemoveMember(ctx, &authv1.RemoveMemberRequest{OrgId: orgID, UserId: userID})
	assertCode(t, err, codes.FailedPrecondition, domain.ErrLastActiveMember.Error())
	_, err = srv.SuspendMember(ctx, &authv1.SuspendMemberRequest{OrgId: orgID, UserId: userID})
	assertCode(t, err, codes.FailedPrecondition, domain.ErrLastActiveMember.Error())
}

func TestOrgServiceInvalidInput(t *testing.T) {
	app := &fakeOrgService{active: true}
	srv := NewOrg(app)
	ctx := authenticatedOrgContext("caller-1")

	_, err := srv.ListMembers(ctx, &authv1.ListMembersRequest{OrgId: "not-a-uuid"})
	assertCode(t, err, codes.InvalidArgument, "invalid org_id")
	_, err = srv.AddMember(ctx, &authv1.AddMemberRequest{OrgId: uuid.NewString(), UserId: "not-a-uuid"})
	assertCode(t, err, codes.InvalidArgument, "invalid user_id")
	_, err = srv.RemoveMember(ctx, &authv1.RemoveMemberRequest{OrgId: uuid.NewString()})
	assertCode(t, err, codes.InvalidArgument, "invalid user_id")
	if len(app.activeCalls) != 0 {
		t.Fatal("invalid input reached the membership gate")
	}
}

func TestOrgServiceMapsUserNotFound(t *testing.T) {
	orgID := uuid.NewString()
	userID := uuid.NewString()
	app := &fakeOrgService{
		active:     true,
		addErr:     domain.ErrUserNotFound,
		removeErr:  domain.ErrUserNotFound,
		suspendErr: domain.ErrUserNotFound,
	}
	srv := NewOrg(app)
	ctx := authenticatedOrgContext("caller-1")

	_, err := srv.AddMember(ctx, &authv1.AddMemberRequest{OrgId: orgID, UserId: userID})
	assertCode(t, err, codes.NotFound, "user not found")
	_, err = srv.RemoveMember(ctx, &authv1.RemoveMemberRequest{OrgId: orgID, UserId: userID})
	assertCode(t, err, codes.NotFound, "user not found")
	_, err = srv.SuspendMember(ctx, &authv1.SuspendMemberRequest{OrgId: orgID, UserId: userID})
	assertCode(t, err, codes.NotFound, "user not found")
}

func TestOrgServiceHidesUnmappedStoreError(t *testing.T) {
	orgID := uuid.NewString()
	userID := uuid.NewString()
	app := &fakeOrgService{
		active: true,
		addErr: errors.New(`postgres: insert organization_memberships: ERROR: insert or update on table "organization_memberships" violates foreign key constraint`),
	}
	srv := NewOrg(app)

	_, err := srv.AddMember(authenticatedOrgContext("caller-1"), &authv1.AddMemberRequest{OrgId: orgID, UserId: userID})
	assertCode(t, err, codes.Internal, "internal error")
}

func TestOrgServiceCallerComesFromAccessTokenNotBody(t *testing.T) {
	keys, key := orgTestKeys(t)
	orgID := uuid.NewString()
	bodyUserID := uuid.NewString()
	app := &fakeOrgService{active: true}
	srv := NewOrg(app)

	raw, err := orbittoken.SignAccessToken(key, "test-key", "token-user", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+raw))
	interceptor := AccessTokenUnaryServerInterceptor(keys)
	request := &authv1.AddMemberRequest{OrgId: orgID, UserId: bodyUserID}

	_, err = interceptor(ctx, request, &googlegrpc.UnaryServerInfo{
		FullMethod: "/auth.v1.OrgService/AddMember",
	}, func(ctx context.Context, req any) (any, error) {
		return srv.AddMember(ctx, req.(*authv1.AddMemberRequest))
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(app.activeCalls) != 1 || app.activeCalls[0].userID != "token-user" {
		t.Fatalf("membership gate caller = %+v, want token-user", app.activeCalls)
	}
	if app.addUserID != bodyUserID {
		t.Fatalf("added user = %q, want request target %q", app.addUserID, bodyUserID)
	}
}

func TestOrgAccessTokenInterceptorRejectsMissingAndInvalidTokens(t *testing.T) {
	keys, _ := orgTestKeys(t)
	interceptor := AccessTokenUnaryServerInterceptor(keys)
	info := &googlegrpc.UnaryServerInfo{FullMethod: "/auth.v1.OrgService/ListMyOrgs"}
	handler := func(context.Context, any) (any, error) {
		t.Fatal("the handler must not run without a valid token")
		return nil, nil
	}

	_, err := interceptor(context.Background(), &authv1.ListMyOrgsRequest{}, info, handler)
	assertCode(t, err, codes.Unauthenticated, "authentication required")

	invalid := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer not-a-token"))
	_, err = interceptor(invalid, &authv1.ListMyOrgsRequest{}, info, handler)
	assertCode(t, err, codes.Unauthenticated, "invalid or expired access token")
}

func TestOrgAccessTokenInterceptorLeavesAuthServiceAlone(t *testing.T) {
	keys, _ := orgTestKeys(t)
	interceptor := AccessTokenUnaryServerInterceptor(keys)
	_, err := interceptor(context.Background(), nil, &googlegrpc.UnaryServerInfo{
		FullMethod: "/auth.v1.AuthService/RequestOTP",
	}, func(context.Context, any) (any, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("AuthService call was intercepted: %v", err)
	}
}

var (
	orgTestKeyOnce sync.Once
	orgTestKey     *rsa.PrivateKey
	orgTestKeyErr  error
)

func orgTestKeys(t *testing.T) (orbittoken.KeySet, *rsa.PrivateKey) {
	t.Helper()
	orgTestKeyOnce.Do(func() {
		orgTestKey, orgTestKeyErr = rsa.GenerateKey(rand.Reader, 2048)
	})
	if orgTestKeyErr != nil {
		t.Fatal(orgTestKeyErr)
	}
	return orbittoken.KeySet{"test-key": &orgTestKey.PublicKey}, orgTestKey
}

func TestRequireActiveMemberValidatesBeforeGate(t *testing.T) {
	app := &fakeOrgService{active: true}
	srv := NewOrg(app)
	if _, _, err := srv.requireActiveMember(authenticatedOrgContext("caller-1"), "bad"); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
	if len(app.activeCalls) != 0 {
		t.Fatal("invalid organization reached the membership gate")
	}
}

func TestOrgServiceInviteMemberGatesOnMembershipAndUsesTokenCaller(t *testing.T) {
	orgID := uuid.NewString()
	expires := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	created := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	app := &fakeOrgService{
		active: true,
		inviteResult: domain.Invite{
			ID: uuid.NewString(), OrgID: orgID, Email: "member@example.com",
			InviterID: "caller-1", Status: domain.InvitePending,
			ExpiresAt: expires, CreatedAt: created,
		},
	}
	srv := NewOrg(app)

	resp, err := srv.InviteMember(authenticatedOrgContext("caller-1"), &authv1.InviteMemberRequest{
		OrgId: orgID, Email: "member@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(app.activeCalls) != 1 || app.activeCalls[0] != (activeCall{orgID: orgID, userID: "caller-1"}) {
		t.Fatalf("active calls = %+v", app.activeCalls)
	}
	if app.inviteOrgID != orgID || app.inviteInviter != "caller-1" || app.inviteEmail != "member@example.com" {
		t.Fatalf("invite = %q %q %q", app.inviteOrgID, app.inviteInviter, app.inviteEmail)
	}
	got := resp.GetInvite()
	if got.GetId() != app.inviteResult.ID || got.GetEmail() != "member@example.com" ||
		got.GetStatus() != string(domain.InvitePending) || got.GetExpiresAt() != "2026-11-09T12:00:00Z" ||
		got.GetAcceptedAt() != "" {
		t.Fatalf("response = %+v", got)
	}
}

func TestOrgServiceAcceptInviteWorksForNonMembers(t *testing.T) {
	accepted := time.Date(2026, 11, 2, 12, 5, 0, 0, time.UTC)
	app := &fakeOrgService{
		active: false,
		acceptResult: domain.Invite{
			ID: uuid.NewString(), OrgID: uuid.NewString(), Email: "member@example.com",
			Status: domain.InviteAccepted, AcceptedAt: &accepted,
			ExpiresAt: accepted.Add(7 * 24 * time.Hour), CreatedAt: accepted,
		},
	}
	srv := NewOrg(app)

	resp, err := srv.AcceptInvite(authenticatedOrgContext("new-user"), &authv1.AcceptInviteRequest{Token: "invite-token"})
	if err != nil {
		t.Fatal(err)
	}
	if len(app.activeCalls) != 0 {
		t.Fatalf("AcceptInvite gated on membership: %+v", app.activeCalls)
	}
	if app.acceptUserID != "new-user" || app.acceptToken != "invite-token" {
		t.Fatalf("accept = %q %q", app.acceptUserID, app.acceptToken)
	}
	if got := resp.GetInvite(); got.GetStatus() != string(domain.InviteAccepted) || got.GetAcceptedAt() != "2026-11-02T12:05:00Z" {
		t.Fatalf("response = %+v", got)
	}
}

func TestOrgServiceRevokeAndListInvites(t *testing.T) {
	orgID := uuid.NewString()
	inviteID := uuid.NewString()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	app := &fakeOrgService{
		active: true,
		invites: []domain.Invite{
			{ID: inviteID, OrgID: orgID, Email: "one@example.com", Status: domain.InvitePending, ExpiresAt: now.Add(time.Hour), CreatedAt: now},
			{ID: uuid.NewString(), OrgID: orgID, Email: "two@example.com", Status: domain.InviteRevoked, ExpiresAt: now, CreatedAt: now},
		},
	}
	srv := NewOrg(app)
	ctx := authenticatedOrgContext("caller-1")

	if _, err := srv.RevokeInvite(ctx, &authv1.RevokeInviteRequest{OrgId: orgID, InviteId: inviteID}); err != nil {
		t.Fatal(err)
	}
	if app.revokeInviteOrgID != orgID || app.revokeInviteID != inviteID || app.revokeInviteCaller != "caller-1" {
		t.Fatalf("revoke = %q %q %q", app.revokeInviteOrgID, app.revokeInviteID, app.revokeInviteCaller)
	}

	resp, err := srv.ListInvites(ctx, &authv1.ListInvitesRequest{OrgId: orgID})
	if err != nil {
		t.Fatal(err)
	}
	if app.listInvitesOrgID != orgID || len(resp.GetInvites()) != 2 ||
		resp.GetInvites()[0].GetEmail() != "one@example.com" ||
		resp.GetInvites()[1].GetStatus() != string(domain.InviteRevoked) {
		t.Fatalf("list = %+v", resp.GetInvites())
	}
}

func TestOrgServiceInviteErrors(t *testing.T) {
	orgID := uuid.NewString()
	inviteID := uuid.NewString()
	app := &fakeOrgService{active: true, inviteErr: domain.ErrInvalidEmail}
	srv := NewOrg(app)
	ctx := authenticatedOrgContext("caller-1")

	_, err := srv.InviteMember(ctx, &authv1.InviteMemberRequest{OrgId: orgID, Email: "bad"})
	assertCode(t, err, codes.InvalidArgument, "invalid email")

	app.inviteErr = errors.New("notifications unavailable")
	_, err = srv.InviteMember(ctx, &authv1.InviteMemberRequest{OrgId: orgID, Email: "member@example.com"})
	assertCode(t, err, codes.Internal, "internal error")

	app.acceptErr = domain.ErrInvalidInvite
	_, err = srv.AcceptInvite(ctx, &authv1.AcceptInviteRequest{Token: "bad"})
	assertCode(t, err, codes.InvalidArgument, "invalid invite")

	app.revokeInviteErr = domain.ErrInviteNotFound
	_, err = srv.RevokeInvite(ctx, &authv1.RevokeInviteRequest{OrgId: orgID, InviteId: inviteID})
	assertCode(t, err, codes.NotFound, "invite not found")

	app.listInvitesErr = domain.ErrOrgNotFound
	_, err = srv.ListInvites(ctx, &authv1.ListInvitesRequest{OrgId: orgID})
	assertCode(t, err, codes.NotFound, "organization not found")
}

func TestOrgServiceInviteInvalidInput(t *testing.T) {
	orgID := uuid.NewString()
	app := &fakeOrgService{active: true}
	srv := NewOrg(app)
	ctx := authenticatedOrgContext("caller-1")

	_, err := srv.InviteMember(ctx, &authv1.InviteMemberRequest{OrgId: "bad"})
	assertCode(t, err, codes.InvalidArgument, "invalid org_id")
	_, err = srv.RevokeInvite(ctx, &authv1.RevokeInviteRequest{OrgId: orgID, InviteId: "bad"})
	assertCode(t, err, codes.InvalidArgument, "invalid invite_id")
	if app.inviteOrgID != "" || app.revokeInviteID != "" {
		t.Fatal("invalid input reached the use case")
	}
}

func TestOrgServiceWritesPassTheCallerAndMapAuthorizationErrors(t *testing.T) {
	orgID, userID := uuid.NewString(), uuid.NewString()
	calls := map[string]func(srv *OrgServer, ctx context.Context) error{
		"add": func(srv *OrgServer, ctx context.Context) error {
			_, err := srv.AddMember(ctx, &authv1.AddMemberRequest{OrgId: orgID, UserId: userID})
			return err
		},
		"remove": func(srv *OrgServer, ctx context.Context) error {
			_, err := srv.RemoveMember(ctx, &authv1.RemoveMemberRequest{OrgId: orgID, UserId: userID})
			return err
		},
		"suspend": func(srv *OrgServer, ctx context.Context) error {
			_, err := srv.SuspendMember(ctx, &authv1.SuspendMemberRequest{OrgId: orgID, UserId: userID})
			return err
		},
		"role": func(srv *OrgServer, ctx context.Context) error {
			_, err := srv.SetMemberRole(ctx, &authv1.SetMemberRoleRequest{OrgId: orgID, UserId: userID, Role: "admin"})
			return err
		},
		"invite": func(srv *OrgServer, ctx context.Context) error {
			_, err := srv.InviteMember(ctx, &authv1.InviteMemberRequest{OrgId: orgID, Email: "a@example.com"})
			return err
		},
		"revoke": func(srv *OrgServer, ctx context.Context) error {
			_, err := srv.RevokeInvite(ctx, &authv1.RevokeInviteRequest{OrgId: orgID, InviteId: uuid.NewString()})
			return err
		},
		"list invites": func(srv *OrgServer, ctx context.Context) error {
			_, err := srv.ListInvites(ctx, &authv1.ListInvitesRequest{OrgId: orgID})
			return err
		},
	}
	setErr := func(app *fakeOrgService, err error) {
		app.addErr, app.removeErr, app.suspendErr, app.roleErr = err, err, err, err
		app.inviteErr, app.revokeInviteErr, app.listInvitesErr = err, err, err
	}
	for name, call := range calls {
		t.Run(name+" forbidden", func(t *testing.T) {
			app := &fakeOrgService{active: true}
			setErr(app, domain.ErrForbidden)
			err := call(NewOrg(app), authenticatedOrgContext("caller-1"))
			assertCode(t, err, codes.PermissionDenied, domain.ErrForbidden.Error())
		})
		t.Run(name+" last owner", func(t *testing.T) {
			app := &fakeOrgService{active: true}
			setErr(app, domain.ErrLastOwner)
			err := call(NewOrg(app), authenticatedOrgContext("caller-1"))
			assertCode(t, err, codes.FailedPrecondition, domain.ErrLastOwner.Error())
		})
		t.Run(name+" non-member", func(t *testing.T) {
			app := &fakeOrgService{active: false}
			err := call(NewOrg(app), authenticatedOrgContext("caller-1"))
			assertCode(t, err, codes.NotFound, "organization not found")
		})
		t.Run(name+" unauthenticated", func(t *testing.T) {
			err := call(NewOrg(&fakeOrgService{active: true}), context.Background())
			assertCode(t, err, codes.Unauthenticated, "authentication required")
		})
	}
}

func TestOrgServiceSetMemberRole(t *testing.T) {
	orgID, userID := uuid.NewString(), uuid.NewString()
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	app := &fakeOrgService{active: true, roleResult: domain.Membership{
		OrgID: orgID, UserID: userID, Status: domain.MembershipActive, Role: domain.RoleAdmin, CreatedAt: now, UpdatedAt: now,
	}}
	srv := NewOrg(app)

	resp, err := srv.SetMemberRole(authenticatedOrgContext("caller-1"), &authv1.SetMemberRoleRequest{OrgId: orgID, UserId: userID, Role: " admin "})
	if err != nil {
		t.Fatal(err)
	}
	if app.roleCaller != "caller-1" || app.roleOrgID != orgID || app.roleUserID != userID || app.roleValue != domain.RoleAdmin {
		t.Fatalf("service call = %+v", app)
	}
	if resp.GetMembership().GetRole() != "admin" || resp.GetMembership().GetStatus() != "active" {
		t.Fatalf("response = %+v", resp.GetMembership())
	}

	_, err = srv.SetMemberRole(authenticatedOrgContext("caller-1"), &authv1.SetMemberRoleRequest{OrgId: orgID, UserId: userID, Role: "root"})
	assertCode(t, err, codes.InvalidArgument, domain.ErrInvalidRole.Error())
}

func TestOrgServiceMembersCarryRole(t *testing.T) {
	orgID := uuid.NewString()
	app := &fakeOrgService{active: true, members: []domain.Membership{
		{OrgID: orgID, UserID: uuid.NewString(), Status: domain.MembershipActive, Role: domain.RoleOwner},
	}}
	resp, err := NewOrg(app).ListMembers(authenticatedOrgContext("caller-1"), &authv1.ListMembersRequest{OrgId: orgID})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetMembers()[0].GetRole() != "owner" {
		t.Fatalf("role = %q, want owner", resp.GetMembers()[0].GetRole())
	}
}

func TestOrgServiceSetOrgSlug(t *testing.T) {
	orgID := uuid.NewString()
	app := &fakeOrgService{active: true, slugResult: domain.Organization{ID: orgID, Name: "Acme", Slug: "new-name"}}
	srv := NewOrg(app)

	resp, err := srv.SetOrgSlug(authenticatedOrgContext("caller-1"), &authv1.SetOrgSlugRequest{OrgId: orgID, Slug: "new-name"})
	if err != nil {
		t.Fatal(err)
	}
	if app.slugOrgID != orgID || app.slugCaller != "caller-1" || app.slugValue != "new-name" {
		t.Fatalf("service call = %+v", app)
	}
	if resp.GetOrganization().GetSlug() != "new-name" {
		t.Fatalf("response slug = %q", resp.GetOrganization().GetSlug())
	}

	for _, tc := range []struct {
		err  error
		code codes.Code
	}{
		{domain.ErrForbidden, codes.PermissionDenied},
		{domain.ErrInvalidSlug, codes.InvalidArgument},
		{domain.ErrSlugTaken, codes.AlreadyExists},
	} {
		app.slugErr = tc.err
		_, err := srv.SetOrgSlug(authenticatedOrgContext("caller-1"), &authv1.SetOrgSlugRequest{OrgId: orgID, Slug: "x"})
		assertCode(t, err, tc.code, tc.err.Error())
	}
	app.active = false
	_, err = srv.SetOrgSlug(authenticatedOrgContext("caller-1"), &authv1.SetOrgSlugRequest{OrgId: orgID, Slug: "x"})
	assertCode(t, err, codes.NotFound, "organization not found")
}

func TestOrgServiceCreateOrgPassesTheRequestedSlug(t *testing.T) {
	app := &fakeOrgService{created: domain.Organization{ID: uuid.NewString(), Name: "Acme", Slug: "acme"}}
	if _, err := NewOrg(app).CreateOrg(authenticatedOrgContext("caller-1"), &authv1.CreateOrgRequest{Name: "Acme", Slug: "acme"}); err != nil {
		t.Fatal(err)
	}
	if app.createSlug != "acme" {
		t.Fatalf("slug passed to the service = %q", app.createSlug)
	}
}

func TestTenantServiceResolvesWithoutACaller(t *testing.T) {
	orgID := uuid.NewString()
	app := &fakeOrgService{resolved: domain.Organization{ID: orgID, Name: "Acme", Slug: "current"}}
	srv := NewTenant(app)

	// No caller in the context: the edge asks before anyone signs in.
	resp, err := srv.ResolveTenant(context.Background(), &authv1.ResolveTenantRequest{Slug: "current"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetOrgId() != orgID || resp.GetName() != "Acme" || resp.GetSlug() != "current" || resp.GetRetired() {
		t.Fatalf("response = %+v", resp)
	}
	retired, err := srv.ResolveTenant(context.Background(), &authv1.ResolveTenantRequest{Slug: " Old-Name "})
	if err != nil || !retired.GetRetired() || retired.GetSlug() != "current" {
		t.Fatalf("retired response = %+v, %v", retired, err)
	}
	if app.resolveSlug != " Old-Name " {
		t.Fatalf("slug forwarded = %q", app.resolveSlug)
	}

	app.resolveErr = domain.ErrSlugNotFound
	_, err = srv.ResolveTenant(context.Background(), &authv1.ResolveTenantRequest{Slug: "nobody"})
	assertCode(t, err, codes.NotFound, "tenant not found")
	app.resolveErr = errors.New("database down")
	_, err = srv.ResolveTenant(context.Background(), &authv1.ResolveTenantRequest{Slug: "x"})
	assertCode(t, err, codes.Internal, "internal error")
}
