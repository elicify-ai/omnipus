// NOTE: this tag applies to every file in pkg/gateway — it is a package-wide
// constraint enforcing CGO_ENABLED=0 for the single-binary open-source build.
// It is NOT specific to this file; see gateway.go for the package entry point.

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	agent "github.com/elicify-ai/omnipus/pkg/agent"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// replayMaxResultBytes is the maximum JSON-encoded size of a tool_call_result
// frame's result field before it is truncated. Per FR-I-011: 1 MiB.
const replayMaxResultBytes = 1 * 1024 * 1024

// replayResultPreviewBytes is the number of bytes preserved in the preview
// when a result is truncated. Per FR-I-011: 10 KiB.
const replayResultPreviewBytes = 10 * 1024

// streamReplayState carries the shared state of streamReplay across its stages.
type streamReplayState struct {
	sessionID                   string
	entries                     []session.TranscriptEntry
	toolStore                   *toolResultStore
	terminalAsk                 *askuser.PendingSet
	terminalAskEmitted          bool
	seenPaths                   map[string]struct{}
	spawnIDsWithChildren        map[string]bool
	spanRealAgentIDs            map[string]string
	persistedSubagentStartSpans map[string]bool
	persistedSubagentEndSpans   map[string]bool
	latestByID                  map[string]tcAddr
	lastSeenAgentID             string
	msgFrame                    generated.ReplayMessageFrame
	tcID                        string
	tcParentID                  string
	isNested                    bool
	isOrphan                    bool
	effectiveAgentID            string
	isSpawnParent               bool
	stillActive                 bool
	spanID                      string
	spanAgentID                 string
	subStart                    generated.SubagentStartFrame
	subEnd                      generated.SubagentEndFrame
}

// streamReplayStateFlow reports how a block stage of streamReplayState wants the conductor to proceed.
type streamReplayStateFlow int

const (
	streamReplayStateNext streamReplayStateFlow = iota
	streamReplayStateReturn
	streamReplayStateContinue
	streamReplayStateBreak
)

// streamReplay emits replay frames for the given transcript entries, calling
// emit for each frame in order.  It is extracted from handleAttachSession so
// that unit tests can drive it with a slice-backed sink without a real
// WebSocket connection.
//
// Contract:
//   - Compaction entries are skipped (FR-I-006).
//   - ADR-057 D1/W11 (FR-034/FR-038): a delegated child now owns its own
//     real store-backed session (FR-005), so its narration lands in the
//     CHILD's OWN transcript.jsonl and never appears in these entries at
//     all — there is no longer a same-transcript delegate-narration case
//     for this function to withhold. The old ParentSpawnCallID-based skip
//     (the retired child-entry visibility predicate that used to live on
//     session.TranscriptEntry, FR-034) is deleted, not replaced; no read
//     boundary may reintroduce a transcript visibility filter (FR-038).
//   - For user/system entries: emit replay_message{role, content, agent_id}.
//   - For assistant entries: emit replay_message if content is non-empty, then
//     for each ToolCall emit tool_call_start + tool_call_result (FR-I-001).
//   - Spawn spans: scan all entries first to build the set of spawn IDs whose
//     children are present.  Nested tool calls (ParentToolCallID != "") are
//     wrapped with subagent_start / subagent_end when the parent is in the set
//     (FR-I-003).  Orphan parents log slog.Warn (FR-I-007).
//   - Duplicate ToolCall.IDs: only the last occurrence is emitted; earlier ones
//     log slog.Warn (FR-I-012).
//   - Oversized results are truncated (FR-I-011).
//   - Context cancellation is honored between every frame (FR-I-005).
//   - Returns after emitting exactly one done frame (FR-I-004).
//
// rs is the pre-computed replayStats from computeReplayStats. Passing it
// in avoids recomputing spawnIDsWithChildren a second time inside this function.
// The done frame's Stats map is populated from rs so operators see counts in the
// WS trace.
//
// The returned error is non-nil only when emit itself returns an error (e.g.
// context canceled or send-channel full).
//
// emit accepts any generated frame type. Production callers pass the gateway
// WebSocket writer; tests pass a slice-backed sink — both honor the ServerFrame
// contract because the Go contract test and SPA Zod schemas independently
// validate the emitted frames at runtime.
func streamReplay(
	ctx context.Context,
	sessionID string,
	entries []session.TranscriptEntry,
	rs replayStats,
	emit func(any) error,
	mediaStore media.MediaStore,
	toolStore *toolResultStore,
	terminalAsk *askuser.PendingSet,
) (framesEmitted int, err error) {
	sr := &streamReplayState{sessionID: sessionID, entries: entries, toolStore: toolStore, terminalAsk: terminalAsk}

	sr.prepareReplay()

	// ── Pass 2: emit frames ──────────────────────────────────────────────────

	emitFrame := func(f any) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err2 := emit(f); err2 != nil {
			return err2
		}
		framesEmitted++
		return nil
	}

	// buildStart returns a generated.ToolCallStartFrame for tc.
	// Nil params are coerced to an empty map to satisfy the schema contract
	// (ToolCallStartFrame.yaml: params is required and must be an object, never null).
	buildStart := func(tc session.ToolCall, agentID, parentCallID string) generated.ToolCallStartFrame {
		return sr.buildStartFrame(tc, agentID, parentCallID)
	}

	// buildResult returns a generated.ToolCallResultFrame for tc.
	buildResult := func(tc session.ToolCall, agentID, parentCallID string) generated.ToolCallResultFrame {
		return sr.buildResultFrame(tc, agentID, parentCallID)
	}

	// lastSeenAgentID tracks the most recent non-empty AgentID across entries.
	// Used as fallback when a spawn entry has an empty AgentID.
	sr.lastSeenAgentID = ""

	for ei, entry := range sr.entries {
		switch flow, err2 := sr.dispatchSpecialEntry(entry, emitFrame); flow {
		case streamReplayStateContinue:
			continue
		case streamReplayStateReturn:
			return framesEmitted, err2
		}

		// ADR-087 D2/Codex C8: a truncated assistant entry can have EMPTY
		// content — D4a persists a zero-content entry stamped
		// truncated/max_output_tokens when the answer was cut off before any
		// text was produced. That entry must still replay (with no body) so
		// the SPA can render the "(cut off at the output limit)" suffix on
		// reattach/reload; a non-truncated empty entry is unaffected and
		// continues to be skipped entirely below.
		truncatedEmptyAssistant := entry.Content == "" && entry.Truncated && entry.Role == "assistant"

		// FR-I-002: emit replay_message for non-empty content (or a
		// truncated-empty assistant entry, per ADR-087 above).
		if entry.Content != "" || truncatedEmptyAssistant {
			// Phase 1B (FR-014): system-error entries (Type=system + Status="error")
			// are emitted as ReplayErrorFrame so the SPA can render the typed
			// rate-limit-denial or generic error component. Without this, the
			// empty Role would fall through to the assistant render path and the
			// rate-limit text would render as a regular assistant bubble.
			if entry.Type == session.EntryTypeSystem && entry.Status == "error" {
				if err2 := emitFrame(buildReplayErrorFrame(sr.sessionID, entry)); err2 != nil {
					return framesEmitted, err2
				}
				continue
			}

			sr.buildEntryMessage(entry)

			if err2 := emitFrame(sr.msgFrame); err2 != nil {
				return framesEmitted, err2
			}

			// Bug 2 fix: for handoff and return_to_default system entries, emit
			// a typed agent_switched frame so the SPA can render the agent
			// transition visually rather than treating it as plain chat text.
			// HandoffTool writes AgentID = target; ReturnToDefaultTool writes
			// AgentID = returning agent (not target), so we only emit the switch
			// frame for entries whose content starts with "Handoff:" and where
			// the entry carries the target agent ID.
			if entry.Type == session.EntryTypeSystem && entry.AgentID != "" &&
				strings.HasPrefix(entry.Content, "Handoff:") {
				switchF := generated.AgentSwitchedFrame{
					Type:      string(generated.WsFrameTypeAgentSwitched),
					SessionId: sr.sessionID,
				}
				agentIDCopy := entry.AgentID
				switchF.AgentId = &agentIDCopy
				if err2 := emitFrame(switchF); err2 != nil {
					return framesEmitted, err2
				}
			}
		}

		// FR-I-001: emit tool_call_start + tool_call_result for each ToolCall.
	streamReplayStateLoop1:
		for ti, tc := range entry.ToolCalls {
			switch sr.classifyToolCall(ei, entry, ti, tc) {
			case streamReplayStateContinue:
				continue streamReplayStateLoop1
			}

			switch flow, err2 := sr.emitToolCallFrames(tc, mediaStore, buildStart, buildResult, emitFrame); flow {
			case streamReplayStateContinue:
				continue streamReplayStateLoop1
			case streamReplayStateReturn:
				return framesEmitted, err2
			}
		}
	}

	// A terminal AskUserQuestion record with no matching resume message in
	// the replayed entries still gets its collapsed card, appended at the
	// end of the stream: a set cancelled via session Stop dispatches no
	// resume turn at all (CancelOnSessionStop), a resume dispatch can fail
	// after the terminal persist, and an incremental (since-cursor) replay
	// may have filtered the resume entry out. Re-sending the same terminal
	// card on a later incremental replay is idempotent — the SPA stores the
	// card verbatim per session.
	if sr.terminalAsk != nil && !sr.terminalAskEmitted {
		if err2 := emitFrame(buildAskUserQuestionFrame(sr.terminalAsk)); err2 != nil {
			return framesEmitted, err2
		}
	}

	// When the transcript contained duplicate tool_call_ids, surface a one-shot
	// replay_warning frame before the done frame so the SPA can toast the operator.
	// The full counts still live in done.Stats for diagnostics; this frame is the
	// visible UX hook.
	if rs.duplicateToolCallIDCount > 0 {
		if ctx.Err() != nil {
			return framesEmitted, ctx.Err()
		}
		dupCount := rs.duplicateToolCallIDCount
		if err2 := emit(generated.ReplayWarningFrame{
			Type:      string(generated.WsFrameTypeReplayWarning),
			SessionId: sr.sessionID,
			Message:   "transcript contained duplicate tool calls — older copies omitted",
			Stats: &generated.ReplayWarningStats{
				DuplicateToolCallIdCount: &dupCount,
			},
		}); err2 != nil {
			return framesEmitted, err2
		}
	}

	// FR-I-004: exactly one done frame at the end. Populate Stats with the
	// pre-computed counters so operators reading the WS trace can see orphan /
	// duplicate / truncated counts inline. Emitted OUTSIDE emitFrame so it is
	// NOT counted in framesEmitted — that counter represents content frames only.
	framesEmittedF := float64(framesEmitted)
	orphanCountF := float64(rs.orphanCount)
	dupCountF := float64(rs.duplicateToolCallIDCount)
	truncCountF := float64(rs.truncatedResultCount)
	if ctx.Err() != nil {
		return framesEmitted, ctx.Err()
	}
	if err2 := emit(generated.DoneFrame{
		Type:      string(generated.WsFrameTypeDone),
		SessionId: sr.sessionID,
		Stats: &generated.DoneStats{
			FramesEmitted:            &framesEmittedF,
			OrphanCount:              &orphanCountF,
			DuplicateToolCallIdCount: &dupCountF,
			TruncatedResultCount:     &truncCountF,
		},
	}); err2 != nil {
		return framesEmitted, err2
	}
	return framesEmitted, nil
}

