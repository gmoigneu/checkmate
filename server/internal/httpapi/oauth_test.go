package httpapi_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/nls/checkmate/server/internal/oauth"
	"github.com/nls/checkmate/server/internal/store"
)

// ---------------------------------------------------------------------------
// OAuth test helpers
// ---------------------------------------------------------------------------

const testVerifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk-abcdef"

func testChallenge() string {
	sum := sha256.Sum256([]byte(testVerifier))

	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// requestIDPattern pulls the consent form's hidden request_id out of the page.
var requestIDPattern = regexp.MustCompile(`name="request_id" value="([^"]+)"`)

// form posts an application/x-www-form-urlencoded body.
func (h *harness) form(path string, values url.Values) response {
	h.t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return h.send(req)
}

// registerClient registers a public native client via RFC 7591.
func (h *harness) registerClient(redirectURIs ...string) string {
	h.t.Helper()

	if len(redirectURIs) == 0 {
		redirectURIs = []string{"http://127.0.0.1:41234/callback"}
	}

	body := h.do(http.MethodPost, "/oauth/register", "", map[string]any{
		"client_name":                "Test MCP Client",
		"redirect_uris":              redirectURIs,
		"application_type":           "native",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}).expect(http.StatusCreated).decode()

	clientID, _ := body["client_id"].(string)
	if clientID == "" {
		h.t.Fatalf("registration returned no client_id: %s", body)
	}

	return clientID
}

// authorizeParams builds a valid authorization request query.
func authorizeParams(clientID, redirectURI string, extra url.Values) url.Values {
	q := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {"read write"},
		"state":                 {"opaque-state"},
		"code_challenge":        {testChallenge()},
		"code_challenge_method": {"S256"},
		"resource":              {testBaseURL},
	}

	for k, v := range extra {
		q[k] = v
	}

	return q
}

// consent walks the authorize + consent pages and returns the redirect location.
func (h *harness) consent(session, clientID, redirectURI string, extra url.Values) string {
	h.t.Helper()

	q := authorizeParams(clientID, redirectURI, extra)

	page := h.doCookie(http.MethodGet, "/oauth/authorize?"+q.Encode(), session, nil).
		expect(http.StatusOK)

	match := requestIDPattern.FindSubmatch(page.Body)
	if match == nil {
		h.t.Fatalf("no request_id in the consent page: %s", page.Body)
	}

	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize",
		strings.NewReader(url.Values{
			"request_id": {string(match[1])},
			"decision":   {"approve"},
		}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "checkmate_session", Value: session})
	req.Header.Set("Origin", testBaseURL)
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	res := h.send(req)
	if res.Status != http.StatusSeeOther {
		h.t.Fatalf("consent POST = %d, want 303; body %s", res.Status, res.Body)
	}

	return res.Header.Get("Location")
}

// codeFrom extracts the authorization code from a redirect Location.
func codeFrom(t *testing.T, location string) string {
	t.Helper()

	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse redirect %q: %v", location, err)
	}

	code := parsed.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in redirect %q", location)
	}

	return code
}

// exchange posts to the token endpoint and returns the decoded response.
func (h *harness) exchange(clientID, code, verifier, redirectURI string) response {
	h.t.Helper()

	return h.form("/oauth/token", url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"resource":      {testBaseURL},
	})
}

// fullFlow runs registration through to a token pair.
func (h *harness) fullFlow(u testUser) (clientID, accessToken, refreshToken string) {
	h.t.Helper()

	const redirectURI = "http://127.0.0.1:41234/callback"

	clientID = h.registerClient(redirectURI)
	session := h.session(u)
	location := h.consent(session, clientID, redirectURI, nil)

	body := h.exchange(clientID, codeFrom(h.t, location), testVerifier, redirectURI).
		expect(http.StatusOK).decode()

	accessToken, _ = body["access_token"].(string)
	refreshToken, _ = body["refresh_token"].(string)

	if accessToken == "" {
		h.t.Fatalf("no access_token in %s", body)
	}

	return clientID, accessToken, refreshToken
}

// ---------------------------------------------------------------------------
// Discovery
// ---------------------------------------------------------------------------

func TestAuthorizationServerMetadata(t *testing.T) {
	h := newHarness(t)

	body := h.do(http.MethodGet, "/.well-known/oauth-authorization-server", "", nil).
		expect(http.StatusOK).decode()

	if body["issuer"] != testBaseURL {
		t.Errorf("issuer = %v, want %v", body["issuer"], testBaseURL)
	}

	// The MCP spec tells clients to refuse a server whose metadata omits
	// code_challenge_methods_supported, so this field is what makes the server
	// usable at all.
	methods, ok := body["code_challenge_methods_supported"].([]any)
	if !ok || len(methods) != 1 || methods[0] != "S256" {
		t.Errorf("code_challenge_methods_supported = %v, want exactly [S256]", body["code_challenge_methods_supported"])
	}

	// OAuth 2.1 removed the implicit grant.
	responseTypes, _ := body["response_types_supported"].([]any)
	for _, rt := range responseTypes {
		if rt == "token" {
			t.Error("the implicit grant is advertised; OAuth 2.1 removed it")
		}
	}

	for _, field := range []string{
		"authorization_response_iss_parameter_supported",
		"client_id_metadata_document_supported",
	} {
		if body[field] != true {
			t.Errorf("%s = %v, want true", field, body[field])
		}
	}

	for _, field := range []string{
		"authorization_endpoint", "token_endpoint", "revocation_endpoint", "registration_endpoint",
	} {
		if value, _ := body[field].(string); !strings.HasPrefix(value, testBaseURL) {
			t.Errorf("%s = %v, want it under %s", field, body[field], testBaseURL)
		}
	}
}

