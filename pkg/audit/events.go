// Package audit — Tool Registry Redesign (Wave A2) audit event types.
//
// This file declares the audit event-name constants, severity constants,
// reason enum, and lightweight emitter helpers required by the Central
// Tool Registry redesign spec (`docs/internal/specs/tool-registry-redesign-spec.md`,
// revision 6). The events listed below are referenced by FRs:
//
//	FR-011, FR-038, FR-047, FR-049, FR-051, FR-054, FR-057, FR-060,
//	FR-063, FR-066, FR-074, FR-080, FR-083 — and the spec's
//	"Audit & Observability" table.
//
// Every emitter in this file is non-blocking and best-effort: emission
// failure is logged via slog but never bubbled up to the caller, except
// the boot-abort path (FR-063) which uses a stderr fallback before exit
// (see boot_abort.go).

package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// ---------------------------------------------------------------------------
// Event-name constants (FR-038, FR-047, FR-051, FR-054, FR-066, FR-074, FR-083).
//
// These names are part of the audit wire contract: log shippers and SIEM
// rules grep on them. Renaming any constant here is a breaking change.
// ---------------------------------------------------------------------------

const (
	// EventToolPolicyDenyAttempted — WARN. LLM emitted a tool_call for a tool
	// whose effective policy is `deny`. Reachable only via stale model state
	// (e.g. mid-turn policy change). Spec table; FR-079.
	EventToolPolicyDenyAttempted = "tool.policy.deny.attempted"

	// EventToolPolicyAskRequested — INFO. Approval event emitted (loop paused
	// awaiting human-in-the-loop). FR-011, FR-074.
	EventToolPolicyAskRequested = "tool.policy.ask.requested"

	// EventToolPolicyAskGranted — INFO. User approved an ask call. FR-054, FR-074.
	EventToolPolicyAskGranted = "tool.policy.ask.granted"

	// EventToolPolicyAskDenied — INFO. User denied an ask call OR a system-deny
	// path fired (timeout, cancel, restart, saturated, batch_short_circuit).
	// FR-047, FR-054, FR-074, FR-065 (combined event for batch short-circuit).
	EventToolPolicyAskDenied = "tool.policy.ask.denied"

	// EventToolCollisionMCPRejected — WARN. Central registry refused an MCP
	// registration because of a name collision with a builtin or a
	// previously-registered MCP server. FR-034, FR-060.
	EventToolCollisionMCPRejected = "tool.collision.mcp_rejected"

	// EventAgentConfigCorrupt — HIGH. Boot-time validator could not parse or
	// read a particular agent.json. FR-023.
	EventAgentConfigCorrupt = "agent.config.corrupt"

	// EventAgentConfigInvalidPolicyValue — HIGH. Boot-time validator rejected
	// a per-tool policy value that is outside `{"allow", "ask", "deny"}`.
	// Includes empty strings (FR-085 supersedes the legacy
	// `agent.config.empty_policy_value_coerced`). (There is no
	// default_policy/GlobalDefaultPolicy field any more — CLAUDE.md hard
	// constraint 6 — every tool-policy decision is an explicit, literal
	// entry.) FR-049, FR-085.
	EventAgentConfigInvalidPolicyValue = "agent.config.invalid_policy_value"

	// EventAgentConfigUnknownToolInPolicy — WARN. Boot-time validator found
	// a per-agent policy entry referring to an unregistered tool name. FR-057.
	EventAgentConfigUnknownToolInPolicy = "agent.config.unknown_tool_in_policy"

	// EventToolAssemblyDuplicateName — HIGH. The final dedup pass during
	// `tools[]` assembly observed two registry entries with the same name —
	// an invariant violation. FR-066.
	EventToolAssemblyDuplicateName = "tool.assembly.duplicate_name"

	// EventMCPServerRenamed — HIGH. A reload detected an MCP server config
	// rename (transport+endpoint identical, name changed). Old entries
	// evicted, new entries added. FR-051, FR-068, FR-083.
	EventMCPServerRenamed = "mcp.server.renamed"

	// EventGatewayStartupGuardDisabled — WARN. Operator booted with
	// `gateway.tool_approval_max_pending=0` (sentinel "unlimited"); DoS risk
	// flagged for visibility. FR-016.
	EventGatewayStartupGuardDisabled = "gateway.startup.guard_disabled"

	// EventGatewayConfigInvalidValue — HIGH. Operator booted with a negative
	// `gateway.tool_approval_max_pending` or another invalid gateway config
	// value. The gateway exits non-zero after emitting this event. FR-016.
	EventGatewayConfigInvalidValue = "gateway.config.invalid_value"

	// EventTurnAbortedToolDenialBudget — WARN. The loop aborted a turn after
	// its aggregate per-turn tool-denial budget (turnDenialBudget = 10,
	// pkg/agent/tool_denial.go) was exhausted — every denial response handed
	// to the model in the turn, real or served from the ADR-058 quarantine
	// cache, regardless of tool or reason. Replaces FR-084's retired
	// synthetic-loop abort event (ADR-058 §10.A3, spec §3.3): that
	// mechanism's one surviving call site could never fire more than once per
	// turn (#595), so it was deleted rather than repaired. Emitted directly
	// via audit.EmitEntry from AgentLoop.abortTurn's "tool_denial_budget"
	// caller — there is no dedicated Emit* helper for this event, matching
	// how the code it replaces was already emitted (F2: its Emit* helper had
	// zero callers).
	EventTurnAbortedToolDenialBudget = "turn.aborted_tool_denial_budget"

	// EventApproverFallback — HIGH. The agent loop hit `nopPolicyApprover`
	// in a default (production) build, meaning `SetToolApprover` was never
	// called and an `ask`-policy tool would be denied with reason
	// "no_approver_configured". This event signals that the approval gate
	// is mis-wired and ANY ask-policy tool — including admin-flagged ones —
	// is being failed-closed in production.
	//
	// Emitted at most once per process via sync.Once: the first hit is the
	// diagnostic signal, subsequent denies are repeated by definition and
	// would flood the audit log if a misconfigured deployment kept calling
	// ask-policy tools. Closes V2.B silent-failure-hunter BE CRIT-1.
	EventApproverFallback = "approver.fallback"

	// EventTurnCancelAttempt — INFO. A cancel request arrived for a session.
	// Emitted for every attempt including duplicates and no-op cancels (FR-10,
	// FR-11). The was_fired field indicates whether this attempt triggered an
	// actual interrupt or was a no-op (e.g. turn already finished).
	EventTurnCancelAttempt = "turn.cancel.attempt"

	// EventTurnCancelled — INFO. A canceled turn has fully exited and the
	// transcript has been marked. Contains the cancel_method ("graceful" or
	// "hard") and the list of descendant turn IDs that were also canceled
	// (FR-15, FR-17, FR-18).
	EventTurnCancelled = "turn.cancelled"

	// EventTurnCancelStuck — WARN. The turn goroutine did not exit within
	// 5 seconds after the hard-abort signal was sent. The turn has been
	// detached (abandoned=true) and the gateway will stop waiting for it
	// (FR-19, FR-20, FR-21).
	EventTurnCancelStuck = "turn.cancel.stuck"

	// EventTurnCancelBackgroundKilled — INFO. RequestCancel invoked
	// hooks.KillBackgroundSessions for a resolvable sessionID and records the
	// count killed (may be 0 — a session with no background work). Emitted
	// UNCONDITIONALLY whenever the hook fires, independent of whether an
	// active turn was found/claimed (ClaimCancel's wasFired) — this is
	// deliberately decoupled from EventTurnCancelAttempt/EventTurnCancelled
	// because a `bash run_in_background=true` call's own turn ends
	// immediately, well before a user later cancels the still-running
	// background job; by then there is no active turn to claim, so this is
	// the ONLY audit record of that cancel's background-kill cascade. See
	// pkg/agent/cancel.go's RequestCancel doc comment for the full root-cause
	// writeup.
	EventTurnCancelBackgroundKilled = "turn.cancel.background_killed"

	// EventCancelAbusePattern — WARN. A single canceller (user + channel)
	// sent >= 10 cancel requests within 60 seconds, suggesting runaway client
	// logic or intentional abuse (FR-25a).
	EventCancelAbusePattern = "cancel.abuse_pattern"

	// EventTurnOrphanTimeout — INFO. The orphan-foreground-turn watchdog
	// (ADR-045) fired: a webchat session's grace period elapsed with no
	// client reattaching, no surviving Critical/background delegate was found
	// on the session, and nobody had reconnected — so the watchdog handed the
	// session's root turn to al.RequestCancel (the SAME cancellation state
	// machine every other cancel surface uses), attributed to
	// "system:orphan-watchdog" via CancelCanceller rather than a real
	// user/channel canceller. Emitted immediately BEFORE the RequestCancel
	// call, so the audit trail always records WHY a cancel was triggered even
	// if RequestCancel itself no-ops (turn already finished) or errors.
	// Distinct from — and normally followed by — RequestCancel's OWN
	// EventTurnCancelAttempt (always) and EventTurnCancelled (unless the root
	// turn finishes naturally in the narrow gap before RequestCancel claims it,
	// in which case the reap is a logged no-op — see reapOrphanForegroundTurn),
	// which carry the full
	// graceful->hard->detached escalation, approval auto-deny,
	// background-session kill, and transcript writes uniformly with every
	// other cancel surface. There is no separate turn.orphan_hard_aborted
	// event (retired 2026-07 redesign) — RequestCancel's own turn_canceled
	// event (cancel_method: "hard") is the single source of truth for how a
	// reaped orphan turn actually terminated.
	EventTurnOrphanTimeout = "turn.orphan_timeout"

	// EventBrowserInstanceCreated — INFO. A workspace's browser instance came
	// into existence: the first turn to resolve a browser for a given
	// BrowsingKey established it (ADR-075 FR-027). Fires exactly ONCE per
	// browser instance, not once per agent and not once per turn. Fields:
	// {workspace_id, browsing_key}; the establishing agent is Entry.AgentID
	// and the turn's transcript session is Entry.SessionID.
	//
	// NAME SHAPE, deliberately: underscores only, no dots. FR-058 requires
	// ^[a-z_]+$ of every name this change introduces. The AuditEntry contract
	// itself was widened to ^[a-z_.]+$ by issue #667, so a dotted name would
	// also be legal on the wire — but a name that satisfies BOTH the spec and
	// the contract needs no adjudication between them. Do not "tidy" these two
	// into the dotted browser.* family without reopening FR-058.
	EventBrowserInstanceCreated = "browser_instance_created"

	// EventBrowserAction — INFO. ONE event per WRITE-CLASS browser tool call:
	// the seven controlledResult-gated tools (browser_navigate, browser_click,
	// browser_type, browser_evaluate, browser_switch_tab, browser_close_tab,
	// browser_open_tab). Read-only calls (browser_list_tabs,
	// browser_screenshot, browser_get_text, browser_wait) are NOT recorded per
	// call. Fields: {workspace_id, browsing_key, tab_owner, host}; the acting
	// agent is Entry.AgentID and the tool is Entry.Tool.
	//
	// PER ACTION, NOT PER FIRST USE. ADR D2.11 rejects first-use-only auditing
	// by name: an event on first use of a context an agent did not establish
	// fires once per agent per workspace and says nothing about the tenth
	// action, or about which agent made the purchase. That matters more now
	// that every agent on a workspace drives the operator's live logins.
	//
	// Same name-shape rule as EventBrowserInstanceCreated above.
	EventBrowserAction = "browser_action"

	// EventBrowserUploadFile — INFO (Decision=allow) or WARN (Decision=deny).
	// ONE event per browser_upload_file invocation, allowed OR denied (D2
	// FR-031). The denied half is the load-bearing half: a trail that records
	// only the successes cannot answer "did this agent try to hand a file it
	// was not allowed to reach to a page on the operator's logged-in session?",
	// which is the whole question the event exists for.
	//
	// Fields: {workspace_id, browsing_key, tab_owner, resolved_path,
	// page_origin, fs_op: "write", fs_op_reason, reason?, detail?}; the acting
	// agent is Entry.AgentID and Entry.Tool is "browser_upload_file".
	// resolved_path is the path AFTER ResolvePath, because an unresolved
	// relative path is not something an operator can act on.
	//
	// Same name-shape rule as EventBrowserInstanceCreated above: underscores,
	// never dots.
	EventBrowserUploadFile = "browser_upload_file"

	// EventBrowserSnapshot — INFO. ONE metadata-only event per
	// browser_snapshot capture (D2 FR-028). METADATA ONLY, and that is a
	// requirement rather than an economy: browser_snapshot renders field
	// VALUES by operator ruling, so an audit row carrying the captured text
	// would copy every password and card number the page held into a file
	// whose whole purpose is to be kept and read later.
	//
	// Fields: {workspace_id, browsing_key, tab_owner, page_origin, node_count,
	// output_bytes, value_nodes_emitted, truncated}. Never the values.
	//
	// Same name-shape rule as EventBrowserInstanceCreated above.
	EventBrowserSnapshot = "browser_snapshot"

	// EventBrowserLiveControlTaken — INFO (Decision=allow) or WARN
	// (Decision=deny). A /api/v1/browser/ws viewer requested interactive
	// control of an agent's live browser (ADR-038 D6). Decision=allow means
	// the viewer now holds the control lock; Decision=deny means the request
	// was refused (another viewer already controls it, or
	// tools.browser.take_control_enabled=false). This is the remote-control
	// surface ADR-038 flags as needing an audit trail.
	EventBrowserLiveControlTaken = "browser.live.control_taken"

	// EventBrowserLiveControlReleased — INFO. A viewer released (explicitly,
	// or implicitly via detach/disconnect) interactive control of an agent's
	// live browser (ADR-038 D6).
	EventBrowserLiveControlReleased = "browser.live.control_released"

	// EventChannelRoutingDriftDrop — WARN. A workspace-bound channel instance's
	// configured agent is unresolvable (deleted or a worker): the
	// inbound message is dropped rather than silently degraded to the global
	// default (ADR-029 FR-012, FR-028). Fields emitted by the caller:
	// {instance_id, workspace_id, intended_agent_id, chat_id, reason}.
	EventChannelRoutingDriftDrop = "channel.routing.drift_drop"

	// EventChannelRoutingChanged — INFO. An operator updated the workspace
	// binding or routing configuration of a channel instance via the REST API
	// (ADR-029 FR-029). Emitted by pkg/gateway/rest.go's channel routing PUT
	// handler. Fields: {channel_id, workspace_id, agent_id, flow}.
	EventChannelRoutingChanged = "channel.routing.changed"

	// EventChannelInstanceDeleted — INFO. An operator deleted a channel instance
	// via DELETE /api/v1/channels/{id} (ADR-029 FR-025). Emitted by
	// pkg/gateway/rest.go's deleteChannelInstance handler after the config entry,
	// credential refs, and per-instance state directory have been torn down.
	// Deleting an instance is a destructive, admin-only, security-relevant action
	// (it revokes stored encrypted credentials against the operator's explicit
	// intent), so it MUST leave an audit trail even on the happy path. Fields:
	// {channel_id, type, cleanup_failed}. cleanup_failed=true means at least one
	// credential blob could not be removed and is now orphaned in the store — the
	// operator asked to delete the credential but it remains encrypted at rest.
	EventChannelInstanceDeleted = "channel.instance.deleted"

	// EventBrowserWebRTCStreamStarted — INFO. A per-agent WebRTC capture
	// session's encoder page was successfully started (the FIRST
	// WebRTC-capable viewer offer for that agent — ADR-047 D2, wave-plan
	// W2-A). Fields: {agent_id, session_id}.
	EventBrowserWebRTCStreamStarted = "browser.webrtc.stream_started"

	// EventBrowserWebRTCStreamStopped — INFO. A per-agent WebRTC capture
	// session fully stopped (last-viewer grace timer, browser death, or
	// manager shutdown — ADR-047 D2/D3). Fields: {agent_id}.
	EventBrowserWebRTCStreamStopped = "browser.webrtc.stream_stopped"

	// EventBrowserWebRTCStreamStartFailed — WARN. A per-agent WebRTC capture
	// session's encoder page failed to start (cs.Start returned an error —
	// e.g. the managed Chrome could not be launched/reached, or the encoder
	// page failed to navigate). Distinct from EventBrowserWebRTCStreamStarted
	// (which is INFO-only and fires exclusively on success) so a start
	// failure never has to reuse a "success" event name at WARN severity —
	// fix-wave BE finding: the two outcomes are semantically different and
	// deserve different event names for SIEM routing. The gateway also calls
	// CaptureSession.Stop() on this path so the failed session is cleared
	// and the next viewer offer builds a fresh one (see
	// EventBrowserWebRTCStreamStopped, which fires immediately after via the
	// Stop()->onStopped hook). Fields: {agent_id, session_id, error}.
	EventBrowserWebRTCStreamStartFailed = "browser.webrtc.stream_start_failed"

	// EventBrowserWarmUpFailed — WARN. The boot-time shared-Chrome warm-up
	// (tools.browser.warm_at_boot, default true) failed or panicked. NOT
	// fatal: warm-up is best-effort by contract (Hard Constraint #4) and
	// Chrome still launches lazily at the first browser tool call — this
	// records that the operator paid for that lazy cold start (ADR-042:
	// historically ~30-60s on a fresh install) instead of a warm one.
	//
	// Exists because every other browser-lifecycle failure in this package is
	// auditable (StreamStartFailed, IngestAuthRejected, ViewerOfferFailed)
	// while warm-up was log-only, so an operator reconstructing "did Chrome
	// come up at boot?" from the audit trail found nothing at all — silence
	// that is indistinguishable from "warm-up was disabled" and from
	// "warm-up succeeded". Fields: {reason} where reason is "error" or
	// "panic", plus {error}.
	EventBrowserWarmUpFailed = "browser.warm_up_failed"

	// EventBrowserWebRTCIngestAuthRejected — WARN. A connection to the
	// loopback-only /api/v1/browser/capture-ingest endpoint was rejected:
	// either the RemoteAddr was not loopback, or the first frame's
	// browser_capture_hello token did not match any active CaptureSession's
	// minted token (ADR-047 D6: "loopback is NOT a trust boundary ... the
	// gateway audits any hello with a missing/invalid/expired token as a
	// rejected ingest-auth attempt"). Fields: {remote_addr, reason}.
	EventBrowserWebRTCIngestAuthRejected = "browser.webrtc.ingest_auth_rejected"

	// EventBrowserWebRTCViewerOfferFailed — WARN. A viewer's
	// browser_webrtc_offer was accepted (the capability gate passed and the
	// agent's CaptureSession started/already existed) but
	// CaptureSession.HandleViewerOffer itself failed — distinct from
	// EventBrowserWebRTCStreamStartFailed (which is specifically cs.Start()
	// failing to bring the encoder page up at all). Added 2026-07-28: before
	// this event existed, a failed viewer offer was visible only as an
	// unstructured slog.Warn line (pkg/gateway/browser_webrtc.go's
	// handleWebRTCOffer) with no audit trail and no way to distinguish a
	// genuinely transient ingest-track race from a real defect without
	// reading raw logs. reason classifies the failure: "ingest_timeout" when
	// the underlying error is webrtc.ErrNoIngestVideoTrack (waitForTracks
	// gave up before the encoder's video track arrived — see that sentinel's
	// doc comment for the incident this closes), "error" for every other
	// HandleViewerOffer failure (bad SDP, closed session, PC/track
	// negotiation). Fields: {agent_id, session_id, viewer_id, reason, error}.
	EventBrowserWebRTCViewerOfferFailed = "browser.webrtc.viewer_offer_failed"

	// EventChannelInstanceConfigured — INFO. An operator created or updated a
	// channel instance's configuration via PUT /api/v1/channels/{id}/configure
	// (SEC-23 / #289). Emitted by pkg/gateway/rest.go's configureChannel handler
	// after the config write (and any credential store writes/clears) have
	// committed. A configure call can create, rotate, or clear stored encrypted
	// secrets and change arbitrary instance fields, so — like
	// EventChannelInstanceDeleted — it MUST leave an audit trail even on the
	// happy path. Fields: {channel_id, type, fields, cleanup_failed}.
	// fields is the set of top-level config keys touched by the request (names
	// only — never values, to avoid leaking secrets into the audit log).
	// cleanup_failed=true means a cleared secret's stored credential could not
	// be removed and is now orphaned in the store (matches its sibling event
	// EventChannelInstanceDeleted's field name for the same concept).
	EventChannelInstanceConfigured = "channel.instance.configured"

	// EventMediaDelete — INFO. A workspace library file was explicitly deleted
	// by an operator (FR-008). Emitted by pkg/media/library/library.go's
	// Delete method (the per-file delete API surface; the REST handler will
	// pass through to this surface in a later slice). Fields: {actor,
	// workspace_id, media_id, filename, bytes_freed, mime, sha256}. Matches
	// the workspace.delete precedent in shape (Details carries the
	// event-specific fields; the top-level `event` field IS the FR-033
	// "action" discriminator).
	EventMediaDelete = "media.delete"

	// EventMediaCascadeDelete — INFO. A workspace's media library was
	// cascade-deleted as part of workspace deletion (FR-009). Emitted by
	// pkg/workspace/media_delete.go's WorkspaceDeleteHook after the library's
	// CascadeDelete() returned the deleted-entry summary. ONE event per
	// cascade operation (NOT one per file) — the spec calls for a list of
	// media_ids and filenames. Fields: {actor, workspace_id, media_ids,
	// filenames, bytes_freed, count}. Same shape convention as
	// EventMediaDelete / workspace.delete.
	EventMediaCascadeDelete = "media.cascade_delete"
)

