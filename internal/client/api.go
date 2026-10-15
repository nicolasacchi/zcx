package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ErrStop can be returned by an iteration callback to stop paging cleanly.
var ErrStop = errors.New("stop iteration")

// pageOf decodes {"<key>": [...], "pagination": {...}}.
func pageOf[T any](raw []byte, key string) ([]T, Pagination, error) {
	var env map[string]json.RawMessage
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, Pagination{}, err
	}
	var items []T
	if v, ok := env[key]; ok {
		if err := json.Unmarshal(v, &items); err != nil {
			return nil, Pagination{}, fmt.Errorf("decode %q: %w", key, err)
		}
	}
	var p Pagination
	if v, ok := env["pagination"]; ok {
		_ = json.Unmarshal(v, &p)
	}
	return items, p, nil
}

// listAll walks every page of a list endpoint.
func listAll[T any](ctx context.Context, c *Client, path, key string, base url.Values) ([]T, error) {
	var all []T
	for page := 1; ; page++ {
		q := url.Values{}
		for k, v := range base {
			q[k] = v
		}
		q.Set("page", strconv.Itoa(page))
		q.Set("per_page", strconv.Itoa(MaxPerPage))
		raw, err := c.do(ctx, http.MethodGet, path, q, nil)
		if err != nil {
			return nil, err
		}
		items, p, err := pageOf[T](raw, key)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		all = append(all, items...)
		if !p.HasNextPage || len(items) == 0 || page >= 10000 {
			return all, nil
		}
	}
}

func chatPath(chatID, rest string) string { return "chats/" + url.PathEscape(chatID) + "/" + rest }

// ListChats returns every chat reachable with the credential.
func (c *Client) ListChats(ctx context.Context) ([]Chat, error) {
	return listAll[Chat](ctx, c, "chats", "chats", nil)
}

// ListUsers returns the assignable users of a chat.
func (c *Client) ListUsers(ctx context.Context, chatID string) ([]User, error) {
	return listAll[User](ctx, c, chatPath(chatID, "users"), "users", nil)
}

// GetUser returns one assignable user.
func (c *Client) GetUser(ctx context.Context, chatID string, id int64) (*User, error) {
	var out struct {
		User User `json:"user"`
	}
	if err := c.getJSON(ctx, chatPath(chatID, "users/"+strconv.FormatInt(id, 10)), nil, &out); err != nil {
		return nil, err
	}
	return &out.User, nil
}

// ListConversationsOptions filters ListConversations. Zero values are omitted.
type ListConversationsOptions struct {
	Page               int // default 1
	PerPage            int // default 50, max 100
	LastMessageAtSince time.Time
	UpdatedAtSince     time.Time
}

func (o ListConversationsOptions) values() url.Values {
	q := url.Values{}
	if o.Page > 0 {
		q.Set("page", strconv.Itoa(o.Page))
	}
	if o.PerPage > 0 {
		q.Set("per_page", strconv.Itoa(o.PerPage))
	}
	if !o.LastMessageAtSince.IsZero() {
		q.Set("last_message_at_since", formatTime(o.LastMessageAtSince))
	}
	if !o.UpdatedAtSince.IsZero() {
		q.Set("updated_at_since", formatTime(o.UpdatedAtSince))
	}
	return q
}

// ConversationPage is one page of the conversation list (no messages).
type ConversationPage struct {
	Conversations []Conversation `json:"conversations"`
	Pagination    Pagination     `json:"pagination"`
}

// ListConversations fetches ONE page (ordered by last_message_at desc).
func (c *Client) ListConversations(ctx context.Context, chatID string, o ListConversationsOptions) (*ConversationPage, error) {
	raw, err := c.do(ctx, http.MethodGet, chatPath(chatID, "conversations"), o.values(), nil)
	if err != nil {
		return nil, err
	}
	items, p, err := pageOf[Conversation](raw, "conversations")
	if err != nil {
		return nil, fmt.Errorf("decode conversations: %w", err)
	}
	return &ConversationPage{Conversations: items, Pagination: p}, nil
}

// EachConversation pages through the list (starting at o.Page, page size
// o.PerPage or 100) calling fn per conversation. fn returning ErrStop ends the
// walk with a nil error; any other error aborts.
func (c *Client) EachConversation(ctx context.Context, chatID string, o ListConversationsOptions, fn func(Conversation) error) error {
	if o.PerPage <= 0 {
		o.PerPage = MaxPerPage
	}
	if o.Page <= 0 {
		o.Page = 1
	}
	for {
		pg, err := c.ListConversations(ctx, chatID, o)
		if err != nil {
			return err
		}
		for _, cv := range pg.Conversations {
			if err := fn(cv); err != nil {
				if errors.Is(err, ErrStop) {
					return nil
				}
				return err
			}
		}
		if !pg.Pagination.HasNextPage || len(pg.Conversations) == 0 {
			return nil
		}
		o.Page++
	}
}

