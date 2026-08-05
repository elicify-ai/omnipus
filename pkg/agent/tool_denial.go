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

import "fmt"

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

// denialTable is the normative ten-row classification (ADR-058 spec §4.1).
// Exactly one row ("saturated") is Permanent: false — every other denial
// reason the system can produce is treated as terminal for the rest of the
// turn. "approved" is deliberately ABSENT: pkg/gateway/approvals.go emits
// it with Approved: true, so it is not a denial at all (spec §2.2); a
// classifier asserting it "known" would be asserting a lie.
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
	// through denialPayloadJSON without changing what the model reads.
	"policy_denied": {
		Reason:         "policy_denied",
		Permanent:      true,
		ModelMessage:   "Tool execution denied by policy.",
		TranscriptText: "Not run: tool execution was denied by policy.",
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
// looks reason up in the normative ten-row table (denialTable) and reports
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
// reason is passed separately from cls.Reason so a caller can never
// accidentally emit a payload whose "reason" field disagrees with the
// DenialClass it classified — callers MUST pass the same reason string they
// gave to ClassifyDenial.
//
// Uses fmt.Sprintf with %q (matching the pre-existing construction at the
// three loop.go call sites this replaces) rather than encoding/json: %q's
// Go-string escaping produces valid JSON string literals for the plain
// ASCII/control-character content every field here carries, and keeping
// the same mechanism as the code being replaced avoids introducing a new
// dependency for this one file.
func denialPayloadJSON(tool, reason string, cls DenialClass) string {
	return fmt.Sprintf(
		`{"error":"permission_denied","message":%q,"tool":%q,"reason":%q,"permanent":%t}`,
		cls.ModelMessage, tool, reason, cls.Permanent,
	)
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
// model: it increments the aggregate per-turn budget and, if permanent,
// quarantines tool on this — its FIRST — occurrence in the turn (FR-058-10).
// A later call for the same tool that is already quarantined does not
// overwrite the cached entry; the first permanent denial's payload is what
// every replay serves for the rest of the turn.
//
// permanent must be false ONLY for the "saturated" reason (ADR-058 §3.5 /
// Binding Rule 4, the positive lower bound): recordToolDenial itself does
// not classify anything — callers pass cls.Permanent from ClassifyDenial —
// so a saturated denial never populates the quarantine map here, and a
// later call to the same tool in the same turn is free to reach the
// approver and execute.
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
func (ts *turnState) recordToolDenial(tool, reason string, permanent bool, payload string) (used int, exhausted bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	ts.denialLedger.used++
	if permanent {
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
// quarantine cache for an already-quarantined tool. It still consumes one
// unit of the aggregate per-turn budget — ADR-058 §10.A2 is explicit that
// counting cache-served replays is deliberate: without it, a model
// repeating a quarantined tool would be bounded only by MaxIterations (200),
// cheap in wall-clock but expensive in real LLM calls.
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
