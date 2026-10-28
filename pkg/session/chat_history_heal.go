package session

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"path/filepath"
	"time"
)

// Demand self-heal of a derived chat-history index that is too far behind for
// the bounded catch-up (#1470, selfheal-design.md). Only catchup_limit is a
// trigger: every capture/anchor check already passed in the same owner-locked
// reconcile, so the canonical layout is consistent with the old checkpoint.
// Boot and retention keep the non-healing reconcileChatHistoryIndex.
const (
	chatHistoryHealMaxSourceBytes = int64(32 * 1024 * 1024) // strictly greater is refused
	chatHistoryHealMaxAttempts    = 3
	chatHistoryHealBackoff        = 60 * time.Second
	chatHistoryHealTimeout        = 30 * time.Second
)

// chatHistoryHealSlot is the process-wide, capacity-one rebuild slot: at most
// one full rebuild runs at a time. A heal that cannot take it is a transient
// "busy" attempt. Tests may hold it by sending into the channel.
var chatHistoryHealSlot = make(chan struct{}, 1)

// chatHistoryHealState is the per-owner attempt state, guarded by owner.mu.
// It lives for the store's (process's) lifetime; a restart starts afresh.
type chatHistoryHealState struct {
	attempts int
	last     time.Time
	latched  bool
}

func (us *UnifiedStore) historyHealClock() time.Time {
	if us.historyHealNow != nil {
		return us.historyHealNow()
	}
	return time.Now()
}

// historyHealContext bounds one heal attempt by chatHistoryHealTimeout.
func (us *UnifiedStore) historyHealContext(parent context.Context) (context.Context, context.CancelFunc) {
	if us.historyHealWithTimeout != nil {
		return us.historyHealWithTimeout(parent, chatHistoryHealTimeout)
	}
	return context.WithTimeout(parent, chatHistoryHealTimeout)
}

// historyHealTransient reports whether a failure class may be retried after
// the backoff, rather than latching for the process lifetime.
func historyHealTransient(class string) bool {
	return class == "busy" || class == "source_changed" || class == "deadline"
}

// healChatHistoryOwned runs with owner.mu held, after reconcileHistoryOwned
// returned trigger (a catchup_limit unavailability). It returns the original
// trigger unchanged whenever no heal completes.
func (us *UnifiedStore) healChatHistoryOwned(ctx context.Context, sid string, owner *chatHistoryOwner, trigger error) (chatHistoryReconcileResult, error) {
	state := &owner.heal
	now := us.historyHealClock()
	if state.latched || state.attempts > 0 && now.Sub(state.last) < chatHistoryHealBackoff {
		return chatHistoryReconcileResult{}, trigger
	}
	state.attempts++
	state.last = now
	attempt := state.attempts
	started := time.Now()
	fail := func(class string, cause error) (chatHistoryReconcileResult, error) {
		retry := "after_restart"
		if historyHealTransient(class) && state.attempts < chatHistoryHealMaxAttempts {
			retry = "after_backoff"
		} else {
			state.latched = true
		}
		slog.Warn("session: chat history index rebuild failed", "session_id", sid, "trigger", "catchup_limit", "class", class,
			"duration_ms", time.Since(started).Milliseconds(), "attempt", attempt, "retry", retry)
		if cause != nil {
			return chatHistoryReconcileResult{}, cause
		}
		return chatHistoryReconcileResult{}, trigger
	}

	select {
	case chatHistoryHealSlot <- struct{}{}:
		defer func() { <-chatHistoryHealSlot }()
	default:
		return fail("busy", nil)
	}
	dir, err := filepath.Abs(filepath.Join(us.baseDir, sid))
	if err != nil {
		return fail("storage_error", nil)
	}
	size, err := historyHealSourceSize(dir)
	if errors.Is(err, fs.ErrNotExist) {
		// A listed source file vanished during the walk: a proven move such
		// as a day rollover, not a stable storage failure.
		return fail("source_changed", nil)
	}
	if err != nil {
		return fail("storage_error", nil)
	}
	if size > chatHistoryHealMaxSourceBytes {
		return fail("too_large", nil)
	}
	hctx, cancel := us.historyHealContext(context.WithoutCancel(ctx))
	defer cancel()
	build, err := PrepareChatHistoryIndex(hctx, dir, ChatHistoryIndexBuildOptions{demand: true})
	if err == nil {
		summary := build.Summary()
		// Retire in the store's own path spelling: reader pins (retain) are
		// keyed by filepath.Join(us.baseDir, ...), while the builder works on
		// an absolute, symlink-resolved directory.
		generations := filepath.Join(historyIndexPath(filepath.Join(us.baseDir, sid)), "generations")
		err = build.publish(hctx, func(_, keep string) error {
			// Retire with the RUNTIME owner so page readers' retained
			// generations survive until their last release.
			h := us.lockSession(sid)
			defer h.Unlock()
			return owner.retire(generations, keep)
		})
		err = errors.Join(err, build.Close())
		if err == nil {
			// Fold in appends after the build's captured ends; this bounded
			// reconcile is the proof of readiness with live writers. It runs
			// on the detached heal context: a client leaving mid-heal is not
			// a readiness failure.
			result, readyErr := us.reconcileHistoryOwned(hctx, sid)
			if readyErr != nil {
				if errors.Is(hctx.Err(), context.DeadlineExceeded) {
					return fail("deadline", nil)
				}
				return fail("readiness", readyErr)
			}
			*state = chatHistoryHealState{}
			slog.Warn("session: chat history index rebuilt", "session_id", sid, "trigger", "catchup_limit", "action", summary.Action,
				"records", summary.ArchiveRecordCount, "source_groups", summary.SourceGroupCount, "partitions", summary.PartitionCount,
				"bytes", size, "duration_ms", time.Since(started).Milliseconds(), "attempt", attempt)
			return result, nil
		}
	}
	if errors.Is(hctx.Err(), context.DeadlineExceeded) {
		return fail("deadline", nil)
	}
	class := "storage_error"
	var buildErr *ChatHistoryIndexBuildError
	if errors.As(err, &buildErr) {
		class = buildErr.HistoryIndexBuildClass()
	}
	if class == "index_inconsistent" {
		// The existing published index failed its complete proof: real
		// derived damage, refused as such rather than as plain lag.
		return fail(class, historyUnavailable("index_inconsistent", err))
	}
	return fail(class, nil)
}

// historyHealSourceSize is a stat-only total of the session folder, excluding
// the derived .chat-history subtree. No file content is read.
func historyHealSourceSize(dir string) (int64, error) {
	var total int64
	index := historyIndexPath(dir)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == index {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}
