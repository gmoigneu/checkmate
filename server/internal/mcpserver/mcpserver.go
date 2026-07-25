// Package mcpserver exposes Checkmate over the Model Context Protocol.
//
// The protocol layer is the official SDK (github.com/modelcontextprotocol/go-sdk),
// which is spec-complete for revision 2025-11-25. Hand-rolling JSON-RPC framing,
// the Streamable HTTP transport, SSE, session handling and resumability would be
// a large amount of subtle code with no upside; what is worth writing by hand is
// the part specific to Checkmate, which is the tool surface and how it maps onto
// the ownership and scope model the rest of the server already enforces.
//
// Authentication bridges into the SDK through auth.RequireBearerToken with a
// verifier that resolves a Checkmate credential. That means an MCP request is
// authorized by exactly the same store calls as a REST request, and the user id a
// tool sees is one the caller could not choose.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nls/checkmate/server/internal/model"
	"github.com/nls/checkmate/server/internal/oauth"
	"github.com/nls/checkmate/server/internal/recurrence"
	"github.com/nls/checkmate/server/internal/store"
)

// ServerName and ServerVersion identify this server to clients.
const (
	ServerName    = "checkmate"
	ServerVersion = "1.0.0"
)

// instructions is sent to the client on initialize and ends up in the model's
// context, so it is written for a model rather than for a person: what the
// vocabulary means, and which tool to reach for.
const instructions = `Checkmate is a personal task manager.

Structure:
- A "context" is a top-level area of life or work (for example Upsun, Personal).
  Every task belongs to at most one.
- A "project" groups tasks inside a single context.
- A task with no context is in the inbox: it was captured quickly and not yet
  triaged. Creating a task without a context_id is the right way to capture
  something when the area is not obvious.
- "due_on" is when a task is actually due. "planned_on" is the day the user
  intends to work on it. They are different and both optional.
- A task delegated to someone has status "delegated" and names that person; use
  delegate_task rather than setting the status by hand.

Start with daily_brief for an overview of the day before making changes. Use
list_contexts to learn the available context ids, since they are per-user.
All dates are YYYY-MM-DD in the user's own timezone.`

// Handler serves the MCP endpoint.
type Handler struct {
	store   *store.Store
	spawner *recurrence.Spawner
	log     *slog.Logger

	// audiences is the set of resource identifiers a token may be bound to.
	audiences []string

	// resourceMetadataURL is handed to clients in the WWW-Authenticate challenge
	// so an unauthenticated one can discover where to authenticate.
	resourceMetadataURL string

	// allowedOrigins, when non-empty, restricts the Origin header.
	allowedOrigins []string

	handler http.Handler
}

// Options configures the MCP handler.
type Options struct {
	// Audiences are the RFC 8707 resource identifiers this endpoint accepts.
	Audiences []string

	// ResourceMetadataURL is the RFC 9728 document for this resource.
	ResourceMetadataURL string

	// AllowedOrigins restricts the Origin header when non-empty. See the comment
	// on checkOrigin for why the default is permissive.
	AllowedOrigins []string
}

// New builds the MCP endpoint handler.
func New(
	st *store.Store,
	spawner *recurrence.Spawner,
	log *slog.Logger,
	opts Options,
) *Handler {
	h := &Handler{
		store:               st,
		spawner:             spawner,
		log:                 log,
		audiences:           opts.Audiences,
		resourceMetadataURL: opts.ResourceMetadataURL,
		allowedOrigins:      opts.AllowedOrigins,
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    ServerName,
		Title:   "Checkmate",
		Version: ServerVersion,
	}, &mcp.ServerOptions{
		Instructions: instructions,
		Logger:       log,
	})

	h.registerTools(server)

	streamable := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{
			// Stateless: every request carries its own bearer token and this
			// server holds no per-session state, so requiring clients to track an
			// Mcp-Session-Id would add a failure mode without buying anything.
			// The spec makes session ids optional for exactly this case.
			Stateless: true,

			// Answer with application/json rather than an SSE stream. Clients
			// must support both, and nothing here streams: every tool is a
			// handful of sqlite queries, so there are no progress notifications
			// to interleave and no long-running call to keep a connection open
			// for. The plain form is also the one a person can read with curl,
			// which matters more than it sounds for a server you operate alone.
			JSONResponse: true,

			Logger: log,
		},
	)

	// Order matters: the origin check runs before authentication so a rejected
	// origin never reaches a token lookup, and scope enforcement runs after, once
	// there is a token whose scopes can be compared.
	h.handler = h.checkOrigin(
		auth.RequireBearerToken(h.verifyToken, &auth.RequireBearerTokenOptions{
			ResourceMetadataURL: opts.ResourceMetadataURL,

			// Read is the floor: it is enough to initialize and list tools. Write
			// tools are gated individually by enforceToolScope, because a
			// blanket write requirement would stop a read-only client from
			// connecting at all.
			Scopes: []string{oauth.ScopeRead},
		})(h.enforceToolScope(streamable)),
	)

	return h
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.handler.ServeHTTP(w, r)
}

