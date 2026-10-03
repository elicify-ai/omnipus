package tools

// The agent attachment tools (ADR-20261001 F3; w4 spec US-3): list and read
// received attachments freely (shipped ceiling allow), save with the
// ordinary ask default and the ordinary Auto classification (founder Q4=A —
// no exception, no attachment-specific mechanism, no blocklist, no extra
// approval layer).
//
// WORKSPACE RESOLUTION IS STRUCTURAL, NEVER MODEL-SUPPLIED — the same rule
// as the rest of the email toolset: the mailbox comes from the turn context
// via EmailTransports.resolve; the message reference comes from the
// gateway-issued message_ref (consumed UNCHANGED — never synthesized, never
// Message-ID-dependent); the save destination is ALWAYS the authorized
// workspace's mail hierarchy (mail/<mailbox>/<year-month>/) — there is no
// path argument to abuse.
//
// The journey (US-3.AC-4): read_message → list → read → download runs from
// returned values alone; a wrong pair, an old epoch or a recreated folder is
// refused with the typed stale-reference error BEFORE any fetch (the pkg/
// email reader validates on the same connection that would fetch).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/mailattachment"
)

// mailAttachmentTransfers is the capability the attachment tools need on a
// resolved mailbox transport — *email.Client implements it (pkg/email/
// attachment_parts.go). The Transport interface stays dial-shaped; this is
// the same optional-capability pattern as mailboxIdentity/messageAppender.
type mailAttachmentTransfers interface {
	ReadPartCapped(ctx context.Context, slug, ref string, partIndex int) (*email.AttachmentPart, error)
	ReadPartFull(ctx context.Context, slug, ref string, partIndex int) (*email.AttachmentPart, error)
	DescribeParts(ctx context.Context, slug, ref string) ([]email.AttachmentPartDescriptor, error)
}

// mailAttachmentFolderArg validates the folder argument against the closed
// D5 slug set — the same set every folder parameter accepts.
func mailAttachmentFolderArg(toolName string, args map[string]any) (string, error) {
	folder, _ := args["folder"].(string)
	switch folder {
	case email.FolderInbox, email.FolderSent, email.FolderDrafts:
		return folder, nil
	}
	return "", fmt.Errorf("%s: folder must be one of inbox, sent, drafts", toolName)
}

// mailAttachmentRefArg validates the message_ref argument is present — the
// gateway-issued reference consumed UNCHANGED; the mail layer validates its
// shape and epoch.
func mailAttachmentRefArg(toolName string, args map[string]any) (string, error) {
	ref, _ := args["message_ref"].(string)
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("%s: message_ref is required (it is issued by read_message and list output)", toolName)
	}
	return ref, nil
}

// mailAttachmentPartIndexArg coerces the part index argument.
func mailAttachmentPartIndexArg(toolName string, args map[string]any) (int, error) {
	v, ok := args["part_index"].(float64)
	if !ok || v < 0 || v != float64(int(v)) {
		return 0, fmt.Errorf("%s: part_index must be a non-negative integer (from the attachment descriptors)", toolName)
	}
	return int(v), nil
}

// attachmentToolTransports resolves and capability-checks the transport.
func attachmentToolTransports(ctx context.Context, tps EmailTransports, toolName string) (mailAttachmentTransfers, error) {
	tp, err := tps.resolve(ctx, toolName)
	if err != nil {
		return nil, err
	}
	at, ok := tp.(mailAttachmentTransfers)
	if !ok {
		return nil, fmt.Errorf("%s: the mailbox transport does not support attachment access", toolName)
	}
	return at, nil
}

// marshalAttachmentResult serializes a tool result payload.
func marshalAttachmentResult(toolName string, v any) *ToolResult {
	data, err := json.Marshal(v)
	if err != nil {
		return ErrorResult(fmt.Sprintf("%s: marshal: %v", toolName, err))
	}
	return NewToolResult(string(data))
}

// --- list_email_attachments ---

// ListEmailAttachmentsTool lists one message's attachments (descriptors
// only).
type ListEmailAttachmentsTool struct {
	BaseTool
	tps EmailTransports
	// budget + budgetAgentID are wired at registration (SetMailBudget).
	budget        *email.MailBudget
	budgetAgentID string
}

// SetMailBudget wires the shared A8 mail-operation budget.
func (t *ListEmailAttachmentsTool) SetMailBudget(b *email.MailBudget, agentID string) {
	t.budget = b
	t.budgetAgentID = agentID
}

// NewListEmailAttachmentsTool constructs the list tool.
func NewListEmailAttachmentsTool(tps EmailTransports) *ListEmailAttachmentsTool {
	return &ListEmailAttachmentsTool{tps: tps}
}

