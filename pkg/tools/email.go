package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dapicom-ai/omnipus/pkg/email"
)

// Email tools (M11) model a single configured mailbox as a TOOL surface rather
// than a conversational channel. They are registered ONLY for the agent that
// owns a configured mailbox (gated in pkg/agent/loop.go on a resolvable
// password ref) and flow through the normal per-agent tool policy, so god-mode /
// O7 policy applies exactly as for any other tool.
//
// All five tools depend only on the email.Transport interface, so they are
// fully unit-testable against an in-memory fake without a real IMAP/SMTP server.

const defaultEmailListLimit = 20

// --- read_inbox ---

// ReadInboxTool lists recent inbox messages (envelope only).
type ReadInboxTool struct {
	BaseTool
	tp email.Transport
}

// NewReadInboxTool constructs the read_inbox tool over the given transport.
func NewReadInboxTool(tp email.Transport) *ReadInboxTool { return &ReadInboxTool{tp: tp} }

func (t *ReadInboxTool) Name() string           { return "read_inbox" }
func (t *ReadInboxTool) Scope() ToolScope       { return ScopeGeneral }
func (t *ReadInboxTool) Category() ToolCategory { return CategoryCommunication }
func (t *ReadInboxTool) Description() string {
	return "Read your mailbox inbox. Returns recent messages (newest first) with uid, from, subject, and date — but no body. Use read_message with a uid to read the full body. Set unseen_only=true to see only unread mail."
}

func (t *ReadInboxTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"limit": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     100,
				"description": "Maximum number of messages to return (default 20).",
			},
			"unseen_only": map[string]any{
				"type":        "boolean",
				"description": "When true, return only unread messages.",
			},
		},
	}
}

func (t *ReadInboxTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.tp == nil {
		return ErrorResult("read_inbox: no mailbox is configured for this agent")
	}
	limit := defaultEmailListLimit
	if v, ok := args["limit"].(float64); ok && v >= 1 {
		limit = int(v)
	}
	unseenOnly, _ := args["unseen_only"].(bool)

	msgs, err := t.tp.ReadInbox(ctx, limit, unseenOnly)
	if err != nil {
		return ErrorResult(fmt.Sprintf("read_inbox failed: %v", err))
	}
	return marshalMessages(msgs, "read_inbox")
}

// --- search_email ---

// SearchEmailTool searches the mailbox by free-text query.
type SearchEmailTool struct {
	BaseTool
	tp email.Transport
}

// NewSearchEmailTool constructs the search_email tool over the given transport.
func NewSearchEmailTool(tp email.Transport) *SearchEmailTool { return &SearchEmailTool{tp: tp} }

func (t *SearchEmailTool) Name() string           { return "search_email" }
func (t *SearchEmailTool) Scope() ToolScope       { return ScopeGeneral }
func (t *SearchEmailTool) Category() ToolCategory { return CategoryCommunication }
func (t *SearchEmailTool) Description() string {
	return "Search your mailbox for messages matching a free-text query (matched against subject, sender, and body). Returns envelope-only results (uid, from, subject, date), newest first."
}

func (t *SearchEmailTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Free-text search query.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     100,
				"description": "Maximum number of results (default 20).",
			},
		},
		"required": []string{"query"},
	}
}

func (t *SearchEmailTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.tp == nil {
		return ErrorResult("search_email: no mailbox is configured for this agent")
	}
	query, _ := args["query"].(string)
	if strings.TrimSpace(query) == "" {
		return ErrorResult("search_email: query is required")
	}
	limit := defaultEmailListLimit
	if v, ok := args["limit"].(float64); ok && v >= 1 {
		limit = int(v)
	}
	msgs, err := t.tp.Search(ctx, query, limit)
	if err != nil {
		return ErrorResult(fmt.Sprintf("search_email failed: %v", err))
	}
	return marshalMessages(msgs, "search_email")
}

// --- read_message ---

// ReadMessageTool fetches a single message (with body) by UID.
type ReadMessageTool struct {
	BaseTool
	tp email.Transport
}

// NewReadMessageTool constructs the read_message tool over the given transport.
func NewReadMessageTool(tp email.Transport) *ReadMessageTool { return &ReadMessageTool{tp: tp} }

func (t *ReadMessageTool) Name() string           { return "read_message" }
func (t *ReadMessageTool) Scope() ToolScope       { return ScopeGeneral }
func (t *ReadMessageTool) Category() ToolCategory { return CategoryCommunication }
func (t *ReadMessageTool) Description() string {
	return "Read the full body of a single email by its uid (from read_inbox or search_email). Returns the sender, subject, date, message_id, and body."
}

func (t *ReadMessageTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"uid": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "The IMAP uid of the message to read.",
			},
		},
		"required": []string{"uid"},
	}
}

func (t *ReadMessageTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.tp == nil {
		return ErrorResult("read_message: no mailbox is configured for this agent")
	}
	uid, ok := parseUID(args["uid"])
	if !ok {
		return ErrorResult("read_message: uid is required and must be a positive integer")
	}
	msg, err := t.tp.ReadMessage(ctx, uid)
	if err != nil {
		return ErrorResult(fmt.Sprintf("read_message failed: %v", err))
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return ErrorResult(fmt.Sprintf("read_message: marshal: %v", err))
	}
	return NewToolResult(string(data))
}

