package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/nls/checkmate/server/internal/account"
	"github.com/nls/checkmate/server/internal/config"
	"github.com/nls/checkmate/server/internal/database"
	"github.com/nls/checkmate/server/internal/httpapi"
	"github.com/nls/checkmate/server/internal/mcpserver"
	"github.com/nls/checkmate/server/internal/oauth"
	"github.com/nls/checkmate/server/internal/recurrence"
	"github.com/nls/checkmate/server/internal/store"
)

// testUser is one account plus a working token for it.
type testUser struct {
	ID    string
	Email string
	Token string
}

// testBaseURL is the origin the harness pretends to be served from; the CSRF
// check compares the Origin header against it.
const testBaseURL = "http://checkmate.test"

// harness is a live server backed by a temporary database.
type harness struct {
	t      *testing.T
	server http.Handler
	store  *store.Store
	cfg    config.Config
}

func newHarness(t *testing.T) *harness {
	return newHarnessWithConfig(t, nil)
}

func newHarnessWithConfig(t *testing.T, configure func(*config.Config)) *harness {
	t.Helper()

	ctx := context.Background()

	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	t.Cleanup(func() { db.Close() })

	if err := database.Migrate(ctx, db, nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	st := store.New(db)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	oauthSvc := oauth.New(st, oauth.Config{
		Issuer:                    testBaseURL,
		Resource:                  testBaseURL,
		ResourceAliases:           []string{testBaseURL + "/mcp"},
		AllowDynamicRegistration:  true,
		MaxDynamicClients:         50,
		AllowPrivateMetadataHosts: true,
	})

	// Built directly rather than through config.Load so tests do not depend on
	// the process environment.
	cfg := config.Config{
		Env:                "development",
		BaseURL:            testBaseURL,
		SecureCookies:      false,
		SessionIdleTimeout: time.Hour,
		SessionMaxLifetime: 24 * time.Hour,
	}
	if configure != nil {
		configure(&cfg)
	}

	spawner := recurrence.New(st, log)

	// Discard logs so a failing test's output is only assertions. No login
	// service: the OIDC round trip needs a real provider, so cookie-auth tests
	// mint sessions through the store instead.
	return &harness{
		t: t,
		server: httpapi.New(st, nil, oauthSvc, spawner, mcpserver.New(st, spawner, log, mcpserver.Options{
			Audiences:           oauthSvc.AcceptedAudiences(),
			ResourceMetadataURL: testBaseURL + "/.well-known/oauth-protected-resource",
		}), cfg, log, "test").Handler(),
		store: st,
		cfg:   cfg,
	}
}

// session opens a browser session for u and returns the cookie value.
//
// Created through the store rather than by driving the OIDC flow, which would
// need a live identity provider. What is under test here is what a session
// permits once it exists; the flow that mints one is tested in package login.
func (h *harness) session(u testUser) string {
	h.t.Helper()

	secret, _, err := h.store.CreateSession(
		context.Background(), u.ID,
		h.cfg.SessionIdleTimeout, h.cfg.SessionMaxLifetime,
		"test-agent", "127.0.0.1",
	)
	if err != nil {
		h.t.Fatalf("create session: %v", err)
	}

	return secret
}

// user creates an account, a fixture context, and a full-scope token.
func (h *harness) user(email string) testUser {
	h.t.Helper()

	ctx := context.Background()

	u, err := account.CreateUser(ctx, h.store.DB(), email, "Test "+email, "UTC")
	if err != nil {
		h.t.Fatalf("create user %s: %v", email, err)
	}

	if _, err := h.store.CreateContext(ctx, u.ID, store.ContextCreate{Name: "Test context"}); err != nil {
		h.t.Fatalf("create fixture context for %s: %v", email, err)
	}

	token, err := account.CreateToken(ctx, h.store.DB(), u.ID, "test", "")
	if err != nil {
		h.t.Fatalf("create token for %s: %v", email, err)
	}

	return testUser{ID: u.ID, Email: u.Email, Token: token}
}

func (h *harness) createContextID(u testUser, name string) string {
	h.t.Helper()

	return h.do(http.MethodPost, "/v1/contexts", u.Token, map[string]any{"name": name}).
		expect(http.StatusCreated).id()
}

// tokenWithScopes issues an extra token for u limited to scopes.
func (h *harness) tokenWithScopes(u testUser, scopes string) string {
	h.t.Helper()

	token, err := account.CreateToken(context.Background(), h.store.DB(), u.ID, "scoped", scopes)
	if err != nil {
		h.t.Fatalf("create scoped token: %v", err)
	}

	return token
}

// response is a decoded HTTP response.
type response struct {
	t      *testing.T
	Status int
	Body   []byte
	Header http.Header
}

// do sends a request with a bearer token.
func (h *harness) do(method, path, token string, body any) response {
	h.t.Helper()

	var reader io.Reader

	if body != nil {
		switch v := body.(type) {
		case string:
			reader = bytes.NewBufferString(v)
		default:
			encoded, err := json.Marshal(v)
			if err != nil {
				h.t.Fatalf("marshal body: %v", err)
			}

			reader = bytes.NewReader(encoded)
		}
	}

	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return h.send(req)
}

// doCookie sends a request authenticated by a session cookie, with the
// same-origin headers a browser would attach.
func (h *harness) doCookie(method, path, sessionSecret string, body any) response {
	h.t.Helper()

	req := h.request(method, path, body)
	req.AddCookie(&http.Cookie{Name: "checkmate_session", Value: sessionSecret})
	req.Header.Set("Origin", testBaseURL)
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	return h.send(req)
}

// doCookieRaw sends a cookie-authenticated request with caller-controlled
// origin headers, for exercising the CSRF check.
func (h *harness) doCookieRaw(
	method, path, sessionSecret string,
	body any,
	headers map[string]string,
) response {
	h.t.Helper()

	req := h.request(method, path, body)
	req.AddCookie(&http.Cookie{Name: "checkmate_session", Value: sessionSecret})

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	return h.send(req)
}

func (h *harness) request(method, path string, body any) *http.Request {
	h.t.Helper()

	var reader io.Reader

	if body != nil {
		switch v := body.(type) {
		case string:
			reader = bytes.NewBufferString(v)
		default:
			encoded, err := json.Marshal(v)
			if err != nil {
				h.t.Fatalf("marshal body: %v", err)
			}

			reader = bytes.NewReader(encoded)
		}
	}

	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return req
}

func (h *harness) send(req *http.Request) response {
	h.t.Helper()

	rec := httptest.NewRecorder()
	h.server.ServeHTTP(rec, req)

	return response{t: h.t, Status: rec.Code, Body: rec.Body.Bytes(), Header: rec.Header()}
}

// expect fails unless the status matches.
func (r response) expect(status int) response {
	r.t.Helper()

	if r.Status != status {
		r.t.Fatalf("status = %d, want %d; body: %s", r.Status, status, r.Body)
	}

	return r
}

// decode unmarshals the body into a map.
func (r response) decode() map[string]any {
	r.t.Helper()

	var out map[string]any
	if err := json.Unmarshal(r.Body, &out); err != nil {
		r.t.Fatalf("decode body %q: %v", r.Body, err)
	}

	return out
}

// id returns the id field of a created resource.
func (r response) id() string {
	r.t.Helper()

	v, ok := r.decode()["id"].(string)
	if !ok || v == "" {
		r.t.Fatalf("response has no id: %s", r.Body)
	}

	return v
}

// list returns the data array of a collection response.
func (r response) list() []map[string]any {
	r.t.Helper()

	var out struct {
		Data       []map[string]any `json:"data"`
		NextCursor *string          `json:"next_cursor"`
	}

	if err := json.Unmarshal(r.Body, &out); err != nil {
		r.t.Fatalf("decode list %q: %v", r.Body, err)
	}

	return out.Data
}

// decodeInto unmarshals the body into dst.
func (r response) decodeInto(dst any) {
	r.t.Helper()

	if err := json.Unmarshal(r.Body, dst); err != nil {
		r.t.Fatalf("decode body %q: %v", r.Body, err)
	}
}

// fields returns the per-field validation errors.
func (r response) fields() map[string]string {
	r.t.Helper()

	var out struct {
		Error  string            `json:"error"`
		Fields map[string]string `json:"fields"`
	}

	if err := json.Unmarshal(r.Body, &out); err != nil {
		r.t.Fatalf("decode error body %q: %v", r.Body, err)
	}

	return out.Fields
}

// firstContextID returns the id of the user's fixture context.
func (h *harness) firstContextID(u testUser) string {
	h.t.Helper()

	items := h.do(http.MethodGet, "/v1/contexts", u.Token, nil).expect(http.StatusOK).list()
	if len(items) == 0 {
		h.t.Fatal("user has no fixture contexts")
	}

	id, _ := items[0]["id"].(string)

	return id
}

// ---------------------------------------------------------------------------
// Authentication
// ---------------------------------------------------------------------------

func TestAuthRequired(t *testing.T) {
	h := newHarness(t)

	paths := []string{
		"/v1/tasks", "/v1/contexts", "/v1/projects",
		"/v1/people", "/v1/recurrences", "/v1/sources",
	}

	for _, path := range paths {
		res := h.do(http.MethodGet, path, "", nil)
		if res.Status != http.StatusUnauthorized {
			t.Errorf("GET %s without a token = %d, want 401", path, res.Status)
		}
	}
}

func TestAuthRejectsBadTokens(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	cases := map[string]string{
		"garbage":             "cm_nonsense",
		"empty bearer":        "",
		"token of no one":     "cm_" + "0123456789012345678901234567890123456789",
		"valid but mangled":   u.Token + "x",
		"truncated by a char": u.Token[:len(u.Token)-1],
	}

	for label, token := range cases {
		res := h.do(http.MethodGet, "/v1/tasks", token, nil)
		if res.Status != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", label, res.Status)
		}
	}

	// The real token still works, proving the rejections were about the token
	// and not a broken harness.
	h.do(http.MethodGet, "/v1/tasks", u.Token, nil).expect(http.StatusOK)
}

