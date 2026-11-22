package domain

import (
	"errors"
	"testing"
)

var testAcceptHosts = []string{"app.example.com", "localhost"}

func TestResolveAcceptURL(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		slug  string
		hosts []string
		want  string
		err   bool
	}{
		{name: "empty is no url", raw: "", slug: "acme", hosts: testAcceptHosts, want: ""},
		{name: "blank is no url", raw: "   ", slug: "acme", hosts: testAcceptHosts, want: ""},
		{name: "plain allowed host", raw: "https://app.example.com/invite", slug: "acme", hosts: testAcceptHosts,
			want: "https://app.example.com/invite"},
		{name: "slug placeholder replaced in host", raw: "https://{slug}.app.example.com/invite", slug: "acme", hosts: testAcceptHosts,
			want: "https://acme.app.example.com/invite"},
		{name: "subdomain of allowed host", raw: "https://{slug}.app.example.com/invite", slug: "acme-2", hosts: testAcceptHosts,
			want: "https://acme-2.app.example.com/invite"},
		{name: "wrong host", raw: "https://evil.example.com/invite", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "suffix trick without dot boundary", raw: "https://evilapp.example.com/invite", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "allowed host as prefix of evil domain", raw: "https://app.example.com.evil.com/invite", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "userinfo", raw: "https://app.example.com@evil.com/invite", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "userinfo with slug", raw: "https://{slug}@app.example.com/invite", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "query", raw: "https://app.example.com/invite?x=1", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "empty query", raw: "https://app.example.com/invite?", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "fragment", raw: "https://app.example.com/invite#t", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "port on non-localhost", raw: "https://app.example.com:8443/invite", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "http non-localhost", raw: "http://app.example.com/invite", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "http localhost", raw: "http://localhost/invite", slug: "acme", hosts: testAcceptHosts, want: "http://localhost/invite"},
		{name: "http localhost with port", raw: "http://localhost:10010/invite", slug: "acme", hosts: testAcceptHosts, want: "http://localhost:10010/invite"},
		{name: "https localhost with port", raw: "https://localhost:3000/invite", slug: "acme", hosts: testAcceptHosts, want: "https://localhost:3000/invite"},
		{name: "localhost subdomain", raw: "http://{slug}.localhost:3000/invite", slug: "acme", hosts: testAcceptHosts,
			want: "http://acme.localhost:3000/invite"},
		{name: "uppercase host matches and is lower-cased", raw: "HTTPS://{slug}.App.Example.Com/Invite", slug: "acme", hosts: testAcceptHosts,
			want: "https://acme.app.example.com/Invite"},
		{name: "allowed host entries are case-insensitive", raw: "https://app.example.com/invite", slug: "acme", hosts: []string{"  App.Example.Com "},
			want: "https://app.example.com/invite"},
		{name: "placeholder in path refused", raw: "https://app.example.com/{slug}/invite", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "placeholder in query refused", raw: "https://app.example.com/invite?s={slug}", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "empty slug with placeholder refused", raw: "https://{slug}.app.example.com/invite", slug: "", hosts: testAcceptHosts, err: true},
		{name: "empty label after replacement refused", raw: "https://{slug}.app.example.com/invite", slug: "", hosts: []string{"example.com"}, err: true},
		{name: "no allowed hosts configured", raw: "https://app.example.com/invite", slug: "acme", hosts: nil, err: true},
		{name: "empty allowed entries only", raw: "https://app.example.com/invite", slug: "acme", hosts: []string{"", " "}, err: true},
		{name: "missing scheme", raw: "app.example.com/invite", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "javascript scheme", raw: "javascript://app.example.com/invite", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "ftp scheme", raw: "ftp://app.example.com/invite", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "space in path", raw: "https://app.example.com/in vite", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "ipv6 literal", raw: "https://[::1]/invite", slug: "acme", hosts: []string{"::1"}, err: true},
		{name: "empty port", raw: "http://localhost:/invite", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "non-numeric port on localhost", raw: "http://localhost:abc/invite", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "newline in path", raw: "https://app.example.com/invite\nhttps://evil.example", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "quote in path", raw: "https://app.example.com/in\"vite", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "angle bracket in path", raw: "https://app.example.com/<b>", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "percent-encoded host", raw: "https://evil.example%2f.app.example.com/invite", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "backslash in authority", raw: "https://evil.example\\.app.example.com/invite", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "non-ascii host", raw: "https://ａ.app.example.com/invite", slug: "acme", hosts: testAcceptHosts, err: true},
		{name: "no host", raw: "https:///invite", slug: "acme", hosts: testAcceptHosts, err: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveAcceptURL(tc.raw, tc.slug, tc.hosts)
			if tc.err {
				if !errors.Is(err, ErrInvalidAcceptURL) {
					t.Fatalf("ResolveAcceptURL(%q) = %q, %v; want ErrInvalidAcceptURL", tc.raw, got, err)
				}
				if got != "" {
					t.Fatalf("ResolveAcceptURL(%q) returned %q with an error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveAcceptURL(%q): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("ResolveAcceptURL(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
