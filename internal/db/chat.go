package db

import (
	"database/sql"
	"fmt"
	"time"

	"agent-relay/internal/models"

	"github.com/google/uuid"
)

// InsertChatMessage inserts a human→executive message and a copy into the executive's messages inbox.
// Both writes happen in a single transaction. The recipient is read from projects.chat_executive_role
// and stamped on the row at INSERT time (anti-hijack: client cannot influence the recipient field).
// Returns 403-flavour error when chat_executive_role IS NULL for the project.
func (d *DB) InsertChatMessage(project, senderEmail, content string) (*models.ChatMessage, error) {
	tx, err := d.conn.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Read recipient from projects table (server-side anti-hijack).
	var recipient sql.NullString
	err = tx.QueryRow("SELECT chat_executive_role FROM projects WHERE name = ?", project).Scan(&recipient)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("project not found: %s", project)
	}
	if err != nil {
		return nil, fmt.Errorf("query project: %w", err)
	}
	if !recipient.Valid || recipient.String == "" {
		return nil, ErrChatNotEnabled
	}

	id := uuid.New().String()
	// memoryTimeFmt (microsecond precision) so same-second batch sends stay paginable.
	now := time.Now().UTC().Format(memoryTimeFmt)

	msg := &models.ChatMessage{
		ID:          id,
		Project:     project,
		SenderRole:  "human",
		SenderEmail: &senderEmail,
		Recipient:   recipient.String,
		Content:     content,
		CreatedAt:   now,
	}

	_, err = tx.Exec(
		`INSERT INTO chat_messages (id, project, sender_role, sender_email, recipient, content, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.Project, msg.SenderRole, msg.SenderEmail, msg.Recipient, msg.Content, msg.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert chat_message: %w", err)
	}

	// Drop a copy into the executive's messages inbox so the runner's chat-gate picks it up.
	inboxID := uuid.New().String()
	_, err = tx.Exec(
		`INSERT INTO messages (id, from_agent, to_agent, type, subject, content, metadata, created_at, project, priority)
		 VALUES (?, ?, ?, 'notification', ?, ?, '{}', ?, ?, 'P2')`,
		inboxID, senderEmail, recipient.String,
		fmt.Sprintf("chat:%s", project),
		content, now, project,
	)
	if err != nil {
		return nil, fmt.Errorf("insert inbox message: %w", err)
	}

	// Stamp the matching deliveries row so GetInboxViaDeliveries surfaces the
	// notification — GetInbox switches to delivery-based routing whenever
	// HasDeliveries() is true, which is always the case in production.
	// Without this row the inbox INSERT above is invisible to the runner's
	// chat-gate (CodeFire #101, smoke 2026-05-28).
	deliveryID := uuid.New().String()
	_, err = tx.Exec(
		`INSERT INTO deliveries (id, message_id, to_agent, state, sequence_number, created_at, project)
		 VALUES (?, ?, ?, 'queued', 0, ?, ?)`,
		deliveryID, inboxID, recipient.String, now, project,
	)
	if err != nil {
		return nil, fmt.Errorf("insert delivery: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return msg, nil
}

// GetChatMessagesSince returns chat_messages for a project created after the given ISO timestamp.
// It also drains any executive-role replies sitting in the messages inbox into chat_messages
// (single transaction).
func (d *DB) GetChatMessagesSince(project, since string) ([]models.ChatMessage, error) {
	tx, err := d.conn.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Determine project's executive role.
	var recipient sql.NullString
	err = tx.QueryRow("SELECT chat_executive_role FROM projects WHERE name = ?", project).Scan(&recipient)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("project not found: %s", project)
	}
	if err != nil {
		return nil, fmt.Errorf("query project: %w", err)
	}

	// Drain executive-role replies from the messages inbox into chat_messages.
	if recipient.Valid && recipient.String != "" {
		drainRows, qErr := tx.Query(
			`SELECT id, content, created_at FROM messages
			 WHERE project = ? AND from_agent = ? AND read_at IS NULL
			 ORDER BY created_at ASC`,
			project, recipient.String,
		)
		if qErr == nil {
			type inboxRow struct{ id, content, createdAt string }
			var toInsert []inboxRow
			for drainRows.Next() {
				var r inboxRow
				if sErr := drainRows.Scan(&r.id, &r.content, &r.createdAt); sErr == nil {
					toInsert = append(toInsert, r)
				}
			}
			_ = drainRows.Close()

			now := time.Now().UTC().Format(time.RFC3339)
			for _, r := range toInsert {
				chatID := uuid.New().String()
				_, _ = tx.Exec(
					`INSERT OR IGNORE INTO chat_messages (id, project, sender_role, recipient, content, created_at)
					 VALUES (?, ?, ?, 'human', ?, ?)`,
					chatID, project, recipient.String, r.content, r.createdAt,
				)
				_, _ = tx.Exec("UPDATE messages SET read_at = ? WHERE id = ?", now, r.id)
			}
		}
	}

	rows, err := tx.Query(
		`SELECT id, project, sender_role, sender_email, recipient, content, created_at, read_at
		 FROM chat_messages
		 WHERE project = ? AND created_at > ?
		 ORDER BY created_at ASC`,
		project, since,
	)
	if err != nil {
		return nil, fmt.Errorf("query chat_messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	msgs, err := scanChatMessages(rows)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return msgs, nil
}

// GetChatHistory returns paginated chat_messages for a project before the given ISO timestamp.
// Default page size is 50; max is 200.
func (d *DB) GetChatHistory(project, before string, limit int) ([]models.ChatMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if before == "" {
		before = time.Now().UTC().Format(memoryTimeFmt)
	}

	rows, err := d.ro().Query(
		`SELECT id, project, sender_role, sender_email, recipient, content, created_at, read_at
		 FROM chat_messages
		 WHERE project = ? AND created_at < ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		project, before, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query chat_history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanChatMessages(rows)
}

// ListChatProjects returns projects where chat_executive_role IS NOT NULL.
func (d *DB) ListChatProjects() ([]models.ChatProject, error) {
	rows, err := d.ro().Query(
		`SELECT name, chat_executive_role FROM projects
		 WHERE chat_executive_role IS NOT NULL AND chat_executive_role != ''
		 ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("query chat projects: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var projects []models.ChatProject
	for rows.Next() {
		var p models.ChatProject
		if err := rows.Scan(&p.Slug, &p.ChatExecutiveRole); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// SetChatExecutiveRole sets projects.chat_executive_role for the given project (idempotent).
// Passing an empty role sets the column to NULL, effectively disabling chat for the project.
func (d *DB) SetChatExecutiveRole(project, role string) (*models.Project, error) {
	var err error
	if role == "" {
		_, err = d.conn.Exec("UPDATE projects SET chat_executive_role = NULL WHERE name = ?", project)
	} else {
		_, err = d.conn.Exec("UPDATE projects SET chat_executive_role = ? WHERE name = ?", role, project)
	}
	if err != nil {
		return nil, fmt.Errorf("update chat_executive_role: %w", err)
	}
	return d.GetProject(project)
}

// ErrChatNotEnabled is returned when a project has no chat_executive_role configured.
var ErrChatNotEnabled = fmt.Errorf("chat not enabled for this project (chat_executive_role is NULL)")

func scanChatMessages(rows *sql.Rows) ([]models.ChatMessage, error) {
	var msgs []models.ChatMessage
	for rows.Next() {
		var m models.ChatMessage
		if err := rows.Scan(&m.ID, &m.Project, &m.SenderRole, &m.SenderEmail, &m.Recipient, &m.Content, &m.CreatedAt, &m.ReadAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}