// emitToolCallFrames emits the tool_call_start/result frames (and any
// bracketing subagent_start/subagent_end or media frame) for ONE ToolCall
// during streamReplay's per-entry tool-call loop. Extracted verbatim from
// that loop body — same branches, same order, no behavior change — to keep
// streamReplay under the founder's cyclomatic-complexity budget
// (scripts/budgets/gocyclo.txt). Dispatches to the spawn-parent bracketing
// path or the flat (non-spawn / nested) path; each keeps its own doc
// comments at the call site below.
func (sr *streamReplayState) emitToolCallFrames(
	tc session.ToolCall,
	mediaStore media.MediaStore,
	buildStart func(session.ToolCall, string, string) generated.ToolCallStartFrame,
	buildResult func(session.ToolCall, string, string) generated.ToolCallResultFrame,
	emitFrame func(any) error,
) (streamReplayStateFlow, error) {
	if sr.isSpawnParent {
		return sr.emitSpawnParentToolCall(tc, mediaStore, buildStart, buildResult, emitFrame)
	}
	return sr.emitFlatToolCall(tc, mediaStore, buildStart, buildResult, emitFrame)
}

// emitSpawnParentToolCall emits the tool_call_start / subagent_start /
// [subagent_end] / tool_call_result / [media] sequence that brackets a
// spawn/delegate ToolCall's nested span. Always returns
// streamReplayStateContinue on success (the outer loop's next iteration),
// matching the original inline `continue` this was extracted from.
func (sr *streamReplayState) emitSpawnParentToolCall(
	tc session.ToolCall,
	mediaStore media.MediaStore,
	buildStart func(session.ToolCall, string, string) generated.ToolCallStartFrame,
	buildResult func(session.ToolCall, string, string) generated.ToolCallResultFrame,
	emitFrame func(any) error,
) (streamReplayStateFlow, error) {
	// Emit tool_call_start for the spawn call itself FIRST.
	if err2 := emitFrame(buildStart(tc, sr.effectiveAgentID, "")); err2 != nil {
		return streamReplayStateReturn, err2
	}

	// Emit subagent_start to bracket nested frames — UNLESS this span
	// already has a REAL persisted subagent_start (deliverSubagentStart),
	// in which case dispatchSpecialEntry already emitted — or will emit,
	// at that entry's own later position in the transcript — the
	// authoritative one, and this synthetic, tc-derived one must be
	// suppressed (mirrors the identical subagent_end suppression just
	// below). Building it from tc unconditionally was the bug: the
	// reconstruction's ChildSessionId comes from tc.Result["session_id"],
	// a key no production writer ever sets, and its AgentId comes from
	// buildSpanRealAgentIDs, which requires a nested child tool call under
	// this span — a shape the ADR-091 launcher never produces (D1: a
	// delegated/task child owns its own store-backed session). Both
	// fields are correct on the persisted entry deliverSubagentStart
	// already wrote (ChildSessionId := childRec.SessionID, AgentId :=
	// childRec.AgentID); reading that back, same as subagent_end, makes
	// the real values win. buildSubagentStart's tc-based reconstruction
	// remains correct and is still exercised below for a genuinely legacy
	// pre-ADR-091 span, which never gets a persisted start entry at all
	// and DOES nest its child tool calls under the parent
	// (buildSpanRealAgentIDs' designed-for shape).
	if !sr.persistedSubagentStartSpans[sr.spanID] {
		sr.buildSubagentStart(tc)

		if err2 := emitFrame(sr.subStart); err2 != nil {
			return streamReplayStateReturn, err2
		}
	}

	// A delegated/task child owns its own store-backed session, so its tool
	// calls are never recorded under this outer span in the parent's
	// transcript. A pre-ADR-091 transcript that DOES carry nested child
	// tool calls this way loses their nested replay (greenfield migration,
	// §6: old delegate sessions stay readable as history, not
	// full-fidelity) — the outer span's own start/end brackets below are
	// unaffected.

	if sr.stillActive {
		// Withhold subagent_end + the outer tool_call_result: the real
		// sub-turn is still genuinely running. The client already has
		// tool_call_start + subagent_start for this call from above, which
		// is the same "started, no result yet" shape a genuinely in-flight
		// LIVE call shows.
		return streamReplayStateContinue, nil
	}

	// Emit subagent_end — UNLESS this span already has a REAL persisted
	// subagent_end (deliverSubagentEnd), in which case dispatchSpecialEntry
	// above already emitted — or will emit, at that entry's own later
	// position in the transcript — the authoritative one, and this
	// synthetic, tc-derived one must be suppressed (finding 1 fix).
	// Building it from tc here unconditionally was the bug:
	// tc.Status/DurationMS on a delegate/spawn ToolCall is only ever the
	// PLACEHOLDER ack async delegation writes the instant the spawning call
	// returns (Status="success", DurationMS≈0) — nothing in the current
	// architecture ever corrects that record in place. The real terminal
	// status/duration lives ONLY in the persisted subagent_end entry.
	// tc.Status is still the right (indeed the only) source for a legacy
	// pre-ADR-091 span — see persistedSubagentEndSpans's own doc comment
	// (prepareReplay) — which never gets a persisted end entry at all.
	if !sr.persistedSubagentEndSpans[sr.spanID] {
		sr.buildSubagentEnd(tc)

		if err2 := emitFrame(sr.subEnd); err2 != nil {
			return streamReplayStateReturn, err2
		}
	}

	// Emit tool_call_result for the spawn call.
	if err2 := emitFrame(buildResult(tc, sr.effectiveAgentID, "")); err2 != nil {
		return streamReplayStateReturn, err2
	}
	if mf, ok := buildMediaFrame(sr.sessionID, tc, mediaStore, sr.seenPaths); ok {
		if err2 := emitFrame(mf); err2 != nil {
			return streamReplayStateReturn, err2
		}
	}
	return streamReplayStateContinue, nil
}

// emitFlatToolCall emits the tool_call_start / [tool_call_result] / [media]
// sequence for a regular (non-spawn, or nested) ToolCall. Returns
// streamReplayStateContinue when the result is deliberately withheld
// (stillActive), matching the original inline `continue`, or
// streamReplayStateNext to let the outer loop's iteration end normally.
func (sr *streamReplayState) emitFlatToolCall(
	tc session.ToolCall,
	mediaStore media.MediaStore,
	buildStart func(session.ToolCall, string, string) generated.ToolCallStartFrame,
	buildResult func(session.ToolCall, string, string) generated.ToolCallResultFrame,
	emitFrame func(any) error,
) (streamReplayStateFlow, error) {
	// Orphan tool calls are emitted WITHOUT ParentCallID so the client
	// takes the flat non-nested path immediately (not after 10s TTL).
	parentForFlat := ""
	if sr.isNested && !sr.isOrphan {
		parentForFlat = sr.tcParentID
	}
	if err2 := emitFrame(buildStart(tc, sr.effectiveAgentID, parentForFlat)); err2 != nil {
		return streamReplayStateReturn, err2
	}
	if sr.stillActive {
		// A spawn/delegate call whose real sub-turn is still running but
		// has made no (recorded) nested tool calls yet — e.g. a background
		// delegate reloaded before its first step landed (symptom:
		// "0 steps working" live, "done 0ms" on reload). Withhold the
		// result frame; the client sees only tool_call_start, i.e.
		// genuinely in progress.
		return streamReplayStateContinue, nil
	}
	if err2 := emitFrame(buildResult(tc, sr.effectiveAgentID, parentForFlat)); err2 != nil {
		return streamReplayStateReturn, err2
	}
	if mf, ok := buildMediaFrame(sr.sessionID, tc, mediaStore, sr.seenPaths); ok {
		if err2 := emitFrame(mf); err2 != nil {
			return streamReplayStateReturn, err2
		}
	}
	return streamReplayStateNext, nil
}