// ---------------------------------------------------------------------------
// Severity constants. These are NOT slog levels — they are Omnipus audit
// severities, used as a top-level field on each emitted JSONL record so
// SIEM rules can route on `.severity` without inferring from the event name.
// ---------------------------------------------------------------------------

// Severity is the Omnipus audit severity classification.
type Severity string

const (
	// SeverityInfo — routine, expected events that operators want a record of.
	SeverityInfo Severity = "INFO"

	// SeverityWarn — unexpected but recoverable; operator attention recommended.
	SeverityWarn Severity = "WARN"

	// SeverityHigh — security-relevant or correctness-critical; operator
	// attention required. Includes any path that aborts boot.
	SeverityHigh Severity = "HIGH"
)

// ---------------------------------------------------------------------------
// Ask-deny reason enum (FR-047, FR-065). Exhaustive list — any caller that
// emits `tool.policy.ask.denied` MUST use one of these constants. The
// helper `IsValidAskDenyReason` is exposed for boundary validation.
// ---------------------------------------------------------------------------

// AskDenyReason is the discriminator on `tool.policy.ask.denied` events.
type AskDenyReason string

const (
	// AskDenyReasonUser — user clicked "deny" in the SPA approval modal.
	AskDenyReasonUser AskDenyReason = "user"

	// AskDenyReasonTimeout — `gateway.tool_approval_timeout` elapsed before
	// any approve/deny/cancel action arrived.
	AskDenyReasonTimeout AskDenyReason = "timeout"

	// AskDenyReasonCancel — user (or another client) clicked "cancel"
	// (third action on the approval endpoint, FR-017).
	AskDenyReasonCancel AskDenyReason = "cancel"

	// AskDenyReasonRestart — the gateway restarted while the approval was
	// pending (FR-013). Emitted from the next-boot recovery path AND the
	// graceful-shutdown path (FR-048, FR-069).
	AskDenyReasonRestart AskDenyReason = "restart"

	// AskDenyReasonSaturated — the pending-approval cap
	// (`gateway.tool_approval_max_pending`) was reached; the new ask was
	// synthetically denied without ever emitting a WS approval event.
	// FR-016.
	AskDenyReasonSaturated AskDenyReason = "saturated"

	// AskDenyReasonBatchShortCircuit — a prior call in the same sequential
	// ask batch was denied or canceled, so this and every subsequent
	// sibling call is auto-denied. FR-065.
	AskDenyReasonBatchShortCircuit AskDenyReason = "batch_short_circuit"

	// AskDenyReasonScheduled — the run is a headless cron/scheduled run with
	// no operator present to approve. Any `ask`-policy tool is auto-denied
	// immediately so the run never stalls waiting for human approval that can
	// never arrive (F-13, FR-009, O-3).
	//
	// The tool.policy.ask.denied entry (emitted via EmitToolPolicyAskDenied) does
	// NOT carry schedule_job_id or schedule_job_name — EmitToolPolicyAskDenied has
	// a fixed field set without them. The schedule identity rides on the companion
	// tool.policy.deny.attempted entry (via emitPolicyDenyAudit's extra Details
	// map) and on the structured INFO log line in emitScheduledAutoDenyAudit.
	AskDenyReasonScheduled AskDenyReason = "scheduled"
)

