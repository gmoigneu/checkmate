package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nls/checkmate/server/internal/database"
	"github.com/nls/checkmate/server/internal/oauth"
	"github.com/nls/checkmate/server/internal/store"
)

// ---------------------------------------------------------------------------
// Discovery
// ---------------------------------------------------------------------------

// handleAuthorizationServerMetadata serves the RFC 8414 document.
func (s *Server) handleAuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		s.writeError(w, r, http.StatusNotFound, "the authorization server is not enabled")

		return
	}

	// Discovery documents are public and stable; letting clients cache them
	// avoids a round trip on every connection.
	w.Header().Set("Cache-Control", "public, max-age=3600")
	s.writeJSON(w, r, http.StatusOK, s.oauth.Metadata())
}

// handleProtectedResourceMetadata serves the RFC 9728 document.
//
// Registered at both the root and a path-inserted form, because MCP clients that
// cannot read the WWW-Authenticate header probe
// /.well-known/oauth-protected-resource/<resource path> first and the bare root
// second.
func (s *Server) handleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		s.writeError(w, r, http.StatusNotFound, "the authorization server is not enabled")

		return
	}

	// The suffix identifies which resource is being asked about, so a token
	// audience-bound to /mcp gets metadata naming /mcp.
	resource := s.oauth.Resource()

	if suffix := strings.Trim(r.PathValue("resource"), "/"); suffix != "" {
		candidate := s.cfg.BaseURL + "/" + suffix

		if !containsString(s.oauth.AcceptedAudiences(), candidate) {
			s.writeError(w, r, http.StatusNotFound, "unknown resource")

			return
		}

		resource = candidate
	}

	w.Header().Set("Cache-Control", "public, max-age=3600")
	s.writeJSON(w, r, http.StatusOK, s.oauth.ResourceMetadata(resource))
}

// ---------------------------------------------------------------------------
// Authorization endpoint
// ---------------------------------------------------------------------------

// handleAuthorize starts an authorization request and shows the consent screen.
func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		s.writeError(w, r, http.StatusNotFound, "the authorization server is not enabled")

		return
	}

	query := r.URL.Query()

	params := oauth.AuthorizeParams{
		ClientID:            query.Get("client_id"),
		RedirectURI:         query.Get("redirect_uri"),
		ResponseType:        query.Get("response_type"),
		Scope:               query.Get("scope"),
		State:               query.Get("state"),
		CodeChallenge:       query.Get("code_challenge"),
		CodeChallengeMethod: query.Get("code_challenge_method"),
		Resource:            query.Get("resource"),
	}

	// Consent needs a human. An unauthenticated visitor is sent through the
	// sign-in flow and lands back on this same URL with its parameters intact.
	ident, ok := identityFrom(r.Context())
	if !ok {
		s.redirectToLogin(w, r)

		return
	}

	pending, err := s.oauth.BeginAuthorization(r.Context(), params, ident.UserID)
	if err != nil {
		s.writeAuthorizeError(w, r, params, err)

		return
	}

	s.renderConsent(w, r, pending, "")
}

// handleAuthorizeDecision records the user's answer to the consent screen.
func (s *Server) handleAuthorizeDecision(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		s.writeError(w, r, http.StatusNotFound, "the authorization server is not enabled")

		return
	}

	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "malformed form submission")

		return
	}

	requestID := r.PostFormValue("request_id")
	if requestID == "" {
		s.writeError(w, r, http.StatusBadRequest, "request_id is required")

		return
	}

	var (
		redirect string
		err      error
	)

	if r.PostFormValue("decision") == "approve" {
		redirect, err = s.oauth.CompleteAuthorization(r.Context(), requestID, ident.UserID)
	} else {
		redirect, err = s.oauth.DenyAuthorization(r.Context(), requestID, ident.UserID)
	}

	if err != nil {
		oauthErr := oauth.AsError(err)
		if oauthErr.Code == "server_error" {
			s.log.Error("complete authorization", slog.Any("error", err))
		}

		s.writeError(w, r, oauthErr.Status, oauthErr.Description)

		return
	}

	parsedRedirect, parseErr := url.Parse(redirect)
	if parseErr == nil && parsedRedirect.Scheme != "http" && parsedRedirect.Scheme != "https" {
		s.renderNativeAppRedirect(w, redirect)

		return
	}

	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