func (t *ListEmailAttachmentsTool) Name() string           { return "list_email_attachments" }
func (t *ListEmailAttachmentsTool) Scope() ToolScope       { return ScopeGeneral }
func (t *ListEmailAttachmentsTool) Category() ToolCategory { return CategoryCommunication }
func (t *ListEmailAttachmentsTool) Description() string {
	return "List the attachments of one received email. Provide folder (inbox, sent or drafts) and message_ref (issued by read_message and message-list output). Returns each attachment's part_index (use it to read or save that attachment), filename, content_type and the server-reported transfer size. Structure metadata only: no body bytes are fetched, the message's read state never changes, and nothing is written."
}

func (t *ListEmailAttachmentsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"folder": map[string]any{
				"type":        "string",
				"enum":        []string{"inbox", "sent", "drafts"},
				"description": "The folder the message lives in.",
			},
			"message_ref": map[string]any{
				"type":        "string",
				"description": "The message reference issued by read_message or the message list — consumed as issued.",
			},
		},
		"required": []string{"folder", "message_ref"},
	}
}

// attachmentDescriptorView is the honest list descriptor: the reported
// transfer size is LABELLED (never substituted for a decoded byte count),
// and no decoded size is claimed for a metadata-only listing.
type attachmentDescriptorView struct {
	PartIndex         int    `json:"part_index"`
	Filename          string `json:"filename"`
	ContentType       string `json:"content_type"`
	Disposition       string `json:"disposition,omitempty"`
	ReportedSizeBytes *int64 `json:"reported_size_bytes,omitempty"`
}

func (t *ListEmailAttachmentsTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	const toolName = "list_email_attachments"
	folder, err := mailAttachmentFolderArg(toolName, args)
	if err != nil {
		return ErrorResult(err.Error())
	}
	ref, err := mailAttachmentRefArg(toolName, args)
	if err != nil {
		return ErrorResult(err.Error())
	}
	at, err := attachmentToolTransports(ctx, t.tps, toolName)
	if err != nil {
		return ErrorResult(err.Error())
	}
	descs, err := gateMailDial(ctx, t.budget, t.budgetAgentID, toolName, t.tps.mustResolve(ctx, toolName), map[string]any{
		"folder": folder, "ref": ref,
	}, func(c context.Context) ([]email.AttachmentPartDescriptor, error) {
		return at.DescribeParts(c, folder, ref)
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("%s failed: %v", toolName, err))
	}
	out := struct {
		MessageRef  string                     `json:"message_ref"`
		Attachments []attachmentDescriptorView `json:"attachments"`
	}{MessageRef: ref, Attachments: []attachmentDescriptorView{}}
	for _, d := range descs {
		if !d.IsAttachment || d.OmnipusDraftBody {
			// The draft's own body bookkeeping part is never a listed
			// attachment; a genuine user message.md is.
			continue
		}
		view := attachmentDescriptorView{
			PartIndex:   d.PartIndex,
			Filename:    d.Filename,
			ContentType: d.ContentType,
			Disposition: d.Disposition,
		}
		if d.ReportedSizeBytes >= 0 {
			sz := d.ReportedSizeBytes
			view.ReportedSizeBytes = &sz
		}
		out.Attachments = append(out.Attachments, view)
	}
	return marshalAttachmentResult(toolName, out)
}

// mustResolve re-resolves the transport for the budget gate's account key.
// The first resolve already succeeded, so this cannot fail in practice; the
// error path degrades to an empty account key (ungated synthetic key) rather
// than panicking.
func (m EmailTransports) mustResolve(ctx context.Context, toolName string) email.Transport {
	tp, err := m.resolve(ctx, toolName)
	if err != nil || tp == nil {
		return nil
	}
	return tp
}

// --- read_email_attachment ---

// ReadEmailAttachmentTool transiently reads one attachment part.
type ReadEmailAttachmentTool struct {
	BaseTool
	tps EmailTransports
	// budget + budgetAgentID are wired at registration (SetMailBudget).
	budget        *email.MailBudget
	budgetAgentID string
}

// SetMailBudget wires the shared A8 mail-operation budget.
func (t *ReadEmailAttachmentTool) SetMailBudget(b *email.MailBudget, agentID string) {
	t.budget = b
	t.budgetAgentID = agentID
}

// NewReadEmailAttachmentTool constructs the read tool.
func NewReadEmailAttachmentTool(tps EmailTransports) *ReadEmailAttachmentTool {
	return &ReadEmailAttachmentTool{tps: tps}
}

