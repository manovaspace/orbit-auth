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
	// ErrInvalidOTPCode and ErrTooManyOTPAttempts are the generic OTP
	// failures shared by login and sign-up verification.
	ErrInvalidOTPCode     = errors.New("invalid or expired code")
	ErrTooManyOTPAttempts = errors.New("too many attempts")
)
