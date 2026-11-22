// Package google is the Google OAuth 2.0 and OpenID Connect adapter. It builds
// the authorization URL, exchanges a code at the token endpoint and verifies
// the ID token against Google's published signing keys. Every failure is
// domain.ErrGoogleExchangeFailed with a fixed reason; no response body, code or
// token is ever included.
package google

import (
	"context"
	"crypto/rsa"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// Google endpoints. The Provider fields hold them; tests replace them.
const (
	DefaultAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	DefaultTokenURL = "https://oauth2.googleapis.com/token"
	DefaultJWKSURL  = "https://www.googleapis.com/oauth2/v3/certs"
)

const (
	// Scope asks for the identity, the email address and the profile name.
	scope = "openid email profile"
	// httpTimeout bounds every request to Google.
	httpTimeout = 10 * time.Second
	// maxBodyBytes bounds every response body read from Google.
	maxBodyBytes = 1 << 20
	// jwksTTL is how long fetched signing keys are used without a refetch.
	jwksTTL = time.Hour
	// unknownKeyRefetch is the minimum gap between two key fetches while keys
	// are cached.
	unknownKeyRefetch = time.Minute
	// failedFetchRetry is the gap before a failed key fetch is retried while no
	// keys are cached.
	failedFetchRetry = 2 * time.Second
	// maxKeyAge is the longest a key set may verify a token, counted from its
	// last successful fetch. Older keys are dropped even when a refetch fails.
	maxKeyAge = 24 * time.Hour
	// clockLeeway tolerates clock skew on the iat and exp checks.
	clockLeeway = time.Minute
	// maxSubjectBytes bounds the sub claim stored as the identity subject.
	maxSubjectBytes = 255
	issuerHTTPS     = "https://accounts.google.com"
	issuerBare      = "accounts.google.com"
	signingMethod   = "RS256"
)

var (
	errBodyTooLarge = errors.New("response too large")
	errUnknownKey   = errors.New("unknown signing key")
)

// Provider implements domain.GoogleProvider. The endpoint fields are exported
// for tests only; production uses NewProvider's defaults. The Provider is safe
// for concurrent use.
type Provider struct {
	ClientID     string
	ClientSecret string
	AuthURL      string
	TokenURL     string
	JWKSURL      string
	// HTTPClient performs token and JWKS requests. NewProvider builds it with a
	// 10 second timeout and redirects disabled.
	HTTPClient *http.Client

	now func() time.Time

	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	fetchedAt   time.Time // last successful fetch
	lastAttempt time.Time // last fetch attempt, successful or not
}

var _ domain.GoogleProvider = (*Provider)(nil)

// NewProvider returns a Provider for the given OAuth client with Google's
// production endpoints.
func NewProvider(clientID, clientSecret string) *Provider {
	return &Provider{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		AuthURL:      DefaultAuthURL,
		TokenURL:     DefaultTokenURL,
		JWKSURL:      DefaultJWKSURL,
		HTTPClient:   newHTTPClient(),
		now:          time.Now,
	}
}

// newHTTPClient returns the client used for every request to Google. A
// redirect is returned as is, not followed.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: httpTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// AuthorizationURL returns the Google authorization URL for one sign-in. The
// prompt is select_account so the user can pick the account on every sign-in.
func (p *Provider) AuthorizationURL(redirectURI, state, nonce, codeChallenge string) string {
	query := url.Values{
		"client_id":             {p.ClientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {scope},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
		"prompt":                {"select_account"},
	}
	return p.AuthURL + "?" + query.Encode()
}

// Exchange trades code at the token endpoint and verifies the returned ID
// token. It checks the signature (RS256 only), the issuer, the audience (and
// azp when there is more than one audience), exp, iat, the nonce and the
// subject, then returns the identity.
func (p *Provider) Exchange(ctx context.Context, redirectURI, code, codeVerifier, nonce string) (domain.GoogleIdentity, error) {
	form := url.Values{
		"client_id":     {p.ClientID},
		"client_secret": {p.ClientSecret},
		"code":          {code},
		"code_verifier": {codeVerifier},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {redirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return domain.GoogleIdentity{}, exchangeFailed("token request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return domain.GoogleIdentity{}, exchangeFailed("token request")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return domain.GoogleIdentity{}, exchangeFailed("token endpoint refused")
	}
	body, err := readLimited(resp.Body)
	if err != nil {
		return domain.GoogleIdentity{}, exchangeFailed("token response")
	}
	var tokens struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tokens); err != nil || tokens.IDToken == "" {
		return domain.GoogleIdentity{}, exchangeFailed("token response")
	}
	return p.verifyIDToken(ctx, tokens.IDToken, nonce)
}

// idClaims are the ID-token claims this adapter reads.
type idClaims struct {
	jwt.RegisteredClaims
	Nonce           string `json:"nonce"`
	Email           string `json:"email"`
	EmailVerified   any    `json:"email_verified"`
	HostedDomain    string `json:"hd"`
	Name            string `json:"name"`
	AuthorizedParty string `json:"azp"`
}

// verifyIDToken verifies an ID token for this client and nonce.
func (p *Provider) verifyIDToken(ctx context.Context, raw, nonce string) (domain.GoogleIdentity, error) {
	if nonce == "" {
		return domain.GoogleIdentity{}, exchangeFailed("id token")
	}
	var claims idClaims
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{signingMethod}),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(clockLeeway),
		jwt.WithTimeFunc(p.clock()),
	)
	_, err := parser.ParseWithClaims(raw, &claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errUnknownKey
		}
		key, err := p.signingKey(ctx, kid)
		if err != nil {
			return nil, err
		}
		return key, nil
	})
	if err != nil {
		return domain.GoogleIdentity{}, exchangeFailed("id token")
	}
	if claims.IssuedAt == nil {
		return domain.GoogleIdentity{}, exchangeFailed("id token issued-at")
	}
	if claims.Issuer != issuerHTTPS && claims.Issuer != issuerBare {
		return domain.GoogleIdentity{}, exchangeFailed("id token issuer")
	}
	if !slices.Contains(claims.Audience, p.ClientID) {
		return domain.GoogleIdentity{}, exchangeFailed("id token audience")
	}
	if len(claims.Audience) > 1 && claims.AuthorizedParty != p.ClientID {
		return domain.GoogleIdentity{}, exchangeFailed("id token authorized party")
	}
	if subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 {
		return domain.GoogleIdentity{}, exchangeFailed("id token nonce")
	}
	if claims.Subject == "" || len(claims.Subject) > maxSubjectBytes {
		return domain.GoogleIdentity{}, exchangeFailed("id token subject")
	}
	return domain.GoogleIdentity{
		Subject:       claims.Subject,
		Email:         claims.Email,
		EmailVerified: emailVerified(claims.EmailVerified),
		HostedDomain:  claims.HostedDomain,
		Name:          claims.Name,
	}, nil
}

