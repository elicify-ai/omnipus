// approval_transcript.go — transcript records for the human-in-the-loop
// tool-approval (`ask` policy) gate.
//
// WHY THIS FILE EXISTS
//
// An `ask`-policy tool call used to leave ZERO trace in the session transcript.
// The loop raised an approval, blocked on it, and on denial pushed a synthetic
// `permission_denied` message into the PROVIDER message list only — never a
// `tool_call` transcript entry — before `continue`ing past the block that
// records one (pkg/agent/loop.go's runTurn). The live `tool_approval_required`
// WS frame was the sole user-visible artefact, and it is not persisted.
//
// Two things followed, both observed in a CI e2e run on 2026-07-28:
//
//  1. The chat thread showed NOTHING for the entire wait. A run_task approval
//     that nobody answered blocked the turn for the registry's full
//     gateway.go::defaultToolApprovalTimeout (pkg/gateway/approvals.go) and
//     rendered no thread content at all.
//     A page refresh during the wait dropped the live modal too, turning a
//     stalled turn into a permanent, causeless void.
//  2. On non-webchat channels (Telegram, Discord, …) there is no approval UI at
//     all, so EVERY ask-policy call there stalls the full timeout with nothing
//     to show for it, live or replayed.
//
// The fix mirrors what the external-CLI path already does (recordExternalPermission
// / recordExternalToolCall in external_dispatch.go): write a `pending` tool_call
// entry BEFORE blocking, then settle it in place once the approval resolves.
// `pending` and `denied` are already legal ToolCall.Status values on the wire
// (contracts/components/schemas/ToolCall.yaml) and the SPA already maps
// `denied` → `cancelled` (src/lib/api.ts), so this needs no contract change.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// transcriptMutateMissed is incremented each time mutateToolCall
// (below) cannot find a target to mutate — either the session's transcript
// file does not exist yet (ADR-057 FR-099's "session not found" case) or an
// existing transcript has no tool_call entry matching the given
// callID/expectStatus ("entry not found"). This is the read-modify-write
// twin of AC-1's silent-append fix (mirrors pkg/agent/turn.go's
// abandonedWritesSuppressed / TranscriptSuppressedErrors pattern): before
// FR-099, both cases returned a bare `false` with no operator-visible
// signal — the exact success-shaped failure this migration exists to close,
// on the mutation side.
var transcriptMutateMissed atomic.Uint64

// TranscriptMutateMissed returns the current value of the
// omnipus_transcript_mutate_missed_total counter (FR-099). Used by tests and
// operator tooling.
func TranscriptMutateMissed() uint64 {
	return transcriptMutateMissed.Load()
}

// u22RecordTranscriptMutateMissed increments transcriptMutateMissed and
// emits the matching WARN naming the session id and call id (FR-099). Shared
// by mutateToolCall's three miss sites (the common
// directory-does-not-exist case, the narrow post-open-deletion race, and the
// entry-not-found case) so the log fields cannot drift out of sync between
// them. Uses slog (not this file's other logger.WarnCF call, used for a
// genuinely different, non-FR-099 I/O-error class below) so tests can
// assert the record via slog.SetDefault capture — this package's own
// established technique for exactly this class of requirement (see
// wave3_fix5b_test.go).
func u22RecordTranscriptMutateMissed(msg, sessionID string, callID session.ToolCallID, extra ...any) {
	transcriptMutateMissed.Add(1)
	args := append([]any{"session_id", sessionID, "tool_call_id", string(callID)}, extra...)
	slog.Warn(msg, args...)
}

// Transcript ToolCall.Status values used by the approval gate. `pending` and
// `denied` are both in the wire enum (contracts/components/schemas/ToolCall.yaml);
// they are named here so the write site and the in-place settle site can never
// disagree on the literal.
const (
	toolCallStatusPending = "pending"
	toolCallStatusDenied  = "denied"
)

// askPendingPlaceholdersWritten counts every recordAskPendingToolCall call
// that actually wrote a `pending` placeholder (ts != nil) — the direct
// instrument for the §5.7 "no placeholder when a standing grant already
// settles the call" invariant (loop_run_turn_tools.go's resolveAskPolicy and
// requestAskApproval both consult the grant store BEFORE calling
// recordAskPendingToolCall, specifically so a grant-covered call never
// reaches here). Mirrors the transcriptMutateMissed counter pattern already
// established in this file.
var askPendingPlaceholdersWritten atomic.Uint64

// AskPendingPlaceholdersWritten returns the current value of the
// omnipus_ask_pending_placeholders_written_total counter. Used by tests to
// assert that a grant-covered ask-policy call writes zero placeholders while
// a call that genuinely blocks on a human writes exactly one.
func AskPendingPlaceholdersWritten() uint64 {
	return askPendingPlaceholdersWritten.Load()
}

