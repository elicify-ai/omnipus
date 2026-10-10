// unified_write.go: Append and rewrite session transcripts and their sidecar files

package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// SetMeta applies a partial update to a session's meta.json.
//
// ADR-057 W23/FR-084: unlike the pre-split version, this no longer funnels
// every patch through one whole-document rewrite. Each non-nil patch field
// is routed to its OWN field group (identity/stats/goal/loop, FR-053), and
// ONLY the group(s) actually touched by this call are written — a `/goal`
// or `/loop` patch no longer rewrites meta.json's UpdatedAt or touches
// stats.json at all (BDD-59: a goal round leaves loop.json AND meta.json
// byte-identical). meta.json's own UpdatedAt is bumped only when an
// identity-group field (including the new ParentSessionID) is touched;
// Goal*/Loop* groups carry no UpdatedAt of their own (only stats.json does,
// per FR-053/FR-066) so a goal/loop-only patch does not change the
// session's composed recency.
func (us *UnifiedStore) SetMeta(sessionID string, patch MetaPatch) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()

	meta, err := us.readMetaLocked(sessionID)
	if err != nil {
		return err
	}

	var identityTouched, loopTouched, pendingAskTouched bool

	if patch.Title != nil {
		meta.Title = *patch.Title
		identityTouched = true
	}
	if patch.Status != nil {
		meta.Status = *patch.Status
		identityTouched = true
	}
	if patch.TaskID != nil {
		meta.TaskID = *patch.TaskID
		identityTouched = true
	}
	if patch.InstanceID != nil {
		meta.InstanceID = *patch.InstanceID
	}
	if patch.Owner != nil {
		meta.Owner = *patch.Owner
		identityTouched = true
	}
	if patch.WorkspaceID != nil {
		// session-core U1 / C-MAIN: a main's identity is immutable — its
		// computed id IS the (workspace, agent) pair, so re-pointing the
		// workspace tag would leave the id naming a pair the record no longer
		// matches. Refuse rather than write a self-contradicting main.
		if meta.Type == SessionTypeMain && *patch.WorkspaceID != meta.WorkspaceID {
			return fmt.Errorf("unified_store: session %q is a main; workspace_id is immutable", sessionID)
		}
		meta.WorkspaceID = *patch.WorkspaceID
		identityTouched = true
	}
	if patch.ParentSessionID != nil {
		meta.ParentSessionID = *patch.ParentSessionID
		identityTouched = true
	}
	if patch.PendingAskJSON != nil {
		// ADR-086 GOAL-FR-005 (wave S2): PendingAskJSON persists to its own
		// pending_ask.json group (u5WritePendingAskLocked, pending_ask.go).
		// It is session-scoped interaction state, never goal state. Pass an
		// empty string to CLEAR it.
		meta.PendingAskJSON = *patch.PendingAskJSON
		pendingAskTouched = true
	}
	if patch.LoopMode != nil {
		meta.LoopMode = *patch.LoopMode
		loopTouched = true
	}
	if patch.LoopPrompt != nil {
		meta.LoopPrompt = *patch.LoopPrompt
		loopTouched = true
	}
	if patch.LoopRunCount != nil {
		meta.LoopRunCount = *patch.LoopRunCount
		loopTouched = true
	}
	if patch.LoopMaxRuns != nil {
		meta.LoopMaxRuns = *patch.LoopMaxRuns
		loopTouched = true
	}
	if patch.LoopIntervalMS != nil {
		meta.LoopIntervalMS = *patch.LoopIntervalMS
		loopTouched = true
	}
	if patch.LoopNextDelayMS != nil {
		meta.LoopNextDelayMS = *patch.LoopNextDelayMS
		loopTouched = true
	}
	if patch.LoopJobID != nil {
		meta.LoopJobID = *patch.LoopJobID
		loopTouched = true
	}
	if patch.LoopStartedAt != nil {
		meta.LoopStartedAt = *patch.LoopStartedAt
		loopTouched = true
	}
	if patch.LoopLastActivityAt != nil {
		meta.LoopLastActivityAt = *patch.LoopLastActivityAt
		loopTouched = true
	}

	if identityTouched {
		meta.UpdatedAt = time.Now().UTC()
		if err := us.u5WriteIdentityLocked(sessionID, meta); err != nil {
			return err
		}
	}
	if patch.Status != nil {
		// ADR-057 U6 W24 (FR-064): a Status transition is one of the four
		// forced-flush points — a session about to pause/complete/error
		// must not leave pending counter deltas stranded in the cache until
		// the next periodic tick. This call already holds sessionID's shard
		// (h, acquired above), so u6FlushDirtySessionLocked is safe to call
		// directly. A flush failure here is logged, not returned: the
		// Status write itself already succeeded durably above, and failing
		// the whole SetMeta call would make a successfully-persisted status
		// change look like a lost one (same rationale AppendTranscript uses
		// for its own post-write stats failure).
		if err := us.u6FlushDirtySessionLocked(sessionID); err != nil {
			slog.Warn("unified_store: forced stats flush on status change failed",
				"session_id", sessionID, "error", err)
		}
	}
	if pendingAskTouched {
		if err := us.u5WritePendingAskLocked(sessionID, meta); err != nil {
			return err
		}
	}
	if loopTouched {
		if err := us.u5WriteLoopLocked(sessionID, meta); err != nil {
			return err
		}
	}
	return nil
}

