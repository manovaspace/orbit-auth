package application

import "errors"

var (
	ErrEmailOTPDisabled  = errors.New("email OTP is disabled")
	ErrMobileOTPDisabled = errors.New("mobile OTP is disabled")
	// ErrSignupDisabled is returned by the sign-up use cases while
	// AUTH_SIGNUP is not open. Nothing is written.
	ErrSignupDisabled = errors.New("sign-up is disabled")
	// ErrPasswordRequired is the existing password rule: a sign-up password
	// must be present. There is no separate composition policy in this service.
	ErrPasswordRequired = errors.New("password is required")
	// ErrInviteSignupDisabled is returned by AcceptInviteWithSignup while
	// AUTH_INVITE_SIGNUP is off. Nothing is written.
	ErrInviteSignupDisabled = errors.New("invite sign-up is disabled")
	// ErrPasswordTooShort is the minimum for invite sign-up: at least eight
	// characters after trimming. The other sign-up paths keep ErrPasswordRequired.
	ErrPasswordTooShort = errors.New("password must be at least 8 characters")
	// ErrAccountExists is returned by AcceptInviteWithSignup when an account
	// already uses the invited mobile number. Nothing is written. The person
	// must sign in and accept the invitation with AcceptInvite instead.
	ErrAccountExists = errors.New("account exists")
	// ErrInvalidOTPCode and ErrTooManyOTPAttempts are the generic OTP
	// failures shared by login and sign-up verification.
	ErrInvalidOTPCode     = errors.New("invalid or expired code")
	ErrTooManyOTPAttempts = errors.New("too many attempts")
)
