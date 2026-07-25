package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nls/checkmate/server/internal/account"
	"github.com/nls/checkmate/server/internal/store"
)

// ---------------------------------------------------------------------------
// Session cookies
// ---------------------------------------------------------------------------

func TestSessionCookieAuthenticates(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	sess := h.session(u)

	body := h.doCookie(http.MethodGet, "/v1/me", sess, nil).expect(http.StatusOK).decode()

	if body["user_id"] != u.ID {
		t.Errorf("user_id = %v, want %v", body["user_id"], u.ID)
	}

	if body["auth_via"] != "session" {
		t.Errorf("auth_via = %v, want session", body["auth_via"])
	}

	// And a bearer token reports the other kind, with the same user details.
	body = h.do(http.MethodGet, "/v1/me", u.Token, nil).expect(http.StatusOK).decode()

	if body["auth_via"] != "token" {
		t.Errorf("auth_via = %v, want token", body["auth_via"])
	}

	for _, field := range []string{"email", "name", "timezone"} {
		if body[field] == "" || body[field] == nil {
			t.Errorf("%s is empty on the token path; both credential kinds should "+
				"resolve the same user details", field)
		}
	}
}

func TestSessionCookieScopedToItsOwner(t *testing.T) {
	h := newHarness(t)
	alice := h.user("alice@example.com")
	bob := h.user("bob@example.com")

	aliceTask := h.do(http.MethodPost, "/v1/tasks", alice.Token, map[string]any{
		"title": "alice's", "context_id": h.firstContextID(alice),
	}).expect(http.StatusCreated).id()

	// A session is just another credential: it must obey the same ownership rule.
	bobSession := h.session(bob)

	h.doCookie(http.MethodGet, "/v1/tasks/"+aliceTask, bobSession, nil).
		expect(http.StatusNotFound)

	if items := h.doCookie(http.MethodGet, "/v1/tasks", bobSession, nil).
		expect(http.StatusOK).list(); len(items) != 0 {
		t.Errorf("bob's session sees %d of alice's tasks, want 0", len(items))
	}
}

func TestInvalidSessionRejectedAndCookieCleared(t *testing.T) {
	h := newHarness(t)

	res := h.doCookie(http.MethodGet, "/v1/me", "not-a-real-session", nil)
	res.expect(http.StatusUnauthorized)

	// The browser should be told to stop presenting a cookie that will never work.
	if cookies := res.Header.Values("Set-Cookie"); len(cookies) == 0 {
		t.Error("no Set-Cookie on a rejected session; the dead cookie was not cleared")
	} else if !strings.Contains(cookies[0], "Max-Age=0") {
		t.Errorf("Set-Cookie = %q, want it to expire the cookie", cookies[0])
	}
}

func TestExpiredSessionRejected(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	sess := h.session(u)

	h.doCookie(http.MethodGet, "/v1/me", sess, nil).expect(http.StatusOK)

	if _, err := h.store.DB().Exec(
		`UPDATE sessions SET expires_at = '2020-01-01T00:00:00.000Z' WHERE token_hash = ?`,
		store.HashSecret(sess),
	); err != nil {
		t.Fatalf("expire session: %v", err)
	}

	h.doCookie(http.MethodGet, "/v1/me", sess, nil).expect(http.StatusUnauthorized)
}

// TestAbsoluteExpiryCapsTheSlidingWindow proves the idle timeout cannot push a
// session past its hard ceiling, which is the whole reason there are two
// expiry columns.
func TestAbsoluteExpiryCapsTheSlidingWindow(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	// A session whose ceiling is sooner than one idle window away.
	secret, sess, err := h.store.CreateSession(
		context.Background(), u.ID, time.Hour, time.Hour, "test", "127.0.0.1")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	if _, err := h.store.DB().Exec(
		`UPDATE sessions SET absolute_expires_at = ? WHERE id = ?`,
		time.Now().UTC().Add(2*time.Minute).Format("2006-01-02T15:04:05.000Z"), sess.ID,
	); err != nil {
		t.Fatalf("shorten the ceiling: %v", err)
	}

	// Using it slides expires_at forward, but not past the ceiling.
	h.doCookie(http.MethodGet, "/v1/me", secret, nil).expect(http.StatusOK)

	var expires, absolute string
	if err := h.store.DB().QueryRow(
		`SELECT expires_at, absolute_expires_at FROM sessions WHERE id = ?`, sess.ID,
	).Scan(&expires, &absolute); err != nil {
		t.Fatalf("read session: %v", err)
	}

	if expires > absolute {
		t.Errorf("expires_at %s slid past absolute_expires_at %s", expires, absolute)
	}
}