// --- send_email ---

// SendEmailTool sends a new outbound message.
type SendEmailTool struct {
	BaseTool
	tp email.Transport
}

// NewSendEmailTool constructs the send_email tool over the given transport.
func NewSendEmailTool(tp email.Transport) *SendEmailTool { return &SendEmailTool{tp: tp} }

func (t *SendEmailTool) Name() string           { return "send_email" }
func (t *SendEmailTool) Scope() ToolScope       { return ScopeGeneral }
func (t *SendEmailTool) Category() ToolCategory { return CategoryCommunication }
func (t *SendEmailTool) Description() string {
	return "Send a new email from your mailbox to a recipient. Provide to, subject, and body."
}

func (t *SendEmailTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"to":      map[string]any{"type": "string", "description": "Recipient email address."},
			"subject": map[string]any{"type": "string", "description": "Subject line."},
			"body":    map[string]any{"type": "string", "description": "Plain-text message body."},
		},
		"required": []string{"to", "subject", "body"},
	}
}

func (t *SendEmailTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.tp == nil {
		return ErrorResult("send_email: no mailbox is configured for this agent")
	}
	to, _ := args["to"].(string)
	subject, _ := args["subject"].(string)
	body, _ := args["body"].(string)
	if strings.TrimSpace(to) == "" {
		return ErrorResult("send_email: to is required")
	}
	if strings.TrimSpace(body) == "" {
		return ErrorResult("send_email: body is required")
	}
	if err := t.tp.Send(ctx, email.SendRequest{To: to, Subject: subject, Body: body}); err != nil {
		return ErrorResult(fmt.Sprintf("send_email failed: %v", err))
	}
	return NewToolResult(fmt.Sprintf(`{"sent":true,"to":%q}`, to))
}

// --- reply ---

// ReplyTool replies to an existing message by UID, threading via In-Reply-To.
type ReplyTool struct {
	BaseTool
	tp email.Transport
}

// NewReplyTool constructs the reply tool over the given transport.
func NewReplyTool(tp email.Transport) *ReplyTool { return &ReplyTool{tp: tp} }

func (t *ReplyTool) Name() string           { return "reply" }
func (t *ReplyTool) Scope() ToolScope       { return ScopeGeneral }
func (t *ReplyTool) Category() ToolCategory { return CategoryCommunication }
func (t *ReplyTool) Description() string {
	return "Reply to an email by its uid (from read_inbox or search_email). The reply goes to the original sender, threads correctly (In-Reply-To), and uses a 'Re:' subject. Provide the uid and the reply body."
}

func (t *ReplyTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"uid": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "The uid of the message to reply to.",
			},
			"body": map[string]any{"type": "string", "description": "Plain-text reply body."},
		},
		"required": []string{"uid", "body"},
	}
}

func (t *ReplyTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.tp == nil {
		return ErrorResult("reply: no mailbox is configured for this agent")
	}
	uid, ok := parseUID(args["uid"])
	if !ok {
		return ErrorResult("reply: uid is required and must be a positive integer")
	}
	body, _ := args["body"].(string)
	if strings.TrimSpace(body) == "" {
		return ErrorResult("reply: body is required")
	}

	orig, err := t.tp.ReadMessage(ctx, uid)
	if err != nil {
		return ErrorResult(fmt.Sprintf("reply: could not load original message: %v", err))
	}
	if orig.From == "" {
		return ErrorResult("reply: original message has no sender to reply to")
	}
	subject := orig.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	req := email.SendRequest{
		To:        orig.From,
		Subject:   subject,
		Body:      body,
		InReplyTo: orig.MessageID,
	}
	if err := t.tp.Send(ctx, req); err != nil {
		return ErrorResult(fmt.Sprintf("reply failed: %v", err))
	}
	return NewToolResult(fmt.Sprintf(`{"replied":true,"to":%q,"uid":%d}`, orig.From, uid))
}

// --- helpers ---

// marshalMessages serializes an envelope-only message slice to a tool result.
func marshalMessages(msgs []email.Message, toolName string) *ToolResult {
	if msgs == nil {
		msgs = []email.Message{}
	}
	data, err := json.Marshal(msgs)
	if err != nil {
		return ErrorResult(fmt.Sprintf("%s: marshal: %v", toolName, err))
	}
	return NewToolResult(string(data))
}

// parseUID coerces a JSON arg (float64 from encoding/json, or int) to a uint32
// UID. Returns ok=false for missing, non-numeric, or non-positive values.
func parseUID(raw any) (uint32, bool) {
	switch v := raw.(type) {
	case float64:
		if v >= 1 {
			return uint32(v), true
		}
	case int:
		if v >= 1 {
			return uint32(v), true
		}
	}
	return 0, false
}

// EmailToolset constructs the full set of email tools over a transport. Returned
// in a stable order. Used by the per-agent wiring in pkg/agent.
func EmailToolset(tp email.Transport) []Tool {
	return []Tool{
		NewReadInboxTool(tp),
		NewSearchEmailTool(tp),
		NewReadMessageTool(tp),
		NewSendEmailTool(tp),
		NewReplyTool(tp),
	}
}
