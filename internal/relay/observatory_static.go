package relay

import (
	"io/fs"
	"net/http"
	"strings"
)

// serveObservatoryStatic serves the embedded observatory dashboard SPA
// (a Vite/React app built into internal/web/static/observatory) at the
// /observatory/ prefix.
//
// Mirrors serveChatStatic, but uses a try-file-then-index strategy so the
// dashboard's react-router routes (/observatory/projects/:slug, /agents/:slug,
// /sessions/:id, /tasks/:id, /budgets) all fall back to index.html for
// client-side routing, while real asset requests (/observatory/assets/*) are
// served from the embedded FS.
//
// ServeMux longest-prefix routing guarantees this handler never sees
// /observatory/api/v1/* — those are claimed by the ingest and read handlers.
func (r *Relay) serveObservatoryStatic(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.NotFound(w, req)
		return
	}
	if r.ObservatoryStaticFS == nil {
		http.Error(w, "observatory UI not built — run 'make ui' first", http.StatusServiceUnavailable)
		return
	}

	rel := strings.TrimPrefix(req.URL.Path, "/observatory/")
	if rel != "" {
		// Serve the file if it exists in the embedded FS (an asset); otherwise
		// fall through to the SPA index for client-side routing.
		if f, err := r.ObservatoryStaticFS.Open(rel); err == nil {
			_ = f.Close()
			http.StripPrefix("/observatory/", http.FileServer(http.FS(r.ObservatoryStaticFS))).ServeHTTP(w, req)
			return
		}
	}
	r.serveObservatoryIndex(w)
}

// serveObservatoryIndex writes the SPA entrypoint (index.html).
func (r *Relay) serveObservatoryIndex(w http.ResponseWriter) {
	data, err := fs.ReadFile(r.ObservatoryStaticFS, "index.html")
	if err != nil {
		http.Error(w, "observatory UI not built — run 'make ui' first", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}
