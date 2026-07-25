package login_test

import (
	"testing"

	"github.com/nls/checkmate/server/internal/config"
	"github.com/nls/checkmate/server/internal/login"
)

// TestSafeRedirect covers the open-redirect surface: ?redirect_to= is attacker
// controlled and is followed immediately after a successful sign-in, which is
// exactly when a victim is least suspicious.
func TestSafeRedirect(t *testing.T) {
	cases := map[string]string{
		// Kept: same-site paths.
		"/":                  "/",
		"/tasks":             "/tasks",
		"/tasks?context=all": "/tasks?context=all",
		"/a/b/c":             "/a/b/c",

		// Rejected: anything that could leave the site.
		"":                         "/",
		"https://evil.example":     "/",
		"http://evil.example/path": "/",
		"//evil.example":           "/",
		"//evil.example/path":      "/",
		"javascript:alert(1)":      "/",
		"data:text/html,<script>":  "/",
		"tasks":                    "/",
		"../admin":                 "/",
		"https://evil.example//x":  "/",
		"\\\\evil.example":         "/",
	}

	for input, want := range cases {
		if got := login.SafeRedirect(input); got != want {
			t.Errorf("SafeRedirect(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestEmailAllowed pins the provisioning gate. An empty allowlist must admit
// nobody: the server is on the public internet and "sign in with Google" would
// otherwise hand an account to anyone who has one.
func TestEmailAllowed(t *testing.T) {
	t.Run("empty allowlist admits nobody", func(t *testing.T) {
		cfg := config.Config{}

		for _, email := range []string{"you@example.com", "anyone@gmail.com", ""} {
			if cfg.EmailAllowed(email) {
				t.Errorf("EmailAllowed(%q) = true with an empty allowlist, want false", email)
			}
		}
	})

	t.Run("exact addresses", func(t *testing.T) {
		cfg := config.Config{AllowedEmails: []string{"you@example.com", "other@example.org"}}

		for email, want := range map[string]bool{
			"you@example.com":           true,
			"YOU@EXAMPLE.COM":           true, // addresses compare case-insensitively
			"  you@example.com":         true, // and are trimmed
			"other@example.org":         true,
			"nope@example.com":          false,
			"you@example.com.evil.test": false,
			"":                          false,
		} {
			if got := cfg.EmailAllowed(email); got != want {
				t.Errorf("EmailAllowed(%q) = %v, want %v", email, got, want)
			}
		}
	})

	t.Run("domain entries", func(t *testing.T) {
		cfg := config.Config{AllowedEmails: []string{"@example.com"}}

		for email, want := range map[string]bool{
			"anyone@example.com":  true,
			"someone@example.com": true,
			"anyone@example.org":  false,
			// Must not match a domain that merely ends with the allowed one.
			"anyone@notexample.com": false,
			"anyone@evil.com":       false,
		} {
			if got := cfg.EmailAllowed(email); got != want {
				t.Errorf("EmailAllowed(%q) = %v, want %v", email, got, want)
			}
		}
	})
}

func TestRedirectURI(t *testing.T) {
	cases := map[string]string{
		"https://checkmate.example":  "https://checkmate.example/auth/callback/google",
		"https://checkmate.example/": "https://checkmate.example/auth/callback/google",
		"http://localhost:8080":      "http://localhost:8080/auth/callback/google",
	}

	for baseURL, want := range cases {
		if got := login.RedirectURI(baseURL, "google"); got != want {
			t.Errorf("RedirectURI(%q) = %q, want %q", baseURL, got, want)
		}
	}
}
