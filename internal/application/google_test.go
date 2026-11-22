package application

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

const (
	testGoogleRedirect = "https://app.example.com/auth/google/callback"
	testGoogleSecret   = "test-client-secret"
	testGoogleUserID   = "11111111-1111-4111-8111-111111111111"
)

// movingClock is a clock the test can advance.
type movingClock struct{ now *time.Time }

func (c movingClock) Now() time.Time { return *c.now }

type fakeOAuthState struct {
	domain.OAuthState
	expires time.Time
}

// fakeOAuthStore keeps states in memory with the same single-use and expiry
// rules as the Postgres store.
type fakeOAuthStore struct {
	mu          sync.Mutex
	states      map[string]fakeOAuthState
	deleteCalls int
	signInUser  domain.User
	signInErr   error
	signIns     int
	allowCreate bool
	identity    domain.GoogleIdentity

	acceptResult      domain.InviteAcceptance
	acceptErr         error
	acceptCalls       int
	acceptInviteID    string
	acceptAllowSignup bool
}

func newFakeOAuthStore(user domain.User) *fakeOAuthStore {
	return &fakeOAuthStore{states: map[string]fakeOAuthState{}, signInUser: user}
}

func (f *fakeOAuthStore) CreateOAuthState(_ context.Context, stateHash string, state domain.OAuthState, expiresAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[stateHash] = fakeOAuthState{OAuthState: state, expires: expiresAt}
	return nil
}

func (f *fakeOAuthStore) ConsumeOAuthState(_ context.Context, stateHash string, now time.Time) (domain.OAuthState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.states[stateHash]
	if !ok || !now.Before(st.expires) {
		return domain.OAuthState{}, domain.ErrInvalidGoogleState
	}
	delete(f.states, stateHash)
	return st.OAuthState, nil
}

func (f *fakeOAuthStore) DeleteExpiredOAuthStates(_ context.Context, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	for hash, st := range f.states {
		if !now.Before(st.expires) {
			delete(f.states, hash)
		}
	}
	return nil
}

func (f *fakeOAuthStore) SignInWithIdentity(_ context.Context, provider string, id domain.GoogleIdentity, _ time.Time, allowCreate bool) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signIns++
	f.allowCreate = allowCreate
	f.identity = id
	if provider != domain.GoogleProviderName {
		return domain.User{}, errors.New("unexpected provider")
	}
	return f.signInUser, f.signInErr
}

// AcceptInviteWithIdentity returns acceptResult or acceptErr and records the
// invitation it was asked to accept.
func (f *fakeOAuthStore) AcceptInviteWithIdentity(_ context.Context, provider string, id domain.GoogleIdentity, inviteID string, _ time.Time, _ string, allowSignup bool) (domain.InviteAcceptance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acceptCalls++
	f.acceptInviteID = inviteID
	f.acceptAllowSignup = allowSignup
	f.identity = id
	if provider != domain.GoogleProviderName {
		return domain.InviteAcceptance{}, errors.New("unexpected provider")
	}
	return f.acceptResult, f.acceptErr
}

func (f *fakeOAuthStore) stateCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.states)
}

// fakeGoogleProvider records what the service sends to Google.
type fakeGoogleProvider struct {
	identity domain.GoogleIdentity
	exErr    error
	exCalls  int

	authURLRedirect  string
	authState        string
	authNonce        string
	authChallenge    string
	exchangeRedirect string
	exchangeCode     string
	exchangeVerifier string
	exchangeNonce    string
}

func (p *fakeGoogleProvider) AuthorizationURL(redirectURI, state, nonce, codeChallenge string) string {
	p.authURLRedirect, p.authState, p.authNonce, p.authChallenge = redirectURI, state, nonce, codeChallenge
	return "https://accounts.example.com/auth?state=" + state
}

func (p *fakeGoogleProvider) Exchange(_ context.Context, redirectURI, code, codeVerifier, nonce string) (domain.GoogleIdentity, error) {
	p.exCalls++
	p.exchangeRedirect, p.exchangeCode, p.exchangeVerifier, p.exchangeNonce = redirectURI, code, codeVerifier, nonce
	if p.exErr != nil {
		return domain.GoogleIdentity{}, p.exErr
	}
	return p.identity, nil
}

type googleHarness struct {
	svc      *Service
	store    *fakeOAuthStore
	provider *fakeGoogleProvider
	refresh  *fakeRefreshStore
	clock    *time.Time
	logs     []string
}

