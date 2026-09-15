// Omnipus — browser_handover (ADR-085 D7, BROWSER-FR-046/047/048a/049/051/052).
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// WHY THIS FILE EXISTS. Every other browser tool asks "can I act?" and
// defers to a human who already holds the wheel (controlledResult). This is
// the one tool that runs the other direction: the AGENT decides a step
// should not be its own (a sign-in page, a payment step, anything it should
// not do on the operator's behalf) and hands the wheel to the operator on
// purpose (ADR-085 US-9).
//
// SCOPE, per the joint delivery plan (wave B6, C-70). The catalogue name,
// the global tool-policy ceiling entry and the six per-agent seed entries
// are wave E1's — landed in pkg/coreagent/core.go and pkg/config/defaults.go
// BEFORE this file exists, to avoid the two-commit boot-panic ordering
// Constraint #6/#10 forbids (a per-agent override naming a tool absent from
// allStaticToolNames panics at boot). The handover-pending STATE machine
// itself — the handoverPending/handoverReason fields on LiveView, the
// SetHandoverPending method, the ghost-voiding exemption (FR-047's "MUST
// NOT be subject to the ghost rule of FR-031") and the FR-041 waiting-
// surface emission (FR-048) that a caller's SetHandoverPending transition
// eventually drives — are wave B123's, in live.go/manager.go and the
// gateway. What is left for THIS file is the tool itself: parsing the
// argument, calling the already-built SetHandoverPending seam, refusing per
// FR-052 when the operator has switched the feature off, and returning the
// FR-049 non-parking, non-error "conclude your turn" result.
//
// D-G (operator decision, 2026-09-11): a handover — and this tool's own
// refusal — must never tell the agent to wait for the wheel back, and never
// asks for it back either. If the agent still has browser work to do, the
// way to continue is to open a NEW tab (browser_open_tab), not to sit idle
// on this one. Both the success message and the FR-052 refusal message say
// so explicitly.
//
// AUDIT. audit.EventBrowserHandover's own doc comment (pkg/audit/events.go)
// names this file as its sole intended emitter — unlike every other browser
// audit event, which is emitted from pkg/tools/browser/audit.go (B123's
// file, outside this wave's write-set). recordHandover below is this tool's
// own, self-contained emitter, deliberately NOT added to audit.go.

package browser

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// handoverReasonMaxRunes is BROWSER-FR-048a's cap: the model-authored reason
// reaches an operator-facing surface (the FR-041 waiting notice) and the
// audit trail, so it is capped and always treated as plain text, never
// markup — an injection surface for no benefit otherwise.
const handoverReasonMaxRunes = 200

// HandoverTool implements browser_handover (BROWSER-FR-046).
type HandoverTool struct {
	tools.BaseTool
	// browserAudit is FR-027-style audit-sink plumbing, populated by the
	// tool registry through the auditLoggerAware contract (pkg/tools/
	// registry.go) — same mechanism every other browser tool uses (see
	// audit.go's doc comment on the embedded field). This tool's own
	// EventBrowserHandover record does not reuse browserAudit's
	// recordBrowserAction/recordControlDeferral methods (those are
	// write-class/deferral shaped, and this call is neither) — it uses only
	// the embedded auditLogger() accessor, via recordHandover below.
	browserAudit
	res ManagerResolver
}

func (t *HandoverTool) Name() string                 { return "browser_handover" }
func (t *HandoverTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *HandoverTool) Category() tools.ToolCategory { return tools.CategoryBrowser }

func (t *HandoverTool) Description() string {
	return "Hand the browser over to the human operator when you reach a step you should not perform yourself " +
		"— a sign-in page, a payment step, an MFA prompt, or anything else that needs a person rather than an " +
		"agent. Pass `reason` explaining, in your own words, what you need the operator to do; it is shown to " +
		"them. This tool puts the browser in the operator's hands and ends your turn normally — it is NOT an " +
		"error and does not suspend or park your turn, so conclude your response right after calling it rather " +
		"than attempting further browser actions (they will defer while the operator holds the wheel). The " +
		"operator resumes your driving by sending you a new message; there is no way to ask for the wheel back " +
		"in the same turn. If you have OTHER, unrelated browser work to do, open a new tab with " +
		"browser_open_tab rather than waiting on this one."
}

func (t *HandoverTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{
				"type": "string",
				"description": "Why you are handing the browser over, in your own words. Shown to the operator " +
					"as-is (plain text, truncated to 200 characters) — say what you need them to do.",
			},
		},
	}
}

