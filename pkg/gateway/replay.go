//go:build !cgo

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
	"log/slog"
	"strconv"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
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
) (framesEmitted int, err error) {
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
			Status:     resolveStatus(tc.Status),
			DurationMs: &durationMs,
		}
		if agentID != "" {
			f.AgentId = &agentID
		}
		if parentCallID != "" {
			f.ParentCallId = &parentCallID
		}
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

		// Update the running fallback agent ID.
		if entry.AgentID != "" {
			lastSeenAgentID = entry.AgentID
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

			// For spawn calls that have children: emit subagent_start before
			// the nested frames, then subagent_end after.  We detect this
			// entry as a spawn-parent if its own ID is in spawnIDsWithChildren.
			isSpawnParent := spawnIDsWithChildren[tcID]

			if isSpawnParent {
				// Emit tool_call_start for the spawn call itself FIRST.
				if err2 := emitFrame(buildStart(tc, effectiveAgentID, "")); err2 != nil {
					return framesEmitted, err2
				}

				// Emit subagent_start to bracket nested frames.
				spanID := "span_" + tcID
				taskLabel := resolveTaskLabel(tc)
				subStart := generated.SubagentStartFrame{
					Type:         string(generated.WsFrameTypeSubagentStart),
					SessionId:    sessionID,
					SpanId:       spanID,
					ParentCallId: tcID,
					TaskLabel:    taskLabel,
				}
				if effectiveAgentID != "" {
					agentIDCopy := effectiveAgentID
					subStart.AgentId = &agentIDCopy
				}
				if err2 := emitFrame(subStart); err2 != nil {
					return framesEmitted, err2
				}

				// Emit all nested tool calls (children with ParentToolCallID == tc.ID).
				// The returned aggregate duration/status exist only to drive
				// emitNestedToolCalls' own internal bookkeeping historically — they
				// are deliberately NOT used for the outer span's own Status/
				// DurationMs below (Wave 3 fix 5b, see comment there). This call is
				// still required: it emits the individual nested child
				// tool_call_start/tool_call_result frames.
				_, _, nestedErr := emitNestedToolCalls(
					ctx, sessionID, tcID, entries, latestByID, effectiveAgentID, emitFrame, toolStore,
				)
				if nestedErr != nil {
					return framesEmitted, nestedErr
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
				if effectiveAgentID != "" {
					agentIDCopy := effectiveAgentID
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
			if path, err := mediaStore.Resolve(refStr); err == nil && path != "" {
				if _, dup := seenPaths[path]; dup {
					continue
				}
				seenPaths[path] = struct{}{}
			}
		}
		parts = append(parts, generated.MediaPart{
			Type:        mediaType,
			Url:         "/api/v1/media/" + refStr[len(refPrefix):],
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
			status := resolveStatus(tc.Status)
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
	// Structured payload: for the rate-limit path, decode the trailing
	// "(retry after Ns)" parenthetical into a typed retry_after_seconds so
	// the SPA can render the countdown without re-parsing the message text.
	// Anything beyond that scope is left to the Message field — the typed
	// payload is intentionally minimal until W2-15 lands a typed Status enum.
	if frame.Kind == "rate_limit" {
		if retry := parseRetryAfterSeconds(entry.Content); retry != nil {
			frame.Payload.RetryAfterSeconds = retry
		}
	}
	return frame
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
	rs.spanCount = len(spawnIDsWithChildren)

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
