// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-057 U6 (Wave D) — W24, the FR-061...FR-067 stats-flush throttle, plus
// the FR-097a reconcile prune and FR-102 barrier live in unified.go (the
// method they extend). This is chain link 3/3 of the U4->U5->U6 sequence:
// U4 (unified_lock.go) built the striped lock + FR-101 seam, U5
// (unified_meta_files.go) built the four-file split and the targeted
// u5Write*Locked writers, and this unit throttles the ONE writer that used
// to fire on every streamed token — u5WriteStatsLocked — without adding a
// second stats-writing function (a cross-unit obligation from U5: "do not
// add a parallel stats write path — wrap/defer the existing one").
//
// THE PROBLEM THIS CLOSES. Before this file, AppendTranscript called
// u5WriteStatsLocked synchronously on every streamed transcript line: a
// marshal, a WithFlock, an fsync, a rename and a directory fsync per token
// (US-13's governing complaint). That write is expendable by the store's own
// admission — AppendTranscript already returns nil on a stats-write failure
// today. W24 makes that same tolerance explicit and useful: the per-token
// counter bump becomes an in-memory-only mutation of the cached meta
// (FR-061), marked dirty, and a periodic flusher persists dirty sessions'
// stats.json on a bounded interval (FR-063). Every OTHER write in this
// store — SetMeta's identity/goal/loop groups, SwitchAgent,
// CreateSessionWithID, AppendTranscriptStrict — is untouched and stays
// synchronous (FR-065): only the transcript-append counter path is
// throttled.
//
// AC-22's trap, and how this file avoids it. A grill round found the
// throttle mechanism can be ENTIRELY non-functional — the periodic flusher
// goroutine never started at all — while every "burst does not touch
// stats.json" / "forced flush points work" / "event writes are immediate"
// test still passes, because all of those are satisfiable by a store that
// has flushed NOTHING, ever. The one property that catches a dead flusher is
// BDD-93/AC-22(scenario 7): one append, NO other action whatsoever — no
// SetMeta, no DeleteSession, no Close, no test-driven tick — and stats.json
// still becomes current after more than one flush interval on the real
// clock. startStatsFlusher is therefore called unconditionally from
// NewUnifiedStoreWithHome (unified.go) — every store this package
// constructs has a live background flusher from the moment it exists, not
// only when some caller remembers to start one.
//
// Ownership Rule 6 (`[grill m-4]`): every unexported package-level
// identifier this file introduces is prefixed u6, mirroring U4's u4 and U5's
// u5 prefixes, to rule out a silent redeclaration collision with a
// concurrently-landing unit in the same package. (This wave has no
// concurrent sibling in pkg/session — U6 is the chain's last link — but the
// convention is kept for consistency with the two files it directly
// extends.)
package session