// recordAskPendingToolCall writes the placeholder `tool_call` entry for a tool
// call that is about to block on human approval, and registers its ID so the
// eventual settle (approved-and-executed, or denied) REPLACES this entry rather
// than appending a second one with the same ID — the duplicate-on-replay defect
// external_dispatch.go's S1 note already records for its own flow.
//
// Called immediately before the loop blocks on the approver, so the entry is on
// disk for the whole wait: the thread renders it live, and a reload during the
// wait still shows the turn is parked on an approval rather than showing
// nothing.
//
// Best-effort by design. A failure to persist the placeholder must never block
// the approval itself, so this returns nothing; if the write failed the settle
// path simply finds no placeholder and appends instead (see
// settleAskToolCallTranscript's fallback).
func recordAskPendingToolCall(ts *turnState, callID session.ToolCallID, toolName string, args map[string]any) {
	if ts == nil {
		return
	}
	askPendingPlaceholdersWritten.Add(1)
	ts.askPendingToolCalls.Store(callID, struct{}{})
	ts.appendToolCallTranscript(session.ToolCall{
		ID:         callID,
		Tool:       toolName,
		Status:     toolCallStatusPending,
		Parameters: cloneEventArguments(args),
	})
}

// settleAskToolCallTranscript finalises the placeholder written by
// recordAskPendingToolCall for an approval that did NOT result in execution
// (denied by the user, timed out, cancelled, saturated, or auto-denied on a
// headless run). It rewrites the entry in place to status `denied`, carrying
// the reason so the rendered card and the replayed transcript both explain why
// nothing ran.
//
// The APPROVED case is not handled here: it is settled by the normal execution
// record path (appendToolCallTranscript, which sees the registered pending ID
// and replaces the placeholder with the real completed call).
//
// reason is the approver's outcome reason ("timeout", "user", "cancel",
// "saturated", or the headless auto-deny note) — it is surfaced verbatim
// because "your tool call was denied" and "nobody answered for the whole
// gateway.go::defaultToolApprovalTimeout window" are very different things
// to a reader.
//
// ADR-058 FR-058-08: the settled Result also carries "permanent" — the same
// bool ClassifyDenial(reason) hands the model-facing payload — INSIDE Result,
// never as a top-level ToolCall field (ToolCall.yaml is
// additionalProperties: false; Result is additionalProperties: true, so this
// needs no contract change, spec §3.6).
func settleAskToolCallTranscript(
	ts *turnState,
	callID session.ToolCallID,
	toolName string,
	args map[string]any,
	reason string,
) {
	if ts == nil {
		return
	}
	cls, _ := ClassifyDenial(reason)
	settled := session.ToolCall{
		ID:         callID,
		Tool:       toolName,
		Status:     toolCallStatusDenied,
		Parameters: cloneEventArguments(args),
		Result: map[string]any{
			"error": true,
			// askDenialText(reason) and cls.TranscriptText are the same
			// value — both trace to the identical denialTable[reason]
			// lookup (askDenialText's own body is `cls, _ :=
			// ClassifyDenial(reason); return cls.TranscriptText`). Calling
			// askDenialText here, rather than reading cls.TranscriptText a
			// second time, is what keeps askDenialText itself the one
			// production call site for "render this reason as transcript
			// text" — not just an assertable-but-unused delegate.
			"text":      askDenialText(reason),
			"reason":    reason,
			"permanent": cls.Permanent,
		},
	}

	// The pending placeholder is the normal case; replacing it keeps one entry
	// per tool call. If it is absent (placeholder write failed, transcripts
	// were disabled at the time, or the entry was trimmed) fall through to a
	// plain append so the denial is still recorded — a missing placeholder must
	// never cost us the record of what happened.
	if _, hadPending := ts.askPendingToolCalls.LoadAndDelete(callID); hadPending {
		if replaceToolCallInTranscript(ts, callID, toolCallStatusPending, settled) {
			return
		}
	}
	ts.appendToolCallTranscript(settled)
}

