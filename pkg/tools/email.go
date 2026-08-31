package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/email"
)

// Email tools (M11) model mailbox accounts as a TOOL surface rather than a
// conversational channel. A mailbox belongs to one (agent, workspace) pair —
// the same agent can hold a different mailbox in each workspace it belongs to
// (different roles, different inboxes). The tools are registered ONLY for an
// agent that owns at least one configured mailbox (gated in pkg/agent on a
// resolvable password ref) and flow through the normal per-agent tool policy,
// so god-mode / O7 policy applies exactly as for any other tool.
//
// WORKSPACE RESOLUTION IS STRUCTURAL, NEVER MODEL-SUPPLIED: each tool holds
// the agent's full workspace→transport map and picks the transport for the
// CURRENT turn via tools.ToolWorkspaceID(ctx) — the same context stamp the
// memory/task/todo tools use. The model cannot address another workspace's
// (or another agent's) mailbox: there is no such parameter.
//
// All five tools depend only on the email.Transport interface, so they are
// fully unit-testable against an in-memory fake without a real IMAP/SMTP server.

const defaultEmailListLimit = 20

// EmailTransports maps workspace ID → live transport for ONE agent's
// mailboxes. Built once at tool-registration time (pkg/agent) from the
// agent's enabled, password-resolvable mailbox configs.
type EmailTransports map[string]email.Transport

// resolve picks the transport for the current turn's workspace:
//   - turn bound to a workspace with a mailbox → that mailbox;
//   - turn bound to a workspace WITHOUT one → error naming the workspaces
//     that do have one;
//   - no workspace bound to the turn and the agent has exactly one mailbox →
//     that one (graceful single-mailbox fallback);
//   - no workspace bound and several mailboxes → error (ambiguous — the tool
//     will not guess between inboxes with different roles).
func (m EmailTransports) resolve(ctx context.Context, toolName string) (email.Transport, error) {
	if len(m) == 0 {
		return nil, fmt.Errorf("%s: no mailbox is configured for this agent", toolName)
	}
	wsID := ToolWorkspaceID(ctx)
	if wsID != "" {
		if tp, ok := m[wsID]; ok && tp != nil {
			return tp, nil
		}
		return nil, fmt.Errorf(
			"%s: this agent has no mailbox in the current workspace (%s); it has mailboxes in: %s",
			toolName, wsID, strings.Join(m.workspaceIDs(), ", "))
	}
	if len(m) == 1 {
		for _, tp := range m {
			return tp, nil
		}
	}
	return nil, fmt.Errorf(
		"%s: this turn is not bound to a workspace and the agent has mailboxes in several (%s) — run this from a workspace conversation",
		toolName,
		strings.Join(m.workspaceIDs(), ", "),
	)
}

// workspaceIDs returns the sorted workspace keys (deterministic error text).
func (m EmailTransports) workspaceIDs() []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// --- read_inbox ---

// ReadInboxTool lists recent inbox messages (envelope only).
type ReadInboxTool struct {
	BaseTool
	tps EmailTransports
}

// NewReadInboxTool constructs the read_inbox tool over the agent's
// workspace→transport map.
func NewReadInboxTool(tps EmailTransports) *ReadInboxTool { return &ReadInboxTool{tps: tps} }

func (t *ReadInboxTool) Name() string           { return "read_inbox" }
func (t *ReadInboxTool) Scope() ToolScope       { return ScopeGeneral }
func (t *ReadInboxTool) Category() ToolCategory { return CategoryCommunication }
func (t *ReadInboxTool) Description() string {
	return "Read your mailbox INBOX folder. Returns recent messages (newest first) with uid, from, subject, and date — but no body. Use read_message with a uid to read the full body. Set unseen_only=true to see only unread mail; combine it with before_uid to page through only-unread mail. IMPORTANT: this always reads the INBOX folder specifically — there is no parameter to select a different folder (Sent, Archive, Spam, etc.), so mail filed or moved elsewhere is never returned here."
}

func (t *ReadInboxTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"limit": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     100,
				"description": "Maximum number of messages to return (default 20, hard-capped at 100).",
			},
			"unseen_only": map[string]any{
				"type":        "boolean",
				"description": "When true, return only unread messages.",
			},
			"before_uid": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "Pagination cursor: return only messages with a uid strictly less than this. Pass the smallest uid from the previous page to fetch the next (older) page. before_uid=1 always returns an empty list (nothing has a uid below 1) — that is how a pagination loop terminates.",
			},
		},
	}
}

func (t *ReadInboxTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	tp, err := t.tps.resolve(ctx, "read_inbox")
	if err != nil {
		return ErrorResult(err.Error())
	}
	limit := defaultEmailListLimit
	if v, ok := args["limit"].(float64); ok && v >= 1 {
		limit = int(v)
	}
	unseenOnly, _ := args["unseen_only"].(bool)
	beforeUID, err := parseBeforeUID(args)
	if err != nil {
		return ErrorResult(err.Error())
	}

	msgs, err := tp.ReadInbox(ctx, email.InboxOptions{
		Limit:      limit,
		UnseenOnly: unseenOnly,
		BeforeUID:  beforeUID,
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("read_inbox failed: %v", err))
	}
	return marshalMessages(msgs, "read_inbox")
}