func (t *ReadEmailAttachmentTool) Name() string           { return "read_email_attachment" }
func (t *ReadEmailAttachmentTool) Scope() ToolScope       { return ScopeGeneral }
func (t *ReadEmailAttachmentTool) Category() ToolCategory { return CategoryCommunication }
func (t *ReadEmailAttachmentTool) Description() string {
	return "Read one email attachment's content as text. Provide folder (inbox, sent, drafts), message_ref (issued by read_message/list output) and part_index (from list_email_attachments). Text attachments return their content; binary formats (images, PDFs, Office documents) return an explicit unsupported outcome plus the descriptor — never binary disguised as text — and download_email_attachment saves them into the workspace instead. Nothing is written; the message's read state never changes."
}

func (t *ReadEmailAttachmentTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"folder": map[string]any{
				"type":        "string",
				"enum":        []string{"inbox", "sent", "drafts"},
				"description": "The folder the message lives in.",
			},
			"message_ref": map[string]any{
				"type":        "string",
				"description": "The message reference issued by read_message or the message list — consumed as issued.",
			},
			"part_index": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"description": "The attachment's part_index from list_email_attachments.",
			},
		},
		"required": []string{"folder", "message_ref", "part_index"},
	}
}

// textReadableContentType reports whether the part's declared type is text
// the agent reader can honestly represent (E-2 default (a): text-only first
// wave; no fake read for binary, ever).
func textReadableContentType(ct string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(ct)), "text/")
}

func (t *ReadEmailAttachmentTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	const toolName = "read_email_attachment"
	folder, err := mailAttachmentFolderArg(toolName, args)
	if err != nil {
		return ErrorResult(err.Error())
	}
	ref, err := mailAttachmentRefArg(toolName, args)
	if err != nil {
		return ErrorResult(err.Error())
	}
	idx, err := mailAttachmentPartIndexArg(toolName, args)
	if err != nil {
		return ErrorResult(err.Error())
	}
	at, err := attachmentToolTransports(ctx, t.tps, toolName)
	if err != nil {
		return ErrorResult(err.Error())
	}
	part, err := gateMailDial(ctx, t.budget, t.budgetAgentID, toolName, t.tps.mustResolve(ctx, toolName), map[string]any{
		"folder": folder, "ref": ref, "part_index": idx,
	}, func(c context.Context) (*email.AttachmentPart, error) {
		return at.ReadPartCapped(c, folder, ref, idx)
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("%s failed: %v", toolName, err))
	}
	if part.DataUnavailable || part.Data == nil {
		return ErrorResult(fmt.Sprintf("%s: attachment part %d (%s) is unavailable — over the 25 MiB per-part cap or failed to decode; use download_email_attachment for larger files", toolName, idx, part.Filename))
	}
	if !textReadableContentType(part.ContentType) {
		// The explicit unsupported outcome (US-3.AC-3): descriptor + the
		// Save option — never an empty "read", never binary as text.
		return marshalAttachmentResult(toolName, struct {
			Unsupported bool   `json:"unsupported"`
			Reason      string `json:"reason"`
			PartIndex   int    `json:"part_index"`
			Filename    string `json:"filename"`
			ContentType string `json:"content_type"`
			CanSave     bool   `json:"can_save"`
		}{
			Unsupported: true,
			Reason:      "binary content is not returned as text; save it into the workspace with download_email_attachment",
			PartIndex:   part.PartIndex,
			Filename:    part.Filename,
			ContentType: part.ContentType,
			CanSave:     true,
		})
	}
	return marshalAttachmentResult(toolName, struct {
		PartIndex   int    `json:"part_index"`
		Filename    string `json:"filename"`
		ContentType string `json:"content_type"`
		SizeBytes   int    `json:"size_bytes"`
		Content     string `json:"content"`
	}{
		PartIndex:   part.PartIndex,
		Filename:    part.Filename,
		ContentType: part.ContentType,
		SizeBytes:   len(part.Data),
		Content:     string(part.Data),
	})
}

// --- download_email_attachment ---

// DownloadEmailAttachmentTool saves one attachment into the workspace's mail
// hierarchy through the SHARED transfer service (the same service, cap,
// naming, audit event and refusal semantics as the panel's Save).
type DownloadEmailAttachmentTool struct {
	BaseTool
	tps EmailTransports
	// budget + budgetAgentID are wired at registration (SetMailBudget).
	budget        *email.MailBudget
	budgetAgentID string
	// service + home/restrict are wired at registration by the optional
	// SetTransferService setter (the SetMailBudget pattern); an unwired
	// tool refuses with an explicit, visible error — never a silent no-op.
	service   *mailattachment.Service
	agentHome string
	restrict  bool
}

// SetMailBudget wires the shared A8 mail-operation budget.
func (t *DownloadEmailAttachmentTool) SetMailBudget(b *email.MailBudget, agentID string) {
	t.budget = b
	t.budgetAgentID = agentID
}