// writeMetaLocked is RETAINED, post-W23, as a backward-compatible DISPATCHER
// over the FR-054/GOAL-FR-005 targeted field-group writers
// (u5WriteIdentityLocked/u5WriteStatsLocked/u5WriteLoopLocked,
// unified_meta_files.go; u5WritePendingAskLocked,
// pending_ask.go) — it is no longer "the single invalidation/update point
// for every mutation path" (that whole-document funnel is exactly what
// FR-084/Alternative-F forbids; see the doc comments above metaCache and
// readMetaLocked). This file's OWN four mutation paths (createSessionLocked,
// SetMeta, AppendTranscript, NewChannelSession) call the
// targeted writers DIRECTLY and never reach this function.
//
// It survives only for pkg/session/unified_api.go's two call sites
// (AppendTranscriptStrict's stats-only mutation, CreateSessionWithID's
// identity-only Owner stamp) — U2's file, landed commit acfd0e5a in this
// same wave BEFORE this rewrite reached unified.go, both passing one fully-
// mutated *UnifiedMeta rather than a field-group-scoped patch. Ownership
// Rule 1/2 forbids this unit from editing unified_api.go to convert those
// two call sites itself, so this function closes the gap from this side of
// the boundary: it diffs the supplied meta against the meta CURRENTLY
// cached/persisted for sessionID to determine exactly which of the field
// groups actually changed (identity's own UpdatedAt is excluded from that
// comparison — a Stats-only touch, e.g. AppendTranscriptStrict, always
// changes the single in-memory UpdatedAt field, and that must not be
// misread as an identity change), and calls ONLY the targeted writer(s)
// for the group(s) that differ. This keeps FR-084's "update only its own
// field group, never a wholesale cache replace" true for these two call
// sites as well, and preserves W23's lazy-file-creation property (a
// delegated child that only ever calls AppendTranscriptStrict never gains
// an empty loop.json/pending_ask.json it never touched).
//
// Caller must hold sessionID's shard (see lockSession) — was: caller must
// hold us.mu. (The DEL-09 generic helper writeUnifiedMetaDirect is deleted;
// CONV publishes the current-format identity group through its own
// convWriteIdentityFile, and no reader exists for the pre-split fused shape
// it once wrote. Nothing in this dispatcher touches either.)
func (us *UnifiedStore) writeMetaLocked(sessionID string, meta *UnifiedMeta) error {
	prev, prevErr := us.readMetaLocked(sessionID)

	writeIdentity, writeStats, writeLoop, writePendingAsk := true, true, true, true
	if prevErr == nil {
		prevIdentity, curIdentity := u5IdentityFromMeta(prev), u5IdentityFromMeta(meta)
		// UpdatedAt is the single in-memory field shared by BOTH the identity
		// and stats groups (FR-066 composes them on read) — excluded here so
		// a Stats-only caller (AppendTranscriptStrict) that bumps it does not
		// register as an identity change too.
		prevIdentity.UpdatedAt, curIdentity.UpdatedAt = time.Time{}, time.Time{}
		writeIdentity = !u5SameJSON(prevIdentity, curIdentity)
		writeStats = !u5SameJSON(u5StatsFromMeta(prev), u5StatsFromMeta(meta))
		writeLoop = !u5SameJSON(u5LoopFromMeta(prev), u5LoopFromMeta(meta))
		writePendingAsk = !u5SameJSON(u5PendingAskFromMeta(prev), u5PendingAskFromMeta(meta))
	}

	if writeIdentity {
		if err := us.u5WriteIdentityLocked(sessionID, meta); err != nil {
			return err
		}
	}
	if writeStats {
		if err := us.u5WriteStatsLocked(sessionID, meta); err != nil {
			return err
		}
	}
	if writePendingAsk {
		if err := us.u5WritePendingAskLocked(sessionID, meta); err != nil {
			return err
		}
	}
	if writeLoop {
		if err := us.u5WriteLoopLocked(sessionID, meta); err != nil {
			return err
		}
	}
	return nil
}

