package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"net"
	"net/http"
	"testing"
	"time"
)

func challengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))

	return base64.RawURLEncoding.EncodeToString(sum[:])
}

const validVerifier = "abcdefghijklmnopqrstuvwxyz0123456789-._~ABCDE"

func TestVerifyPKCE(t *testing.T) {
	challenge := challengeFor(validVerifier)

	if err := VerifyPKCE(challenge, "S256", validVerifier); err != nil {
		t.Errorf("the matching verifier was rejected: %v", err)
	}

	cases := map[string]struct {
		challenge, method, verifier string
	}{
		"wrong verifier":  {challenge, "S256", "wrong" + validVerifier},
		"plain rejected":  {validVerifier, "plain", validVerifier},
		"empty method":    {challenge, "", validVerifier},
		"verifier short":  {challenge, "S256", "tooshort"},
		"verifier absent": {challenge, "S256", ""},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := VerifyPKCE(tc.challenge, tc.method, tc.verifier); err == nil {
				t.Error("expected rejection")
			}
		})
	}

	t.Run("verifier over 128 chars", func(t *testing.T) {
		long := make([]byte, 129)
		for i := range long {
			long[i] = 'a'
		}

		if err := VerifyPKCE(challengeFor(string(long)), "S256", string(long)); err == nil {
			t.Error("expected rejection of an over-long verifier")
		}
	})
}

func TestValidateCodeChallenge(t *testing.T) {
	valid := challengeFor(validVerifier)

	if err := ValidateCodeChallenge(valid, "S256"); err != nil {
		t.Errorf("valid challenge rejected: %v", err)
	}

	cases := map[string][2]string{
		"missing challenge": {"", "S256"},
		"missing method":    {valid, ""},
		"plain method":      {valid, "plain"},
		"wrong length":      {"tooshort", "S256"},
		"not base64url":     {"!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!", "S256"},
		"padded base64":     {"cbLZ0nSpVQVsMHHFhpFvKMFxfB4gLJ2p3EJrTLTfKZ0=", "S256"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateCodeChallenge(tc[0], tc[1]); err == nil {
				t.Error("expected rejection")
			}
		})
	}
}

