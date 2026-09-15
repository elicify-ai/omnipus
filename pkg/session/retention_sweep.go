package session

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// goalRetentionSweepFn is a package-level, swappable hook (GOAL-FR-043,
// C-26, wave S3) that RetentionSweep invokes as a SEPARATE pass, strictly
// AFTER its own lockAllSessionShards hold has been fully released — never
// nested inside it. It receives the exact retentionDays RetentionSweep
// itself was called with ("same schedule, same retention-days argument",
// C-26's own words), so a goal record ages out under precisely the rule
// this method already applies to session transcripts.
//
// The zero value (nil) is a no-op: a build that never wires a goal store
// behaves exactly as it did before ADR-086. This package deliberately does
// NOT import pkg/goal (mirrors pkg/goal/doc.go's "this package does not
// itself import pkg/session" the other way round) — a direct
// *goal.Store-typed hook here would force pkg/session to import pkg/goal,
// and pkg/goal/retention.go's own hook contract (its sessionExists
// predicate) needs the opposite direction to stay dependency-free, which
// would be a real import cycle if both packages depended on each other.
// Keeping this hook's signature free of any pkg/goal type means the
// closure that DOES know about *goal.Store — built by whichever component
// constructs both a *session.UnifiedStore and a *goal.Store together, via
// SetGoalRetentionSweepFn — lives in that (necessarily higher-level)
// package instead, exactly the way this package's own
// retentionToolResultSweepFn/retentionTaskRunSweepFn hooks are wired in
// pkg/gateway/gateway.go today.
//
// A non-nil error from the hook is logged at Warn and does not fail
// RetentionSweep's own return value — the session-file sweep already
// completed successfully by the time the hook runs, and a goal-side sweep
// failure must not be reported as "the whole retention sweep failed" (this
// mirrors executeSweepTick's existing treatment of
// retentionToolResultSweepFn/retentionTaskRunSweepFn's own errors).
var goalRetentionSweepFn func(retentionDays int) (removed int, err error)

// SetGoalRetentionSweepFn installs (or, passed nil, uninstalls) the goal
// retention hook RetentionSweep calls after releasing its session-shard
// lock. Intended to be called once, at boot, before any concurrent sweep
// can run — mirroring this codebase's existing SetXxx hook-injection
// convention (e.g. pkg/agent's SetLiveWindowLookup, pkg/providers'
// SetDefaultCredentialStore) rather than adding a mutex no other hook of
// this shape in this codebase carries.
func SetGoalRetentionSweepFn(fn func(retentionDays int) (removed int, err error)) {
	goalRetentionSweepFn = fn
}

// RetentionSweep deletes .jsonl files inside session subdirectories whose
// mtime is older than retentionDays*24h. It returns the count of files deleted.
//
// When retentionDays <= 0 the method is a no-op and returns (0, nil) —
// goalRetentionSweepFn is not invoked either in that case, keeping the two
// stores' "retention disabled" behaviour identical (GOAL-FR-043).
// Per-file delete errors are logged at Warn and the sweep continues.
// An error is returned only if the base directory walk cannot start; on
// that path the goal hook is also skipped, since "same schedule" does not
// mean "run even when the session sweep itself never completed".
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
//
// Cross-package lock order (ADR-086, C-26, this file's own contribution to
// the chain documented authoritatively in unified_lock.go): goalLock ->
// sessionLock -> cacheMu, one-directional. This method therefore unlocks
// its own session shards EXPLICITLY (not via a deferred call reaching all
// the way to function return) before ever invoking goalRetentionSweepFn,
// on every return path — nothing inside the lockAllSessionShards hold may
// call into a goal store, in either direction.
func (us *UnifiedStore) RetentionSweep(retentionDays int) (int, error) {
	if retentionDays <= 0 {
		return 0, nil
	}

	unlock := us.lockAllSessionShards()

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
		unlock()
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
			delete(us.dirtyStats, sessID) // ADR-057 U6 W24: no dangling flush target
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

	// Cross-package lock order (see this method's doc comment and
	// unified_lock.go's authoritative copy): every session shard is
	// released here, BEFORE the goal retention pass — never nested inside
	// lockAllSessionShards's hold, in either direction.
	unlock()

	if goalRetentionSweepFn != nil {
		goalRemoved, goalErr := goalRetentionSweepFn(retentionDays)
		if goalErr != nil {
			slog.Warn("session: retention_sweep: goal sweep failed",
				"error", goalErr)
		} else if goalRemoved > 0 {
			slog.Info("session: retention_sweep: goal records removed",
				"removed", goalRemoved, "retention_days", retentionDays)
		}
	}

	return removed, nil
}
