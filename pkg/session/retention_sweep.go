package session

import (
	"errors"
	"fmt"
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
	removed, err := us.retentionSweep(retentionDays)
	if dispatcher := us.retentionNoticeDispatch.Load(); dispatcher != nil {
		err = errors.Join(err, (*dispatcher)())
	}
	return removed, err
}

func (us *UnifiedStore) retentionSweep(retentionDays int) (int, error) {
	release := us.lockAllSessionShards()
	locked := true
	unlock := func() {
		if locked {
			locked = false
			release()
		}
	}
	defer unlock() // Observation-only crash tests must unwind the ownership hold.

	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	removed := 0
	// Track session dirs we touched so we can decide which to remove entirely.
	touchedSessionDirs := make(map[string]struct{})

	// Rolled day partitions expire through the archive's own path, which also
	// moves each session's window past the expired content. The generic walk
	// below never deletes them.
	var notices []expiryNotice
	blocked := make(map[string]bool)
	expiryErr := us.expireArchiveSessions(retentionDays, cutoff, &removed, touchedSessionDirs, &notices, blocked)

	// A main chat is deleted only with its agent (founder rule). Retention still
	// expires a main chat's old rolled content above (FR-007), but never deletes
	// its live partition, its identity (meta.json) or its directory.
	protectedMain := make(map[string]bool)
	isProtected := func(sessionID string) bool {
		if blocked[sessionID] {
			return true // Never follow a failed classification with destructive fallback.
		}
		if p, ok := protectedMain[sessionID]; ok {
			return p
		}
		p := us.retentionProtected(sessionID)
		protectedMain[sessionID] = p
		return p
	}

	var err error
	if retentionDays > 0 {
		err = filepath.WalkDir(us.baseDir, func(path string, d os.DirEntry, walkErr error) error {
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
			if parts[0] != ".context" && transcriptDayPattern.MatchString(d.Name()) {
				return nil // a rolled day partition: owned by expireArchiveSessions
			}
			if parts[0] != ".context" && isProtected(parts[0]) {
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
						if metaDelErr := os.Remove(metaPath); metaDelErr != nil && !errors.Is(metaDelErr, os.ErrNotExist) {
							slog.Warn("session: retention_sweep: delete context meta failed",
								"file", metaPath, "error", metaDelErr)
						}
					}
				}
			}

			return nil
		})
	}
	if err != nil {
		unlock()
		return removed, errors.Join(err, expiryErr)
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
		if isProtected(filepath.Base(sessDir)) {
			continue
		}
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
		state, controlErr := loadSavedInputLifecycle(sessDir, filepath.Base(sessDir))
		if controlErr != nil {
			expiryErr = errors.Join(expiryErr, controlErr)
			continue
		}
		owed := false
		for _, notice := range state.Notices {
			owed = owed || !notice.ModelAdmitted
		}
		if owed {
			// Keep only identity/control while an operational notice is owed.
			// Its aged payload was still deleted; this is not a report pin.
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
		if rmErr := os.RemoveAll(uploadsDir); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			slog.Warn("session: retention_sweep: cascade-delete uploads failed",
				"session_id", sessID, "error", rmErr)
		}
	}

	// Cross-package lock order (see this method's doc comment and
	// unified_lock.go's authoritative copy): every session shard is
	// released here, BEFORE the goal retention pass — never nested inside
	// lockAllSessionShards's hold, in either direction.
	unlock()

	// FR-007: tell each session whose window moved. Written after the shards
	// are released (the append takes its own shard).
	expiryErr = errors.Join(expiryErr, us.recordExpiryNotices(notices))

	if retentionDays > 0 && goalRetentionSweepFn != nil {
		goalRemoved, goalErr := goalRetentionSweepFn(retentionDays)
		if goalErr != nil {
			slog.Warn("session: retention_sweep: goal sweep failed",
				"error", goalErr)
		} else if goalRemoved > 0 {
			slog.Info("session: retention_sweep: goal records removed",
				"removed", goalRemoved, "retention_days", retentionDays)
		}
	}

	return removed, expiryErr
}

