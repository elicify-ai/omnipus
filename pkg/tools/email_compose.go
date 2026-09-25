package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/mail"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/email"
)

// Outbound mail composition for the email tools (email-mail-view-spec §2.4):
// recipient lists, workspace-file attachments, the draft APPEND path, and the
// Sent-copy APPEND. The tools compose via email.Compose and hand the transport
// the transmitted bytes in SendRequest.Raw (the documented composed path);
// SendRequest.Body keeps the caller's text so the persistence shape is stable.
//
// All optional transport capabilities are structural interfaces (the
// production *Client implements them; the test fake extends them in a
// fixture-only test file):
const (
	// maxMailAttachmentFiles is MC-32: at most 10 files per outbound message.
	maxMailAttachmentFiles = 10
	// maxMailAttachmentBytes is MC-32: at most 25 MiB total across all files.
	maxMailAttachmentBytes = 25 << 20
	// maxMailRecipients is MC-27: at most 50 recipients after de-duplication
	// across To+Cc+Bcc, enforced before any dial or APPEND.
	maxMailRecipients = 50
)

// mailboxIdentity exposes the mailbox's sending identity. *Client implements
// it; a transport without it cannot be composed for (no From is derivable), so
// the send/draft paths degrade to the transport's own plain send.
type mailboxIdentity interface {
	AccountAddress() string
}

// messageAppender saves a fully composed RFC 5322 message to a folder
// (Sent copies, D7/D18/MC-9; drafts with \Draft, D8/FR-029).
type messageAppender interface {
	AppendMessage(ctx context.Context, folder string, flags []string, raw []byte) (uint32, uint32, error)
}

// mailboxFolders overrides the Sent/Drafts folder names (spec §2.1
// sent_folder_name/drafts_folder_name). *Client implements it from the
// mailbox config; the defaults are "Sent"/"Drafts".
type mailboxFolders interface {
	SentFolderName() string
	DraftsFolderName() string
}

// recipientArgEntries coerces a to/cc/bcc argument (a single string or a list
// of strings — the legacy single-string form stays valid) into entries.
func recipientArgEntries(toolName, field string, v any) ([]string, error) {
	switch val := v.(type) {
	case nil:
		return nil, nil
	case string:
		if strings.TrimSpace(val) == "" {
			return nil, nil
		}
		return []string{val}, nil
	case []any:
		entries := make([]string, 0, len(val))
		for _, item := range val {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s: %s entries must be strings", toolName, field)
			}
			entries = append(entries, s)
		}
		return entries, nil
	default:
		return nil, fmt.Errorf("%s: %s must be a string or a list of strings", toolName, field)
	}
}

// mailRecipients is the parsed, de-duplicated, cap-checked recipient set of
// one outbound call.
type mailRecipients struct {
	To, Cc, Bcc []mail.Address
	// Envelope is every unique bare address across To+Cc+Bcc in first-seen
	// order (MC-27 counts these; D29 sends to all of them).
	Envelope []string
}

// parseMailRecipients parses to/cc/bcc with the transport's own MC-4 rules
// (email.ParseRecipientList: CR/LF+comma split, display names, case-insensitive
// de-dup), applies the MC-27 50-recipient cap across the merged set, and
// rejects a missing to — all before any dial or APPEND.
func parseMailRecipients(toolName string, args map[string]any) (*mailRecipients, error) {
	toEntries, err := recipientArgEntries(toolName, "to", args["to"])
	if err != nil {
		return nil, err
	}
	if len(toEntries) == 0 {
		return nil, fmt.Errorf("%s: to is required (at least one recipient)", toolName)
	}
	ccEntries, err := recipientArgEntries(toolName, "cc", args["cc"])
	if err != nil {
		return nil, err
	}
	bccEntries, err := recipientArgEntries(toolName, "bcc", args["bcc"])
	if err != nil {
		return nil, err
	}

	toAddrs, bad := email.ParseRecipientList(toEntries)
	if len(bad) > 0 {
		return nil, fmt.Errorf("%s: recipient %q is not a valid email address", toolName, bad[0])
	}
	ccAddrs, badCc := email.ParseRecipientList(ccEntries)
	if len(badCc) > 0 {
		return nil, fmt.Errorf("%s: recipient %q is not a valid email address", toolName, badCc[0])
	}
	bccAddrs, badBcc := email.ParseRecipientList(bccEntries)
	if len(badBcc) > 0 {
		return nil, fmt.Errorf("%s: recipient %q is not a valid email address", toolName, badBcc[0])
	}

	env := envelopeRecipientsDedup(toAddrs, ccAddrs, bccAddrs)
	if len(env) > maxMailRecipients {
		return nil, fmt.Errorf("%s: more than %d recipients after de-duplication (got %d); the maximum is %d",
			toolName, maxMailRecipients, len(env), maxMailRecipients)
	}
	return &mailRecipients{
		To:       toAddrs,
		Cc:       ccAddrs,
		Bcc:      bccAddrs,
		Envelope: env,
	}, nil
}