// dispatchSpecialEntry handles every entry-type/subtype special case that
// either fully replays an entry as its own typed frame (returning
// streamReplayStateContinue so streamReplay's loop moves on to the next
// entry) or falls through to streamReplay's own generic content-emission +
// tool-call replay for this SAME entry (returning streamReplayStateNext).
// Extracted verbatim from streamReplay's per-entry loop — same branches,
// same order, same fall-through cases (a malformed goal_outcome/
// subagent_message/subagent_state entry still falls through to the generic
// rendering below rather than being dropped) — to keep both functions under
// the founder's function-size and cyclomatic-complexity budgets
// (scripts/budgets/functions.txt, scripts/budgets/gocyclo.txt) with no
// behavior change.
func (sr *streamReplayState) dispatchSpecialEntry(entry session.TranscriptEntry, emitFrame func(any) error) (streamReplayStateFlow, error) {
	// FR-I-006: skip compaction entries.
	if entry.Type == session.EntryTypeCompaction {
		return streamReplayStateContinue, nil
	}

	// Wave 3 fix 5c: emit a role:"turn_canceled" ReplayMessageFrame for
	// EntryTypeTurnCancelled entries (pkg/agent/cancel.go's onCancelFinish
	// callback, ~line 224). Before this fix, replay had no code path that
	// read these persisted entries at all — a canceled turn simply
	// vanished on reload instead of showing the same cancellation marker
	// the live WS stream showed. entry.TurnID (stamped by the same
	// callback) travels onto the frame's turn_id field so the client can
	// match this cancellation to the specific preceding assistant message
	// it interrupted without relying on stream-adjacency — async
	// delegation can interleave other agents'/turns' frames in between.
	// This entry type carries no Content (cancel.go's literal never sets
	// it), so it needs its own unconditional branch rather than falling
	// through the `entry.Content != ""` gate below.
	if entry.Type == session.EntryTypeTurnCancelled {
		cancelFrame := generated.ReplayMessageFrame{
			Type:      string(generated.WsFrameTypeReplayMessage),
			SessionId: sr.sessionID,
			Role:      "turn_canceled",
			Content:   turnCancelledContent(entry),
		}
		if entry.TurnID != "" {
			turnIDCopy := entry.TurnID
			cancelFrame.TurnId = &turnIDCopy
		}
		if err2 := emitFrame(cancelFrame); err2 != nil {
			return streamReplayStateReturn, err2
		}
		return streamReplayStateContinue, nil
	}

	// review r2 RV1: EntryTypeJudgeVerdict entries (ADR-049 D2/D4, written
	// by TaskExecutor.writeJudgeVerdictTranscript / goal_loop.go's
	// writeGoalVerdictTranscript) carry Role="system" and raw
	// json.Marshal(task.JudgeVerdict) Content. Before this fix there was no
	// dedicated case for this entry type, so it fell through to the generic
	// entry.Content != "" branch below and rendered as a garbled raw-JSON
	// system chat bubble on WS reconnect — defeating SD-C10 (a verdict is
	// panel-only by default, never a raw thread bubble). Emit a typed
	// generated.JudgeVerdictFrame instead — the SAME frame shape/type the
	// SPA's WS frame switch already routes to useJudgeActivityStore (NOT
	// the thread; src/store/chat.ts's `case 'judge_verdict'`), so replay
	// parity with a live push is exact regardless of which code path a
	// verdict frame arrived through.
	if entry.Type == session.EntryTypeJudgeVerdict {
		var verdict task.JudgeVerdict
		if uerr := json.Unmarshal([]byte(entry.Content), &verdict); uerr != nil {
			slog.Warn("replay: could not parse judge_verdict transcript entry — skipping",
				"session_id", sr.sessionID, "entry_id", entry.ID, "error", uerr)
			return streamReplayStateContinue, nil
		}
		if err2 := emitFrame(toJudgeVerdictFrame(sr.sessionID, verdict)); err2 != nil {
			return streamReplayStateReturn, err2
		}
		return streamReplayStateContinue, nil
	}

	// ADR-085 BROWSER-FR-043a: a persisted browser-handover waiting-line
	// entry replays as the SAME frame type FR-042 delivers live
	// (BrowserHandoverNoticeFrame), never the generic ReplayMessageFrame
	// the fallthrough below would otherwise produce. Discriminates on
	// the STAMPED entry.SystemSubtype field alone — never by
	// prefix-matching entry.Content, which is exactly the anti-pattern
	// the existing "Handoff:" branch elsewhere in this function is
	// documented as being (see the FR-043a spec citation). The message
	// id is the entry's own ID, per FR-044: "the deterministic notice id
	// rides the existing TranscriptEntry.ID".
	if entry.Type == session.EntryTypeSystem && entry.SystemSubtype == "browser_handover_notice" {
		if err2 := emitFrame(generated.BrowserHandoverNoticeFrame{
			Type:      string(generated.WsFrameTypeBrowserHandoverNotice),
			SessionId: sr.sessionID,
			MessageId: entry.ID,
			Text:      entry.Content,
		}); err2 != nil {
			return streamReplayStateReturn, err2
		}
		return streamReplayStateContinue, nil
	}

	// Goal outcome line (founder decision 2026-09-14): a persisted goal
	// ending replays as the SAME goal_outcome frame the ending sent live
	// (goalOutcomeFrame, shared with websocket.go), discriminated on the
	// stamped entry.SystemSubtype — never on Content — with the entry's own
	// ID as message_id so the SPA keeps exactly one line per ending. An
	// entry stamped goal_outcome but carrying no outcome is malformed: it
	// is logged and falls through to the ordinary rendering below so its
	// text is not lost.
	if entry.Type == session.EntryTypeSystem && entry.SystemSubtype == session.SystemSubtypeGoalOutcome {
		if entry.GoalOutcome != nil {
			if err2 := emitFrame(goalOutcomeFrame(sr.sessionID, entry.ID, *entry.GoalOutcome)); err2 != nil {
				return streamReplayStateReturn, err2
			}
			return streamReplayStateContinue, nil
		}
		slog.Warn("replay: goal_outcome transcript entry carries no outcome — replaying it as a plain entry",
			"session_id", sr.sessionID, "entry_id", entry.ID)
	}

	// ADR-091 D7/I-4: a persisted subagent_start/subagent_message/
	// subagent_state/subagent_end entry (steer_frames.go's
	// deliverSubagentStart/deliverSubagentMessage/deliverSubagentState/
	// deliverSubagentEnd, written into the PARENT's own transcript) replays
	// as the SAME frame type the live push sent, discriminated on the
	// stamped entry.SystemSubtype — never Content — with the entry's own
	// ID as message_id, mirroring goal_outcome's identical contract two
	// blocks above.
	//
	// FIX (finding 1, CRITICAL): subagent_end used to be excluded from
	// this list and stay on the tool-call-structure reconstruction below
	// (buildSubagentEnd) exclusively. That reconstruction reads
	// Status/DurationMS off the spawn/delegate ToolCall record itself —
	// which async delegation stamps with a PLACEHOLDER ack
	// (Status="success", DurationMS≈0) the instant the spawning call
	// returns, not the real terminal outcome. The intended correction
	// path (a live in-process turn tracker flipping isSpanActive true
	// until the real status landed) was structurally dead: its two data
	// sources, steering.go's markSubTurnSpanOpen/subTurnSpanOpen and
	// turnState.parentSpawnCallID, had zero real callers / were never
	// assigned (ADR-091 deleted subturn.go, their only writer), so
	// isSpanActive always answered false and the withhold never fired —
	// every persisted subagent_end (failed, cancelled, timed out) replayed
	// as a fabricated "success, 0 ms". Reading the entry back here — the
	// same self-healing SessionId-restamp pattern subagent_message/
	// subagent_state already use — makes the real, persisted terminal
	// status/duration win. classifyToolCall below now also suppresses the
	// tool-call-derived synthetic subagent_end whenever this span already
	// has one of these persisted (buildSubagentEnd() is only still called
	// for a delegate/spawn call this transcript never persisted an end
	// entry for — legacy pre-ADR-091 data, where tc.Status is the ONLY
	// terminal record that has ever existed for it).
	//
	// FIX (RX-CI round): subagent_start had the identical bug — see
	// dispatchPersistedSubagentStart's doc comment below for the root
	// cause/fix. Extracted to its own method (unlike message/state/end
	// below) to keep this function under the function-size budget.
	if flow, err2, handled := sr.dispatchPersistedSubagentStart(entry, emitFrame); handled {
		return flow, err2
	}
	if entry.Type == session.EntryTypeSystem && entry.SystemSubtype == session.SystemSubtypeSubagentMessage {
		if entry.SubagentMessage != nil {
			// UAT defect 1: stamp SessionId from sr.sessionID (the
			// transcript this entry was read FROM — persistSubagentEntry
			// only ever writes these into the PARENT's own transcript)
			// rather than trusting whatever the stored frame carries, the
			// same self-healing choice this function already makes for
			// browser_handover_notice/goal_outcome just above (both built
			// fresh from sr.sessionID, never from a stored SessionId).
			// This makes a reload correct even for an entry persisted
			// BEFORE deliverSubagentMessage's fix (steer_frames.go), whose
			// stored SessionId is the child's — no separate data migration
			// needed.
			frame := *entry.SubagentMessage
			frame.SessionId = sr.sessionID
			if err2 := emitFrame(frame); err2 != nil {
				return streamReplayStateReturn, err2
			}
			return streamReplayStateContinue, nil
		}
		slog.Warn("replay: subagent_message transcript entry carries no frame — replaying it as a plain entry",
			"session_id", sr.sessionID, "entry_id", entry.ID)
	}
	if entry.Type == session.EntryTypeSystem && entry.SystemSubtype == session.SystemSubtypeSubagentState {
		if entry.SubagentState != nil {
			// UAT defect 1: same self-healing stamp as subagent_message
			// above.
			frame := *entry.SubagentState
			frame.SessionId = sr.sessionID
			if err2 := emitFrame(frame); err2 != nil {
				return streamReplayStateReturn, err2
			}
			return streamReplayStateContinue, nil
		}
		slog.Warn("replay: subagent_state transcript entry carries no frame — replaying it as a plain entry",
			"session_id", sr.sessionID, "entry_id", entry.ID)
	}
	if entry.Type == session.EntryTypeSystem && entry.SystemSubtype == session.SystemSubtypeSubagentEnd {
		if entry.SubagentEnd != nil {
			// UAT defect 1: same self-healing stamp as subagent_message/
			// subagent_state above — the entry was written into the
			// PARENT's own transcript (steer_frames.go's
			// persistSubagentEntry), so sr.sessionID (the transcript this
			// entry was read FROM) is always correct regardless of
			// whatever SessionId the stored frame happens to carry.
			frame := replayedSubagentEnd(entry)
			frame.SessionId = sr.sessionID
			if err2 := emitFrame(frame); err2 != nil {
				return streamReplayStateReturn, err2
			}
			return streamReplayStateContinue, nil
		}
		slog.Warn("replay: subagent_end transcript entry carries no frame — replaying it as a plain entry",
			"session_id", sr.sessionID, "entry_id", entry.ID)
	}

	// Update the running fallback agent ID.
	if entry.AgentID != "" {
		sr.lastSeenAgentID = entry.AgentID
	}

	// AskUserQuestion resume messages (spec v3 §0.2) — the persisted
	// user-role `Answers to your questions (card_id=<id>): {...}` turn
	// opener — are NEVER replayed as a raw replay_message: the §0.2
	// presentation rule says the SPA renders the resume message AS the
	// collapsed answer record, never as raw JSON, and the collapsed
	// record itself is reconstructed here from the terminal registry/
	// session-meta record (§0.6), which makes simple suppression of the
	// raw bubble correct — the card frame emitted in its place IS the
	// render of this message. When the resume message's card id matches
	// the terminal record, the reconstructed card frame is emitted at
	// this exact position, so the collapsed record lands where the
	// resume happened in the thread. A resume message with NO matching
	// terminal record (an older set — PendingAskJSON holds only the
	// latest, so an earlier set's record is overwritten by the next
	// CreatePending) is still suppressed: raw JSON must never render,
	// and its park-time tool_call/tool_result stub remains in the
	// stream as the historical trace. Resume entries are plain inbound
	// user messages (dispatched via PublishInbound) and carry no tool
	// calls, so skipping the whole entry loses nothing else.
	if entry.Role == "user" {
		if cardID, isResume := askuser.ParseResumeCardID(entry.Content); isResume {
			if sr.terminalAsk != nil && !sr.terminalAskEmitted && sr.terminalAsk.CardID == cardID {
				sr.terminalAskEmitted = true
				if err2 := emitFrame(buildAskUserQuestionFrame(sr.terminalAsk)); err2 != nil {
					return streamReplayStateReturn, err2
				}
			}
			return streamReplayStateContinue, nil
		}
	}

	return streamReplayStateNext, nil
}

