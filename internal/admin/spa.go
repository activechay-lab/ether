package admin

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// ConsoleBuilt reports whether this binary was compiled with a built web
// console bundle in internal/admin/dist (as opposed to the unbuilt placeholder).
func ConsoleBuilt() bool {
	sub, err := fs.Sub(distFS, "dist")
	return err == nil && fileExists(sub, "index.html")
}

// handleSPA serves the embedded React build. Real files are served
// directly (fingerprinted assets get immutable caching); any other GET
// falls back to index.html so client-side routes survive reloads.
func (h *Handler) handleSPA(w http.ResponseWriter, r *http.Request) {
	if h.ui != nil {
		serveSPAFrom(w, r, h.ui)
		return
	}
	serveSPA(w, r)
}

// Shell serves the console's static files without its API, for a server
// whose settings and database are not open yet (internal/startup's status
// page). The owner's frame allowlist is not loaded then, so framing is
// refused outright.
func Shell() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = strings.TrimPrefix(r.URL.Path, "/ether")
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'; "+consoleScriptPolicy())
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		serveSPA(w, r2)
	})
}

func serveSPA(w http.ResponseWriter, r *http.Request) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "embedded UI unavailable")
		return
	}
	serveSPAFrom(w, r, sub)
}

func serveSPAFrom(w http.ResponseWriter, r *http.Request, sub fs.FS) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported here")
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" || name == "." {
		name = "index.html"
	}
	if name == "index.html" || !fileExists(sub, name) {
		serveIndex(w, r, sub)
		return
	}
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeFileFS(w, r, sub, name)
}

func fileExists(fsys fs.FS, name string) bool {
	fi, err := fs.Stat(fsys, name)
	return err == nil && !fi.IsDir()
}

func serveIndex(w http.ResponseWriter, r *http.Request, sub fs.FS) {
	if !fileExists(sub, "index.html") {
		// `go build` from a checkout without `pnpm build`: the API still
		// works; tell the owner how to build the UI rather than 404ing mutely.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusServiceUnavailable)
		if r.Method != http.MethodHead {
			_, _ = w.Write(placeholderHTML)
		}
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, sub, "index.html")
}
