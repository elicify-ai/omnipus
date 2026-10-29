// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/library"
)

// ShowToUserToolName is the tool's registered name.
const ShowToUserToolName = "show_to_user"

// Result texts (chat-links spec, "Watching" and BS-6.9). Success is a
// request, never a claim that the Library opened (FR-015). No refusal names
// the raw rejected reference.
const (
	showToUserAskedFmt   = "Asked the Library to open %s."
	showToUserNotWatched = `The user is not watching this chat. Left an "Open in Library" notice; the Library was not opened.`

	showToUserOutside     = "show_to_user: this file is not in a Library, give the user the full path in text."
	showToUserOtherWS     = "show_to_user: that file belongs to another workspace; this chat can only open files in its own workspace."
	showToUserNotFound    = "show_to_user: there is no file the Library can open at that reference."
	showToUserNoWorkspace = "show_to_user: this chat has no workspace, so there is no Library to open."
	showToUserWorkerOnly  = "show_to_user: only the chat the user is in can open the Library."
	showToUserNoUI        = "show_to_user: no UI attached to this channel; tell the user the file name instead."
	showToUserRateLimited = "show_to_user: limit reached (5 calls per minute in this chat); tell the user where the file is instead."
	showToUserNoRequestID = "show_to_user: could not create a request id; the Library was not opened."
	showToUserNoPolicy    = "show_to_user: the file access policy for this turn could not be resolved; the Library was not opened. Retry once; if it keeps failing, ask the operator to check the gateway log."
)

// Audit reason codes, worded exactly as the chat-links spec's "Audit" rule
// lists them; request_id_unavailable is the spec's own code for a failed
// crypto/rand read. policy_unavailable covers a turn whose file policy could
// not be resolved at all.
const (
	showToUserReasonOK          = "ok"
	showToUserReasonOutside     = "outside"
	showToUserReasonOtherWS     = "other workspace"
	showToUserReasonDenied      = "denied-or-missing"
	showToUserReasonNoWorkspace = "no workspace"
	showToUserReasonDelegated   = "delegated session"
	showToUserReasonChannel     = "wrong channel"
	showToUserReasonRateLimit   = "rate limit"
	showToUserReasonRequestID   = "request_id_unavailable"
	showToUserReasonPolicy      = "policy_unavailable"
)

// ShowToUserTool asks the user's open web chat to show a file in the Library
// (chat-links US-6, FR-014). It reads nothing the agent could not already
// read; it only produces an open request the gateway delivers live.
type ShowToUserTool struct {
	BaseTool
	agentHome   string
	restrict    bool
	limiter     *ShowToUserRateLimiter
	watching    atomic.Pointer[func(sessionID string) bool]
	auditLogger *audit.Logger
}

// NewShowToUserTool builds the tool on the given shared limiter; nil means
// the process-wide limiter. Every instance must share one limiter object
// (D-limit), which is why the default is package-level, not per instance.
func NewShowToUserTool(agentHome string, restrict bool, limiter *ShowToUserRateLimiter) *ShowToUserTool {
	if limiter == nil {
		limiter = DefaultShowToUserRateLimiter()
	}
	return &ShowToUserTool{agentHome: agentHome, restrict: restrict, limiter: limiter}
}

// SetWatching injects the gateway's presence lookup: fn(sessionID) reports
// whether some browser tab is watching that chat right now (D-watch). A nil
// fn means "not watching", never "watching".
func (t *ShowToUserTool) SetWatching(fn func(sessionID string) bool) {
	if fn == nil {
		t.watching.Store(nil)
		return
	}
	t.watching.Store(&fn)
}

// SetAuditLogger satisfies the registry's auditLoggerAware contract.
func (t *ShowToUserTool) SetAuditLogger(l *audit.Logger) {
	if t == nil {
		return
	}
	t.auditLogger = l
}

func (t *ShowToUserTool) Name() string           { return ShowToUserToolName }
func (t *ShowToUserTool) Scope() ToolScope       { return ScopeGeneral }
func (t *ShowToUserTool) Category() ToolCategory { return CategoryCommunication }

func (t *ShowToUserTool) Description() string {
	return "Open the Library beside the user's chat at a file in this chat's workspace, so the user sees it. " +
		"ref is a workspace-relative path, an absolute path, or the omnipus://file/ link from a link: line. " +
		"Works only in the user's own web chat. Call it when the context line says User watching: yes; " +
		"when it says User watching: no, tell the user where the file is and do not expect the Library to open."
}