func TestProtectedResourceMetadata(t *testing.T) {
	h := newHarness(t)

	for path, wantResource := range map[string]string{
		"/.well-known/oauth-protected-resource":     testBaseURL,
		"/.well-known/oauth-protected-resource/mcp": testBaseURL + "/mcp",
	} {
		body := h.do(http.MethodGet, path, "", nil).expect(http.StatusOK).decode()

		if body["resource"] != wantResource {
			t.Errorf("%s: resource = %v, want %v", path, body["resource"], wantResource)
		}

		servers, ok := body["authorization_servers"].([]any)
		if !ok || len(servers) == 0 {
			t.Fatalf("%s: authorization_servers = %v, want at least one", path, body["authorization_servers"])
		}

		if servers[0] != testBaseURL {
			t.Errorf("%s: authorization_servers[0] = %v, want %v", path, servers[0], testBaseURL)
		}

		// MCP forbids tokens in the query string.
		methods, _ := body["bearer_methods_supported"].([]any)
		if len(methods) != 1 || methods[0] != "header" {
			t.Errorf("%s: bearer_methods_supported = %v, want [header]", path, methods)
		}

		// offline_access governs refresh issuance at the AS and must not appear
		// as something the resource requires.
		scopes, _ := body["scopes_supported"].([]any)
		for _, scope := range scopes {
			if scope == "offline_access" {
				t.Errorf("%s: offline_access should not be in the resource's scopes_supported", path)
			}
		}
	}

	// An unknown resource suffix is not ours to describe.
	h.do(http.MethodGet, "/.well-known/oauth-protected-resource/nope", "", nil).
		expect(http.StatusNotFound)
}

// TestUnauthorizedChallenge covers the header an MCP client depends on to
// discover where to authenticate.
func TestUnauthorizedChallenge(t *testing.T) {
	h := newHarness(t)

	res := h.do(http.MethodGet, "/v1/tasks", "", nil).expect(http.StatusUnauthorized)

	challenge := res.Header.Get("WWW-Authenticate")
	if challenge == "" {
		t.Fatal("no WWW-Authenticate header on 401")
	}

	wantMetadata := testBaseURL + "/.well-known/oauth-protected-resource"
	if !strings.Contains(challenge, `resource_metadata="`+wantMetadata+`"`) {
		t.Errorf("challenge %q does not point at %q", challenge, wantMetadata)
	}

	if !strings.Contains(challenge, "scope=") {
		t.Errorf("challenge %q has no scope hint", challenge)
	}
}

// TestChallengeScopeMatchesTheMethod pins a fix: the 401 on an unauthenticated
// write used to advertise scope="read". A client following that challenge would
// authorize, receive a read-only token, and fail its very next request with
// insufficient_scope, sending the user through consent twice for one operation.
func TestChallengeScopeMatchesTheMethod(t *testing.T) {
	h := newHarness(t)

	cases := map[string]struct {
		method    string
		body      any
		wantScope string
	}{
		"read on a GET":            {http.MethodGet, nil, `scope="read"`},
		"read and write on a POST": {http.MethodPost, map[string]any{"title": "x"}, `scope="read write"`},
		"read and write on DELETE": {http.MethodDelete, nil, `scope="read write"`},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := h.do(tc.method, "/v1/tasks", "", tc.body)

			// DELETE without an id is a 404 route, so aim it at one that exists.
			if tc.method == http.MethodDelete {
				res = h.do(tc.method, "/v1/tasks/whatever", "", nil)
			}

			res.expect(http.StatusUnauthorized)

			if challenge := res.Header.Get("WWW-Authenticate"); !strings.Contains(challenge, tc.wantScope) {
				t.Errorf("challenge = %q, want it to contain %s", challenge, tc.wantScope)
			}
		})
	}
}

func TestInsufficientScopeChallenge(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	readOnly := h.tokenWithScopes(u, "read")

	res := h.do(http.MethodPost, "/v1/tasks", readOnly, map[string]any{
		"title": "blocked by scope", "context_id": h.firstContextID(u),
	}).expect(http.StatusForbidden)

	challenge := res.Header.Get("WWW-Authenticate")

	if !strings.Contains(challenge, `error="insufficient_scope"`) {
		t.Errorf("challenge %q should carry error=insufficient_scope", challenge)
	}

	if !strings.Contains(challenge, "scope=") {
		t.Errorf("challenge %q should name the scopes needed", challenge)
	}
}

// ---------------------------------------------------------------------------
// Dynamic client registration
// ---------------------------------------------------------------------------

func TestDynamicRegistration(t *testing.T) {
	h := newHarness(t)

	body := h.do(http.MethodPost, "/oauth/register", "", map[string]any{
		"client_name":      "Claude Desktop",
		"redirect_uris":    []string{"http://127.0.0.1:33418/callback"},
		"application_type": "native",
	}).expect(http.StatusCreated).decode()

	if body["client_id"] == "" || body["client_id"] == nil {
		t.Error("no client_id issued")
	}

	// A public client must not be handed a secret it cannot protect.
	if _, hasSecret := body["client_secret"]; hasSecret {
		t.Error("a public client was issued a client_secret")
	}

	if body["client_id_issued_at"] == nil {
		t.Error("client_id_issued_at is missing")
	}
}