// envelopeRecipientsDedup merges the address lists into unique bare addresses
// in first-seen order — the MC-27 count and the D29 envelope recipient set.
func envelopeRecipientsDedup(lists ...[]mail.Address) []string {
	seen := make(map[string]bool)
	var out []string
	for _, l := range lists {
		for _, a := range l {
			key := strings.ToLower(a.Address)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, a.Address)
		}
	}
	return out
}

// resolveMailAttachments resolves every attachment ref through the one
// authoritative per-turn filesystem policy (FR-036) with the FSOpSend
// disclosure op (D33/sign-off C2: allowed anywhere except the secret set —
// no outside-workspace gate), reads the bytes through the handle (the
// credentials/master.key carve-out is re-verified at I/O time inside
// PathHandle.ReadFile), and enforces the MC-32 caps pre-dial: at most 10
// files, at most 25 MiB total. Errors name the ORIGINAL ref the agent passed.
func resolveMailAttachments(ctx context.Context, toolName string, refs []any) ([]email.Attachment, error) {
	if len(refs) > maxMailAttachmentFiles {
		return nil, fmt.Errorf("%s: attachments: %d files given; the maximum is %d files per message",
			toolName, len(refs), maxMailAttachmentFiles)
	}
	if len(refs) == 0 {
		return nil, nil
	}
	policy, err := ResolveTurnFSPolicy(ctx, "", false)
	if err != nil {
		return nil, fmt.Errorf("%s: attachments: resolve filesystem policy: %w", toolName, err)
	}
	var (
		atts  []email.Attachment
		total int
	)
	for _, refAny := range refs {
		ref, ok := refAny.(string)
		if !ok || strings.TrimSpace(ref) == "" {
			return nil, fmt.Errorf("%s: attachments entries must be file paths (strings)", toolName)
		}
		handle, err := ResolvePath(ctx, policy, toolName, "", FSOpSend, ref)
		if err != nil {
			return nil, fmt.Errorf("%s: attachments: %s: %w", toolName, ref, err)
		}

		data, err := handle.ReadFile()
		if err != nil {
			_ = handle.Close()
			return nil, fmt.Errorf("%s: attachments: %s: %w", toolName, ref, err)
		}
		if err := handle.Close(); err != nil {
			return nil, fmt.Errorf("%s: attachments: %s: %w", toolName, ref, err)
		}
		if total+len(data) > maxMailAttachmentBytes {
			return nil, fmt.Errorf("%s: attachments: %s would take the message over the 25 MiB total attachment limit (MC-32)",
				toolName, ref)
		}
		atts = append(atts, email.Attachment{Name: filepath.Base(ref), ContentType: "", Data: data})
	}
	return atts, nil
}

// checkMailBodyBound enforces MC-22/FR-031: the Markdown body is bounded at
// 1 MiB, rejected before any dial or APPEND (Compose re-validates, but the
// tool layer owns the pre-flight).
func checkMailBodyBound(toolName, body string) error {
	if len(body) > 1<<20 {
		return fmt.Errorf("%s: the message body is %d bytes; the bound is 1 MiB (1048576 bytes) (MC-22)",
			toolName, len(body))
	}
	return nil
}

// mailComposeInput carries what send_email / reply / create_email_draft share
// before composition.
type mailComposeInput struct {
	toolName  string
	from      string
	subject   string
	markdown  string
	inReplyTo string
	recips    *mailRecipients
	attach    []email.Attachment
	draft     bool
}

