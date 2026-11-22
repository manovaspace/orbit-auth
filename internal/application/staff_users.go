package application

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// StaffUserListInput selects one page of accounts. Status is "", "active",
// "disabled" or "signup_pending". PageSize zero means the default; PageToken is
// empty for the first page.
type StaffUserListInput struct {
	Query     string
	Status    string
	PageSize  int
	PageToken string
}

// StaffUserPage is one page of accounts. NextPageToken is empty on the last page.
type StaffUserPage struct {
	Users         []domain.StaffUser
	NextPageToken string
}

// StaffAuditInput selects one page of audit rows. At least one of OrgID and
// SubjectUserID is required.
type StaffAuditInput struct {
	OrgID         string
	SubjectUserID string
	PageSize      int
	PageToken     string
}

// StaffAuditPage is one page of audit rows, newest first. NextPageToken is empty
// on the last page.
type StaffAuditPage struct {
	Events        []domain.AuditEvent
	NextPageToken string
}

// ListStaffUsers returns one page of accounts, oldest first.
func (s *Service) ListStaffUsers(ctx context.Context, in StaffUserListInput) (StaffUserPage, error) {
	if s.staffUsers == nil {
		return StaffUserPage{}, fmt.Errorf("staff user store is not configured")
	}
	status, err := parseStaffUserStatus(in.Status)
	if err != nil {
		return StaffUserPage{}, err
	}
	limit, err := staffPageLimit(in.PageSize)
	if err != nil {
		return StaffUserPage{}, err
	}
	after, err := decodeStaffUserCursor(in.PageToken)
	if err != nil {
		return StaffUserPage{}, err
	}
	// One extra row tells whether another page exists.
	rows, err := s.staffUsers.ListUsersForStaff(ctx, domain.StaffUserListQuery{
		Query:  strings.TrimSpace(in.Query),
		Status: status,
		After:  after,
		Limit:  limit + 1,
	})
	if err != nil {
		return StaffUserPage{}, err
	}
	page := StaffUserPage{Users: rows}
	if len(rows) > limit {
		page.Users = rows[:limit]
		last := page.Users[limit-1]
		page.NextPageToken = encodeStaffUserCursor(last.CreatedAt, last.ID)
	}
	return page, nil
}

// GetStaffUser returns one account with its memberships.
func (s *Service) GetStaffUser(ctx context.Context, userID string) (domain.StaffUser, []domain.StaffUserMembership, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return domain.StaffUser{}, nil, fmt.Errorf("user_id required")
	}
	if s.staffUsers == nil {
		return domain.StaffUser{}, nil, fmt.Errorf("staff user store is not configured")
	}
	return s.staffUsers.GetUserForStaff(ctx, userID)
}

// DisableStaffUser disables an active account and revokes its sessions. The
// reason is required. The result is false when the account was already disabled.
func (s *Service) DisableStaffUser(ctx context.Context, userID string, by domain.StaffActor) (bool, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false, fmt.Errorf("user_id required")
	}
	if strings.TrimSpace(by.Reason) == "" {
		return false, domain.ErrStaffReasonRequired
	}
	if s.staffUsers == nil {
		return false, fmt.Errorf("staff user store is not configured")
	}
	return s.staffUsers.DisableUser(ctx, userID, by)
}

// EnableStaffUser makes a disabled account active again. The reason is required.
// The result is false when the account was already active.
func (s *Service) EnableStaffUser(ctx context.Context, userID string, by domain.StaffActor) (bool, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false, fmt.Errorf("user_id required")
	}
	if strings.TrimSpace(by.Reason) == "" {
		return false, domain.ErrStaffReasonRequired
	}
	if s.staffUsers == nil {
		return false, fmt.Errorf("staff user store is not configured")
	}
	return s.staffUsers.EnableUser(ctx, userID, by)
}

// ListStaffAuditEvents returns one page of audit rows, newest first. A listing
// with neither an organization nor a user is domain.ErrAuditFilterRequired.
func (s *Service) ListStaffAuditEvents(ctx context.Context, in StaffAuditInput) (StaffAuditPage, error) {
	orgID := strings.TrimSpace(in.OrgID)
	subjectID := strings.TrimSpace(in.SubjectUserID)
	if orgID == "" && subjectID == "" {
		return StaffAuditPage{}, domain.ErrAuditFilterRequired
	}
	if s.staffUsers == nil {
		return StaffAuditPage{}, fmt.Errorf("staff user store is not configured")
	}
	limit, err := staffPageLimit(in.PageSize)
	if err != nil {
		return StaffAuditPage{}, err
	}
	after, err := decodeAuditCursor(in.PageToken)
	if err != nil {
		return StaffAuditPage{}, err
	}
	rows, err := s.staffUsers.ListAuditEvents(ctx, domain.AuditEventQuery{
		OrgID:         orgID,
		SubjectUserID: subjectID,
		After:         after,
		Limit:         limit + 1,
	})
	if err != nil {
		return StaffAuditPage{}, err
	}
	page := StaffAuditPage{Events: rows}
	if len(rows) > limit {
		page.Events = rows[:limit]
		page.NextPageToken = encodeAuditCursor(page.Events[limit-1].ID)
	}
	return page, nil
}

// staffPageLimit applies the default page size and rejects sizes outside 1 to
// 100.
func staffPageLimit(size int) (int, error) {
	if size == 0 {
		return defaultStaffPageSize, nil
	}
	if size < 1 || size > maxStaffPageSize {
		return 0, domain.ErrInvalidPageSize
	}
	return size, nil
}

func parseStaffUserStatus(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "", nil
	case domain.UserActive, domain.UserDisabled, domain.UserSignupPending:
		return strings.ToLower(strings.TrimSpace(raw)), nil
	default:
		return "", domain.ErrInvalidUserStatus
	}
}

// encodeStaffUserCursor encodes the keyset position after an account.
func encodeStaffUserCursor(createdAt time.Time, id string) string {
	raw := createdAt.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeStaffUserCursor reads a token from encodeStaffUserCursor. An empty token
// is the first page and returns nil.
func decodeStaffUserCursor(token string) (*domain.StaffUserCursor, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, domain.ErrInvalidPageToken
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return nil, domain.ErrInvalidPageToken
	}
	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return nil, domain.ErrInvalidPageToken
	}
	return &domain.StaffUserCursor{CreatedAt: createdAt, ID: parts[1]}, nil
}

// encodeAuditCursor encodes the id of the last audit row of a page.
func encodeAuditCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}

// decodeAuditCursor reads a token from encodeAuditCursor. An empty token is the
// first page and returns nil.
func decodeAuditCursor(token string) (*domain.AuditEventCursor, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, domain.ErrInvalidPageToken
	}
	id, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || id <= 0 {
		return nil, domain.ErrInvalidPageToken
	}
	return &domain.AuditEventCursor{ID: id}, nil
}