func TestRegistrationRejectsUnusableRedirects(t *testing.T) {
	h := newHarness(t)

	cases := map[string]map[string]any{
		"no name": {
			"redirect_uris": []string{"https://app.example.com/cb"},
		},
		"no redirects": {
			"client_name": "X",
		},
		"plain http off-loopback": {
			"client_name": "X", "redirect_uris": []string{"http://evil.example.com/cb"},
		},
		"web client on loopback": {
			"client_name": "X", "application_type": "web",
			"redirect_uris": []string{"http://127.0.0.1:3000/cb"},
		},
		"implicit grant": {
			"client_name": "X", "redirect_uris": []string{"https://app.example.com/cb"},
			"response_types": []string{"token"},
		},
		"password grant": {
			"client_name": "X", "redirect_uris": []string{"https://app.example.com/cb"},
			"grant_types": []string{"password"},
		},
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			res := h.do(http.MethodPost, "/oauth/register", "", body)
			if res.Status == http.StatusCreated {
				t.Errorf("registration succeeded, want rejection; body %s", res.Body)
			}
		})
	}
}

func TestRegistrationIgnoresUnknownMetadata(t *testing.T) {
	h := newHarness(t)

	// RFC 7591 expects a server to ignore metadata it does not implement, unlike
	// the rest of this API which rejects unknown fields.
	h.do(http.MethodPost, "/oauth/register", "", map[string]any{
		"client_name":     "Forward Compatible Client",
		"redirect_uris":   []string{"https://app.example.com/cb"},
		"contacts":        []string{"dev@example.com"},
		"tos_uri":         "https://app.example.com/tos",
		"future_field_42": "whatever",
	}).expect(http.StatusCreated)
}

func TestConfidentialClientGetsASecret(t *testing.T) {
	h := newHarness(t)

	body := h.do(http.MethodPost, "/oauth/register", "", map[string]any{
		"client_name":                "Server-Side Client",
		"redirect_uris":              []string{"https://app.example.com/cb"},
		"application_type":           "web",
		"token_endpoint_auth_method": "client_secret_basic",
	}).expect(http.StatusCreated).decode()

	secret, _ := body["client_secret"].(string)
	if secret == "" {
		t.Fatal("a confidential client was not issued a secret")
	}
}

// ---------------------------------------------------------------------------
// Authorization endpoint
// ---------------------------------------------------------------------------

func TestAuthorizeRequiresSignIn(t *testing.T) {
	h := newHarness(t)
	clientID := h.registerClient()

	q := authorizeParams(clientID, "http://127.0.0.1:41234/callback", nil)

	// No provider is configured in the harness, so the flow cannot authenticate
	// a human and says so rather than pretending.
	h.do(http.MethodGet, "/oauth/authorize?"+q.Encode(), "", nil).
		expect(http.StatusNotImplemented)
}

func TestAuthorizeRejectsBadRequests(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	session := h.session(u)
	clientID := h.registerClient()

	const redirectURI = "http://127.0.0.1:41234/callback"

	cases := map[string]url.Values{
		"no PKCE challenge":   {"code_challenge": {""}, "code_challenge_method": {""}},
		"plain PKCE":          {"code_challenge_method": {"plain"}},
		"truncated challenge": {"code_challenge": {"short"}},
		"implicit response":   {"response_type": {"token"}},
		"unknown scope":       {"scope": {"admin"}},
		"foreign resource":    {"resource": {"https://someone-else.example.com"}},
	}

	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			q := authorizeParams(clientID, redirectURI, extra)

			res := h.doCookie(http.MethodGet, "/oauth/authorize?"+q.Encode(), session, nil)

			// Either an error page or a redirect carrying error=, but never a
			// consent screen offering to approve an invalid request.
			if res.Status == http.StatusOK && requestIDPattern.Match(res.Body) {
				t.Errorf("an invalid request reached the consent screen")
			}

			if res.Status == http.StatusFound {
				location := res.Header.Get("Location")
				if !strings.Contains(location, "error=") {
					t.Errorf("redirect %q carries no error", location)
				}
			}
		})
	}
}

// TestAuthorizeDoesNotRedirectUnverifiedURIs is the open-redirect guard: until
// the redirect URI is proven to belong to the client, an error must be rendered
// rather than sent to a URI the caller chose.
func TestAuthorizeDoesNotRedirectUnverifiedURIs(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	session := h.session(u)
	clientID := h.registerClient("http://127.0.0.1:41234/callback")

	cases := map[string]url.Values{
		"unregistered redirect_uri": authorizeParams(clientID, "https://evil.example.com/steal", nil),
		"unknown client":            authorizeParams("not-a-client", "https://evil.example.com/steal", nil),
	}

	for name, q := range cases {
		t.Run(name, func(t *testing.T) {
			res := h.doCookie(http.MethodGet, "/oauth/authorize?"+q.Encode(), session, nil)

			if res.Status == http.StatusFound {
				t.Errorf("redirected to %q; an unverified redirect_uri must not be followed",
					res.Header.Get("Location"))
			}

			if res.Status != http.StatusBadRequest && res.Status != http.StatusUnauthorized {
				t.Errorf("status = %d, want a 4xx error page", res.Status)
			}
		})
	}
}