func TestHealthNeedsNoToken(t *testing.T) {
	newHarness(t).do(http.MethodGet, "/healthz", "", nil).expect(http.StatusOK)
}

func TestScopesEnforced(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	contextID := h.firstContextID(u)

	readOnly := h.tokenWithScopes(u, "read")

	h.do(http.MethodGet, "/v1/tasks", readOnly, nil).expect(http.StatusOK)

	res := h.do(http.MethodPost, "/v1/tasks", readOnly, map[string]any{
		"title":      "should not be created",
		"context_id": contextID,
	})
	res.expect(http.StatusForbidden)

	writeOnly := h.tokenWithScopes(u, "write")
	h.do(http.MethodGet, "/v1/tasks", writeOnly, nil).expect(http.StatusForbidden)
	h.do(http.MethodPost, "/v1/tasks", writeOnly, map[string]any{
		"title":      "written by a write-only token",
		"context_id": contextID,
	}).expect(http.StatusCreated)
}

func TestRevokedAndExpiredTokensRejected(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	revoked := h.tokenWithScopes(u, "read write")
	_, err := h.store.DB().Exec(
		`UPDATE api_tokens SET revoked_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		 WHERE token_hash = ?`, account.HashToken(revoked))
	if err != nil {
		t.Fatalf("revoke token: %v", err)
	}

	h.do(http.MethodGet, "/v1/tasks", revoked, nil).expect(http.StatusUnauthorized)

	expired := h.tokenWithScopes(u, "read write")
	_, err = h.store.DB().Exec(
		`UPDATE api_tokens SET expires_at = '2020-01-01T00:00:00.000Z' WHERE token_hash = ?`,
		account.HashToken(expired))
	if err != nil {
		t.Fatalf("expire token: %v", err)
	}

	h.do(http.MethodGet, "/v1/tasks", expired, nil).expect(http.StatusUnauthorized)
}

