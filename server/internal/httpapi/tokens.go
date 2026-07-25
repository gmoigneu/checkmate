package httpapi

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/nls/checkmate/server/internal/database"
	"github.com/nls/checkmate/server/internal/store"
)

type tokenCreateRequest struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	ExpiresAt string   `json:"expires_at"`
}

// tokenCreateResponse is the only place a token secret ever appears.
type tokenCreateResponse struct {
	store.TokenInfo

	// Token is shown once. Only its hash is kept, so it cannot be retrieved.
	Token string `json:"token"`
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	items, err := s.store.ListTokens(r.Context(), ident.UserID)
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	writeList(s, w, r, items, "")
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	// Minting a long-lived credential from a long-lived credential would let a
	// leaked token renew itself forever. Requiring a browser session means
	// stealing one token does not grant the ability to issue more.
	if !ident.ViaCookie() {
		s.writeError(w, r, http.StatusForbidden,
			"tokens can only be issued from a signed-in session; use the CLI otherwise")

		return
	}

	var req tokenCreateRequest
	if err := s.decodeBody(w, r, &req); err != nil {
		return
	}

	v := newValidationError()

	name := strings.TrimSpace(req.Name)
	if name == "" {
		v.add("name", "is required")
	}

	for _, scope := range req.Scopes {
		if !slices.Contains([]string{ScopeRead, ScopeWrite}, scope) {
			v.add("scopes", "must contain only read and write")

			break
		}
	}

	if req.ExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, req.ExpiresAt)

		switch {
		case err != nil:
			v.add("expires_at", "must be an RFC3339 timestamp")
		case !parsed.After(time.Now()):
			v.add("expires_at", "must be in the future")
		default:
			req.ExpiresAt = parsed.UTC().Format(database.Timestamp)
		}
	}

	if v.any() {
		s.writeStoreError(w, r, v)

		return
	}

	secret, info, err := s.store.IssueToken(
		r.Context(), ident.UserID, name, strings.Join(req.Scopes, " "), req.ExpiresAt)
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	s.writeJSON(w, r, http.StatusCreated, tokenCreateResponse{TokenInfo: info, Token: secret})
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	if err := s.store.RevokeToken(r.Context(), ident.UserID, r.PathValue("id")); err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