func TestConsentScreenShowsRedirectHostAndWarning(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	session := h.session(u)

	const redirectURI = "http://127.0.0.1:41234/callback"

	clientID := h.registerClient(redirectURI)

	q := authorizeParams(clientID, redirectURI, nil)
	page := string(h.doCookie(http.MethodGet, "/oauth/authorize?"+q.Encode(), session, nil).
		expect(http.StatusOK).Body)

	// The spec requires the redirect host to be displayed: for a loopback client
	// it is the only thing distinguishing the real client from an impersonator.
	if !strings.Contains(page, "127.0.0.1:41234") {
		t.Error("the consent screen does not show the redirect host")
	}

	if !strings.Contains(page, "own machine") {
		t.Error("no loopback warning on a loopback-only client")
	}

	if !strings.Contains(page, u.Email) {
		t.Error("the consent screen does not say who is signed in")
	}

	// Scopes are described, not printed as raw scope names.
	if !strings.Contains(page, "See your tasks") {
		t.Error("scopes are not described in human terms")
	}
}

// TestConsentEscapesClientName checks the untrusted-input path: client_name comes
// from a stranger's registration and is rendered into HTML.
func TestConsentEscapesClientName(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	session := h.session(u)

	const redirectURI = "http://127.0.0.1:41234/callback"

	body := h.do(http.MethodPost, "/oauth/register", "", map[string]any{
		"client_name":      `<script>alert(1)</script>`,
		"redirect_uris":    []string{redirectURI},
		"application_type": "native",
	}).expect(http.StatusCreated).decode()

	clientID, _ := body["client_id"].(string)

	q := authorizeParams(clientID, redirectURI, nil)
	page := string(h.doCookie(http.MethodGet, "/oauth/authorize?"+q.Encode(), session, nil).
		expect(http.StatusOK).Body)

	if strings.Contains(page, "<script>alert(1)</script>") {
		t.Error("client_name was rendered unescaped into the consent page")
	}

	if !strings.Contains(page, "&lt;script&gt;") {
		t.Error("expected the client name to appear escaped")
	}
}

func TestNativeAppConsentRendersExplicitHandoff(t *testing.T) {
	tests := []struct {
		decision         string
		heading          string
		forbiddenHeading string
		callback         string
	}{
		{
			decision:         "approve",
			heading:          "Authorization approved",
			forbiddenHeading: "Authorization declined",
			callback:         "code=",
		},
		{
			decision:         "deny",
			heading:          "Authorization declined",
			forbiddenHeading: "Authorization approved",
			callback:         "error=access_denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.decision, func(t *testing.T) {
			h := newHarness(t)
			u := h.user("you@example.com")
			session := h.session(u)

			const redirectURI = "io.nls.checkmate:/oauth/callback"
			clientID := h.registerClient(redirectURI)
			q := authorizeParams(clientID, redirectURI, nil)
			page := h.doCookie(http.MethodGet, "/oauth/authorize?"+q.Encode(), session, nil).
				expect(http.StatusOK)

			match := requestIDPattern.FindSubmatch(page.Body)
			if match == nil {
				t.Fatal("no request_id in the consent page")
			}

			req := httptest.NewRequest(http.MethodPost, "/oauth/authorize",
				strings.NewReader(url.Values{
					"request_id": {string(match[1])},
					"decision":   {tt.decision},
				}.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: "checkmate_session", Value: session})
			req.Header.Set("Origin", testBaseURL)
			req.Header.Set("Sec-Fetch-Site", "same-origin")

			res := h.send(req).expect(http.StatusOK)
			body := string(res.Body)
			if !strings.Contains(body, tt.heading) ||
				strings.Contains(body, tt.forbiddenHeading) ||
				!strings.Contains(body, `href="io.nls.checkmate:/oauth/callback?`) ||
				!strings.Contains(body, tt.callback) {
				t.Errorf("native handoff page does not describe or link the %s result: %s", tt.decision, body)
			}
			if location := res.Header.Get("Location"); location != "" {
				t.Errorf("native handoff unexpectedly redirects to %q", location)
			}
		})
	}
}

func TestDenyingConsentRedirectsWithError(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	session := h.session(u)

	const redirectURI = "http://127.0.0.1:41234/callback"

	clientID := h.registerClient(redirectURI)

	q := authorizeParams(clientID, redirectURI, nil)
	page := h.doCookie(http.MethodGet, "/oauth/authorize?"+q.Encode(), session, nil).
		expect(http.StatusOK)

	match := requestIDPattern.FindSubmatch(page.Body)
	if match == nil {
		t.Fatal("no request_id in the consent page")
	}

	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize",
		strings.NewReader(url.Values{
			"request_id": {string(match[1])},
			"decision":   {"deny"},
		}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "checkmate_session", Value: session})
	req.Header.Set("Origin", testBaseURL)

	res := h.send(req).expect(http.StatusSeeOther)

	location := res.Header.Get("Location")
	if !strings.Contains(location, "error=access_denied") {
		t.Errorf("deny redirect = %q, want error=access_denied", location)
	}

	// RFC 9207: iss goes on error responses too.
	if !strings.Contains(location, "iss=") {
		t.Errorf("deny redirect %q carries no iss parameter", location)
	}
}

