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
	ChatExecutiveRole string `json:"chat_executive_role"`
}