// expireArchiveSessions runs the archive expiry path over every session
// directory, adding the removed partition count to *removed and marking each
// touched directory for the empty-directory pass. A session whose expiry fails
// is skipped and its error joined into the result; the others still run.
func (us *UnifiedStore) expireArchiveSessions(retentionDays int, cutoff time.Time, removed *int, touched map[string]struct{}, notices *[]expiryNotice, blocked map[string]bool) error {
	entries, err := os.ReadDir(us.baseDir)
	if err != nil {
		return fmt.Errorf("session: retention_sweep: list sessions: %w", err)
	}
	var errs error
	for _, ent := range entries {
		if !ent.IsDir() || strings.HasPrefix(ent.Name(), ".") {
			continue
		}
		var notice ExpiryNotice
		var expErr error
		if us.retentionProtected(ent.Name()) {
			notice, expErr = us.backend.expireSession(ent.Name(), retentionDays)
		} else {
			notice, expErr = us.backend.expireSessionWithCurrent(ent.Name(), retentionDays, &cutoff)
		}
		// Save the deterministic chat/prepared-model notice before empty-directory
		// cleanup can erase the addressed identity or its retryable control file.
		expErr = errors.Join(expErr, us.prepareRetentionNoticeInputsLocked(ent.Name()))
		// Account for what happened even when part of the expiry failed: the
		// partitions that were removed are gone and the window moved past them.
		if notice.Note != "" {
			*notices = append(*notices, expiryNotice{sessionID: ent.Name(), note: notice.Note})
		}
		if notice.RemovedPartitions > 0 {
			*removed += notice.RemovedPartitions
			touched[filepath.Join(us.baseDir, ent.Name())] = struct{}{}
		}
		if expErr != nil {
			blocked[ent.Name()] = true
			slog.Warn("session: retention_sweep: archive expiry failed", "session_id", ent.Name(), "error", expErr)
			errs = errors.Join(errs, fmt.Errorf("session %s: %w", ent.Name(), expErr))
		}
	}
	return errs
}

// retentionProtected reports whether retention must leave a session's files and
// directory alone: a main chat, or a session whose identity cannot be read (an
// unknown session is never deleted on a guess). The caller holds every session
// shard.
func (us *UnifiedStore) retentionProtected(sessionID string) bool {
	meta, err := us.readMetaLocked(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false
		}
		slog.Warn("session: retention_sweep: cannot read session identity; leaving it untouched",
			"session_id", sessionID, "error", err)
		return true
	}
	return meta.Type == SessionTypeMain
}

// expiryNotice is one session's FR-007 agent expiry notice, waiting to be
// written to its transcript.
type expiryNotice struct{ sessionID, note string }

// recordExpiryNotices appends each notice to its session's chat transcript as a
// system entry (SystemSubtypeExpiryNotice). A session that no longer exists
// (its emptied directory was removed by the same sweep) has nothing to tell.
func (us *UnifiedStore) recordExpiryNotices(notices []expiryNotice) error {
	var errs error
	for _, n := range notices {
		meta, err := us.GetMeta(n.sessionID)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				errs = errors.Join(errs, fmt.Errorf("session %s: expiry notice: %w", n.sessionID, err))
			}
			continue
		}
		now := time.Now().UTC()
		if _, err := us.AppendTranscriptAddressed(n.sessionID, TranscriptEntry{
			ID:             fmt.Sprintf("expiry-%d", now.UnixNano()),
			Type:           EntryTypeSystem,
			Role:           "system",
			Content:        n.note,
			SystemSubtype:  SystemSubtypeExpiryNotice,
			AgentID:        meta.AgentID,
			Timestamp:      now,
			ViewMembership: ViewMembershipChat,
		}); err != nil {
			errs = errors.Join(errs, fmt.Errorf("session %s: expiry notice: %w", n.sessionID, err))
		}
	}
	return errs
}
