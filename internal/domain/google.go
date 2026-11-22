package domain

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Google sign-in errors. The gRPC adapter maps each one to a fixed status, so
// the messages below never reach a client that the status does not already
// describe.
var (
	// ErrGoogleDisabled is returned while GOOGLE_CLIENT_ID is not configured.
	ErrGoogleDisabled = errors.New("google sign-in is off")
	// ErrInvalidRedirectURI is returned when redirect_uri is not one of the
	// configured GOOGLE_REDIRECT_URIS.
	ErrInvalidRedirectURI = errors.New("invalid redirect uri")
	// ErrInvalidReturnTo is returned when return_to is not a local path.
	ErrInvalidReturnTo = errors.New("invalid return_to")
	// ErrInvalidGoogleState covers an unknown, used or expired state. The three
	// cases are one error on purpose.
	ErrInvalidGoogleState = errors.New("invalid or expired google sign-in state")
	// ErrGoogleExchangeFailed is any failure to exchange the code or verify the
	// ID token. It never carries the response body, the code or a token.
	ErrGoogleExchangeFailed = errors.New("google sign-in exchange failed")
	// ErrGoogleAccountNotAllowed is returned when the identity has no usable
	// subject, the account is not active, or the sign-in would create an account
	// while self-serve sign-up is closed.
	ErrGoogleAccountNotAllowed = errors.New("account not allowed")
	// ErrInviteNeedsPassword is returned when a Google sign-in is started for a
	// phone invitation. Only an email invitation can be accepted with Google.
	ErrInviteNeedsPassword = errors.New("google cannot accept a phone invitation")
	// ErrInviteEmailMismatch is returned when the account or the verified Google
	// email does not match the invited email. Nothing is written.
	ErrInviteEmailMismatch = errors.New("invite email mismatch")
	// ErrGoogleAlreadyLinked is returned when the account already has a Google
	// identity, or the Google subject is already linked to this account.
	ErrGoogleAlreadyLinked = errors.New("google is already linked")
	// ErrGoogleIdentityTaken is returned when the Google subject is linked to
	// another account.
	ErrGoogleIdentityTaken = errors.New("google account is linked to another user")
	// ErrGoogleNotLinked is returned when unlinking an account with no Google
	// identity.
	ErrGoogleNotLinked = errors.New("google is not linked")
)

// OAuthStateTTL is how long a started Google sign-in may take to complete.
const OAuthStateTTL = 10 * time.Minute

// GoogleProviderName is the provider value stored in user_identities.
const GoogleProviderName = "google"

// GoogleIdentity is the verified subject and profile of a Google ID token.
type GoogleIdentity struct {
	Subject       string
	Email         string
	EmailVerified bool
	HostedDomain  string
	Name          string
}

// GoogleProvider talks to Google's authorization and token endpoints.
type GoogleProvider interface {
	// AuthorizationURL returns the URL the browser is sent to. It carries the
	// state, the nonce and the S256 PKCE challenge of the verifier.
	AuthorizationURL(redirectURI, state, nonce, codeChallenge string) string
	// Exchange trades the code for an ID token, verifies it and returns its
	// identity. Every failure is ErrGoogleExchangeFailed.
	Exchange(ctx context.Context, redirectURI, code, codeVerifier, nonce string) (GoogleIdentity, error)
}

// Purposes of a Google sign-in state. The stored values are the contract of
// oauth_sign_in_states.purpose (migration 018).
const (
	// OAuthPurposeSignIn is a plain sign-in. It carries no invitation or user.
	OAuthPurposeSignIn = "sign_in"
	// OAuthPurposeInvite is a sign-in that accepts one invitation. It carries
	// the invitation id, never the token.
	OAuthPurposeInvite = "invite"
	// OAuthPurposeLink links a Google identity to the signed-in account. It
	// carries that account's user id.
	OAuthPurposeLink = "link"
)

// OAuthState is what Start stored for one state. Purpose decides which
// Complete RPC may consume it; InviteID and UserID are set only for the
// purposes that need them.
type OAuthState struct {
	RedirectURI string
	ReturnTo    string
	Purpose     string
	InviteID    string
	UserID      string
}

