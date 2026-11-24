// Package searchpath forces the PostgreSQL search_path of an orbit-auth
// connection. The search_path lists the schemas that hold the tables (identity,
// tenancy, platform), then public. Application queries are schema-qualified.
// The search_path keeps unqualified names working for the orbit-kit outbox
// relay, which accepts only a plain table name, and for older migrations.
// Error messages never include the DSN: it may carry a password.
package searchpath

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
)

// Value is the search_path every application connection uses. Schemas that do
// not exist yet are skipped by PostgreSQL, so before the schema migration
// everything still resolves to public.
const Value = "identity,tenancy,platform,public"

// MigrationValue is the search_path the migrator uses. goose looks up its
// version table through current_schema(), which is the first schema of the
// search_path that exists. With Value that is identity once migration 024 has
// created it, so the version table would be looked up in identity, not found
// there, and created again. The migrator therefore lists public first:
// goose_db_version stays in public, where the applied history is.
const MigrationValue = "public,identity,tenancy,platform"

// Apply returns dsn with search_path forced to Value. See ApplyValue.
func Apply(dsn string) (string, error) {
	return ApplyValue(dsn, Value)
}

// ApplyValue returns dsn with search_path forced to value. A postgres:// or
// postgresql:// URL gets a search_path query parameter, replacing any existing
// one. A keyword/value DSN loses any existing search_path pair and gets a
// quoted one appended.
func ApplyValue(dsn, value string) (string, error) {
	trimmed := strings.TrimSpace(dsn)
	if trimmed == "" {
		return "", errors.New("searchpath: database DSN is empty")
	}
	if hasURLScheme(trimmed) {
		return applyURL(trimmed, value)
	}
	return applyKeyword(trimmed, value)
}

func hasURLScheme(dsn string) bool {
	lower := strings.ToLower(dsn)
	return strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://")
}

func applyURL(dsn, value string) (string, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		// url.Parse errors quote the whole URL, password included, so the
		// underlying error is dropped.
		return "", errors.New("searchpath: database URL is malformed")
	}
	query := parsed.Query()
	query.Set("search_path", value)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// applyKeyword rebuilds a keyword/value DSN. Every pair except search_path
// keeps its original text; the new pair is appended last.
func applyKeyword(dsn, value string) (string, error) {
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
	kept = append(kept, "search_path='"+value+"'")
	return strings.Join(kept, " "), nil
}

func isSpace(b byte) bool {
	return unicode.IsSpace(rune(b))
}
