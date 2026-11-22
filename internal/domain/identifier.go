package domain

import (
	"errors"
	"regexp"
	"strings"
)

var (
	// ErrInvalidUsername reports a username outside the username rule.
	ErrInvalidUsername = errors.New("invalid username")
	// ErrUsernameTaken reports a username another account already holds.
	ErrUsernameTaken = errors.New("username is taken")
)

// usernamePattern is the whole username rule. A username starts with a letter
// and never contains '@' or '+', so it can never look like an email address or
// a mobile number.
var usernamePattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{2,31}$`)

// NormalizeUsername trims and lower-cases value and reports whether the result
// follows the username rule. The returned value is the stored form.
func NormalizeUsername(value string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if !usernamePattern.MatchString(normalized) {
		return "", false
	}
	return normalized, true
}

// LoginIdentifierKind says what a login identifier names.
type LoginIdentifierKind string

const (
	LoginIdentifierEmail    LoginIdentifierKind = "email"
	LoginIdentifierMobile   LoginIdentifierKind = "mobile"
	LoginIdentifierUsername LoginIdentifierKind = "username"
)

// ParseLoginIdentifier classifies a value typed into the sign-in form and
// returns its stored form. callingCode is the default country calling code,
// digits only, without "+"; an empty callingCode accepts no local numbers.
//
// A value with '@' is an email address, lower-cased and trimmed. Otherwise
// spaces, '-', '(' and ')' are dropped and the rest is a mobile number when it
// is E.164, when it has the international "00" prefix, or when it is a local
// number that the calling code completes. Anything else is a username, checked
// on the trimmed value as typed. Persian and Arabic-Indic digits are read as
// ASCII digits first.
func ParseLoginIdentifier(value, callingCode string) (LoginIdentifierKind, string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", "", false
	}
	ascii := asciiDigits(trimmed)
	if strings.Contains(ascii, "@") {
		email := NormalizeEmail(ascii)
		return LoginIdentifierEmail, email, true
	}

	stripped := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(ascii)
	if IsE164(stripped) {
		return LoginIdentifierMobile, stripped, true
	}
	if strings.HasPrefix(stripped, "00") && IsE164("+"+stripped[2:]) {
		return LoginIdentifierMobile, "+" + stripped[2:], true
	}
	if callingCode != "" && isASCIIDigits(stripped) {
		// One leading zero is the national trunk prefix: 09121234567 with
		// calling code 98 is +989121234567. "00" was handled above.
		if len(stripped) > 1 && stripped[0] == '0' && stripped[1] != '0' {
			if local := "+" + callingCode + stripped[1:]; IsE164(local) {
				return LoginIdentifierMobile, local, true
			}
		}
		if strings.HasPrefix(stripped, callingCode) && IsE164("+"+stripped) {
			return LoginIdentifierMobile, "+" + stripped, true
		}
	}

	if username, ok := NormalizeUsername(trimmed); ok {
		return LoginIdentifierUsername, username, true
	}
	return "", "", false
}

// asciiDigits maps Persian (U+06F0 to U+06F9) and Arabic-Indic (U+0660 to
// U+0669) digits to ASCII digits. Every other rune is kept.
func asciiDigits(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 0x06F0 && r <= 0x06F9:
			return '0' + (r - 0x06F0)
		case r >= 0x0660 && r <= 0x0669:
			return '0' + (r - 0x0660)
		default:
			return r
		}
	}, value)
}

// isASCIIDigits reports whether value is non-empty and only ASCII digits.
func isASCIIDigits(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}