// dispatchPersistedSubagentStart handles a persisted subagent_start system
// entry (steer_frames.go's deliverSubagentStart) — the same self-healing
// SessionId-restamp pattern dispatchSpecialEntry's own subagent_message/
// subagent_state/subagent_end cases use, extracted into its own method
// purely to keep dispatchSpecialEntry under the founder's function-size
// budget. handled=false means "not a subagent_start entry, or a malformed
// one — fall through to the rest of dispatchSpecialEntry", exactly the
// fall-through this had inline before extraction; handled=true means flow/
// err is dispatchSpecialEntry's own return value.
//
// FIX (RX-CI round, ADR-091 fix): subagent_start had the identical bug
// subagent_end's "finding 1" fix (in dispatchSpecialEntry above) already
// covers — a previous version of this comment claimed "nothing about its
// content was ever wrong", which was itself wrong. The tool-call-structure
// reconstruction (replay.go's buildSubagentStart) builds ChildSessionId
// from tc.Result["session_id"] — a key NO production writer ever sets
// (delegate_run.go's result shape has no top-level session_id; the child id
// only ever appears as JSON text inside Result["text"]), so the
// reconstructed frame's child_session_id was always nil and the "open
// child session" control never appeared after a reload. Its agent_id had
// the matching bug: buildSpanRealAgentIDs resolves the real delegate's
// identity from a NESTED child tool call recorded under the parent
// transcript — a shape the ADR-091 launcher never produces (a
// delegated/task child owns its own store-backed session, D1; its tool
// calls are never recorded under the parent's outer span), so the lookup
// always missed and the reconstruction silently fell back to the
// DELEGATOR's own agent_id. Both fields are correct on the entry
// deliverSubagentStart already persisted (ChildSessionId :=
// childRec.SessionID, AgentId := childRec.AgentID) — reading it back here,
// same as subagent_end, makes the real values win. The tool-call-derived
// synthetic subStart is now suppressed by emitSpawnParentToolCall whenever
// this span already has one of these persisted
// (persistedSubagentStartSpans), the same suppression subagent_end already
// had — buildSubagentStart's own tc-based reconstruction remains correct
// and is still exercised for a genuinely legacy pre-ADR-091 span, which
// never gets a persisted start entry at all and DOES nest its child tool
// calls under the parent (buildSpanRealAgentIDs' designed-for shape).
func (sr *streamReplayState) dispatchPersistedSubagentStart(entry session.TranscriptEntry, emitFrame func(any) error) (flow streamReplayStateFlow, err error, handled bool) {
	if entry.Type != session.EntryTypeSystem || entry.SystemSubtype != session.SystemSubtypeSubagentStart {
		return streamReplayStateNext, nil, false
	}
	if entry.SubagentStart == nil {
		slog.Warn("replay: subagent_start transcript entry carries no frame — replaying it as a plain entry",
			"session_id", sr.sessionID, "entry_id", entry.ID)
		return streamReplayStateNext, nil, false
	}
	// UAT defect 1: same self-healing stamp as subagent_message/
	// subagent_state/subagent_end use — the entry was written into the
	// PARENT's own transcript (steer_frames.go's persistSubagentEntry), so
	// sr.sessionID (the transcript this entry was read FROM) is always
	// correct regardless of whatever SessionId the stored frame happens to
	// carry.
	f := *entry.SubagentStart
	f.SessionId = sr.sessionID
	f.SpanId = canonicalReplaySpanID(entry.ID, f.SpanId, f.ParentCallId)
	if err2 := emitFrame(f); err2 != nil {
		return streamReplayStateReturn, err2, true
	}
	return streamReplayStateContinue, nil, true
}

// prepareReplay normalizes replay inputs and builds the ancillary indexes.
func (sr *streamReplayState) prepareReplay() {
	// terminalAsk is the session's persisted TERMINAL (answered/cancelled)
	// AskUserQuestion record from UnifiedMeta's PendingAskJSON (see
	// loadTerminalAskRecord), or nil. askuserquestion-tool-spec v3 §0.6: the
	// collapsed card on history reload renders from THIS record — not from
	// the tool_call/tool_result pair (which holds only the park-time
	// "pending" stub) and not from parsing the resume message — so replay
	// reconstructs it into an ask_user_question frame (the same frame the
	// live terminal emission sent, mirroring how judge_verdict entries are
	// rebuilt above). A still-PENDING record is never passed here (and is
	// defensively dropped below): the live pending card is the registry's to
	// deliver via session_state's pending_asks snapshot.
	if sr.terminalAsk != nil && sr.terminalAsk.Status == askuser.StatusPending {
		sr.terminalAsk = nil
	}
	sr.terminalAskEmitted = false

	// Track underlying file paths already emitted so the SPA never receives
	// two media frames for the same file. Older transcripts can carry
	// multiple media:// refs pointing at the same on-disk file (browser.
	// screenshot stored an inline copy AND send_file registered a second
	// ref before the RefByPath dedup landed). Without this guard, both
	// frames replay and the user sees the screenshot twice.
	sr.seenPaths = make(map[string]struct{})
	// ── Pass 1: build ancillary indexes ─────────────────────────────────────

	// spawnIDsPresent: set of ToolCall.IDs where tool == "spawn" or "delegate"
	// AND at least one other tool call in the transcript has ParentToolCallID
	// == that ID. This is the signal that the parent span has live children
	// to bracket. See buildSpawnIDsWithChildren's own doc comment below for
	// why both tool names are checked (ADR-036 spawn→delegate rename).
	sr.spawnIDsWithChildren = buildSpawnIDsWithChildren(sr.entries)

	// spanRealAgentIDs maps a spawn/delegate ToolCall.ID (that has at least
	// one child) to the REAL delegate agent's own ID, resolved from its
	// first nested child tool call's own transcript entry. See
	// buildSpanRealAgentIDs' doc comment for why this differs from — and is
	// more correct than — entry.AgentID on the OUTER spawn/delegate call.
	sr.spanRealAgentIDs = buildSpanRealAgentIDs(sr.entries, sr.spawnIDsWithChildren)

	// persistedSubagentStartSpans / persistedSubagentEndSpans: the set of
	// span IDs from agent.SubagentSpanID. Generation 1 is "span_" + the
	// originating call id. Generation N >= 2 appends "_g<N>", recovered
	// from the entry id by canonicalReplaySpanID, because the call id
	// alone is not enough. The indexes hold every id that already has a REAL
	// persisted subagent_start / subagent_end system entry somewhere in
	// this transcript (steer_frames.go's deliverSubagentStart/
	// deliverSubagentEnd — ADR-091 D7/I-4). Two independent uses:
	//
	//   - persistedSubagentEndSpans gates the suppression in
	//     classifyToolCall below: when a span already has a real
	//     persisted end, the tool-call-derived synthetic one (built from
	//     the spawn call's own placeholder Status/DurationMS) is never
	//     built — dispatchSpecialEntry emits the real one instead, at its
	//     own position in the transcript.
	//   - persistedSubagentStartSpans combined with the absence of a
	//     matching entry in persistedSubagentEndSpans is classifyToolCall's
	//     replacement for the dead isSpanActive/IsSubTurnActiveForSpawnCall
	//     liveness callback (finding 1): deliverSubagentStart persists
	//     synchronously at launch (steer_launcher.go's
	//     publishSteeredLaunch), before the child does any real work, so
	//     "has a persisted start, no persisted end yet" reliably means
	//     "genuinely still running" for any transcript written by the
	//     current (post-ADR-091) delegation path. A legacy transcript
	//     recorded before this mechanism existed has NEITHER a persisted
	//     start nor a persisted end for its spawn calls — requiring BOTH a
	//     start and the absence of an end (not just the absence of an end
	//     alone) keeps those old, already-finished calls on the original
	//     tc.Status-derived rendering instead of showing them as
	//     perpetually running.
	sr.persistedSubagentStartSpans, sr.persistedSubagentEndSpans = buildPersistedSubagentSpanIndexes(sr.entries)

	// deduped: for each ToolCall.ID keep only the index of the last occurrence
	// across ALL entries.  key = ToolCall.ID, value = (entryIdx, tcIdx).
	sr.latestByID = make(map[string]tcAddr)
	for ei, entry := range sr.entries {
		for ti, tc := range entry.ToolCalls {
			if tc.ID == "" {
				continue
			}
			if prev, dup := sr.latestByID[string(tc.ID)]; dup {
				// Duplicate detected — log the previous address for diagnostics; last occurrence wins.
				slog.Warn("replay: duplicate tool_call_id detected — only latest will emit",
					"previous_entry_index", prev.entryIdx,
					"previous_tool_index", prev.tcIdx,
					"event", "replay_duplicate_tool_call_id",
					"session_id", sr.sessionID,
					"tool_call_id", string(tc.ID),
				)
			}
			sr.latestByID[string(tc.ID)] = tcAddr{ei, ti}
		}
	}
}

// buildStartFrame builds a tool-call start frame with schema-safe parameters.
func (sr *streamReplayState) buildStartFrame(tc session.ToolCall, agentID string, parentCallID string) generated.ToolCallStartFrame {
	params := tc.Parameters
	if params == nil {
		params = map[string]any{}
	}
	f := generated.ToolCallStartFrame{
		Type:      string(generated.WsFrameTypeToolCallStart),
		SessionId: sr.sessionID,
		CallId:    string(tc.ID),
		Tool:      tc.Tool,
		Params:    params,
	}
	if agentID != "" {
		f.AgentId = &agentID
	}
	if parentCallID != "" {
		f.ParentCallId = &parentCallID
	}
	return f
}

// buildResultFrame builds a persisted tool-call result frame.
func (sr *streamReplayState) buildResultFrame(tc session.ToolCall, agentID string, parentCallID string) generated.ToolCallResultFrame {
	resultPayload := truncateResult(sr.sessionID, tc, sr.toolStore)
	durationMs := int(tc.DurationMS)
	f := generated.ToolCallResultFrame{
		Type:       string(generated.WsFrameTypeToolCallResult),
		SessionId:  sr.sessionID,
		CallId:     string(tc.ID),
		Tool:       tc.Tool,
		Result:     resultPayload,
		Status:     toolCallResultStatus(tc.Status),
		DurationMs: &durationMs,
	}
	if agentID != "" {
		f.AgentId = &agentID
	}
	if parentCallID != "" {
		f.ParentCallId = &parentCallID
	}
	applyPersistedFailureReason(&f, tc)
	return f
}