// askDenialText renders the operator-facing one-liner for a non-approval
// outcome. Kept separate from the raw reason so the transcript carries BOTH a
// human sentence and the machine reason — but that separation only holds for
// a KNOWN denialTable row (ADR-058 §4.2's invariant: TranscriptText is a
// reader-facing "Not run: …" sentence, distinct from ModelMessage's
// instruction to the model). For an UNCLASSIFIED reason (no table row),
// FR-058-03 deliberately makes ModelMessage and TranscriptText
// byte-identical (unknownReasonText) — so what this function renders in that
// case is verbatim the model-directed instruction ("Treat this as permanent;
// do not retry — stop and report the blocker."), not a separate human
// sentence. loop.go's headless-scheduled-run auto-deny literal
// (autoDenyHeadlessReason, "auto-denied: ask-policy tool not allowed in a
// headless scheduled run") now HAS its own denialTable row (tool_denial.go)
// and no longer reaches this fallback. As of this writing the two reasons
// that DO still reach it in production are the hook-denial ledger literals
// loop.go's before_tool hook branches record — "hook_denied"
// (HookActionDenyTool) and "approval_hook_denied" (a ToolApprover hook's
// rejection) — neither of which has a denialTable row (loop.go's own
// comments there explain why: hook-supplied free text has no fixed literal
// to classify against). Those two reasons reach askDenialText specifically
// via the QUARANTINE REPLAY path: recordToolDenial caches the reason
// alongside the payload on the tool's first hook-denied call in a turn, and
// a later call to the same tool in the same turn is short-circuited through
// quarantinedDenialFor -> settleAskToolCallTranscript with that cached
// reason, landing here unclassified.
//
// ADR-058 D1: this is the transcript half of the single classification table
// in tool_denial.go — it holds no switch of its own. Before this change the
// switch here and the model-facing sentence built independently in loop.go
// were two hand-maintained renderings of the same event, and D1 exists
// precisely because they had already diverged (ADR-058 §1.4). Delegating to
// ClassifyDenial makes that divergence structurally impossible: both
// surfaces now read the same denialTable row. The five reasons this switch
// used to branch on ("timeout", "user", "cancel", "saturated", and the
// empty-reason case) render byte-for-byte the same TranscriptText they
// always did — denialTable's rows for them were written to match verbatim
// (tool_denial.go's package doc, spec N1) — so no persisted transcript
// string already on disk is reinterpreted differently by this change.
func askDenialText(reason string) string {
	cls, _ := ClassifyDenial(reason)
	return cls.TranscriptText
}

// callRecord is what a turn remembers of one chat tool_call record it wrote:
// the record's exact archive address (returned by the append that wrote it) and
// the post-image it carried. A later settle names that address, so no search of
// the transcript ever decides which record is meant (U2 Decision D5).
type callRecord struct {
	addr session.ArchiveAddress
	tc   session.ToolCall
}

// rememberCallRecord stores the address of a just-appended tool_call record.
func (ts *turnState) rememberCallRecord(tc session.ToolCall, addr session.ArchiveAddress) {
	ts.callRecords.Store(tc.ID, callRecord{addr: addr, tc: tc})
}

// callRecordFor returns the remembered record of a tool call.
func (ts *turnState) callRecordFor(id session.ToolCallID) (callRecord, bool) {
	v, ok := ts.callRecords.Load(id)
	if !ok {
		return callRecord{}, false
	}
	return v.(callRecord), true
}

// replaceToolCallInTranscript settles the remembered tool_call record of callID
// to replacement as ONE appended settle effect, which applies only while the
// call's merged status is still expectStatus (a double settle is a no-op on
// read, so it cannot clobber a real result). It returns true when the effect was
// durably appended. A false return is counted and logged and the caller appends
// the result as a fresh record instead.
func replaceToolCallInTranscript(
	ts *turnState,
	callID session.ToolCallID,
	expectStatus string,
	replacement session.ToolCall,
) bool {
	return mutateToolCall(ts, callID, expectStatus, func(tc *session.ToolCall) { *tc = replacement })
}

// mutateToolCall is the one settle primitive behind replaceToolCallInTranscript
// and external_dispatch.go's result updater: it applies mutate to the remembered
// post-image of callID's record and appends that as a settle effect guarded by
// expectStatus.
func mutateToolCall(
	ts *turnState,
	callID session.ToolCallID,
	expectStatus string,
	mutate func(*session.ToolCall),
) bool {
	if ts == nil || ts.abandoned.Load() || ts.transcriptStore == nil ||
		ts.transcriptSessionID == "" || mutate == nil {
		return false
	}
	rec, ok := ts.callRecordFor(callID)
	if !ok {
		u22RecordTranscriptMutateMissed("transcript mutate: tool_call entry not found", ts.transcriptSessionID, callID,
			"expect_status", expectStatus, "reason", "entry_not_found")
		return false
	}
	post := rec.tc
	mutate(&post)
	if _, err := ts.transcriptStore.SettleToolCall(ts.transcriptSessionID, rec.addr, expectStatus, post); err != nil {
		transcriptWriteFailures.Add(1)
		// FR-099: tell "the session is gone" from "the target record is gone".
		if _, statErr := os.Stat(filepath.Join(ts.transcriptStore.BaseDir(), ts.transcriptSessionID)); errors.Is(statErr, os.ErrNotExist) {
			u22RecordTranscriptMutateMissed("transcript mutate: session not found", ts.transcriptSessionID, callID,
				"reason", "session_not_found")
		} else {
			u22RecordTranscriptMutateMissed("transcript mutate: tool_call entry not found", ts.transcriptSessionID, callID,
				"expect_status", expectStatus, "reason", "entry_not_found")
		}
		logger.WarnCF("agent", "tool_call settle failed; caller will append instead",
			map[string]any{
				"session_id":   ts.transcriptSessionID,
				"tool_call_id": string(callID),
				"error":        err.Error(),
			})
		return false
	}
	ts.rememberCallRecord(post, rec.addr)
	return true
}
