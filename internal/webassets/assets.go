// Package webassets serves the production administration UI without Node.
package webassets

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// Generate dist with `sh scripts/build-web.sh` before compiling or testing Go
// packages that import webassets. GoReleaser runs this step through its build
// hooks. Git ignores the output; the compiled executable serves it without Node.
//
// Include Start's underscore-prefixed route chunks as well as ordinary assets.
//
//go:embed all:dist
var assets embed.FS

// Handler returns the UI handler. Mount API/session/health handlers separately;
// reserved namespaces never fall through to the SPA, even when unimplemented.
func Handler() http.Handler {
	root, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, prefix := range []string{"/api", "/mcp", "/session", "/health", "/metrics", "/debug"} {
			if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
				http.NotFound(w, r)
				return
			}
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name != "" && (!fs.ValidPath(name) || path.Clean(name) != name) {
			http.NotFound(w, r)
			return
		}
		spa := name == "" || name == "index.html"
		if !spa {
			for _, route := range []string{"overview", "performance", "queries", "clients", "dhcp", "lists", "rules", "records", "upstreams", "settings", "jobs", "diagnostics"} {
				if name == route {
					spa = true
					break
				}
			}
		}
		if spa {
			name = "index.html"
		}
		content, err := fs.ReadFile(root, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("ETag", fmt.Sprintf(`"%x"`, sha256.Sum256(content)))
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(content))
	})
}