// buildEntryMessage builds an ordinary replay message with optional metadata.
func (sr *streamReplayState) buildEntryMessage(entry session.TranscriptEntry) {
	sr.msgFrame = generated.ReplayMessageFrame{
		Type:      string(generated.WsFrameTypeReplayMessage),
		SessionId: sr.sessionID,
		Role:      entry.Role,
		Content:   entry.Content,
	}
	if entry.AgentID != "" {
		agentIDCopy := entry.AgentID
		sr.msgFrame.AgentId = &agentIDCopy
	}
	// #823 catch-up redesign (BE-DESIGN.md §4.2/§6.3): the persisted entry
	// id is the same id the live frames used (user_message.id; an assistant
	// round's token/done message_id), so a client can merge a replayed
	// message with its live copy by id, and the snapshot path can tell which
	// active-turn items the transcript read already covered.
	if entry.ID != "" {
		idCopy := entry.ID
		sr.msgFrame.Id = &idCopy
	}
	// §4.7: the sender's own message id rides the replayed user entry, so a
	// pending bubble whose message was already persisted reconciles instead
	// of duplicating.
	if entry.ClientMessageID != "" {
		cidCopy := entry.ClientMessageID
		sr.msgFrame.ClientMessageId = &cidCopy
	}
	// Wave 3 fix 5c/1: surface TranscriptEntry.TurnID — stamped on
	// every real assistant entry at its three production write sites:
	// pkg/agent/turn.go's appendIntermediateAssistantTranscript and
	// appendAssistantTranscript (both set TurnID: ts.turnID), and
	// pkg/gateway/websocket.go's wsStreamer.Finalize (stamped via
	// SetTurnID, mirroring SetProducerAgentID's pattern) — so the
	// client can correlate a later turn_canceled frame to the
	// specific assistant message it cancels. Empty for legacy
	// entries written before turn-id stamping landed.
	if entry.TurnID != "" {
		turnIDCopy := entry.TurnID
		sr.msgFrame.TurnId = &turnIDCopy
	}
	// Phase 1B (FR-013/FR-014): surface per-turn model. Populated from
	// TranscriptEntry.Model on every assistant message written via
	// pkg/agent/turn.go since Phase 1B landed. Empty for legacy turns;
	// the UI omits the model field entirely (no placeholder) for those
	// entries — see MessageItem.tsx model-footer rendering (FR-014).
	if entry.Model != "" {
		modelCopy := entry.Model
		sr.msgFrame.Model = &modelCopy
	}
	// ADR-087 D2: surface truncation on every replayed assistant
	// entry that carries it — not only the empty-content case above.
	// D4b (auto-continue exhausted/ineligible) stamps Truncated on
	// an entry that DOES have content, and that must replay with the
	// same "(cut off at the output limit)" suffix as a live turn.
	// Absent reason on a truncated entry means "cancelled" (legacy —
	// every entry written before TruncationReason existed was always
	// a cancel; see TranscriptEntry.TruncationReason's doc comment).
	if entry.Role == "assistant" && entry.Truncated {
		truncatedCopy := true
		sr.msgFrame.Truncated = &truncatedCopy
		reason := entry.TruncationReason
		if reason == "" {
			reason = "cancelled"
		}
		sr.msgFrame.TruncationReason = &reason
	}
}

// classifyToolCall classifies one tool call and resolves its replay identity.
func (sr *streamReplayState) classifyToolCall(ei int, entry session.TranscriptEntry, ti int, tc session.ToolCall) streamReplayStateFlow {
	if tc.ID == "" {
		return streamReplayStateContinue
	}
	sr.tcID = string(tc.ID)
	sr.tcParentID = string(tc.ParentToolCallID)
	// Dedup: skip if this is not the latest occurrence.
	if latest := sr.latestByID[sr.tcID]; latest.entryIdx != ei || latest.tcIdx != ti {
		return streamReplayStateContinue
	}

	sr.isNested = sr.tcParentID != ""
	parentIsSpawn := sr.isNested && sr.spawnIDsWithChildren[sr.tcParentID]
	sr.isOrphan = sr.isNested && !parentIsSpawn

	if sr.isNested && parentIsSpawn {
		// A delegated/task child owns its own transcript, so this branch is dead
		// for any transcript ADR-091's real launcher produced. Still
		// skipped here (not re-processed as a top-level call) for a
		// pre-ADR-091 transcript that DOES carry a nested recording —
		// greenfield migration, §6: it is simply dropped from replay
		// rather than mis-rendered as a flat top-level call.
		return streamReplayStateContinue
	}

	if sr.isOrphan {
		// FR-I-007: orphan — parent not found in transcript.
		slog.Warn("replay: orphan tool call — parent spawn not in transcript",
			"event", "replay_orphan",
			"session_id", sr.sessionID,
			"parent_tool_call_id", sr.tcParentID,
		)
		// The orphan is emitted as a flat tool call (no ParentCallID on the wire).
		// This causes the client to take the non-nested rendering path immediately
		// rather than waiting 10 s for the orphan TTL to expire.
		// The slog.Warn above records the full context for operator debugging.
	}

	// Resolve the effective agent ID for this tool call's frames.
	// If the spawn entry has an empty AgentID, fall back to the most recently
	// seen agent ID in the transcript so the span is never emitted with a blank agent_id.
	sr.effectiveAgentID = entry.AgentID
	if sr.effectiveAgentID == "" {
		sr.effectiveAgentID = sr.lastSeenAgentID
	}

	// isDelegateSpawnCall identifies a spawn/delegate/create_task tool call
	// (the two legacy names checked mirror buildSpawnIDsWithChildren's own
	// ADR-036 rename note; create_task added by ADR-091 D7/I-4 — "learns
	// create_task alongside delegate", both fronts sharing one bracketing
	// rule since both are steered sessions now). Used below both to
	// resolve span-level agent-id and to gate the still-active liveness
	// check — a terminal snapshot is only ever withheld for THIS call
	// kind, never for an ordinary tool call.
	isDelegateSpawnCall := tc.Tool == "spawn" || tc.Tool == "delegate" || tc.Tool == "create_task"

	// Finding C (A-I4 round 4): every spawn/delegate call gets a
	// subagent_start/subagent_end bracket on replay, matching live
	// unconditionally — pkg/agent/subturn.go's spawnSubTurn always
	// fires EventKindSubTurnSpawn/EventKindSubTurnEnd for a delegate
	// call regardless of how many tool calls the CHILD itself made,
	// so pkg/gateway/websocket.go's eventForwarder always emits a
	// live subagent_start/subagent_end pair too. This used to be
	// gated on spawnIDsWithChildren (spans requiring at least one
	// recorded nested child tool call), which was wrong as the gate for
	// whether to bracket at all: a delegate whose child
	// replies directly with zero tool calls (a common case — many
	// delegated tasks are simple, no-tool Q&A, and it's also exactly
	// what a child interrupted before its first tool call looks
	// like) got NO span bracket whatsoever on reload, silently
	// dropping the nested "label, 0 steps, status, duration"
	// progress row live always shows, even though the outer call's
	// own Status/DurationMS are fully known and persisted either
	// way. isDelegateSpawnCall (above) is the correct test because it
	// does not require any child tool calls to exist.
	sr.isSpawnParent = isDelegateSpawnCall

	// spanID is generation 1 of the shared rule (agent.SubagentSpanID).
	// This tool call is the original delegate/spawn/create_task call, which
	// is generation 1. A follow-up generation is not a second tool call: its
	// bracket is a persisted subagent_start/subagent_end, rebuilt by
	// canonicalReplaySpanID from the entry id.
	sr.spanID = agent.SubagentSpanID(sr.tcID, 1)

	// stillActive (finding 1 fix — see dispatchSpecialEntry's subagent_end
	// case above for the full root-cause writeup) is true when this
	// spawn/delegate call's REAL sub-turn has a persisted subagent_start
	// entry (deliverSubagentStart, stamped synchronously at launch) but no
	// matching persisted subagent_end yet — i.e. the transcript's own
	// record says "started, not yet concluded" for a delegation launched
	// through the current (post-ADR-091) path. Requiring the start
	// entry too (not just "no end yet") keeps a legacy pre-ADR-091
	// transcript — which has neither — on its original tc.Status-derived
	// rendering instead of showing an already-finished old delegation as
	// stuck running forever. When stillActive is true, this call's OWN
	// terminal frame(s) — tool_call_result / subagent_end — are withheld
	// so the client is never shown a fabricated "done" for a turn that is,
	// in truth, still working; the real completion arrives later either
	// over the live WS event stream, or on the next reload once
	// deliverSubagentEnd has persisted it.
	sr.stillActive = isDelegateSpawnCall && sr.persistedSubagentStartSpans[sr.spanID] && !sr.persistedSubagentEndSpans[sr.spanID]
	return streamReplayStateNext
}

// buildSubagentStart builds the start frame for a delegated subagent span.
func (sr *streamReplayState) buildSubagentStart(tc session.ToolCall) {
	sr.spanID = agent.SubagentSpanID(sr.tcID, 1)
	taskLabel := resolveTaskLabel(tc)
	sr.spanAgentID = sr.effectiveAgentID
	if realAgentID, ok := sr.spanRealAgentIDs[sr.tcID]; ok && realAgentID != "" {
		sr.spanAgentID = realAgentID
	}
	sr.subStart = generated.SubagentStartFrame{
		Type:         string(generated.WsFrameTypeSubagentStart),
		SessionId:    sr.sessionID,
		SpanId:       sr.spanID,
		ParentCallId: sr.tcID,
		TaskLabel:    taskLabel,
	}
	if sr.spanAgentID != "" {
		agentIDCopy := sr.spanAgentID
		sr.subStart.AgentId = &agentIDCopy
	}
	// ADR-091 I-4: "gains one optional field, child_session_id, so the
	// open control knows where to go". Best-effort extraction from the
	// persisted tool-call result — the delegate/create_task tool's own
	// result shape is defined elsewhere; a missing/differently-keyed
	// result simply leaves ChildSessionId nil (the open control degrades
	// gracefully, per the field's own "optional" contract).
	if tc.Result != nil {
		if sid, ok := tc.Result["session_id"].(string); ok && sid != "" {
			sidCopy := sid
			sr.subStart.ChildSessionId = &sidCopy
		}
	}
}

// buildSubagentEnd builds the terminal frame for a delegated subagent span.
func (sr *streamReplayState) buildSubagentEnd(tc session.ToolCall) {
	spanDurationMS := int(tc.DurationMS)
	sr.subEnd = generated.SubagentEndFrame{
		Type:       string(generated.WsFrameTypeSubagentEnd),
		SessionId:  sr.sessionID,
		SpanId:     sr.spanID,
		DurationMs: &spanDurationMS,
		Status:     resolveStatus(tc.Status),
	}
	if sr.spanAgentID != "" {
		agentIDCopy := sr.spanAgentID
		sr.subEnd.AgentId = &agentIDCopy
	}
}

