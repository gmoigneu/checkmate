package httpapi

import (
	"errors"
	"log/slog"
	"net"
	"net/http"

	"github.com/nls/checkmate/server/internal/login"
	"github.com/nls/checkmate/server/internal/store"
)

// handleLoginStart redirects the browser to the identity provider.
func (s *Server) handleLoginStart(w http.ResponseWriter, r *http.Request) {
	if s.login == nil || !s.login.Enabled() {
		s.writeError(w, r, http.StatusNotImplemented, "no identity provider is configured")

		return
	}

	provider := r.PathValue("provider")

	authURL, err := s.login.Begin(r.Context(), provider, r.URL.Query().Get("redirect_to"))
	if err != nil {
		s.log.Warn("start login", slog.String("provider", provider), slog.Any("error", err))
		s.writeError(w, r, http.StatusBadRequest, "unknown identity provider")

		return
	}

	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleLoginCallback finishes the provider round trip and opens a session.
func (s *Server) handleLoginCallback(w http.ResponseWriter, r *http.Request) {
	if s.login == nil || !s.login.Enabled() {
		s.writeError(w, r, http.StatusNotImplemented, "no identity provider is configured")

		return
	}

	query := r.URL.Query()

	// The provider reports a refusal in-band; surface it rather than failing on
	// the missing code.
	if providerErr := query.Get("error"); providerErr != "" {
		s.log.Info("provider declined the login",
			slog.String("error", providerErr),
			slog.String("description", query.Get("error_description")))
		s.writeError(w, r, http.StatusUnauthorized, "the identity provider declined the sign-in")

		return
	}

	state, code := query.Get("state"), query.Get("code")
	if state == "" || code == "" {
		s.writeError(w, r, http.StatusBadRequest, "callback is missing state or code")

		return
	}

	result, err := s.login.Complete(r.Context(), state, code)
	if err != nil {
		s.writeLoginError(w, r, err)

		return
	}

	secret, _, err := s.store.CreateSession(
		r.Context(),
		result.UserID,
		s.cfg.SessionIdleTimeout,
		s.cfg.SessionMaxLifetime,
		r.UserAgent(),
		clientIP(r),
	)
	if err != nil {
		s.log.Error("create session", slog.Any("error", err))
		s.writeError(w, r, http.StatusInternalServerError, "could not start a session")

		return
	}

	s.setSessionCookie(w, secret)

	s.log.Info("signed in",
		slog.String("user_id", result.UserID),
		slog.Bool("provisioned", result.Created))

	http.Redirect(w, r, login.SafeRedirect(result.RedirectTo), http.StatusFound)
}

// writeLoginError maps a login failure onto a status, keeping the reason vague
// to the client while logging the detail.
func (s *Server) writeLoginError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrUnknownFlow):
		// Also what a replayed callback looks like, which is the point.
		s.writeError(w, r, http.StatusBadRequest,
			"this sign-in link has expired or was already used; please start again")

	case errors.Is(err, login.ErrNotAllowed):
		s.log.Warn("sign-in refused: address not on the allowlist")
		s.writeError(w, r, http.StatusForbidden,
			"this address does not have a Checkmate account")

	case errors.Is(err, login.ErrEmailUnverified):
		s.writeError(w, r, http.StatusForbidden,
			"the identity provider did not verify this email address")

	default:
		s.log.Error("complete login", slog.Any("error", err))
		s.writeError(w, r, http.StatusUnauthorized, "sign-in failed")
	}
}

// handleLogout ends the current session.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	// A bearer token has no session to end; revoking the token is a different
	// operation with its own endpoint.
	if !ident.ViaCookie() {
		s.writeError(w, r, http.StatusBadRequest,
			"not a cookie session; revoke the token instead")

		return
	}

	if r.URL.Query().Get("everywhere") == "true" {
		if err := s.store.RevokeUserSessions(r.Context(), ident.UserID); err != nil {
			s.log.Error("revoke all sessions", slog.Any("error", err))
			s.writeError(w, r, http.StatusInternalServerError, "internal server error")

			return
		}
	} else if err := s.store.RevokeSession(r.Context(), ident.SessionID); err != nil {
		s.log.Error("revoke session", slog.Any("error", err))
		s.writeError(w, r, http.StatusInternalServerError, "internal server error")

		return
	}

	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

type meResponse struct {
	UserID   string `json:"user_id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Timezone string `json:"timezone"`

	// AuthVia is "session" or "token", so a client can tell whether it is in a
	// browser session or running on a device credential.
	AuthVia string   `json:"auth_via"`
	Scopes  []string `json:"scopes"`
}

// handleMe reports who the caller is.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	via := "token"
	if ident.ViaCookie() {
		via = "session"
	}

	s.writeJSON(w, r, http.StatusOK, meResponse{
		UserID:   ident.UserID,
		Email:    ident.Email,
		Name:     ident.Name,
		Timezone: ident.Timezone,
		AuthVia:  via,
		Scopes:   ident.Scopes,
	})
}

type authConfigResponse struct {
	Providers []string `json:"providers"`
}

// handleAuthConfig tells the web UI which sign-in buttons to render. Public,
// because it is needed before anyone is authenticated.
func (s *Server) handleAuthConfig(w http.ResponseWriter, r *http.Request) {
	providers := []string{}
	if s.login != nil {
		providers = s.login.Providers()
	}

	s.writeJSON(w, r, http.StatusOK, authConfigResponse{Providers: providers})
}

// clientIP reports the caller's address for the session record.
//
// Deliberately RemoteAddr only. X-Forwarded-For is attacker-controlled unless a
// trusted proxy is known to overwrite it, and this value is diagnostic, so
// believing a spoofable header would only make the audit trail lie.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}
