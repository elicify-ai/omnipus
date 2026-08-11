// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-058 (tool-denial semantics): this file is the sole source of denial
// classification and the per-turn denial ledger.
//
// The defect this exists to close (ADR-058 §1): every approval denial that
// reached the model was rendered as `"User denied tool execution."`,
// regardless of whether a human denied anything — a `timeout` (nobody
// answered) and a `saturated` queue got the identical false sentence as a
// genuine human "no". The model then retried reasonably on false
// information, and nothing bounded the retry. One UAT task looped for 13+
// minutes and needed a manual Stop.
//
// ClassifyDenial is the one place that decides whether a denial reason is
// PERMANENT (never worth retrying within this turn) or RETRYABLE
// (`saturated` is the only one), and what the model and the transcript are
// each told about it. denialPayloadJSON is the one place the wire form of
// that classification is built. The turnDenialLedger + turnState methods
// below are the per-turn bookkeeping: an aggregate budget across every
// denial the model has been handed, and a quarantine cache so a tool that
// has already produced one permanent denial is never re-asked about in the
// same turn.
package agent

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// turnDenialBudget is the aggregate, per-turn ceiling on tool-call denial
// responses a turn may accumulate — real or served from the quarantine
// cache, regardless of tool or reason — before the turn is aborted
// (ADR-058 D4, amended by §10.A2). It is a bare constant, not a config
// field: FR-084 (`turn_synthetic_error_floor`) shipped with an "unset means
// disabled" sentinel that no operator ever triggered on purpose, and the
// fix for that (this spec, §3.2) is deletion, not a repeat of the same
// mistake. A bare const has no unset state to invert.
const turnDenialBudget = 10

// denialUserMarker is the lower-cased substring that FR-058-06 requires to
// appear in exactly one message string in pkg/agent after this change:
// DenialMessageUser below. Tests assert its absence from every other
// denial message and its presence only when the classified reason is
// literally "user".
const denialUserMarker = "user denied"

// DenialMessageUser is the model-facing message for a denial whose reason
// is a genuine human "no" (reason "user"). Exported so tests in this
// package and in pkg/gateway can assert against the exact pinned literal
// (ADR-058 spec §4.2) rather than a paraphrase.
const DenialMessageUser = "The user denied this tool call. Do not retry it; stop and report the blocker."

// autoDenyHeadlessReason is the fixed loop-side pseudo-reason for the
// headless auto-deny path (FR-009/#264, loop.go's runTurn AutoDenyAsk
// branch): a scheduled run has no operator to approve an ask-policy tool
// call, for the whole run, so it is denied without ever opening an
// approval request. Declared once here — rather than as a local const at
// the loop.go call site — so denialTable's row and the call site that
// classifies against it can never drift onto two different literals.
const autoDenyHeadlessReason = "auto-denied: ask-policy tool not allowed in a headless scheduled run"

// DenialClass is the single classification of one tool-call denial reason:
// whether it is Permanent (never worth retrying within this turn) and the
// two rendered strings for the model (ModelMessage) and the transcript
// (TranscriptText). ClassifyDenial is the sole source of denial semantics
// (FR-058-01) — no other switch on a denial reason string may exist
// elsewhere in pkg/agent.
type DenialClass struct {
	Reason         string
	Permanent      bool
	ModelMessage   string
	TranscriptText string
}

// unknownReasonText is the byte-identical string used for BOTH
// DenialClass.ModelMessage and DenialClass.TranscriptText when a reason has
// no table row (FR-058-03). Byte-identical on both surfaces is deliberate:
// an unrecognised reason gets the same conservative, honest treatment
// everywhere rather than a divergent guess on either surface.
func unknownReasonText(reason string) string {
	return fmt.Sprintf(
		"Not run: the tool call was refused (reason: %s). Treat this as permanent; do not retry — stop and report the blocker.",
		reason,
	)
}