// --- search_email ---

// SearchEmailTool searches the mailbox by free-text query.
type SearchEmailTool struct {
	BaseTool
	tps EmailTransports
}

// NewSearchEmailTool constructs the search_email tool over the agent's
// workspace→transport map.
func NewSearchEmailTool(tps EmailTransports) *SearchEmailTool { return &SearchEmailTool{tps: tps} }

func (t *SearchEmailTool) Name() string           { return "search_email" }
func (t *SearchEmailTool) Scope() ToolScope       { return ScopeGeneral }
func (t *SearchEmailTool) Category() ToolCategory { return CategoryCommunication }
func (t *SearchEmailTool) Description() string {
	return "Search your mailbox for messages matching a free-text query. By default matches sender and subject (fast); set body=true to also scan message bodies (slower on large mailboxes). Returns an object with an envelope-only results array (newest first), the total number of matches, a truncated flag when there were more matches than the limit, and a next_before_uid pagination cursor."
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
				"description": "Maximum number of results (default 20, hard-capped at 100).",
			},
			"before_uid": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "Pagination cursor: return only matches with a uid strictly less than this. Pass next_before_uid from the previous page to fetch the next (older) page. before_uid=1 always returns an empty results array (nothing has a uid below 1) — that is how a pagination loop terminates; check truncated/next_before_uid on each page rather than looping past that.",
			},
			"body": map[string]any{
				"type":        "boolean",
				"description": "When true, also scan message bodies (not just sender/subject). Slower on large mailboxes; leave false unless a header match is insufficient.",
			},
		},
		"required": []string{"query"},
	}
}

func (t *SearchEmailTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	tp, err := t.tps.resolve(ctx, "search_email")
	if err != nil {
		return ErrorResult(err.Error())
	}
	query, _ := args["query"].(string)
	if strings.TrimSpace(query) == "" {
		return ErrorResult("search_email: query is required")
	}
	limit := defaultEmailListLimit
	if v, ok := args["limit"].(float64); ok && v >= 1 {
		limit = int(v)
	}
	beforeUID, err := parseBeforeUID(args)
	if err != nil {
		return ErrorResult(err.Error())
	}
	bodySearch, _ := args["body"].(bool)

	result, err := tp.Search(ctx, query, email.SearchOptions{
		Limit:     limit,
		BeforeUID: beforeUID,
		Body:      bodySearch,
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("search_email failed: %v", err))
	}
	if result.Messages == nil {
		result.Messages = []email.Message{}
	}
	data, err := json.Marshal(result)
	if err != nil {
		return ErrorResult(fmt.Sprintf("search_email: marshal: %v", err))
	}
	return NewToolResult(string(data))
}

// --- read_message ---

// ReadMessageTool fetches a single message (with body) by UID.
type ReadMessageTool struct {
	BaseTool
	tps EmailTransports
}

// NewReadMessageTool constructs the read_message tool over the agent's
// workspace→transport map.
func NewReadMessageTool(tps EmailTransports) *ReadMessageTool { return &ReadMessageTool{tps: tps} }

func (t *ReadMessageTool) Name() string           { return "read_message" }
func (t *ReadMessageTool) Scope() ToolScope       { return ScopeGeneral }
func (t *ReadMessageTool) Category() ToolCategory { return CategoryCommunication }
func (t *ReadMessageTool) Description() string {
	return "Read the full body of a single email by its uid (from read_inbox or search_email). Returns the sender, subject, date, message_id, and body. IMPORTANT: reading a message here marks it \\Seen in the mailbox, the same as a person opening it in their inbox — it will no longer appear in read_inbox(unseen_only=true) or in the unread scan afterward. This is intentional, not a side effect to avoid."
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
	tp, err := t.tps.resolve(ctx, "read_message")
	if err != nil {
		return ErrorResult(err.Error())
	}
	uid, ok := parseUID(args["uid"])
	if !ok {
		return ErrorResult("read_message: uid is required and must be a positive integer")
	}
	msg, err := tp.ReadMessage(ctx, uid)
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
	tps EmailTransports
}

// NewSendEmailTool constructs the send_email tool over the agent's
// workspace→transport map.
func NewSendEmailTool(tps EmailTransports) *SendEmailTool { return &SendEmailTool{tps: tps} }

func (t *SendEmailTool) Name() string           { return "send_email" }
func (t *SendEmailTool) Scope() ToolScope       { return ScopeGeneral }
func (t *SendEmailTool) Category() ToolCategory { return CategoryCommunication }
func (t *SendEmailTool) Description() string {
	return "Send a new email from your mailbox to a recipient. Provide to, subject, and body. IMPORTANT: to accepts exactly ONE recipient address — there is no CC/BCC and no way to address multiple recipients in one call. If subject is left empty, the sent message uses the literal subject \"(no subject)\" rather than failing."
}

