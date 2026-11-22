package domain

import (
	"errors"
	"strings"
)

// ErrInvalidAcceptURL reports an accept_url that is malformed, not https (http
// only for localhost), carries userinfo, a query or a fragment, places
// "{slug}" outside the host, or whose host is not one of the allowed accept
// hosts. Callers map it to InvalidArgument "invalid accept_url".
var ErrInvalidAcceptURL = errors.New("invalid accept_url")

// acceptSlugPlaceholder is replaced with the organization's slug, and only in
// the host.
const acceptSlugPlaceholder = "{slug}"

// ResolveAcceptURL validates an optional acceptance-page URL and returns it with
// the organization slug in place of "{slug}". An empty raw value is no URL and
// returns "", nil. Otherwise raw must be an absolute https URL (http only for
// localhost and *.localhost) with no userinfo, query or fragment. Its host,
// lower-cased, must equal or end with "."+entry for one of allowedHosts
// (compared case-insensitively, on label boundaries). A port is allowed only for
// localhost hosts and is ignored for matching. With no allowedHosts a non-empty
// raw value is refused.
func ResolveAcceptURL(raw, slug string, allowedHosts []string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	// Printable ASCII only, so nothing in the value can break out of the line or
	// the attribute an email puts it in.
	for _, r := range raw {
		if r <= ' ' || r > '~' || strings.ContainsRune("?#\"'<>`", r) {
			return "", ErrInvalidAcceptURL
		}
	}
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return "", ErrInvalidAcceptURL
	}
	authority, path := rest, ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		authority, path = rest[:i], rest[i:]
	}
	if strings.Contains(path, acceptSlugPlaceholder) || strings.ContainsAny(path, "{}\\ \t") {
		return "", ErrInvalidAcceptURL
	}
	if strings.Contains(authority, acceptSlugPlaceholder) {
		slug = strings.TrimSpace(slug)
		if slug == "" {
			return "", ErrInvalidAcceptURL
		}
		authority = strings.ReplaceAll(authority, acceptSlugPlaceholder, slug)
	}
	host, port := splitAcceptAuthority(authority)
	host = strings.ToLower(host)
	if host == "" || !validAcceptHostChars(host) || strings.Contains(host, "..") || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return "", ErrInvalidAcceptURL
	}
	local := host == "localhost" || strings.HasSuffix(host, ".localhost")
	switch strings.ToLower(scheme) {
	case "https":
	case "http":
		if !local {
			return "", ErrInvalidAcceptURL
		}
	default:
		return "", ErrInvalidAcceptURL
	}
	if port != "" && !local {
		return "", ErrInvalidAcceptURL
	}
	if !acceptHostAllowed(host, allowedHosts) {
		return "", ErrInvalidAcceptURL
	}
	if port != "" {
		host += ":" + port
	}
	return strings.ToLower(scheme) + "://" + host + path, nil
}

// splitAcceptAuthority splits "host" or "host:port". A bracketed IPv6 literal or
// a second colon is not an accept host and yields an empty host, which the
// caller refuses.
func splitAcceptAuthority(authority string) (string, string) {
	if strings.Count(authority, ":") > 1 {
		return "", ""
	}
	host, port, found := strings.Cut(authority, ":")
	if !found {
		return host, ""
	}
	if port == "" {
		return "", ""
	}
	for _, r := range port {
		if r < '0' || r > '9' {
			return "", ""
		}
	}
	return host, port
}

// validAcceptHostChars reports whether a lower-cased host uses only letters,
// digits, dots and hyphens. It refuses userinfo, brackets and percent-encoding.
func validAcceptHostChars(host string) bool {
	for _, r := range host {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '.' && r != '-' {
			return false
		}
	}
	return true
}

// acceptHostAllowed reports whether host equals or is a subdomain of one of the
// allowed entries. Entries are trimmed, lower-cased and empty entries ignored.
func acceptHostAllowed(host string, allowedHosts []string) bool {
	for _, entry := range allowedHosts {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}