// denialTable is the denial classification (ADR-058 spec §4.1's ten rows,
// plus two more rows added post-epic-review — the headless auto-deny
// literal, autoDenyHeadlessReason, and "session canceled" — for reasons the
// original spec table missed; see each row's own comment). Exactly one row
// ("saturated") is Permanent: false — every other denial reason the system
// can produce is treated as terminal for the rest of the turn. "approved"
// is deliberately ABSENT: pkg/gateway/approvals.go emits it with
// Approved: true, so it is not a denial at all (spec §2.2); a classifier
// asserting it "known" would be asserting a lie.
//
// Wording for rows the ADR gives verbatim (user, timeout, saturated,
// no_approver_configured, policy_denied) matches ADR-058 D2's illustrative
// table and, for TranscriptText, the pre-existing askDenialText switch
// (approval_transcript.go) byte-for-byte — W2 deletes that switch and
// delegates to this table, so its rendered output must not change for the
// reasons it already handled. The "" row's TranscriptText is pinned
// verbatim by spec N1. Remaining rows' wording is free within two
// invariants (spec §4.2): TranscriptText keeps the existing "Not run: …"
// reader-facing form, and ModelMessage names the productive next step
// (ADR D2/D3) — stop and report the blocker, not retry.
var denialTable = map[string]DenialClass{
	"user": {
		Reason:         "user",
		Permanent:      true,
		ModelMessage:   DenialMessageUser,
		TranscriptText: "Not run: the approval request was denied.",
	},
	"timeout": {
		Reason:    "timeout",
		Permanent: true,
		ModelMessage: "The approval request expired with no answer — nobody is available to approve it. " +
			"Do not retry; stop and report the blocker.",
		TranscriptText: "Not run: the approval request expired before anyone answered it.",
	},
	"saturated": {
		Reason:         "saturated",
		Permanent:      false,
		ModelMessage:   "Too many approval requests were already pending. This one may be retried later.",
		TranscriptText: "Not run: too many approval requests were already pending.",
	},
	"cancel": {
		Reason:    "cancel",
		Permanent: true,
		ModelMessage: "The turn was cancelled while this approval request was open. " +
			"Do not retry; stop and report the blocker.",
		TranscriptText: "Not run: the turn was cancelled while the approval request was open.",
	},
	// "session canceled" (two words — distinct from the single-word
	// "cancel" row above) is a SEPARATE reason literal, reachable end to
	// end via pkg/agent/cancel.go::AgentLoop.RequestCancel ->
	// hooks.CancelPendingApprovals -> pkg/gateway/approvals.go's
	// denialReasonSessionCanceled constant, passed to
	// cancelAllPendingForSessions when a whole session (not just one
	// in-flight turn) is cancelled. An earlier revision of a nearby loop.go
	// comment enumerated ClassifyDenial's coverage without this reason at
	// all; verified end-to-end and added here so it renders as a known,
	// honestly-worded denial rather than falling through to the generic
	// unknown-reason fallback.
	"session canceled": {
		Reason:    "session canceled",
		Permanent: true,
		ModelMessage: "The session was cancelled, which auto-denied this pending approval request. " +
			"Do not retry; stop and report the blocker.",
		TranscriptText: "Not run: the session was cancelled, which auto-denied this pending approval request.",
	},
	"restart": {
		Reason:    "restart",
		Permanent: true,
		ModelMessage: "The gateway is shutting down, which cancelled this approval request. " +
			"Do not retry within this turn; stop and report the blocker.",
		TranscriptText: "Not run: the gateway was shutting down when the approval request was cancelled.",
	},
	"batch_short_circuit": {
		Reason:    "batch_short_circuit",
		Permanent: true,
		ModelMessage: "A prior call in this batch was denied or cancelled, so this call was never sent for approval. " +
			"Do not retry; stop and report the blocker.",
		TranscriptText: "Not run: a prior call in the same batch was denied or canceled.",
	},
	"internal_error": {
		Reason:    "internal_error",
		Permanent: true,
		ModelMessage: "An internal error prevented this approval request from being processed. " +
			"Do not retry; report this as a system fault.",
		TranscriptText: "Not run: an internal error prevented the approval request from being processed.",
	},
	"no_approver_configured": {
		Reason:    "no_approver_configured",
		Permanent: true,
		ModelMessage: "No approval mechanism is configured on this gateway. " +
			"Do not retry; report this as a configuration blocker.",
		TranscriptText: "Not run: no approval mechanism is configured on this gateway.",
	},
	// policy_denied is the loop-side pseudo-reason for site 1 (TOCTOU
	// policy flip to deny), which today emits no "reason" field at all.
	// ModelMessage is unchanged from the existing literal ("already true"
	// per ADR D2) — this row exists so site 1 can be uniformly rewired
	// through denialPayloadJSON while keeping that literal unchanged.
	// (An earlier revision of this comment overstated the result as
	// "without changing what the model reads" — that is true only of the
	// message TEXT; the payload as a whole now ALSO carries "reason":
	// "policy_denied" and "permanent":true, both absent before this
	// change, since site 1 previously emitted no reason field at all.)
	//
	// This row's Permanent:true is the correct MESSAGE classification (a
	// mid-turn policy flip to deny is, from the model's point of view,
	// not worth retrying) but loop.go's site 1 deliberately does NOT pass
	// it through to recordToolDenial's quarantine-controlling argument —
	// see that call site's own comment. FR-079's TOCTOU re-check exists
	// precisely because policy can change again mid-turn, and quarantining
	// this reason would silently disable that re-check for the rest of
	// the turn: a later deny→allow flip would be served a stale cached
	// denial with no re-resolution at all. The classification here stays
	// Permanent:true (the model is still told not to retry); only the
	// system's own internal quarantine behaviour is excluded.
	"policy_denied": {
		Reason:         "policy_denied",
		Permanent:      true,
		ModelMessage:   "Tool execution denied by policy.",
		TranscriptText: "Not run: tool execution was denied by policy.",
	},
	// autoDenyHeadlessReason (FR-009/#264): a scheduled run has no operator
	// by construction, for the entire run, so any ask-policy tool is
	// denied without ever opening an approval request. This literal used
	// to have NO dedicated row here — it rode ClassifyDenial's
	// unknown-reason fallback, producing a stuttering message ("the tool
	// call was refused (reason: auto-denied: ask-policy tool not allowed
	// in a headless scheduled run)") with no headless-specific guidance,
	// and failing AC-01's "every driven reason must be known" guard (spec
	// §8, ADR-058 W1's own "including the fixed-literal headless reason"
	// requirement). Its ModelMessage/TranscriptText below are worded
	// specifically for this case rather than the generic unknown-reason
	// template.
	autoDenyHeadlessReason: {
		Reason:    autoDenyHeadlessReason,
		Permanent: true,
		ModelMessage: "This is a headless scheduled run with no operator available to approve " +
			"ask-policy tools. Do not retry; stop and report the blocker.",
		TranscriptText: "Not run: this is a headless scheduled run with no operator available " +
			"to approve ask-policy tools.",
	},
	// "" is the empty-reason row askDenialText already handles today. Its
	// TranscriptText is preserved verbatim (spec N1); reachability of this
	// row in production is unverified but the row must exist so a caller
	// that has genuinely no reason still gets a classified, honest answer
	// rather than falling through to the unknown-reason path.
	"": {
		Reason:         "",
		Permanent:      true,
		ModelMessage:   "The approval request was not granted, and no reason was given. Treat this as permanent; do not retry — stop and report the blocker.",
		TranscriptText: "Not run: the approval request was not granted.",
	},
}

