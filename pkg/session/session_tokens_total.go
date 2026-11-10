package session

import (
	"context"
	"os"
	"path/filepath"
)

// ReadSessionTokensTotal returns the existing accounted session total, not a
// sum of transcript rows. Nil means the lifetime baseline is unavailable even
// when later messages/calls have recorded truthful partial usage.
func (us *UnifiedStore) ReadSessionTokensTotal(sessionID string) *int {
	if us == nil || validateSessionID(sessionID) != nil {
		return nil
	}
	// Only scalar values leave the cache lock: no alias, metadata-map clone,
	// archive scan or inference from a newly incremented message/token count.
	us.cacheMu.RLock()
	if cached := us.metaCache[sessionID]; cached != nil {
		total, known := cached.Stats.TokensTotal, cached.usageBaselineKnown
		us.cacheMu.RUnlock()
		if known && total >= 0 {
			return &total
		}
		return nil
	}
	us.cacheMu.RUnlock()
	h := us.lockSession(sessionID)
	defer h.Unlock()
	meta, err := us.readMetaLocked(sessionID)
	if err != nil || !meta.usageBaselineKnown || meta.Stats.TokensTotal < 0 {
		return nil
	}
	total := meta.Stats.TokensTotal
	return &total
}

// A missing lazy stats file is real zero only for a verified unused session.
// This runs when metadata loads, BEFORE a later append can obscure that fact.
// Missing fields in an existing stats document never take this exception.
func historyEmptyUsageBaseline(dir, sid string, partitions int) bool {
	if partitions != 0 {
		return false
	}
	cp, err := historyLoadCheckpoint(context.Background(), dir, sid)
	if err != nil || cp.Root != 0 || cp.Rank != 0 || len(cp.ArchiveEnds) != 0 || cp.DispositionEnd != 0 {
		return false
	}
	info, err := os.Lstat(filepath.Join(dir, archiveCurrentFile))
	return err == nil && info.Mode().IsRegular() && info.Size() == 0
}
