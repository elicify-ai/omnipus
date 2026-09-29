// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_performance_r5_pending_apply_test.go — #904 gate round 4,
// security-lead finding (Medium): a false "applied" clear via the
// file-watcher reload path.
//
// clearAfterReload cleared performancePendingApply whenever a successful
// reload's cycle-start sequence number (reloadOutcomeTracker.startedCount,
// captured as markedAtReload at mark time) was greater than the mark's own.
// That is not enough: gateway.go's serve loop can run runReloadCycle with a
// `first` config snapshot the file-watcher poller
// (gateway_reload.go::setupConfigWatcherPolling, 2s tick + 500ms debounce,
// buffered chan of 1) loaded BEFORE an admin's PUT /performance write —
// queued on configReloadChan while some OTHER reload ran in between and
// bumped the reload-cycle-sequence counter past markedAtReload. That queued
// cycle's cycle NUMBER then postdates the mark even though its CONFIG
// predates it, and the old seq-only check could not tell the two apart: a
// successful `exec(staleConfig)` cleared pending_apply although the admin's
// change was never actually applied.
//
// The fix (rest_performance_pending_apply.go, gateway_reload.go): every
// config a reload cycle applies now also carries its own read-sequence
// number (reloadOutcomeTracker.markConfigRead — stamped by the poller at
// load time, wrapped in watchedConfigChange, or by runReloadCycle's own
// loadNext calls), and clearAfterReload requires readSeq >
// markedAtConfigRead in addition to seq > markedAtReload.
//
// Oracle: rest_performance_pending_apply.go's own doc comment — "The state
// is cleared only by a refresh AND a registry reload that both succeeded
// AFTER it was marked" — a reload whose CONFIG predates the mark did not
// apply anything after it, whatever its cycle number says.
package gateway

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// errUnreachableLoadNext is a sentinel for a loadNext stub that must never
// actually be reached (t.Fatal above it always exits the goroutine first) —
// a fixed non-nil error, never a bare "nilnil" return, so the stub's
// dead-code return statement cannot be mistaken for a real nil-value/nil-err
// success shape.
var errUnreachableLoadNext = errors.New("test bug: this loadNext stub must never be called")

// r5NewCfg returns a valid, minimal config. Its content plays no role in
// these tests — runReloadCycle and exec treat *config.Config as opaque; only
// each config's stamped read-seq matters.
func r5NewCfg() *config.Config {
	return &config.Config{}
}

// TestPerformancePendingApply_StaleWatcherSnapshot_DoesNotFalselyClear
// reproduces the round-4 finding end to end through the real runReloadCycle:
// a config snapshot read BEFORE the mark, handed to runReloadCycle as
// `first` — exactly as gateway.go's serve loop hands the file-watcher
// poller's queued watchedConfigChange to it — whose reload cycle happens to
// be NUMBERED after the mark (seq > markedAtReload) because another reload
// (the admin's own, failed, attempt) ran in between and consumed cycle
// number 1.
func TestPerformancePendingApply_StaleWatcherSnapshot_DoesNotFalselyClear(t *testing.T) {
	api := newMTIAPI(t, "200")
	svc := &services{reloadOutcome: &reloadOutcomeTracker{}}
	svc.reloadOutcome.onSuccess = api.pendingApply.clearAfterReload

	// The file-watcher poller loads a config and stamps it — read #1 — well
	// before the admin's PUT below. This is exactly the queued
	// watchedConfigChange gateway.go's serve loop would later hand to
	// runReloadCycle as `first`.
	staleCfg := r5NewCfg()
	staleReadSeq := svc.reloadOutcome.markConfigRead()
	require.EqualValues(t, 1, staleReadSeq, "instrument: the watcher's snapshot is read #1")

	// An admin's PUT /performance writes config.json, then its OWN reload
	// attempt runs (a fresh read — #2 — and cycle #1) and fails, exactly
	// like rest_performance.go's real call:
	// mark(..., startedCount(), configReadsCount()).
	adminReadSeq := svc.reloadOutcome.markConfigRead()
	require.EqualValues(t, 2, adminReadSeq, "instrument: the admin's own reload reads config fresh (#2)")
	adminCycleSeq := svc.reloadOutcome.begin()
	require.EqualValues(t, 1, adminCycleSeq, "instrument: the admin's own reload is cycle #1")
	svc.reloadOutcome.finish(adminCycleSeq, errors.New("injected admin reload failure"), adminReadSeq)
	api.pendingApply.mark(gen.PerformanceReloadFailedDetailsStageReload,
		[]gen.PerformanceReloadFailedDetailsChangedFields{gen.PerformanceReloadFailedDetailsChangedFieldsMaxToolIterations},
		svc.reloadOutcome.startedCount(), svc.reloadOutcome.configReadsCount())
	require.NotNil(t, api.pendingApply.snapshot(), "instrument: the admin's failed reload left the save pending")

	// The file-watcher's queued snapshot (read BEFORE the mark) now reaches
	// the serve loop and runs — exec succeeds (the finding's own words:
	// "exec(S) succeeds"). Its cycle number (#2) IS greater than
	// markedAtReload (#1) — exactly the condition the pre-fix seq-only check
	// treated as sufficient to clear.
	execCalls := 0
	exec := func(*config.Config) error { execCalls++; return nil }
	loadNext := func() (*config.Config, error) {
		t.Fatal("loadNext must not be called: first is non-nil and this cycle has no coalesced follow-up")
		return nil, errUnreachableLoadNext
	}
	runReloadCycle(api.agentLoop, svc, staleCfg, staleReadSeq, exec, loadNext)

	require.Equal(t, 1, execCalls, "instrument: the stale-snapshot reload must have run exec")
	got := api.pendingApply.snapshot()
	if got == nil {
		t.Fatalf("expected pending_apply to remain set (the applied config predates the mark: "+
			"its readSeq=%d is not greater than markedAtConfigRead=%d), got nil — falsely cleared",
			staleReadSeq, adminReadSeq)
	}
	assert.Equal(t, gen.PerformancePendingApplyStageReload, got.Stage,
		"pending_apply must still report the admin's own unapplied reload-stage save")
	assert.Equal(t, []gen.PerformancePendingApplyChangedFields{gen.PerformancePendingApplyChangedFieldsMaxToolIterations},
		got.ChangedFields)
}

