package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrationChatMessages(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chat_test.db")
	d, err := NewTestDB(dbPath)
	if err != nil {
		t.Fatalf("NewTestDB: %v", err)
	}
	defer func() { _ = d.Close() }()

	// Assert chat_messages table exists with correct columns.
	cols := tableColumns(t, d.conn, "chat_messages")
	for _, required := range []string{"id", "project", "sender_role", "sender_email", "recipient", "content", "created_at", "read_at"} {
		if !cols[required] {
			t.Errorf("chat_messages missing column %q", required)
		}
	}

	// Assert idx_chat_messages_project_created index exists.
	var count int
	err = d.conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_chat_messages_project_created'`).Scan(&count)
	if err != nil || count == 0 {
		t.Error("idx_chat_messages_project_created index not found")
	}

	// Assert projects table has chat_executive_role column.
	projectCols := tableColumns(t, d.conn, "projects")
	if !projectCols["chat_executive_role"] {
		t.Error("projects missing column chat_executive_role")
	}
}

func TestChatSetAndGetExecutiveRole(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chat_exec_test.db")
	d, err := NewTestDB(dbPath)
	if err != nil {
		t.Fatalf("NewTestDB: %v", err)
	}
	defer func() { _ = d.Close() }()

	d.EnsureProject("testproj")

	proj, err := d.SetChatExecutiveRole("testproj", "cto")
	if err != nil {
		t.Fatalf("SetChatExecutiveRole: %v", err)
	}
	if proj == nil || proj.ChatExecutiveRole == nil || *proj.ChatExecutiveRole != "cto" {
		t.Errorf("expected chat_executive_role=cto, got %v", proj)
	}

	// Idempotent: calling again with same value must not error.
	proj2, err := d.SetChatExecutiveRole("testproj", "cto")
	if err != nil {
		t.Fatalf("SetChatExecutiveRole idempotent: %v", err)
	}
	if proj2 == nil || proj2.ChatExecutiveRole == nil || *proj2.ChatExecutiveRole != "cto" {
		t.Errorf("idempotent: expected chat_executive_role=cto, got %v", proj2)
	}
}

func TestChatInsertAndPoll(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chat_insert_test.db")
	d, err := NewTestDB(dbPath)
	if err != nil {
		t.Fatalf("NewTestDB: %v", err)
	}
	defer func() { _ = d.Close() }()

	d.EnsureProject("myproject")
	_, err = d.SetChatExecutiveRole("myproject", "cto")
	if err != nil {
		t.Fatalf("SetChatExecutiveRole: %v", err)
	}

	msg, err := d.InsertChatMessage("myproject", "user@example.com", "hello!")
	if err != nil {
		t.Fatalf("InsertChatMessage: %v", err)
	}
	if msg.Recipient != "cto" {
		t.Errorf("expected recipient=cto, got %q", msg.Recipient)
	}
	if msg.SenderRole != "human" {
		t.Errorf("expected sender_role=human, got %q", msg.SenderRole)
	}

	// Poll: should return the message.
	msgs, err := d.GetChatMessagesSince("myproject", "2000-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("GetChatMessagesSince: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].ID != msg.ID {
		t.Errorf("expected msg ID %q, got %q", msg.ID, msgs[0].ID)
	}

	// Also verify the messages inbox row was created for the executive.
	var inboxCount int
	err = d.conn.QueryRow(`SELECT COUNT(*) FROM messages WHERE to_agent = 'cto' AND project = 'myproject'`).Scan(&inboxCount)
	if err != nil || inboxCount == 0 {
		t.Error("expected inbox message for executive, found none")
	}

	// And the matching deliveries row — without it, GetInboxViaDeliveries
	// (the production code path whenever HasDeliveries() is true) cannot
	// surface the notification to the runner's chat-gate.
	var deliveryCount int
	err = d.conn.QueryRow(
		`SELECT COUNT(*) FROM deliveries d JOIN messages m ON d.message_id = m.id
		 WHERE d.to_agent = 'cto' AND d.project = 'myproject' AND d.state = 'queued'`,
	).Scan(&deliveryCount)
	if err != nil || deliveryCount == 0 {
		t.Error("expected queued delivery row for executive, found none")
	}

	// End-to-end: GetInbox (delivery-based routing engaged) must return the
	// chat notification keyed on subject='chat:<project>'.
	inboxMsgs, err := d.GetInbox("myproject", "cto", false, 10)
	if err != nil {
		t.Fatalf("GetInbox: %v", err)
	}
	foundChat := false
	for _, m := range inboxMsgs {
		if m.Subject == "chat:myproject" && m.To == "cto" {
			foundChat = true
			break
		}
	}
	if !foundChat {
		t.Errorf("expected chat:myproject notification in cto inbox, got %d messages: %+v", len(inboxMsgs), inboxMsgs)
	}
}

func TestChatSendChatNotEnabled(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chat_disabled_test.db")
	d, err := NewTestDB(dbPath)
	if err != nil {
		t.Fatalf("NewTestDB: %v", err)
	}
	defer func() { _ = d.Close() }()

	d.EnsureProject("nonchat")

	_, err = d.InsertChatMessage("nonchat", "user@example.com", "hello")
	if err == nil {
		t.Error("expected error for project with no chat_executive_role, got nil")
	}
}

func TestChatHistoryPagination(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chat_history_test.db")
	d, err := NewTestDB(dbPath)
	if err != nil {
		t.Fatalf("NewTestDB: %v", err)
	}
	defer func() { _ = d.Close() }()

	d.EnsureProject("pagproj")
	_, _ = d.SetChatExecutiveRole("pagproj", "cto")

	// Insert 60 messages.
	for i := 0; i < 60; i++ {
		_, err := d.InsertChatMessage("pagproj", "user@example.com", "msg")
		if err != nil {
			t.Fatalf("InsertChatMessage[%d]: %v", i, err)
		}
	}

	// GetChatHistory with limit=50 should return exactly 50 rows.
	msgs, err := d.GetChatHistory("pagproj", "", 50)
	if err != nil {
		t.Fatalf("GetChatHistory: %v", err)
	}
	if len(msgs) != 50 {
		t.Errorf("expected 50 messages, got %d", len(msgs))
	}

	// Verify descending order (most recent first).
	for i := 1; i < len(msgs); i++ {
		if msgs[i-1].CreatedAt < msgs[i].CreatedAt {
			t.Errorf("messages not in descending order at index %d", i)
		}
	}
}

func TestListChatProjects(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chat_list_test.db")
	d, err := NewTestDB(dbPath)
	if err != nil {
		t.Fatalf("NewTestDB: %v", err)
	}
	defer func() { _ = d.Close() }()

	d.EnsureProject("alpha")
	d.EnsureProject("beta")
	d.EnsureProject("gamma") // no chat

	_, _ = d.SetChatExecutiveRole("alpha", "cto")
	_, _ = d.SetChatExecutiveRole("beta", "director")

	projects, err := d.ListChatProjects()
	if err != nil {
		t.Fatalf("ListChatProjects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2 chat projects, got %d", len(projects))
	}
}

// tableColumns returns a map of column names for the given table.
func tableColumns(t *testing.T, conn *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := conn.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	cols := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dflt sql.NullString
		var pk int
		_ = rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
		cols[name] = true
	}
	return cols
}
