package testutil

import "testing"

func TestDatabaseName(t *testing.T) {
	tests := []struct {
		url     string
		want    string
		wantErr bool
	}{
		{"postgres://postgres:postgres@localhost:5432/test?sslmode=disable", "test", false},
		{"postgres://postgres:postgres@localhost:5432/test_auth", "test_auth", false},
		{"postgres://postgres:postgres@localhost:5432/auth", "auth", false},
		{"postgres://postgres:postgres@localhost:5432/", "", true},
	}
	for _, tc := range tests {
		got, err := databaseName(tc.url)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("databaseName(%q) = %q, want error", tc.url, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("databaseName(%q) = %q, %v, want %q", tc.url, got, err, tc.want)
		}
	}
}

func TestWithDatabaseName(t *testing.T) {
	tests := []struct {
		name string
		url  string
		db   string
		want string
	}{
		{
			name: "replaces database and keeps query",
			url:  "postgres://postgres:postgres@localhost:5432/test?sslmode=disable",
			db:   "test_auth_abc",
			want: "postgres://postgres:postgres@localhost:5432/test_auth_abc?sslmode=disable",
		},
		{
			name: "URL without query",
			url:  "postgres://postgres:postgres@localhost:5432/test",
			db:   "test_auth_abc",
			want: "postgres://postgres:postgres@localhost:5432/test_auth_abc",
		},
		{
			name: "keeps an existing search_path untouched",
			url:  "postgres://postgres:postgres@localhost:5432/test?search_path=public",
			db:   "test_auth_abc",
			want: "postgres://postgres:postgres@localhost:5432/test_auth_abc?search_path=public",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := withDatabaseName(tc.url, tc.db)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("withDatabaseName = %q, want %q", got, tc.want)
			}
		})
	}
}