// redirectToLogin sends an unauthenticated visitor through sign-in and back.
func (s *Server) redirectToLogin(w http.ResponseWriter, r *http.Request) {
	if s.login == nil || !s.login.Enabled() {
		// Without a provider there is no way to authenticate a human in a
		// browser, so the flow cannot continue.
		s.writeError(w, r, http.StatusNotImplemented,
			"this server has no identity provider configured, so it cannot authorize interactive clients")

		return
	}

	providers := s.login.Providers()

	target := "/auth/login/" + providers[0] +
		"?redirect_to=" + url.QueryEscape(r.URL.RequestURI())

	http.Redirect(w, r, target, http.StatusFound)
}

// writeAuthorizeError decides whether an error may be redirected to the client.
//
// Errors are only sent to the redirect URI once the client and that URI have
// both been validated. Before then the URI is attacker-controlled input, and
// redirecting to it would make this endpoint an open redirect. Those errors are
// rendered to the user instead.
func (s *Server) writeAuthorizeError(
	w http.ResponseWriter,
	r *http.Request,
	params oauth.AuthorizeParams,
	err error,
) {
	oauthErr := oauth.AsError(err)

	if oauthErr.Code == "server_error" {
		s.log.Error("begin authorization", slog.Any("error", err))
	}

	redirectable := oauthErr.Code != "invalid_client" &&
		!strings.Contains(oauthErr.Description, "redirect_uri")

	if redirectable && params.RedirectURI != "" && params.ClientID != "" {
		// Re-verify: only redirect to a URI actually registered to this client.
		if client, resolveErr := s.oauth.ResolveClient(r.Context(), params.ClientID); resolveErr == nil {
			if client.AllowsRedirectURI(params.RedirectURI) {
				http.Redirect(w, r,
					s.oauth.ErrorRedirect(params.RedirectURI, params.State, oauthErr),
					http.StatusFound)

				return
			}
		}
	}

	s.renderOAuthError(w, r, oauthErr)
}

// ---------------------------------------------------------------------------
// Token endpoint
// ---------------------------------------------------------------------------

type tokenErrorBody struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		s.writeOAuthJSONError(w, r, &oauth.Error{
			Code: "invalid_request", Description: "the authorization server is not enabled", Status: 404,
		})

		return
	}

	if err := r.ParseForm(); err != nil {
		s.writeOAuthJSONError(w, r, &oauth.Error{
			Code: "invalid_request", Description: "malformed form body", Status: 400,
		})

		return
	}

	req := oauth.TokenRequest{
		GrantType:    r.PostFormValue("grant_type"),
		Code:         r.PostFormValue("code"),
		RedirectURI:  r.PostFormValue("redirect_uri"),
		CodeVerifier: r.PostFormValue("code_verifier"),
		RefreshToken: r.PostFormValue("refresh_token"),
		ClientID:     r.PostFormValue("client_id"),
		ClientSecret: r.PostFormValue("client_secret"),
		Resource:     r.PostFormValue("resource"),
		Scope:        r.PostFormValue("scope"),
	}

	// HTTP Basic takes precedence over form parameters, per OAuth 2.1.
	if clientID, secret, ok := basicAuth(r); ok {
		req.ClientID = clientID
		req.ClientSecret = secret
	}

	res, err := s.oauth.Token(r.Context(), req)
	if err != nil {
		oauthErr := oauth.AsError(err)
		if oauthErr.Code == "server_error" {
			s.log.Error("token endpoint", slog.Any("error", err))
		}

		s.writeOAuthJSONError(w, r, oauthErr)

		return
	}

	// Tokens must never be cached by an intermediary.
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, r, http.StatusOK, res)
}

func (s *Server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		s.writeError(w, r, http.StatusNotFound, "the authorization server is not enabled")

		return
	}

	if err := r.ParseForm(); err != nil {
		s.writeOAuthJSONError(w, r, &oauth.Error{
			Code: "invalid_request", Description: "malformed form body", Status: 400,
		})

		return
	}

	clientID := r.PostFormValue("client_id")
	if id, _, ok := basicAuth(r); ok {
		clientID = id
	}

	if clientID == "" {
		s.writeOAuthJSONError(w, r, &oauth.Error{
			Code: "invalid_client", Description: "client_id is required", Status: 401,
		})

		return
	}

	err := s.oauth.Revoke(r.Context(), clientID,
		r.PostFormValue("token"), r.PostFormValue("token_type_hint"))
	if err != nil {
		oauthErr := oauth.AsError(err)
		if oauthErr.Code == "server_error" {
			s.log.Error("revoke endpoint", slog.Any("error", err))
		}

		s.writeOAuthJSONError(w, r, oauthErr)

		return
	}

	// RFC 7009: a successful revocation returns 200 whether or not the token
	// existed. Reporting the difference would be a probing oracle.
	w.WriteHeader(http.StatusOK)
}