func newGoogleHarness(t *testing.T, user domain.User, signupOpen bool) *googleHarness {
	t.Helper()
	h := &googleHarness{
		store:    newFakeOAuthStore(user),
		provider: &fakeGoogleProvider{identity: domain.GoogleIdentity{Subject: "sub-1", Email: "ali@example.com", EmailVerified: true}},
		refresh:  newFakeRefreshStore(),
	}
	start := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	h.clock = &start
	h.svc = NewService(Deps{
		Users:      &fakeUsers{user: user},
		Refresh:    h.refresh,
		Signer:     testSigner{},
		IDs:        testUUIDs{},
		Clock:      movingClock{now: h.clock},
		Hasher:     testHasher{},
		Secrets:    testSecrets{},
		SignupOpen: signupOpen,
		Google: &GoogleSignIn{
			Provider:     h.provider,
			States:       h.store,
			RedirectURIs: []string{testGoogleRedirect},
			StateKey:     DeriveGoogleStateKey(testGoogleSecret),
		},
		Logger: func(msg string, _ ...any) { h.logs = append(h.logs, msg) },
	})
	return h
}

func activeGoogleUser() domain.User {
	return domain.User{ID: testGoogleUserID, Status: domain.UserActive}
}

func startSignIn(t *testing.T, h *googleHarness) string {
	t.Helper()
	_, state, err := h.svc.StartGoogleSignIn(context.Background(), testGoogleRedirect, "/inbox", "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	return state
}

func TestGoogleSignInOffReturnsDisabledForBothRPCs(t *testing.T) {
	svc := NewService(Deps{Users: &fakeUsers{}, Refresh: newFakeRefreshStore(), Signer: testSigner{}, IDs: testUUIDs{}, Clock: fixedClock{at: time.Now()}, Secrets: testSecrets{}})
	if _, _, err := svc.StartGoogleSignIn(context.Background(), testGoogleRedirect, "/", ""); !errors.Is(err, domain.ErrGoogleDisabled) {
		t.Fatalf("start err = %v, want ErrGoogleDisabled", err)
	}
	if _, err := svc.CompleteGoogleSignIn(context.Background(), "state", "code"); !errors.Is(err, domain.ErrGoogleDisabled) {
		t.Fatalf("complete err = %v, want ErrGoogleDisabled", err)
	}
}

func TestStartGoogleSignInRejectsRedirectOutsideAllowlist(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	for _, uri := range []string{"https://evil.example.com/cb", testGoogleRedirect + "?x=1", strings.ToUpper(testGoogleRedirect), ""} {
		if _, _, err := h.svc.StartGoogleSignIn(context.Background(), uri, "/", ""); !errors.Is(err, domain.ErrInvalidRedirectURI) {
			t.Fatalf("redirect %q err = %v, want ErrInvalidRedirectURI", uri, err)
		}
	}
	if h.store.stateCount() != 0 {
		t.Fatal("a refused start stored a state")
	}
}

func TestStartGoogleSignInRejectsBadReturnTo(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	for _, value := range []string{"//evil.example.com", "https://evil.example.com", "/\\evil"} {
		if _, _, err := h.svc.StartGoogleSignIn(context.Background(), testGoogleRedirect, value, ""); !errors.Is(err, domain.ErrInvalidReturnTo) {
			t.Fatalf("return_to %q err = %v, want ErrInvalidReturnTo", value, err)
		}
	}
}

func TestStartGoogleSignInStoresOnlyHashAndDerivesPKCE(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	authURL, state, err := h.svc.StartGoogleSignIn(context.Background(), testGoogleRedirect, "/inbox", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(state) != 43 {
		t.Fatalf("state has %d characters, want 43 (32 random bytes)", len(state))
	}
	if !strings.Contains(authURL, state) {
		t.Fatalf("authorization URL %q does not carry the state", authURL)
	}

	sum := sha256.Sum256([]byte(state))
	wantHash := hex.EncodeToString(sum[:])
	if _, ok := h.store.states[wantHash]; !ok || len(h.store.states) != 1 {
		t.Fatalf("stored states = %v, want only sha256 of the state", h.store.states)
	}
	if _, ok := h.store.states[state]; ok {
		t.Fatal("the raw state was stored")
	}

	mac := hmac.New(sha256.New, DeriveGoogleStateKey(testGoogleSecret))
	mac.Write([]byte("pkce:" + state))
	verifier := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	challengeSum := sha256.Sum256([]byte(verifier))
	if want := base64.RawURLEncoding.EncodeToString(challengeSum[:]); h.provider.authChallenge != want {
		t.Fatalf("challenge = %q, want S256 of the derived verifier", h.provider.authChallenge)
	}
	mac = hmac.New(sha256.New, DeriveGoogleStateKey(testGoogleSecret))
	mac.Write([]byte("nonce:" + state))
	if want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil)); h.provider.authNonce != want {
		t.Fatalf("nonce = %q, want the derived nonce", h.provider.authNonce)
	}
	if h.provider.authURLRedirect != testGoogleRedirect || h.provider.authState != state {
		t.Fatal("provider did not receive the redirect and state")
	}
	if h.store.deleteCalls != 1 {
		t.Fatalf("expired states deleted %d times on start, want 1", h.store.deleteCalls)
	}
}

