package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// ReadSessionTokensTotal returns the existing accounted session total, not a
// sum of transcript rows. Nil means usage authority is unavailable. This is
// bounded metadata work and never reconstructs missing historical accounting.
func (us *UnifiedStore) ReadSessionTokensTotal(sessionID string) *int {
	if us == nil || validateSessionID(sessionID) != nil {
		return nil
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	meta, err := us.readMetaLocked(sessionID)
	if err != nil || meta.Stats.TokensTotal < 0 {
		return nil
	}
	total := meta.Stats.TokensTotal
	// Nonempty accounting in the current cache remains authoritative even if
	// history is unavailable. The stats writer updates this cache only after
	// accepting the aggregate; live_only attach must not erase that evidence.
	if total != 0 || meta.Stats.MessageCount != 0 {
		return &total
	}
	dir := filepath.Join(us.baseDir, sessionID)
	if info, statErr := os.Stat(filepath.Join(dir, "stats.json")); statErr == nil {
		if info.Mode().IsRegular() && info.Size() > 0 {
			return &total
		}
		return nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil
	}
	// A genuinely unused session has no lazy stats file yet (FR-053). Require
	// the published empty baseline AND the empty current file, rather than
	// treating a historical session whose stats were lost as a new zero.
	if len(meta.Partitions) != 0 {
		return nil
	}
	cp, err := historyLoadCheckpoint(context.Background(), dir, sessionID)
	if err != nil || cp.Rank != 0 {
		return nil
	}
	info, err := os.Stat(filepath.Join(dir, archiveCurrentFile))
	if err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
		return nil
	}
	return &total
}