// AppendTranscript appends an entry to {session-id}/transcript.jsonl.
//
// R-8 fix (ADR-057 FR-048): this used to take the store-global us.mu on
// EVERY streamed line, held across the append AND a full meta rewrite — a
// 24-way delegation fan-out serialised 24 fsync-bound creates/appends and
// stalled token streaming in every OTHER session in the store. Now it takes
// only sessionID's own shard, so a concurrent append/create against a
// DIFFERENT session never waits on this one.
//
// STRICT (ADR-057 FR-002/W3a, AC-1). Before this change, the existence
// check ran AFTER the transcript write: fileutil.AppendJSONL begins with
// os.MkdirAll, so an append against a session id with no meta.json silently
// minted an orphan directory, wrote the line into it, then failed the
// follow-up meta-stats read, logged a WARN, and returned nil — "success"
// for a write that both landed somewhere nobody asked for AND told its
// caller nothing went wrong. That lenient branch is DELETED, not converted
// into a strict SIBLING: AC-1's frozen text is a property of
// AppendTranscript itself, so a sibling (AppendTranscriptStrict, U2's
// unified_api.go) would only satisfy it for callers that switched to the
// sibling — every one of the 22 tree-wide AppendTranscript( matches would
// still be able to silently create (`[grill2 C2-3]`). The existence check
// now runs FIRST, under sessionID's own shard, before ANY filesystem write:
// an unknown session id returns a non-nil error and creates ZERO
// directories (SC-001), matching AppendTranscriptStrict's contract exactly
// — "a name, not a second behavior" per FR-002's own text.
func (us *UnifiedStore) AppendTranscript(sessionID string, entry TranscriptEntry) error {
	_, err := us.appendTranscript(sessionID, entry, false, "append transcript", nil)
	return err
}

// validTruncationReasons enumerates the only accepted values for the reason
// parameter of MarkLastEntryTruncated (ADR-087 D2). Absent on a persisted
// entry means "cancelled" (legacy) — but a caller of this function must
// always name one explicitly; there is no default at the write path.
var validTruncationReasons = map[string]bool{
	"cancelled":         true,
	"max_output_tokens": true,
}

