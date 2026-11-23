package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// spyRefreshStore counts RevokeFamily calls on top of the in-memory store.
type spyRefreshStore struct {
	*fakeRefreshStore
	revokeFamilyCalls int
}

func (s *spyRefreshStore) RevokeFamily(ctx context.Context, familyID string) (int64, error) {
	s.revokeFamilyCalls++
	return s.fakeRefreshStore.RevokeFamily(ctx, familyID)
}

// loggedEvent is one call to the service logger.
type loggedEvent struct {
	msg  string
	args []any
}

// familyHarness is a service with a movable clock, a refresh store that stamps
// rotations with that clock, and a recorded logger.
type familyHarness struct {
	t       *testing.T
	svc     *Service
	refresh *fakeRefreshStore
	spy     *spyRefreshStore
	orgs    *fakeOrgStore
	now     *time.Time
	logs    []loggedEvent
}

func newFamilyHarness(t *testing.T) *familyHarness {
	t.Helper()
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	h := &familyHarness{t: t, now: &now, orgs: newFakeOrgStore()}
	h.refresh = newFakeRefreshStore()
	h.refresh.now = func() time.Time { return *h.now }
	h.spy = &spyRefreshStore{fakeRefreshStore: h.refresh}
	h.svc = NewService(Deps{
		OTP:      &fakeOTP{},
		Users:    &fakeUsers{},
		Refresh:  h.spy,
		Tokens:   fakeApiTokens{},
		Orgs:     h.orgs,
		Notifier: &fakeNotifier{},
		Flags:    fakeFlags{},
		Clock:    movingClock{now: h.now},
		IDs:      testUUIDs{},
		Signer:   testSigner{},
		Hasher:   testHasher{},
		Codes:    testCodes{},
		Secrets:  testSecrets{},
		Logger: func(msg string, args ...any) {
			h.logs = append(h.logs, loggedEvent{msg: msg, args: args})
		},
	})
	return h
}

// advance moves the shared clock forward.
func (h *familyHarness) advance(d time.Duration) {
	*h.now = h.now.Add(d)
}

// login issues a pair for userID, as a sign-in does, and returns its refresh token.
func (h *familyHarness) login(userID string) string {
	h.t.Helper()
	_, refresh, _, err := h.svc.issueTokens(context.Background(), userID)
	if err != nil {
		h.t.Fatalf("login %s: %v", userID, err)
	}
	return refresh
}

// familyOf returns the family of a token, whether it is live or rotated.
func (h *familyHarness) familyOf(token string) string {
	h.t.Helper()
	hash := domain.HashRefreshToken(token)
	if live, ok := h.refresh.tokens[hash]; ok {
		return live.familyID
	}
	if rotated, ok := h.refresh.rotated[hash]; ok {
		return rotated.familyID
	}
	h.t.Fatalf("token has no family record")
	return ""
}

// logged returns the arguments of the first logged event named msg.
func (h *familyHarness) logged(msg string) (map[string]any, bool) {
	for _, e := range h.logs {
		if e.msg != msg {
			continue
		}
		fields := make(map[string]any)
		for i := 0; i+1 < len(e.args); i += 2 {
			key, _ := e.args[i].(string)
			fields[key] = e.args[i+1]
		}
		return fields, true
	}
	return nil, false
}

func TestLoginRefreshChainSharesOneFamily(t *testing.T) {
	h := newFamilyHarness(t)
	ctx := context.Background()

	a := h.login("u1")
	_, b, _, err := h.svc.Refresh(ctx, a)
	if err != nil {
		t.Fatalf("refresh A: %v", err)
	}
	_, c, _, err := h.svc.Refresh(ctx, b)
	if err != nil {
		t.Fatalf("refresh B: %v", err)
	}

	if fa, fb, fc := h.familyOf(a), h.familyOf(b), h.familyOf(c); fa != fb || fb != fc {
		t.Fatalf("families differ: A=%s B=%s C=%s", fa, fb, fc)
	}
	for _, rotated := range []string{a, b} {
		if _, err := h.refresh.FindRotated(ctx, domain.HashRefreshToken(rotated)); err != nil {
			t.Fatalf("rotated token not recorded as rotated: %v", err)
		}
	}
	if _, err := h.refresh.FindRotated(ctx, domain.HashRefreshToken(c)); !errors.Is(err, domain.ErrRefreshTokenNotFound) {
		t.Fatalf("live token C reported as rotated: %v", err)
	}
}

