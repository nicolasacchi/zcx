package client

import (
	"encoding/json"
	"time"
)

// Pagination is the pagination envelope returned by every list endpoint and by
// the message pagination of a conversation detail.
type Pagination struct {
	Page        int  `json:"page"`
	PerPage     int  `json:"per_page"`
	TotalCount  int  `json:"total_count"`
	TotalPages  int  `json:"total_pages"`
	HasNextPage bool `json:"has_next_page"`
}

// Chat is one Zipchat chat (a store's assistant).
type Chat struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Domain    string     `json:"domain"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
}

// User is an assignable chat user (owner or participant). Email is PII.
type User struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Email     string  `json:"email"`
	AvatarURL *string `json:"avatar_url"`
}

// Lead is the customer contact attached to a conversation (all PII). The list
// endpoint returns a subset of the fields the detail endpoint returns.
type Lead struct {
	ID                    int64           `json:"id"`
	FirstName             string          `json:"first_name,omitempty"`
	LastName              string          `json:"last_name,omitempty"`
	FullName              string          `json:"full_name,omitempty"`
	Email                 string          `json:"email,omitempty"`
	PhoneNumber           string          `json:"phone_number,omitempty"`
	EmailMarketingConsent bool            `json:"email_marketing_consent"`
	PhoneMarketingConsent bool            `json:"phone_marketing_consent,omitempty"`
	ShopifyCustomerID     string          `json:"shopify_customer_id,omitempty"`
	AdditionalAttributes  json.RawMessage `json:"additional_attributes,omitempty"`
	CreatedAt             *time.Time      `json:"created_at,omitempty"`
}

// Message is one conversation message. Role is "user" or "assistant"; a human
// operator reply has ManualReply true (and usually SenderID set).
type Message struct {
	ID           int64     `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	Role         string    `json:"role"`
	Message      string    `json:"message"`
	CountAsReply bool      `json:"count_as_reply"`
	ManualReply  bool      `json:"manual_reply"`
	Status       string    `json:"status"`
	Rate         *string   `json:"rate"`
	SenderID     *int64    `json:"sender_id"`
}

// Conversation is a conversation. List responses omit Messages; detail
// responses (GetConversation) carry all of them.
type Conversation struct {
	ID                       int64       `json:"id"`
	ChatID                   int64       `json:"chat_id,omitempty"`
	CurrentURL               *string     `json:"current_url"`
	StartLink                *string     `json:"start_link"`
	ShareURL                 string      `json:"share_url"`
	CreatedAt                time.Time   `json:"created_at"`
	UpdatedAt                time.Time   `json:"updated_at"`
	Archived                 bool        `json:"archived"`
	LastMessageAt            *time.Time  `json:"last_message_at"`
	Channel                  string      `json:"channel"`
	ManualReplyMode          bool        `json:"manual_reply_mode"`
	LastEscalatedAt          *time.Time  `json:"last_escalated_at"`
	LastEscalationResolvedAt *time.Time  `json:"last_escalation_resolved_at"`
	Assignee                 *User       `json:"assignee"`
	Lead                     *Lead       `json:"lead"`
	Messages                 []Message   `json:"messages,omitempty"`
	MessagesPagination       *Pagination `json:"messages_pagination,omitempty"`
}

// Prompt is the chat's master prompt.
type Prompt struct {
	ChatID                int64  `json:"chat_id"`
	MasterPrompt          string `json:"master_prompt"`
	MasterPromptMaxLength int    `json:"master_prompt_max_length"`
}

// CustomTool is a per-chat action ("skill") the AI can invoke.
type CustomTool struct {
	ID                   int64      `json:"id"`
	Name                 string     `json:"name"`
	Instructions         string     `json:"instructions"`
	Active               bool       `json:"active"`
	Channels             []string   `json:"channels"`
	IconID               *int64     `json:"icon_id"`
	CustomToolTemplateID *int64     `json:"custom_tool_template_id"`
	VariableIDs          []int64    `json:"variable_ids"`
	CreatedAt            *time.Time `json:"created_at,omitempty"`
	UpdatedAt            *time.Time `json:"updated_at,omitempty"`
}

// Variable is a per-chat secret. The API never returns values, only HasValue.
type Variable struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	Kind          string     `json:"kind"`
	HasValue      bool       `json:"has_value"`
	UsageCount    int        `json:"usage_count"`
	CustomToolIDs []int64    `json:"custom_tool_ids"`
	CreatedAt     *time.Time `json:"created_at,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
}
