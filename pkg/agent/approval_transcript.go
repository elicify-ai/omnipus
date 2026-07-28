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
//     that nobody answered blocked the turn for the registry's full 300 s
//     timeout (pkg/gateway/approvals.go) and rendered no thread content at all.
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
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

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
// because "your tool call was denied" and "nobody answered for five minutes"
// are very different things to a reader.
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
	settled := session.ToolCall{
		ID:         callID,
		Tool:       toolName,
		Status:     toolCallStatusDenied,
		Parameters: cloneEventArguments(args),
		Result: map[string]any{
			"error":  true,
			"text":   askDenialText(reason),
			"reason": reason,
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
// human sentence and the machine reason.
func askDenialText(reason string) string {
	switch reason {
	case "timeout":
		return "Not run: the approval request expired before anyone answered it."
	case "user":
		return "Not run: the approval request was denied."
	case "cancel":
		return "Not run: the turn was cancelled while the approval request was open."
	case "saturated":
		return "Not run: too many approval requests were already pending."
	case "":
		return "Not run: the approval request was not granted."
	default:
		return "Not run: " + reason
	}
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
