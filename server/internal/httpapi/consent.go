package httpapi

import (
	"html/template"
	"log/slog"
	"net/http"

	"github.com/nls/checkmate/server/internal/oauth"
)

// The consent screen.
//
// Rendered server-side with html/template rather than handed to the frontend,
// because it is a security surface: it is the only place a user can tell a
// legitimate client from one impersonating it, and it has to work before any
// frontend has loaded.
//
// The redirect host is shown prominently because the MCP security guidance
// requires it. A Client ID Metadata Document proves who published the document,
// not who is receiving the code, so for loopback clients the hostname is the
// only signal a user has, and an extra warning is shown for those.
//
// All interpolation goes through html/template's contextual escaping. client_name
// and logo_uri come from a document a stranger controls, so they are untrusted
// text and are never emitted unescaped.
var consentTemplate = template.Must(template.New("consent").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>Authorize {{.Client.Name}} &middot; Checkmate</title>
<style>
  :root { color-scheme: light dark; }
  * { box-sizing: border-box; }
  body {
    margin: 0; min-height: 100vh; display: grid; place-items: center; padding: 1.5rem;
    font: 16px/1.55 -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
    background: #f6f7f9; color: #16181d;
  }
  .card {
    width: 100%; max-width: 27rem; background: #fff; border-radius: 14px;
    border: 1px solid #e3e6ea; padding: 1.75rem; box-shadow: 0 1px 3px rgb(0 0 0 / .06);
  }
  h1 { font-size: 1.2rem; margin: 0 0 .35rem; }
  .sub { color: #5c6370; font-size: .9rem; margin: 0 0 1.25rem; }
  dl { margin: 0 0 1.25rem; display: grid; grid-template-columns: auto 1fr; gap: .5rem 1rem; font-size: .9rem; }
  dt { color: #5c6370; }
  dd { margin: 0; word-break: break-all; }
  code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .85em; }
  ul.scopes { list-style: none; margin: 0 0 1.25rem; padding: 0; font-size: .9rem; }
  ul.scopes li { padding: .4rem 0; border-bottom: 1px solid #eef0f3; }
  ul.scopes li:last-child { border-bottom: 0; }
  .warn {
    background: #fff8e6; border: 1px solid #f0d999; border-radius: 8px;
    padding: .75rem .9rem; font-size: .85rem; margin: 0 0 1.25rem;
  }
  .err { background: #fdecec; border-color: #f3bcbc; }
  form { display: flex; gap: .6rem; }
  button {
    flex: 1; padding: .65rem 1rem; border-radius: 8px; font: inherit; font-weight: 600;
    cursor: pointer; border: 1px solid transparent;
  }
  .approve { background: #1f6feb; color: #fff; }
  .approve:hover { background: #1a5fd0; }
  .deny { background: #fff; border-color: #d3d7de; color: #16181d; }
  .deny:hover { background: #f3f4f6; }
  .who { margin: 1.1rem 0 0; font-size: .8rem; color: #5c6370; text-align: center; }
  @media (prefers-color-scheme: dark) {
    body { background: #0f1115; color: #e7e9ee; }
    .card { background: #171a20; border-color: #272b33; }
    .sub, dt, .who { color: #9aa1ad; }
    ul.scopes li { border-bottom-color: #23262d; }
    .warn { background: #2a2312; border-color: #5c4a1d; }
    .err { background: #2d1a1a; border-color: #5f2b2b; }
    .deny { background: #171a20; border-color: #373c46; color: #e7e9ee; }
    .deny:hover { background: #1e222a; }
  }
</style>
</head>
<body>
<main class="card">
  <h1>Authorize {{.Client.Name}}</h1>
  <p class="sub">
    {{if .AlreadyGranted}}This client is reconnecting to your Checkmate account.
    {{else}}This client is asking to connect to your Checkmate account.{{end}}
  </p>

  {{if .LoopbackWarning}}
  <p class="warn">
    <strong>This client receives the response on your own machine.</strong>
    Checkmate cannot verify which program is listening on that port, so only
    continue if you just started this connection yourself.
  </p>
  {{end}}

  <dl>
    <dt>Client</dt>
    <dd>{{.Client.Name}}{{if .Client.ClientURI}} &middot; {{.Client.ClientURI}}{{end}}</dd>
    <dt>Redirects to</dt>
    <dd>{{range .RedirectHosts}}<code>{{.}}</code> {{end}}</dd>
    <dt>Identified by</dt>
    <dd>{{if eq .Client.Kind "cimd"}}a published metadata document at <code>{{.Client.ID}}</code>
        {{else}}registration with this server{{end}}</dd>
    <dt>Access to</dt>
    <dd><code>{{.Resource}}</code></dd>
  </dl>

  <ul class="scopes">
    {{range .ScopeDescriptions}}<li>{{.}}</li>{{end}}
  </ul>

  <form method="POST" action="/oauth/authorize">
    <input type="hidden" name="request_id" value="{{.RequestID}}">
    <button class="deny" type="submit" name="decision" value="deny">Cancel</button>
    <button class="approve" type="submit" name="decision" value="approve">Authorize</button>
  </form>

  <p class="who">Signed in as {{.UserEmail}}</p>
</main>
</body>
</html>
`))

var oauthErrorTemplate = template.Must(template.New("oauth_error").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>Authorization failed &middot; Checkmate</title>
<style>
  :root { color-scheme: light dark; }
  body {
    margin: 0; min-height: 100vh; display: grid; place-items: center; padding: 1.5rem;
    font: 16px/1.55 -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
    background: #f6f7f9; color: #16181d;
  }
  .card {
    width: 100%; max-width: 27rem; background: #fff; border-radius: 14px;
    border: 1px solid #e3e6ea; padding: 1.75rem;
  }
  h1 { font-size: 1.15rem; margin: 0 0 .5rem; }
  p { margin: 0 0 .75rem; }
  code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .85em; }
  .hint { color: #5c6370; font-size: .875rem; margin: 0; }
  @media (prefers-color-scheme: dark) {
    body { background: #0f1115; color: #e7e9ee; }
    .card { background: #171a20; border-color: #272b33; }
    .hint { color: #9aa1ad; }
  }
</style>
</head>
<body>
<main class="card">
  <h1>Authorization failed</h1>
  <p>{{.Description}}</p>
  <p class="hint">Error code: <code>{{.Code}}</code></p>
  <p class="hint">Nothing was shared. You can close this window and try connecting again.</p>
</main>
</body>
</html>
`))

var nativeAppRedirectTemplate = template.Must(template.New("native_app_redirect").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>Return to Checkmate</title>
<style>
  :root { color-scheme: light dark; }
  body {
    margin: 0; min-height: 100vh; display: grid; place-items: center; padding: 1.5rem;
    font: 16px/1.55 -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
    background: #f6f7f9; color: #16181d;
  }
  .card {
    width: 100%; max-width: 27rem; background: #fff; border-radius: 14px;
    border: 1px solid #e3e6ea; padding: 1.75rem; text-align: center;
  }
  h1 { font-size: 1.2rem; margin: 0 0 .5rem; }
  p { color: #5c6370; font-size: .9rem; margin: 0 0 1.25rem; }
  a {
    display: block; padding: .7rem 1rem; border-radius: 8px; font-weight: 600;
    text-decoration: none; background: #1f6feb; color: #fff;
  }
  a:hover { background: #1a5fd0; }
  @media (prefers-color-scheme: dark) {
    body { background: #0f1115; color: #e7e9ee; }
    .card { background: #171a20; border-color: #272b33; }
    p { color: #9aa1ad; }
  }
</style>
</head>
<body>
<main class="card">
  {{if .Approved}}
  <h1>Authorization approved</h1>
  <p>Return to the Checkmate app to finish connecting your account.</p>
  {{else}}
  <h1>Authorization declined</h1>
  <p>Return to the Checkmate app to continue without connecting this account.</p>
  {{end}}
  <a href="{{.Redirect}}">Open Checkmate</a>
</main>
</body>
</html>
`))

type nativeAppRedirectView struct {
	Redirect template.URL
	Approved bool
}

// consentView is the data the consent template renders.
type consentView struct {
	oauth.PendingAuthorization

	ScopeDescriptions []string
	UserEmail         string
}

// scopeDescriptions turns scope names into something a human can consent to.
// A consent screen listing "read write" asks the user to approve jargon.
var scopeDescriptions = map[string]string{
	oauth.ScopeRead:  "See your tasks, projects, contexts and the people you delegate to",
	oauth.ScopeWrite: "Create, change and delete your tasks, projects and contexts",
	oauth.ScopeOfflineAccess: "Stay connected without asking you again " +
		"(it can refresh its own access until you disconnect it)",
}

func (s *Server) renderConsent(
	w http.ResponseWriter,
	r *http.Request,
	pending oauth.PendingAuthorization,
	_ string,
) {
	ident, _ := identityFrom(r.Context())

	descriptions := make([]string, 0, len(pending.Scopes))

	for _, scope := range pending.Scopes {
		if description, ok := scopeDescriptions[scope]; ok {
			descriptions = append(descriptions, description)

			continue
		}

		descriptions = append(descriptions, scope)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// A consent screen must never be framed: clickjacking it would let another
	// page harvest an approval the user did not mean to give.
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")

	view := consentView{
		PendingAuthorization: pending,
		ScopeDescriptions:    descriptions,
		UserEmail:            ident.Email,
	}

	if err := consentTemplate.Execute(w, view); err != nil {
		s.log.Error("render consent screen", slog.Any("error", err))
	}
}

func (s *Server) renderOAuthError(w http.ResponseWriter, _ *http.Request, err *oauth.Error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")

	status := err.Status
	if status == 0 {
		status = http.StatusBadRequest
	}

	w.WriteHeader(status)

	if execErr := oauthErrorTemplate.Execute(w, err); execErr != nil {
		s.log.Error("render oauth error page", slog.Any("error", execErr))
	}
}

func (s *Server) renderNativeAppRedirect(w http.ResponseWriter, redirect string, approved bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")

	// Both authorization outcomes derive their callback from the exact, previously
	// validated redirect registered to this client. Marking it as a template URL
	// preserves its custom scheme instead of html/template replacing it with #ZgotmplZ.
	view := nativeAppRedirectView{Redirect: template.URL(redirect), Approved: approved}
	if err := nativeAppRedirectTemplate.Execute(w, view); err != nil {
		s.log.Error("render native app redirect", slog.Any("error", err))
	}
}