func (t *ShowToUserTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ref": map[string]any{
				"type":        "string",
				"description": "The file to show: a workspace-relative path, an absolute path, or an omnipus://file/ link.",
			},
		},
		"required": []string{"ref"},
	}
}

// showToUserCall carries one call's facts through the validation steps.
type showToUserCall struct {
	ctx         context.Context
	workspaceID string
	rootSession string
	scope       library.ResolveScope
}

// Execute validates in the spec's order (FR-014, as amended by ruling B2):
// channel, root chat session, workspace present, parse, secret carve-out,
// resolve, own workspace, read policy (regular files only), rate limit. Only a
// call that passes every check consumes budget.
func (t *ShowToUserTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if ToolChannel(ctx) != webChannelName {
		return t.refuse(ctx, showToUserReasonChannel, showToUserNoUI)
	}
	transcript, root := ToolTranscriptSessionID(ctx), ToolRootChatSessionID(ctx)
	if transcript == "" || root == "" || transcript != root {
		// A steered child's channel is stamped web chat, so the channel check
		// alone is not the worker check (BS-6.9, SEC-14).
		return t.refuse(ctx, showToUserReasonDelegated, showToUserWorkerOnly)
	}
	call := showToUserCall{ctx: ctx, workspaceID: ToolWorkspaceID(ctx), rootSession: root}
	if call.workspaceID == "" {
		return t.refuse(ctx, showToUserReasonNoWorkspace, showToUserNoWorkspace)
	}
	call.scope = library.ResolveScope{
		Home:        config.OmnipusHomeDir(),
		WorkspaceID: call.workspaceID,
	}
	ref, _ := args["ref"].(string)
	host, reason, refusal := t.parseRef(call, strings.TrimSpace(ref))
	if refusal != "" {
		return t.refuse(ctx, reason, refusal)
	}
	return t.openChecked(call, host)
}

// parseRef turns ref into an absolute host path. An omnipus:// link naming
// another workspace is refused here; a relative path is joined onto the turn
// workspace's Library directory. "", "." and anything CleanRelPath rejects
// get the not-found text, byte-identical to a missing file (BS-6.9).
func (t *ShowToUserTool) parseRef(call showToUserCall, ref string) (host, reason, refusal string) {
	rel := ref
	if strings.HasPrefix(ref, library.LinkScheme) {
		ws, linkRel, ok := library.ParseLibraryLink(ref)
		if !ok {
			return "", showToUserReasonDenied, showToUserNotFound
		}
		if ws != call.workspaceID {
			return "", showToUserReasonOtherWS, showToUserOtherWS
		}
		rel = linkRel
	} else if filepath.IsAbs(ref) {
		return filepath.Clean(ref), "", ""
	}
	cleaned, err := library.CleanRelPath(rel)
	if err != nil || cleaned == "" {
		return "", showToUserReasonDenied, showToUserNotFound
	}
	dir := call.scope.LibraryDir()
	if dir == "" {
		return "", showToUserReasonDenied, showToUserNotFound
	}
	return filepath.Join(dir, filepath.FromSlash(cleaned)), "", ""
}

// openChecked runs the resolve, own-workspace, read-policy and rate-limit
// steps, then mints the request.
func (t *ShowToUserTool) openChecked(call showToUserCall, host string) *ToolResult {
	ctx := call.ctx
	policy, err := ResolveTurnFSPolicy(ctx, t.agentHome, t.restrict)
	if err != nil {
		// SF-4: keep the cause for the operator; the agent still gets the
		// fixed refusal (no path or system detail in model-facing text).
		slog.Error("show_to_user: resolve turn file policy failed",
			"error", err,
			"agent_id", ToolAgentID(ctx),
			"session_id", ToolTranscriptSessionID(ctx),
			"call_id", ToolCallID(ctx))
		res := t.refuse(ctx, showToUserReasonPolicy, showToUserNoPolicy)
		res.Err = err
		return res
	}
	// SEC-3 / SEC-9: a secret carve-out answers with the not-found bytes even
	// when it lies outside every Library, so the agent is never told to hand
	// the user a secret file's path — and it does so whether or not the
	// secret EXISTS, or a missing master.key would get the "outside" text
	// while an existing one gets "not found", telling the agent which is on
	// disk (architect ruling B2, 2026-10-29).
	if showToUserCarveOut(host, policy) {
		return t.refuse(ctx, showToUserReasonDenied, showToUserNotFound)
	}
	ref := library.ResolveHostPath(call.scope, host)
	if !ref.InLibrary() {
		return t.refuse(ctx, showToUserReasonOutside, showToUserOutside)
	}
	if ref.WorkspaceID != call.workspaceID {
		return t.refuse(ctx, showToUserReasonOtherWS, showToUserOtherWS)
	}
	if ref.Rel == "" || !t.readable(ctx, policy, host) {
		return t.refuse(ctx, showToUserReasonDenied, showToUserNotFound)
	}
	if !t.limiter.Allow(call.rootSession) {
		return t.refuse(ctx, showToUserReasonRateLimit, showToUserRateLimited)
	}
	requestID, err := newShowToUserRequestID()
	if err != nil {
		return t.refuse(ctx, showToUserReasonRequestID, showToUserNoRequestID)
	}
	text := showToUserNotWatched
	if t.isWatching(call.rootSession) {
		text = fmt.Sprintf(showToUserAskedFmt, path.Base(ref.Rel))
	}
	res := SilentResult(withLinkLine(text, LinkLine(ctx, call.scope.Home, host)))
	res.OpenInLibrary = &LibraryOpenTarget{RequestID: requestID, WorkspaceID: call.workspaceID, Path: ref.Rel}
	t.audit(ctx, audit.DecisionAllow, showToUserReasonOK, map[string]any{
		"workspace_id": call.workspaceID, "path": ref.Rel,
	})
	return res
}

