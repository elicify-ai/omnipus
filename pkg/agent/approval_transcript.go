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
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// transcriptMutateMissed is incremented each time mutateToolCallInTranscript
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
// by mutateToolCallInTranscript's three miss sites (the common
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
// sentence. As of this writing the one reason still reaching this fallback
// in production is loop.go's headless-scheduled-run auto-deny literal
// ("auto-denied: ask-policy tool not allowed in a headless scheduled run"),
// which has no denialTable row; a sibling unit of this same epic is adding
// one, which will remove it from the fallback described here — this
// paragraph documents current behavior, not that eventual state.
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

// replaceToolCallInTranscript finds the most recent transcript entry of type
// tool_call carrying a ToolCall whose ID is callID and whose Status is
// expectStatus, replaces that ToolCall wholesale with replacement, and rewrites
// the transcript file atomically under an advisory flock. Returns true when an
// entry was found and the rewrite succeeded.
//
// This is the shared read-modify-rewrite core; recordExternalToolResultUpdateInPlace
// (external_dispatch.go) resolves its own case through
// mutateToolCallInTranscript below. The expectStatus guard is what makes the
// operation idempotent: an entry that already carries a terminal status is left
// alone, so a double-settle cannot clobber a real result.
//
// Concurrency caveat, inherited and unchanged: AppendTranscript is guarded by
// an in-process mutex rather than the flock this takes, so a sibling sub-turn
// sharing the same transcript session can in the worst case land an appended
// line inside the read→rewrite gap and lose it. Accepted here for the same
// reason it is accepted for the external-CLI path — a structural fix means an
// exported UpdateToolCall method on UnifiedStore.
func replaceToolCallInTranscript(
	ts *turnState,
	callID session.ToolCallID,
	expectStatus string,
	replacement session.ToolCall,
) bool {
	if ts == nil || ts.abandoned.Load() ||
		ts.transcriptStore == nil || ts.transcriptSessionID == "" {
		return false
	}
	return mutateToolCallInTranscript(
		ts.transcriptStore, ts.transcriptSessionID, callID, expectStatus,
		func(tc *session.ToolCall) { *tc = replacement },
	)
}

// mutateToolCallInTranscript is the file-level primitive behind
// replaceToolCallInTranscript and external_dispatch.go's result updater: it
// locates the tool_call entry for callID whose Status == expectStatus, applies
// mutate to that ToolCall, and rewrites the file. Split from the two callers so
// the JSONL read-modify-rewrite exists exactly once.
func mutateToolCallInTranscript(
	store *session.UnifiedStore,
	sessionID string,
	callID session.ToolCallID,
	expectStatus string,
	mutate func(*session.ToolCall),
) bool {
	if store == nil || sessionID == "" || mutate == nil {
		return false
	}
	transcriptPath := filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl")

	var found bool
	rewriteErr := fileutil.WithFlock(transcriptPath, func() error {
		found = false
		data, err := os.ReadFile(transcriptPath)
		if err != nil {
			if os.IsNotExist(err) {
				// Narrow race window: the flock's own os.OpenFile (below,
				// via WithFlock) already succeeded — meaning transcriptPath
				// existed a moment ago — but this fresh, independent
				// os.ReadFile(transcriptPath) now sees it gone. That can
				// only happen if the session's directory was removed
				// concurrently (e.g. DeleteSession) between the flock open
				// and this read. Classified the same as "session not
				// found" (FR-099) — the session is, for this call's
				// purposes, gone.
				u22RecordTranscriptMutateMissed("transcript mutate: session not found", sessionID, callID,
					"reason", "session_not_found")
				return nil // no transcript yet — nothing to update
			}
			return err
		}

		// Preserve malformed lines verbatim so the file layout is unchanged on
		// rewrite (matches ReadTranscript's skip-and-continue behaviour).
		rawLines := bytes.Split(data, []byte{'\n'})
		targetIdx := -1
		var updatedEntry session.TranscriptEntry
		for i, raw := range rawLines {
			line := bytes.TrimSpace(raw)
			if len(line) == 0 {
				continue
			}
			var entry session.TranscriptEntry
			if jErr := json.Unmarshal(line, &entry); jErr != nil {
				continue
			}
			if entry.Type != session.EntryTypeToolCall {
				continue
			}
			for j := range entry.ToolCalls {
				if entry.ToolCalls[j].ID != callID {
					continue
				}
				if entry.ToolCalls[j].Status != expectStatus {
					// Already settled — idempotent guard against a
					// double-processed outcome overwriting a real result.
					continue
				}
				targetIdx = i
				updatedEntry = entry
				mutate(&updatedEntry.ToolCalls[j])
				break
			}
			if targetIdx == i {
				break
			}
		}
		if targetIdx == -1 {
			// FR-099: "entry not found" — session exists but no tool_call
			// entry matches callID/expectStatus. Distinguishable from the
			// session-not-found case above via the "reason" field, per
			// BDD-109. Surfaced as a counter increment plus a WARN naming
			// the session id and call id, rather than the bare false this
			// used to return silently.
			u22RecordTranscriptMutateMissed("transcript mutate: tool_call entry not found", sessionID, callID,
				"expect_status", expectStatus, "reason", "entry_not_found")
			return nil // no matching entry — signal fallback to the caller
		}

		rewritten, mErr := json.Marshal(updatedEntry)
		if mErr != nil {
			return mErr
		}
		rawLines[targetIdx] = rewritten

		var buf bytes.Buffer
		for i, line := range rawLines {
			// Skip a trailing empty line produced by a final-newline split so
			// the file does not grow a blank line on each rewrite.
			if i == len(rawLines)-1 && len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			buf.Write(line)
			buf.WriteByte('\n')
		}
		if wErr := fileutil.WriteFileAtomic(transcriptPath, buf.Bytes(), 0o600); wErr != nil {
			return wErr
		}
		found = true
		return nil
	})

	if rewriteErr != nil {
		// FR-099: the MAIN "session not found" path. fileutil.WithFlock opens
		// transcriptPath with os.O_CREATE, which can create the leaf FILE but
		// not a missing PARENT directory — so for a session id that was never
		// minted (no per-session directory at all, matching BDD-109's "no
		// meta.json exists"), WithFlock's own open fails with ENOENT and the
		// closure above never runs at all. Verified: reproduced against a
		// real UnifiedStore with an unminted session id — the closure's own
		// os.IsNotExist(err) branch on os.ReadFile is NOT reached for this
		// case; only this outer branch is. Distinguish it from a genuine I/O
		// failure (permission denied, disk error, flock contention) via
		// errors.Is, so only the true "does not exist" case gets FR-099's
		// specific counter+reason — a real I/O error keeps the existing
		// generic WARN below, which is a different failure class entirely.
		if errors.Is(rewriteErr, os.ErrNotExist) {
			u22RecordTranscriptMutateMissed("transcript mutate: session not found", sessionID, callID,
				"reason", "session_not_found")
			return false
		}
		logger.WarnCF("agent", "in-place tool_call transcript update failed; caller will append instead",
			map[string]any{
				"session_id":   sessionID,
				"tool_call_id": string(callID),
				"error":        rewriteErr.Error(),
			})
		return false
	}
	return found
}