// emailVerified reads email_verified, which Google sends as a bool or as the
// string "true".
func emailVerified(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return v == "true"
	default:
		return false
	}
}

// jwkSet is the JWKS document Google publishes.
type jwkSet struct {
	Keys []struct {
		KeyType   string `json:"kty"`
		KeyID     string `json:"kid"`
		Use       string `json:"use"`
		Algorithm string `json:"alg"`
		Modulus   string `json:"n"`
		Exponent  string `json:"e"`
	} `json:"keys"`
}

// signingKey returns the RSA key for kid. Keys are cached for jwksTTL. Keys
// older than maxKeyAge since their last successful fetch are dropped and never
// verify a token. While keys are cached, a refetch, for an expired cache or an
// unknown kid, happens at most once per unknownKeyRefetch, and a failed refetch
// keeps the cached keys. While no keys are cached, a failed fetch is retried
// after failedFetchRetry, so one failure does not block sign-in for a minute.
func (p *Provider) signingKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.currentTime()
	if p.keys != nil && now.Sub(p.fetchedAt) >= maxKeyAge {
		p.keys = nil
	}
	key, known := p.keys[kid]
	fresh := p.keys != nil && now.Sub(p.fetchedAt) < jwksTTL
	if fresh && known {
		return key, nil
	}
	throttle := unknownKeyRefetch
	if p.keys == nil {
		throttle = failedFetchRetry
	}
	if p.lastAttempt.IsZero() || now.Sub(p.lastAttempt) >= throttle {
		if err := p.fetchKeysLocked(ctx, now); err != nil && p.keys == nil {
			return nil, err
		}
	}
	key, known = p.keys[kid]
	if !known {
		return nil, errUnknownKey
	}
	return key, nil
}

// fetchKeysLocked downloads the JWKS and replaces the cached keys. The fetch
// is detached from the caller's cancellation, so a cancelled sign-in cannot
// leave the cache empty, and is bounded by httpTimeout. The caller holds p.mu.
func (p *Provider) fetchKeysLocked(ctx context.Context, now time.Time) error {
	p.lastAttempt = now
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), httpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.JWKSURL, nil)
	if err != nil {
		return exchangeFailed("signing keys request")
	}
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return exchangeFailed("signing keys request")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return exchangeFailed("signing keys endpoint refused")
	}
	body, err := readLimited(resp.Body)
	if err != nil {
		return exchangeFailed("signing keys response")
	}
	var set jwkSet
	if err := json.Unmarshal(body, &set); err != nil {
		return exchangeFailed("signing keys response")
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.KeyType != "RSA" || k.KeyID == "" {
			continue
		}
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		if k.Algorithm != "" && k.Algorithm != signingMethod {
			continue
		}
		key, ok := rsaKey(k.Modulus, k.Exponent)
		if ok {
			keys[k.KeyID] = key
		}
	}
	if len(keys) == 0 {
		return exchangeFailed("signing keys response")
	}
	p.keys = keys
	p.fetchedAt = now
	return nil
}

// rsaKey builds a public key from base64url modulus and exponent.
func rsaKey(modulus, exponent string) (*rsa.PublicKey, bool) {
	n, err := base64.RawURLEncoding.DecodeString(modulus)
	if err != nil || len(n) == 0 {
		return nil, false
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(exponent)
	if err != nil || len(eBytes) == 0 {
		return nil, false
	}
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() || e.Int64() < 2 {
		return nil, false
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(e.Int64())}, true
}

func (p *Provider) httpClient() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return newHTTPClient()
}

func (p *Provider) clock() func() time.Time {
	if p.now != nil {
		return p.now
	}
	return time.Now
}

// currentTime is the clock the key cache uses. Tests replace p.now.
func (p *Provider) currentTime() time.Time {
	return p.clock()()
}

// readLimited reads at most maxBodyBytes. A longer body is an error.
func readLimited(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBodyBytes {
		return nil, errBodyTooLarge
	}
	return body, nil
}

// exchangeFailed wraps domain.ErrGoogleExchangeFailed with a fixed reason. The
// reason names the stage only.
func exchangeFailed(reason string) error {
	return fmt.Errorf("%w: %s", domain.ErrGoogleExchangeFailed, reason)
}