// ClassifyDenial is the sole source of denial semantics (FR-058-01). It
// looks reason up in the classification table (denialTable) and reports
// whether it was found (known). An unrecognised reason — including
// "approved", which is not a denial at all — returns Permanent: true and a
// byte-identical ModelMessage/TranscriptText (FR-058-03), so a reason with
// no classification fails safe (treated as terminal) rather than defaulting
// to retryable.
func ClassifyDenial(reason string) (DenialClass, bool) {
	if cls, ok := denialTable[reason]; ok {
		return cls, true
	}
	text := unknownReasonText(reason)
	return DenialClass{
		Reason:         reason,
		Permanent:      true,
		ModelMessage:   text,
		TranscriptText: text,
	}, false
}

// denialPayloadJSON builds the single wire form of a permission_denied tool
// result the model receives (FR-058-05), shared by every loop.go emit site.
//
// reason is accepted as its own parameter rather than read off cls.Reason
// purely for call-site convenience — every caller already has the raw
// reason string in hand (it is what they passed to ClassifyDenial a line or
// two earlier) and cls.Reason is, by ClassifyDenial's own construction,
// ALWAYS equal to it: both the known-row branch (denialTable[reason].Reason
// is the map key itself) and the unknown-reason fallback set
// Reason: reason explicitly. So reason == cls.Reason holds for every value
// this function can be called with — there is no case where they can
// legitimately disagree. That also means the separate parameter is NOT a
// safeguard against disagreement (the opposite of what an earlier revision
// of this comment claimed): reading cls.Reason internally instead would
// make disagreement structurally IMPOSSIBLE, whereas keeping reason as an
// independent parameter is the only way a future caller could ever
// introduce one (by passing a reason that does not match the cls it
// classified). Callers MUST pass the same reason string they gave to
// ClassifyDenial.
//
// Builds via tools.PermissionDeniedPayload (contracts/asyncapi.yaml's
// PermissionDenied schema), the ONE producer this function shares with
// pkg/tools' filesystem-scope denial path (fserrors.go's
// PermissionDeniedResult) — issue #618.
//
// This used to build the JSON by hand with fmt.Sprintf's %q verb, on the
// claim that %q's Go-string escaping "produces valid JSON string literals
// for the plain ASCII/control-character content every field here carries".
// That claim is false: %q emits \xNN for a control byte outside \n\t\r
// (e.g. NUL, 0x01, DEL), and \x is not a legal JSON escape — json.Unmarshal
// rejects it with "invalid character 'x' in string escape code". tool is
// narrower than the filesystem-scope side (closed reason enums keep
// message/reason short and fixed), but tool itself takes MCP-declared names
// that are not statically enumerable, so the failure mode is reachable here
// too, not just on the filesystem-scope side. encoding/json (via
// PermissionDeniedPayload -> marshalWithinBudget) escapes every control byte
// as \u00NN, which is always valid JSON, and also bounds the encoded size —
// this function had no length budget at all before this fix.
func denialPayloadJSON(tool, reason string, cls DenialClass) string {
	encoded, err := tools.PermissionDeniedPayload(tool, cls.ModelMessage, reason, cls.Permanent)
	if err != nil {
		// Fully static fallback — no caller-supplied content interpolated —
		// so a marshal failure (should not happen for this plain-string
		// payload) can never itself reintroduce the escaping bug this
		// function exists to close. Reported rather than swallowed: this
		// branch replaces the caller's real reason and tool with fixed text,
		// which is exactly the kind of substitution that should never happen
		// invisibly.
		tools.ReportStructuredFailureMarshalError("pkg/agent.denialPayloadJSON", tool, tools.PermissionDeniedCode, err)
		return `{"error":"permission_denied","message":"An internal error occurred while building the denial payload. Do not retry; stop and report the blocker.","tool":"unknown","reason":"internal_error","permanent":true}`
	}
	return string(encoded)
}

