package login

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/nls/checkmate/server/internal/config"
	"github.com/nls/checkmate/server/internal/database"
	"github.com/nls/checkmate/server/internal/store"
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
		if got := SafeRedirect(input); got != want {
			t.Errorf("SafeRedirect(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestResolveUserProvisionsVerifiedIdentity(t *testing.T) {
	svc, st := newTestService(t)
	ctx := context.Background()

	userID, created, err := svc.resolveUser(
		ctx, "google", "google-subject", "person@example.com", true, "Person Example")
	if err != nil {
		t.Fatalf("resolve user: %v", err)
	}

	if !created {
		t.Error("created = false, want true")
	}

	if userID == "" {
		t.Fatal("user ID is empty")
	}

	linkedUserID, err := st.FindUserByOIDCSubject(ctx, "google", "google-subject")
	if err != nil {
		t.Fatalf("find linked identity: %v", err)
	}

	if linkedUserID != userID {
		t.Errorf("linked user ID = %q, want %q", linkedUserID, userID)
	}

	var contexts int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM contexts WHERE user_id = ?`, userID).Scan(&contexts); err != nil {
		t.Fatalf("count contexts: %v", err)
	}

	if contexts != 0 {
		t.Errorf("contexts = %d, want 0", contexts)
	}

	gotUserID, created, err := svc.resolveUser(
		ctx, "google", "google-subject", "person@example.com", true, "Renamed Person")
	if err != nil {
		t.Fatalf("resolve linked user: %v", err)
	}

	if created {
		t.Error("created = true for linked identity, want false")
	}

	if gotUserID != userID {
		t.Errorf("linked user ID = %q, want %q", gotUserID, userID)
	}
}

func TestResolveUserRejectsUnverifiedEmail(t *testing.T) {
	svc, st := newTestService(t)

	_, _, err := svc.resolveUser(
		context.Background(), "google", "google-subject", "person@example.com", false, "Person Example")
	if !errors.Is(err, ErrEmailUnverified) {
		t.Fatalf("resolve user error = %v, want ErrEmailUnverified", err)
	}

	var users int
	if err := st.DB().QueryRow(`SELECT count(*) FROM users`).Scan(&users); err != nil {
		t.Fatalf("count users: %v", err)
	}

	if users != 0 {
		t.Errorf("users = %d, want 0", users)
	}
}

func TestRedirectURI(t *testing.T) {
	cases := map[string]string{
		"https://checkmate.example":  "https://checkmate.example/auth/callback/google",
		"https://checkmate.example/": "https://checkmate.example/auth/callback/google",
		"http://localhost:8080":      "http://localhost:8080/auth/callback/google",
	}

	for baseURL, want := range cases {
		if got := RedirectURI(baseURL, "google"); got != want {
			t.Errorf("RedirectURI(%q) = %q, want %q", baseURL, got, want)
		}
	}
}

func newTestService(t *testing.T) (*Service, *store.Store) {
	t.Helper()

	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	t.Cleanup(func() { _ = db.Close() })

	if err := database.Migrate(ctx, db, nil); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	st := store.New(db)
	return &Service{store: st, cfg: config.Config{}}, st
}
