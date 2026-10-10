// unified_write.go: Append and rewrite session transcripts and their sidecar files

package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

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
// readMetaLocked). This file's OWN five mutation paths (createSessionLocked,
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
	_, err := us.appendTranscript(sessionID, entry, "append transcript", nil)
	return err
}

// ReadTranscript returns all entries from {session-id}/transcript.jsonl, in
// order across the UTC day partitions. Private members of a line (a paired
// save's trusted source) are not part of TranscriptEntry and never leave the
// store through this reader. This is the single reader every person- and
// model-facing transcript consumer reaches the store through.
func (us *UnifiedStore) ReadTranscript(sessionID string) ([]TranscriptEntry, error) {
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}
	return us.readTranscriptMerged(sessionID)
}

// AddMessage implements SessionStore — appends a simple role/content message to
// the addressed archive (session-core Decision D; the .context backend is gone).
func (us *UnifiedStore) AddMessage(sessionKey, role, content string) {
	h := us.lockSession(owningSessionID(sessionKey))
	defer h.Unlock()
	us.backend.AddMessage(sessionKey, role, content)
}

// AddFullMessage implements SessionStore — appends a complete message to the
// addressed archive.
func (us *UnifiedStore) AddFullMessage(sessionKey string, msg providers.Message) {
	h := us.lockSession(owningSessionID(sessionKey))
	defer h.Unlock()
	us.backend.AddFullMessage(sessionKey, msg)
}

// GetHistory implements SessionStore — returns the live model window.
func (us *UnifiedStore) GetHistory(sessionKey string) []providers.Message {
	return us.backend.GetHistory(sessionKey)
}

// Projection implements SessionStore.
func (us *UnifiedStore) Projection(sessionKey string) memory.ProjectionMeta {
	return us.backend.Projection(sessionKey)
}

// SetProjectionState implements SessionStore.
func (us *UnifiedStore) SetProjectionState(sessionKey string, pk memory.ProjectionKey, state memory.ProjectionState) {
	us.backend.SetProjectionState(sessionKey, pk, state)
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

// deriveCancelTruncation marks the assistant entry a cancel cut short. A
// canceled turn appends one turn_canceled record right after its last assistant
// entry (pkg/agent/cancel.go), so the truncation is read from that record
// instead of being written into the earlier line: the nearest earlier assistant
// entry of the same turn (any turn when the cancel record names none) reads as
// truncated with reason "cancelled". An entry already marked truncated keeps its
// own reason. Nothing on disk is rewritten (FR-006), so the physical order
// assistant-then-turn_canceled is load-bearing.
func deriveCancelTruncation(entries []TranscriptEntry) []TranscriptEntry {
	for i := range entries {
		if entries[i].Type != EntryTypeTurnCancelled {
			continue
		}
		turnID := entries[i].TurnID
		for j := i - 1; j >= 0; j-- {
			e := &entries[j]
			if e.Role != "assistant" || (turnID != "" && e.TurnID != turnID) {
				continue
			}
			if !e.Truncated {
				e.Truncated = true
				e.TruncationReason = "cancelled"
			}
			break
		}
	}
	return entries
}