// composeMail renders the outbound message via email.Compose (MC-2/3/4, D29,
// FR-029) and returns the full compose output: the transmitted bytes (no Bcc
// header), the Sent copy (Bcc kept), and the composed Message-ID. The caller
// has already enforced MC-22 (1 MiB MC-22) and parsed recipients (MC-27).
func composeMail(in mailComposeInput) (email.ComposeOutput, error) {
	out, err := email.Compose(email.ComposeInput{
		From:        in.from,
		To:          bareAddresses(in.recips.To),
		Cc:          bareAddresses(in.recips.Cc),
		Bcc:         bareAddresses(in.recips.Bcc),
		Subject:     in.subject,
		Markdown:    in.markdown,
		InReplyTo:   in.inReplyTo,
		Attachments: in.attach,
		Draft:       in.draft,
	})
	if err != nil {
		return email.ComposeOutput{}, fmt.Errorf("%s: %w", in.toolName, err)
	}
	return out, nil
}

// bareAddresses maps parsed addresses to their bare address strings for
// email.ComposeInput.
func bareAddresses(addrs []mail.Address) []string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.Address)
	}
	return out
}

// saveSentCopy APPENDs the Sent copy (Bcc header kept — D29) to the mailbox's
// Sent folder after a successful SMTP send (D7/D18/MC-9). A failure never
// fails the send: the caller reports sent_saved=false plus a save_warning in
// the tool result. A transport without the APPEND capability, or without a
// composable identity (no Sent copy was rendered), reports the warning shape
// rather than pretending a copy was saved.
func saveSentCopy(ctx context.Context, tp email.Transport, sentCopy []byte) (bool, string) {
	if len(sentCopy) == 0 {
		return false, "the message was sent, but no Sent copy could be rendered for saving (the mailbox did not expose its sending address)"
	}
	appender, ok := tp.(messageAppender)
	if !ok {
		return false, "the mailbox transport does not support saving sent copies"
	}
	folder := "Sent"
	if fp, ok := tp.(mailboxFolders); ok && fp.SentFolderName() != "" {
		folder = fp.SentFolderName()
	}
	if _, _, err := appender.AppendMessage(ctx, folder, nil, sentCopy); err != nil {
		return false, fmt.Sprintf("the message was sent, but saving the Sent copy to %s failed: %v", folder, err)
	}
	return true, ""
}

// draftChatLink builds the deep link to the draft in the Mail panel (FR-015):
// origin + hash route with workspace, mailbox and message. The origin is the
// gateway's canonical public origin injected at registration
// (SetChatLinkOrigin from pkg/agent via middleware.CanonicalGatewayOrigin).
// Returns ("", reason) when no origin is set or it is not a usable absolute
// http(s) URL; ("link", "") otherwise.
func draftChatLink(origin, wsID, agentID, messageID string) (string, string) {
	if strings.TrimSpace(origin) == "" {
		return "", "no public origin is configured for this deployment (gateway.public_url unset or wildcard bind), so no chat link can be built; the draft is in your Drafts folder"
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", "the configured public origin is not a usable absolute http(s) URL, so no chat link can be built; the draft is in your Drafts folder"
	}
	link := strings.TrimRight(origin, "/") + "/#/workspaces/" + url.PathEscape(wsID) +
		"/mail?mailbox=" + url.PathEscape(agentID) +
		"&folder=drafts&message=" + url.QueryEscape("mid:"+messageID)
	return link, ""
}

// --- create_email_draft ---

// CreateEmailDraftResult is the create_email_draft tool result (spec §2.2
// CreateEmailDraftResult — the SPA chat tool-result renderer consumes these
// property names; the gateway wire type is generated from the same contract).
// ChatLink is nil when no origin is derivable (FR-015), with ChatLinkReason
// stating why.
type CreateEmailDraftResult struct {
	Created        bool    `json:"created"`
	MessageID      string  `json:"message_id"`
	UID            uint32  `json:"uid"`
	UIDValidity    uint32  `json:"uidvalidity"`
	ChatLink       *string `json:"chat_link"`
	ChatLinkReason string  `json:"chat_link_reason"`
}

