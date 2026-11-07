package domain

import (
	"errors"
	"regexp"
	"strings"
)

var (
	// ErrInvalidSlug reports an organization slug that is not 3 to 30
	// lowercase letters, digits and single hyphens, starting with a letter and
	// ending with a letter or digit, or that is reserved.
	ErrInvalidSlug = errors.New("invalid organization slug")
	// ErrSlugTaken reports a slug that another organization uses or has used.
	ErrSlugTaken = errors.New("organization slug is taken")
	// ErrSlugNotFound reports a slug no organization has ever used.
	ErrSlugNotFound = errors.New("organization slug not found")
)

// slugPattern is the whole slug rule except the reserved list and the double
// hyphen rule, which a regular expression states badly.
var slugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,28}[a-z0-9]$`)

// reservedSlugs are never allocated to an organization. They name platform
// hosts and roles that a tenant host such as <slug>.falak.manova.space must not
// be confused with, and words that read like official accounts.
var reservedSlugs = map[string]struct{}{
	"admin": {}, "administrator": {}, "api": {}, "app": {}, "assets": {}, "auth": {},
	"billing": {}, "cdn": {}, "dev": {}, "demo": {}, "docs": {}, "falak": {},
	"git": {}, "handbook": {}, "help": {}, "internal": {}, "login": {}, "logout": {},
	"mail": {}, "manova": {}, "orbit": {}, "root": {}, "signup": {}, "smtp": {},
	"sta": {}, "staging": {}, "static": {}, "status": {}, "support": {}, "system": {},
	"test": {}, "www": {},
}

// ValidateSlug checks the format and the reserved list. It does not check
// uniqueness.
func ValidateSlug(slug string) error {
	if !slugPattern.MatchString(slug) || strings.Contains(slug, "--") {
		return ErrInvalidSlug
	}
	if _, reserved := reservedSlugs[slug]; reserved {
		return ErrInvalidSlug
	}
	return nil
}

// SlugFromName derives a candidate slug from an organization name. It keeps
// ASCII letters and digits, turns every other run into one hyphen and trims
// hyphens. A name with no usable ASCII, for example a Persian name, gives "" and
// the caller falls back to FallbackSlug. The result is not guaranteed valid or
// free: callers pass it through ValidateSlug and handle ErrSlugTaken.
func SlugFromName(name string) string {
	var builder strings.Builder
	pendingHyphen := false
	for _, r := range strings.ToLower(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			if pendingHyphen && builder.Len() > 0 {
				builder.WriteByte('-')
			}
			pendingHyphen = false
			builder.WriteRune(r)
		default:
			pendingHyphen = true
		}
	}
	slug := builder.String()
	if len(slug) > 30 {
		slug = strings.TrimRight(slug[:30], "-")
	}
	return slug
}

// FallbackSlug builds a valid slug from an organization id: "org-" followed by
// the first twelve hex digits. It is the slug an organization gets when its name
// yields none, and the next candidate when a derived slug is taken.
func FallbackSlug(orgID string) string {
	hex := strings.ReplaceAll(strings.ToLower(orgID), "-", "")
	if len(hex) > 12 {
		hex = hex[:12]
	}
	return "org-" + hex
}