// verifyToken resolves a Checkmate credential for the SDK.
//
// Both credential kinds are accepted. An OAuth access token is audience-checked
// against this resource, which is what MCP requires. A device token is not
// audience-bound because it predates the OAuth surface, but it is issued by the
// account owner for this server and cannot be presented anywhere else, so it
// satisfies the requirement that a resource server only accept tokens intended
// for it. Accepting it is what makes pointing a local MCP client at Checkmate a
// one-line configuration.
//
// A session cookie is deliberately not accepted here: cookies are attached by
// browsers automatically, and the MCP endpoint is the one place where that would
// turn a cross-site page into an authenticated caller.
func (h *Handler) verifyToken(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	var (
		ident model.Identity
		err   error
	)

	switch {
	case strings.HasPrefix(token, oauth.AccessTokenPrefix):
		ident, err = h.store.AuthenticateAccessToken(ctx, token, h.audiences)

	case strings.HasPrefix(token, oauth.RefreshTokenPrefix):
		// A refresh token is for the token endpoint, never for a resource.
		return nil, fmt.Errorf("%w: a refresh token cannot be used as an access token",
			auth.ErrInvalidToken)

	default:
		ident, err = h.store.AuthenticateToken(ctx, token)
	}

	if err != nil {
		switch {
		case errors.Is(err, store.ErrWrongAudience):
			return nil, fmt.Errorf("%w: issued for a different resource", auth.ErrInvalidToken)
		case errors.Is(err, store.ErrInvalidToken):
			return nil, fmt.Errorf("%w: unknown, revoked or expired", auth.ErrInvalidToken)
		default:
			h.log.Error("mcp: verify token", slog.Any("error", err))

			return nil, err
		}
	}

	return &auth.TokenInfo{
		Scopes:     ident.Scopes,
		Expiration: ident.ExpiresAt,

		// The SDK uses UserID to bind a session to one user, so a leaked session
		// id cannot be replayed by somebody else.
		UserID: ident.UserID,

		Extra: map[string]any{
			extraTimezone: ident.Timezone,
			extraEmail:    ident.Email,
		},
	}, nil
}

// Keys for the TokenInfo.Extra map.
const (
	extraTimezone = "timezone"
	extraEmail    = "email"
)

// caller extracts the authenticated user from a tool handler's context.
//
// The bearer middleware has already run by the time a tool executes, so a miss
// means the handler was mounted without it, which is a wiring bug rather than
// anything the client did.
func caller(ctx context.Context) (userID, timezone string, scopes []string, err error) {
	info := auth.TokenInfoFromContext(ctx)
	if info == nil || info.UserID == "" {
		return "", "", nil, errors.New("mcp: no authenticated caller on the context")
	}

	timezone = "UTC"
	if value, ok := info.Extra[extraTimezone].(string); ok && value != "" {
		timezone = value
	}

	return info.UserID, timezone, info.Scopes, nil
}

// requireWrite reports a scope error for a tool that mutates data.
//
// Defence in depth: enforceToolScope already rejects the HTTP request with a 403
// challenge, which is the response that lets a client step up. This is the second
// check, so a write cannot happen if that middleware is ever bypassed or if a new
// write tool is added to the wrong list.
func requireWrite(scopes []string) error {
	if slices.Contains(scopes, oauth.ScopeWrite) {
		return nil
	}

	return fmt.Errorf("this token only has the %q scope; %q is required to change anything",
		strings.Join(scopes, " "), oauth.ScopeWrite)
}