func (t *HandoverTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	rawReason, _ := args["reason"].(string)
	reason := truncateRunes(strings.TrimSpace(rawReason), handoverReasonMaxRunes)

	mgr, key, _, owner, sid, failure := resolveTurn(ctx, t.res, &t.browserAudit, t.Name())
	if failure != nil {
		return failure
	}

	// BROWSER-FR-052: with take-control disabled, browser_handover MUST NOT
	// take effect, and MUST NOT become a bypass for the disabled feature —
	// so this returns BEFORE touching the live view at all; no
	// handover-pending state is set, no audit record is written. US-11 AS-2.
	//
	// D-G: the refusal itself must not tell the agent to wait, and must not
	// suggest asking for the wheel back — there is nothing to wait FOR here
	// (nobody can take this tab from the agent while the feature is off).
	// If the agent's own reason for calling this was to free the browser up
	// for something else, a new tab is that route.
	if !mgr.cfg.TakeControlEnabled {
		return tools.NewToolResult(
			"browser_handover: take-control is disabled on this installation (tools.browser.take_control_enabled=false), " +
				"so the browser was NOT handed to the operator — this tab is still yours to drive. Do not wait for " +
				"this to change and do not ask for it back. If you have other browser work to do, open a new tab " +
				"with browser_open_tab rather than waiting on this one.",
		)
	}

	// BROWSER-FR-047: puts the browser in the operator's hands by setting
	// handover-pending on the turn's RESOLVED tab set (sid/owner — a
	// delegated child's own tab set, never the root chat's, per FR-050's
	// doc comment on why that distinction matters at release time). This is
	// the one call every other consequence in this file follows from: the
	// control gate (isStoodDownLocked) already treats a live
	// handover-pending exactly like a held wheel, exempt from the FR-031
	// ghost rule, with no further code needed here — that plumbing is
	// wave B123's, in live.go.
	mgr.Live().SetHandoverPending(sid, reason)

	// BROWSER-FR-062: the agent's own, successful, affirmative action —
	// recorded here, not in audit.go, per EventBrowserHandover's own doc
	// comment (pkg/audit/events.go).
	t.recordHandover(ctx, key, owner, reason)

	// BROWSER-FR-049: a non-error result instructing the agent to conclude
	// its turn with an explanation. ParksTurn is left at its zero value
	// (false) — never set here, satisfying "MUST NOT set ParksTurn"
	// structurally rather than by omission alone.
	msg := "browser_handover: the browser has been handed to the operator"
	if reason != "" {
		msg += " (" + reason + ")"
	}
	msg += ". Conclude your turn now with a brief explanation of why you stopped. Do not attempt further " +
		"browser actions this turn — they will defer while the operator holds the wheel. Do not wait for or " +
		"ask for control back: the operator resumes your driving by sending a new message. If you have other, " +
		"unrelated browser work to do, open a new tab with browser_open_tab rather than waiting on this one."
	return tools.NewToolResult(msg)
}

// truncateRunes returns s unchanged if it has at most limit runes, otherwise
// the first limit runes. Rune-based (not byte-based) so a multi-byte
// character is never split mid-encoding — BROWSER-FR-048a's cap is stated
// in runes ("200 runes"), not bytes.
//
// The parameter is named limit rather than max because max is a predeclared
// identifier since Go 1.21 and golangci-lint's `predeclared` linter rejects
// shadowing it.
func truncateRunes(s string, limit int) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit])
}

// recordHandover writes the ADR-085 BROWSER-FR-062 audit record for one
// browser_handover call. Unlike recordControlDeferral (audit.go, B123's
// file) — which reports something DONE TO the agent by a human, with the
// human as the subject and the agent as the one deferred — this reports the
// agent's OWN affirmative action, so the agent is the actor (Entry.AgentID),
// never an "acting_user". reason has already been trimmed and truncated to
// handoverReasonMaxRunes by Execute; an empty reason produces no "reason"
// key in Details at all, never an empty-string one (FR-048a's "no empty
// parenthetical" rule, applied to the audit record the same way it applies
// to the waiting-surface body).
func (t *HandoverTool) recordHandover(ctx context.Context, key BrowsingKey, owner TabOwner, reason string) {
	log := t.auditLogger()
	if log == nil {
		return
	}
	details := map[string]any{
		"workspace_id":         key.WorkspaceID(),
		"browsing_key":         key.String(),
		"tab_owner":            owner.String(),
		"root_chat_session_id": tools.ToolRootChatSessionID(ctx),
		"tool_call_id":         tools.ToolCallID(ctx),
	}
	if reason != "" {
		details["reason"] = reason
	}
	entry := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventBrowserHandover,
		Decision:  audit.DecisionAllow,
		AgentID:   tools.ToolAgentID(ctx),
		SessionID: tools.ToolTranscriptSessionID(ctx),
		Tool:      t.Name(),
		Details:   details,
	}
	if err := log.Log(entry); err != nil {
		slog.Error("browser audit: handover log write failed",
			"error", err, "workspace_id", key.WorkspaceID())
	}
}

// Compile-time interface check.
var _ tools.Tool = (*HandoverTool)(nil)
