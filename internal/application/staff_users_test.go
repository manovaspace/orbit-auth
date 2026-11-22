package application

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// fakeStaffUsers is an in-memory domain.StaffUserStore. ListUsersForStaff pages
// by (created_at, id) like the real store.
type fakeStaffUsers struct {
	users    []domain.StaffUser
	audit    []domain.AuditEvent
	disabled map[string]bool
	lastBy   domain.StaffActor
	lastID   string
}

func (f *fakeStaffUsers) ListUsersForStaff(_ context.Context, q domain.StaffUserListQuery) ([]domain.StaffUser, error) {
	var out []domain.StaffUser
	for _, u := range f.users {
		if q.Status != "" && u.Status != q.Status {
			continue
		}
		if !afterUserCursor(u, q.After) {
			continue
		}
		out = append(out, u)
		if len(out) == q.Limit {
			break
		}
	}
	return out, nil
}

// afterUserCursor reports whether an account sorts after the keyset position.
func afterUserCursor(u domain.StaffUser, c *domain.StaffUserCursor) bool {
	if c == nil {
		return true
	}
	if u.CreatedAt.Equal(c.CreatedAt) {
		return u.ID > c.ID
	}
	return u.CreatedAt.After(c.CreatedAt)
}

func (f *fakeStaffUsers) GetUserForStaff(_ context.Context, id string) (domain.StaffUser, []domain.StaffUserMembership, error) {
	for _, u := range f.users {
		if u.ID == id {
			return u, nil, nil
		}
	}
	return domain.StaffUser{}, nil, domain.ErrUserNotFound
}

func (f *fakeStaffUsers) DisableUser(_ context.Context, id string, by domain.StaffActor) (bool, error) {
	f.lastID, f.lastBy = id, by
	if f.disabled[id] {
		return false, nil
	}
	f.disabled[id] = true
	return true, nil
}

func (f *fakeStaffUsers) EnableUser(_ context.Context, id string, by domain.StaffActor) (bool, error) {
	f.lastID, f.lastBy = id, by
	if !f.disabled[id] {
		return false, nil
	}
	delete(f.disabled, id)
	return true, nil
}

func (f *fakeStaffUsers) ListAuditEvents(_ context.Context, q domain.AuditEventQuery) ([]domain.AuditEvent, error) {
	var out []domain.AuditEvent
	for _, e := range f.audit {
		if q.OrgID != "" && e.OrgID != q.OrgID {
			continue
		}
		if q.SubjectUserID != "" && e.SubjectUserID != q.SubjectUserID {
			continue
		}
		if q.After != nil && e.ID >= q.After.ID {
			continue
		}
		out = append(out, e)
		if len(out) == q.Limit {
			break
		}
	}
	return out, nil
}

func newStaffUserService(store *fakeStaffUsers) *Service {
	return NewService(Deps{StaffUsers: store})
}

func seededStaffUsers(n int) *fakeStaffUsers {
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	store := &fakeStaffUsers{disabled: map[string]bool{}}
	for i := 0; i < n; i++ {
		store.users = append(store.users, domain.StaffUser{
			ID:        fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1),
			Status:    domain.UserActive,
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		})
	}
	return store
}

func TestListStaffUsersPagesOldestFirst(t *testing.T) {
	svc := newStaffUserService(seededStaffUsers(5))
	first, err := svc.ListStaffUsers(context.Background(), StaffUserListInput{PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Users) != 2 || first.NextPageToken == "" {
		t.Fatalf("first page = %d users, token %q", len(first.Users), first.NextPageToken)
	}
	second, err := svc.ListStaffUsers(context.Background(), StaffUserListInput{PageSize: 2, PageToken: first.NextPageToken})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Users) != 2 || second.Users[0].ID == first.Users[1].ID {
		t.Fatalf("second page overlaps or is short: %+v", second.Users)
	}
	last, err := svc.ListStaffUsers(context.Background(), StaffUserListInput{PageSize: 2, PageToken: second.NextPageToken})
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Users) != 1 || last.NextPageToken != "" {
		t.Fatalf("last page = %d users, token %q", len(last.Users), last.NextPageToken)
	}
}

func TestListStaffUsersRejectsBadInput(t *testing.T) {
	svc := newStaffUserService(seededStaffUsers(1))
	ctx := context.Background()
	if _, err := svc.ListStaffUsers(ctx, StaffUserListInput{Status: "banned"}); !errors.Is(err, domain.ErrInvalidUserStatus) {
		t.Fatalf("status err = %v, want ErrInvalidUserStatus", err)
	}
	if _, err := svc.ListStaffUsers(ctx, StaffUserListInput{PageSize: 101}); !errors.Is(err, domain.ErrInvalidPageSize) {
		t.Fatalf("page size err = %v, want ErrInvalidPageSize", err)
	}
	if _, err := svc.ListStaffUsers(ctx, StaffUserListInput{PageToken: "not-a-token!"}); !errors.Is(err, domain.ErrInvalidPageToken) {
		t.Fatalf("token err = %v, want ErrInvalidPageToken", err)
	}
}

func TestDisableAndEnableStaffUserNeedReasonAndReportChange(t *testing.T) {
	store := seededStaffUsers(1)
	svc := newStaffUserService(store)
	ctx := context.Background()
	id := store.users[0].ID
	staff := domain.StaffActor{Subject: "staff-1", Reason: "fraud review"}

	if _, err := svc.DisableStaffUser(ctx, id, domain.StaffActor{Subject: "staff-1"}); !errors.Is(err, domain.ErrStaffReasonRequired) {
		t.Fatalf("disable without reason err = %v, want ErrStaffReasonRequired", err)
	}
	changed, err := svc.DisableStaffUser(ctx, id, staff)
	if err != nil || !changed {
		t.Fatalf("disable = %v, %v; want changed", changed, err)
	}
	if store.lastBy != staff {
		t.Fatalf("store received actor %+v", store.lastBy)
	}
	changed, err = svc.DisableStaffUser(ctx, id, staff)
	if err != nil || changed {
		t.Fatalf("second disable = %v, %v; want unchanged", changed, err)
	}
	changed, err = svc.EnableStaffUser(ctx, id, staff)
	if err != nil || !changed {
		t.Fatalf("enable = %v, %v; want changed", changed, err)
	}
}

func TestListStaffAuditEventsRequiresAFilter(t *testing.T) {
	svc := newStaffUserService(seededStaffUsers(1))
	if _, err := svc.ListStaffAuditEvents(context.Background(), StaffAuditInput{}); !errors.Is(err, domain.ErrAuditFilterRequired) {
		t.Fatalf("err = %v, want ErrAuditFilterRequired", err)
	}
}

func TestListStaffAuditEventsPagesNewestFirst(t *testing.T) {
	store := &fakeStaffUsers{disabled: map[string]bool{}}
	for i := int64(1); i <= 3; i++ {
		store.audit = append(store.audit, domain.AuditEvent{ID: i, Action: "org.created", OrgID: "org-1"})
	}
	svc := newStaffUserService(store)
	page, err := svc.ListStaffAuditEvents(context.Background(), StaffAuditInput{OrgID: "org-1", PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 || page.NextPageToken == "" {
		t.Fatalf("page = %d events, token %q", len(page.Events), page.NextPageToken)
	}
	next, err := svc.ListStaffAuditEvents(context.Background(), StaffAuditInput{OrgID: "org-1", PageSize: 2, PageToken: page.NextPageToken})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Events) != 1 || next.Events[0].ID != 1 {
		t.Fatalf("next page = %+v", next.Events)
	}
}