import (
	"log/slog"
	"maps"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// u6MarkStatsDirtyLocked is the FR-061 in-memory-only counterpart to
// u5WriteStatsLocked (unified_meta_files.go): it updates ONLY the Stats
// field group on the cached entry — the exact same cache-mutation logic
// u5WriteStatsLocked itself performs — but issues NO disk write, and instead
// records sessionID in dirtyStats for the periodic flusher (or the next
// forced-flush point) to persist later (FR-063). This is AppendTranscript's
// (unified.go) per-token write path ONLY; every other stats-touching path in
// this package continues to call u5WriteStatsLocked directly and is
// therefore never throttled. Caller must hold sessionID's shard (see
// lockSession) — same convention as every other *Locked method in this
// package.
func (us *UnifiedStore) u6MarkStatsDirtyLocked(sessionID string, meta *UnifiedMeta) {
	us.cacheMu.Lock()
	if cached, ok := us.metaCache[sessionID]; ok {
		cached.Stats = meta.Stats
		cached.Stats.ByModel = maps.Clone(meta.Stats.ByModel)
		if meta.UpdatedAt.After(cached.UpdatedAt) {
			cached.UpdatedAt = meta.UpdatedAt
		}
	} else {
		us.metaCache[sessionID] = meta.Clone()
	}
	if us.dirtyStats == nil {
		us.dirtyStats = make(map[string]struct{})
	}
	us.dirtyStats[sessionID] = struct{}{}
	us.cacheMu.Unlock()
}

// u6FlushDirtySessionLocked persists sessionID's stats.json via
// u5WriteStatsLocked — the SAME disk-writing function every other stats
// mutation path in this package already uses; this throttle adds no second
// writer — using the CURRENTLY cached meta, if and only if sessionID
// currently has a pending in-memory-only delta (FR-063). No-op (nil error)
// when the session is not dirty, or when its cache entry is already gone
// (e.g. evicted by a concurrent DeleteSession that won the race for this
// same shard — impossible for the SAME session id, since both this method's
// callers and DeleteSession require the caller to already hold sessionID's
// shard, but reachable for an id this store never cached at all). Clears the
// dirty mark ONLY on a successful write, so a failed flush is retried on the
// next tick or forced-flush point rather than being silently dropped.
// Caller must hold sessionID's shard.
func (us *UnifiedStore) u6FlushDirtySessionLocked(sessionID string) error {
	us.cacheMu.Lock()
	_, dirty := us.dirtyStats[sessionID]
	var metaClone *UnifiedMeta
	if dirty {
		if cached, ok := us.metaCache[sessionID]; ok {
			metaClone = cached.Clone()
		}
	}
	us.cacheMu.Unlock()

	if !dirty || metaClone == nil {
		return nil
	}
	if err := us.u5WriteStatsLocked(sessionID, metaClone); err != nil {
		return err
	}
	us.cacheMu.Lock()
	delete(us.dirtyStats, sessionID)
	us.cacheMu.Unlock()
	return nil
}

// u6SnapshotDirtySessions returns a point-in-time copy of the dirty-session
// set's keys (FR-063: "only for dirty sessions"). Deliberately does not
// clear the set itself — each per-id flush clears only its own entry, under
// that session's own shard, via u6FlushDirtySessionLocked, exactly like
// every other mutation in this store (FR-050's one-directional lock order).
func (us *UnifiedStore) u6SnapshotDirtySessions() []string {
	us.cacheMu.RLock()
	ids := make([]string, 0, len(us.dirtyStats))
	for id := range us.dirtyStats {
		ids = append(ids, id)
	}
	us.cacheMu.RUnlock()
	return ids
}

// flushAllDirtyStats persists every currently-dirty session's stats.json,
// each write taking that session's own shard (FR-063: "each write taking
// that session's shard"). Used by both the periodic flusher's tick and
// UnifiedStore.Close's forced final flush (FR-064).
func (us *UnifiedStore) flushAllDirtyStats() {
	for _, id := range us.u6SnapshotDirtySessions() {
		h := us.lockSession(id)
		if err := us.u6FlushDirtySessionLocked(id); err != nil {
			slog.Warn("unified_store: stats flush failed", "session_id", id, "error", err)
		}
		h.Unlock()
	}
}

// FlushSessionStats forces sessionID's pending in-memory-only stats delta
// (if any) to stats.json immediately, clearing its dirty mark. Exported as
// the forced-flush entry point for callers OUTSIDE this package — FR-064's
// fourth forced-flush point, the child-terminal CloseSession teardown, is
// owned by U17b (pkg/agent/session_end.go, Wave E — lands after this unit)
// and is expected to call this once a delegated child's sub-turn completes.
// No-op (nil error) for a session with nothing pending.
func (us *UnifiedStore) FlushSessionStats(sessionID string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	return us.u6FlushDirtySessionLocked(sessionID)
}

// --- the periodic flusher goroutine (FR-063, FR-067) ---

// startStatsFlusher launches the FR-063 periodic flusher goroutine exactly
// once per store (a second call is a no-op — guarded by flusherStarted
// under flushMu), defaulting its period to config.DefaultSessionStatsFlushInterval
// (5s). Called unconditionally from NewUnifiedStoreWithHome (unified.go) —
// see this file's package doc comment for why an unconditionally-started
// flusher is load-bearing for AC-22, not a nicety.
//
// Config wiring, without an import cycle: pkg/config imports only pkg,
// pkg/fileutil and pkg/logger (verified 2026-08-03 — none of the three
// imports pkg/session), so pkg/session importing pkg/config for this one
// constant is safe in EITHER direction. What pkg/session does NOT have is a
// *config.Config value at construction time — NewUnifiedStoreWithHome's two
// existing call sites (pkg/agent/loop.go:736, pkg/agent/instance.go:966) are
// outside this unit's ownership (Rule 2), and their signature is a
// cross-unit contract this unit must not change. So the DEFAULT is read
// here, directly from the shared constant (never duplicated as a bare "5 *
// time.Second" literal that could silently drift from config's own
// default), and a caller that HAS resolved an operator's
// config.SessionConfig — by calling
// cfg.Sessions.EffectiveStatsFlushInterval() — overrides it after
// construction via SetStatsFlushInterval, rather than this package resolving
// config for itself.
func (us *UnifiedStore) startStatsFlusher() {
	us.flushMu.Lock()
	if us.flusherStarted {
		us.flushMu.Unlock()
		return
	}
	us.flusherStarted = true
	if us.statsFlushInterval <= 0 {
		us.statsFlushInterval = config.DefaultSessionStatsFlushInterval
	}
	timer := time.NewTimer(us.statsFlushInterval)
	us.flushTimer = timer
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	us.flushStopCh = stopCh
	us.flushDoneCh = doneCh
	us.flushMu.Unlock()

	go us.runStatsFlusher(timer, stopCh, doneCh)
}

// runStatsFlusher is the flusher goroutine's body: on every tick it flushes
// all currently-dirty sessions (flushAllDirtyStats), then re-arms timer with
// the current interval so a SetStatsFlushInterval call between ticks takes
// effect from the NEXT tick onward without needing to restart the
// goroutine. Exits when stopCh is closed (see stopStatsFlusher).
func (us *UnifiedStore) runStatsFlusher(timer *time.Timer, stopCh, doneCh chan struct{}) {
	defer close(doneCh)
	defer timer.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-timer.C:
			us.flushAllDirtyStats()
			timer.Reset(us.currentFlushInterval())
		}
	}
}

