// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-086 GOAL-FR-005 (wave S2) — the AskUserQuestion parked-question set
// relocates off goal.json onto its own session-owned file, pending_ask.json,
// with the same per-session striped-shard path, atomic-write and
// advisory-lock discipline as the other ADR-057 W23 targeted field groups
// (u5GoalFile/u5LoopFile/u5StatsFile, unified_meta_files.go).
//
// WHY THIS MOVES. PendingAskJSON (askuser.PendingSet, pkg/askuser) is
// session-scoped interaction state for the AskUserQuestion tool — a parked
// question predates, outlives and is orthogonal to whichever goal (if any)
// is active on the session when it is asked. It rode inside goal.json
// (u5GoalFile) only for ADR-057 U5's original storage-layout convenience
// (ADR-086 §"PendingAskJSON does NOT move with the goal"). ADR-086 turns the
// OTHER Goal* fields into their own stored entity (pkg/goal), and goal.json
// itself is deleted wholesale once every Goal* reader is re-pointed (wave
// S6, which deletes u5ReadGoalFile/u5WriteGoalLocked outright — see that
// wave's row). PendingAskJSON MUST NOT go with them (GOAL-FR-005: "It MUST
// NOT be carried into the goal entity") and MUST already be living
// somewhere that survives S6's deletion by the time S6 runs — this file is
// that somewhere, landed additively, in this same commit, ahead of it.
//
// WHAT DOES NOT CHANGE. SessionMeta.PendingAskJSON, UnifiedMeta's embedded
// copy of it, and MetaPatch.PendingAskJSON (daypartition.go/unified.go) keep
// their exact names, JSON tags and semantics — pkg/askuser and
// pkg/gateway/replay.go read and write them exactly as before them and need
// no change in this wave or any other. Only the ON-DISK LOCATION changes:
// FROM goal.json's "pending_ask" key TO this file's own pending_ask.json,
// under the SAME "pending_ask" JSON key, so an askuser.PendingSet blob
// written before this wave's u5WriteGoalLocked and one written after this
// wave's u5WritePendingAskLocked are byte-identical once relocated.
//
// GREENFIELD (D-F, operator decision 2026-09-11): no rescue of a
// pre-existing goal.json's "pending_ask" key is built or attempted. This is
// a code move in a fresh tree, not a migration — nothing reads an old
// install's goal.json for this field, and no per-session warning is
// emitted. See goal_meta_greenfield_test.go's own greenfield contract for
// the sibling precedent (ADR-081 D9's confirm-gate field removal) this
// follows.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// u5PendingAskFile is pending_ask.json's on-disk shape (GOAL-FR-005):
// exactly the one field PendingAskJSON used to carry inside u5GoalFile,
// under the SAME JSON key ("pending_ask"). Carries no UpdatedAt of its own,
// for the same reason u5GoalFile/u5LoopFile do not (unified_meta_files.go):
// a parked-question change is not "session activity" for recency-sort
// purposes (mirrors goal.json's own no-recency-bump rule, BDD-59).
type u5PendingAskFile struct {
	PendingAskJSON string `json:"pending_ask,omitempty"`
}

// u5PendingAskFromMeta extracts pending_ask.json's on-disk shape from an
// in-memory *UnifiedMeta. Used both by the targeted writer below and by
// writeMetaLocked's (unified.go) diff-based dispatch to decide whether a
// caller's supplied meta actually changed PendingAskJSON.
func u5PendingAskFromMeta(meta *UnifiedMeta) u5PendingAskFile {
	return u5PendingAskFile{PendingAskJSON: meta.PendingAskJSON}
}

// u5ReadPendingAskFile reads pending_ask.json. Same absent-vs-corrupt
// contract as the other optional group readers (u5ReadStatsFile/
// u5ReadGoalFile/u5ReadLoopFile, unified_meta_files.go): absent -> zero
// value, nil error (a session that never parked a question never gains an
// empty pending_ask.json it never touched — the same lazy-file-creation
// property W23 established for goal.json/loop.json); present-but-corrupt ->
// error, never silently zeroed (BDD-62's "corrupt and absent are
// deliberately different outcomes", unchanged by this relocation).
func u5ReadPendingAskFile(sessionDir string) (u5PendingAskFile, error) {
	var f u5PendingAskFile
	path := filepath.Join(sessionDir, "pending_ask.json")
	data, err := readFileFn(path)
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return f, fmt.Errorf("read %q: %w", path, err)
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("parse %q: %w", path, err)
	}
	return f, nil
}

// u5WritePendingAskLocked writes pending_ask.json and updates ONLY the
// PendingAskJSON field on the cached entry. Never touches meta.json/
// stats.json/goal.json/loop.json, and never bumps the composed UpdatedAt —
// same field-group isolation and no-recency-bump rationale as
// u5WriteGoalLocked/u5WriteLoopLocked (unified_meta_files.go). Disk write
// happens BEFORE any cache mutation (FR-049 — cacheMu is never held across
// an os.*/fileutil.* call; a failed disk write must never touch the cache,
// the same MB-1 guarantee the other three targeted writers preserve).
// Caller holds sessionID's shard throughout (the "Locked" suffix convention
// shared with the retired writeMetaLocked/readMetaLocked).
func (us *UnifiedStore) u5WritePendingAskLocked(sessionID string, meta *UnifiedMeta) error {
	file := u5PendingAskFromMeta(meta)
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("unified_store: marshal pending_ask.json: %w", err)
	}
	pendingAskPath := filepath.Join(us.baseDir, sessionID, "pending_ask.json")
	if err := fileutil.WithFlock(pendingAskPath, func() error {
		return writeFileAtomicFn(pendingAskPath, data, 0o600)
	}); err != nil {
		return err
	}

	us.cacheMu.Lock()
	if cached, ok := us.metaCache[sessionID]; ok {
		cached.PendingAskJSON = meta.PendingAskJSON
	} else {
		us.metaCache[sessionID] = meta.Clone()
	}
	us.cacheMu.Unlock()
	return nil
}
