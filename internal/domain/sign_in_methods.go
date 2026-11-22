package domain

import (
	"context"
	"errors"
	"time"
)

// Sign-in method errors. The gRPC adapter maps each one to a fixed status.
var (
	// ErrNoPasswordIdentifier is returned by SetPassword when the account has
	// no verified email, no mobile number and no username to sign in with.
	ErrNoPasswordIdentifier = errors.New("account needs a verified email, a mobile number or a username to sign in with a password")
	// ErrLastSignInMethod is returned by UnlinkGoogle when no password with a
	// usable identifier would remain.
	ErrLastSignInMethod = errors.New("the last sign-in method cannot be removed")
)

// GoogleLink is the Google identity linked to an account, as its owner sees it.
// The subject is never part of it.
type GoogleLink struct {
	Email       string
	LinkedAt    time.Time
	LastLoginAt time.Time
}

// SignInMethods is what the owner of an account sees about how it signs in.
// It never carries a password hash, a token or a Google subject.
type SignInMethods struct {
	HasPassword    bool
	Google         *GoogleLink
	Username       string
	Email          string
	EmailVerified  bool
	Mobile         string
	MobileVerified bool
}

// PasswordIdentifierUsable reports whether a password can sign the account in:
// a verified email, a mobile number or a username. It is the identifier half
// of the last-method rule; HasUsablePassword adds the password itself.
func PasswordIdentifierUsable(user User) bool {
	return (user.Email != "" && user.EmailVerified) || user.Mobile != "" || user.Username != ""
}

// HasUsablePassword reports whether the account has a password and an
// identifier that a password sign-in can use.
func HasUsablePassword(user User) bool {
	return user.PasswordHash != "" && PasswordIdentifierUsable(user)
}

// SignInMethodStore persists the changes an account owner makes to how the
// account signs in. Every method locks the account row, and every write that
// changes the account writes its audit row in the same transaction.
type SignInMethodStore interface {
	// GoogleLinkForUser returns the Google identity linked to userID. found is
	// false when there is none.
	GoogleLinkForUser(ctx context.Context, provider, userID string) (link GoogleLink, found bool, err error)
	// LinkGoogleIdentity links a verified Google subject to the active account
	// userID and writes identity.linked. A subject linked to another account is
	// ErrGoogleIdentityTaken; an account that already has a Google identity, or
	// a subject it already holds, is ErrGoogleAlreadyLinked. It returns the
	// Google email that was linked.
	LinkGoogleIdentity(ctx context.Context, provider string, id GoogleIdentity, userID string, at time.Time) (linkedEmail string, err error)
	// UnlinkGoogleIdentity deletes the Google identity of userID and writes
	// identity.unlinked. It is ErrGoogleNotLinked when there is none, and
	// ErrLastSignInMethod when the account would have no usable password left.
	UnlinkGoogleIdentity(ctx context.Context, provider, userID string, by AuditActor, at time.Time) error
	// SetPassword stores passwordHash for the active account userID, revokes
	// every live refresh token of the account except keepTokenHash (empty keeps
	// none) and writes password.set with first. The caller has already checked
	// the current password and the identifier.
	SetPassword(ctx context.Context, userID, passwordHash, keepTokenHash string, first bool, by AuditActor, at time.Time) error
	// RevokeSessions revokes every live refresh token of userID except
	// keepTokenHash (empty keeps none) and writes sessions.revoked.
	RevokeSessions(ctx context.Context, userID, keepTokenHash string, by AuditActor, at time.Time) error
}
