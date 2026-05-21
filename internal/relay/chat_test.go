package relay

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"agent-relay/internal/config"
	"agent-relay/internal/db"

	"github.com/mark3labs/mcp-go/server"
)

// testRelay builds a minimal Relay with chat enabled/disabled for HTTP handler tests.
func testRelay(t *testing.T, chatEnabled, devMode bool) *Relay {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "relay_chat_test.db")
	database, err := db.NewTestDB(dbPath)
	if err != nil {
		t.Fatalf("NewTestDB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	mcpSrv := server.NewMCPServer("test", "0.0.0")
	registry := NewSessionRegistry(mcpSrv)
	events := NewEventBus()
	handlers := NewHandlers(database, registry, nil, nil, events)

	return &Relay{
		DB:       database,
		Handlers: handlers,
		Config: config.Config{
			ChatEnabled: chatEnabled,
			DevMode:     devMode,
		},
	}
}

// easyAuthHeader encodes a minimal EasyAuth principal as a base64 JSON string.
func easyAuthHeader(email string) string {
	principal := map[string]any{
		"claims": []map[string]string{
			{"typ": "preferred_username", "val": email},
		},
	}
	b, _ := json.Marshal(principal)
	return base64.StdEncoding.EncodeToString(b)
}

// withProjectAndAgent sets project and agent in context (mirrors HTTPContextFunc).
func withProjectAndAgent(ctx context.Context, project, agent string) context.Context {
	ctx = context.WithValue(ctx, agentNameKey, agent)
	return context.WithValue(ctx, projectKey, project)
}

// --- EasyAuth parser tests ---

func TestEasyAuthParserHappyPath(t *testing.T) {
	header := easyAuthHeader("alice@example.com")
	p := parseClientPrincipal(header)
	if p == nil {
		t.Fatal("expected non-nil principal")
	}
	if p.Email != "alice@example.com" {
		t.Errorf("expected email=alice@example.com, got %q", p.Email)
	}
	if p.Source != "easyauth" {
		t.Errorf("expected source=easyauth, got %q", p.Source)
	}
}

func TestEasyAuthParserMissingHeader(t *testing.T) {
	p := parseClientPrincipal("")
	if p != nil {
		t.Errorf("expected nil for empty header, got %+v", p)
	}
}

func TestEasyAuthParserBadBase64(t *testing.T) {
	p := parseClientPrincipal("not-valid-base64!!!")
	if p != nil {
		t.Errorf("expected nil for invalid base64, got %+v", p)
	}
}

func TestEasyAuthParserDevFallback(t *testing.T) {
	r := testRelay(t, true, true)

	req := httptest.NewRequest(http.MethodGet, "/chat/api/projects", nil)
	// No X-MS-CLIENT-PRINCIPAL header — dev mode should fall back to dev@local.
	w := httptest.NewRecorder()
	r.ServeChat(w, req)

	if w.Code == http.StatusUnauthorized {
		t.Errorf("expected dev-mode fallback, got 401")
	}
}

func TestEasyAuthParserDevFallbackOff(t *testing.T) {
	r := testRelay(t, true, false)

	req := httptest.NewRequest(http.MethodGet, "/chat/api/projects", nil)
	// No header, dev mode off → 401.
	w := httptest.NewRecorder()
	r.ServeChat(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// --- HTTP route tests ---

func TestChatSendHappyPath(t *testing.T) {
	r := testRelay(t, true, false)

	r.DB.EnsureProject("myproj")
	_, err := r.DB.SetChatExecutiveRole("myproj", "cto")
	if err != nil {
		t.Fatalf("SetChatExecutiveRole: %v", err)
	}

	body := `{"content":"hello from the browser"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/api/p/myproj/send", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-MS-CLIENT-PRINCIPAL", easyAuthHeader("user@example.com"))

	w := httptest.NewRecorder()
	r.ServeChat(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["recipient"] != "cto" {
		t.Errorf("expected recipient=cto, got %v", resp["recipient"])
	}
}

func TestChatSendAuthMissing(t *testing.T) {
	r := testRelay(t, true, false)

	r.DB.EnsureProject("authtest")
	_, _ = r.DB.SetChatExecutiveRole("authtest", "cto")

	body := `{"content":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/api/p/authtest/send", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	// No EasyAuth header, dev mode off.

	w := httptest.NewRecorder()
	r.ServeChat(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestChatSendWrongProject(t *testing.T) {
	r := testRelay(t, true, false)

	body := `{"content":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/api/p/nonexistent/send", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-MS-CLIENT-PRINCIPAL", easyAuthHeader("user@example.com"))

	w := httptest.NewRecorder()
	r.ServeChat(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestChatSendChatDisabledForProject(t *testing.T) {
	r := testRelay(t, true, false)
	r.DB.EnsureProject("nochat")

	body := `{"content":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/api/p/nochat/send", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-MS-CLIENT-PRINCIPAL", easyAuthHeader("user@example.com"))

	w := httptest.NewRecorder()
	r.ServeChat(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestChatPollDrainsReplies(t *testing.T) {
	r := testRelay(t, true, false)

	r.DB.EnsureProject("pollproj")
	_, _ = r.DB.SetChatExecutiveRole("pollproj", "cto")

	// Seed an executive-role reply in the messages inbox.
	_, err := r.DB.InsertMessage("pollproj", "cto", "cto", "notification",
		"chat:pollproj", "reply from executive", "{}", "P2", 0, nil, nil)
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/chat/api/p/pollproj/poll?since=2000-01-01T00:00:00Z", nil)
	req.Header.Set("X-MS-CLIENT-PRINCIPAL", easyAuthHeader("user@example.com"))

	w := httptest.NewRecorder()
	r.ServeChat(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	msgs, ok := resp["messages"].([]any)
	if !ok || len(msgs) == 0 {
		t.Errorf("expected at least 1 drained message in poll response, got %v", resp["messages"])
	}
}

func TestChatHistoryPaginationRoute(t *testing.T) {
	r := testRelay(t, true, false)

	r.DB.EnsureProject("histproj")
	_, _ = r.DB.SetChatExecutiveRole("histproj", "cto")

	for i := 0; i < 60; i++ {
		_, _ = r.DB.InsertChatMessage("histproj", "user@example.com", "msg")
	}

	req := httptest.NewRequest(http.MethodGet, "/chat/api/p/histproj/history?limit=50", nil)
	req.Header.Set("X-MS-CLIENT-PRINCIPAL", easyAuthHeader("user@example.com"))

	w := httptest.NewRecorder()
	r.ServeChat(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	msgs, ok := resp["messages"].([]any)
	if !ok || len(msgs) != 50 {
		t.Errorf("expected 50 messages, got %d", len(msgs))
	}
}

// --- MCP tool tests ---

func TestSetChatExecutiveIdempotent(t *testing.T) {
	h := testHandlers(t)

	ctx := withProjectAndAgent(context.Background(), "execproj", "admin-user")

	// Register an executive agent.
	_, err := h.HandleRegisterAgent(ctx, call(map[string]any{
		"name":         "admin-user",
		"is_executive": true,
		"project":      "execproj",
	}))
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}

	callArgs := map[string]any{
		"project": "execproj",
		"role":    "cto",
		"as":      "admin-user",
	}

	// First call.
	res1, err := h.HandleSetChatExecutive(ctx, call(callArgs))
	if err != nil {
		t.Fatalf("first set_chat_executive: %v", err)
	}
	if res1.IsError {
		t.Fatalf("first call returned error: %v", res1.Content)
	}

	// Second call with same args (idempotent).
	res2, err := h.HandleSetChatExecutive(ctx, call(callArgs))
	if err != nil {
		t.Fatalf("second set_chat_executive: %v", err)
	}
	if res2.IsError {
		t.Fatalf("second call returned error: %v", res2.Content)
	}

	// Verify only one row in projects for execproj with chat configured.
	projects, _ := h.db.ListChatProjects()
	found := 0
	for _, p := range projects {
		if p.Slug == "execproj" {
			found++
		}
	}
	if found != 1 {
		t.Errorf("expected 1 chat project row for execproj, got %d", found)
	}
}

func TestSetChatExecutiveAdminOnly(t *testing.T) {
	h := testHandlers(t)

	ctx := withProjectAndAgent(context.Background(), "execproj2", "regular-user")

	// Register a non-executive agent.
	_, _ = h.HandleRegisterAgent(ctx, call(map[string]any{
		"name":         "regular-user",
		"is_executive": false,
		"project":      "execproj2",
	}))

	res, err := h.HandleSetChatExecutive(ctx, call(map[string]any{
		"project": "execproj2",
		"role":    "cto",
		"as":      "regular-user",
	}))
	if err != nil {
		t.Fatalf("set_chat_executive: %v", err)
	}
	if !res.IsError {
		t.Error("expected error for non-executive caller, got success")
	}
}
