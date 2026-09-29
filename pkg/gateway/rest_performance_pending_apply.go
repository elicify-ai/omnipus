// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_performance_pending_apply.go — the server-side "saved but not applied
// yet" state of the Performance settings (issue #904, gate round 3;
// contracts/components/schemas/PerformancePendingApply.yaml) and the
// registry-reload outcome record it is cleared from.
//
// A PUT /performance that answers performance_reload_failed has committed
// its writes but they are not in force. Before this state existed that fact
// lived only in that one 500 response: a client that lost it (page reload,
// second tab, another admin) saw plain values on GET and no hint that they
// were not applied. The state is cleared only by a refresh AND a registry
// reload that both succeeded after it was marked.

import (
	"errors"
	"sync"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// performancePendingApplyFieldOrder is the contract enum order of
// PerformancePendingApply.changed_fields.
var performancePendingApplyFieldOrder = []gen.PerformancePendingApplyChangedFields{
	gen.PerformancePendingApplyChangedFieldsMaxParallelAgents,
	gen.PerformancePendingApplyChangedFieldsToolsOnDemand,
	gen.PerformancePendingApplyChangedFieldsGoalMaxRounds,
	gen.PerformancePendingApplyChangedFieldsMaxToolIterations,
}

// performancePendingApply holds the pending-apply state. The zero value is
// ready (nothing pending). All methods are safe for concurrent use.
type performancePendingApply struct {
	mu sync.Mutex
	// epoch is bumped on every mark. A PUT whose in-memory refresh succeeded
	// records the epoch it covered (epochNow, under configMu, after the
	// refresh); its later successful registry reload clears only when no
	// mark happened since (clearIfEpoch).
	epoch uint64
	// markedAtReload is reloadOutcomeTracker.startedCount() at the latest
	// mark: only a reload that STARTED after it (sequence number greater
	// than this) read a config that includes the marked save.
	markedAtReload uint64
	// markedAtConfigRead is reloadOutcomeTracker.configReadsCount() at the
	// latest mark (round-4 finding, gate round 4): a reload cycle STARTING
	// after the mark (markedAtReload) does not by itself prove the config it
	// APPLIED was read after the mark — the file-watcher poller
	// (gateway_reload.go::setupConfigWatcherPolling) can hand runReloadCycle
	// a config snapshot it loaded from disk well before this mark, queued on
	// configReloadChan while an unrelated reload ran in between and bumped
	// the reload-sequence counter past markedAtReload. clearAfterReload
	// requires the reload's OWN config-read sequence number (stamped by
	// reloadOutcomeTracker.markConfigRead, either by the poller at load time
	// or by runReloadCycle's own loadNext calls) to be greater than this
	// value too, so a stale watcher snapshot that merely rode a
	// later-numbered reload cycle can never falsely clear the notice.
	markedAtConfigRead uint64
	stage              gen.PerformancePendingApplyStage
	fields             map[gen.PerformancePendingApplyChangedFields]bool
}

// mark records a failed apply: the stage of this failure (the latest wins)
// and the union of its changed fields with those already pending.
// configReadsAtMark is reloadOutcomeTracker.configReadsCount() taken at the
// same moment as reloadsStarted (round-4 finding: see markedAtConfigRead's
// doc comment).
func (p *performancePendingApply) mark(stage gen.PerformanceReloadFailedDetailsStage,
	changed []gen.PerformanceReloadFailedDetailsChangedFields, reloadsStarted uint64, configReadsAtMark uint64,
) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.epoch++
	p.markedAtReload = reloadsStarted
	p.markedAtConfigRead = configReadsAtMark
	p.stage = gen.PerformancePendingApplyStage(stage)
	if p.fields == nil {
		p.fields = map[gen.PerformancePendingApplyChangedFields]bool{}
	}
	for _, f := range changed {
		p.fields[gen.PerformancePendingApplyChangedFields(f)] = true
	}
}

// isSet reports whether saved settings are pending apply.
func (p *performancePendingApply) isSet() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stage != ""
}

// epochNow returns the current mark epoch (see the epoch field).
func (p *performancePendingApply) epochNow() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.epoch
}

// clearIfEpoch clears the state when no mark happened since epoch was read.
func (p *performancePendingApply) clearIfEpoch(epoch uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.epoch == epoch {
		p.clearLocked()
	}
}

// clearAfterReload clears the state when the successful reload with
// sequence number seq started after the latest mark AND the config it
// actually applied was itself read from disk after the latest mark
// (readSeq, reloadOutcomeTracker.markConfigRead's return value for that
// reload — see markedAtConfigRead's doc comment for why seq alone is not
// enough: round-4 finding). Installed as the reload outcome tracker's
// success hook, so a manual or automatic reload clears it as well as a
// PUT's own.
func (p *performancePendingApply) clearAfterReload(seq uint64, readSeq uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stage != "" && seq > p.markedAtReload && readSeq > p.markedAtConfigRead {
		p.clearLocked()
	}
}

func (p *performancePendingApply) clearLocked() {
	p.stage = ""
	p.fields = nil
}