// ---------------------------------------------------------------------------
// Dynamic client registration
// ---------------------------------------------------------------------------

type registrationResponse struct {
	store.OAuthClient

	ClientIDIssuedAt int64  `json:"client_id_issued_at"`
	ClientSecret     string `json:"client_secret,omitempty"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		s.writeError(w, r, http.StatusNotFound, "the authorization server is not enabled")

		return
	}

	var meta oauth.ClientMetadata

	// Unknown fields are tolerated here, unlike the rest of the API: RFC 7591
	// lets clients send metadata this server does not implement, and rejecting
	// registration over an extra key would break interoperability.
	if err := s.decodeBodyLenient(w, r, &meta); err != nil {
		return
	}

	client, secret, err := s.oauth.RegisterDynamic(r.Context(), meta)
	if err != nil {
		oauthErr := oauth.AsError(err)
		if oauthErr.Code == "server_error" {
			s.log.Error("register client", slog.Any("error", err))
		}

		s.writeOAuthJSONError(w, r, oauthErr)

		return
	}

	s.log.Info("registered oauth client",
		slog.String("client_id", client.ID),
		slog.String("client_name", client.Name),
		slog.String("application_type", client.ApplicationType))

	s.writeJSON(w, r, http.StatusCreated, registrationResponse{
		OAuthClient:      client,
		ClientIDIssuedAt: parsedUnix(client.CreatedAt),
		ClientSecret:     secret,
	})
}

// ---------------------------------------------------------------------------
// Grants (the account UI's view of connected clients)
// ---------------------------------------------------------------------------

func (s *Server) handleListGrants(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	items, err := s.store.ListGrants(r.Context(), ident.UserID)
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	writeList(s, w, r, items, "")
}

func (s *Server) handleRevokeGrant(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	if err := s.store.RevokeGrant(r.Context(), ident.UserID, r.PathValue("id")); err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (s *Server) writeOAuthJSONError(w http.ResponseWriter, r *http.Request, err *oauth.Error) {
	w.Header().Set("Cache-Control", "no-store")

	// RFC 6749: an invalid_client at the token endpoint gets a challenge header.
	if err.Code == "invalid_client" {
		w.Header().Set("WWW-Authenticate", `Basic realm="checkmate"`)
	}

	status := err.Status
	if status == 0 {
		status = http.StatusBadRequest
	}

	s.writeJSON(w, r, status, tokenErrorBody{
		Error:            err.Code,
		ErrorDescription: err.Description,
	})
}

// basicAuth reads client credentials from an Authorization: Basic header.
//
// Hand-rolled rather than using r.BasicAuth because OAuth requires the values to
// be form-urlencoded inside the header, which the stdlib does not undo.
func basicAuth(r *http.Request) (clientID, secret string, ok bool) {
	header := r.Header.Get("Authorization")

	scheme, encoded, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Basic") {
		return "", "", false
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", "", false
	}

	rawID, rawSecret, found := strings.Cut(string(decoded), ":")
	if !found {
		return "", "", false
	}

	clientID, err = url.QueryUnescape(rawID)
	if err != nil {
		return "", "", false
	}

	secret, err = url.QueryUnescape(rawSecret)
	if err != nil {
		return "", "", false
	}

	return clientID, secret, true
}

func containsString(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}

	return false
}

// parsedUnix converts a stored RFC3339 timestamp to seconds, for the
// client_id_issued_at field RFC 7591 defines as a Unix time.
func parsedUnix(timestamp string) int64 {
	parsed, err := time.Parse(database.Timestamp, timestamp)
	if err != nil {
		return 0
	}

	return parsed.Unix()
}

// decodeBodyLenient decodes JSON without rejecting unknown fields.
//
// Used only for client registration, where RFC 7591 expects a server to ignore
// metadata it does not implement. Everywhere else unknown fields are an error.
func (s *Server) decodeBodyLenient(w http.ResponseWriter, r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" && !isJSONContentType(ct) {
		s.writeError(w, r, http.StatusUnsupportedMediaType, "expected application/json")

		return errHandled
	}

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))

	if err := dec.Decode(dst); err != nil {
		s.writeOAuthJSONError(w, r, &oauth.Error{
			Code:        "invalid_client_metadata",
			Description: "the request body is not valid JSON",
			Status:      http.StatusBadRequest,
		})

		return errHandled
	}

	return nil
}