// IsValidAskDenyReason reports whether `r` is one of the known enum values
// defined by FR-047 + FR-065 + F-13. Useful at API boundaries before logging.
func IsValidAskDenyReason(r AskDenyReason) bool {
	switch r {
	case AskDenyReasonUser,
		AskDenyReasonTimeout,
		AskDenyReasonCancel,
		AskDenyReasonRestart,
		AskDenyReasonSaturated,
		AskDenyReasonBatchShortCircuit,
		AskDenyReasonScheduled:
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// `conflict_with` enum for `tool.collision.mcp_rejected` (FR-034, FR-060).
// ---------------------------------------------------------------------------

const (
	// ConflictWithBuiltin — incoming MCP tool collided with an existing
	// builtin name; builtin wins (FR-034).
	ConflictWithBuiltin = "builtin"

	// ConflictWithReservedPrefix — incoming MCP tool name begins with
	// `system.`; the central registry reserves that prefix exclusively
	// for builtins (FR-015, FR-060).
	ConflictWithReservedPrefix = "reserved_prefix"
	// ConflictWithMCPPrefix is the prefix for the discriminator value when
	// the conflict is with another MCP server. The full value is
	// `mcp:<server_id>` (e.g. `mcp:srv-A`). The prefix exists so SIEM rules
	// can match all MCP-vs-MCP collisions with one regex.
	ConflictWithMCPPrefix = "mcp:"
)

// ---------------------------------------------------------------------------
// Generic emitter for the new structured-record events.
//
// Every event type here writes a flat JSONL record to the audit logger
// using the same wire shape:
//
//	{
//	  "timestamp": "<RFC3339Nano UTC>",
//	  "event":     "<EventXxx>",
//	  "severity":  "<Severity>",
//	  "fields":    { ... event-specific ... }
//	}
//
// We do NOT reuse the `Entry` struct because the Tool Registry events have
// substantially different field sets from the existing tool_call/exec
// schema, and forcing them through `Parameters`/`Details` would discard
// type information that downstream consumers need (e.g. `args_hash`,
// `canceled_tool_call_ids`, `latency_ms`).
//
// Best-effort contract: emission failure logs to slog and returns nil.
// The audit subsystem MUST NOT block tool execution. Boot-abort paths
// (FR-063) use the dedicated stderr fallback in boot_abort.go.
// ---------------------------------------------------------------------------

// Record is the canonical wire shape for Tool Registry redesign audit
// events. `Fields` carries the per-event-type payload defined by the spec.
type Record struct {
	Timestamp string         `json:"timestamp"`
	Event     string         `json:"event"`
	Severity  Severity       `json:"severity"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// Emit writes one Record to the audit logger. `fields` is shallow-copied
// into the record so subsequent caller mutation cannot corrupt the
// already-flushed JSON. logger == nil is a no-op (audit disabled).
//
// `ctx` is reserved for future actor-extraction; today the function does
// not consult it (the events emitted here either have a synthetic / system
// actor, or carry the user-id explicitly in fields).
func Emit(ctx context.Context, logger *Logger, event string, sev Severity, fields map[string]any) {
	_ = ctx
	if logger == nil {
		return
	}
	if !IsValidSeverity(sev) {
		slog.Warn("audit: invalid severity, defaulting to WARN", "event", event, "severity", string(sev))
		sev = SeverityWarn
	}

	rec := Record{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Event:     event,
		Severity:  sev,
		Fields:    cloneFields(fields),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		slog.Error("audit: marshal record failed", "error", err, "event", event)
		return
	}
	// CRIT-5: structured Record emissions inherit the same fsync gate as
	// Logger.Log — High-severity events and the policy-deny event names go
	// to disk synchronously so they survive a crash. INFO/WARN allow rows
	// batch through bufio.
	fsyncRequired := sev == SeverityHigh ||
		event == EventToolPolicyDenyAttempted ||
		event == EventToolPolicyAskDenied ||
		event == EventBootAbort
	if writeErr := logger.writeLine(data, fsyncRequired); writeErr != nil {
		slog.Error("audit: write record failed", "error", writeErr, "event", event)
		// CRIT-6: bump the skipped counter so /health audit_degraded surfaces
		// this write failure. Mirror the contract of audit.EmitEntry: a wired
		// logger that fails to write is a runtime health signal, distinct from
		// audit being explicitly disabled (auditLogger==nil, skipped counter
		// must NOT be bumped). The event name is passed as the tool label;
		// however IncSkipped only has a dedicated bucket for "web_serve" —
		// all other values (including event names) aggregate into the single
		// "other" counter, and the decision argument is ignored for non-web_serve
		// calls. There is no per-event-family breakout in /metrics today.
		IncSkipped(event, DecisionDeny)
	}
}

// IsValidSeverity reports whether `s` is one of the three declared severities.
func IsValidSeverity(s Severity) bool {
	switch s {
	case SeverityInfo, SeverityWarn, SeverityHigh:
		return true
	}
	return false
}

// cloneFields returns a shallow copy of m so the caller cannot mutate the
// emitted record after Emit returns. Returns nil for nil input (omits the
// `fields` JSON key in that case).
func cloneFields(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ---------------------------------------------------------------------------
// Typed convenience emitters. Each of the 13 events listed above has a
// dedicated function that enforces the field contract from the spec
// at compile time. Call sites SHOULD prefer these over raw Emit() so that
// renaming a field is a single-edit operation.
// ---------------------------------------------------------------------------

// EmitToolPolicyDenyAttempted — WARN. FR-079; spec table.
//
// `note` is optional; the spec mandates the literal string "mid_turn_policy_change"
// when the re-check (FR-079) observes a flip from the filter snapshot.
func EmitToolPolicyDenyAttempted(
	ctx context.Context,
	logger *Logger,
	agentID, toolName, source, sessionID, turnID, toolCallID, note string,
) {
	fields := map[string]any{
		"agent_id":     agentID,
		"tool_name":    toolName,
		"source":       source, // "global" | "agent"
		"session_id":   sessionID,
		"turn_id":      turnID,
		"tool_call_id": toolCallID,
	}
	if note != "" {
		fields["note"] = note
	}
	Emit(ctx, logger, EventToolPolicyDenyAttempted, SeverityWarn, fields)
}

// EmitToolPolicyAskRequested — INFO. FR-011, FR-074, FR-080.
//
// `args` is hashed via ArgsHash and previewed via ArgsPreview before
// emission so callers do not have to remember to redact.
func EmitToolPolicyAskRequested(
	ctx context.Context,
	logger *Logger,
	approvalID, toolCallID, toolName, agentID, sessionID, turnID string,
	args map[string]any,
) {
	hash, hashErr := ArgsHash(args)
	if hashErr != nil {
		// A hashing failure means args_hash below will be empty — log it so
		// the gap in the audit trail is visible rather than silent.
		slog.Error("audit: failed to hash tool args for audit event",
			"approval_id", approvalID, "tool_call_id", toolCallID, "error", hashErr)
	}
	fields := map[string]any{
		"approval_id":  approvalID,
		"tool_call_id": toolCallID,
		"tool_name":    toolName,
		"agent_id":     agentID,
		"session_id":   sessionID,
		"turn_id":      turnID,
		"args_hash":    hash,
		"args_preview": ArgsPreview(args),
	}
	Emit(ctx, logger, EventToolPolicyAskRequested, SeverityInfo, fields)
}

// EmitToolPolicyAskGranted — INFO. FR-054, FR-074.
func EmitToolPolicyAskGranted(
	ctx context.Context,
	logger *Logger,
	approvalID, approverUserID, toolName, agentID, sessionID, turnID string,
	latencyMS int64,
	argsHash string,
) {
	fields := map[string]any{
		"approval_id":      approvalID,
		"approver_user_id": approverUserID,
		"tool_name":        toolName,
		"agent_id":         agentID,
		"session_id":       sessionID,
		"turn_id":          turnID,
		"latency_ms":       latencyMS,
		"args_hash":        argsHash,
	}
	Emit(ctx, logger, EventToolPolicyAskGranted, SeverityInfo, fields)
}

// EmitToolPolicyAskDenied — INFO. FR-047, FR-054, FR-074, FR-065.
//
// For batch short-circuit denies (FR-065) the caller passes the list of
// canceled tool_call_ids in `cancelledToolCallIDs`; pass nil for non-batch
// denies. `approverUserID` is "" for system-deny paths (timeout, restart,
// saturated, batch_short_circuit).
func EmitToolPolicyAskDenied(
	ctx context.Context,
	logger *Logger,
	approvalID, approverUserID, toolName, agentID, sessionID, turnID string,
	reason AskDenyReason,
	argsHash string,
	cancelledToolCallIDs []string,
) {
	if !IsValidAskDenyReason(reason) {
		slog.Error("audit: invalid AskDenyReason, refusing to emit",
			"approval_id", approvalID, "reason", string(reason))
		return
	}
	fields := map[string]any{
		"approval_id":      approvalID,
		"approver_user_id": approverUserID,
		"tool_name":        toolName,
		"agent_id":         agentID,
		"session_id":       sessionID,
		"turn_id":          turnID,
		"reason":           string(reason),
		"args_hash":        argsHash,
	}
	if len(cancelledToolCallIDs) > 0 {
		fields["canceled_tool_call_ids"] = cancelledToolCallIDs
	}
	Emit(ctx, logger, EventToolPolicyAskDenied, SeverityInfo, fields)
}

// EmitToolCollisionMCPRejected — WARN. FR-034, FR-060.
//
// `conflictWith` is one of the ConflictWith* constants above, OR
// `ConflictWithMCPPrefix + "<server_id>"` for MCP-vs-MCP collisions.
func EmitToolCollisionMCPRejected(ctx context.Context, logger *Logger, mcpServerID, toolName, conflictWith string) {
	Emit(ctx, logger, EventToolCollisionMCPRejected, SeverityWarn, map[string]any{
		"mcp_server_id": mcpServerID,
		"tool_name":     toolName,
		"conflict_with": conflictWith,
	})
}

// EmitAgentConfigCorrupt — HIGH. FR-023.
//
// `agentType` is one of {"core", "custom"}. The boot validator decides
// the skip-or-abort disposition by consulting the constructor-seed
// disposition map (FR-062), not by `agentType` alone.
func EmitAgentConfigCorrupt(ctx context.Context, logger *Logger, agentID, agentType, path string, err error) {
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	Emit(ctx, logger, EventAgentConfigCorrupt, SeverityHigh, map[string]any{
		"agent_id":   agentID,
		"agent_type": agentType,
		"path":       path,
		"error":      errMsg,
	})
}

// EmitAgentConfigInvalidPolicyValue — HIGH. FR-049, FR-085.
//
// `entries` lists each invalid entry (e.g. `policies["bash"]="banana"`);
// the slice form is preserved so multiple invalid values in one config
// emit one event, not N.
func EmitAgentConfigInvalidPolicyValue(
	ctx context.Context,
	logger *Logger,
	agentID, agentType, path string,
	entries []InvalidPolicyEntry,
) {
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"field":  e.Field,
			"value":  e.Value,
			"reason": e.Reason,
		})
	}
	Emit(ctx, logger, EventAgentConfigInvalidPolicyValue, SeverityHigh, map[string]any{
		"agent_id":   agentID,
		"agent_type": agentType,
		"path":       path,
		"entries":    out,
	})
}

// InvalidPolicyEntry describes one bad value found by the boot validator.
// `Field` is a JSON-pointer-ish path (e.g. `policies["set_config"]`);
// `Value` is the raw string the operator wrote; `Reason` is a short human
// explanation.
type InvalidPolicyEntry struct {
	Field  string
	Value  string
	Reason string
}

// EmitAgentConfigUnknownToolInPolicy — WARN. FR-057.
func EmitAgentConfigUnknownToolInPolicy(ctx context.Context, logger *Logger, agentID, path string, toolNames []string) {
	Emit(ctx, logger, EventAgentConfigUnknownToolInPolicy, SeverityWarn, map[string]any{
		"agent_id":   agentID,
		"path":       path,
		"tool_names": toolNames,
	})
}

// EmitToolAssemblyDuplicateName — HIGH. FR-066.
//
// `sources` is the ordered list of sources observed for the colliding name,
// e.g. ["builtin", "mcp:srv-A"]. `kept` is the source whose entry survived
// the dedup pass per FR-034 precedence (builtin > first-MCP).
func EmitToolAssemblyDuplicateName(
	ctx context.Context,
	logger *Logger,
	toolName string,
	sources []string,
	kept string,
) {
	Emit(ctx, logger, EventToolAssemblyDuplicateName, SeverityHigh, map[string]any{
		"tool_name": toolName,
		"sources":   sources,
		"kept":      kept,
	})
}

// EmitMCPServerRenamed — HIGH. FR-051, FR-068, FR-083.
func EmitMCPServerRenamed(ctx context.Context, logger *Logger, oldName, newName, transportType, endpointURL string) {
	Emit(ctx, logger, EventMCPServerRenamed, SeverityHigh, map[string]any{
		"old_name":       oldName,
		"new_name":       newName,
		"transport_type": transportType,
		"endpoint_url":   endpointURL,
	})
}

// EmitGatewayStartupGuardDisabled — WARN. FR-016.
//
// Emitted exactly once at boot when `gateway.tool_approval_max_pending == 0`
// (sentinel "unlimited").
func EmitGatewayStartupGuardDisabled(ctx context.Context, logger *Logger, configKey string) {
	Emit(ctx, logger, EventGatewayStartupGuardDisabled, SeverityWarn, map[string]any{
		"config_key": configKey,
		"message":    "approval saturation guard disabled — DoS risk",
	})
}

// EmitGatewayConfigInvalidValue — HIGH. FR-016.
//
// Emitted when boot validation observes a negative cap or another invalid
// gateway config value. The gateway exits non-zero immediately after.
func EmitGatewayConfigInvalidValue(ctx context.Context, logger *Logger, configKey, value, reason string) {
	Emit(ctx, logger, EventGatewayConfigInvalidValue, SeverityHigh, map[string]any{
		"config_key": configKey,
		"value":      value,
		"reason":     reason,
	})
}
