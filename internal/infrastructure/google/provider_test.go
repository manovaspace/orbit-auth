package google

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

const (
	testClientID    = "client.example.com"
	testRedirectURI = "https://app.example.com/auth/google/callback"
	testNonce       = "nonce-1"
	// testVerifier is a 43-character PKCE verifier.
	testVerifier = "verifier-0123456789-abcdefghijklmnopqrstuv"
)

// idp is a fake Google: an authorization endpoint, a token endpoint and a JWKS
// endpoint on one httptest server, signing ID tokens with its own RSA key.
type idp struct {
	t   *testing.T
	key *rsa.PrivateKey
	kid string
	srv *httptest.Server

	mu            sync.Mutex
	jwksHits      int
	jwksKid       string
	jwksStatus    int
	tokenStatus   int
	tokenRedirect bool
	tokenBody     []byte
	idToken       string
	lastForm      url.Values
}

func newIDP(t *testing.T) *idp {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &idp{t: t, key: key, kid: "kid-1", jwksKid: "kid-1", jwksStatus: http.StatusOK, tokenStatus: http.StatusOK}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", f.serveToken)
	mux.HandleFunc("/jwks", f.serveJWKS)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *idp) serveToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.lastForm = r.PostForm
	status, redirect, body, idToken := f.tokenStatus, f.tokenRedirect, f.tokenBody, f.idToken
	f.mu.Unlock()
	switch {
	case redirect:
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	case body != nil:
		_, _ = w.Write(body)
	case status != http.StatusOK:
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	default:
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": idToken, "access_token": "unused"})
	}
}

func (f *idp) serveJWKS(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	f.jwksHits++
	kid, status := f.jwksKid, f.jwksStatus
	f.mu.Unlock()
	if status != http.StatusOK {
		w.WriteHeader(status)
		return
	}
	n := base64.RawURLEncoding.EncodeToString(f.key.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(f.key.E)).Bytes())
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256", "n": n, "e": e,
	}}})
}

// setJWKSStatus makes the JWKS endpoint answer with status until it is changed.
func (f *idp) setJWKSStatus(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jwksStatus = status
}

func (f *idp) hits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.jwksHits
}