// MarkLastEntryTruncated finds the last assistant transcript entry for the
// given session in transcript.jsonl that belongs to turnID and rewrites it
// with truncated=true and truncation_reason=reason.
//
// reason MUST be one of "cancelled" or "max_output_tokens" (ADR-087 D2); any
// other value is rejected with an error and the entry is left untouched —
// this function never writes an unrecognized reason to disk.
//
// H2: The turnID parameter scopes the backward-walk to entries whose
// turn_id matches. This prevents a cancel on turn T2 from mutating the
// clean final assistant entry of a previously-completed turn T1 when both
// share the same sessionID.
//
// If turnID is empty, the function falls back to the pre-H2 behavior (match
// the last assistant entry regardless of turn_id) and logs a warning. This
// preserves backward compatibility with any call sites that cannot supply
// a turn ID.
//
// Acquires the same per-session shard as AppendTranscript (see lockSession),
// so it serializes with any concurrent operation against THIS session
// without contending with any other session's shard. Does NOT touch
// context.jsonl (LLM history) — per FR-14a, the partial content there remains
// untouched so the next turn's LLM context sees natural truncation.
//
// Returns nil if no matching assistant entry is found (e.g., cancel arrived
// before any assistant content was written). Returns an error only on I/O
// failure or an unrecognized reason.
func (us *UnifiedStore) MarkLastEntryTruncated(sessionID, turnID, reason string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	if !validTruncationReasons[reason] {
		return fmt.Errorf("unified_store: mark truncated: invalid truncation reason %q (must be \"cancelled\" or \"max_output_tokens\")", reason)
	}
	if turnID == "" {
		slog.Warn(
			"unified_store: MarkLastEntryTruncated called with empty turnID — falling back to last-assistant-entry behavior",
			"session_id",
			sessionID,
		)
	}

	h := us.lockSession(sessionID)
	defer h.Unlock()

	transcriptPath := filepath.Join(us.baseDir, sessionID, "transcript.jsonl")
	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// No transcript at all — nothing to mark; treat as no-op.
			return nil
		}
		return fmt.Errorf("unified_store: mark truncated: read transcript: %w", err)
	}

	// Split into non-empty lines and parse.
	rawLines := bytes.Split(data, []byte{'\n'})
	entries := make([]json.RawMessage, 0, len(rawLines))
	for _, line := range rawLines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		entries = append(entries, json.RawMessage(line))
	}

	if len(entries) == 0 {
		return nil
	}

	// Walk backward to find the last assistant entry matching turnID.
	// When turnID is empty (backward-compat path) any assistant entry matches.
	targetIdx := -1
	for i := len(entries) - 1; i >= 0; i-- {
		var e TranscriptEntry
		if jsonErr := json.Unmarshal(entries[i], &e); jsonErr != nil {
			// Skip malformed lines.
			slog.Warn(
				"unified_store: mark truncated: skipping malformed line",
				"session_id",
				sessionID,
				"index",
				i,
				"error",
				jsonErr,
			)
			continue
		}
		if e.Role != "assistant" {
			continue
		}
		if turnID != "" && e.TurnID != turnID {
			continue
		}
		targetIdx = i
		break
	}

	if targetIdx == -1 {
		// No matching assistant entry found — no-op, not an error.
		return nil
	}

	// Unmarshal the target entry, set Truncated, re-marshal into the slot.
	var target TranscriptEntry
	if jsonErr := json.Unmarshal(entries[targetIdx], &target); jsonErr != nil {
		return fmt.Errorf("unified_store: mark truncated: unmarshal target entry: %w", jsonErr)
	}
	target.Truncated = true
	target.TruncationReason = reason
	rewritten, jsonErr := json.Marshal(target)
	if jsonErr != nil {
		return fmt.Errorf("unified_store: mark truncated: marshal updated entry: %w", jsonErr)
	}
	entries[targetIdx] = json.RawMessage(rewritten)

	// Rebuild the file contents: one JSON object per line, WITH a trailing
	// newline after the LAST line too. This is load-bearing, not cosmetic:
	// a rewrite that omits the final newline would leave the next
	// AppendTranscript call's record concatenated directly onto this
	// rewrite's last line — e.g. "{lastEntry}{newRecord}\n" — which
	// ReadTranscript cannot parse as JSON and silently drops via its
	// "skipping malformed transcript line" continue, losing BOTH entries.
	// Confirmed via a byte-level repro (rewrite → append → inspect raw
	// bytes → ReadTranscript entry count) before this fix; see
	// TestMarkLastEntryTruncated_TrailingNewlineSurvivesSubsequentAppend
	// and TestUpdateToolCallStatus_TrailingNewlineSurvivesSubsequentAppend.
	// This is the PRIMARY fix; AppendJSONL (pkg/fileutil/file.go) now also
	// carries a SECOND, independent defensive layer — it detects a missing
	// trailing newline on the existing file and prepends one before its own
	// record — so even a future rewrite site that forgets this discipline
	// degrades to a defensively-recovered file, not silent data loss.
	var buf bytes.Buffer
	for _, line := range entries {
		buf.Write(line)
		buf.WriteByte('\n')
	}

	if writeErr := fileutil.WriteFileAtomic(transcriptPath, buf.Bytes(), 0o600); writeErr != nil {
		return fmt.Errorf("unified_store: mark truncated: write transcript: %w", writeErr)
	}
	return nil
}