// InviteAcceptance is the outcome of accepting an invitation with a Google
// identity. AlreadyMember is true when the account was an active member of the
// organization before, and Role is the stored tier in that case.
type InviteAcceptance struct {
	User          User
	OrgID         string
	Role          MembershipRole
	AlreadyMember bool
}

// OAuthStore persists Google sign-in states and linked identities.
type OAuthStore interface {
	// CreateOAuthState stores a state by its SHA-256 hex digest, with the
	// purpose and the ids it carries.
	CreateOAuthState(ctx context.Context, stateHash string, state OAuthState, expiresAt time.Time) error
	// ConsumeOAuthState deletes a live state and returns what Start stored for
	// it. An unknown, used or expired state is ErrInvalidGoogleState.
	ConsumeOAuthState(ctx context.Context, stateHash string, now time.Time) (OAuthState, error)
	// DeleteExpiredOAuthStates removes states that expired at or before now.
	DeleteExpiredOAuthStates(ctx context.Context, now time.Time) error
	// SignInWithIdentity returns the user for a verified identity, linking or
	// creating one as the sign-in rules allow. allowCreate says whether a new
	// account may be created.
	SignInWithIdentity(ctx context.Context, provider string, id GoogleIdentity, now time.Time, allowCreate bool) (User, error)
	// AcceptInviteWithIdentity accepts the pending invitation inviteID for the
	// account that the verified identity signs in to, creating or linking that
	// account by the invitation's email as the invitation rules allow. Nothing
	// is written when it returns an error. allowSignup is false when invite
	// sign-up is switched off; a new account or a signup_pending takeover is then
	// ErrInviteSignupDisabled. newUserID is the id of an account created here.
	AcceptInviteWithIdentity(ctx context.Context, provider string, id GoogleIdentity, inviteID string, now time.Time, newUserID string, allowSignup bool) (InviteAcceptance, error)
}

// maxReturnToBytes bounds return_to; a longer value is refused.
const maxReturnToBytes = 1024

// ValidateReturnTo checks the local path a sign-in returns to. An empty value
// is allowed and becomes "/". Otherwise the value must be a single-slash path:
// it starts with "/", its second character is neither "/" nor "\", it has no
// backslash, no control characters, is at most 1024 bytes, and has no scheme or
// host. The returned value is the value to store.
func ValidateReturnTo(value string) (string, bool) {
	if value == "" {
		return "/", true
	}
	if len(value) > maxReturnToBytes || !utf8.ValidString(value) {
		return "", false
	}
	if value[0] != '/' {
		return "", false
	}
	if len(value) > 1 && (value[1] == '/' || value[1] == '\\') {
		return "", false
	}
	if strings.ContainsRune(value, '\\') {
		return "", false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" {
		return "", false
	}
	return value, true
}

// ValidRedirectURI reports whether value may be a configured Google redirect
// URI: an absolute https URL, or http for the host localhost or 127.0.0.1. It
// has no fragment and no userinfo.
func ValidRedirectURI(value string) bool {
	if value == "" || strings.Contains(value, "#") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	switch parsed.Scheme {
	case "https":
		return true
	case "http":
		host := parsed.Hostname()
		return host == "localhost" || host == "127.0.0.1"
	default:
		return false
	}
}

// GoogleEmailIsAuthoritative reports whether a Google email may be used to
// find or create an account. The email must be verified and non-empty, and its
// domain must be gmail.com or the identity's hosted domain (the Google Workspace
// domain), compared case-insensitively.
func GoogleEmailIsAuthoritative(id GoogleIdentity) bool {
	if !id.EmailVerified {
		return false
	}
	email := NormalizeEmail(id.Email)
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return false
	}
	emailDomain := email[at+1:]
	if emailDomain == "gmail.com" {
		return true
	}
	hosted := strings.ToLower(strings.TrimSpace(id.HostedDomain))
	return hosted != "" && hosted == emailDomain
}