func TestNormalizeScopes(t *testing.T) {
	cases := map[string]struct {
		in      string
		want    string
		wantErr bool
	}{
		"empty defaults to resource scopes": {"", "read write", false},
		"single":                            {"read", "read", false},
		"both":                              {"read write", "read write", false},
		"offline access":                    {"read offline_access", "read offline_access", false},
		"deduplicated":                      {"read read write", "read write", false},
		"unknown scope":                     {"read admin", "", true},
		"only offline_access":               {"offline_access", "", true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := NormalizeScopes(tc.in)

			if tc.wantErr {
				if err == nil {
					t.Errorf("NormalizeScopes(%q) = %q, want an error", tc.in, got)
				}

				return
			}

			if err != nil {
				t.Fatalf("NormalizeScopes(%q): %v", tc.in, err)
			}

			if got != tc.want {
				t.Errorf("NormalizeScopes(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCanonicalResource(t *testing.T) {
	cases := map[string]struct {
		want    string
		wantErr bool
	}{
		"https://mcp.example.com":      {"https://mcp.example.com", false},
		"https://mcp.example.com/":     {"https://mcp.example.com", false},
		"https://mcp.example.com/mcp":  {"https://mcp.example.com/mcp", false},
		"https://MCP.Example.COM/mcp":  {"https://mcp.example.com/mcp", false},
		"HTTPS://mcp.example.com":      {"https://mcp.example.com", false},
		"https://mcp.example.com:8443": {"https://mcp.example.com:8443", false},
		"https://mcp.example.com#frag": {"", true},
		"mcp.example.com":              {"", true},
		"":                             {"", true},
		"/relative":                    {"", true},
	}

	for input, tc := range cases {
		got, err := CanonicalResource(input)

		if tc.wantErr {
			if err == nil {
				t.Errorf("CanonicalResource(%q) = %q, want an error", input, got)
			}

			continue
		}

		if err != nil {
			t.Errorf("CanonicalResource(%q): %v", input, err)

			continue
		}

		if got != tc.want {
			t.Errorf("CanonicalResource(%q) = %q, want %q", input, got, tc.want)
		}
	}
}

func TestValidateRedirectURI(t *testing.T) {
	cases := []struct {
		uri     string
		appType string
		wantErr bool
	}{
		// https is always fine.
		{"https://app.example.com/callback", "web", false},
		{"https://app.example.com/callback", "native", false},

		// Loopback over http is native-only.
		{"http://127.0.0.1:3000/callback", "native", false},
		{"http://localhost:3000/callback", "native", false},
		{"http://[::1]:3000/callback", "native", false},
		{"http://127.0.0.1:3000/callback", "web", true},

		// A non-loopback http redirect would ship the code in the clear.
		{"http://app.example.com/callback", "native", true},
		{"http://evil.example.com/callback", "web", true},

		// Private-use schemes are how mobile apps receive redirects.
		{"com.example.app://oauth/callback", "native", false},
		{"com.example.app:/oauth/callback", "native", false},
		{"com.example.app:/oauth/callback", "web", true},
		{"myapp:/callback", "native", true}, // not a reversed domain

		// Structural problems.
		{"https://app.example.com/callback#fragment", "native", true},
		{"/relative", "native", true},
		{"", "native", true},
	}

	for _, tc := range cases {
		err := ValidateRedirectURI(tc.uri, tc.appType)

		if tc.wantErr && err == nil {
			t.Errorf("ValidateRedirectURI(%q, %q) succeeded, want an error", tc.uri, tc.appType)
		}

		if !tc.wantErr && err != nil {
			t.Errorf("ValidateRedirectURI(%q, %q): %v", tc.uri, tc.appType, err)
		}
	}
}

func TestIsLoopbackOnly(t *testing.T) {
	cases := map[bool][][]string{
		true: {
			{"http://127.0.0.1:1234/cb"},
			{"http://localhost:1/cb", "http://127.0.0.1:2/cb"},
		},
		false: {
			{},
			{"https://app.example.com/cb"},
			{"http://127.0.0.1:1/cb", "https://app.example.com/cb"},
		},
	}

	for want, sets := range cases {
		for _, set := range sets {
			if got := IsLoopbackOnly(set); got != want {
				t.Errorf("IsLoopbackOnly(%v) = %v, want %v", set, got, want)
			}
		}
	}
}

// TestIsPublicIP guards the SSRF defence on Client ID Metadata Document fetches.
// A client picks the URL, so anything that resolves inward has to be refused.
func TestIsPublicIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.1.2.3", "::1", // loopback
		"10.0.0.1", "172.16.0.1", "192.168.1.1", // RFC 1918
		"169.254.169.254",      // cloud metadata service
		"fe80::1",              // link-local
		"100.64.0.1",           // carrier NAT / Tailscale
		"0.0.0.0",              // unspecified
		"fd00::1",              // IPv6 unique local
		"224.0.0.1", "ff02::1", // multicast
		"192.0.0.1",       // IETF protocol assignments
		"198.18.0.1",      // benchmarking
		"240.0.0.1",       // reserved
		"::ffff:10.0.0.1", // IPv4-mapped private address
	}

	for _, raw := range blocked {
		ip := net.ParseIP(raw)
		if ip == nil {
			t.Fatalf("test bug: %q is not an IP", raw)
		}

		if isPublicIP(ip) {
			t.Errorf("isPublicIP(%s) = true, want false; this address is reachable inward", raw)
		}
	}

	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"}

	for _, raw := range allowed {
		ip := net.ParseIP(raw)
		if !isPublicIP(ip) {
			t.Errorf("isPublicIP(%s) = false, want true", raw)
		}
	}
}

func TestIsMetadataDocumentID(t *testing.T) {
	cases := map[string]bool{
		"https://app.example.com/client.json":  true,
		"https://app.example.com/oauth/meta":   true,
		"https://app.example.com":              false, // no path
		"https://app.example.com/":             false, // empty path
		"http://app.example.com/client.json":   false, // not https
		"019f98c7-ecf8-72de-8cb3-072b18c0e5a0": false, // an opaque DCR id
		"":                                     false,
	}

	for input, want := range cases {
		if got := IsMetadataDocumentID(input); got != want {
			t.Errorf("IsMetadataDocumentID(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestValidateMetadata(t *testing.T) {
	const url = "https://app.example.com/client.json"

	valid := func() ClientMetadata {
		return ClientMetadata{
			ClientID:     url,
			ClientName:   "Example Client",
			RedirectURIs: []string{"http://127.0.0.1:3000/callback"},
		}
	}

	t.Run("valid document", func(t *testing.T) {
		meta := valid()

		if err := validateMetadata(&meta, url); err != nil {
			t.Fatalf("valid document rejected: %v", err)
		}

		// Defaults are filled in rather than left empty.
		if meta.ApplicationType != "native" {
			t.Errorf("application_type = %q, want native by default", meta.ApplicationType)
		}

		if meta.TokenEndpointAuthMethod != "none" {
			t.Errorf("token_endpoint_auth_method = %q, want none", meta.TokenEndpointAuthMethod)
		}

		if len(meta.GrantTypes) == 0 || len(meta.ResponseTypes) == 0 {
			t.Error("grant_types and response_types should be defaulted")
		}
	})

	t.Run("client_id must match the URL", func(t *testing.T) {
		meta := valid()
		meta.ClientID = "https://other.example.com/client.json"

		if err := validateMetadata(&meta, url); err == nil {
			t.Error("a document claiming another client_id was accepted")
		}
	})

	t.Run("rejections", func(t *testing.T) {
		cases := map[string]func(*ClientMetadata){
			"no name":           func(m *ClientMetadata) { m.ClientName = "" },
			"no redirects":      func(m *ClientMetadata) { m.RedirectURIs = nil },
			"bad app type":      func(m *ClientMetadata) { m.ApplicationType = "toaster" },
			"unusable redirect": func(m *ClientMetadata) { m.RedirectURIs = []string{"http://evil.example/cb"} },
			"wants a secret":    func(m *ClientMetadata) { m.TokenEndpointAuthMethod = "client_secret_basic" },
			"implicit grant":    func(m *ClientMetadata) { m.ResponseTypes = []string{"token"} },
			"unsupported grant": func(m *ClientMetadata) { m.GrantTypes = []string{"password"} },
		}

		for name, mutate := range cases {
			t.Run(name, func(t *testing.T) {
				meta := valid()
				mutate(&meta)

				if err := validateMetadata(&meta, url); err == nil {
					t.Error("expected rejection")
				}
			})
		}
	})
}

func TestCacheExpiry(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

	cases := map[string]struct {
		header http.Header
		want   time.Duration
	}{
		"no headers falls back to the floor": {
			http.Header{}, cimdMinCacheTTL,
		},
		"max-age is honoured": {
			http.Header{"Cache-Control": {"public, max-age=3600"}}, time.Hour,
		},
		"a tiny max-age is raised to the floor": {
			http.Header{"Cache-Control": {"max-age=1"}}, cimdMinCacheTTL,
		},
		"a huge max-age is clamped to the ceiling": {
			http.Header{"Cache-Control": {"max-age=999999999"}}, cimdMaxCacheTTL,
		},
		"no-store still caches briefly": {
			http.Header{"Cache-Control": {"no-store"}}, cimdMinCacheTTL,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := cacheExpiry(tc.header, now).Sub(now)
			if got != tc.want {
				t.Errorf("cacheExpiry = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRedirectHosts(t *testing.T) {
	got := RedirectHosts([]string{
		"http://127.0.0.1:3000/cb",
		"http://127.0.0.1:4000/cb",
		"https://app.example.com/cb",
		"io.nls.checkmate://oauth/callback",
	})

	// Distinct host:port values, in order, no duplicates.
	want := []string{"127.0.0.1:3000", "127.0.0.1:4000", "app.example.com", "io.nls.checkmate"}

	if len(got) != len(want) {
		t.Fatalf("RedirectHosts = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("RedirectHosts[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
