// Package searchpath forces the PostgreSQL search_path of an orbit-auth
// connection. The application connects with the schemas that hold its tables
// first, so unqualified names resolve without each query being qualified.
// Error messages never include the DSN: it may carry a password.
package searchpath

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
)

// Value is the search_path every orbit-auth connection uses. Schemas that do
// not exist yet are skipped by PostgreSQL, so before the schema migration
// everything still resolves to public.
const Value = "identity,tenancy,platform,public"

// Apply returns dsn with search_path forced to Value. A postgres:// or
// postgresql:// URL gets a search_path query parameter, replacing any existing
// one. A keyword/value DSN loses any existing search_path pair and gets a
// quoted one appended.
func Apply(dsn string) (string, error) {
	trimmed := strings.TrimSpace(dsn)
	if trimmed == "" {
		return "", errors.New("searchpath: database DSN is empty")
	}
	if hasURLScheme(trimmed) {
		return applyURL(trimmed)
	}
	return applyKeyword(trimmed)
}

func hasURLScheme(dsn string) bool {
	lower := strings.ToLower(dsn)
	return strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://")
}

func applyURL(dsn string) (string, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		// url.Parse errors quote the whole URL, password included, so the
		// underlying error is dropped.
		return "", errors.New("searchpath: database URL is malformed")
	}
	query := parsed.Query()
	query.Set("search_path", Value)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// applyKeyword rebuilds a keyword/value DSN. Every pair except search_path
// keeps its original text; the new pair is appended last.
func applyKeyword(dsn string) (string, error) {
	var kept []string
	n := len(dsn)
	i := 0
	for {
		for i < n && isSpace(dsn[i]) {
			i++
		}
		if i >= n {
			break
		}
		start := i
		for i < n && dsn[i] != '=' && !isSpace(dsn[i]) {
			i++
		}
		key := dsn[start:i]
		if key == "" {
			return "", errors.New("searchpath: keyword/value DSN has an empty keyword")
		}
		for i < n && isSpace(dsn[i]) {
			i++
		}
		if i >= n || dsn[i] != '=' {
			return "", errors.New("searchpath: keyword/value DSN has a keyword without a value")
		}
		i++
		for i < n && isSpace(dsn[i]) {
			i++
		}
		if i < n && dsn[i] == '\'' {
			i++
			for i < n && dsn[i] != '\'' {
				if dsn[i] == '\\' && i+1 < n {
					i++
				}
				i++
			}
			if i >= n {
				return "", errors.New("searchpath: keyword/value DSN has an unterminated quoted value")
			}
			i++
		} else {
			for i < n && !isSpace(dsn[i]) {
				i++
			}
		}
		if key != "search_path" {
			kept = append(kept, dsn[start:i])
		}
	}
	kept = append(kept, "search_path='"+Value+"'")
	return strings.Join(kept, " "), nil
}

func isSpace(b byte) bool {
	return unicode.IsSpace(rune(b))
}
