package application

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
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

// GoogleSignInResult is what CompleteGoogleSignIn returns. Access and Refresh
// are empty only with the error. For an invitation sign-in, InviteID, OrgID,
// OrgSlug, Role and AlreadyMember describe the accepted invitation; they are
// empty and false for a plain sign-in. When the organization is suspended the
// tokens carry no organization, as a SelectOrg into it is refused.
type GoogleSignInResult struct {
	Access        string
	Refresh       string
	Expires       time.Time
	ReturnTo      string
	InviteID      string
	OrgID         string
	OrgSlug       string
	Role          domain.MembershipRole
	AlreadyMember bool
}

// GoogleLinkResult is what CompleteGoogleLink returns: the return path given to
// StartGoogleLink and the Google email that was linked. It carries no tokens.
type GoogleLinkResult struct {
	ReturnTo string
	Email    string
}

// StartGoogleSignIn checks redirectURI against the allowlist and returnTo
// against the local-path rule, then creates a single-use state and returns the
// authorization URL and the state. Only sha256(state) is stored. Expired states
// are deleted on a best-effort basis.
//
// A non-empty inviteToken makes the sign-in accept that invitation. The token
// must resolve to a pending, unexpired invitation (ErrInvalidInvite otherwise),
// and a phone invitation is refused with ErrInviteNeedsPassword. Only the
// invitation id is stored with the state.
func (s *Service) StartGoogleSignIn(ctx context.Context, redirectURI, returnTo, inviteToken string) (authorizationURL, state string, err error) {
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
	st := domain.OAuthState{RedirectURI: redirectURI, ReturnTo: returnTo, Purpose: domain.OAuthPurposeSignIn}
	if token := strings.TrimSpace(inviteToken); token != "" {
		invite, err := s.pendingInvite(ctx, token, s.clock.Now())
		if err != nil {
			return "", "", err
		}
		if invite.Phone != "" {
			return "", "", domain.ErrInviteNeedsPassword
		}
		st.Purpose = domain.OAuthPurposeInvite
		st.InviteID = invite.ID
	}
	return s.startGoogleState(ctx, st)
}

// startGoogleState stores a new single-use state for st and returns the
// authorization URL and the state.
func (s *Service) startGoogleState(ctx context.Context, st domain.OAuthState) (authorizationURL, state string, err error) {
	state, err = s.secrets.NewSecret()
	if err != nil {
		return "", "", err
	}
	verifier := googlePKCEVerifier(s.google.StateKey, state)
	nonce := googleNonce(s.google.StateKey, state)

	now := s.clock.Now()
	_ = s.google.States.DeleteExpiredOAuthStates(ctx, now)
	if err := s.google.States.CreateOAuthState(ctx, googleStateHash(state), st, now.Add(domain.OAuthStateTTL)); err != nil {
		return "", "", err
	}
	authorizationURL = s.google.Provider.AuthorizationURL(st.RedirectURI, state, nonce, pkceChallenge(verifier))
	return authorizationURL, state, nil
}

// CompleteGoogleSignIn consumes the state, exchanges the code, signs the
// identity in and issues a token pair. The state is consumed before the
// exchange, so it is single use even when the exchange fails. A state of the
// link purpose is refused with ErrInvalidGoogleState; only CompleteGoogleLink
// consumes it.
func (s *Service) CompleteGoogleSignIn(ctx context.Context, state, code string) (GoogleSignInResult, error) {
	if s.google == nil {
		return GoogleSignInResult{}, domain.ErrGoogleDisabled
	}
	if state == "" || code == "" || len(state) > maxGoogleStateBytes || len(code) > maxGoogleCodeBytes {
		return GoogleSignInResult{}, domain.ErrInvalidGoogleState
	}
	now := s.clock.Now()
	st, err := s.google.States.ConsumeOAuthState(ctx, googleStateHash(state), now)
	if err != nil {
		return GoogleSignInResult{}, err
	}
	if st.Purpose != domain.OAuthPurposeSignIn && st.Purpose != domain.OAuthPurposeInvite {
		return GoogleSignInResult{}, domain.ErrInvalidGoogleState
	}
	identity, err := s.exchangeGoogle(ctx, st, state, code)
	if err != nil {
		return GoogleSignInResult{}, err
	}
	if st.Purpose == domain.OAuthPurposeInvite {
		return s.completeInviteSignIn(ctx, st, identity, now)
	}
	user, err := s.google.States.SignInWithIdentity(ctx, domain.GoogleProviderName, identity, now, s.signupOpen)
	if err != nil {
		return GoogleSignInResult{}, err
	}
	// Pending, suspended and any other non-active account get no tokens, the same
	// as the password and OTP paths.
	if user.Status != domain.UserActive {
		return GoogleSignInResult{}, domain.ErrGoogleAccountNotAllowed
	}
	_ = s.users.UpdateLastLogin(ctx, user.ID)
	s.logger("google_login", "user_id", user.ID)
	access, refresh, expires, err := s.issueTokens(ctx, user.ID)
	if err != nil {
		return GoogleSignInResult{}, err
	}
	return GoogleSignInResult{Access: access, Refresh: refresh, Expires: expires, ReturnTo: st.ReturnTo}, nil
}