// TestAuthorizationResponseCarriesIss covers RFC 9207, which the MCP draft spec
// adds to mitigate mix-up attacks.
func TestAuthorizationResponseCarriesIss(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	const redirectURI = "http://127.0.0.1:41234/callback"

	clientID := h.registerClient(redirectURI)
	location := h.consent(h.session(u), clientID, redirectURI, nil)

	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}

	if got := parsed.Query().Get("iss"); got != testBaseURL {
		t.Errorf("iss = %q, want %q", got, testBaseURL)
	}

	if got := parsed.Query().Get("state"); got != "opaque-state" {
		t.Errorf("state = %q, want it echoed back", got)
	}
}

// ---------------------------------------------------------------------------
// Token endpoint
// ---------------------------------------------------------------------------

func TestAuthorizationCodeFlow(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	_, accessToken, refreshToken := h.fullFlow(u)

	if !strings.HasPrefix(accessToken, oauth.AccessTokenPrefix) {
		t.Errorf("access token %q lacks the expected prefix", accessToken)
	}

	if refreshToken == "" {
		t.Error("no refresh token issued")
	}

	// The token works against the API and resolves to the right user.
	body := h.do(http.MethodGet, "/v1/me", accessToken, nil).expect(http.StatusOK).decode()

	if body["user_id"] != u.ID {
		t.Errorf("user_id = %v, want %v", body["user_id"], u.ID)
	}

	// And it can actually do work.
	h.do(http.MethodPost, "/v1/tasks", accessToken, map[string]any{
		"title": "created through OAuth", "context_id": h.firstContextID(u),
	}).expect(http.StatusCreated)
}

func TestTokenEndpointRejections(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	const redirectURI = "http://127.0.0.1:41234/callback"

	t.Run("wrong code_verifier", func(t *testing.T) {
		clientID := h.registerClient(redirectURI)
		location := h.consent(h.session(u), clientID, redirectURI, nil)

		res := h.exchange(clientID, codeFrom(t, location), "the-wrong-verifier-aaaaaaaaaaaaaaaaaaaaaaaa", redirectURI)
		res.expect(http.StatusBadRequest)

		if got := res.decode()["error"]; got != "invalid_grant" {
			t.Errorf("error = %v, want invalid_grant", got)
		}
	})

	t.Run("missing code_verifier", func(t *testing.T) {
		clientID := h.registerClient(redirectURI)
		location := h.consent(h.session(u), clientID, redirectURI, nil)

		h.exchange(clientID, codeFrom(t, location), "", redirectURI).
			expect(http.StatusBadRequest)
	})

	t.Run("code is single use", func(t *testing.T) {
		clientID := h.registerClient(redirectURI)
		location := h.consent(h.session(u), clientID, redirectURI, nil)
		code := codeFrom(t, location)

		h.exchange(clientID, code, testVerifier, redirectURI).expect(http.StatusOK)

		// A replayed code must not mint a second token.
		res := h.exchange(clientID, code, testVerifier, redirectURI).
			expect(http.StatusBadRequest)

		if got := res.decode()["error"]; got != "invalid_grant" {
			t.Errorf("error = %v, want invalid_grant", got)
		}
	})

	t.Run("code bound to its client", func(t *testing.T) {
		clientID := h.registerClient(redirectURI)
		other := h.registerClient(redirectURI)

		location := h.consent(h.session(u), clientID, redirectURI, nil)

		// Another client must not be able to redeem someone else's code.
		h.exchange(other, codeFrom(t, location), testVerifier, redirectURI).
			expect(http.StatusBadRequest)
	})

	t.Run("mismatched redirect_uri", func(t *testing.T) {
		clientID := h.registerClient(redirectURI, "http://127.0.0.1:9999/other")
		location := h.consent(h.session(u), clientID, redirectURI, nil)

		h.exchange(clientID, codeFrom(t, location), testVerifier, "http://127.0.0.1:9999/other").
			expect(http.StatusBadRequest)
	})

	t.Run("unknown grant type", func(t *testing.T) {
		res := h.form("/oauth/token", url.Values{
			"grant_type": {"password"},
			"client_id":  {h.registerClient(redirectURI)},
		}).expect(http.StatusBadRequest)

		if got := res.decode()["error"]; got != "unsupported_grant_type" {
			t.Errorf("error = %v, want unsupported_grant_type", got)
		}
	})

	t.Run("unknown client", func(t *testing.T) {
		res := h.form("/oauth/token", url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {"whatever"},
			"code_verifier": {testVerifier},
			"client_id":     {"nobody"},
		}).expect(http.StatusUnauthorized)

		if got := res.decode()["error"]; got != "invalid_client" {
			t.Errorf("error = %v, want invalid_client", got)
		}
	})

	t.Run("public client sending a secret", func(t *testing.T) {
		clientID := h.registerClient(redirectURI)

		h.form("/oauth/token", url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {"whatever"},
			"code_verifier": {testVerifier},
			"client_id":     {clientID},
			"client_secret": {"invented"},
		}).expect(http.StatusUnauthorized)
	})
}