// ---------------------------------------------------------------------------
// Ownership: the property the whole store layer exists to guarantee
// ---------------------------------------------------------------------------

// TestOwnershipIsolation walks every resource and confirms that one user cannot
// read, change or delete another user's rows, and that the refusal is a 404 so
// the API never reveals that the id is real.
func TestOwnershipIsolation(t *testing.T) {
	h := newHarness(t)
	alice := h.user("alice@example.com")
	bob := h.user("bob@example.com")

	aliceContext := h.firstContextID(alice)

	aliceProject := h.do(http.MethodPost, "/v1/projects", alice.Token, map[string]any{
		"context_id": aliceContext, "name": "Alice's project",
	}).expect(http.StatusCreated).id()

	alicePerson := h.do(http.MethodPost, "/v1/people", alice.Token, map[string]any{
		"name": "Alice's colleague",
	}).expect(http.StatusCreated).id()

	aliceTask := h.do(http.MethodPost, "/v1/tasks", alice.Token, map[string]any{
		"title": "Alice's task", "context_id": aliceContext,
	}).expect(http.StatusCreated).id()

	aliceRecurrence := h.do(http.MethodPost, "/v1/recurrences", alice.Token, map[string]any{
		"context_id": aliceContext, "title": "Alice's standup",
		"rrule": "FREQ=DAILY", "starts_on": "2026-07-25",
	}).expect(http.StatusCreated).id()

	// The patch body has to be valid for the resource, otherwise the request
	// fails on an unknown field before ownership is ever consulted, and the test
	// would pass for the wrong reason.
	for _, res := range []struct {
		collection string
		id         string
		patch      map[string]any
	}{
		{"contexts", aliceContext, map[string]any{"name": "pwned"}},
		{"projects", aliceProject, map[string]any{"name": "pwned"}},
		{"people", alicePerson, map[string]any{"name": "pwned"}},
		{"tasks", aliceTask, map[string]any{"title": "pwned"}},
		{"recurrences", aliceRecurrence, map[string]any{"title": "pwned"}},
	} {
		path := "/v1/" + res.collection + "/" + res.id

		if got := h.do(http.MethodGet, path, bob.Token, nil).Status; got != http.StatusNotFound {
			t.Errorf("bob GET %s = %d, want 404", path, got)
		}

		if got := h.do(http.MethodPatch, path, bob.Token, res.patch).Status; got != http.StatusNotFound {
			t.Errorf("bob PATCH %s = %d, want 404", path, got)
		}

		if got := h.do(http.MethodDelete, path, bob.Token, nil).Status; got != http.StatusNotFound {
			t.Errorf("bob DELETE %s = %d, want 404", path, got)
		}

		// Alice's row must still be intact, and unmodified, after all of that.
		body := h.do(http.MethodGet, path, alice.Token, nil).expect(http.StatusOK).decode()

		for field, value := range res.patch {
			if got := body[field]; got == value {
				t.Errorf("bob's PATCH changed alice's %s.%s to %v", res.collection, field, got)
			}
		}
	}
}