// toolDenialAbortReason formats the abort reason for a turn that has
// exhausted its aggregate tool-denial budget (FR-058-13). The format is
// pinned by spec §3.8/§9's shared contract — tests in this package and in
// pkg/agent/task_executor_tool_denial_test.go assert against it verbatim.
func toolDenialAbortReason(tool, reason, agentID string, budget int) string {
	return fmt.Sprintf(
		"blocked: tool %q denied (%s) for agent %q — the turn reached its limit of %d tool denials",
		tool, reason, agentID, budget,
	)
}

// quarantinedDenial is the cached payload for a tool that has already
// produced one PERMANENT denial in this turn (ADR-058 §10.A1). Every
// subsequent call to that tool in the same turn is answered from this
// cache: no hook call, no policy re-resolution, no
// CheckGrantOrRequestApproval, no RequestApproval, no
// tool_approval_required frame (FR-058-11).
type quarantinedDenial struct {
	reason  string
	payload string
}

// turnDenialLedger is the per-turn tool-denial bookkeeping (FR-058-09): an
// aggregate count of every denial response handed to the model in this
// turn — real or replayed from the quarantine cache — and a quarantine map
// keyed by TOOL NAME ALONE (ADR-058 §10.A5 / spec N4: the quarantine gate
// runs before a fresh attempt's own reason exists, so keying by (tool,
// reason) would be a shape mismatch with the gate that reads it).
//
// This type lives only as a field on turnState, which newTurnState
// constructs fresh for every turn. There is no package-level or
// cross-turn/cross-session state anywhere in this file: a turnDenialLedger's
// entire lifetime is bounded by the turnState that owns it.
type turnDenialLedger struct {
	used        int
	quarantined map[string]quarantinedDenial
}

