package application

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"time"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// Input limits of CompleteGoogleSignIn. Anything longer is an invalid state.
const (
	maxGoogleStateBytes = 128
	maxGoogleCodeBytes  = 2048
	// googleStateKeyLabel domain-separates the state key from the client secret.
	googleStateKeyLabel = "orbit-auth google sign-in state v1"
)

// GoogleSignIn wires Google sign-in into the service. Deps.Google is nil when
// Google sign-in is off, and both RPCs then return domain.ErrGoogleDisabled.
type GoogleSignIn struct {
	Provider domain.GoogleProvider
	States   domain.OAuthStore
	// RedirectURIs is the exact allowlist of redirect URIs.
	RedirectURIs []string
	// StateKey is the 32-byte HMAC key that derives the PKCE verifier and the
	// nonce of each sign-in. Build it once with DeriveGoogleStateKey.
	StateKey []byte
}

// DeriveGoogleStateKey returns HMAC-SHA256(clientSecret, label). The key lets
// the service derive each sign-in's PKCE verifier and nonce from its state, so
// neither is stored.
func DeriveGoogleStateKey(clientSecret string) []byte {
	mac := hmac.New(sha256.New, []byte(clientSecret))
	mac.Write([]byte(googleStateKeyLabel))
	return mac.Sum(nil)
}

// StartGoogleSignIn checks redirectURI against the allowlist and returnTo
// against the local-path rule, then creates a single-use state and returns the
// authorization URL and the state. Only sha256(state) is stored. Expired states
// are deleted on a best-effort basis.
func (s *Service) StartGoogleSignIn(ctx context.Context, redirectURI, returnTo string) (authorizationURL, state string, err error) {
	if s.google == nil {
		return "", "", domain.ErrGoogleDisabled
	}
	if !s.google.allowsRedirect(redirectURI) {
		return "", "", domain.ErrInvalidRedirectURI
	}
	returnTo, ok := domain.ValidateReturnTo(returnTo)
	if !ok {
		return "", "", domain.ErrInvalidReturnTo
	}
	state, err = s.secrets.NewSecret()
	if err != nil {
		return "", "", err
	}
	verifier := googlePKCEVerifier(s.google.StateKey, state)
	nonce := googleNonce(s.google.StateKey, state)

	now := s.clock.Now()
	_ = s.google.States.DeleteExpiredOAuthStates(ctx, now)
	if err := s.google.States.CreateOAuthState(ctx, googleStateHash(state), redirectURI, returnTo, now.Add(domain.OAuthStateTTL)); err != nil {
		return "", "", err
	}
	authorizationURL = s.google.Provider.AuthorizationURL(redirectURI, state, nonce, pkceChallenge(verifier))
	return authorizationURL, state, nil
}

// CompleteGoogleSignIn consumes the state, exchanges the code, signs the
// identity in and issues a token pair. The state is consumed before the
// exchange, so it is single use even when the exchange fails. The returned
// returnTo is the value Start stored.
func (s *Service) CompleteGoogleSignIn(ctx context.Context, state, code string) (access, refresh string, expires time.Time, returnTo string, err error) {
	if s.google == nil {
		return "", "", time.Time{}, "", domain.ErrGoogleDisabled
	}
	if state == "" || code == "" || len(state) > maxGoogleStateBytes || len(code) > maxGoogleCodeBytes {
		return "", "", time.Time{}, "", domain.ErrInvalidGoogleState
	}
	now := s.clock.Now()
	redirectURI, returnTo, err := s.google.States.ConsumeOAuthState(ctx, googleStateHash(state), now)
	if err != nil {
		return "", "", time.Time{}, "", err
	}
	verifier := googlePKCEVerifier(s.google.StateKey, state)
	nonce := googleNonce(s.google.StateKey, state)
	identity, err := s.google.Provider.Exchange(ctx, redirectURI, code, verifier, nonce)
	if err != nil {
		return "", "", time.Time{}, "", domain.ErrGoogleExchangeFailed
	}
	if identity.Subject == "" {
		return "", "", time.Time{}, "", domain.ErrGoogleAccountNotAllowed
	}
	user, err := s.google.States.SignInWithIdentity(ctx, domain.GoogleProviderName, identity, now, s.signupOpen)
	if err != nil {
		return "", "", time.Time{}, "", err
	}
	// Pending, suspended and any other non-active account get no tokens, the same
	// as the password and OTP paths.
	if user.Status != domain.UserActive {
		return "", "", time.Time{}, "", domain.ErrGoogleAccountNotAllowed
	}
	_ = s.users.UpdateLastLogin(ctx, user.ID)
	s.logger("google_login", "user_id", user.ID)
	access, refresh, expires, err = s.issueTokens(ctx, user.ID)
	if err != nil {
		return "", "", time.Time{}, "", err
	}
	return access, refresh, expires, returnTo, nil
}

// allowsRedirect reports whether redirectURI is one of the configured URIs.
func (g *GoogleSignIn) allowsRedirect(redirectURI string) bool {
	for _, allowed := range g.RedirectURIs {
		if redirectURI == allowed {
			return true
		}
	}
	return false
}

// googlePKCEVerifier derives the PKCE code verifier of a state.
func googlePKCEVerifier(key []byte, state string) string {
	return hmacBase64URL(key, "pkce:"+state)
}

// googleNonce derives the OpenID nonce of a state.
func googleNonce(key []byte, state string) string {
	return hmacBase64URL(key, "nonce:"+state)
}

// hmacBase64URL returns HMAC-SHA256(key, message) as unpadded base64url.
func hmacBase64URL(key []byte, message string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// pkceChallenge is the S256 code challenge of a verifier.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// googleStateHash is the stored form of a state: its SHA-256 hex digest.
func googleStateHash(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}