// CreateEmailDraftTool APPENDs a composed draft (X-Omnipus-Draft, text/
// markdown part) to the mailbox's Drafts folder with \Draft and never sends
// (D8/FR-029/FR-034). The Message-ID is minted by Compose and kept through
// panel edits and the final send (round-1 MIN-004).
type CreateEmailDraftTool struct {
	BaseTool
	tps    EmailTransports
	origin string
}

// NewCreateEmailDraftTool constructs the create_email_draft tool over the
// agent's workspace→transport map.
func NewCreateEmailDraftTool(tps EmailTransports) *CreateEmailDraftTool {
	return &CreateEmailDraftTool{tps: tps}
}

// SetChatLinkOrigin injects the gateway's canonical public origin (FR-015).
// Empty/unset keeps chat_link null with a stated reason.
func (t *CreateEmailDraftTool) SetChatLinkOrigin(origin string) { t.origin = origin }

func (t *CreateEmailDraftTool) Name() string           { return "create_email_draft" }
func (t *CreateEmailDraftTool) Scope() ToolScope       { return ScopeGeneral }
func (t *CreateEmailDraftTool) Category() ToolCategory { return CategoryCommunication }

func (t *CreateEmailDraftTool) Description() string {
	return "Create an email DRAFT in your mailbox's Drafts folder — it is never sent. The human approves, edits and sends it from the Mail panel; pass chat_link on to the user so they can open it. Provide to (at least one address), subject, and body (Markdown; rendered to HTML + plain text server-side). Optional: cc, bcc (lists of addresses), in_reply_to (the Message-ID this draft replies to, keeping the thread), and attachments (workspace file paths — files anywhere except protected credential files; at most 10 files, 25 MiB total)."
}

func (t *CreateEmailDraftTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"to": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"minItems":    1,
				"description": "Recipient addresses (at least one).",
			},
			"cc": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Carbon-copy addresses.",
			},

			"bcc": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Blind carbon-copy addresses. In the saved draft the Bcc header is kept so the panel can show it; on transmission the Bcc header is stripped and every bcc address still receives the mail.",
			},
			"subject": map[string]any{
				"type":        "string",
				"description": "Subject line (optional — leave empty for none).",
			},
			"body": map[string]any{
				"type":        "string",
				"description": "Message body in Markdown (rendered server-side to HTML and plain text).",
			},
			"in_reply_to": map[string]any{
				"type":        "string",
				"description": "Message-ID of the message this draft replies to (sets In-Reply-To and References).",
			},
			"attachments": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Workspace file paths to attach — resolved like send_file (any file anywhere except protected credential files), at most 10 files, 25 MiB total, enforced before the draft is saved.",
			},
		},
		"required": []string{"to", "body"},
	}
}

func (t *CreateEmailDraftTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	tp, err := t.tps.resolve(ctx, t.Name())
	if err != nil {
		return ErrorResult(err.Error())
	}
	recips, err := parseMailRecipients(t.Name(), args)
	if err != nil {
		return ErrorResult(err.Error())
	}
	body, _ := args["body"].(string)
	if strings.TrimSpace(body) == "" {
		return ErrorResult("create_email_draft: body is required")
	}
	if err := checkMailBodyBound(t.Name(), body); err != nil {
		return ErrorResult(err.Error())
	}
	subject, _ := args["subject"].(string)
	inReplyTo, _ := args["in_reply_to"].(string)
	refs, _ := args["attachments"].([]any)
	attach, err := resolveMailAttachments(ctx, t.Name(), refs)
	if err != nil {
		return ErrorResult(err.Error())
	}

	var from string
	if ident, ok := tp.(mailboxIdentity); ok {
		from = ident.AccountAddress()
	}
	if strings.TrimSpace(from) == "" {
		return ErrorResult("create_email_draft: cannot determine the mailbox's sending address (transport does not expose its identity)")
	}
	out, err := composeMail(mailComposeInput{
		toolName: t.Name(), from: from, subject: subject, markdown: body,
		inReplyTo: inReplyTo, recips: recips, attach: attach, draft: true,
	})
	if err != nil {
		return ErrorResult(err.Error())
	}
	messageID := out.MessageID
	composed := out.Transmitted

	appender, ok := tp.(messageAppender)
	if !ok {
		return ErrorResult("create_email_draft: the mailbox transport does not support saving drafts (no APPEND capability)")
	}
	folder := "Drafts"
	if fp, ok := tp.(mailboxFolders); ok && fp.DraftsFolderName() != "" {
		folder = fp.DraftsFolderName()
	}
	uid, uidvalidity, err := appender.AppendMessage(ctx, folder, []string{"\\Draft"}, composed)
	if err != nil {
		return ErrorResult(fmt.Sprintf("create_email_draft: saving the draft to %s failed: %v", folder, err))
	}

	link, reason := draftChatLink(t.origin, ToolWorkspaceID(ctx), ToolAgentID(ctx), messageID)
	res := CreateEmailDraftResult{
		Created:        true,
		MessageID:      messageID,
		UID:            uid,
		UIDValidity:    uidvalidity,
		ChatLinkReason: reason,
	}
	if link != "" {
		res.ChatLink = &link
	}
	data, err := json.Marshal(res)
	if err != nil {
		return ErrorResult(fmt.Sprintf("create_email_draft: marshal: %v", err))
	}
	return NewToolResult(string(data))
}

