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
func (us *UnifiedStore) RetentionSweep(retentionDays int) (int, error) {
	if retentionDays <= 0 {
		return 0, nil
	}

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
			rel, err := filepath.Rel(us.baseDir, path)
			if err != nil {
				return err
			}
			parts := strings.SplitN(rel, string(filepath.Separator), 2)
			if len(parts) == 1 {
				name := parts[0]
				if name == ".context" {
					return filepath.SkipDir
				}
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
		if parts[0] == ".context" {
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
				touchedSessionDirs[filepath.Join(us.baseDir, parts[0])] = struct{}{}
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
		if rmErr := os.RemoveAll(sessDir); rmErr != nil {
			slog.Warn("session: retention_sweep: dir remove failed", "dir", sessDir, "error", rmErr)
		}
	}

	return removed, nil
}