// SetTransferService wires the shared transfer service and the turn
// filesystem policy inputs (agentHome/restrict), mirroring how the file
// tools receive their workspace root. The destination stays the authorized
// workspace's mail hierarchy — there is no path argument.
func (t *DownloadEmailAttachmentTool) SetTransferService(s *mailattachment.Service, agentHome string, restrict bool) {
	t.service = s
	t.agentHome = agentHome
	t.restrict = restrict
}

// NewDownloadEmailAttachmentTool constructs the save tool.
func NewDownloadEmailAttachmentTool(tps EmailTransports) *DownloadEmailAttachmentTool {
	return &DownloadEmailAttachmentTool{tps: tps}
}

func (t *DownloadEmailAttachmentTool) Name() string           { return "download_email_attachment" }
func (t *DownloadEmailAttachmentTool) Scope() ToolScope       { return ScopeGeneral }
func (t *DownloadEmailAttachmentTool) Category() ToolCategory { return CategoryCommunication }
func (t *DownloadEmailAttachmentTool) Description() string {
	return "Save one email attachment into your workspace's Library under mail/<mailbox>/<year-month>/ (the same place, name rules, 25 MiB cap and audit entry as the panel's Save to Library). Provide folder (inbox, sent, drafts), message_ref (issued by read_message/list output) and part_index (from list_email_attachments). The destination is chosen for you — there is no path parameter. Returns the saved file's workspace-relative path, absolute path and size; your normal file tools can then read that exact path. Saving a file is a normal permission-gated action: it asks under the ordinary permission settings and Auto-approve treats it like any other workspace write."
}

func (t *DownloadEmailAttachmentTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"folder": map[string]any{
				"type":        "string",
				"enum":        []string{"inbox", "sent", "drafts"},
				"description": "The folder the message lives in.",
			},
			"message_ref": map[string]any{
				"type":        "string",
				"description": "The message reference issued by read_message or the message list — consumed as issued.",
			},
			"part_index": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"description": "The attachment's part_index from list_email_attachments.",
			},
		},
		"required": []string{"folder", "message_ref", "part_index"},
	}
}

// AutoApproveVerdict classifies the save under the EXISTING workspace-path
// conditional class (founder Q4=A — no exception, no attachment-specific
// mechanism): the destination is ALWAYS the workspace mail hierarchy, so
// the evaluated path is that fixed hierarchy root inside the turn's work
// dir. It never refuses the call itself — an unevaluable case asks.
func (t *DownloadEmailAttachmentTool) AutoApproveVerdict(ctx context.Context, args map[string]any) AutoVerdict {
	if t.service == nil {
		return autoAsks(t.Name() + " save destination is not configured")
	}
	return autoWorkspaceVerdict(ctx, t.agentHome, t.restrict, t.Name(), FSOpWrite,
		"mail/", nil, fspolicy.PathGrantAccessWrite)
}

func (t *DownloadEmailAttachmentTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	const toolName = "download_email_attachment"
	folder, err := mailAttachmentFolderArg(toolName, args)
	if err != nil {
		return ErrorResult(err.Error())
	}
	ref, err := mailAttachmentRefArg(toolName, args)
	if err != nil {
		return ErrorResult(err.Error())
	}
	idx, err := mailAttachmentPartIndexArg(toolName, args)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if t.service == nil {
		return ErrorResult(fmt.Sprintf("%s: the shared transfer service is not wired for this agent; saving is unavailable", toolName))
	}
	_, err = attachmentToolTransports(ctx, t.tps, toolName)
	if err != nil {
		return ErrorResult(err.Error())
	}
	// The save runs through the SHARED service — same cap, naming, audit
	// and refusal semantics as the panel (US-3.AC-7). The token is
	// generated per call: this is one explicit save attempt.
	receipt, err := t.service.Save(ctx, mailattachment.SaveRequest{
		Slug:      folder,
		Ref:       ref,
		PartIndex: idx,
		Token:     fmt.Sprintf("agent-%s-%d", ToolAgentID(ctx), time.Now().UnixNano()),
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("%s: could not save to Library: %v", toolName, err))
	}
	return marshalAttachmentResult(toolName, struct {
		Saved        bool   `json:"saved"`
		Path         string `json:"path"`
		AbsolutePath string `json:"absolute_path"`
		SizeBytes    int64  `json:"size_bytes"`
		AuditStatus  string `json:"audit_status"`
		WarningCode  string `json:"warning_code,omitempty"`
		FromRetry    bool   `json:"prior_receipt,omitempty"`
	}{
		Saved:        true,
		Path:         receipt.Path,
		AbsolutePath: receipt.AbsolutePath,
		SizeBytes:    receipt.SizeBytes,
		AuditStatus:  receipt.AuditStatus,
		WarningCode:  receipt.WarningCode,
		FromRetry:    receipt.FromReceipt,
	})
}
