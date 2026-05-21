package relay

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agent-relay/internal/db"
	"agent-relay/internal/models"
)

// chatIdentity resolves the caller's identity from EasyAuth or dev-mode fallback.
// Returns (email, true) on success or ("", false) on auth failure.
func (r *Relay) chatIdentity(w http.ResponseWriter, req *http.Request) (string, bool) {
	header := req.Header.Get("X-MS-CLIENT-PRINCIPAL")
	principal := parseClientPrincipal(header)
	if principal != nil {
		return principal.Email, true
	}
	if r.Config.DevMode {
		return "dev@local", true
	}
	http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	return "", false
}

func chatJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// chatSlug extracts :slug from a path after stripping prefix.
// For /chat/api/p/myproject/send with prefix /chat/api/p/ → "myproject".
func chatSlug(path, prefix string) string {
	s := strings.TrimPrefix(path, prefix)
	if idx := strings.Index(s, "/"); idx >= 0 {
		return s[:idx]
	}
	return s
}

// serveChatStatic serves the embedded Vite/React SPA.
//
// Routing within this handler:
//   - GET /chat/ and GET /chat/p/* → serve index.html (SPA fallback for client-side routing)
//   - Everything else → http.FileServer on the embedded chat sub-FS (assets, etc.)
//
// Architect note: http.StripPrefix("/chat/", ...) is required before delegating to
// http.FileServer so that embedded paths resolve correctly (e.g. /chat/assets/x → assets/x).
func (r *Relay) serveChatStatic(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.NotFound(w, req)
		return
	}
	if r.ChatStaticFS == nil {
		http.Error(w, "chat UI not built — run 'make ui' first", http.StatusServiceUnavailable)
		return
	}
	path := req.URL.Path
	if path == "/chat/" || strings.HasPrefix(path, "/chat/p/") {
		data, err := fs.ReadFile(r.ChatStaticFS, "index.html")
		if err != nil {
			http.Error(w, "chat UI not built — run 'make ui' first", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
		return
	}
	http.StripPrefix("/chat/", http.FileServer(http.FS(r.ChatStaticFS))).ServeHTTP(w, req)
}

// serveChatProjects handles GET /chat/api/projects
func (r *Relay) serveChatProjects(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.chatIdentity(w, req); !ok {
		return
	}

	projects, err := r.DB.ListChatProjects()
	if err != nil {
		chatJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list projects"})
		return
	}
	if projects == nil {
		projects = []models.ChatProject{}
	}
	chatJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

// serveChatSend handles POST /chat/api/p/:slug/send
func (r *Relay) serveChatSend(w http.ResponseWriter, req *http.Request) {
	email, ok := r.chatIdentity(w, req)
	if !ok {
		return
	}

	slug := chatSlug(req.URL.Path, "/chat/api/p/")
	if slug == "" {
		chatJSON(w, http.StatusBadRequest, map[string]string{"error": "missing project slug"})
		return
	}

	// Verify project exists (chat_executive_role check happens inside InsertChatMessage).
	project, err := r.DB.GetProject(slug)
	if err != nil {
		chatJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up project"})
		return
	}
	if project == nil {
		chatJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}

	var body struct {
		Content string `json:"content"`
	}
	if dErr := json.NewDecoder(req.Body).Decode(&body); dErr != nil || body.Content == "" {
		chatJSON(w, http.StatusBadRequest, map[string]string{"error": "content is required"})
		return
	}

	msg, err := r.DB.InsertChatMessage(slug, email, body.Content)
	if errors.Is(err, db.ErrChatNotEnabled) {
		chatJSON(w, http.StatusForbidden, map[string]string{"error": "chat not enabled for this project"})
		return
	}
	if err != nil {
		chatJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to send message"})
		return
	}

	chatJSON(w, http.StatusCreated, msg)
}

// serveChatPoll handles GET /chat/api/p/:slug/poll?since=<iso>
func (r *Relay) serveChatPoll(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.chatIdentity(w, req); !ok {
		return
	}

	slug := chatSlug(req.URL.Path, "/chat/api/p/")
	if slug == "" {
		chatJSON(w, http.StatusBadRequest, map[string]string{"error": "missing project slug"})
		return
	}

	since := req.URL.Query().Get("since")
	if since == "" {
		since = time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339)
	}

	msgs, err := r.DB.GetChatMessagesSince(slug, since)
	if err != nil {
		if strings.Contains(err.Error(), "project not found") {
			chatJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
			return
		}
		chatJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to poll messages"})
		return
	}
	if msgs == nil {
		msgs = []models.ChatMessage{}
	}
	chatJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

// serveChatHistory handles GET /chat/api/p/:slug/history?before=<iso>&limit=N
func (r *Relay) serveChatHistory(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.chatIdentity(w, req); !ok {
		return
	}

	slug := chatSlug(req.URL.Path, "/chat/api/p/")
	if slug == "" {
		chatJSON(w, http.StatusBadRequest, map[string]string{"error": "missing project slug"})
		return
	}

	before := req.URL.Query().Get("before")
	limit := 50
	if l := req.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	msgs, err := r.DB.GetChatHistory(slug, before, limit)
	if err != nil {
		if strings.Contains(err.Error(), "project not found") {
			chatJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
			return
		}
		chatJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get history"})
		return
	}
	if msgs == nil {
		msgs = []models.ChatMessage{}
	}
	chatJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

// ServeChat routes all /chat/ requests. These routes bypass bearer auth (they use EasyAuth).
// Returns 404 immediately when WRAITH_CHAT_ENABLED=0, consistent with the HTTP-mux-level gate.
func (r *Relay) ServeChat(w http.ResponseWriter, req *http.Request) {
	if !r.Config.ChatEnabled {
		http.NotFound(w, req)
		return
	}

	path := req.URL.Path

	switch {
	case path == "/chat/api/projects" && req.Method == http.MethodGet:
		r.serveChatProjects(w, req)

	case strings.HasPrefix(path, "/chat/api/p/") && strings.HasSuffix(path, "/send") && req.Method == http.MethodPost:
		r.serveChatSend(w, req)

	case strings.HasPrefix(path, "/chat/api/p/") && strings.HasSuffix(path, "/poll") && req.Method == http.MethodGet:
		r.serveChatPoll(w, req)

	case strings.HasPrefix(path, "/chat/api/p/") && strings.HasSuffix(path, "/history") && req.Method == http.MethodGet:
		r.serveChatHistory(w, req)

	default:
		r.serveChatStatic(w, req)
	}
}
