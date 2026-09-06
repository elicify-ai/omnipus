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
	isSpanActive func(parentSpawnCallID string) bool,
	terminalAsk *askuser.PendingSet,
) (framesEmitted int, err error) {
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
	if terminalAsk != nil && terminalAsk.Status == askuser.StatusPending {
		terminalAsk = nil
	}
	terminalAskEmitted := false
	// isSpanActive lets a caller (handleAttachSession, wired to
	// agent.AgentLoop.IsSubTurnActiveForSpawnCall) tell replay that a given
	// spawn/delegate ToolCall's real sub-turn is STILL genuinely running,
	// even though its persisted record may carry a placeholder terminal
	// status (async delegation writes Status="success", DurationMS≈0 the
	// instant the spawning call returns — see
	// session.UnifiedStore.UpdateToolCallStatus's doc comment — well before
	// the delegate itself finishes). When isSpanActive reports true for a
	// call, streamReplay withholds that call's own terminal frame(s)
	// (tool_call_result / subagent_end) so the client is never shown a
	// fabricated "done" for a turn that has not actually completed — it
	// sees only tool_call_start / subagent_start, the same "started, no
	// result yet" shape a genuinely in-flight LIVE call would show. The
	// real completion then arrives over the live WS event stream once the
	// sub-turn actually finishes. nil is treated as "nothing is active"
	// (never withhold) — callers that have no liveness authority (e.g.
	// tests) can pass nil safely.
	if isSpanActive == nil {
		isSpanActive = func(string) bool { return false }
	}

	// Track underlying file paths already emitted so the SPA never receives
	// two media frames for the same file. Older transcripts can carry
	// multiple media:// refs pointing at the same on-disk file (browser.
	// screenshot stored an inline copy AND send_file registered a second
	// ref before the RefByPath dedup landed). Without this guard, both
	// frames replay and the user sees the screenshot twice.
	seenPaths := make(map[string]struct{})
	// ── Pass 1: build ancillary indexes ─────────────────────────────────────

	// spawnIDsPresent: set of ToolCall.IDs where tool == "spawn" or "delegate"
	// AND at least one other tool call in the transcript has ParentToolCallID
	// == that ID. This is the signal that the parent span has live children
	// to bracket. See buildSpawnIDsWithChildren's own doc comment below for
	// why both tool names are checked (ADR-036 spawn→delegate rename).
	spawnIDsWithChildren := buildSpawnIDsWithChildren(entries)

	// spanRealAgentIDs maps a spawn/delegate ToolCall.ID (that has at least
	// one child) to the REAL delegate agent's own ID, resolved from its
	// first nested child tool call's own transcript entry. See
	// buildSpanRealAgentIDs' doc comment for why this differs from — and is
	// more correct than — entry.AgentID on the OUTER spawn/delegate call.
	spanRealAgentIDs := buildSpanRealAgentIDs(entries, spawnIDsWithChildren)

	// deduped: for each ToolCall.ID keep only the index of the last occurrence
	// across ALL entries.  key = ToolCall.ID, value = (entryIdx, tcIdx).
	latestByID := make(map[string]tcAddr)
	for ei, entry := range entries {
		for ti, tc := range entry.ToolCalls {
			if tc.ID == "" {
				continue
			}
			if prev, dup := latestByID[string(tc.ID)]; dup {
				// Duplicate detected — log the previous address for diagnostics; last occurrence wins.
				slog.Warn("replay: duplicate tool_call_id detected — only latest will emit",
					"previous_entry_index", prev.entryIdx,
					"previous_tool_index", prev.tcIdx,
					"event", "replay_duplicate_tool_call_id",
					"session_id", sessionID,
					"tool_call_id", string(tc.ID),
				)
			}
			latestByID[string(tc.ID)] = tcAddr{ei, ti}
		}
	}

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
		params := tc.Parameters
		if params == nil {
			params = map[string]any{}
		}
		f := generated.ToolCallStartFrame{
			Type:      string(generated.WsFrameTypeToolCallStart),
			SessionId: sessionID,
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

	// buildResult returns a generated.ToolCallResultFrame for tc.
	buildResult := func(tc session.ToolCall, agentID, parentCallID string) generated.ToolCallResultFrame {
		resultPayload := truncateResult(sessionID, tc, toolStore)
		durationMs := int(tc.DurationMS)
		f := generated.ToolCallResultFrame{
			Type:       string(generated.WsFrameTypeToolCallResult),
			SessionId:  sessionID,
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

	// lastSeenAgentID tracks the most recent non-empty AgentID across entries.
	// Used as fallback when a spawn entry has an empty AgentID.
	lastSeenAgentID := ""

	for ei, entry := range entries {
		// FR-I-006: skip compaction entries.
		if entry.Type == session.EntryTypeCompaction {
			continue
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
				SessionId: sessionID,
				Role:      "turn_canceled",
				Content:   turnCancelledContent(entry),
			}
			if entry.TurnID != "" {
				turnIDCopy := entry.TurnID
				cancelFrame.TurnId = &turnIDCopy
			}
			if err2 := emitFrame(cancelFrame); err2 != nil {
				return framesEmitted, err2
			}
			continue
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
					"session_id", sessionID, "entry_id", entry.ID, "error", uerr)
				continue
			}
			if err2 := emitFrame(toJudgeVerdictFrame(verdict)); err2 != nil {
				return framesEmitted, err2
			}
			continue
		}

		// Update the running fallback agent ID.
		if entry.AgentID != "" {
			lastSeenAgentID = entry.AgentID
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
				if terminalAsk != nil && !terminalAskEmitted && terminalAsk.CardID == cardID {
					terminalAskEmitted = true
					if err2 := emitFrame(buildAskUserQuestionFrame(terminalAsk)); err2 != nil {
						return framesEmitted, err2
					}
				}
				continue
			}
		}

		// FR-I-002: emit replay_message for non-empty content.
		if entry.Content != "" {
			// Phase 1B (FR-014): system-error entries (Type=system + Status="error")
			// are emitted as ReplayErrorFrame so the SPA can render the typed
			// rate-limit-denial or generic error component. Without this, the
			// empty Role would fall through to the assistant render path and the
			// rate-limit text would render as a regular assistant bubble.
			if entry.Type == session.EntryTypeSystem && entry.Status == "error" {
				if err2 := emitFrame(buildReplayErrorFrame(sessionID, entry)); err2 != nil {
					return framesEmitted, err2
				}
				continue
			}
			msgFrame := generated.ReplayMessageFrame{
				Type:      string(generated.WsFrameTypeReplayMessage),
				SessionId: sessionID,
				Role:      entry.Role,
				Content:   entry.Content,
			}
			if entry.AgentID != "" {
				agentIDCopy := entry.AgentID
				msgFrame.AgentId = &agentIDCopy
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
				msgFrame.TurnId = &turnIDCopy
			}
			// Phase 1B (FR-013/FR-014): surface per-turn model. Populated from
			// TranscriptEntry.Model on every assistant message written via
			// pkg/agent/turn.go since Phase 1B landed. Empty for legacy turns;
			// the UI omits the model field entirely (no placeholder) for those
			// entries — see MessageItem.tsx model-footer rendering (FR-014).
			if entry.Model != "" {
				modelCopy := entry.Model
				msgFrame.Model = &modelCopy
			}
			if err2 := emitFrame(msgFrame); err2 != nil {
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
					SessionId: sessionID,
				}
				agentIDCopy := entry.AgentID
				switchF.AgentId = &agentIDCopy
				if err2 := emitFrame(switchF); err2 != nil {
					return framesEmitted, err2
				}
			}
		}

		// FR-I-001: emit tool_call_start + tool_call_result for each ToolCall.
		for ti, tc := range entry.ToolCalls {
			if tc.ID == "" {
				continue
			}
			tcID := string(tc.ID)
			tcParentID := string(tc.ParentToolCallID)
			// Dedup: skip if this is not the latest occurrence.
			if latest := latestByID[tcID]; latest.entryIdx != ei || latest.tcIdx != ti {
				continue
			}

			isNested := tcParentID != ""
			parentIsSpawn := isNested && spawnIDsWithChildren[tcParentID]
			isOrphan := isNested && !parentIsSpawn

			if isNested && parentIsSpawn {
				// This tool call will be emitted by emitNestedToolCalls when its
				// parent spawn is processed.  Skip it here to avoid double-emission.
				continue
			}

			if isOrphan {
				// FR-I-007: orphan — parent not found in transcript.
				slog.Warn("replay: orphan tool call — parent spawn not in transcript",
					"event", "replay_orphan",
					"session_id", sessionID,
					"parent_tool_call_id", tcParentID,
				)
				// The orphan is emitted as a flat tool call (no ParentCallID on the wire).
				// This causes the client to take the non-nested rendering path immediately
				// rather than waiting 10 s for the orphan TTL to expire.
				// The slog.Warn above records the full context for operator debugging.
			}

			// Resolve the effective agent ID for this tool call's frames.
			// If the spawn entry has an empty AgentID, fall back to the most recently
			// seen agent ID in the transcript so the span is never emitted with a blank agent_id.
			effectiveAgentID := entry.AgentID
			if effectiveAgentID == "" {
				effectiveAgentID = lastSeenAgentID
			}

			// isDelegateSpawnCall identifies a spawn/delegate tool call (the
			// two names checked mirror buildSpawnIDsWithChildren's own
			// ADR-036 rename note). Used below both to resolve span-level
			// agent-id and to gate the still-active liveness check — a
			// terminal snapshot is only ever withheld for THIS call kind,
			// never for an ordinary tool call.
			isDelegateSpawnCall := tc.Tool == "spawn" || tc.Tool == "delegate"

			// Finding C (A-I4 round 4): every spawn/delegate call gets a
			// subagent_start/subagent_end bracket on replay, matching live
			// unconditionally — pkg/agent/subturn.go's spawnSubTurn always
			// fires EventKindSubTurnSpawn/EventKindSubTurnEnd for a delegate
			// call regardless of how many tool calls the CHILD itself made,
			// so pkg/gateway/websocket.go's eventForwarder always emits a
			// live subagent_start/subagent_end pair too. This used to be
			// gated on spawnIDsWithChildren (spans requiring at least one
			// RECORDED NESTED CHILD tool call) — correct for deciding
			// whether emitNestedToolCalls has anything to emit, but wrong as
			// the gate for whether to bracket at all: a delegate whose child
			// replies directly with zero tool calls (a common case — many
			// delegated tasks are simple, no-tool Q&A, and it's also exactly
			// what a child interrupted before its first tool call looks
			// like) got NO span bracket whatsoever on reload, silently
			// dropping the nested "label, 0 steps, status, duration"
			// progress row live always shows, even though the outer call's
			// own Status/DurationMS are fully known and persisted either
			// way. isDelegateSpawnCall (above) is the correct test — it
			// doesn't require any children to exist; emitNestedToolCalls
			// below naturally emits zero nested frames when there are none.
			isSpawnParent := isDelegateSpawnCall

			// stillActive is true when isSpanActive (wired to
			// agent.AgentLoop.IsSubTurnActiveForSpawnCall) reports that this
			// spawn/delegate call's REAL sub-turn has not actually finished
			// yet, even though its persisted ToolCall record may already
			// carry a terminal-looking Status/DurationMS (async delegation's
			// placeholder ack — see streamReplay's isSpanActive doc comment
			// above). When true, this call's OWN terminal frame(s) —
			// tool_call_result / subagent_end — are withheld so the client
			// is never shown a fabricated "done" for a turn that is, in
			// truth, still working; the real completion arrives later over
			// the live WS event stream.
			stillActive := isDelegateSpawnCall && isSpanActive(tcID)

			if isSpawnParent {
				// Emit tool_call_start for the spawn call itself FIRST.
				if err2 := emitFrame(buildStart(tc, effectiveAgentID, "")); err2 != nil {
					return framesEmitted, err2
				}

				// Emit subagent_start to bracket nested frames.
				//
				// Span-level agent attribution: prefer the REAL delegate
				// agent's own ID (resolved from its first nested child's own
				// transcript entry, see buildSpanRealAgentIDs) over
				// effectiveAgentID, which reflects the PARENT's active agent
				// (the outer spawn/delegate ToolCall's own entry.AgentID is
				// written by the PARENT turn's appendToolCallTranscript, not
				// the child's). Live subagent_start/subagent_end frames
				// (pkg/gateway/websocket.go's eventForwarder) already carry
				// the child's real identity (SubTurnSpawnPayload.AgentID :=
				// childTS.agentID, pkg/agent/subturn.go) — this makes replay
				// match live instead of mislabeling the specialized
				// per-agent span with the delegator's own identity.
				spanID := "span_" + tcID
				taskLabel := resolveTaskLabel(tc)
				spanAgentID := effectiveAgentID
				if realAgentID, ok := spanRealAgentIDs[tcID]; ok && realAgentID != "" {
					spanAgentID = realAgentID
				}
				subStart := generated.SubagentStartFrame{
					Type:         string(generated.WsFrameTypeSubagentStart),
					SessionId:    sessionID,
					SpanId:       spanID,
					ParentCallId: tcID,
					TaskLabel:    taskLabel,
				}
				if spanAgentID != "" {
					agentIDCopy := spanAgentID
					subStart.AgentId = &agentIDCopy
				}
				if err2 := emitFrame(subStart); err2 != nil {
					return framesEmitted, err2
				}

				// Emit all nested tool calls (children with ParentToolCallID == tc.ID).
				// emitNestedToolCalls returns an aggregate (totalDurationMS,
				// aggregateStatus) computed by summing/rolling up its own child
				// tool calls — historically THIS CALLER (not the function
				// itself) used that aggregate to set the outer span's own
				// Status/DurationMs on the subagent_end frame below. Wave 3 fix
				// 5b replaced that with tc's own persisted Status/DurationMS
				// (see the comment there), so the aggregate is now discarded
				// here (`_, _`) — deliberately, not an oversight. This call is
				// still required regardless: it's what emits the individual
				// nested child tool_call_start/tool_call_result frames. These
				// are always emitted, even when stillActive — they are
				// historical, already-completed child tool calls; only the
				// OUTER span's own terminal frames are conditionally withheld
				// below.
				_, _, nestedErr := emitNestedToolCalls(
					ctx, sessionID, tcID, entries, latestByID, effectiveAgentID, emitFrame, toolStore,
				)
				if nestedErr != nil {
					return framesEmitted, nestedErr
				}

				if stillActive {
					// Withhold subagent_end + the outer tool_call_result: the
					// real sub-turn is still genuinely running. The client
					// already has tool_call_start + subagent_start for this
					// call from above, which is the same "started, no result
					// yet" shape a genuinely in-flight LIVE call shows.
					continue
				}

				// Emit subagent_end.
				//
				// Wave 3 fix 5b: the span's own Status/DurationMs are read directly
				// from tc — the spawn/delegate ToolCall's OWN persisted record —
				// instead of emitNestedToolCalls' aggregate over child tool calls.
				// pkg/agent/subturn.go's spawnSubTurn (EventKindSubTurnEnd) now
				// persists the sub-turn's REAL completion status/wall-clock
				// duration onto this exact record via session.UnifiedStore.
				// UpdateToolCallStatus once the child turn actually finishes. The
				// aggregate is a fundamentally different, incompatible semantic —
				// it flips to "error" whenever ANY child tool call was denied, even
				// when the sub-turn itself legitimately completed "success" at the
				// LLM level (a denied tool attempt is not itself a sub-turn
				// failure), and its duration is a sum of child tool-call durations
				// that structurally cannot reflect real wall-clock time. Live
				// rendering (pkg/gateway/websocket.go's EventKindSubTurnEnd
				// handler) has always used this real status/duration directly —
				// this makes replay match it.
				spanDurationMS := int(tc.DurationMS)
				subEnd := generated.SubagentEndFrame{
					Type:       string(generated.WsFrameTypeSubagentEnd),
					SessionId:  sessionID,
					SpanId:     spanID,
					DurationMs: &spanDurationMS,
					Status:     resolveStatus(tc.Status),
				}
				if spanAgentID != "" {
					agentIDCopy := spanAgentID
					subEnd.AgentId = &agentIDCopy
				}
				if err2 := emitFrame(subEnd); err2 != nil {
					return framesEmitted, err2
				}

				// Emit tool_call_result for the spawn call.
				if err2 := emitFrame(buildResult(tc, effectiveAgentID, "")); err2 != nil {
					return framesEmitted, err2
				}
				if mf, ok := buildMediaFrame(sessionID, tc, mediaStore, seenPaths); ok {
					if err2 := emitFrame(mf); err2 != nil {
						return framesEmitted, err2
					}
				}
				continue
			}

			// Regular (non-spawn, or nested) tool call: flat emission.
			// Orphan tool calls are emitted WITHOUT ParentCallID so the
			// client takes the flat non-nested path immediately (not after 10s TTL).
			parentForFlat := ""
			if isNested && !isOrphan {
				parentForFlat = tcParentID
			}
			if err2 := emitFrame(buildStart(tc, effectiveAgentID, parentForFlat)); err2 != nil {
				return framesEmitted, err2
			}
			if stillActive {
				// A spawn/delegate call whose real sub-turn is still running
				// but has made no (recorded) nested tool calls yet — e.g. a
				// background delegate reloaded before its first step landed
				// (symptom: "0 steps working" live, "done 0ms" on reload).
				// Withhold the result frame; the client sees only
				// tool_call_start, i.e. genuinely in progress.
				continue
			}
			if err2 := emitFrame(buildResult(tc, effectiveAgentID, parentForFlat)); err2 != nil {
				return framesEmitted, err2
			}
			if mf, ok := buildMediaFrame(sessionID, tc, mediaStore, seenPaths); ok {
				if err2 := emitFrame(mf); err2 != nil {
					return framesEmitted, err2
				}
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
	if terminalAsk != nil && !terminalAskEmitted {
		if err2 := emitFrame(buildAskUserQuestionFrame(terminalAsk)); err2 != nil {
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
			SessionId: sessionID,
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
		SessionId: sessionID,
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
func buildSpawnIDsWithChildren(entries []session.TranscriptEntry) map[string]bool {
	// Pass 1: collect all spawn/delegate tool call IDs.
	isSpawn := make(map[string]struct{})
	for _, entry := range entries {
		for _, tc := range entry.ToolCalls {
			if (tc.Tool == "spawn" || tc.Tool == "delegate") && tc.ID != "" {
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

type tcAddr struct{ entryIdx, tcIdx int }

// emitNestedToolCalls emits all tool calls across all entries whose
// ParentToolCallID == parentID.  It respects dedup (latestByID), emits
// start+result pairs, and returns the aggregate duration and status.
func emitNestedToolCalls(
	ctx context.Context,
	sessionID string,
	parentID string,
	entries []session.TranscriptEntry,
	latestByID map[string]tcAddr,
	agentID string,
	emitFrame func(any) error,
	toolStore *toolResultStore,
) (totalDurationMS int64, aggregateStatus string, err error) {
	aggregateStatus = "success"

	for ei, entry := range entries {
		for ti, tc := range entry.ToolCalls {
			if string(tc.ParentToolCallID) != parentID {
				continue
			}
			if tc.ID == "" {
				continue
			}
			tcID := string(tc.ID)
			// Dedup.
			if latest := latestByID[tcID]; latest.entryIdx != ei || latest.tcIdx != ti {
				continue
			}

			if ctx.Err() != nil {
				return totalDurationMS, aggregateStatus, ctx.Err()
			}

			// Coerce nil params to empty map (schema: params required, must be object).
			params := tc.Parameters
			if params == nil {
				params = map[string]any{}
			}
			startFrame := generated.ToolCallStartFrame{
				Type:      string(generated.WsFrameTypeToolCallStart),
				SessionId: sessionID,
				CallId:    tcID,
				Tool:      tc.Tool,
				Params:    params,
			}
			effectiveAgentID := entry.AgentID
			if effectiveAgentID == "" {
				effectiveAgentID = agentID
			}
			if effectiveAgentID != "" {
				agentIDCopy := effectiveAgentID
				startFrame.AgentId = &agentIDCopy
			}
			startFrame.ParentCallId = &parentID
			if err2 := emitFrame(startFrame); err2 != nil {
				return totalDurationMS, aggregateStatus, err2
			}

			resultPayload := truncateResult(sessionID, tc, toolStore)
			status := toolCallResultStatus(tc.Status)
			durationMs := int(tc.DurationMS)
			resultFrame := generated.ToolCallResultFrame{
				Type:         string(generated.WsFrameTypeToolCallResult),
				SessionId:    sessionID,
				CallId:       tcID,
				Tool:         tc.Tool,
				Result:       resultPayload,
				Status:       status,
				DurationMs:   &durationMs,
				ParentCallId: &parentID,
			}
			if effectiveAgentID != "" {
				agentIDCopy := effectiveAgentID
				resultFrame.AgentId = &agentIDCopy
			}
			// Same treatment as the top-level builder. This path emits the
			// tool calls a DELEGATED worker made — the exact calls this whole
			// change set is named after — and it previously set no Error at
			// all, neither RC-5c's copy nor W5's parse. A delegated worker's
			// failed write showed its reason live and a bare failure after a
			// reload.
			applyPersistedFailureReason(&resultFrame, tc)
			if err2 := emitFrame(resultFrame); err2 != nil {
				return totalDurationMS, aggregateStatus, err2
			}

			totalDurationMS += tc.DurationMS
			if status == "error" {
				aggregateStatus = "error"
			}
		}
	}
	return totalDurationMS, aggregateStatus, nil
}

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
// JudgeVerdictFrame deliberately carries no session_id (see chat.ts's own
// comment on the live frame) — it is correlated by task_id/plan_id, or by
// the session the judge_verdict transcript entry itself lives in for the
// scope=goal case.
func toJudgeVerdictFrame(v task.JudgeVerdict) generated.JudgeVerdictFrame {
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
	// Fix-wave finding #3: PerCriterion is a required array on the wire
	// (asyncapi_types.gen.go, no `omitempty`) — a nil slice marshals as JSON
	// `null`, which fails the SPA's zod schema for a required array and gets
	// dropped. An empty (zero-criteria) verdict must still round-trip as `[]`,
	// so start from a non-nil, empty slice rather than appending onto a nil
	// one.
	f.PerCriterion = make([]struct {
		CriterionId   string  `json:"criterion_id"`
		EvidenceQuote *string `json:"evidence_quote,omitempty"`
		Met           bool    `json:"met"`
		Reason        string  `json:"reason"`
	}, 0, len(v.PerCriterion))
	for _, c := range v.PerCriterion {
		// ADR-074 D7: optional + empty-safe — an empty quote (fail-closed /
		// pre-D7 verdicts) stays absent from the wire, never "".
		var quote *string
		if c.EvidenceQuote != "" {
			q := c.EvidenceQuote
			quote = &q
		}
		f.PerCriterion = append(f.PerCriterion, struct {
			CriterionId   string  `json:"criterion_id"`
			EvidenceQuote *string `json:"evidence_quote,omitempty"`
			Met           bool    `json:"met"`
			Reason        string  `json:"reason"`
		}{CriterionId: c.CriterionID, EvidenceQuote: quote, Met: c.Met, Reason: c.Reason})
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
			if (tc.Tool == "spawn" || tc.Tool == "delegate") && tc.ID != "" {
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
// and writes it to a wsConn's sendCh, respecting context cancellation.
func wsEmitFunc(ctx context.Context, wc *wsConn) func(any) error {
	return func(f any) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		data, err := json.Marshal(f)
		if err != nil {
			return err
		}
		select {
		case wc.sendCh <- data:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