func (t *SendEmailTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"to":      map[string]any{"type": "string", "description": "A single recipient email address. Only one address is supported — this tool has no CC/BCC and cannot address multiple recipients."},
			"subject": map[string]any{"type": "string", "description": "Subject line. If omitted or empty, the message is sent with the subject \"(no subject)\"."},
			"body":    map[string]any{"type": "string", "description": "Plain-text message body."},
		},
		"required": []string{"to", "subject", "body"},
	}
}

func (t *SendEmailTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	tp, err := t.tps.resolve(ctx, "send_email")
	if err != nil {
		return ErrorResult(err.Error())
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
	if err := tp.Send(ctx, email.SendRequest{To: to, Subject: subject, Body: body}); err != nil {
		return ErrorResult(fmt.Sprintf("send_email failed: %v", err))
	}
	return NewToolResult(fmt.Sprintf(`{"sent":true,"to":%q}`, to))
}

// --- reply ---

// ReplyTool replies to an existing message by UID, threading via In-Reply-To.
type ReplyTool struct {
	BaseTool
	tps EmailTransports
}

// NewReplyTool constructs the reply tool over the agent's
// workspace→transport map.
func NewReplyTool(tps EmailTransports) *ReplyTool { return &ReplyTool{tps: tps} }

func (t *ReplyTool) Name() string           { return "reply" }
func (t *ReplyTool) Scope() ToolScope       { return ScopeGeneral }
func (t *ReplyTool) Category() ToolCategory { return CategoryCommunication }
func (t *ReplyTool) Description() string {
	return "Reply to an email by its uid (from read_inbox or search_email). The reply goes to the address the sender wants replies sent to (the message's Reply-To header, when the sender set one; otherwise the original From address), threads correctly (In-Reply-To), and uses a 'Re:' subject. Provide the uid and the reply body. IMPORTANT: this does NOT quote the original message in the reply body — only your body text is sent. It also only replies to the sender (or their Reply-To address): any other To/Cc recipients on the original message are NOT included."
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
	tp, err := t.tps.resolve(ctx, "reply")
	if err != nil {
		return ErrorResult(err.Error())
	}
	uid, ok := parseUID(args["uid"])
	if !ok {
		return ErrorResult("reply: uid is required and must be a positive integer")
	}
	body, _ := args["body"].(string)
	if strings.TrimSpace(body) == "" {
		return ErrorResult("reply: body is required")
	}

	orig, err := tp.ReadMessage(ctx, uid)
	if err != nil {
		return ErrorResult(fmt.Sprintf("reply: could not load original message: %v", err))
	}
	// Transport is an INTERFACE: the "non-nil message or non-nil error"
	// contract is a convention, not something the type system enforces. The
	// bundled *email.Client honours it on every path, but a second
	// implementation returning (nil, nil) would nil-panic on orig.From below —
	// and the package's own test double did exactly that until it was
	// corrected, which is how close this is to being reachable. Guard at the
	// boundary rather than trusting every present and future implementer.
	if orig == nil {
		return ErrorResult(fmt.Sprintf("reply: transport returned no message and no error for uid %d", uid))
	}
	if orig.From == "" && orig.ReplyTo == "" {
		return ErrorResult("reply: original message has no sender to reply to")
	}
	// Prefer the sender's explicit Reply-To over From: for list mail, ticketing
	// systems, and no-reply@ senders, Reply-To names the address the sender
	// actually wants replies sent to.
	to := orig.From
	if orig.ReplyTo != "" {
		to = orig.ReplyTo
	}
	subject := orig.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	req := email.SendRequest{
		To:        to,
		Subject:   subject,
		Body:      body,
		InReplyTo: orig.MessageID,
	}
	if err := tp.Send(ctx, req); err != nil {
		return ErrorResult(fmt.Sprintf("reply failed: %v", err))
	}
	return NewToolResult(fmt.Sprintf(`{"replied":true,"to":%q,"uid":%d}`, to, uid))
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

// parseBeforeUID resolves the optional before_uid pagination cursor from the
// tool arguments. An ABSENT (or null) before_uid is valid and means "no cursor"
// → (0, nil). A PRESENT before_uid that is not a positive integer is an ERROR,
// never silently coerced to 0: a malformed cursor swallowed as 0 would rerun the
// query as page 1, re-processing messages the caller already saw (and, for a
// pagination loop, never terminating).
func parseBeforeUID(args map[string]any) (uint32, error) {
	raw, present := args["before_uid"]
	if !present || raw == nil {
		return 0, nil
	}
	uid, ok := parseUID(raw)
	if !ok {
		return 0, fmt.Errorf("before_uid must be a positive integer")
	}
	return uid, nil
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

// EmailToolset constructs the full set of email tools over an agent's
// workspace→transport map. Returned in a stable order. Used by the per-agent
// wiring in pkg/agent.
func EmailToolset(tps EmailTransports) []Tool {
	return []Tool{
		NewReadInboxTool(tps),
		NewSearchEmailTool(tps),
		NewReadMessageTool(tps),
		NewSendEmailTool(tps),
		NewReplyTool(tps),
	}
}