// parseReplyRecipients builds the recipient set for reply (MC-15/D26): the
// primary (Reply-To-preferring) address is To; cc/bcc arguments add Cc/Bcc;
// reply_all adds the original message's To and Cc recipients — minus the
// mailbox's own address and the primary, which stays the only To. The merged
// envelope honours MC-27 (at most 50 after de-duplication), all pre-dial.
func parseReplyRecipients(toolName string, args map[string]any, orig *email.Message, primary, ownAddress string) (*mailRecipients, error) {
	toAddrs, bad := email.ParseRecipientList([]string{primary})
	if len(bad) > 0 || len(toAddrs) == 0 {
		return nil, fmt.Errorf("%s: reply recipient %q is not a valid email address", toolName, primary)
	}
	ccEntries, err := recipientArgEntries(toolName, "cc", args["cc"])
	if err != nil {
		return nil, err
	}
	bccEntries, err := recipientArgEntries(toolName, "bcc", args["bcc"])
	if err != nil {
		return nil, err
	}
	ccAddrs, badCc := email.ParseRecipientList(ccEntries)
	if len(badCc) > 0 {
		return nil, fmt.Errorf("%s: recipient %q is not a valid email address", toolName, badCc[0])
	}
	bccAddrs, badBcc := email.ParseRecipientList(bccEntries)
	if len(badBcc) > 0 {
		return nil, fmt.Errorf("%s: recipient %q is not a valid email address", toolName, badBcc[0])
	}
	if replyAllArg(args) {
		for _, list := range []string{orig.To, orig.Cc} {
			if strings.TrimSpace(list) == "" {
				continue
			}
			addrs, badList := email.ParseRecipientList([]string{list})
			if len(badList) > 0 {
				return nil, fmt.Errorf("%s: original message recipient %q is not a valid email address", toolName, badList[0])
			}
			ccAddrs = append(ccAddrs, addrs...)
		}
	}

	// Drop the primary (already To) and the mailbox's own address from Cc,
	// then de-duplicate within Cc — a reply-all must not loop mail back to
	// the sender's own mailbox.
	primaryKey := strings.ToLower(primary)
	ownKey := strings.ToLower(strings.TrimSpace(ownAddress))
	seenCc := make(map[string]bool)
	ccClean := make([]mail.Address, 0, len(ccAddrs))
	for _, a := range ccAddrs {
		key := strings.ToLower(a.Address)
		if key == primaryKey || (ownKey != "" && key == ownKey) || seenCc[key] {
			continue
		}
		seenCc[key] = true
		ccClean = append(ccClean, a)
	}
	env := envelopeRecipientsDedup(toAddrs, ccClean, bccAddrs)
	if len(env) > maxMailRecipients {
		return nil, fmt.Errorf("%s: more than %d recipients after de-duplication (got %d); the maximum is %d",
			toolName, maxMailRecipients, len(env), maxMailRecipients)
	}
	return &mailRecipients{To: toAddrs, Cc: ccClean, Bcc: bccAddrs, Envelope: env}, nil
}

// replyAllArg reports the optional reply_all flag (absent/false → false).
func replyAllArg(args map[string]any) bool {
	b, _ := args["reply_all"].(bool)
	return b
}