// currentFlushInterval returns the interval the flusher goroutine is
// currently configured to use.
func (us *UnifiedStore) currentFlushInterval() time.Duration {
	us.flushMu.Lock()
	defer us.flushMu.Unlock()
	return us.statsFlushInterval
}

// SetStatsFlushInterval overrides the FR-067 periodic flush interval this
// store's flusher goroutine uses. Unlike a design that only updates the
// stored value for the flusher to pick up on its NEXT tick, this resets the
// LIVE timer immediately (best-effort — the drain below can race a timer
// that fires in the same instant, in which case one extra tick at the OLD
// interval may still occur before the new one takes hold; harmless, since a
// spurious extra flush is a no-op for a non-dirty session and idempotent for
// a dirty one). Without this, a test or operator override would silently
// wait out however much of the OLD interval happened to be left — up to the
// full 5s default — before ever taking effect, which defeats the purpose of
// an override. See startStatsFlusher's doc comment for why this exists
// instead of a constructor parameter or an in-package config read. d <= 0 is
// treated as "use the FR-067 default" (config.DefaultSessionStatsFlushInterval)
// rather than disabling the flusher — FR-067 defines no "off" state; a
// caller that wants deterministic forced-flush-only behavior in a test
// should instead call FlushSessionStats or drive an actual forced-flush
// point (SetMeta/DeleteSession/Close), not try to stop the periodic path.
func (us *UnifiedStore) SetStatsFlushInterval(d time.Duration) {
	if d <= 0 {
		d = config.DefaultSessionStatsFlushInterval
	}
	us.flushMu.Lock()
	us.statsFlushInterval = d
	timer := us.flushTimer
	us.flushMu.Unlock()
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(d)
}

// StatsFlushInterval returns the interval this store's periodic flusher is
// currently configured to use — an introspection seam for tests (#105:
// "the key exists, defaults to 5s").
func (us *UnifiedStore) StatsFlushInterval() time.Duration {
	return us.currentFlushInterval()
}

// stopStatsFlusher stops the periodic flusher goroutine and waits for it to
// fully exit before returning. Safe to call more than once, and safe to call
// on a store whose flusher was never started (both are no-ops, guarded by
// flusherStarted under flushMu). Called from Close (unified.go).
func (us *UnifiedStore) stopStatsFlusher() {
	us.flushMu.Lock()
	if !us.flusherStarted {
		us.flushMu.Unlock()
		return
	}
	stopCh := us.flushStopCh
	doneCh := us.flushDoneCh
	us.flusherStarted = false
	us.flushMu.Unlock()

	close(stopCh)
	<-doneCh
}

// u6ReconcileSnapshotBarrierFn is the ADR-057 FR-102 test seam exposing
// ListSessions' reconcile -> snapshot boundary (unified.go) to an in-package
// test. BDD-95/#92/SC-041 require a DeleteSession to be interleaved
// DETERMINISTICALLY between ListSessions' reconcile pass (add + FR-097a
// prune) and its final cacheMu-guarded snapshot — go test -race is not a
// scheduler and will not reliably hit that window on its own, and this
// property (like lock-acquisition order, FR-101) has no on-disk artefact to
// assert on, so binding rules 1-2's narrow stated exception (this spec's
// "The one narrow exception to rules 1-2") applies: a test may install a
// production seam here, because it IS one — required by FR-102, not a
// test-only substitute for the thing under test. Defaults to a no-op; only a
// test in this package may override it (unexported, same discipline as
// unified_lock.go's sessionLockAcquireFn/sessionLockReleaseFn).
var u6ReconcileSnapshotBarrierFn = func() {}