func TestTokenResponseIsNotCacheable(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	const redirectURI = "http://127.0.0.1:41234/callback"

	clientID := h.registerClient(redirectURI)
	location := h.consent(h.session(u), clientID, redirectURI, nil)

	res := h.exchange(clientID, codeFrom(t, location), testVerifier, redirectURI).
		expect(http.StatusOK)

	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store on a token response", cc)
	}
}

// ---------------------------------------------------------------------------
// Refresh rotation and theft detection
// ---------------------------------------------------------------------------

func TestRefreshRotation(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	clientID, firstAccess, firstRefresh := h.fullFlow(u)

	res := h.form("/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {firstRefresh},
		"client_id":     {clientID},
	}).expect(http.StatusOK).decode()

	secondAccess, _ := res["access_token"].(string)
	secondRefresh, _ := res["refresh_token"].(string)

	if secondAccess == "" || secondAccess == firstAccess {
		t.Error("refresh did not produce a new access token")
	}

	// OAuth 2.1 requires rotation for public clients.
	if secondRefresh == "" || secondRefresh == firstRefresh {
		t.Error("the refresh token was not rotated")
	}

	h.do(http.MethodGet, "/v1/me", secondAccess, nil).expect(http.StatusOK)
}

// TestRefreshReuseRevokesTheGrant is the theft-detection property: replaying a
// rotated refresh token means it leaked, and the attacker may already hold the
// successor, so refusing just that one request would not help.
func TestRefreshReuseRevokesTheGrant(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	clientID, _, firstRefresh := h.fullFlow(u)

	rotated := h.form("/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {firstRefresh},
		"client_id":     {clientID},
	}).expect(http.StatusOK).decode()

	successorAccess, _ := rotated["access_token"].(string)
	successorRefresh, _ := rotated["refresh_token"].(string)

	// Replay the already-used token.
	res := h.form("/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {firstRefresh},
		"client_id":     {clientID},
	}).expect(http.StatusBadRequest)

	if got := res.decode()["error"]; got != "invalid_grant" {
		t.Errorf("error = %v, want invalid_grant", got)
	}

	// The whole family is now dead, including the successor the attacker would
	// be holding.
	h.do(http.MethodGet, "/v1/me", successorAccess, nil).expect(http.StatusUnauthorized)

	h.form("/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {successorRefresh},
		"client_id":     {clientID},
	}).expect(http.StatusBadRequest)
}

func TestRefreshCannotWidenScope(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	const redirectURI = "http://127.0.0.1:41234/callback"

	clientID := h.registerClient(redirectURI)
	location := h.consent(h.session(u), clientID, redirectURI, url.Values{"scope": {"read"}})

	body := h.exchange(clientID, codeFrom(t, location), testVerifier, redirectURI).
		expect(http.StatusOK).decode()

	refresh, _ := body["refresh_token"].(string)

	if got := body["scope"]; got != "read" {
		t.Fatalf("scope = %v, want read", got)
	}

	// Asking for write on refresh must not grant it.
	res := h.form("/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {clientID},
		"scope":         {"read write"},
	}).expect(http.StatusBadRequest)

	if got := res.decode()["error"]; got != "invalid_scope" {
		t.Errorf("error = %v, want invalid_scope", got)
	}
}

func TestOAuthScopeIsEnforced(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	const redirectURI = "http://127.0.0.1:41234/callback"

	clientID := h.registerClient(redirectURI)
	location := h.consent(h.session(u), clientID, redirectURI, url.Values{"scope": {"read"}})

	body := h.exchange(clientID, codeFrom(t, location), testVerifier, redirectURI).
		expect(http.StatusOK).decode()

	accessToken, _ := body["access_token"].(string)

	h.do(http.MethodGet, "/v1/tasks", accessToken, nil).expect(http.StatusOK)

	h.do(http.MethodPost, "/v1/tasks", accessToken, map[string]any{
		"title": "should be refused", "context_id": h.firstContextID(u),
	}).expect(http.StatusForbidden)
}

// ---------------------------------------------------------------------------
// Audience binding
// ---------------------------------------------------------------------------

// TestAudienceIsEnforced is the MCP requirement that a resource server reject a
// token minted for something else.
func TestAudienceIsEnforced(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	_, accessToken, _ := h.fullFlow(u)

	h.do(http.MethodGet, "/v1/me", accessToken, nil).expect(http.StatusOK)

	// Rewrite the stored audience to another resource, as if the token had been
	// issued by this server for a different one.
	if _, err := h.store.DB().Exec(
		`UPDATE oauth_access_tokens SET audience = 'https://someone-else.example.com' WHERE token_hash = ?`,
		store.HashSecret(accessToken),
	); err != nil {
		t.Fatalf("rewrite audience: %v", err)
	}

	res := h.do(http.MethodGet, "/v1/me", accessToken, nil).expect(http.StatusUnauthorized)

	if !strings.Contains(res.Header.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Errorf("challenge %q should report invalid_token", res.Header.Get("WWW-Authenticate"))
	}
}