func TestCompleteGoogleSignInIsSingleUse(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	state := startSignIn(t, h)

	res, err := h.svc.CompleteGoogleSignIn(context.Background(), state, "code-1")
	if err != nil {
		t.Fatalf("first complete: %v", err)
	}
	access, refresh, returnTo := res.Access, res.Refresh, res.ReturnTo
	if access == "" || refresh == "" || returnTo != "/inbox" {
		t.Fatalf("first complete tokens=%q/%q return_to=%q", access, refresh, returnTo)
	}
	if _, err := h.svc.CompleteGoogleSignIn(context.Background(), state, "code-1"); !errors.Is(err, domain.ErrInvalidGoogleState) {
		t.Fatalf("second complete err = %v, want ErrInvalidGoogleState", err)
	}
	if h.provider.exCalls != 1 {
		t.Fatalf("exchange ran %d times, want 1", h.provider.exCalls)
	}
}

func TestCompleteGoogleSignInRejectsExpiredState(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	state := startSignIn(t, h)
	*h.clock = h.clock.Add(domain.OAuthStateTTL)

	if _, err := h.svc.CompleteGoogleSignIn(context.Background(), state, "code"); !errors.Is(err, domain.ErrInvalidGoogleState) {
		t.Fatalf("expired state err = %v, want ErrInvalidGoogleState", err)
	}
	if h.provider.exCalls != 0 {
		t.Fatal("an expired state reached the exchange")
	}
}

func TestFailedExchangeStillConsumesState(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	state := startSignIn(t, h)
	h.provider.exErr = errors.New("token endpoint said no")

	if _, err := h.svc.CompleteGoogleSignIn(context.Background(), state, "code"); !errors.Is(err, domain.ErrGoogleExchangeFailed) {
		t.Fatalf("exchange failure err = %v, want ErrGoogleExchangeFailed", err)
	}
	h.provider.exErr = nil
	if _, err := h.svc.CompleteGoogleSignIn(context.Background(), state, "code"); !errors.Is(err, domain.ErrInvalidGoogleState) {
		t.Fatalf("retry after failed exchange err = %v, want ErrInvalidGoogleState", err)
	}
}

func TestCompleteGoogleSignInPassesStoredRedirectAndDerivedVerifier(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	state := startSignIn(t, h)
	if _, err := h.svc.CompleteGoogleSignIn(context.Background(), state, "code-9"); err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, DeriveGoogleStateKey(testGoogleSecret))
	mac.Write([]byte("pkce:" + state))
	wantVerifier := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if h.provider.exchangeRedirect != testGoogleRedirect || h.provider.exchangeCode != "code-9" || h.provider.exchangeVerifier != wantVerifier {
		t.Fatalf("exchange got redirect=%q code=%q verifier=%q", h.provider.exchangeRedirect, h.provider.exchangeCode, h.provider.exchangeVerifier)
	}
	if h.provider.exchangeNonce == "" || h.provider.exchangeNonce == wantVerifier {
		t.Fatal("nonce missing or equal to the verifier")
	}
}

func TestCompleteGoogleSignInRejectsEmptyAndOverlongInput(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	state := startSignIn(t, h)
	cases := map[string][2]string{
		"empty state":    {"", "code"},
		"empty code":     {state, ""},
		"state too long": {strings.Repeat("s", 129), "code"},
		"code too long":  {state, strings.Repeat("c", 2049)},
	}
	for name, tc := range cases {
		if _, err := h.svc.CompleteGoogleSignIn(context.Background(), tc[0], tc[1]); !errors.Is(err, domain.ErrInvalidGoogleState) {
			t.Fatalf("%s: err = %v, want ErrInvalidGoogleState", name, err)
		}
	}
	if h.provider.exCalls != 0 || h.store.stateCount() != 1 {
		t.Fatal("invalid input reached the exchange or consumed the real state")
	}
}