// showToUserCarveOut reports whether host is a secret carve-out by any of its
// forms: the real path when it resolves, the real path through the deepest
// existing ancestor (a missing file under a symlinked directory), and the
// cleaned lexical path (a missing file whose parents do not resolve either).
// Checking every form makes a missing secret indistinguishable from an
// existing one.
func showToUserCarveOut(host string, policy fspolicy.FSPolicy) bool {
	candidates := []string{filepath.Clean(host)}
	if real, err := filepath.EvalSymlinks(host); err == nil {
		candidates = append(candidates, real)
	}
	if deepest, ok := library.RealPathDeepest(host); ok {
		candidates = append(candidates, deepest)
	}
	for _, p := range candidates {
		if fspolicy.IsCarveOut(p, policy) {
			return true
		}
	}
	return false
}

// readable applies the agent's read policy and carve-outs through the same
// resolver every file tool uses, then requires an existing regular file.
// Denied and missing are deliberately indistinguishable (FR-015, SEC-9).
func (t *ShowToUserTool) readable(ctx context.Context, policy fspolicy.FSPolicy, host string) bool {
	handle, err := ResolvePath(ctx, policy, t.Name(), ToolCallID(ctx), FSOpRead, host)
	if err != nil {
		return false
	}
	defer handle.Close()
	// SF-1: metadata alone does not prove the Library can show the file. Open
	// it once for reading through the authorised handle — the same open the
	// Library's own reader performs — and close it without reading. A file
	// whose permissions or ACL deny reading is refused with the not-found
	// text before the limiter is charged. OpenRegularNonBlocking also refuses
	// anything that is not a regular file.
	f, err := handle.OpenRegularNonBlocking()
	if err != nil {
		return false
	}
	_ = f.Close() // read-only probe; a close error cannot undo a successful open
	return true
}

func (t *ShowToUserTool) isWatching(sessionID string) bool {
	fn := t.watching.Load()
	return fn != nil && *fn != nil && (*fn)(sessionID)
}

// refuse returns the error result and audits the refusal with its reason
// code. The audit row never carries the raw rejected reference.
func (t *ShowToUserTool) refuse(ctx context.Context, reason, text string) *ToolResult {
	t.audit(ctx, audit.DecisionDeny, reason, nil)
	return ErrorResult(text)
}

// audit writes one tool_call row per call (FR-020), success or refusal.
func (t *ShowToUserTool) audit(ctx context.Context, decision, reason string, extra map[string]any) {
	if t.auditLogger == nil {
		return
	}
	details := map[string]any{"outcome": decision, "reason": reason}
	for k, v := range extra {
		details[k] = v
	}
	entry := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventToolCall,
		Decision:  decision,
		AgentID:   ToolAgentID(ctx),
		SessionID: ToolTranscriptSessionID(ctx),
		Tool:      ShowToUserToolName,
		Details:   details,
	}
	if err := t.auditLogger.Log(entry); err != nil {
		slog.Error("show_to_user: audit log write failed", "error", err, "reason", reason)
	}
}

// newShowToUserRequestID mints the server-side request id: 16 bytes from
// crypto/rand as 32 lowercase hex characters (D-dedupe). There is no
// fallback: a failure refuses the call.
func newShowToUserRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