func TestMCPResourceAudienceAccepted(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	const redirectURI = "http://127.0.0.1:41234/callback"

	clientID := h.registerClient(redirectURI)

	// A client naming the more specific MCP endpoint as the resource, which the
	// spec tells it to prefer.
	location := h.consent(h.session(u), clientID, redirectURI,
		url.Values{"resource": {testBaseURL + "/mcp"}})

	body := h.form("/oauth/token", url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {codeFrom(t, location)},
		"code_verifier": {testVerifier},
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"resource":      {testBaseURL + "/mcp"},
	}).expect(http.StatusOK).decode()

	accessToken, _ := body["access_token"].(string)
	h.do(http.MethodGet, "/v1/me", accessToken, nil).expect(http.StatusOK)
}

func TestTokenExchangeRejectsRetargetedResource(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	const redirectURI = "http://127.0.0.1:41234/callback"

	clientID := h.registerClient(redirectURI)
	location := h.consent(h.session(u), clientID, redirectURI, nil)

	// The code was bound to the base resource; asking for a different one at the
	// token endpoint must not silently retarget the grant.
	res := h.form("/oauth/token", url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {codeFrom(t, location)},
		"code_verifier": {testVerifier},
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"resource":      {testBaseURL + "/mcp"},
	}).expect(http.StatusBadRequest)

	if got := res.decode()["error"]; got != "invalid_target" {
		t.Errorf("error = %v, want invalid_target", got)
	}
}

// ---------------------------------------------------------------------------
// Revocation and grants
// ---------------------------------------------------------------------------

func TestRevocationEndpoint(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	clientID, accessToken, refreshToken := h.fullFlow(u)

	h.form("/oauth/revoke", url.Values{
		"token":           {refreshToken},
		"token_type_hint": {"refresh_token"},
		"client_id":       {clientID},
	}).expect(http.StatusOK)

	// Revoking a refresh token takes the access token with it.
	h.do(http.MethodGet, "/v1/me", accessToken, nil).expect(http.StatusUnauthorized)

	// RFC 7009: revoking an unknown token still succeeds, so it cannot be used
	// to probe which tokens exist.
	h.form("/oauth/revoke", url.Values{
		"token":     {"cmrt_never-existed"},
		"client_id": {clientID},
	}).expect(http.StatusOK)
}

func TestGrantsListedAndRevocable(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	_, accessToken, _ := h.fullFlow(u)
	session := h.session(u)

	grants := h.doCookie(http.MethodGet, "/v1/grants", session, nil).
		expect(http.StatusOK).list()

	if len(grants) != 1 {
		t.Fatalf("grants = %d, want 1", len(grants))
	}

	if grants[0]["client_name"] != "Test MCP Client" {
		t.Errorf("client_name = %v, want the registered name", grants[0]["client_name"])
	}

	grantID, _ := grants[0]["id"].(string)

	h.doCookie(http.MethodDelete, "/v1/grants/"+grantID, session, nil).
		expect(http.StatusNoContent)

	// Withdrawing consent kills the token immediately.
	h.do(http.MethodGet, "/v1/me", accessToken, nil).expect(http.StatusUnauthorized)

	if items := h.doCookie(http.MethodGet, "/v1/grants", session, nil).
		expect(http.StatusOK).list(); len(items) != 0 {
		t.Errorf("grants = %d after revocation, want 0", len(items))
	}
}

// TestReconsentReusesTheGrant exercises the ON CONFLICT path on oauth_grants,
// whose conflict target is a partial unique index. A client reconnecting must
// update its existing consent rather than fail on the index or accumulate
// duplicates.
func TestReconsentReusesTheGrant(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	session := h.session(u)

	const redirectURI = "http://127.0.0.1:41234/callback"

	clientID := h.registerClient(redirectURI)

	// First connection, with both scopes.
	first := h.consent(session, clientID, redirectURI, nil)
	h.exchange(clientID, codeFrom(t, first), testVerifier, redirectURI).expect(http.StatusOK)

	grants := h.doCookie(http.MethodGet, "/v1/grants", session, nil).
		expect(http.StatusOK).list()
	if len(grants) != 1 {
		t.Fatalf("grants = %d after the first consent, want 1", len(grants))
	}

	grantID, _ := grants[0]["id"].(string)

	// The same client reconnects, this time asking for less.
	second := h.consent(session, clientID, redirectURI, url.Values{"scope": {"read"}})

	body := h.exchange(clientID, codeFrom(t, second), testVerifier, redirectURI).
		expect(http.StatusOK).decode()

	if got := body["scope"]; got != "read" {
		t.Errorf("scope = %v after reconsenting to read only, want read", got)
	}

	grants = h.doCookie(http.MethodGet, "/v1/grants", session, nil).
		expect(http.StatusOK).list()

	if len(grants) != 1 {
		t.Fatalf("grants = %d after reconsent, want the one grant updated in place", len(grants))
	}

	if grants[0]["id"] != grantID {
		t.Errorf("grant id changed from %v to %v; the consent should be updated, not replaced",
			grantID, grants[0]["id"])
	}

	// The narrower consent is what is recorded, not the union with the old one.
	scopes, _ := grants[0]["scopes"].([]any)
	if len(scopes) != 1 || scopes[0] != "read" {
		t.Errorf("recorded scopes = %v, want just read; a reconsent must not silently keep "+
			"a wider grant the user was not shown", scopes)
	}

	// And a fresh authorization after revoking works, since the unique index is
	// partial on revoked_at.
	h.doCookie(http.MethodDelete, "/v1/grants/"+grantID, session, nil).
		expect(http.StatusNoContent)

	third := h.consent(session, clientID, redirectURI, nil)
	h.exchange(clientID, codeFrom(t, third), testVerifier, redirectURI).expect(http.StatusOK)

	if items := h.doCookie(http.MethodGet, "/v1/grants", session, nil).
		expect(http.StatusOK).list(); len(items) != 1 {
		t.Errorf("grants = %d after revoke and re-authorize, want 1", len(items))
	}
}