// signed returns an RS256 ID token whose claims are the valid defaults after
// mutate has changed them.
func (f *idp) signed(now time.Time, mutate func(jwt.MapClaims)) string {
	claims := jwt.MapClaims{
		"iss":            "https://accounts.google.com",
		"aud":            testClientID,
		"sub":            "google-sub-1",
		"email":          "ali@example.com",
		"email_verified": true,
		"nonce":          testNonce,
		"iat":            now.Unix(),
		"exp":            now.Add(time.Hour).Unix(),
		"name":           "Ali",
	}
	if mutate != nil {
		mutate(claims)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = f.kid
	out, err := token.SignedString(f.key)
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

// newTestProvider points a Provider at the fake IdP and a controllable clock.
func newTestProvider(f *idp, now *time.Time) *Provider {
	p := NewProvider(testClientID, "client-secret-value")
	p.AuthURL = f.srv.URL + "/auth"
	p.TokenURL = f.srv.URL + "/token"
	p.JWKSURL = f.srv.URL + "/jwks"
	p.now = func() time.Time { return *now }
	return p
}

func setup(t *testing.T) (*idp, *Provider, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	f := newIDP(t)
	return f, newTestProvider(f, &now), &now
}

// exchangeWith signs a token with mutate and exchanges it.
func exchangeWith(t *testing.T, f *idp, p *Provider, now time.Time, mutate func(jwt.MapClaims)) (domain.GoogleIdentity, error) {
	t.Helper()
	f.mu.Lock()
	f.idToken = f.signed(now, mutate)
	f.mu.Unlock()
	return p.Exchange(context.Background(), testRedirectURI, "auth-code", testVerifier, testNonce)
}

func TestExchangeHappyPathReturnsIdentityAndSendsForm(t *testing.T) {
	f, p, now := setup(t)
	id, err := exchangeWith(t, f, p, *now, nil)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	want := domain.GoogleIdentity{Subject: "google-sub-1", Email: "ali@example.com", EmailVerified: true, Name: "Ali"}
	if id != want {
		t.Fatalf("identity = %+v, want %+v", id, want)
	}
	form := f.lastForm
	checks := map[string]string{
		"client_id":     testClientID,
		"client_secret": "client-secret-value",
		"code":          "auth-code",
		"code_verifier": testVerifier,
		"grant_type":    "authorization_code",
		"redirect_uri":  testRedirectURI,
	}
	for key, value := range checks {
		if form.Get(key) != value {
			t.Fatalf("token form %s = %q, want %q", key, form.Get(key), value)
		}
	}
}

func TestAuthorizationURLCarriesPKCEAndNonce(t *testing.T) {
	_, p, _ := setup(t)
	challenge := base64.RawURLEncoding.EncodeToString(sumSHA256(testVerifier))
	raw := p.AuthorizationURL(testRedirectURI, "state-1", testNonce, challenge)
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	checks := map[string]string{
		"client_id":             testClientID,
		"redirect_uri":          testRedirectURI,
		"response_type":         "code",
		"scope":                 "openid email profile",
		"state":                 "state-1",
		"nonce":                 testNonce,
		"code_challenge":        challenge,
		"code_challenge_method": "S256",
		"prompt":                "select_account",
	}
	for key, value := range checks {
		if q.Get(key) != value {
			t.Fatalf("%s = %q, want %q", key, q.Get(key), value)
		}
	}
	// The challenge must be the S256 of the verifier the token exchange sends.
	if q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(sumSHA256(testVerifier)) {
		t.Fatal("code_challenge is not S256 of the verifier")
	}
}

func sumSHA256(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func TestExchangeRejectsInvalidTokens(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(jwt.MapClaims)
	}{
		{"wrong audience", func(c jwt.MapClaims) { c["aud"] = "other-client" }},
		{"wrong issuer", func(c jwt.MapClaims) { c["iss"] = "https://evil.example.com" }},
		{"wrong nonce", func(c jwt.MapClaims) { c["nonce"] = "nonce-2" }},
		{"missing nonce", func(c jwt.MapClaims) { delete(c, "nonce") }},
		{"expired", func(c jwt.MapClaims) { c["exp"] = time.Date(2026, 11, 22, 11, 0, 0, 0, time.UTC).Unix() }},
		{"missing exp", func(c jwt.MapClaims) { delete(c, "exp") }},
		{"missing iat", func(c jwt.MapClaims) { delete(c, "iat") }},
		{"empty subject", func(c jwt.MapClaims) { c["sub"] = "" }},
		{"subject too long", func(c jwt.MapClaims) { c["sub"] = strings.Repeat("s", 256) }},
		{"several audiences without azp", func(c jwt.MapClaims) { c["aud"] = []string{testClientID, "other"} }},
		{"several audiences with other azp", func(c jwt.MapClaims) {
			c["aud"] = []string{testClientID, "other"}
			c["azp"] = "other"
		}},
		{"audience list without ours", func(c jwt.MapClaims) { c["aud"] = []string{"other"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, p, now := setup(t)
			_, err := exchangeWith(t, f, p, *now, tc.mutate)
			if !errors.Is(err, domain.ErrGoogleExchangeFailed) {
				t.Fatalf("err = %v, want ErrGoogleExchangeFailed", err)
			}
		})
	}
}

func TestExchangeAcceptsSeveralAudiencesWithOurAzp(t *testing.T) {
	f, p, now := setup(t)
	id, err := exchangeWith(t, f, p, *now, func(c jwt.MapClaims) {
		c["aud"] = []string{testClientID, "other"}
		c["azp"] = testClientID
	})
	if err != nil || id.Subject != "google-sub-1" {
		t.Fatalf("several audiences with our azp: id=%+v err=%v", id, err)
	}
}

func TestExchangeAcceptsBareIssuerAndStringEmailVerified(t *testing.T) {
	f, p, now := setup(t)
	id, err := exchangeWith(t, f, p, *now, func(c jwt.MapClaims) {
		c["iss"] = "accounts.google.com"
		c["email_verified"] = "true"
		c["hd"] = "example.com"
	})
	if err != nil {
		t.Fatalf("bare issuer: %v", err)
	}
	if !id.EmailVerified || id.HostedDomain != "example.com" {
		t.Fatalf("identity = %+v", id)
	}
	id, err = exchangeWith(t, f, p, *now, func(c jwt.MapClaims) { c["email_verified"] = "false" })
	if err != nil || id.EmailVerified {
		t.Fatalf("string false: id=%+v err=%v", id, err)
	}
}

func TestExchangeRejectsTokensNotSignedByTheKey(t *testing.T) {
	f, p, now := setup(t)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "https://accounts.google.com", "aud": testClientID, "sub": "x", "nonce": testNonce,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	token.Header["kid"] = f.kid
	forged, err := token.SignedString(other)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.idToken = forged
	f.mu.Unlock()
	if _, err := p.Exchange(context.Background(), testRedirectURI, "code", testVerifier, testNonce); !errors.Is(err, domain.ErrGoogleExchangeFailed) {
		t.Fatalf("forged signature err = %v, want ErrGoogleExchangeFailed", err)
	}
}

func TestExchangeRejectsHS256AndNone(t *testing.T) {
	f, p, now := setup(t)
	claims := jwt.MapClaims{
		"iss": "https://accounts.google.com", "aud": testClientID, "sub": "x", "nonce": testNonce,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	hs.Header["kid"] = f.kid
	hsToken, err := hs.SignedString([]byte("attacker-chosen-secret"))
	if err != nil {
		t.Fatal(err)
	}
	none := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	none.Header["kid"] = f.kid
	noneToken, err := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{"HS256": hsToken, "none": noneToken} {
		f.mu.Lock()
		f.idToken = token
		f.mu.Unlock()
		if _, err := p.Exchange(context.Background(), testRedirectURI, "code", testVerifier, testNonce); !errors.Is(err, domain.ErrGoogleExchangeFailed) {
			t.Fatalf("%s token err = %v, want ErrGoogleExchangeFailed", name, err)
		}
	}
}

func TestExchangeRejectsUnknownKid(t *testing.T) {
	f, p, now := setup(t)
	f.mu.Lock()
	f.idToken = f.signed(*now, nil)
	f.mu.Unlock()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "https://accounts.google.com", "aud": testClientID, "sub": "x", "nonce": testNonce,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	token.Header["kid"] = "kid-unknown"
	signed, err := token.SignedString(f.key)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.idToken = signed
	f.mu.Unlock()
	if _, err := p.Exchange(context.Background(), testRedirectURI, "code", testVerifier, testNonce); !errors.Is(err, domain.ErrGoogleExchangeFailed) {
		t.Fatalf("unknown kid err = %v, want ErrGoogleExchangeFailed", err)
	}
}

func TestExchangeTokenEndpointFailures(t *testing.T) {
	cases := map[string]func(f *idp){
		"400 from token endpoint":  func(f *idp) { f.tokenStatus = http.StatusBadRequest },
		"redirect is not followed": func(f *idp) { f.tokenRedirect = true },
		"oversized body":           func(f *idp) { f.tokenBody = []byte(strings.Repeat("x", maxBodyBytes+10)) },
		"no id_token":              func(f *idp) { f.tokenBody = []byte(`{"access_token":"a"}`) },
		"not json":                 func(f *idp) { f.tokenBody = []byte("<html>") },
	}
	for name, setupFn := range cases {
		t.Run(name, func(t *testing.T) {
			f, p, _ := setup(t)
			setupFn(f)
			_, err := p.Exchange(context.Background(), testRedirectURI, "code", testVerifier, testNonce)
			if !errors.Is(err, domain.ErrGoogleExchangeFailed) {
				t.Fatalf("err = %v, want ErrGoogleExchangeFailed", err)
			}
			if strings.Contains(err.Error(), "invalid_grant") || strings.Contains(err.Error(), "<html>") {
				t.Fatalf("error includes the response body: %v", err)
			}
		})
	}
}

func TestJWKSIsCachedForAnHour(t *testing.T) {
	f, p, now := setup(t)
	for i := 0; i < 3; i++ {
		if _, err := exchangeWith(t, f, p, *now, nil); err != nil {
			t.Fatal(err)
		}
	}
	if hits := f.hits(); hits != 1 {
		t.Fatalf("JWKS fetched %d times within an hour, want 1", hits)
	}
	*now = now.Add(jwksTTL + time.Minute)
	if _, err := exchangeWith(t, f, p, *now, nil); err != nil {
		t.Fatal(err)
	}
	if hits := f.hits(); hits != 2 {
		t.Fatalf("JWKS fetched %d times after the TTL, want 2", hits)
	}
}

func TestUnknownKidRefetchesAtMostOncePerMinute(t *testing.T) {
	f, p, now := setup(t)
	if _, err := exchangeWith(t, f, p, *now, nil); err != nil {
		t.Fatal(err)
	}
	// Rotate the IdP key: the token now names a kid the cache lacks.
	f.mu.Lock()
	f.jwksKid = "kid-2"
	f.mu.Unlock()
	f.kid = "kid-2"
	if _, err := exchangeWith(t, f, p, *now, nil); !errors.Is(err, domain.ErrGoogleExchangeFailed) {
		t.Fatalf("unknown kid err = %v", err)
	}
	if hits := f.hits(); hits != 1 {
		t.Fatalf("unknown kid refetched inside the minute: hits = %d, want 1", hits)
	}
	*now = now.Add(unknownKeyRefetch)
	if _, err := exchangeWith(t, f, p, *now, nil); err != nil {
		t.Fatalf("after the minute the rotated key should be found: %v", err)
	}
	if hits := f.hits(); hits != 2 {
		t.Fatalf("hits after the minute = %d, want 2", hits)
	}
}

func TestConcurrentExchangesShareOneKeyFetch(t *testing.T) {
	f, p, now := setup(t)
	f.mu.Lock()
	f.idToken = f.signed(*now, nil)
	f.mu.Unlock()
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := p.Exchange(context.Background(), testRedirectURI, "code", testVerifier, testNonce)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent exchange: %v", err)
		}
	}
	if hits := f.hits(); hits != 1 {
		t.Fatalf("concurrent exchanges fetched JWKS %d times, want 1", hits)
	}
}

func TestRSAKeyRejectsBadParameters(t *testing.T) {
	if _, ok := rsaKey("", "AQAB"); ok {
		t.Fatal("empty modulus accepted")
	}
	if _, ok := rsaKey("AQAB", "!!"); ok {
		t.Fatal("bad exponent accepted")
	}
	if _, ok := rsaKey("AQAB", "AQ"); ok {
		t.Fatal("exponent 1 accepted")
	}
}

func TestEmailVerifiedAcceptsBoolAndTrueString(t *testing.T) {
	cases := map[string]bool{"true": true, "false": false}
	for in, want := range cases {
		if got := emailVerified(in); got != want {
			t.Fatalf("emailVerified(%q) = %v", in, got)
		}
	}
	if !emailVerified(true) || emailVerified(nil) || emailVerified(1) {
		t.Fatal("bool and non-bool values mishandled")
	}
}

func TestFirstFetchWithCancelledContextDoesNotBlockRetry(t *testing.T) {
	f, p, now := setup(t)
	f.setJWKSStatus(http.StatusInternalServerError)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.signingKey(cancelled, f.kid); !errors.Is(err, domain.ErrGoogleExchangeFailed) {
		t.Fatalf("failed first fetch err = %v, want ErrGoogleExchangeFailed", err)
	}
	f.setJWKSStatus(http.StatusOK)
	// Inside failedFetchRetry the failed fetch is not repeated.
	if _, err := p.signingKey(context.Background(), f.kid); err == nil {
		t.Fatal("retry inside failedFetchRetry found a key")
	}
	if hits := f.hits(); hits != 1 {
		t.Fatalf("hits inside failedFetchRetry = %d, want 1", hits)
	}
	// After the short throttle the next sign-in fetches and succeeds, not a minute later.
	*now = now.Add(failedFetchRetry)
	key, err := p.signingKey(context.Background(), f.kid)
	if err != nil || key == nil {
		t.Fatalf("retry after failedFetchRetry: key=%v err=%v", key, err)
	}
	if hits := f.hits(); hits != 2 {
		t.Fatalf("hits after retry = %d, want 2", hits)
	}
}

func TestFirstFetchIsNotAbortedByCallerCancellation(t *testing.T) {
	f, p, _ := setup(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.signingKey(cancelled, f.kid); err != nil {
		t.Fatalf("first fetch with cancelled caller context: %v", err)
	}
	if hits := f.hits(); hits != 1 {
		t.Fatalf("hits = %d, want 1", hits)
	}
}

func TestCachedKeysStopVerifyingAfterMaxAge(t *testing.T) {
	f, p, now := setup(t)
	start := *now
	if _, err := exchangeWith(t, f, p, *now, nil); err != nil {
		t.Fatal(err)
	}
	// Google stops answering; the cached keys carry on until maxKeyAge.
	f.setJWKSStatus(http.StatusInternalServerError)
	*now = start.Add(23 * time.Hour)
	if _, err := exchangeWith(t, f, p, *now, nil); err != nil {
		t.Fatalf("23h old keys with failing endpoint: %v", err)
	}
	*now = start.Add(maxKeyAge)
	if _, err := exchangeWith(t, f, p, *now, nil); !errors.Is(err, domain.ErrGoogleExchangeFailed) {
		t.Fatalf("24h old keys with failing endpoint err = %v, want ErrGoogleExchangeFailed", err)
	}
	// Once Google answers again, the keys are fetched anew and sign-in works.
	f.setJWKSStatus(http.StatusOK)
	*now = start.Add(maxKeyAge + failedFetchRetry)
	if _, err := exchangeWith(t, f, p, *now, nil); err != nil {
		t.Fatalf("after the endpoint recovers: %v", err)
	}
}
