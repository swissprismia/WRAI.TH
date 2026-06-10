package relay

import (
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"agent-relay/internal/config"
	"agent-relay/internal/db"
	"agent-relay/internal/web"

	"github.com/mark3labs/mcp-go/server"
)

//go:embed testdata/chat
var testChatFSEmbed embed.FS

// testChatRelay builds a minimal Relay with chat enabled/disabled for HTTP handler tests.
func testChatRelay(t *testing.T, chatEnabled, devMode bool) *Relay {
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
		return
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

func TestEasyAuthParserURLSafeBase64(t *testing.T) {
	principal := map[string]any{
		"claims": []map[string]string{
			{"typ": "preferred_username", "val": "bob@example.com"},
		},
	}
	b, _ := json.Marshal(principal)
	header := base64.RawURLEncoding.EncodeToString(b)

	p := parseClientPrincipal(header)
	if p == nil {
		t.Fatal("expected non-nil principal for URL-safe base64 header")
		return
	}
	if p.Email != "bob@example.com" {
		t.Errorf("expected email=bob@example.com, got %q", p.Email)
	}
	if p.Source != "easyauth" {
		t.Errorf("expected source=easyauth, got %q", p.Source)
	}
}

func TestEasyAuthParserNameClaim(t *testing.T) {
	principal := map[string]any{
		"claims": []map[string]string{
			{"typ": "preferred_username", "val": "carol@example.com"},
			{"typ": "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name", "val": "Carol Smith"},
		},
	}
	b, _ := json.Marshal(principal)
	header := base64.StdEncoding.EncodeToString(b)

	p := parseClientPrincipal(header)
	if p == nil {
		t.Fatal("expected non-nil principal")
		return
	}
	if p.Email != "carol@example.com" {
		t.Errorf("expected email=carol@example.com, got %q", p.Email)
	}
	if p.Name != "Carol Smith" {
		t.Errorf("expected name=Carol Smith, got %q", p.Name)
	}
}

func TestEasyAuthParserDevFallback(t *testing.T) {
	r := testChatRelay(t, true, true)

	req := httptest.NewRequest(http.MethodGet, "/chat/api/projects", nil)
	// No X-MS-CLIENT-PRINCIPAL header — dev mode should fall back to dev@local.
	w := httptest.NewRecorder()
	r.ServeChat(w, req)

	if w.Code == http.StatusUnauthorized {
		t.Errorf("expected dev-mode fallback, got 401")
	}
}

func TestEasyAuthParserDevFallbackOff(t *testing.T) {
	r := testChatRelay(t, true, false)

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
	r := testChatRelay(t, true, false)

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
	entry, ok := resp["entry"].(map[string]any)
	if !ok {
		t.Fatalf("expected top-level 'entry' key, got: %v", resp)
	}
	if entry["kind"] != "human" {
		t.Errorf("expected entry.kind=human, got %v", entry["kind"])
	}
	if entry["content"] != "hello from the browser" {
		t.Errorf("expected entry.content to match sent content, got %v", entry["content"])
	}
}

func TestChatSendAuthMissing(t *testing.T) {
	r := testChatRelay(t, true, false)

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
	r := testChatRelay(t, true, false)

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
	r := testChatRelay(t, true, false)
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
	r := testChatRelay(t, true, false)

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
	r := testChatRelay(t, true, false)

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

func TestChatUnreadCountAndMarkRead(t *testing.T) {
	r := testChatRelay(t, true, false)

	r.DB.EnsureProject("unreadproj")
	if _, err := r.DB.SetChatExecutiveRole("unreadproj", "cto"); err != nil {
		t.Fatalf("SetChatExecutiveRole: %v", err)
	}

	findUnread := func() int {
		t.Helper()
		projects, err := r.DB.ListChatProjects()
		if err != nil {
			t.Fatalf("ListChatProjects: %v", err)
		}
		for _, p := range projects {
			if p.Slug == "unreadproj" {
				return p.Unread
			}
		}
		t.Fatalf("unreadproj not found in ListChatProjects")
		return -1
	}

	// A human message is outbound (recipient='cto') — never counted as unread.
	if _, err := r.DB.InsertChatMessage("unreadproj", "user@example.com", "hi cto"); err != nil {
		t.Fatalf("InsertChatMessage: %v", err)
	}
	if got := findUnread(); got != 0 {
		t.Fatalf("after human message, expected unread 0, got %d", got)
	}

	// An undrained executive reply sits in the inbox — counted via the inbox branch.
	if _, err := r.DB.InsertMessage("unreadproj", "cto", "cto", "notification",
		"chat:unreadproj", "reply one", "{}", "P2", 0, nil, nil); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if got := findUnread(); got != 1 {
		t.Fatalf("after undrained reply, expected unread 1, got %d", got)
	}

	// Draining (what a poll does) moves it into chat_messages — still unread,
	// now via the chat_messages branch, so it must not double-count.
	if _, err := r.DB.GetChatMessagesSince("unreadproj", "2000-01-01T00:00:00Z"); err != nil {
		t.Fatalf("GetChatMessagesSince: %v", err)
	}
	if got := findUnread(); got != 1 {
		t.Fatalf("after drain, expected unread 1 (no double count), got %d", got)
	}

	// Opening the chat marks it read.
	n, err := r.DB.MarkChatRead("unreadproj")
	if err != nil {
		t.Fatalf("MarkChatRead: %v", err)
	}
	if n != 1 {
		t.Errorf("expected MarkChatRead to mark 1 row, got %d", n)
	}
	if got := findUnread(); got != 0 {
		t.Fatalf("after mark read, expected unread 0, got %d", got)
	}

	// Idempotent — nothing left to mark.
	if n2, err := r.DB.MarkChatRead("unreadproj"); err != nil || n2 != 0 {
		t.Errorf("second MarkChatRead: n=%d err=%v (want 0, nil)", n2, err)
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

// --- SPA static handler tests ---

// chatSubFS returns the test fixture FS rooted at testdata/chat.
func chatSubFS(t *testing.T) fs.FS {
	t.Helper()
	sub, err := fs.Sub(testChatFSEmbed, "testdata/chat")
	if err != nil {
		t.Fatalf("fs.Sub testdata/chat: %v", err)
	}
	return sub
}

func TestChatStaticServesIndex(t *testing.T) {
	r := testChatRelay(t, true, false)
	r.ChatStaticFS = chatSubFS(t)

	req := httptest.NewRequest(http.MethodGet, "/chat/", nil)
	w := httptest.NewRecorder()
	r.ServeChat(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "<!doctype html>") {
		t.Errorf("expected <!doctype html> in body, got: %s", w.Body.String())
	}
}

func TestChatStaticServesAssets(t *testing.T) {
	r := testChatRelay(t, true, false)
	r.ChatStaticFS = chatSubFS(t)

	req := httptest.NewRequest(http.MethodGet, "/chat/assets/stub.js", nil)
	w := httptest.NewRecorder()
	r.ServeChat(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestChatStaticDisabledWhenFlagOff(t *testing.T) {
	r := testChatRelay(t, false, false)
	r.ChatStaticFS = chatSubFS(t)

	req := httptest.NewRequest(http.MethodGet, "/chat/", nil)
	w := httptest.NewRecorder()
	r.ServeChat(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 when chat disabled, got %d", w.Code)
	}
}

func TestChatStaticSPAFallback(t *testing.T) {
	r := testChatRelay(t, true, false)
	r.ChatStaticFS = chatSubFS(t)

	req := httptest.NewRequest(http.MethodGet, "/chat/p/some-slug", nil)
	w := httptest.NewRecorder()
	r.ServeChat(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for SPA fallback, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "<!doctype html>") {
		t.Errorf("expected index.html body for SPA route, got: %s", w.Body.String())
	}
}

func TestChatStaticEmbedHasIndex(t *testing.T) {
	// Sanity check: verifies that static/chat/index.html exists in the production
	// embedded FS. t.Skip is allowed when make ui hasn't been run locally;
	// CI always runs make ui before go test so this must pass in CI.
	_, err := fs.Stat(web.StaticFiles, "static/chat/index.html")
	if err != nil {
		t.Skipf("static/chat/index.html not found in embedded FS — run 'make ui' first: %v", err)
	}
}

// --- Wire-format integration tests ---

// assertChatEntry validates that v is a map with the expected ChatEntry shape.
func assertChatEntry(t *testing.T, label string, v any, wantContent string) {
	t.Helper()
	entry, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s: expected map, got %T: %v", label, v, v)
	}
	if entry["id"] == "" || entry["id"] == nil {
		t.Errorf("%s: entry.id must be non-empty", label)
	}
	if entry["ts"] == "" || entry["ts"] == nil {
		t.Errorf("%s: entry.ts must be non-empty", label)
	}
	kind, _ := entry["kind"].(string)
	if kind != "human" && kind != "cto" {
		t.Errorf("%s: entry.kind must be 'human' or 'cto', got %q", label, kind)
	}
	if entry["from"] == "" || entry["from"] == nil {
		t.Errorf("%s: entry.from must be non-empty", label)
	}
	if entry["content"] != wantContent {
		t.Errorf("%s: entry.content=%q, want %q", label, entry["content"], wantContent)
	}
	// Reject raw ChatMessage keys that must not appear in the wire shape.
	if _, bad := entry["sender_role"]; bad {
		t.Errorf("%s: raw key 'sender_role' must not appear in ChatEntry response", label)
	}
	if _, bad := entry["created_at"]; bad {
		t.Errorf("%s: raw key 'created_at' must not appear in ChatEntry response", label)
	}
}

// TestChatWireFormat is the end-to-end wire-shape integration test:
//
//	POST /chat/api/p/:slug/send → assert {entry: ChatEntry}
//	GET  /chat/api/p/:slug/poll → assert {messages: [ChatEntry, …]}
func TestChatWireFormat(t *testing.T) {
	r := testChatRelay(t, true, false)

	r.DB.EnsureProject("wireproj")
	_, err := r.DB.SetChatExecutiveRole("wireproj", "cto")
	if err != nil {
		t.Fatalf("SetChatExecutiveRole: %v", err)
	}

	const msgContent = "wire-format smoke test"

	// --- Send ---
	body := `{"content":"` + msgContent + `"}`
	sendReq := httptest.NewRequest(http.MethodPost, "/chat/api/p/wireproj/send", bytes.NewReader([]byte(body)))
	sendReq.Header.Set("Content-Type", "application/json")
	sendReq.Header.Set("X-MS-CLIENT-PRINCIPAL", easyAuthHeader("tester@example.com"))

	sendW := httptest.NewRecorder()
	r.ServeChat(sendW, sendReq)

	if sendW.Code != http.StatusCreated {
		t.Fatalf("send: expected 201, got %d: %s", sendW.Code, sendW.Body.String())
	}

	var sendResp map[string]any
	if err := json.NewDecoder(sendW.Body).Decode(&sendResp); err != nil {
		t.Fatalf("send: decode: %v", err)
	}
	if _, ok := sendResp["entry"]; !ok {
		t.Fatalf("send: response missing top-level 'entry' key; got keys: %v", sendResp)
	}
	assertChatEntry(t, "send.entry", sendResp["entry"], msgContent)

	// --- Poll ---
	pollReq := httptest.NewRequest(http.MethodGet, "/chat/api/p/wireproj/poll?since=2000-01-01T00:00:00Z", nil)
	pollReq.Header.Set("X-MS-CLIENT-PRINCIPAL", easyAuthHeader("tester@example.com"))

	pollW := httptest.NewRecorder()
	r.ServeChat(pollW, pollReq)

	if pollW.Code != http.StatusOK {
		t.Fatalf("poll: expected 200, got %d: %s", pollW.Code, pollW.Body.String())
	}

	var pollResp map[string]any
	if err := json.NewDecoder(pollW.Body).Decode(&pollResp); err != nil {
		t.Fatalf("poll: decode: %v", err)
	}
	msgs, ok := pollResp["messages"].([]any)
	if !ok || len(msgs) == 0 {
		t.Fatalf("poll: expected non-empty 'messages' array, got: %v", pollResp["messages"])
	}
	for i, m := range msgs {
		assertChatEntry(t, strings.Join([]string{"poll.messages[", strconv.Itoa(i), "]"}, ""), m, msgContent)
	}
}