func TestRevokedSessionRejected(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	sess := h.session(u)

	h.doCookie(http.MethodPost, "/v1/logout", sess, nil).expect(http.StatusNoContent)
	h.doCookie(http.MethodGet, "/v1/me", sess, nil).expect(http.StatusUnauthorized)
}

func TestLogoutEverywhereRevokesAllSessions(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	first, second := h.session(u), h.session(u)

	h.doCookie(http.MethodPost, "/v1/logout?everywhere=true", first, nil).
		expect(http.StatusNoContent)

	// The other device's session must be dead too.
	h.doCookie(http.MethodGet, "/v1/me", second, nil).expect(http.StatusUnauthorized)
}

func TestLogoutRejectsBearerToken(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	// There is no session to end, and silently succeeding would suggest the
	// token had been revoked when it had not.
	h.do(http.MethodPost, "/v1/logout", u.Token, nil).expect(http.StatusBadRequest)

	h.do(http.MethodGet, "/v1/me", u.Token, nil).expect(http.StatusOK)
}

// ---------------------------------------------------------------------------
// CSRF
// ---------------------------------------------------------------------------

// TestCSRFProtectionOnCookieWrites covers the attack a cookie makes possible:
// another site causing a state change using a cookie the browser attaches
// automatically.
func TestCSRFProtectionOnCookieWrites(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	sess := h.session(u)
	contextID := h.firstContextID(u)

	body := map[string]any{"title": "created cross-site", "context_id": contextID}

	cases := map[string]struct {
		headers map[string]string
		status  int
	}{
		"same-origin fetch metadata": {
			map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusCreated,
		},
		"direct navigation": {
			map[string]string{"Sec-Fetch-Site": "none"}, http.StatusCreated,
		},
		"matching origin only": {
			map[string]string{"Origin": testBaseURL}, http.StatusCreated,
		},
		"cross-site fetch metadata": {
			map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": testBaseURL},
			http.StatusForbidden,
		},
		"foreign origin": {
			map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden,
		},
		"no origin at all": {
			map[string]string{}, http.StatusForbidden,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := h.doCookieRaw(http.MethodPost, "/v1/tasks", sess, body, tc.headers)
			if res.Status != tc.status {
				t.Errorf("status = %d, want %d; body %s", res.Status, tc.status, res.Body)
			}
		})
	}
}

func TestCSRFDoesNotBlockReads(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	sess := h.session(u)

	// A cross-site GET cannot change anything, and blocking it would break
	// ordinary navigation.
	h.doCookieRaw(http.MethodGet, "/v1/tasks", sess, nil,
		map[string]string{"Origin": "https://evil.example"}).expect(http.StatusOK)
}

func TestCSRFDoesNotApplyToBearerTokens(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	// A bearer token has to be attached deliberately, so a foreign page cannot
	// cause one to be sent. Requiring Origin here would break native clients.
	req := h.request(http.MethodPost, "/v1/tasks", map[string]any{
		"title": "from a native client", "context_id": h.firstContextID(u),
	})
	req.Header.Set("Authorization", "Bearer "+u.Token)
	req.Header.Set("Origin", "https://evil.example")

	h.send(req).expect(http.StatusCreated)
}

// ---------------------------------------------------------------------------
// Token management
// ---------------------------------------------------------------------------

func TestTokenLifecycle(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	sess := h.session(u)

	created := h.doCookie(http.MethodPost, "/v1/tokens", sess, map[string]any{
		"name":   "iPhone",
		"scopes": []string{"read", "write"},
	}).expect(http.StatusCreated).decode()

	secret, _ := created["token"].(string)
	if secret == "" {
		t.Fatal("no token in the create response")
	}

	// The new token works.
	h.do(http.MethodGet, "/v1/me", secret, nil).expect(http.StatusOK)

	// It is listed, without the secret.
	items := h.doCookie(http.MethodGet, "/v1/tokens", sess, nil).expect(http.StatusOK).list()

	var found map[string]any

	for _, item := range items {
		if item["name"] == "iPhone" {
			found = item
		}

		if _, leaked := item["token"]; leaked {
			t.Error("the token listing exposed a secret")
		}
	}

	if found == nil {
		t.Fatal("the new token is not in the listing")
	}

	tokenID, _ := found["id"].(string)

	h.doCookie(http.MethodDelete, "/v1/tokens/"+tokenID, sess, nil).expect(http.StatusNoContent)

	// Revocation takes effect immediately.
	h.do(http.MethodGet, "/v1/me", secret, nil).expect(http.StatusUnauthorized)
}