// checkOrigin implements the transport's DNS rebinding protection.
//
// The spec requires validating Origin, aimed at local servers that have no
// authentication: a web page could otherwise drive one through the user's
// browser. That threat does not apply here, because the only accepted credential
// is a bearer token which no browser attaches automatically, and cookies are
// refused. Rejecting a foreign Origin outright would break legitimate
// browser-hosted MCP clients that send their own origin.
//
// So the check is an opt-in allowlist: empty means any origin, and setting
// CHECKMATE_MCP_ALLOWED_ORIGINS makes it strict. When it does reject, it answers
// 403 with a JSON-RPC error carrying no id, as the spec prescribes.
func (h *Handler) checkOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		if origin != "" && len(h.allowedOrigins) > 0 {
			if !slices.Contains(h.allowedOrigins, strings.TrimRight(origin, "/")) {
				h.log.Warn("mcp: rejected a request from an unlisted origin",
					slog.String("origin", origin))

				writeRPCError(w, http.StatusForbidden, -32600, "origin not allowed")

				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// toolScopes maps a tool name to the scope it needs. Anything absent needs only
// the read scope the transport already requires.
var toolScopes = map[string]string{}

// enforceToolScope answers a write tool call with 403 insufficient_scope when the
// token cannot perform it.
//
// This means peeking at the JSON-RPC body before the SDK reads it. The
// alternative — letting the tool run and returning a tool error — would tell the
// model it failed but would not tell the client how to fix it. A 403 with
// WWW-Authenticate naming the missing scope is what drives the spec's step-up
// flow, so a client can obtain a wider token and retry rather than giving up.
func (h *Handler) enforceToolScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)

			return
		}

		info := auth.TokenInfoFromContext(r.Context())
		if info == nil || slices.Contains(info.Scopes, oauth.ScopeWrite) {
			// Either not our concern, or the token can already do everything.
			next.ServeHTTP(w, r)

			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, maxRPCBody))
		if err != nil {
			writeRPCError(w, http.StatusBadRequest, -32700, "could not read the request body")

			return
		}

		// The body has been consumed, so hand the SDK an identical reader.
		r.Body = io.NopCloser(strings.NewReader(string(body)))

		if name, ok := toolCallName(body); ok {
			if required, needs := toolScopes[name]; needs && required == oauth.ScopeWrite {
				h.challengeInsufficientScope(w, name)

				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// maxRPCBody caps the peeked body. Tool arguments are small; a details field is
// prose, not an upload.
const maxRPCBody = 4 << 20

// toolCallName extracts params.name from a tools/call request, if that is what
// this body is.
func toolCallName(body []byte) (string, bool) {
	var envelope struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}

	// A body this layer cannot parse is not rejected here: the SDK owns protocol
	// errors and will produce a properly formed one.
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", false
	}

	if envelope.Method != "tools/call" || envelope.Params.Name == "" {
		return "", false
	}

	return envelope.Params.Name, true
}

// challengeInsufficientScope writes the 403 that drives step-up authorization.
func (h *Handler) challengeInsufficientScope(w http.ResponseWriter, tool string) {
	params := []string{
		`Bearer error="insufficient_scope"`,
		`scope="` + oauth.ScopeRead + " " + oauth.ScopeWrite + `"`,
		`error_description="the ` + oauth.ScopeWrite + ` scope is required to call ` + tool + `"`,
	}

	if h.resourceMetadataURL != "" {
		params = append(params, `resource_metadata="`+h.resourceMetadataURL+`"`)
	}

	w.Header().Set("WWW-Authenticate", strings.Join(params, ", "))

	writeRPCError(w, http.StatusForbidden, -32600,
		"the write scope is required to call "+tool)
}

// writeRPCError writes a JSON-RPC error response with no id, which is the shape
// the transport spec allows for a request rejected before it could be dispatched.
func writeRPCError(w http.ResponseWriter, status, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
		"id": nil,
	})
}