func TestGrantsScopedToOwner(t *testing.T) {
	h := newHarness(t)
	alice := h.user("alice@example.com")
	bob := h.user("bob@example.com")

	h.fullFlow(alice)

	if items := h.doCookie(http.MethodGet, "/v1/grants", h.session(bob), nil).
		expect(http.StatusOK).list(); len(items) != 0 {
		t.Errorf("bob sees %d of alice's grants, want 0", len(items))
	}
}

// ---------------------------------------------------------------------------
// Client ID Metadata Documents
// ---------------------------------------------------------------------------

func TestClientIDMetadataDocumentFlow(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	const redirectURI = "http://127.0.0.1:41234/callback"

	// The client publishes its own metadata; there is no registration step.
	var documentURL string

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=600")

		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":                  documentURL,
			"client_name":                "Self-Describing Client",
			"client_uri":                 "https://app.example.com",
			"redirect_uris":              []string{redirectURI},
			"grant_types":                []string{"authorization_code", "refresh_token"},
			"response_types":             []string{"code"},
			"token_endpoint_auth_method": "none",
			"application_type":           "native",
		})
	}))
	defer server.Close()

	documentURL = server.URL + "/client.json"

	// A TLS test server, because the https requirement on a client_id URL is not
	// relaxed even in tests. The harness only relaxes the address check and the
	// certificate chain, so the fetch and validation path is the real one.
	location := h.consent(h.session(u), documentURL, redirectURI, nil)

	body := h.exchange(documentURL, codeFrom(t, location), testVerifier, redirectURI).
		expect(http.StatusOK).decode()

	accessToken, _ := body["access_token"].(string)
	if accessToken == "" {
		t.Fatal("no token issued to a metadata-document client")
	}

	h.do(http.MethodGet, "/v1/me", accessToken, nil).expect(http.StatusOK)

	// The document was cached as a client row, so consent can name it.
	grants := h.doCookie(http.MethodGet, "/v1/grants", h.session(u), nil).
		expect(http.StatusOK).list()

	if len(grants) != 1 || grants[0]["client_name"] != "Self-Describing Client" {
		t.Errorf("grants = %v, want one naming the document's client_name", grants)
	}
}

func TestClientIDMetadataDocumentRejections(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")
	session := h.session(u)

	const redirectURI = "http://127.0.0.1:41234/callback"

	cases := map[string]func(documentURL string) map[string]any{
		"client_id does not match the URL": func(string) map[string]any {
			return map[string]any{
				"client_id":     "https://elsewhere.example.com/client.json",
				"client_name":   "Impersonator",
				"redirect_uris": []string{redirectURI},
			}
		},
		"no client_name": func(documentURL string) map[string]any {
			return map[string]any{
				"client_id":     documentURL,
				"redirect_uris": []string{redirectURI},
			}
		},
		"no redirect_uris": func(documentURL string) map[string]any {
			return map[string]any{
				"client_id":   documentURL,
				"client_name": "No Redirects",
			}
		},
		"unusable redirect": func(documentURL string) map[string]any {
			return map[string]any{
				"client_id":     documentURL,
				"client_name":   "Bad Redirect",
				"redirect_uris": []string{"http://evil.example.com/cb"},
			}
		},
	}

	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			var documentURL string

			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(build(documentURL))
			}))
			defer server.Close()

			documentURL = server.URL + "/client.json"

			q := authorizeParams(documentURL, redirectURI, nil)

			res := h.doCookie(http.MethodGet, "/oauth/authorize?"+q.Encode(), session, nil)
			if res.Status == http.StatusOK && requestIDPattern.Match(res.Body) {
				t.Error("an invalid metadata document reached the consent screen")
			}
		})
	}
}

func TestNonJSONMetadataDocumentRejected(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "<html>not json</html>")
	}))
	defer server.Close()

	q := authorizeParams(server.URL+"/client.json", "http://127.0.0.1:41234/callback", nil)

	res := h.doCookie(http.MethodGet, "/oauth/authorize?"+q.Encode(), h.session(u), nil)
	if res.Status == http.StatusOK && requestIDPattern.Match(res.Body) {
		t.Error("a non-JSON document reached the consent screen")
	}
}

// TestDeviceTokenStillWorksAlongsideOAuth checks the two credential systems
// coexist: adding OAuth must not break the tokens the native apps already use.
func TestDeviceTokenStillWorksAlongsideOAuth(t *testing.T) {
	h := newHarness(t)
	u := h.user("you@example.com")

	_, oauthToken, _ := h.fullFlow(u)

	h.do(http.MethodGet, "/v1/me", oauthToken, nil).expect(http.StatusOK)
	h.do(http.MethodGet, "/v1/me", u.Token, nil).expect(http.StatusOK)

	// A refresh token is not an API credential.
	_, _, refresh := h.fullFlow(u)
	h.do(http.MethodGet, "/v1/me", refresh, nil).expect(http.StatusUnauthorized)
}