// buildAskUserQuestionFrame wraps a terminal AskUserQuestion record in the
// same ask_user_question frame shape the live terminal emission broadcast
// (askUserCardSink → broadcastAskUserCard), so replay parity with a live
// collapse is exact. The delay argument to toAskUserCard is irrelevant for a
// terminal card — default_safe_at is only materialized while Status is
// pending — so the production constant is passed unconditionally.
func buildAskUserQuestionFrame(set *askuser.PendingSet) generated.AskUserQuestionFrame {
	return generated.AskUserQuestionFrame{
		Type: string(generated.WsFrameTypeAskUserQuestion),
		Card: toAskUserCard(set, askuser.DefaultSafeDelay),
	}
}

// loadTerminalAskRecord reads the session's persisted AskUserQuestion record
// (UnifiedMeta.PendingAskJSON, spec v3 §0.3/M-R2-1) and returns it when — and
// only when — it is TERMINAL (answered/cancelled): that record is what the
// §0.6 collapsed card renders from on history reload, and streamReplay
// reconstructs it into the frame stream. A pending record returns nil (the
// live registry delivers those via session_state's pending_asks snapshot); a
// missing or corrupt record returns nil with a warning — replay proceeds
// without the collapsed card rather than aborting the whole attach.
func loadTerminalAskRecord(store *session.UnifiedStore, sessionID string) *askuser.PendingSet {
	if store == nil {
		return nil
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil || meta.PendingAskJSON == "" {
		return nil
	}
	var set askuser.PendingSet
	if uerr := json.Unmarshal([]byte(meta.PendingAskJSON), &set); uerr != nil {
		slog.Warn("replay: corrupt pending_ask record — skipping collapsed-card reconstruction",
			"session_id", sessionID, "error", uerr)
		return nil
	}
	if set.Status == askuser.StatusPending {
		return nil
	}
	return &set
}

// buildMediaFrame returns a generated.MediaFrame reconstructed from a
// transcript ToolCall, or ok=false when the call has no persisted media
// descriptors. The agent loop persists media as
// tc.Result["media"] = []map[string]any{{"ref","filename","content_type","type"}}
// so replay can re-emit attachments without re-resolving the MediaStore.
func buildMediaFrame(
	sessionID string,
	tc session.ToolCall,
	mediaStore media.MediaStore,
	seenPaths map[string]struct{},
) (generated.MediaFrame, bool) {
	raw, ok := tc.Result["media"]
	if !ok {
		return generated.MediaFrame{}, false
	}
	list, ok := raw.([]any)
	if !ok {
		return generated.MediaFrame{}, false
	}
	parts := make([]generated.MediaPart, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		refStr, _ := m["ref"].(string)
		if refStr == "" {
			continue
		}
		filename, _ := m["filename"].(string)
		contentType, _ := m["content_type"].(string)
		mediaType, _ := m["type"].(string)
		// The wire URL form mirrors webchat_channel.SendMedia.
		const refPrefix = "media://"
		if len(refStr) <= len(refPrefix) || refStr[:len(refPrefix)] != refPrefix {
			continue
		}
		// Dedup by underlying file path: an old transcript may have two
		// distinct refs pointing at the same on-disk file (one from
		// browser.screenshot's inline-data extraction, another from
		// send_file). Replaying both produces a duplicate image in the
		// SPA. Skip refs whose path was already emitted in this replay.
		if mediaStore != nil && seenPaths != nil {
			if path, err := mediaStore.ResolveWithOpts(refStr, media.ResolveOpts{}); err == nil && path != "" {
				if _, dup := seenPaths[path]; dup {
					continue
				}
				seenPaths[path] = struct{}{}
			}
		}
		parts = append(parts, generated.MediaPart{
			Type:        mediaType,
			Url:         mediaRefURL(refStr),
			Filename:    filename,
			ContentType: contentType,
		})
	}
	if len(parts) == 0 {
		return generated.MediaFrame{}, false
	}
	return generated.MediaFrame{
		Type:      string(generated.WsFrameTypeMedia),
		SessionId: sessionID,
		Parts:     parts,
	}, true
}

// buildSpawnIDsWithChildren scans all entries and returns the set of spawn
// tool call IDs that have at least one child (another tool call carrying that
// ID as ParentToolCallID).  This is used to determine whether to bracket a
// spawn with subagent_start / subagent_end.
//
// Two-pass approach: pass 1 collects isSpawn (spawn IDs seen in the transcript),
// pass 2 collects withChildren (spawn IDs that have at least one child).
// Returning withChildren directly eliminates the three-map + false-sentinel pattern.
//
// ADR-036 (2026-07-04) renamed the async delegation tool from "spawn" to the
// unified "delegate" (merged with run_subagent/check_spawn_status). Historical
// transcripts recorded before the merge still carry tool=="spawn" — this must
// keep matching for those sessions to replay correctly. New transcripts carry
// tool=="delegate" instead, so both names are checked here.
// ADR-091 D7/I-4 adds "create_task": a task child is bracketed by the same
// subagent span rule as a delegate child now (both fronts, one launcher).
func buildSpawnIDsWithChildren(entries []session.TranscriptEntry) map[string]bool {
	// Pass 1: collect all spawn/delegate/create_task tool call IDs.
	isSpawn := make(map[string]struct{})
	for _, entry := range entries {
		for _, tc := range entry.ToolCalls {
			if (tc.Tool == "spawn" || tc.Tool == "delegate" || tc.Tool == "create_task") && tc.ID != "" {
				isSpawn[string(tc.ID)] = struct{}{}
			}
		}
	}
	// Pass 2: mark spawn IDs that have at least one child.
	withChildren := make(map[string]bool)
	for _, entry := range entries {
		for _, tc := range entry.ToolCalls {
			if tc.ParentToolCallID != "" {
				parentID := string(tc.ParentToolCallID)
				if _, ok := isSpawn[parentID]; ok {
					withChildren[parentID] = true
				}
			}
		}
	}
	return withChildren
}

// buildSpanRealAgentIDs maps each spawn/delegate ToolCall.ID present in
// withChildren to the REAL delegate agent's own ID — resolved from the
// AgentID recorded on its FIRST nested child tool call (document order),
// i.e. the earliest transcript entry whose ParentToolCallID equals that
// span's ID.
//
// This exists because the OUTER spawn/delegate ToolCall's own transcript
// entry.AgentID is written by the PARENT turn (turnState.
// appendToolCallTranscript's ts.resolveActiveAgentID(), called when the
// parent's "delegate" tool call itself completes) — it reflects the
// delegator's identity, never the delegate's. A nested CHILD tool call, by
// contrast, is appended by the CHILD sub-turn's own turnState — per ADR-032
// ("no inheritance from the parent"), childTS.agentID is the resolved
// DELEGATE's real identity, so a child entry's AgentID is the correct
// source. Live rendering already gets this right independently:
// pkg/agent/subturn.go's spawnSubTurn stamps SubTurnSpawnPayload.AgentID :=
// childTS.agentID directly, so the live subagent_start/subagent_end frames
// (pkg/gateway/websocket.go's eventForwarder) always carry the delegate's
// own identity. Without this helper, replay instead fell back to
// entry.AgentID (the delegator) or lastSeenAgentID for the span-level
// subagent_start/subagent_end AgentID — a live/replay mismatch that made a
// delegation's specialized, per-agent span widget mismatch or fail to
// render correctly after a reload, even though the flat "Delegate task"
// pill (which intentionally keeps the delegator's own attribution) stayed
// correct.
//
// A span with no children is never bracketed at all (see
// buildSpawnIDsWithChildren), so this only needs to cover IDs already
// present in withChildren.
func buildSpanRealAgentIDs(entries []session.TranscriptEntry, withChildren map[string]bool) map[string]string {
	if len(withChildren) == 0 {
		return nil
	}
	realAgentIDs := make(map[string]string, len(withChildren))
	for _, entry := range entries {
		for _, tc := range entry.ToolCalls {
			if tc.ParentToolCallID == "" {
				continue
			}
			parentID := string(tc.ParentToolCallID)
			if !withChildren[parentID] {
				continue
			}
			if _, already := realAgentIDs[parentID]; already {
				continue
			}
			if entry.AgentID != "" {
				realAgentIDs[parentID] = entry.AgentID
			}
		}
	}
	return realAgentIDs
}

// replayedSubagentEnd copies a persisted subagent_end and rebuilds its span
// id. The session stamp stays with the caller, which knows which transcript
// is being read. Split out so dispatchSpecialEntry stays under the line budget.
func replayedSubagentEnd(entry session.TranscriptEntry) generated.SubagentEndFrame {
	frame := *entry.SubagentEnd
	parentCallID := ""
	if frame.ParentCallId != nil {
		parentCallID = *frame.ParentCallId
	}
	frame.SpanId = canonicalReplaySpanID(entry.ID, frame.SpanId, parentCallID)
	return frame
}

// followUpGenerationFromEntryID reads the generation a follow-up bracket
// recorded in its transcript entry id ("<callID>:g<N>:start" or ":end").
// Generation 1 keeps the historical "<callID>:start|end" form, which this
// returns 0 for. The wire frame has no generation field; the entry id is
// the persisted field that carries it.
func followUpGenerationFromEntryID(id string) int {
	const marker = ":g"
	i := strings.LastIndex(id, marker)
	if i < 0 {
		return 0
	}
	rest := id[i+len(marker):]
	colon := strings.IndexByte(rest, ':')
	if colon <= 0 {
		return 0
	}
	bracket := rest[colon+1:]
	if bracket != "start" && bracket != "end" {
		return 0
	}
	n, err := strconv.Atoi(rest[:colon])
	if err != nil || n < 2 {
		return 0
	}
	return n
}

// generationOneBracketEntry reports a production generation-1 start or end
// entry id, including one a later tidy-up suffixed. A follow-up entry id
// (":g<N>:") is not one of these.
func generationOneBracketEntry(id string) bool {
	if followUpGenerationFromEntryID(id) >= 2 {
		return false
	}
	return strings.HasSuffix(id, ":start") || strings.HasSuffix(id, ":end")
}

// canonicalReplaySpanID is the span id a cold load must emit for one
// persisted subagent_start or subagent_end. Generation N >= 2 is rebuilt
// with agent.SubagentSpanID so it matches the live push even when the
// stored span_id is the pre-fix bare id. Generation 1 is forced back to
// the bare id, so a later tidy-up that suffixes it cannot orphan spans
// already on disk. Anything else keeps the stored id.
func canonicalReplaySpanID(entryID, storedSpanID, parentCallID string) string {
	if parentCallID == "" {
		return storedSpanID
	}
	if gen := followUpGenerationFromEntryID(entryID); gen >= 2 {
		return agent.SubagentSpanID(parentCallID, gen)
	}
	if generationOneBracketEntry(entryID) {
		return agent.SubagentSpanID(parentCallID, 1)
	}
	return storedSpanID
}

// buildPersistedSubagentSpanIndexes scans entries once for every persisted
// subagent_start / subagent_end system entry (steer_frames.go's
// deliverSubagentStart/deliverSubagentEnd, ADR-091 D7/I-4) and returns the
// set of span IDs each carries — see streamReplayState.
// persistedSubagentStartSpans/persistedSubagentEndSpans's own doc comment
// (prepareReplay) for how classifyToolCall uses the two sets together.
func buildPersistedSubagentSpanIndexes(entries []session.TranscriptEntry) (starts map[string]bool, ends map[string]bool) {
	starts = make(map[string]bool)
	ends = make(map[string]bool)
	for _, entry := range entries {
		if entry.Type != session.EntryTypeSystem {
			continue
		}
		switch entry.SystemSubtype {
		case session.SystemSubtypeSubagentStart:
			if entry.SubagentStart != nil {
				if id := canonicalReplaySpanID(entry.ID, entry.SubagentStart.SpanId, entry.SubagentStart.ParentCallId); id != "" {
					starts[id] = true
				}
			}
		case session.SystemSubtypeSubagentEnd:
			if entry.SubagentEnd != nil {
				parentCallID := ""
				if entry.SubagentEnd.ParentCallId != nil {
					parentCallID = *entry.SubagentEnd.ParentCallId
				}
				if id := canonicalReplaySpanID(entry.ID, entry.SubagentEnd.SpanId, parentCallID); id != "" {
					ends[id] = true
				}
			}
		}
	}
	return starts, ends
}

type tcAddr struct{ entryIdx, tcIdx int }

// Delegated/task child steps stay in the child's own store-backed session.
// Parent replay reconstructs only the persisted lifecycle span around that
// session; it never searches the parent's transcript for child tool calls.

// applyPersistedFailureReason restores live/replay parity for a failed tool
// call's reason. It is shared by BOTH frame builders deliberately: they are
// the two places replay reconstructs a ToolCallResultFrame, and it has been
// re-established in only one of them TWICE: RC-5c, and W5's own first cut,
// which fixed the top-level builder and left the nested one — the
// delegated-worker path — untouched. (An earlier version of this comment said
// three and counted RC-5; that change is in loop.go's persistence write and
// touches neither builder.)
//
// Two things happen here:
//
//  1. RC-5c: copy the persisted reason onto Error. The live path populates it;
//     without this a reload silently drops error context that was visible
//     during the turn. tc.Error is persisted for every failed tool call
//     (loop.go's RC-5 write), not just delegation denials.
//
//  2. ADR-059 W5: a STRUCTURED failure payload (a denied delegation, a
//     write_file precondition refusal) is persisted as the raw JSON string
//     (verbatim up to the 2000-rune cap, which the producers bound their
//     fields to stay under), because the persisted value is contentForLLM and
//     these tools' contentForLLM IS the JSON. Parse it into the object the
//     live path
//     delivers and lift the prose reason into Error, so a reload does not show
//     a JSON blob where the live view showed a sentence.
func applyPersistedFailureReason(f *generated.ToolCallResultFrame, tc session.ToolCall) {
	if tc.Error == "" {
		return
	}
	errCopy := tc.Error
	f.Error = &errCopy

	obj, reason, isStructured := parseStructuredToolFailure(tc.Error)
	if !isStructured {
		return
	}
	// tc.Result == nil, NOT f.Result == nil. truncateResult returns tc.Result
	// unchanged when it is nil, and a nil map[string]any boxed into an `any`
	// is a non-nil interface — so the obvious check silently never fires and
	// this would be dead code that still compiles and still looks right.
	//
	// Only fills Result when nothing richer is already there: a call carrying
	// media descriptors or a sync-delegate payload keeps its own shape.
	if tc.Result == nil {
		f.Result = obj
	}
	if reason != "" {
		reasonCopy := reason
		f.Error = &reasonCopy
	}
}

// truncateResult JSON-encodes tc.Result and applies the two-tier size policy:
//
//  1. <= InlineToolResultMaxBytes (50 KiB): inline — return tc.Result unchanged.
//  2. > InlineToolResultMaxBytes and <= replayMaxResultBytes (1 MiB): offload to
//     toolStore if available, emit generated.ToolResultRef sentinel.  When
//     toolStore is nil or the write fails, fall through to inline (which is then
//     capped at 1 MiB by the next check).
//  3. > replayMaxResultBytes (1 MiB): truncate — emit TruncatedResult sentinel
//     (FR-I-011).
//
// Returns the value to place in the ToolCallResultFrame's result field.
func truncateResult(sessionID string, tc session.ToolCall, toolStore *toolResultStore) any {
	if tc.Result == nil {
		return tc.Result
	}
	encoded, err := json.Marshal(tc.Result)
	if err != nil {
		// Marshal failure: return a sentinel map so the downstream WS encoder always
		// succeeds. Passing the raw value through would cause an identical failure at
		// the next marshal site, silently corrupting the replay frame.
		slog.Error("replay: tool_call_result marshal failed — emitting sentinel",
			"event", "replay_result_marshal_error",
			"session_id", sessionID,
			"tool_call_id", string(tc.ID),
			"error", err,
		)
		return map[string]any{"_marshal_error": err.Error()}
	}

	// Tier 2: offload to disk when size is in (50 KiB, 1 MiB].
	if sentinel, offloaded := maybeOffloadResult(toolStore, sessionID, encoded); offloaded {
		return sentinel
	}

	// Tier 1 or store unavailable/disabled: inline (< 50 KiB, or store write failed).
	if len(encoded) <= replayMaxResultBytes {
		return tc.Result
	}

	// Tier 3: hard cap exceeded — truncate (FR-I-011).
	originalSize := len(encoded)
	preview := encoded
	if len(preview) > replayResultPreviewBytes {
		preview = encoded[:replayResultPreviewBytes]
	}
	slog.Warn("replay: tool_call_result exceeds 1 MiB — truncating",
		"event", "replay_result_truncated",
		"session_id", sessionID,
		"tool_call_id", string(tc.ID),
		"original_size_bytes", originalSize,
	)
	return map[string]any{
		"_truncated":          true,
		"original_size_bytes": originalSize,
		"preview":             string(preview),
	}
}

// resolveStatus normalises an empty status string to "success".
func resolveStatus(s string) string {
	if s == "" {
		return "success"
	}
	return s
}

// toolCallResultStatus normalises a persisted session.ToolCall.Status onto
// ToolCallResultFrame's strict wire enum (success/error only —
// ToolCallResultFrame.yaml has no "interrupted" value; only the richer
// SubagentEndFrame.status enum does). pkg/agent/loop.go's tcStatus
// derivation can now persist "interrupted" for a synchronous delegate call
// canceled by its parent (Finding F / A-I4 round 5, ToolResult.Interrupted),
// so tc.Status is no longer guaranteed to be one of ToolCallResultFrame's
// two allowed values — passing it through resolveStatus verbatim, as this
// function replaces at every ToolCallResultFrame call site, would emit a
// contract-invalid frame the SPA's isValidFrame() drops. Any non-empty,
// non-"success" value (error, interrupted, canceled, timeout, ...) reads as
// "error" here, exactly matching what the LIVE EventKindToolExecEnd handler
// already does for the same outer call — IsError is a plain bool there too,
// with no room for a third state — so this clamp changes nothing about the
// OUTER tool_call_result badge's live/reload parity; only the SPAN's own
// subagent_end frame (built via resolveStatus, unclamped) is meant to ever
// show "interrupted".
func toolCallResultStatus(s string) string {
	if s == "" || s == "success" {
		return resolveStatus(s)
	}
	return "error"
}

// turnCancelledContent builds the required `content` string for a
// role:"turn_canceled" ReplayMessageFrame. The SPA treats turn_canceled as
// metadata-only (contracts/components/schemas/ReplayMessageFrame.yaml: "skips
// turn_canceled" — no chat bubble is rendered for it), but `content` is still
// a required field on the frame, so this returns a short human-readable
// description rather than an empty string, for operator-facing traces (WS
// debug logs, future consumers) — entry.Content itself is always empty for
// EntryTypeTurnCancelled (pkg/agent/cancel.go's literal never sets it).
func turnCancelledContent(entry session.TranscriptEntry) string {
	if entry.CancelMethod != "" {
		return fmt.Sprintf("Turn canceled (%s)", entry.CancelMethod)
	}
	return "Turn canceled"
}

// resolveErrorKind maps an error transcript entry to the wire-level `kind`
// discriminant consumed by the SPA's ReplayErrorFrame reducer.
//
// Transcript entries written by appendErrorTranscript currently do not carry
// the originating EventKind ("error" vs "rate_limit") — only the human-readable
// Content string is persisted. Until the producer is upgraded to write a typed
// Status enum (tracked by W2-15), we infer the kind from the Content prefix
// that the two paths use:
//
//   - "rate limit: …" → "rate_limit" (recordRateLimitDenial)
//   - anything else   → "error"      (LLM call failure paths)
//
// The two producers are stable in pkg/agent/turn.go (appendErrorTranscript)
// and pkg/agent/loop.go (recordRateLimitDenial + the two LLM call error
// sites). The heuristic is documented at both ends so a future refactor that
// adds a Kind field to TranscriptEntry can swap this for a direct lookup
// without touching the wire contract.
func resolveErrorKind(content string) string {
	if strings.HasPrefix(content, "rate limit:") {
		return "rate_limit"
	}
	return "error"
}

// buildReplayErrorFrame constructs a generated.ReplayErrorFrame from a
// TranscriptEntry that the loop identified as a system-error entry
// (Type=system + Status="error"). Phase 1B (FR-014) — replaces the previous
// behavior of emitting a ReplayMessageFrame with an empty Role, which the
// SPA would render as a regular assistant bubble.
func buildReplayErrorFrame(sessionID string, entry session.TranscriptEntry) generated.ReplayErrorFrame {
	frame := generated.ReplayErrorFrame{
		Type:      string(generated.WsFrameTypeReplayError),
		SessionId: sessionID,
		EntryId:   entry.ID,
		// Format as RFC 3339 (matches AsyncAPI `format: date-time`); TranscriptEntry.Timestamp
		// is a time.Time and JSON-marshals to RFC 3339 by default.
		Timestamp: entry.Timestamp.UTC().Format(time.RFC3339Nano),
		Kind:      resolveErrorKind(entry.Content),
		Message:   entry.Content,
	}
	if entry.AgentID != "" {
		agentIDCopy := entry.AgentID
		frame.AgentId = &agentIDCopy
	}
	if entry.ErrorCode != "" {
		frame.Payload = &generated.ReplayErrorPayload{
			LlmError: generated.LLMErrorReplay{
				Code:      entry.ErrorCode,
				Message:   entry.Content,
				Retryable: entry.ErrorRetryable,
			},
		}
	}
	return frame
}

// toJudgeVerdictFrame converts an internal task.JudgeVerdict into the
// generated asyncapi wire frame (review r2 RV1). Mirrors
// rest_tasks.go's toWireJudgeVerdict field-for-field — duplicated rather than
// shared because the two callers target different generated types
// (gen.JudgeVerdict, the openapi Message.verdict shape, vs.
// generated.JudgeVerdictFrame, the asyncapi WS frame shape); both live in the
// same pkg/api/generated package but are distinct generated structs.
// sessionID is the verdict's owning chat session — the task run session for
// scope=task, the /goal session for scope=goal, "" for scope=plan (a plan
// round has no single owning chat session; plan-scope verdicts pass "" here,
// same as before this field existed). Stamped onto the frame's OPTIONAL
// session_id (JudgeVerdictFrame.yaml) so the SPA can anchor the card in that
// specific chat thread in addition to the GLOBAL ActivityPanel; a frame
// without it keeps the pre-existing panel-only routing exactly as before.
func toJudgeVerdictFrame(sessionID string, v task.JudgeVerdict) generated.JudgeVerdictFrame {
	f := generated.JudgeVerdictFrame{
		Type:         string(generated.WsFrameTypeJudgeVerdict),
		Id:           v.ID,
		Scope:        v.Scope,
		Round:        v.Round,
		Met:          v.Met,
		Model:        v.Model,
		JudgedAt:     v.JudgedAt,
		JudgeAgentId: v.JudgeAgentID,
	}
	if v.TaskID != "" {
		taskIDCopy := v.TaskID
		f.TaskId = &taskIDCopy
	}
	if v.PlanID != "" {
		planIDCopy := v.PlanID
		f.PlanId = &planIDCopy
	}
	if sessionID != "" {
		sessionIDCopy := sessionID
		f.SessionId = &sessionIDCopy
	}
	// Fix-wave finding #3: PerCriterion is a required array on the wire
	// (asyncapi_types.gen.go, no `omitempty`) — a nil slice marshals as JSON
	// `null`, which fails the SPA's zod schema for a required array and gets
	// dropped. An empty (zero-criteria) verdict must still round-trip as `[]`,
	// so start from a non-nil, empty slice rather than appending onto a nil
	// one.
	//
	// JUDGE-FR-070a/FR-074 (C-02, F2): four new optional fields —
	// evidence_source, evidence_target, provenance, evidence[] — mirror
	// task.CriterionVerdict's new Go fields onto the generated wire shape.
	// Deliberately no `outcome` field anywhere (D-H, C-02) — Met stays the
	// only verdict-shape bool. A pre-existing verdict, whose new Go fields
	// are all zero-valued, produces nil pointers/nil slice here, which
	// `omitempty` drops from the JSON exactly as before this change
	// (FR-074): the wire frame for an old verdict is unchanged byte-for-byte.
	f.PerCriterion = make([]struct {
		CriterionId string `json:"criterion_id"`
		Evidence    []struct {
			Part   string  `json:"part"`
			Quote  string  `json:"quote"`
			Source *string `json:"source,omitempty"`
			Target *string `json:"target,omitempty"`
		} `json:"evidence,omitempty"`
		EvidenceQuote  *string `json:"evidence_quote,omitempty"`
		EvidenceSource *string `json:"evidence_source,omitempty"`
		EvidenceTarget *string `json:"evidence_target,omitempty"`
		Met            bool    `json:"met"`
		Provenance     *string `json:"provenance,omitempty"`
		Reason         string  `json:"reason"`
	}, 0, len(v.PerCriterion))
	for _, c := range v.PerCriterion {
		// ADR-074 D7: optional + empty-safe — an empty quote (fail-closed /
		// pre-D7 verdicts) stays absent from the wire, never "".
		var quote *string
		if c.EvidenceQuote != "" {
			q := c.EvidenceQuote
			quote = &q
		}
		var evidenceSource *string
		if c.EvidenceSource != "" {
			s := string(c.EvidenceSource)
			evidenceSource = &s
		}
		var evidenceTarget *string
		if c.EvidenceTarget != "" {
			t := c.EvidenceTarget
			evidenceTarget = &t
		}
		var provenance *string
		if c.Provenance != "" {
			p := string(c.Provenance)
			provenance = &p
		}
		var evidence []struct {
			Part   string  `json:"part"`
			Quote  string  `json:"quote"`
			Source *string `json:"source,omitempty"`
			Target *string `json:"target,omitempty"`
		}
		if len(c.Evidence) > 0 {
			evidence = make([]struct {
				Part   string  `json:"part"`
				Quote  string  `json:"quote"`
				Source *string `json:"source,omitempty"`
				Target *string `json:"target,omitempty"`
			}, 0, len(c.Evidence))
			for _, e := range c.Evidence {
				var src *string
				if e.Source != "" {
					s := e.Source
					src = &s
				}
				var tgt *string
				if e.Target != "" {
					t := e.Target
					tgt = &t
				}
				evidence = append(evidence, struct {
					Part   string  `json:"part"`
					Quote  string  `json:"quote"`
					Source *string `json:"source,omitempty"`
					Target *string `json:"target,omitempty"`
				}{Part: e.Part, Quote: e.Quote, Source: src, Target: tgt})
			}
		}
		f.PerCriterion = append(f.PerCriterion, struct {
			CriterionId string `json:"criterion_id"`
			Evidence    []struct {
				Part   string  `json:"part"`
				Quote  string  `json:"quote"`
				Source *string `json:"source,omitempty"`
				Target *string `json:"target,omitempty"`
			} `json:"evidence,omitempty"`
			EvidenceQuote  *string `json:"evidence_quote,omitempty"`
			EvidenceSource *string `json:"evidence_source,omitempty"`
			EvidenceTarget *string `json:"evidence_target,omitempty"`
			Met            bool    `json:"met"`
			Provenance     *string `json:"provenance,omitempty"`
			Reason         string  `json:"reason"`
		}{
			CriterionId:    c.CriterionID,
			Evidence:       evidence,
			EvidenceQuote:  quote,
			EvidenceSource: evidenceSource,
			EvidenceTarget: evidenceTarget,
			Met:            c.Met,
			Provenance:     provenance,
			Reason:         c.Reason,
		})
	}
	return f
}

// parseRetryAfterSeconds extracts a "(retry after Ns)" parenthetical from a
// rate-limit error message. Returns nil when the parenthetical is absent or
// unparseable so the SPA falls back to its default retry display.
func parseRetryAfterSeconds(content string) *float64 {
	open := strings.LastIndex(content, "(retry after ")
	if open < 0 {
		return nil
	}
	closeIdx := strings.Index(content[open:], "s)")
	if closeIdx < 0 {
		return nil
	}
	numStr := content[open+len("(retry after ") : open+closeIdx]
	f, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return nil
	}
	return &f
}