// UpdateToolCallStatus finds the transcript entry carrying a ToolCall with the
// given ID and rewrites that ToolCall's Status and DurationMS fields in place.
//
// This exists for the ASYNC delegation path (DelegateTool.executeAsync,
// pkg/tools/delegate.go): the spawning "delegate" tool call itself completes
// — and its own ToolCall record is appended via appendToolCallTranscript —
// almost instantly with a placeholder ack (Status="success", DurationMS≈0,
// from tools.AsyncResult), well BEFORE the actual sub-turn goroutine finishes
// running. The sub-turn's real terminal status/wall-clock duration is only
// known later, at EventKindSubTurnEnd (spawnSubTurn's cleanup defer,
// pkg/agent/subturn.go) — this method lets that defer go back and correct the
// already-persisted placeholder record so a session reload replays the same
// status/duration the live WS stream showed (Wave 3 fix 5b).
//
// Mirrors MarkLastEntryTruncated's read-mutate-rewrite-one-line pattern and
// shares its mutex. Walks backward so a duplicate ID (should not normally
// occur — appendToolCallTranscript writes one entry per completed tool call)
// updates the LATEST occurrence, matching the "last occurrence wins" semantics
// replay.go already applies when reading (buildSpawnIDsWithChildren /
// latestByID in pkg/gateway/replay.go).
//
// Returns found=false (with a nil error) when no entry with a matching
// ToolCall.ID is found. That is not necessarily an error: a child can finish
// before the parent has appended the delegate tool call's placeholder. The
// caller can distinguish this ordering window from a real update failure.
//
// found=false can ALSO legitimately occur for ASYNC delegation due to a real
// race: DelegateTool.executeAsync launches the child sub-turn in a goroutine
// and returns immediately, while the PARENT (the turn that called the
// "delegate" tool) writes this tool call's OWN placeholder ack record only
// after further processing (hooks, media, events) in its own call stack. If
// the child's spawnSubTurn dispatch fails fast (e.g. a depth-limit or
// target-resolution rejection), its cleanup defer can call
// UpdateToolCallStatus BEFORE the parent's placeholder record exists yet.
// Callers in that race window (currently only spawnSubTurn's cleanup defer,
// pkg/agent/subturn.go) MUST retry briefly rather than treat found=false as
// terminal — see updateToolCallStatusWithRetry.
//
// Returns a non-nil error only on I/O failure.
func (us *UnifiedStore) UpdateToolCallStatus(
	sessionID string,
	toolCallID ToolCallID,
	status string,
	durationMS int64,
) (found bool, err error) {
	return us.UpdateToolCallStatusAndResult(sessionID, toolCallID, status, durationMS, nil)
}

// UpdateToolCallStatusAndResult is UpdateToolCallStatus's result-bearing
// sibling (W4): it performs the exact same in-place Status/DurationMS
// correction, and additionally rewrites the matching ToolCall's Result field
// when result is non-nil. Passing a nil result leaves the ToolCall's existing
// Result untouched (UpdateToolCallStatus delegates here with result=nil,
// preserving its original status-only behavior exactly).
//
// This exists for spawnSubTurn's completion path (pkg/agent/subturn.go): the
// persisted "delegate" tool_call previously never received the sub-turn's own
// output — UpdateToolCallStatus corrected Status/DurationMS but had no way to
// carry the result text, so a session reload showed a delegate tool_call with
// a terminal status but an empty `result`, even though the live WS stream
// carried the sub-turn's actual text via SubTurnEndPayload. Mirrors
// recordExternalToolResultUpdateInPlace's (pkg/agent/external_dispatch.go)
// read-modify-rewrite-one-line approach for the equivalent external-cli tool
// call case.
//
// See UpdateToolCallStatus's doc comment above for the found=false semantics
// (same race window, same retry-via-updateToolCallStatusWithRetry contract).
// Returns a non-nil error only on I/O failure.
func (us *UnifiedStore) UpdateToolCallStatusAndResult(
	sessionID string,
	toolCallID ToolCallID,
	status string,
	durationMS int64,
	result map[string]any,
) (found bool, err error) {
	if toolCallID == "" {
		return false, nil
	}
	n, err := us.rewriteTranscriptToolCalls(sessionID, map[ToolCallID]func(*ToolCall){
		toolCallID: func(tc *ToolCall) {
			tc.Status = status
			tc.DurationMS = durationMS
			if result != nil {
				tc.Result = result
			}
		},
	}, "update tool call status")
	return n > 0, err
}