// TestTokenIssuingRequiresASession is the containment property: a stolen token
// must not be able to mint more tokens for itself.
func TestTokenIssuingRequiresASession(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	h.do(http.MethodPost, "/v1/tokens", u.Token, map[string]any{"name": "another"}).
		expect(http.StatusForbidden)

	// Listing with a token is fine: it reveals nothing usable.
	h.do(http.MethodGet, "/v1/tokens", u.Token, nil).expect(http.StatusOK)
}

func TestTokensScopedToOwner(t *testing.T) {
	h := newHarness(t)
	alice := h.user("alice@example.com")
	bob := h.user("bob@example.com")

	aliceTokens := h.doCookie(http.MethodGet, "/v1/tokens", h.session(alice), nil).
		expect(http.StatusOK).list()

	if len(aliceTokens) == 0 {
		t.Fatal("alice has no tokens to test with")
	}

	aliceTokenID, _ := aliceTokens[0]["id"].(string)

	bobSession := h.session(bob)

	// Bob cannot see or revoke alice's token.
	for _, item := range h.doCookie(http.MethodGet, "/v1/tokens", bobSession, nil).
		expect(http.StatusOK).list() {
		if item["id"] == aliceTokenID {
			t.Error("bob's token listing includes alice's token")
		}
	}

	h.doCookie(http.MethodDelete, "/v1/tokens/"+aliceTokenID, bobSession, nil).
		expect(http.StatusNotFound)

	// Alice's token still works.
	h.do(http.MethodGet, "/v1/me", alice.Token, nil).expect(http.StatusOK)
}

func TestTokenValidation(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	sess := h.session(u)

	cases := []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"no name", map[string]any{"scopes": []string{"read"}}, "name"},
		{"blank name", map[string]any{"name": "  "}, "name"},
		{"unknown scope", map[string]any{"name": "x", "scopes": []string{"admin"}}, "scopes"},
		{"bad expiry", map[string]any{"name": "x", "expires_at": "tomorrow"}, "expires_at"},
		{
			"expiry in the past",
			map[string]any{"name": "x", "expires_at": "2020-01-01T00:00:00Z"},
			"expires_at",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.doCookie(http.MethodPost, "/v1/tokens", sess, tc.body)
			res.expect(http.StatusUnprocessableEntity)

			if res.fields()[tc.field] == "" {
				t.Errorf("error names %v, want an entry for %s", res.fields(), tc.field)
			}
		})
	}
}

func TestTokenExpiryIsEnforced(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	sess := h.session(u)

	created := h.doCookie(http.MethodPost, "/v1/tokens", sess, map[string]any{
		"name":       "short lived",
		"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}).expect(http.StatusCreated).decode()

	secret, _ := created["token"].(string)

	h.do(http.MethodGet, "/v1/me", secret, nil).expect(http.StatusOK)

	if _, err := h.store.DB().Exec(
		`UPDATE api_tokens SET expires_at = '2020-01-01T00:00:00.000Z' WHERE token_hash = ?`,
		account.HashToken(secret),
	); err != nil {
		t.Fatalf("expire token: %v", err)
	}

	h.do(http.MethodGet, "/v1/me", secret, nil).expect(http.StatusUnauthorized)
}

// ---------------------------------------------------------------------------
// Sign-in routes without a provider configured
// ---------------------------------------------------------------------------

func TestLoginRoutesWithoutAProvider(t *testing.T) {
	h := newHarness(t)

	// The harness configures no provider, so these must say so rather than
	// panicking on a nil service.
	h.do(http.MethodGet, "/auth/login/google", "", nil).expect(http.StatusNotImplemented)
	h.do(http.MethodGet, "/auth/callback/google?state=x&code=y", "", nil).
		expect(http.StatusNotImplemented)

	body := h.do(http.MethodGet, "/auth/config", "", nil).expect(http.StatusOK).decode()

	providers, ok := body["providers"].([]any)
	if !ok {
		t.Fatalf("providers is %T, want an array", body["providers"])
	}

	if len(providers) != 0 {
		t.Errorf("providers = %v, want empty", providers)
	}
}

func TestPurgeExpiredRemovesDeadSessions(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	sess := h.session(u)

	if _, err := h.store.DB().Exec(
		`UPDATE sessions SET absolute_expires_at = '2020-01-01T00:00:00.000Z' WHERE token_hash = ?`,
		store.HashSecret(sess),
	); err != nil {
		t.Fatalf("age the session: %v", err)
	}

	if _, err := h.store.PurgeExpired(context.Background()); err != nil {
		t.Fatalf("purge: %v", err)
	}

	var remaining int
	if err := h.store.DB().QueryRow(`SELECT count(*) FROM sessions`).Scan(&remaining); err != nil {
		t.Fatalf("count sessions: %v", err)
	}

	if remaining != 0 {
		t.Errorf("%d sessions survived the purge, want 0", remaining)
	}
}
