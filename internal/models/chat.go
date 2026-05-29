package models

type ChatMessage struct {
	ID          string  `json:"id"`
	Project     string  `json:"project"`
	SenderRole  string  `json:"sender_role"`
	SenderEmail *string `json:"sender_email,omitempty"`
	Recipient   string  `json:"recipient"`
	Content     string  `json:"content"`
	CreatedAt   string  `json:"created_at"`
	ReadAt      *string `json:"read_at,omitempty"`
}

type ChatProject struct {
	Slug              string `json:"slug"`
	ChatExecutiveRole string `json:"executive_role"`
	// LatestTS is the ISO timestamp of the most recent message (drained) in the
	// conversation, used to sort the sidebar by recency. Empty when no messages.
	LatestTS string `json:"latest_ts,omitempty"`
	// LastPreview is a truncated snippet of the most recent message; LastKind is
	// "human" or "cto". Best-effort — undrained executive replies lag until polled.
	LastPreview string `json:"last_preview,omitempty"`
	LastKind    string `json:"last_kind,omitempty"`
	// Unread counts CTO→human messages the human hasn't read yet, combining
	// drained chat_messages (read_at IS NULL) and undrained executive replies
	// still sitting in the inbox. Cleared by MarkChatRead when the chat is opened.
	Unread int `json:"unread"`
}