// TestListsAreScopedToOwner is the other half of isolation: not just that a
// direct fetch is refused, but that another user's rows never appear in a list.
func TestListsAreScopedToOwner(t *testing.T) {
	h := newHarness(t)
	alice := h.user("alice@example.com")
	bob := h.user("bob@example.com")

	h.do(http.MethodPost, "/v1/tasks", alice.Token, map[string]any{
		"title": "alice only", "context_id": h.firstContextID(alice),
	}).expect(http.StatusCreated)

	if items := h.do(http.MethodGet, "/v1/tasks", bob.Token, nil).expect(http.StatusOK).list(); len(items) != 0 {
		t.Errorf("bob sees %d tasks, want 0", len(items))
	}

	// The harness creates one context for each fixture user, so neither should
	// see the other's context.
	for _, u := range []testUser{alice, bob} {
		items := h.do(http.MethodGet, "/v1/contexts", u.Token, nil).expect(http.StatusOK).list()
		if len(items) != 1 {
			t.Errorf("%s sees %d contexts, want 1", u.Email, len(items))
		}
	}
}

// TestCannotReferenceAnotherUsersRows is the subtle case: ownership of the row
// being written is not enough, every id inside the body has to be checked too.
func TestCannotReferenceAnotherUsersRows(t *testing.T) {
	h := newHarness(t)
	alice := h.user("alice@example.com")
	bob := h.user("bob@example.com")

	aliceContext := h.firstContextID(alice)
	bobContext := h.firstContextID(bob)

	aliceProject := h.do(http.MethodPost, "/v1/projects", alice.Token, map[string]any{
		"context_id": aliceContext, "name": "Alice's project",
	}).expect(http.StatusCreated).id()

	alicePerson := h.do(http.MethodPost, "/v1/people", alice.Token, map[string]any{
		"name": "Alice's colleague",
	}).expect(http.StatusCreated).id()

	aliceTask := h.do(http.MethodPost, "/v1/tasks", alice.Token, map[string]any{
		"title": "Alice's task", "context_id": aliceContext,
	}).expect(http.StatusCreated).id()

	// Bob tries to hang his own task off each of Alice's rows.
	for field, value := range map[string]string{
		"context_id":      aliceContext,
		"project_id":      aliceProject,
		"parent_id":       aliceTask,
		"blocked_by_id":   aliceTask,
		"delegated_to_id": alicePerson,
	} {
		body := map[string]any{"title": "bob's task", field: value}

		// project_id needs a context to be checked against at all.
		if field == "project_id" {
			body["context_id"] = bobContext
		}

		res := h.do(http.MethodPost, "/v1/tasks", bob.Token, body)
		if res.Status != http.StatusUnprocessableEntity {
			t.Errorf("bob creating a task with %s = alice's id: status %d, want 422; body %s",
				field, res.Status, res.Body)

			continue
		}

		if detail := res.fields()[field]; detail == "" {
			t.Errorf("%s: error names fields %v, want an entry for %s", field, res.fields(), field)
		}
	}

	// And the same via PATCH on a task he does own.
	bobTask := h.do(http.MethodPost, "/v1/tasks", bob.Token, map[string]any{
		"title": "bob's own", "context_id": bobContext,
	}).expect(http.StatusCreated).id()

	for field, value := range map[string]string{
		"context_id":      aliceContext,
		"project_id":      aliceProject,
		"parent_id":       aliceTask,
		"delegated_to_id": alicePerson,
	} {
		res := h.do(http.MethodPatch, "/v1/tasks/"+bobTask, bob.Token, map[string]any{field: value})
		if res.Status != http.StatusUnprocessableEntity {
			t.Errorf("bob patching %s to alice's id: status %d, want 422; body %s",
				field, res.Status, res.Body)
		}
	}
}

func TestProjectMustBelongToTaskContext(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	first := h.firstContextID(u)
	second := h.createContextID(u, "second context")

	project := h.do(http.MethodPost, "/v1/projects", u.Token, map[string]any{
		"context_id": first, "name": "in the first context",
	}).expect(http.StatusCreated).id()

	res := h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "mismatched", "context_id": second, "project_id": project,
	})
	res.expect(http.StatusUnprocessableEntity)

	if detail := res.fields()["project_id"]; detail == "" {
		t.Errorf("expected a project_id problem, got %v", res.fields())
	}

	// The coherent combination is accepted.
	h.do(http.MethodPost, "/v1/tasks", u.Token, map[string]any{
		"title": "matched", "context_id": first, "project_id": project,
	}).expect(http.StatusCreated)
}