// resolveTaskLabel extracts the task label from a spawn tool call's parameters.
// Prefers Parameters["label"]; falls back to Parameters["task"] truncated at 60 chars.
func resolveTaskLabel(tc session.ToolCall) string {
	if tc.Parameters == nil {
		return ""
	}
	if label, ok := tc.Parameters["label"].(string); ok && label != "" {
		return label
	}
	if task, ok := tc.Parameters["task"].(string); ok {
		runes := []rune(task)
		if len(runes) > 60 {
			return string(runes[:60])
		}
		return task
	}
	return ""
}

// replayStats aggregates metrics from a set of transcript entries for slog.Info.
type replayStats struct {
	toolCallCount            int
	spanCount                int
	orphanCount              int // tool calls whose ParentToolCallID has no matching spawn-with-children
	duplicateToolCallIDCount int // tool_call_ids that appear more than once across entries
	truncatedResultCount     int // tool call results that exceeded replayMaxResultBytes
}

// computeReplayStats scans entries and returns aggregate metrics.
// streamReplay accepts the pre-computed stats via its signature so the
// spawnIDsWithChildren map is not rebuilt redundantly on every call.
func computeReplayStats(entries []session.TranscriptEntry) replayStats {
	var rs replayStats
	spawnIDsWithChildren := buildSpawnIDsWithChildren(entries)
	// Finding C (A-I4 round 4): count every delegate/spawn call here, not just
	// ones with at least one recorded nested child (spawnIDsWithChildren) —
	// streamReplay now brackets ALL of them with subagent_start/end (see its
	// isSpawnParent), so this diagnostic (surfaced as span_count_detected in
	// operator logs) must match what actually gets emitted, or a genuinely
	// bracketed "0 steps" delegate call under-reports as if no span existed
	// at all.
	seenSpawnIDs := make(map[string]bool)
	for _, entry := range entries {
		for _, tc := range entry.ToolCalls {
			if (tc.Tool == "spawn" || tc.Tool == "delegate" || tc.Tool == "create_task") && tc.ID != "" {
				seenSpawnIDs[string(tc.ID)] = true
			}
		}
	}
	rs.spanCount = len(seenSpawnIDs)

	// Count duplicates: seenIDs tracks first occurrence; a second hit increments the counter.
	seenIDs := make(map[string]bool, len(entries))
	for _, entry := range entries {
		for _, tc := range entry.ToolCalls {
			rs.toolCallCount++
			if tc.ID != "" {
				tcID := string(tc.ID)
				if seenIDs[tcID] {
					rs.duplicateToolCallIDCount++
				} else {
					seenIDs[tcID] = true
				}
			}
			// Orphan: nested but parent not in spawnIDsWithChildren.
			if tc.ParentToolCallID != "" && !spawnIDsWithChildren[string(tc.ParentToolCallID)] {
				rs.orphanCount++
			}
			// Truncated: would the result exceed the limit?
			if tc.Result != nil {
				if encoded, merr := json.Marshal(tc.Result); merr == nil && len(encoded) > replayMaxResultBytes {
					rs.truncatedResultCount++
				}
			}
		}
	}
	return rs
}

// wsEmitFunc returns an emit function that marshals any generated frame type
// and queues it on wc for an attach's catch-up: it bypasses hold mode (the
// catch-up goes ahead of the held live frames) and is flow-controlled by the
// socket (directWait), respecting context cancellation.
func wsEmitFunc(ctx context.Context, wc *wsConn) func(any) error {
	return func(f any) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		data, err := json.Marshal(f)
		if err != nil {
			return err
		}
		return wc.directWait(ctx, data)
	}
}
