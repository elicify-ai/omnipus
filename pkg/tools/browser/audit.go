// Omnipus — per-action browser audit (ADR-075 D1, FR-027 / FR-058)
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// WHY THIS FILE EXISTS. A workspace's browser carries the operator's live
// logins, and every agent on that workspace drives the same one. "Which agent
// made the purchase?" is therefore a question the audit trail has to be able
// to answer, and it can only answer it if it records the ACTION rather than
// the acquaintance.
//
// ADR D2.11 rejects first-use-only auditing BY NAME: an event on first use of
// a context an agent did not establish fires once per agent per workspace and
// says nothing about the tenth action. So there are exactly two events here:
// one when a browser instance comes into existence, and one PER write-class
// tool call — the tenth included.
//
// READ-ONLY CALLS ARE NOT RECORDED PER CALL. browser_list_tabs,
// browser_screenshot, browser_get_text and browser_wait inject nothing and
// change nothing; auditing them would bury the seven calls that matter under
// the four that do not. The write-class set is not a second list maintained
// alongside the control-lock's: it IS the controlledResult-gated set (§14
// rule 3), and writeClassBrowserTools below is asserted against the code that
// gates them.
//
// PACKAGE BOUNDARY. capture_session.go's header states that THAT FILE does not
// import pkg/audit and that the gateway is the only place that constructs wire
// frames and emits audit entries. That rule is file-local and is about WIRE
// FRAMES; pkg/tools already imports pkg/audit from path_audit.go for exactly
// this shape of tool-side emission, and FR-027's events are only observable
// from inside the tool's Execute path. This file follows path_audit.go's
// contract to the letter: a nil logger is a silent no-op, emission is
// best-effort, and it never changes enforcement.

package browser

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// writeClassBrowserTools is the WRITE-CLASS set: the seven tools that change
// what the browser is doing and are therefore gated by controlledResult.
// Exactly these seven are audited per call (FR-027).
//
// It is ONE list, not two (§14 rule 3). audit_test.go asserts this map and the
// set of tools that actually call controlledResult are the same set, so the two
// cannot drift into a build where a tool is gated but unaudited.
var writeClassBrowserTools = map[string]bool{
	"browser_navigate":   true,
	"browser_click":      true,
	"browser_type":       true,
	"browser_evaluate":   true,
	"browser_switch_tab": true,
	"browser_close_tab":  true,
	"browser_open_tab":   true,
	// ADR-075 D2 — the four new interaction verbs. All four call
	// controlledResult (FR-040), so under §14 rule 3's biconditional all four
	// are write-class and all four are leased. Adding a verb here without
	// wiring controlledResult, or the reverse, is what audit_test.go's
	// set-equality assertion exists to catch.
	"browser_select_option": true,
	"browser_press_key":     true,
	"browser_hover":         true,
	"browser_upload_file":   true,
}

// readOnlyBrowserTools is the complement: the four tools that observe without
// acting. They are never audited per call. Listed explicitly rather than
// derived, so that a NEW browser tool belongs to neither set and audit_test.go
// says so instead of silently defaulting it into the exempt half.
var readOnlyBrowserTools = map[string]bool{
	"browser_list_tabs":  true,
	"browser_screenshot": true,
	"browser_get_text":   true,
	"browser_wait":       true,
	// ADR-075 D2 FR-038 — browser_snapshot is read-only. It calls neither
	// controlledResult nor the write lease, so it belongs here and NOT in the
	// write-class set. It emits its OWN metadata-only browser_snapshot event
	// (FR-028) rather than a browser_action row: the two answer different
	// questions and recordBrowserAction refuses a non-write-class name by
	// design.
	"browser_snapshot": true,
	// ADR-075 D2 FR-035 — browser_handle_dialog is exempt for a DIFFERENT
	// reason from everything else in this map, and the difference is worth
	// stating rather than letting the shared membership imply sameness.
	//
	// It is NOT read-only: HandleJavaScriptDialog changes what the page is
	// doing. It is exempt because it is the RECOVERY verb. The browser_click
	// that raised the dialog is still blocked on CDP — that blockage IS the
	// wedge — and it holds the write lease; controlledResult would defer the
	// dialog tool for the whole wedge window, and a human on the same tab has
	// no button either. Gating a recovery verb behind the mechanisms the fault
	// disables is a deadlock, not a safety property.
	//
	// It is listed HERE rather than left out of both maps because the
	// biconditional test treats an unclassified tool as a defect: a tool in
	// neither set has an undecided audit treatment, which is exactly how a new
	// tool would default silently into the exempt half.
	"browser_handle_dialog": true,
}

// browserAudit is the audit sink every browser tool embeds. It is populated by
// the tool registry through the auditLoggerAware contract
// (pkg/tools/registry.go) — the same mechanism the memory and filesystem tools
// use — so RegisterTools needs no new parameter and no caller changes.
//
// The pointer is atomic because SetAuditLogger runs from the registry (on
// registration and on ToolRegistry.SetAuditLogger, which re-runs on every hot
// reload) while Execute may be reading it on another goroutine. A plain field
// would be a data race with no symptom until -race, or in production a torn
// read of an 8-byte pointer on the architectures where that is possible.
type browserAudit struct {
	log atomic.Pointer[audit.Logger]
}

// SetAuditLogger satisfies pkg/tools' auditLoggerAware interface. A nil logger
// is legitimate — audit logging can be disabled (cfg.Sandbox.AuditLog=false),
// and tests construct tools with no registry at all.
func (b *browserAudit) SetAuditLogger(l *audit.Logger) { b.log.Store(l) }

