package session

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RetentionSweep deletes .jsonl files inside session subdirectories whose
// mtime is older than retentionDays*24h. It returns the count of files deleted.
//
// When retentionDays <= 0 the method is a no-op and returns (0, nil).
// Per-file delete errors are logged at Warn and the sweep continues.
// An error is returned only if the base directory walk cannot start.
//
// After all aged .jsonl files are removed, session directories that contain
// zero remaining .jsonl files are removed entirely (sidecar metadata and
// lock files are ignored). An empty session folder is junk by definition —
// it would otherwise continue to appear in ListSessions with no transcript
// content. This semantic is independent of the folder's mtime, which kernel-
// level filesystems update to "now" the moment a child file is removed
// (making any post-deletion mtime check incorrect).
//
// Locking (ADR-057 W15b, FR-050(a)): this method takes ALL 64 session shards,
// in strictly ascending index order, via lockAllSessionShards — the SAME
// FR-050(a) exception ClearAll uses, and for the same reason: this is a
// full-store operation that can touch metaCache for an arbitrary number of
// sessions, and FR-050 forbids ever holding two session shards in any order
// OTHER than the fixed ascending-index one. This replaces the old narrow
// us.mu.Lock()/Unlock() that used to wrap only the second pass's
// directory-removal step. Holding every shard for the WHOLE sweep (including
// the first-pass filesystem walk, not just the second pass) is a bigger
// declared full-store stall than before, and it is DELIBERATELY accepted,
// not an oversight: the spec's own resolution (ADR-057 Ambiguity item 13)
// is "accept the stall; assert no deadlock and no dropped session — the
// operation is already effectively store-global today [ClearAll already
// holds one lock for its entire call], so 64-in-index-order is not a
// regression" — and batching the shard acquisition would reintroduce the
// exact lock-order question this design closes.
func (us *UnifiedStore) RetentionSweep(retentionDays int) (int, error) {
	if retentionDays <= 0 {
		return 0, nil
	}

	unlock := us.lockAllSessionShards()
	defer unlock()

	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	removed := 0
	// Track session dirs we touched so we can decide which to remove entirely.
	touchedSessionDirs := make(map[string]struct{})

	err := filepath.WalkDir(us.baseDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == us.baseDir {
				return walkErr
			}
			slog.Warn("session: retention_sweep: walk error", "path", path, "error", walkErr)
			return nil
		}

		if d.IsDir() {
			if path == us.baseDir {
				return nil
			}
			return nil
		}

		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}

		rel, err := filepath.Rel(us.baseDir, path)
		if err != nil {
			return err
		}
		parts := strings.SplitN(rel, string(filepath.Separator), 3)
		if len(parts) < 2 {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			slog.Warn("session: retention_sweep: stat failed", "file", path, "error", err)
			return nil
		}

		if info.ModTime().Before(cutoff) {
			if delErr := os.Remove(path); delErr != nil {
				slog.Warn("session: retention_sweep: delete failed", "file", path, "error", delErr)
			} else {
				removed++
				// Only track session dirs for empty-dir cleanup; .context/ is a
				// shared archive container — do not queue it for removal.
				if parts[0] != ".context" {
					touchedSessionDirs[filepath.Join(us.baseDir, parts[0])] = struct{}{}
				} else {
					// The .context/ entry is a memory.JSONLStore file:
					// each <key>.jsonl has a sibling <key>.meta.json holding
					// the Skip/Count offset. Remove the meta file alongside
					// the archive so that a recycled session key never reads a
					// stale Skip/Count against a now-empty .jsonl (phantom
					// offset bug). Failure to remove the meta is logged at
					// Warn but does NOT revert the removed count — the .jsonl
					// is already gone and the meta file on its own is harmless
					// (readMeta returns defaults when the .jsonl is absent).
					metaPath := strings.TrimSuffix(path, ".jsonl") + ".meta.json"
					if metaDelErr := os.Remove(metaPath); metaDelErr != nil && !os.IsNotExist(metaDelErr) {
						slog.Warn("session: retention_sweep: delete context meta failed",
							"file", metaPath, "error", metaDelErr)
					}
				}
			}
		}

		return nil
	})
	if err != nil {
		return removed, err
	}

	// Second pass: remove session directories that lost all their transcripts
	// to the sweep. An empty session folder (no .jsonl files remaining) is
	// junk regardless of any sidecar metadata's age — ListSessions enumerates
	// the folder on disk, so leaving it behind surfaces a content-less ghost
	// session in the UI. This check is mtime-independent because filesystems
	// (ext4, xfs) bump the parent directory's mtime to "now" the moment a
	// child file is removed, breaking any post-deletion mtime comparison.
	uploadsRoot := us.uploadsRoot() // home-rooted per ADR-017 D5 / N-B fix
	for sessDir := range touchedSessionDirs {
		entries, readErr := os.ReadDir(sessDir)
		if readErr != nil {
			continue
		}
		hasTranscript := false
		for _, ent := range entries {
			if !ent.IsDir() && strings.HasSuffix(ent.Name(), ".jsonl") {
				hasTranscript = true
				break
			}
		}
		if hasTranscript {
			continue
		}
		// Removing sessDir also removes its meta.json — with metaCache in
		// play, an unguarded os.RemoveAll here would leave a stale cache
		// entry pointing at a directory that no longer exists on disk
		// (a phantom session that ListSessions/GetMeta would keep serving
		// forever). The whole method already holds every session shard (see
		// this function's doc comment, W15b) so no OTHER goroutine can be
		// mid-mutation on sessID concurrently; the cache delete itself still
		// goes through cacheMu, same as every other metaCache access. The
		// first pass above (aged .jsonl file removal) never touches
		// meta.json, so it needed no lock either, before or after W15b.
		sessID := filepath.Base(sessDir)
		dirRmErr := os.RemoveAll(sessDir)
		if dirRmErr == nil {
			us.cacheMu.Lock()
			delete(us.metaCache, sessID)
			us.cacheMu.Unlock()
			us.u4IndexEvict(sessID) // FR-097
		}
		if dirRmErr != nil {
			slog.Warn("session: retention_sweep: dir remove failed", "dir", sessDir, "error", dirRmErr)
		}
		// Cascade-delete uploads for this session so disk space is reclaimed.
		uploadsDir := filepath.Join(uploadsRoot, sessID)
		if rmErr := os.RemoveAll(uploadsDir); rmErr != nil && !os.IsNotExist(rmErr) {
			slog.Warn("session: retention_sweep: cascade-delete uploads failed",
				"session_id", sessID, "error", rmErr)
		}
	}

	return removed, nil
}