func TestReuseAfterGraceRevokesTheFamily(t *testing.T) {
	h := newFamilyHarness(t)
	ctx := context.Background()

	a := h.login("u1")
	_, b, _, err := h.svc.Refresh(ctx, a)
	if err != nil {
		t.Fatalf("refresh A: %v", err)
	}
	family := h.familyOf(b)

	h.advance(31 * time.Second)
	if _, _, _, err := h.svc.Refresh(ctx, a); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("reused A err = %v, want ErrInvalidRefresh", err)
	}
	if _, _, _, err := h.svc.Refresh(ctx, b); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("B after reuse err = %v, want ErrInvalidRefresh (family revoked)", err)
	}

	fields, ok := h.logged("refresh_token_reuse_detected")
	if !ok {
		t.Fatal("no refresh_token_reuse_detected event")
	}
	if fields["user_id"] != "u1" || fields["family_id"] != family || fields["revoked"] != int64(1) {
		t.Fatalf("reuse event fields = %v", fields)
	}
	if _, ok := h.logged("refresh_token_race"); ok {
		t.Fatal("reuse after grace logged as a race")
	}
	for _, e := range h.logs {
		for _, arg := range e.args {
			if s, ok := arg.(string); ok && (s == a || s == b || s == domain.HashRefreshToken(a)) {
				t.Fatalf("event %s logged a token or its hash", e.msg)
			}
		}
	}
}

func TestReuseWithinGraceIsARaceAndKeepsTheFamily(t *testing.T) {
	h := newFamilyHarness(t)
	ctx := context.Background()

	a := h.login("u1")
	_, b, _, err := h.svc.Refresh(ctx, a)
	if err != nil {
		t.Fatalf("refresh A: %v", err)
	}

	h.advance(5 * time.Second)
	if _, _, _, err := h.svc.Refresh(ctx, a); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("racing A err = %v, want ErrInvalidRefresh", err)
	}
	if _, ok := h.logged("refresh_token_race"); !ok {
		t.Fatal("no refresh_token_race event")
	}
	if _, ok := h.logged("refresh_token_reuse_detected"); ok {
		t.Fatal("race inside the grace window revoked the family")
	}
	if h.spy.revokeFamilyCalls != 0 {
		t.Fatalf("RevokeFamily called %d times inside the grace window", h.spy.revokeFamilyCalls)
	}
	if _, _, _, err := h.svc.Refresh(ctx, b); err != nil {
		t.Fatalf("B after a race inside grace: %v", err)
	}
}

func TestLogoutTokenReuseDoesNotRevokeAnything(t *testing.T) {
	h := newFamilyHarness(t)
	ctx := context.Background()

	a := h.login("u1")
	if err := h.svc.RevokeRefresh(ctx, a); err != nil {
		t.Fatalf("logout: %v", err)
	}

	h.advance(31 * time.Second)
	if _, _, _, err := h.svc.Refresh(ctx, a); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("logged-out A err = %v, want ErrInvalidRefresh", err)
	}
	if h.spy.revokeFamilyCalls != 0 {
		t.Fatalf("RevokeFamily called %d times after a logout", h.spy.revokeFamilyCalls)
	}
	if _, ok := h.logged("refresh_token_reuse_detected"); ok {
		t.Fatal("logout reuse logged as reuse")
	}
}

func TestSelectOrgRotationKeepsTheFamily(t *testing.T) {
	h := newFamilyHarness(t)
	ctx := context.Background()
	orgID := orgWithMember(t, h.orgs, "u1")

	a := h.login("u1")
	_, b, _, err := h.svc.SelectOrg(ctx, "u1", a, orgID)
	if err != nil {
		t.Fatalf("SelectOrg: %v", err)
	}
	if h.familyOf(a) != h.familyOf(b) {
		t.Fatalf("SelectOrg started a new family: A=%s B=%s", h.familyOf(a), h.familyOf(b))
	}

	h.advance(31 * time.Second)
	if _, _, _, err := h.svc.SelectOrg(ctx, "u1", a, orgID); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("reused pre-switch token err = %v, want ErrInvalidRefresh", err)
	}
	if _, _, _, err := h.svc.Refresh(ctx, b); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("post-switch token after reuse err = %v, want ErrInvalidRefresh", err)
	}
	if _, ok := h.logged("refresh_token_reuse_detected"); !ok {
		t.Fatal("no refresh_token_reuse_detected event after SelectOrg reuse")
	}
}

func TestSeparateSignInsHaveSeparateFamilies(t *testing.T) {
	h := newFamilyHarness(t)
	ctx := context.Background()

	first := h.login("u1")
	second := h.login("u1")
	if h.familyOf(first) == h.familyOf(second) {
		t.Fatal("two sign-ins share a family")
	}

	_, firstRotated, _, err := h.svc.Refresh(ctx, first)
	if err != nil {
		t.Fatalf("refresh first: %v", err)
	}
	family := h.familyOf(firstRotated)
	h.advance(31 * time.Second)
	if _, _, _, err := h.svc.Refresh(ctx, first); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("reused first err = %v, want ErrInvalidRefresh", err)
	}
	if _, _, _, err := h.svc.Refresh(ctx, firstRotated); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("first family member after reuse err = %v, want ErrInvalidRefresh", err)
	}
	if _, _, _, err := h.svc.Refresh(ctx, second); err != nil {
		t.Fatalf("the other sign-in was revoked by reuse elsewhere: %v", err)
	}
	if fields, ok := h.logged("refresh_token_reuse_detected"); !ok || fields["family_id"] != family {
		t.Fatalf("reuse event = %v, %v", fields, ok)
	}
}
