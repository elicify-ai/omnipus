// transcript_effects.go: the append-only transcript — addressed appends,
// tool-call effects and the merge-on-read that presents them as one
// []TranscriptEntry (session-core U2 / FR-006, effects design D5).
//
// A chat record is never rewritten. A correction to an earlier tool_call record
// is a NEW record of type tool_call_effect that names the target by the
// ArchiveAddress its append returned:
//
//   - Settle replaces one tool call wholesale (a pending placeholder settled to
//     its outcome), guarded by an optional IfStatus so a double settle is a
//     no-op evaluated on read;
//   - Projections change the projected content state and text of tool calls
//     (a capped or emptied result), as one atomic batch;
//   - Retracts undo earlier Projections effects (an aborted turn's) by naming
//     them, so the pre-effect value reappears exactly.
//
// Effects are applied last-writer-wins per (target address, tool_call_id) in
// physical order, which is acceptance order under the session shard. Because no
// byte is ever rewritten, every address ever issued stays valid for the
// partition's lifetime.
package session

import (
	"bytes"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// appendArchiveRecordLocked appends one record to the session's archive through
// the store every writer of the session directory shares, and returns its exact
// address. The caller holds the session shard (lock order: shard, then the
// store's own lock).
func (us *UnifiedStore) appendArchiveRecordLocked(sessionID string, rec ArchiveRecord) (ArchiveAddress, error) {
	store, err := us.archiveStore(sessionID)
	if err != nil {
		return ArchiveAddress{}, err
	}
	return store.Append(rec)
}

// truncateBack removes bytes a failed append left after pre. They were never
// published (no address was returned), so this is not a rewrite of retained
// bytes (FR-006).
func truncateBack(f *os.File, pre int64) error {
	if err := f.Truncate(pre); err != nil {
		return fmt.Errorf("truncate unpublished bytes: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync truncated transcript: %w", err)
	}
	return nil
}

// readRecordAtLocked reads the one record at addr (one bounded read) and checks
// its id, so a stale or corrupt address is an error, never a wrong record.
func (us *UnifiedStore) readRecordAtLocked(sessionID string, addr ArchiveAddress) (ArchiveRecord, error) {
	store, err := us.archiveStore(sessionID)
	if err != nil {
		return ArchiveRecord{}, err
	}
	return store.ReadAt(addr)
}

// checkToolCallTarget verifies that addr names a chat record carrying tool call
// id: the bounded write-side check every effect runs.
func (us *UnifiedStore) checkToolCallTarget(sessionID string, addr ArchiveAddress, id ToolCallID) error {
	rec, err := us.readRecordAtLocked(sessionID, addr)
	if err != nil {
		return err
	}
	if rec.Type == EntryTypeToolCallEffect || rec.Type == EntryTypeModelRef {
		return fmt.Errorf("record %s is a %q record, not a chat tool_call record", addr.EntryID, rec.Type)
	}
	for _, tc := range rec.ToolCalls {
		if tc.ID == id {
			return nil
		}
	}
	return fmt.Errorf("record %s carries no tool call %q", addr.EntryID, id)
}

func newEffectRecord(effect ToolCallEffect) (ArchiveRecord, error) {
	id, err := ulid.New(ulid.Timestamp(time.Now()), crand.Reader)
	if err != nil {
		return ArchiveRecord{}, fmt.Errorf("generate effect id: %w", err)
	}
	return ArchiveRecord{
		TranscriptEntry: TranscriptEntry{
			ID: "effect_" + id.String(), Type: EntryTypeToolCallEffect,
			ViewMembership: ViewMembershipChat, Timestamp: time.Now().UTC(),
		},
		ToolCallEffect: &effect,
	}, nil
}

// appendEffectLocked validates and appends one effect record.
func (us *UnifiedStore) appendEffectLocked(sessionID string, effect ToolCallEffect) (ArchiveAddress, error) {
	rec, err := newEffectRecord(effect)
	if err != nil {
		return ArchiveAddress{}, err
	}
	if err := rec.Validate(); err != nil {
		return ArchiveAddress{}, err
	}
	return us.appendArchiveRecordLocked(sessionID, rec)
}

// SettleToolCall settles one tool call of the chat record at target to tc (a
// wholesale post-image; tc.ID names the call). When ifStatus is non-empty the
// settle applies only if the call's merged status equals it, which makes a
// double settle a no-op evaluated on read. It returns the effect's own address.
func (us *UnifiedStore) SettleToolCall(sessionID string, target ArchiveAddress, ifStatus string, tc ToolCall) (ArchiveAddress, error) {
	if err := validateSessionID(sessionID); err != nil {
		return ArchiveAddress{}, err
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	if err := us.checkToolCallTarget(sessionID, target, tc.ID); err != nil {
		return ArchiveAddress{}, fmt.Errorf("unified_store: settle tool call: %w", err)
	}
	addr, err := us.appendEffectLocked(sessionID, ToolCallEffect{Settle: &ToolCallSettle{Target: target, IfStatus: ifStatus, ToolCall: tc}})
	if err != nil {
		return ArchiveAddress{}, fmt.Errorf("unified_store: settle tool call: %w", err)
	}
	return addr, nil
}

// ProjectToolCalls records a batch of projection edits as ONE effect, so the
// batch is atomic. Every edit's target is verified first.
func (us *UnifiedStore) ProjectToolCalls(sessionID string, edits []ToolCallProjectionEdit) (ArchiveAddress, error) {
	if err := validateSessionID(sessionID); err != nil {
		return ArchiveAddress{}, err
	}
	if len(edits) == 0 {
		return ArchiveAddress{}, errors.New("unified_store: project tool calls: no edits")
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	for _, e := range edits {
		if err := us.checkToolCallTarget(sessionID, e.Target, e.ToolCallID); err != nil {
			return ArchiveAddress{}, fmt.Errorf("unified_store: project tool calls: %w", err)
		}
	}
	addr, err := us.appendEffectLocked(sessionID, ToolCallEffect{Projections: append([]ToolCallProjectionEdit(nil), edits...)})
	if err != nil {
		return ArchiveAddress{}, fmt.Errorf("unified_store: project tool calls: %w", err)
	}
	return addr, nil
}

// RetractToolCallEffects retracts earlier Projections effects so the values they
// changed read as before them. Every address must be a Projections effect.
func (us *UnifiedStore) RetractToolCallEffects(sessionID string, effects []ArchiveAddress) (ArchiveAddress, error) {
	if err := validateSessionID(sessionID); err != nil {
		return ArchiveAddress{}, err
	}
	if len(effects) == 0 {
		return ArchiveAddress{}, errors.New("unified_store: retract tool call effects: nothing to retract")
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	for _, a := range effects {
		rec, err := us.readRecordAtLocked(sessionID, a)
		if err != nil {
			return ArchiveAddress{}, fmt.Errorf("unified_store: retract tool call effects: %w", err)
		}
		if rec.ToolCallEffect == nil || len(rec.ToolCallEffect.Projections) == 0 {
			return ArchiveAddress{}, fmt.Errorf("unified_store: retract tool call effects: record %s is not a projection effect", a.EntryID)
		}
	}
	addr, err := us.appendEffectLocked(sessionID, ToolCallEffect{Retracts: append([]ArchiveAddress(nil), effects...)})
	if err != nil {
		return ArchiveAddress{}, fmt.Errorf("unified_store: retract tool call effects: %w", err)
	}
	return addr, nil
}

// applyProjectionText sets a tool call's projected text: for an error-bearing
// record with no result the text replaces the error; otherwise it replaces
// Result["text"] (and the error too when one is recorded).
func applyProjectionText(tc *ToolCall, text string) {
	if tc.Result == nil && (tc.Error != "" || tc.Status == "error") {
		tc.Error = text
		return
	}
	result := make(map[string]any, len(tc.Result)+1)
	for k, v := range tc.Result {
		result[k] = v
	}
	result["text"] = text
	tc.Result = result
	if tc.Error != "" {
		tc.Error = text
	}
}

type partitionRef struct{ key, path string }

// transcriptPartitions lists the session's partitions in chronological order
// with their addressing keys.
func (us *UnifiedStore) transcriptPartitions(sessionID string) ([]partitionRef, error) {
	paths, err := us.transcriptPartitionPaths(sessionID)
	if err != nil {
		return nil, err
	}
	mark, err := us.readTranscriptDayMark(sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]partitionRef, 0, len(paths))
	for _, p := range paths {
		base := filepath.Base(p)
		if base == transcriptFileName {
			key := mark
			if key == "" {
				key = "unmarked"
			}
			out = append(out, partitionRef{key: key, path: p})
			continue
		}
		out = append(out, partitionRef{key: strings.TrimSuffix(base, ".jsonl"), path: p})
	}
	return out, nil
}

type pendingEffect struct {
	addr   ArchiveAddress
	effect *ToolCallEffect
}

// readTranscriptMerged reads every partition in order and returns the chat
// view: chat records with the tool-call effects applied and the cancel
// truncation derived. It is the single reader behind ReadTranscript.
func (us *UnifiedStore) readTranscriptMerged(sessionID string) ([]TranscriptEntry, error) {
	parts, err := us.transcriptPartitions(sessionID)
	if err != nil {
		// Never silently drop retained history: fall back to the current file
		// and make the loss visible.
		warnOnStrayDayPartition(sessionID, err)
		parts = []partitionRef{{key: "unmarked", path: filepath.Join(us.baseDir, sessionID, transcriptFileName)}}
	}
	entries := []TranscriptEntry{}
	index := map[ArchiveAddress]int{}
	var effects []pendingEffect
	for _, part := range parts {
		data, err := os.ReadFile(part.path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("unified_store: read transcript: %w", err)
		}
		for pos := 0; pos < len(data); {
			end := bytes.IndexByte(data[pos:], '\n')
			var raw []byte
			start := pos
			if end < 0 {
				raw, pos = data[pos:], len(data)
			} else {
				raw, pos = data[pos:pos+end], pos+end+1
			}
			line := bytes.TrimSpace(raw)
			if len(line) == 0 {
				continue
			}
			var rec ArchiveRecord
			if err := json.Unmarshal(line, &rec); err != nil {
				if isContextWindowNoticeLine(line) {
					return nil, fmt.Errorf("unified_store: read transcript: invalid context_window_notice: %w", err)
				}
				slog.Warn("unified_store: skipping malformed transcript line", "session_id", sessionID, "error", err)
				continue
			}
			if err := validateContextWindowNotice(sessionID, rec.TranscriptEntry); err != nil {
				return nil, fmt.Errorf("unified_store: read transcript: %w", err)
			}
			addr := ArchiveAddress{PartitionKey: part.key, ByteOffset: int64(start), EntryID: rec.ID}
			if rec.Type == EntryTypeToolCallEffect {
				if rec.ToolCallEffect == nil {
					return nil, fmt.Errorf("unified_store: read transcript: effect record %s has no effect", rec.ID)
				}
				effects = append(effects, pendingEffect{addr: addr, effect: rec.ToolCallEffect})
				continue
			}
			if rec.ViewMembership == "" {
				return nil, fmt.Errorf("unified_store: read transcript: record %s at %s@%d has no view_membership (an unconverted line; the archive carries no reader default)",
					rec.ID, part.key, start)
			}
			if rec.ViewMembership == ViewMembershipModel || rec.Type == EntryTypeModelRef {
				continue // model-view records are not chat
			}
			index[addr] = len(entries)
			entries = append(entries, rec.TranscriptEntry)
		}
	}
	if err := applyToolCallEffects(entries, index, effects, parts); err != nil {
		return nil, fmt.Errorf("unified_store: read transcript: %w", err)
	}
	return deriveCancelTruncation(entries), nil
}

// applyToolCallEffects merges the effects into entries (last writer wins per
// target call, in physical order).
func applyToolCallEffects(entries []TranscriptEntry, index map[ArchiveAddress]int, effects []pendingEffect, parts []partitionRef) error {
	if len(effects) == 0 {
		return nil
	}
	byAddr := make(map[ArchiveAddress]pendingEffect, len(effects))
	for _, e := range effects {
		byAddr[e.addr] = e
	}
	oldest := ""
	if len(parts) > 0 {
		keys := make([]string, 0, len(parts))
		for _, p := range parts {
			keys = append(keys, p.key)
		}
		sort.Strings(keys)
		oldest = keys[0]
	}
	retracted := map[ArchiveAddress]bool{}
	for _, e := range effects {
		for _, r := range e.effect.Retracts {
			target, ok := byAddr[r]
			if !ok && oldest != "" && r.PartitionKey < oldest {
				continue // the retracted effect expired by retention
			}
			if !ok || target.effect == nil || len(target.effect.Projections) == 0 {
				return fmt.Errorf("effect %s retracts %s, which is not a projection effect", e.addr.EntryID, r.EntryID)
			}
			retracted[r] = true
		}
	}
	resolve := func(effect pendingEffect, target ArchiveAddress, id ToolCallID) (*ToolCall, bool, error) {
		idx, ok := index[target]
		if !ok {
			if oldest != "" && target.PartitionKey < oldest {
				return nil, false, nil // target expired by retention
			}
			return nil, false, fmt.Errorf("effect %s names %s@%d, which is not a chat record", effect.addr.EntryID, target.PartitionKey, target.ByteOffset)
		}
		calls := entries[idx].ToolCalls
		for j := range calls {
			if calls[j].ID == id {
				return &calls[j], true, nil
			}
		}
		return nil, false, fmt.Errorf("effect %s: record %s carries no tool call %q", effect.addr.EntryID, target.EntryID, id)
	}
	for _, e := range effects {
		if retracted[e.addr] || len(e.effect.Retracts) > 0 {
			continue
		}
		if s := e.effect.Settle; s != nil {
			tc, found, err := resolve(e, s.Target, s.ToolCall.ID)
			if err != nil {
				return err
			}
			if !found || (s.IfStatus != "" && tc.Status != s.IfStatus) {
				continue
			}
			*tc = s.ToolCall
			continue
		}
		for _, p := range e.effect.Projections {
			tc, found, err := resolve(e, p.Target, p.ToolCallID)
			if err != nil {
				return err
			}
			if !found {
				continue
			}
			tc.ContentState = p.ContentState
			applyProjectionText(tc, p.Text)
		}
	}
	return nil
}