// completeInviteSignIn accepts the invitation of an invite-purpose state for the
// verified identity, then issues the tokens for the invitation's organization. A
// suspended organization gets tokens without the organization claim, as a
// SelectOrg into it is refused; the membership stays accepted.
func (s *Service) completeInviteSignIn(ctx context.Context, st domain.OAuthState, identity domain.GoogleIdentity, now time.Time) (GoogleSignInResult, error) {
	if s.orgs == nil {
		return GoogleSignInResult{}, fmt.Errorf("organization store is not configured")
	}
	acceptance, err := s.google.States.AcceptInviteWithIdentity(ctx, domain.GoogleProviderName, identity, st.InviteID, now, s.ids.NewID(), !s.inviteSignupOff)
	if err != nil {
		if errors.Is(err, domain.ErrInviteNotFound) {
			return GoogleSignInResult{}, domain.ErrInvalidInvite
		}
		return GoogleSignInResult{}, err
	}
	if acceptance.User.Status != domain.UserActive {
		return GoogleSignInResult{}, domain.ErrGoogleAccountNotAllowed
	}
	org, err := s.orgs.GetOrg(ctx, acceptance.OrgID)
	if err != nil {
		return GoogleSignInResult{}, err
	}
	_ = s.users.UpdateLastLogin(ctx, acceptance.User.ID)
	s.logger("google_invite_accepted", "org_id", acceptance.OrgID, "invite_id", st.InviteID, "user_id", acceptance.User.ID, "already_member", acceptance.AlreadyMember)
	orgID := acceptance.OrgID
	if org.Status == domain.OrgStatusSuspended {
		orgID = ""
	}
	access, refresh, expires, err := s.issueTokensForOrg(ctx, acceptance.User.ID, orgID)
	if err != nil {
		return GoogleSignInResult{}, err
	}
	return GoogleSignInResult{
		Access:        access,
		Refresh:       refresh,
		Expires:       expires,
		ReturnTo:      st.ReturnTo,
		InviteID:      st.InviteID,
		OrgID:         acceptance.OrgID,
		OrgSlug:       org.Slug,
		Role:          acceptance.Role,
		AlreadyMember: acceptance.AlreadyMember,
	}, nil
}

// StartGoogleLink starts linking a Google identity to the active account
// userID. The redirect and return_to checks are the sign-in ones. An account
// that already has a Google identity is ErrGoogleAlreadyLinked.
func (s *Service) StartGoogleLink(ctx context.Context, userID, redirectURI, returnTo string) (authorizationURL, state string, err error) {
	userID = strings.TrimSpace(userID)
	if _, err := s.activeUser(ctx, userID); err != nil {
		return "", "", err
	}
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
	if s.account == nil {
		return "", "", fmt.Errorf("sign-in method store is not configured")
	}
	_, linked, err := s.account.GoogleLinkForUser(ctx, domain.GoogleProviderName, userID)
	if err != nil {
		return "", "", err
	}
	if linked {
		return "", "", domain.ErrGoogleAlreadyLinked
	}
	return s.startGoogleState(ctx, domain.OAuthState{
		RedirectURI: redirectURI,
		ReturnTo:    returnTo,
		Purpose:     domain.OAuthPurposeLink,
		UserID:      userID,
	})
}

// CompleteGoogleLink links the Google identity of code to the active account
// userID. The state must be a link state of that same account; any other state
// is ErrInvalidGoogleState, and it is consumed either way. No tokens are issued.
func (s *Service) CompleteGoogleLink(ctx context.Context, userID, state, code string) (GoogleLinkResult, error) {
	userID = strings.TrimSpace(userID)
	if _, err := s.activeUser(ctx, userID); err != nil {
		return GoogleLinkResult{}, err
	}
	if s.google == nil {
		return GoogleLinkResult{}, domain.ErrGoogleDisabled
	}
	if state == "" || code == "" || len(state) > maxGoogleStateBytes || len(code) > maxGoogleCodeBytes {
		return GoogleLinkResult{}, domain.ErrInvalidGoogleState
	}
	if s.account == nil {
		return GoogleLinkResult{}, fmt.Errorf("sign-in method store is not configured")
	}
	now := s.clock.Now()
	st, err := s.google.States.ConsumeOAuthState(ctx, googleStateHash(state), now)
	if err != nil {
		return GoogleLinkResult{}, err
	}
	if st.Purpose != domain.OAuthPurposeLink || st.UserID != userID {
		return GoogleLinkResult{}, domain.ErrInvalidGoogleState
	}
	identity, err := s.exchangeGoogle(ctx, st, state, code)
	if err != nil {
		return GoogleLinkResult{}, err
	}
	email, err := s.account.LinkGoogleIdentity(ctx, domain.GoogleProviderName, identity, userID, now)
	if err != nil {
		return GoogleLinkResult{}, err
	}
	s.logger("google_linked", "user_id", userID)
	return GoogleLinkResult{ReturnTo: st.ReturnTo, Email: email}, nil
}

// exchangeGoogle trades the code of a consumed state for its verified identity.
// The subject must be present.
func (s *Service) exchangeGoogle(ctx context.Context, st domain.OAuthState, state, code string) (domain.GoogleIdentity, error) {
	verifier := googlePKCEVerifier(s.google.StateKey, state)
	nonce := googleNonce(s.google.StateKey, state)
	identity, err := s.google.Provider.Exchange(ctx, st.RedirectURI, code, verifier, nonce)
	if err != nil {
		return domain.GoogleIdentity{}, domain.ErrGoogleExchangeFailed
	}
	if identity.Subject == "" {
		return domain.GoogleIdentity{}, domain.ErrGoogleAccountNotAllowed
	}
	return identity, nil
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