// snapshot returns the wire value, or nil when nothing is pending.
func (p *performancePendingApply) snapshot() *gen.PerformancePendingApply {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stage == "" {
		return nil
	}
	fields := make([]gen.PerformancePendingApplyChangedFields, 0, len(p.fields))
	for _, f := range performancePendingApplyFieldOrder {
		if p.fields[f] {
			fields = append(fields, f)
		}
	}
	return &gen.PerformancePendingApply{Stage: p.stage, ChangedFields: fields}
}

// reloadOutcomeTracker records the outcome of every registry reload the
// gateway's reload cycle runs (runReloadCycle around each executeReload).
// It exists because the reload-pending flag that triggerReloadAndWait polls
// is cleared when a reload FINISHES, whether its rebuild succeeded or not —
// so "confirmed" never meant "applied". finish is called before the cycle
// clears that flag, so a poller released by the flag reads the outcome of
// the reload that served it (or of a later one, whose config also includes
// its write).
//
// A nil tracker is valid (tests that build services or restAPI without the
// boot path): it records nothing and reports no outcome.
type reloadOutcomeTracker struct {
	mu      sync.Mutex
	started uint64
	lastSeq uint64
	lastErr error
	// configReads is bumped by markConfigRead every time a config is freshly
	// read from disk for reload purposes: the file-watcher poller
	// (setupConfigWatcherPolling, at the point it accepts an external edit)
	// and runReloadCycle's own loadNext calls. Separate from `started`
	// (round-4 finding): `started` counts reload CYCLES, which can be seeded
	// by a config snapshot read long before the cycle itself ran (the
	// watcher's queued `first`) — configReads instead timestamps the config
	// DATA itself, so performancePendingApply.clearAfterReload can tell a
	// stale snapshot apart from a fresh one regardless of which cycle number
	// carried it.
	configReads uint64
	// onSuccess runs (outside mu) after a reload with sequence number seq,
	// whose applied config was read at configReadSeq (markConfigRead's
	// return value for that config), succeeded. Set once at boot, before any
	// reload can run.
	onSuccess func(seq uint64, configReadSeq uint64)
}

// begin records that a reload starts and returns its sequence number.
func (t *reloadOutcomeTracker) begin() uint64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.started++
	return t.started
}

// finish records the outcome of the reload with sequence number seq, whose
// applied config was read at configReadSeq (markConfigRead's return value
// for that config — 0 when the caller never stamped one, e.g. tests that
// exercise the coalescing contract without the pending-apply wiring).
func (t *reloadOutcomeTracker) finish(seq uint64, err error, configReadSeq uint64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if seq >= t.lastSeq {
		t.lastSeq = seq
		t.lastErr = err
	}
	hook := t.onSuccess
	t.mu.Unlock()
	if err == nil && hook != nil {
		hook(seq, configReadSeq)
	}
}

// startedCount is the number of reloads started so far.
func (t *reloadOutcomeTracker) startedCount() uint64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.started
}

// markConfigRead records a fresh config.json read for reload purposes and
// returns its sequence number (round-4 finding — see the configReads field's
// doc comment). Called by the file-watcher poller (setupConfigWatcherPolling)
// once per accepted external edit, by runReloadCycle once per loadNext read,
// and by gateway_reload.go::reloadConfigForSwap once per successful
// swap-time re-read (round-6 finding: the config THAT call stamps is the one
// handleConfigReload actually applies, which the reload cycle's own outer
// read can predate); nil-safe like every other tracker method (a nil tracker
// records nothing and every stamped read is 0, which never compares greater
// than any mark).
func (t *reloadOutcomeTracker) markConfigRead() uint64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.configReads++
	return t.configReads
}

// configReadsCount is the number of fresh config reads stamped so far.
func (t *reloadOutcomeTracker) configReadsCount() uint64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.configReads
}

// lastFailed reports whether the most recently started reload that has
// finished failed its rebuild.
func (t *reloadOutcomeTracker) lastFailed() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastErr != nil
}

// errRegistryRebuildFailed is the fixed error reloadAgentsAndConfirm returns
// when the reload ran but its rebuild failed. The rebuild's own error is
// logged by runReloadCycle ("Config reload failed") and can carry
// credential-derived text, so it is never carried further.
var errRegistryRebuildFailed = errors.New("the agent registry reload ran but its rebuild failed")

// reloadAgentsAndConfirm triggers a registry reload, waits for it and
// confirms it APPLIED: on top of triggerReloadAndWait's own failures (the
// reload could not start, or did not finish in time) it fails when the
// reload that served the request finished with a failed rebuild — which
// triggerReloadAndWait alone reports as success, because the pending flag it
// polls is cleared on a failed rebuild too. Used by PUT /performance (#904);
// the other triggerReloadAndWait callers keep its original behaviour.
func (a *restAPI) reloadAgentsAndConfirm() error {
	if err := a.triggerReloadAndWait(); err != nil {
		return err
	}
	if a.reloadOutcome.lastFailed() {
		return errRegistryRebuildFailed
	}
	return nil
}