// TestPerformancePendingApply_PostMarkFreshRead_StillClears is the required
// companion: a reload cycle whose config was read from disk AFTER the mark
// (first=nil — the manual/PUT path, or an equivalent coalesced follow-up —
// runReloadCycle's own loadNext call stamps a fresh read-seq via
// markConfigRead) must still clear pending_apply, exactly as before this
// fix. Must stay green across the round-4 fix.
func TestPerformancePendingApply_PostMarkFreshRead_StillClears(t *testing.T) {
	api := newMTIAPI(t, "200")
	svc := &services{reloadOutcome: &reloadOutcomeTracker{}}
	svc.reloadOutcome.onSuccess = api.pendingApply.clearAfterReload

	// Mark first (nothing read yet — configReadsCount is 0).
	api.pendingApply.mark(gen.PerformanceReloadFailedDetailsStageReload,
		[]gen.PerformanceReloadFailedDetailsChangedFields{gen.PerformanceReloadFailedDetailsChangedFieldsGoalMaxRounds},
		svc.reloadOutcome.startedCount(), svc.reloadOutcome.configReadsCount())
	require.NotNil(t, api.pendingApply.snapshot(), "instrument: the mark left the save pending")

	// A later reload cycle with first=nil reads config fresh from disk
	// (necessarily AFTER the mark, since the mark already happened) via
	// loadNext, and succeeds.
	loadCalls := 0
	loadNext := func() (*config.Config, error) { loadCalls++; return r5NewCfg(), nil }
	exec := func(*config.Config) error { return nil }
	runReloadCycle(api.agentLoop, svc, nil, 0, exec, loadNext)

	require.Equal(t, 1, loadCalls, "instrument: the reload must have read config fresh from disk")
	assert.Nil(t, api.pendingApply.snapshot(), "a reload whose config was read after the mark must clear pending_apply")
}

// TestPerformancePendingApply_ReadSeqBoundary_OnlyStrictlyAfterClears exercises
// the readSeq/markedAtConfigRead boundary directly (equal vs. strictly
// greater), independent of the reloadsStarted dimension, which is held fixed
// here at a value that always qualifies (2 > 1).
func TestPerformancePendingApply_ReadSeqBoundary_OnlyStrictlyAfterClears(t *testing.T) {
	var p performancePendingApply
	p.mark(gen.PerformanceReloadFailedDetailsStageReload,
		[]gen.PerformanceReloadFailedDetailsChangedFields{gen.PerformanceReloadFailedDetailsChangedFieldsMaxParallelAgents},
		1, 5)

	p.clearAfterReload(2, 5) // seq qualifies (2>1); readSeq == markedAtConfigRead (boundary)
	require.NotNil(t, p.snapshot(), "readSeq equal to markedAtConfigRead must not clear (boundary)")
	p.clearAfterReload(2, 6) // seq qualifies; readSeq strictly greater
	require.Nil(t, p.snapshot(), "readSeq strictly greater than markedAtConfigRead clears")
}

// TestPerformancePendingApply_StaleCycleNumber_DoesNotClear_EvenWithFreshRead
// proves the AND is genuine: a reload cycle NUMBERED before (or at) the mark
// must not clear pending_apply, even when its readSeq is (implausibly, but
// the check must not rely on that) far ahead of markedAtConfigRead. Both
// checks must independently gate the clear.
func TestPerformancePendingApply_StaleCycleNumber_DoesNotClear_EvenWithFreshRead(t *testing.T) {
	var p performancePendingApply
	p.mark(gen.PerformanceReloadFailedDetailsStageReload,
		[]gen.PerformanceReloadFailedDetailsChangedFields{gen.PerformanceReloadFailedDetailsChangedFieldsMaxParallelAgents},
		5, 1)
	p.clearAfterReload(5, 99) // seq == markedAtReload (not >); readSeq very fresh
	require.NotNil(t, p.snapshot(), "seq not strictly after the mark must not clear, however fresh readSeq is")
}