// recordToolDenial records one denial response about to be handed to the
// model: it increments the aggregate per-turn budget and, if quarantine is
// true, quarantines tool on this — its FIRST — occurrence in the turn
// (FR-058-10). A later call for the same tool that is already quarantined
// does not overwrite the cached entry; the first quarantining denial's
// payload is what every replay serves for the rest of the turn.
//
// quarantine is NOT simply "the caller's ClassifyDenial(reason).Permanent"
// passed straight through, even though that is what every call site except
// one does. It is the narrower, system-level question "should this reason
// disable further checking of this tool for the rest of the turn" —
// distinct from Permanent's model-facing "should the model stop retrying"
// question, which is decided once, earlier, when the payload's message was
// built, and does not change here. The two normally coincide:
//
//   - false for "saturated" (ADR-058 §3.5 / Binding Rule 4, the positive
//     lower bound): a transient queue-full condition, so a later call to the
//     same tool in the same turn is free to reach the approver and execute.
//   - false for "policy_denied" as well (ADR-058 fix, post-epic-review),
//     DESPITE that reason's DenialClass.Permanent being true: FR-079's
//     TOCTOU re-check exists because policy can change again mid-turn, and
//     quarantining this reason would silently disable that re-check for the
//     rest of the turn — a later deny→allow flip would be served a stale
//     cached denial with no re-resolution. The model is still told
//     Permanent:true in the payload (retrying is still bad advice most of
//     the time); only this system-internal quarantine caching is skipped so
//     the TOCTOU check keeps running on every subsequent call.
//   - true for every other reason.
//
// Returns the new aggregate count and whether the turn has now exhausted
// its budget (used >= turnDenialBudget) — the caller aborts the turn on
// exhausted, it does not abort here, so the ledger stays a pure accounting
// mechanism with no control-flow side effects of its own.
//
// Takes ts.mu.Lock(), not RLock(): ts.mu is a sync.RWMutex shared by many
// unrelated turnState fields, and this method mutates turnDenialLedger —
// an RLock() here would let a concurrent RLock()-holding reader observe a
// torn write.
func (ts *turnState) recordToolDenial(tool, reason string, quarantine bool, payload string) (used int, exhausted bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	ts.denialLedger.used++
	if quarantine {
		if ts.denialLedger.quarantined == nil {
			ts.denialLedger.quarantined = make(map[string]quarantinedDenial)
		}
		if _, already := ts.denialLedger.quarantined[tool]; !already {
			ts.denialLedger.quarantined[tool] = quarantinedDenial{reason: reason, payload: payload}
		}
	}
	return ts.denialLedger.used, ts.denialLedger.used >= turnDenialBudget
}

// recordQuarantineReplay records one denial response served from the
// quarantine cache. It still consumes one unit of the aggregate per-turn
// budget — ADR-058 §10.A2 is explicit that counting cache-served replays is
// deliberate: without it, a model repeating a quarantined tool would be
// bounded only by MaxIterations (200), cheap in wall-clock but expensive in
// real LLM calls.
//
// tool is accepted for call-site symmetry with recordToolDenial and
// quarantinedDenialFor (the caller already has it in hand from resolving
// which tool's cache entry to replay) and to leave room for future
// per-tool diagnostics at this call site, but it is NOT consulted by this
// method today: replay accounting is purely aggregate (turnDenialLedger.used
// is a single counter, not keyed by tool), and this method never reads
// ts.denialLedger.quarantined — the caller is responsible for having
// already confirmed tool is quarantined (via quarantinedDenialFor) before
// calling this. An earlier revision of this comment implied per-tool
// bookkeeping happened here; it does not.
//
// Takes ts.mu.Lock() for the same reason as recordToolDenial: it mutates
// turnDenialLedger.used.
func (ts *turnState) recordQuarantineReplay(tool string) (used int, exhausted bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	ts.denialLedger.used++
	return ts.denialLedger.used, ts.denialLedger.used >= turnDenialBudget
}

// quarantinedDenialFor reports whether tool is already quarantined for this
// turn and, if so, the cached payload and the reason that quarantined it.
// The dispatch-loop quarantine gate (FR-058-11, wired by W4) calls this
// immediately after resolving toolName/toolArgs and before hooks.BeforeTool,
// the TOCTOU re-check, or any approval path — a hit here means none of
// those run for this call.
//
// A pure read of turnDenialLedger: takes ts.mu.RLock(), not Lock(), so
// concurrent lookups (e.g. across goroutines inspecting turn state) are not
// serialized against each other — only against the Lock()-held mutators
// above.
func (ts *turnState) quarantinedDenialFor(tool string) (payload, reason string, ok bool) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	qd, ok := ts.denialLedger.quarantined[tool]
	return qd.payload, qd.reason, ok
}