func TestCompleteGoogleSignInRefusesAccountsThatCannotSignIn(t *testing.T) {
	for _, status := range []string{domain.UserDisabled, domain.UserSignupPending, "deleted"} {
		t.Run(status, func(t *testing.T) {
			h := newGoogleHarness(t, domain.User{ID: testGoogleUserID, Status: status}, false)
			state := startSignIn(t, h)
			if _, err := h.svc.CompleteGoogleSignIn(context.Background(), state, "code"); !errors.Is(err, domain.ErrGoogleAccountNotAllowed) {
				t.Fatalf("status %s err = %v, want ErrGoogleAccountNotAllowed", status, err)
			}
			if len(h.refresh.tokens) != 0 {
				t.Fatalf("status %s got a refresh token", status)
			}
		})
	}
}

func TestCompleteGoogleSignInRefusesEmptySubject(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	h.provider.identity = domain.GoogleIdentity{Email: "ali@example.com", EmailVerified: true}
	state := startSignIn(t, h)
	if _, err := h.svc.CompleteGoogleSignIn(context.Background(), state, "code"); !errors.Is(err, domain.ErrGoogleAccountNotAllowed) {
		t.Fatalf("empty subject err = %v, want ErrGoogleAccountNotAllowed", err)
	}
	if h.store.signIns != 0 {
		t.Fatal("an identity without a subject reached the store")
	}
}

func TestSignupClosedForbidsCreatingAccounts(t *testing.T) {
	closed := newGoogleHarness(t, activeGoogleUser(), false)
	state := startSignIn(t, closed)
	if _, err := closed.svc.CompleteGoogleSignIn(context.Background(), state, "code"); err != nil {
		t.Fatal(err)
	}
	if closed.store.allowCreate {
		t.Fatal("SignupOpen=false passed allowCreate=true")
	}

	open := newGoogleHarness(t, activeGoogleUser(), true)
	state = startSignIn(t, open)
	if _, err := open.svc.CompleteGoogleSignIn(context.Background(), state, "code"); err != nil {
		t.Fatal(err)
	}
	if !open.store.allowCreate {
		t.Fatal("SignupOpen=true passed allowCreate=false")
	}
}

func TestStoreRefusalIsReturnedAsNotAllowed(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	h.store.signInErr = domain.ErrGoogleAccountNotAllowed
	state := startSignIn(t, h)
	if _, err := h.svc.CompleteGoogleSignIn(context.Background(), state, "code"); !errors.Is(err, domain.ErrGoogleAccountNotAllowed) {
		t.Fatalf("err = %v, want ErrGoogleAccountNotAllowed", err)
	}
}

func TestCompleteGoogleSignInLogsOnlyTheEvent(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	state := startSignIn(t, h)
	h.logs = nil
	if _, err := h.svc.CompleteGoogleSignIn(context.Background(), state, "code"); err != nil {
		t.Fatal(err)
	}
	if len(h.logs) != 1 || h.logs[0] != "google_login" {
		t.Fatalf("logged %v, want only google_login", h.logs)
	}
}

func TestReturnToDefaultsToRootWhenEmpty(t *testing.T) {
	h := newGoogleHarness(t, activeGoogleUser(), false)
	_, state, err := h.svc.StartGoogleSignIn(context.Background(), testGoogleRedirect, "", "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.svc.CompleteGoogleSignIn(context.Background(), state, "code")
	if err != nil || res.ReturnTo != "/" {
		t.Fatalf("return_to = %q, err = %v; want /", res.ReturnTo, err)
	}
}

func TestDeriveGoogleStateKeyIsThirtyTwoBytesAndSecretBound(t *testing.T) {
	a := DeriveGoogleStateKey("one")
	b := DeriveGoogleStateKey("two")
	if len(a) != 32 || len(b) != 32 {
		t.Fatalf("key lengths %d and %d, want 32", len(a), len(b))
	}
	if hmac.Equal(a, b) {
		t.Fatal("different secrets produced the same key")
	}
	if !hmac.Equal(a, DeriveGoogleStateKey("one")) {
		t.Fatal("key derivation is not deterministic")
	}
}