// ReadTranscript returns all entries from {session-id}/transcript.jsonl.
// A transcript line that still carries the provenance write-phase marker
// (TranscriptEntry.ProvenancePending) AND has no MessageProvenance record for
// its ID is crash residue from an incomplete paired save (ADR-20261004
// Correction C4): it is not shown and not admitted. This is the single
// filter every person- and model-facing reader reaches the store through —
// transcript render, replay and model-context assembly all read via this
// method — while lines without the marker (all agent and tool lines, and
// every historical user line that was never part of a paired save) always
// pass. See filterUnpairedProvenanceLines.
func (us *UnifiedStore) ReadTranscript(sessionID string) ([]TranscriptEntry, error) {
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}
	// FR-005: the archive is UTC day-partitioned. Read every partition in
	// chronological order (rolled day files, then the current transcript.jsonl)
	// so a multi-day session returns its full ordered history in one call.
	paths, err := us.transcriptPartitionPaths(sessionID)
	if err != nil {
		// Never silently drop retained history: fall back to the current file
		// and make the loss visible.
		warnOnStrayDayPartition(sessionID, err)
		paths = []string{filepath.Join(us.baseDir, sessionID, transcriptFileName)}
	}
	entries := []TranscriptEntry{}
	for _, transcriptPath := range paths {
		data, err := os.ReadFile(transcriptPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("unified_store: read transcript: %w", err)
		}
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var entry TranscriptEntry
			if err := json.Unmarshal(line, &entry); err != nil {
				if isContextWindowNoticeLine(line) {
					return nil, fmt.Errorf("unified_store: read transcript: invalid context_window_notice: %w", err)
				}
				slog.Warn("unified_store: skipping malformed transcript line", "session_id", sessionID, "error", err)
				continue
			}
			if err := validateContextWindowNotice(sessionID, entry); err != nil {
				return nil, fmt.Errorf("unified_store: read transcript: %w", err)
			}
			entries = append(entries, entry)
		}
	}
	return us.filterUnpairedProvenanceLines(sessionID, entries), nil
}

// AddMessage implements SessionStore — appends a simple role/content message to
// the addressed archive (session-core Decision D; the .context backend is gone).
func (us *UnifiedStore) AddMessage(sessionKey, role, content string) {
	us.backend.AddMessage(sessionKey, role, content)
}

// AddFullMessage implements SessionStore — appends a complete message to the
// addressed archive.
func (us *UnifiedStore) AddFullMessage(sessionKey string, msg providers.Message) {
	us.backend.AddFullMessage(sessionKey, msg)
}

// GetHistory implements SessionStore — returns the live model window.
func (us *UnifiedStore) GetHistory(sessionKey string) []providers.Message {
	return us.backend.GetHistory(sessionKey)
}

// SetHistory implements SessionStore (first-fill only, FR-047).
func (us *UnifiedStore) SetHistory(sessionKey string, history []providers.Message) {
	us.backend.SetHistory(sessionKey, history)
}

// TruncateHistory implements SessionStore.
func (us *UnifiedStore) TruncateHistory(sessionKey string, keepLast int) {
	us.backend.TruncateHistory(sessionKey, keepLast)
}

// Projection implements SessionStore.
func (us *UnifiedStore) Projection(sessionKey string) memory.ProjectionMeta {
	return us.backend.Projection(sessionKey)
}

// SetProjectionState implements SessionStore.
func (us *UnifiedStore) SetProjectionState(sessionKey string, pk memory.ProjectionKey, state memory.ProjectionState) {
	us.backend.SetProjectionState(sessionKey, pk, state)
}

// MarkHydrated implements SessionStore.
func (us *UnifiedStore) MarkHydrated(sessionKey string) {
	us.backend.MarkHydrated(sessionKey)
}

// ReadArchive implements SessionStore — returns the full archived log for
// sessionKey from line 0, ignoring meta.Skip. Evicted (skipped) turns are
// included. Each ArchivedMessage carries the per-line TS written by addMsg
// (FR-016/FR-017). Legacy lines pre-dating the TS stamp unmarshal with TS==0.
func (us *UnifiedStore) ReadArchive(ctx context.Context, sessionKey string) ([]memory.ArchivedMessage, error) {
	msgs, err := us.backend.ReadArchive(ctx, sessionKey)
	if err != nil {
		slog.Error("unified_store: read archive", "key", sessionKey, "error", err)
		return nil, err
	}
	return msgs, nil
}

// ScanArchive streams the archive for sessionKey line by line, stopping when
// fn returns false — the ADR-066 FR-024 / B-31b path recall by tool_call_id
// uses so one addressed line never costs a whole-archive load. Indexing is
// identical to ReadArchive's slice positions (see memory.JSONLStore.ScanArchive).
func (us *UnifiedStore) ScanArchive(
	ctx context.Context, sessionKey string, fn func(idx int, msg memory.ArchivedMessage) bool,
) error {
	return us.backend.ScanArchive(ctx, sessionKey, fn)
}

// Save implements SessionStore — ensures all writes are durable.
// Since the JSONL backend fsyncs every write immediately, the data is
// already durable at this point.
//
// context-paging (FR-005): Save does NOT compact the JSONL file. Evicted
// (skipped) lines must remain on disk so recall_conversation can reach them.
// The retention sweep is the sole legitimate deleter of context.jsonl content.
func (us *UnifiedStore) Save(sessionKey string) error {
	return nil
}
