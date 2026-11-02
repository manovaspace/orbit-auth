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

func TestWithSearchPath(t *testing.T) {
	got, err := withSearchPath("postgres://postgres:postgres@localhost:5432/test?sslmode=disable", "auth_test_abc")
	if err != nil {
		t.Fatal(err)
	}
	want := "postgres://postgres:postgres@localhost:5432/test?search_path=auth_test_abc%2Cpublic&sslmode=disable"
	if got != want {
		t.Fatalf("withSearchPath = %q, want %q", got, want)
	}
}
