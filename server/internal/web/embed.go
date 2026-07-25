// Package web serves the checked-in TanStack Start client bundle embedded in the
// Checkmate binary. The API remains the source of truth; this package only owns
// browser navigation and immutable frontend assets.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Assets is refreshed by `npm run build` in web/ before a Go build.
//
//go:embed all:ui
var Assets embed.FS

// WithUI adds SPA navigation and static assets around the API handler. API and
// OAuth paths are deliberately passed through unchanged, so a browser request
// can never turn a malformed API URL into an HTML response.
func WithUI(api http.Handler) http.Handler {
	files, err := fs.Sub(Assets, "ui")
	if err != nil {
		panic(err)
	}

	static := http.FileServer(http.FS(files))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			api.ServeHTTP(w, r)
			return
		}
		if isAPIPath(r.URL.Path) {
			api.ServeHTTP(w, r)
			return
		}

		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" {
			if file, openErr := files.Open(name); openErr == nil {
				_ = file.Close()
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				static.ServeHTTP(w, r)
				return
			}
		}

		// Every non-API route is client navigation. Serve the shell without a
		// cache so deployments can replace the hashed asset references safely.
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method == http.MethodHead {
			return
		}
		shell, readErr := fs.ReadFile(files, "index.html")
		if readErr != nil {
			http.Error(w, "Checkmate web bundle is unavailable", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(shell)
	})
}

func isAPIPath(requestPath string) bool {
	for _, prefix := range []string{"/v1/", "/auth/", "/oauth/", "/mcp", "/.well-known/", "/healthz"} {
		if requestPath == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(requestPath, prefix) {
			return true
		}
	}
	return false
}