// ListAllConversations collects conversations across pages; limit <= 0 means
// all.
func (c *Client) ListAllConversations(ctx context.Context, chatID string, o ListConversationsOptions, limit int) ([]Conversation, error) {
	var out []Conversation
	err := c.EachConversation(ctx, chatID, o, func(cv Conversation) error {
		out = append(out, cv)
		if limit > 0 && len(out) >= limit {
			return ErrStop
		}
		return nil
	})
	return out, err
}

// GetConversationPage fetches ONE page of messages of a conversation.
func (c *Client) GetConversationPage(ctx context.Context, chatID string, id int64, page, perPage int) (*Conversation, error) {
	q := url.Values{}
	q.Set("message_page", strconv.Itoa(page))
	q.Set("message_per_page", strconv.Itoa(perPage))
	var out struct {
		Conversation Conversation `json:"conversation"`
	}
	if err := c.getJSON(ctx, chatPath(chatID, "conversations/"+strconv.FormatInt(id, 10)), q, &out); err != nil {
		return nil, err
	}
	return &out.Conversation, nil
}

// GetConversation fetches a conversation with ALL of its messages, following
// message pagination (200 per page) and de-duplicating by message id.
func (c *Client) GetConversation(ctx context.Context, chatID string, id int64) (*Conversation, error) {
	cv, err := c.GetConversationPage(ctx, chatID, id, 1, MaxMessagePerPage)
	if err != nil {
		return nil, err
	}
	seen := make(map[int64]bool, len(cv.Messages))
	for _, m := range cv.Messages {
		seen[m.ID] = true
	}
	for page := 2; cv.MessagesPagination != nil && cv.MessagesPagination.HasNextPage && page < 10000; page++ {
		next, err := c.GetConversationPage(ctx, chatID, id, page, MaxMessagePerPage)
		if err != nil {
			return nil, err
		}
		for _, m := range next.Messages {
			if !seen[m.ID] {
				seen[m.ID] = true
				cv.Messages = append(cv.Messages, m)
			}
		}
		cv.MessagesPagination = next.MessagesPagination
		if next.MessagesPagination == nil || len(next.Messages) == 0 {
			break
		}
	}
	if cv.MessagesPagination != nil {
		cv.MessagesPagination.HasNextPage = false
	}
	return cv, nil
}

// GetPrompt returns the master prompt and its maximum length.
func (c *Client) GetPrompt(ctx context.Context, chatID string) (*Prompt, error) {
	var out struct {
		Prompt Prompt `json:"prompt"`
	}
	if err := c.getJSON(ctx, chatPath(chatID, "prompt"), nil, &out); err != nil {
		return nil, err
	}
	return &out.Prompt, nil
}

// UpdatePrompt REPLACES the whole master prompt (PATCH). Last write wins: there
// is no ETag. It is never retried after a network error. Callers must have
// passed the safety gate (see the prompt feature); this method does not.
func (c *Client) UpdatePrompt(ctx context.Context, chatID, masterPrompt string) (*Prompt, error) {
	b, err := c.do(ctx, http.MethodPatch, chatPath(chatID, "prompt"), nil, map[string]string{"master_prompt": masterPrompt})
	if err != nil {
		return nil, err
	}
	var out struct {
		Prompt Prompt `json:"prompt"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("decode prompt response: %w", err)
	}
	return &out.Prompt, nil
}

// ListCustomTools returns every custom tool (skill) of the chat.
func (c *Client) ListCustomTools(ctx context.Context, chatID string) ([]CustomTool, error) {
	return listAll[CustomTool](ctx, c, chatPath(chatID, "custom_tools"), "custom_tools", nil)
}

// GetCustomTool returns one custom tool. The documented API has no show
// endpoint, so it tries GET custom_tools/:id and, if the server answers 404 or
// 405, falls back to scanning the list (returning a not-found APIError when
// absent).
func (c *Client) GetCustomTool(ctx context.Context, chatID string, id int64) (*CustomTool, error) {
	var out struct {
		CustomTool CustomTool `json:"custom_tool"`
	}
	err := c.getJSON(ctx, chatPath(chatID, "custom_tools/"+strconv.FormatInt(id, 10)), nil, &out)
	if err == nil {
		return &out.CustomTool, nil
	}
	var ae *APIError
	if !errors.As(err, &ae) || (ae.Status != http.StatusNotFound && ae.Status != http.StatusMethodNotAllowed) {
		return nil, err
	}
	tools, lerr := c.ListCustomTools(ctx, chatID)
	if lerr != nil {
		return nil, lerr
	}
	for i := range tools {
		if tools[i].ID == id {
			return &tools[i], nil
		}
	}
	return nil, &APIError{Status: 404, Kind: "not_found", Detail: "custom tool not found", Hint: HintFor("GET", 404)}
}

// ListVariables returns the chat's variables (values are never returned).
func (c *Client) ListVariables(ctx context.Context, chatID string) ([]Variable, error) {
	return listAll[Variable](ctx, c, chatPath(chatID, "variables"), "variables", nil)
}
