package searchpath

import (
	"strings"
	"testing"
)

func TestApply(t *testing.T) {
	const scoped = "identity%2Ctenancy%2Cplatform%2Cpublic"
	const quoted = "search_path='identity,tenancy,platform,public'"
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{
			name: "URL without params",
			dsn:  "postgres://u:p@localhost:5432/auth",
			want: "postgres://u:p@localhost:5432/auth?search_path=" + scoped,
		},
		{
			name: "URL with sslmode",
			dsn:  "postgres://u:p@localhost:5432/auth?sslmode=disable",
			want: "postgres://u:p@localhost:5432/auth?search_path=" + scoped + "&sslmode=disable",
		},
		{
			name: "URL that already has search_path=public",
			dsn:  "postgresql://u:p@localhost:5432/auth?search_path=public&sslmode=disable",
			want: "postgresql://u:p@localhost:5432/auth?search_path=" + scoped + "&sslmode=disable",
		},
		{
			name: "keyword value DSN",
			dsn:  "host=localhost port=5432 dbname=auth sslmode=disable",
			want: "host=localhost port=5432 dbname=auth sslmode=disable " + quoted,
		},
		{
			name: "keyword value with existing search_path",
			dsn:  "host=localhost search_path=public dbname=auth",
			want: "host=localhost dbname=auth " + quoted,
		},
		{
			name: "keyword value with quoted existing search_path and spaces",
			dsn:  "dbname='my db' search_path='public, other' host=localhost",
			want: "dbname='my db' host=localhost " + quoted,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Apply(tc.dsn)
			if err != nil {
				t.Fatalf("Apply(%q): %v", tc.dsn, err)
			}
			if got != tc.want {
				t.Fatalf("Apply(%q) = %q, want %q", tc.dsn, got, tc.want)
			}
		})
	}
}

func TestApplyValueKeepsPublicFirstForTheMigrator(t *testing.T) {
	got, err := ApplyValue("postgres://u:p@localhost:5432/auth?sslmode=disable", MigrationValue)
	if err != nil {
		t.Fatalf("ApplyValue: %v", err)
	}
	const want = "postgres://u:p@localhost:5432/auth?search_path=public%2Cidentity%2Ctenancy%2Cplatform&sslmode=disable"
	if got != want {
		t.Fatalf("ApplyValue = %q, want %q", got, want)
	}
	if !strings.HasPrefix(MigrationValue, "public,") {
		t.Fatalf("MigrationValue = %q, want public first", MigrationValue)
	}
}

func TestApplyEmpty(t *testing.T) {
	for _, dsn := range []string{"", "   "} {
		if _, err := Apply(dsn); err == nil {
			t.Fatalf("Apply(%q) = nil error, want error", dsn)
		}
	}
}

func TestApplyErrorsDoNotEchoDSN(t *testing.T) {
	secret := "s3cret-pass"
	for _, dsn := range []string{
		"postgres://u:" + secret + "@host:5432/auth\x7f",
		"host=localhost password=" + secret + " dbname='unterminated",
		"host=localhost =" + secret,
	} {
		_, err := Apply(dsn)
		if err == nil {
			continue
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("Apply error leaks the DSN secret: %v", err)
		}
	}
}
