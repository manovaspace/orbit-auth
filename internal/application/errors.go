package application

import "errors"

var (
	ErrEmailOTPDisabled  = errors.New("email OTP is disabled")
	ErrMobileOTPDisabled = errors.New("mobile OTP is disabled")
)