// auditLogger returns the current logger, or nil.
func (b *browserAudit) auditLogger() *audit.Logger { return b.log.Load() }

// recordBrowserAction writes the ONE per-call event for a write-class browser
// tool (FR-027).
//
// WHERE IT IS CALLED FROM, and this is a contract rather than a convenience:
// after both gates (controlledResult and the write lease) and BEFORE the CDP
// work. That ordering is deliberate in both directions.
//
//   - AFTER the gates, because a call that deferred to a human holding the
//     live-view control lock, or lost the write lease, never acted on the
//     browser. Recording it as an action would put an event in the trail for
//     something that provably did not happen.
//   - BEFORE the work, because the trail must record the ATTEMPT. A purchase
//     that failed halfway is exactly the entry an operator reconstructing
//     "what did this agent do to my logged-in session?" needs to find, and an
//     after-the-fact emission loses it on every panic, timeout and cancel.
//
// A tool name that is not write-class is refused rather than recorded: the
// classification lives in one place, and a caller that reaches here with a
// read-only tool has a bug that a silent extra audit row would hide.
func (b *browserAudit) recordBrowserAction(
	ctx context.Context, key BrowsingKey, owner TabOwner, toolName, targetHost string,
) {
	if !writeClassBrowserTools[toolName] {
		slog.Error("browser audit: refusing to record a non-write-class tool as an action",
			"tool", toolName)
		return
	}
	log := b.auditLogger()
	if log == nil {
		return
	}
	entry := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventBrowserAction,
		Decision:  audit.DecisionAllow,
		AgentID:   tools.ToolAgentID(ctx),
		SessionID: tools.ToolTranscriptSessionID(ctx),
		Tool:      toolName,
		Details: map[string]any{
			"workspace_id": key.WorkspaceID(),
			"browsing_key": key.String(),
			"tab_owner":    owner.String(),
			"host":         targetHost,
		},
	}
	if err := log.Log(entry); err != nil {
		slog.Error("browser audit: action log write failed",
			"error", err, "tool", toolName, "workspace_id", key.WorkspaceID())
	}
}

// noteBrowserInstance writes the ONE browser-instance-creation event for a
// browsing key, the first time any tool resolves that browser (FR-027).
//
// This is NOT the per-action event in disguise and does not substitute for it.
// It answers a different question — "when did this workspace get a browser, and
// who established it?" — and it fires once per instance, which is why the
// per-call event exists alongside it rather than instead of it.
//
// mgr.markInstanceAudited is the once-only latch: an atomic compare-and-swap on
// the manager, taking no manager lock, so this stays safe to call from the hot
// resolve path of every browser tool call.
func (b *browserAudit) noteBrowserInstance(ctx context.Context, mgr *BrowserManager, key BrowsingKey) {
	if mgr == nil || !mgr.markInstanceAudited() {
		return
	}
	log := b.auditLogger()
	if log == nil {
		return
	}
	entry := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventBrowserInstanceCreated,
		Decision:  audit.DecisionAllow,
		AgentID:   tools.ToolAgentID(ctx),
		SessionID: tools.ToolTranscriptSessionID(ctx),
		Details: map[string]any{
			"workspace_id": key.WorkspaceID(),
			"browsing_key": key.String(),
		},
	}
	if err := log.Log(entry); err != nil {
		slog.Error("browser audit: instance-creation log write failed",
			"error", err, "workspace_id", key.WorkspaceID())
	}
}

// markInstanceAudited reports whether THIS call is the one that should emit the
// instance-creation event. It returns true exactly once per manager, ever.
//
// Lock-free by design: it is reached on the resolve path of every browser tool
// call, and m.mu is held across CDP work elsewhere in this type.
func (m *BrowserManager) markInstanceAudited() bool {
	return m.instanceAudited.CompareAndSwap(false, true)
}

// targetHostForTool computes the "target host" field FR-027 requires. The spec
// names the field and not its derivation, so the rule is fixed here and stated
// once:
//
//	the host of the page the call ACTS ON, evaluated BEFORE the action.
//
//	browser_navigate, browser_open_tab   the host of the requested URL
//	browser_switch_tab, browser_close_tab the host of the tab at `index`
//	browser_click/type/evaluate           the host of the currently-active tab
//
// "Before" is load-bearing for browser_close_tab: after the action the tab is
// gone and there is no host left to name. An unparseable, relative or empty URL
// yields "" rather than a guess — a fabricated host in an audit trail is worse
// than an absent one.
func targetHostForTool(rawURL string) string {
	raw := strings.TrimSpace(rawURL)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// hostOfTabAt returns the host of tab `index` in sessionID's set, or "" when
// there is no such tab. Used by browser_switch_tab and browser_close_tab, which
// name their target by index rather than by URL.
func hostOfTabAt(mgr *BrowserManager, sessionID string, index int) string {
	_, tabs, _, err := mgr.ListTabsState(sessionID)
	if err != nil || index < 0 || index >= len(tabs) {
		return ""
	}
	return targetHostForTool(tabs[index].URL)
}

// hostOfActiveTab returns the host of sessionID's currently-active tab, or ""
// when there is none. Used by the three tools that act on wherever the browser
// already is.
func hostOfActiveTab(mgr *BrowserManager, sessionID string) string {
	_, tabs, activeIdx, err := mgr.ListTabsState(sessionID)
	if err != nil || activeIdx < 0 || activeIdx >= len(tabs) {
		return ""
	}
	return targetHostForTool(tabs[activeIdx].URL)
}
