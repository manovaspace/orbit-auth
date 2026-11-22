package grpc

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	authv1 "github.com/manovaspace/orbit-auth/api/auth/v1"
	"github.com/manovaspace/orbit-auth/internal/application"
	"github.com/manovaspace/orbit-auth/internal/domain"
)

const staffUserWriteMethod = "/auth.v1.StaffUserService/DisableUser"

// fakeStaffUserService records the staff user calls and returns preset results.
type fakeStaffUserService struct {
	err     error
	changed bool
	actor   domain.StaffActor
	userID  string
}

func (f *fakeStaffUserService) ListStaffUsers(context.Context, application.StaffUserListInput) (application.StaffUserPage, error) {
	return application.StaffUserPage{}, nil
}

func (f *fakeStaffUserService) GetStaffUser(_ context.Context, userID string) (domain.StaffUser, []domain.StaffUserMembership, error) {
	f.userID = userID
	return domain.StaffUser{ID: userID}, nil, f.err
}

func (f *fakeStaffUserService) DisableStaffUser(_ context.Context, userID string, by domain.StaffActor) (bool, error) {
	f.userID, f.actor = userID, by
	return f.changed, f.err
}

func (f *fakeStaffUserService) EnableStaffUser(_ context.Context, userID string, by domain.StaffActor) (bool, error) {
	f.userID, f.actor = userID, by
	return f.changed, f.err
}

func (f *fakeStaffUserService) ListStaffAuditEvents(context.Context, application.StaffAuditInput) (application.StaffAuditPage, error) {
	return application.StaffAuditPage{}, f.err
}

func TestStaffInterceptorGuardsUserService(t *testing.T) {
	_, ran, err := runStaffInterceptor(t, staffTestToken, staffMD("wrong-token", "staff-1", "fraud"), staffUserWriteMethod)
	assertCode(t, err, codes.Unauthenticated, "invalid service token")
	if ran {
		t.Fatal("handler must not run with a wrong service token")
	}

	_, ran, err = runStaffInterceptor(t, staffTestToken, staffMD(staffTestToken, "staff-1", ""), staffUserWriteMethod)
	assertCode(t, err, codes.InvalidArgument, "staff reason required")
	if ran {
		t.Fatal("a disable without a reason must not run")
	}

	_, ran, err = runStaffInterceptor(t, staffTestToken, staffMD(staffTestToken, "staff-1", ""), "/auth.v1.StaffUserService/ListUsers")
	if err != nil || !ran {
		t.Fatalf("ListUsers without a reason: ran=%v err=%v, want a read", ran, err)
	}
}

func TestStaffUserServerPassesActorAndMapsErrors(t *testing.T) {
	userID := uuid.NewString()
	app := &fakeStaffUserService{changed: true}
	srv := NewStaffUser(app)
	actor := domain.StaffActor{Subject: "staff-1", Reason: "fraud review"}

	resp, err := srv.DisableUser(staffActorCtx(actor), &authv1.DisableUserRequest{UserId: userID})
	if err != nil || !resp.GetChanged() {
		t.Fatalf("DisableUser = %v, %v", resp, err)
	}
	if app.actor != actor || app.userID != userID {
		t.Fatalf("service got actor %+v user %q", app.actor, app.userID)
	}

	app.err = domain.ErrAccountNotActive
	_, err = srv.EnableUser(staffActorCtx(actor), &authv1.EnableUserRequest{UserId: userID})
	assertCode(t, err, codes.FailedPrecondition, "account is not active")

	app.err = domain.ErrUserNotFound
	_, err = srv.GetUser(context.Background(), &authv1.GetUserRequest{UserId: userID})
	assertCode(t, err, codes.NotFound, "user not found")
}

func TestStaffUserServerRejectsBadIDsAndActorlessWrites(t *testing.T) {
	srv := NewStaffUser(&fakeStaffUserService{})
	_, err := srv.GetUser(context.Background(), &authv1.GetUserRequest{UserId: "not-a-uuid"})
	assertCode(t, err, codes.InvalidArgument, "invalid user_id")

	_, err = srv.DisableUser(context.Background(), &authv1.DisableUserRequest{UserId: uuid.NewString()})
	assertCode(t, err, codes.Unauthenticated, "staff authentication required")

	_, err = srv.ListAuditEvents(context.Background(), &authv1.ListAuditEventsRequest{OrgId: "bad"})
	assertCode(t, err, codes.InvalidArgument, "invalid org_id")
}

func TestOrgServerReactivateMemberCallsService(t *testing.T) {
	orgID, targetID := uuid.NewString(), uuid.NewString()
	app := &fakeOrgService{active: true}
	srv := NewOrg(app)
	if _, err := srv.ReactivateMember(authenticatedOrgContext("caller-1"), &authv1.ReactivateMemberRequest{OrgId: orgID, UserId: targetID}); err != nil {
		t.Fatal(err)
	}
	if app.reactivateOrgID != orgID || app.reactivateCaller != "caller-1" || app.reactivateUserID != targetID {
		t.Fatalf("service got org %q caller %q user %q", app.reactivateOrgID, app.reactivateCaller, app.reactivateUserID)
	}
}
