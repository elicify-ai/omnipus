// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/daemon"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// omnipusShutdownTimeout is the maximum time to wait for in-flight operations
// to complete before force-flushing partial state. Per FUNC-36 / US-9.
//
// This must be larger than the maximum active-turn wait (60 s) plus a margin (5 s).
// Previously 10 s, which was impossible to satisfy when active turns run up to 60 s.
const omnipusShutdownTimeout = 70 * time.Second

// omnipusGracefulShutdown executes the 5-step graceful shutdown per FUNC-36:
//
//  1. Stop channel manager — no new inbound messages accepted
//  2. Drain agent loop — wait for active turns (US-7, FR-008), then close
//  3. Flush partial state — interrupted responses already saved by agent loop cancellation
//  4. Stop background services — heartbeat, cron, device, health
//  5. Close provider connections — release HTTP/WebSocket sessions
//
// Implements US-9 and US-7 acceptance criteria.
func omnipusGracefulShutdown(
	runningServices *services,
	agentLoop *agent.AgentLoop,
	provider providers.LLMProvider,
	// The config is no longer read here (the retired per-turn timeout used
	// to shorten the active-turn wait); kept so callers stay unchanged.
	_ *config.Config,
) {
	runGracefulShutdown(runningServices, agentLoop, provider, defaultShutdownBudget, backgroundStopsOf(runningServices))
}

// runGracefulShutdown is the shutdown sequence under budget b. Every wait in
// steps 1 and 2 is bounded by what is left of b (6a delta review I2), so the
// sequence reaches the agent-loop close with b.margin still available.
func runGracefulShutdown(
	runningServices *services,
	agentLoop shutdownLoop,
	provider providers.LLMProvider,
	b shutdownBudget,
	stops shutdownBackgroundStops,
) {
	ctx, cancel := context.WithTimeout(context.Background(), b.total)
	defer cancel()
	// The background stops below may use the budget up to the margin kept
	// for the agent-loop close, never past it.
	stopsCtx, stopsCancel := context.WithTimeout(ctx, b.total-b.margin)
	defer stopsCancel()

	slog.Info("shutdown: step 1 — stopping new requests")
	// Stop channel manager first so no new inbound messages are accepted.
	// Use context.Background() with its own 2 s budget so that a tight overall
	// ctx deadline (SI2) does not steal from the channel-manager stop budget.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	if runningServices.ChannelManager != nil {
		runningServices.ChannelManager.StopAll(stopCtx)
	}

	// #265: stop the turn-triggering background services (cron, task
	// triggers, /loop, the plan engine) and the background cache writers
	// BEFORE draining active turns — see stopBackgroundWriters.
	stopBackgroundWriters(stopsCtx, stops)

	// US-7 / FR-008: Wait for active turns to complete before force-closing.
	// The wait is what is left of the budget minus the margin, at most
	// b.total - b.margin (65 s in production), so it always fits after the
	// stops above. This is a shutdown drain bound, not a work time limit: the
	// gateway is going away. (The retired agents.defaults.timeout_seconds used
	// to shorten it; turns have no time limit any more, issue #1306.)
	remaining := b.total
	if deadline, ok := ctx.Deadline(); ok {
		remaining = time.Until(deadline)
	}
	activeTurnWait := b.activeTurnWait(remaining)

	slog.Info("shutdown: step 2 — waiting for active turns to complete",
		"active_turn_wait_seconds", int(activeTurnWait.Seconds()))

	// Stop() prevents new turns from starting.
	agentLoop.Stop()

	activeTurnCtx, activeTurnCancel := context.WithTimeout(ctx, activeTurnWait)
	defer activeTurnCancel()
	if agentLoop.WaitForActiveRequestsContext(activeTurnCtx) {
		slog.Info("shutdown: all active turns completed before shutdown")
	} else {
		slog.Warn("shutdown: timeout waiting for active turns — force-canceling",
			"timeout_seconds", int(activeTurnWait.Seconds()))
	}

	// Now close the agent loop resources.
	// SH5: After the active-turn wait (or timeout), Close() is called. If any turns
	// are still in-flight at this point, their context will have been canceled by the
	// overall ctx timeout propagation. Session stores use atomic writes so partial-write
	// corruption is not possible. This is documented behavior per FUNC-36.
	done := make(chan struct{})
	go func() {
		defer close(done)
		agentLoop.Close()
	}()

	select {
	case <-done:
		slog.Info("shutdown: agent loop drained cleanly")
	case <-ctx.Done():
		slog.Warn("shutdown: timeout waiting for agent loop drain — saving partial state")
	}
	if mediaStore, ok := runningServices.MediaStore.(*media.FileMediaStore); ok {
		mediaStore.Stop()
	}

	slog.Info("shutdown: step 3 — verifying partial state persistence")
	// The session PartitionStore uses O_APPEND writes that are already durable.
	// Any in-progress streaming response was saved with status "interrupted" by
	// the agent loop's context cancellation path (handled in pkg/agent).

	slog.Info("shutdown: step 4 — stopping background services")
	if runningServices.browserWS != nil {
		runningServices.browserWS.closeMediaTransport()
	}
	// Heartbeat + cron were already stopped in step 1 (they trigger turns).
	// Stop the remaining services that have no turn side effects.
	if runningServices.HealthServer != nil {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel2()
		if err := runningServices.HealthServer.Stop(ctx2); err != nil {
			slog.Warn("shutdown: health server stop error", "error", err)
		}
	}

	slog.Info("shutdown: step 5 — closing provider connections")
	if cp, ok := provider.(providers.StatefulProvider); ok {
		cp.Close()
	}

	// Step 5b: overwrite the master key the credential store has held since
	// boot. It sits here because this is the last point that can still resolve
	// a credential ref — the in-flight turns drained in step 2 and the provider
	// teardown just above both read through the store. The remaining steps
	// (preview registries, nag banner, PID file) do not. Best-effort by
	// construction: store.Close's doc comment states what it can and cannot
	// erase.
	if runningServices.credStore != nil {
		runningServices.credStore.Close()
		slog.Debug("shutdown: credential store key material wiped")
	}

	// Step 6: Stop Tier 1/3 preview-tool registries so their janitor goroutines exit.
	// servedSubdirs.Stop() is idempotent and safe to call even when Stop was never
	// explicitly called (the janitor goroutine exits when the stop channel closes).
	// devServers.Close() SIGTERMs any remaining dev-server children, preventing zombie
	// processes from outliving the gateway.
	slog.Info("shutdown: step 6 — stopping Tier 1/3 preview registries")
	if runningServices.servedSubdirs != nil {
		runningServices.servedSubdirs.Stop()
	}
	if runningServices.devServers != nil {
		runningServices.devServers.Close()
	}
	if runningServices.egressProxy != nil {
		if epErr := runningServices.egressProxy.Close(); epErr != nil {
			slog.Warn("shutdown: egress proxy close error", "error", epErr)
		}
	}

	// Stop the permissive / production-off nag-banner goroutine if one was
	// armed at boot. Safe to call on nil or never-started state.
	if runningServices.stopNagBanner != nil {
		runningServices.stopNagBanner()
	}

	// Sweep-5: clear the sandbox → audit hooks so in-process test scaffolding
	// that boots and tears down multiple gateways does not leak a closure
	// (which captures agentLoop) from one boot into the next. Both setters are
	// thread-safe and idempotent.
	unwireSandboxAuditHooks()

	// Remove the self-registered PID file so `omnipus status` correctly reports
	// not-running after a clean shutdown. Best-effort: a missing homePath (e.g.
	// in-process gateway test harnesses) is a safe no-op (RemovePID tolerates
	// ENOENT). Symmetric with the WritePID call in setupAndStartServices.
	if runningServices.homePath != "" {
		daemon.RemovePID(runningServices.homePath)
		slog.Debug("shutdown: removed PID file", "home", runningServices.homePath)
	}

	slog.Info("shutdown: complete")
}

// shutdownActiveTurnMargin is kept free after the active-turn wait for the
// agent-loop close.
const shutdownActiveTurnMargin = 5 * time.Second

// shutdownBudget is the time the graceful shutdown may take (total) and the
// part of it kept for the agent-loop close (margin).
type shutdownBudget struct {
	total, margin time.Duration
}

var defaultShutdownBudget = shutdownBudget{total: omnipusShutdownTimeout, margin: shutdownActiveTurnMargin}

// activeTurnWait is the active-turn wait for the budget left: remaining minus
// the margin, never above total minus the margin, never below zero.
func (b shutdownBudget) activeTurnWait(remaining time.Duration) time.Duration {
	return max(min(remaining, b.total)-b.margin, 0)
}

// shutdownActiveTurnWait is the production budget's active-turn wait:
// remaining minus 5 s, never above 65 s, never below zero.
func shutdownActiveTurnWait(remaining time.Duration) time.Duration {
	return defaultShutdownBudget.activeTurnWait(remaining)
}

// shutdownLoop is the part of the agent loop the shutdown sequence drives.
type shutdownLoop interface {
	Stop()
	WaitForActiveRequestsContext(ctx context.Context) bool
	Close()
}

// shutdownBackgroundStops are the stops of the background services that
// start turns or write files, run before the active-turn drain. Nil entries
// are skipped.
type shutdownBackgroundStops struct {
	schedulers    []func() // cron, task triggers, /loop
	plan          func()
	liveLimits    func()
	catalogCancel func()
	catalogDone   <-chan struct{}
}

// backgroundStopsOf collects the running services' background stops.
func backgroundStopsOf(rs *services) shutdownBackgroundStops {
	var s shutdownBackgroundStops
	if rs.CronService != nil {
		s.schedulers = append(s.schedulers, rs.CronService.Stop)
	}
	if rs.TaskTrigger != nil {
		s.schedulers = append(s.schedulers, rs.TaskTrigger.Stop)
	}
	if rs.LoopScheduler != nil {
		s.schedulers = append(s.schedulers, rs.LoopScheduler.Stop)
	}
	if rs.PlanEngine != nil {
		s.plan = rs.PlanEngine.Stop
	}
	if rs.LiveLimits != nil {
		s.liveLimits = rs.LiveLimits.Close
	}
	s.catalogCancel = rs.catalogRefreshCancel
	s.catalogDone = rs.catalogRefreshDone
	return s
}

// stopBackgroundWriters stops, in order, the turn schedulers, the plan
// engine, the live model-limits fetcher and the catalog refresh loop. Every
// wait ends at ctx (the shutdown budget minus the agent-loop margin) or, for
// the catalog, at catalogRefreshStopTimeout, whichever comes first.
//
// None of these Stop/Close calls takes a context, so each runs on its own
// goroutine and the wait for it is bounded by ctx. Each call is still
// launched, in order, even when the budget is already spent: its first act
// closes admission (the cron engines' scheduling; the plan engine's write
// gate, which also fences a Tick pass already running). (Once the budget is spent the calls are not awaited, so
// their goroutines may then start in any order.) A call still running at the deadline is not abandoned
// half-way; its goroutine finishes the callee's own bounded teardown (cron
// and plan drains are 30 s, LiveLimits aborts its fetch before joining) and
// then exits. The waiter only closes channels, so no goroutine of ours stays
// blocked after its callee returns.
func stopBackgroundWriters(ctx context.Context, s shutdownBackgroundStops) {
	// #265: stop the turn-triggering background services (cron) BEFORE
	// draining active turns. If they fire during the drain, the resulting turn's
	// cost.json / session-context writes can outlive RunContext and race
	// t.TempDir cleanup on macOS APFS. The channel manager (stopped first) is
	// the other inbound source; together they guarantee no new bus message —
	// hence no new active-request admission — after this point.
	// Device/health/preview registries have no turn side effects and stop
	// later (step 4).
	//
	// #1490: the task-trigger and /loop schedulers are two more copies of the
	// cron engine, and both start work when a job comes due (a triggered task
	// run, a scheduled /loop turn). They stop here too, for the same reason.
	// Each Stop is idempotent, so a later cleanup pass that stops them again
	// is harmless. Each cron-engine Stop closes admission at once, then may
	// wait up to its 30 s lane drain (pkg/cron defaultStopDrainTimeout). One
	// after another, three engines could take 90 s against the 70 s budget,
	// so the three run side by side (6a review W3).
	if !stopTurnSchedulers(ctx, s.schedulers...) {
		slog.Warn("shutdown: scheduler stop did not finish within the shutdown budget; continuing")
	}
	// ADR-049 D4: the plan engine dispatches member tasks (which spawn
	// turns), so it stops alongside CronService — before draining active
	// turns — for the exact same "no new active-request admission after this
	// point" reason the comment above documents for cron. Its Stop first
	// closes the engine's write gate (no plan/task write, member dispatch or
	// plan turn after that, even from a Tick pass already running), waits
	// for the writes already admitted, joins its loops and then drains
	// judges and wakes for up to 30 s. If this wait gives up at the budget,
	// a still-running pass can therefore no longer write (6a re-check I2).
	if s.plan != nil && !stopTurnSchedulers(ctx, s.plan) {
		slog.Warn("shutdown: plan engine stop did not finish within the shutdown budget; continuing")
	}
	// ADR-066 rung 4 writes $OMNIPUS_HOME/cache/model_limits.json at the tail
	// of a background fetch that nothing else waits for. Close it here, with
	// the other background writers, so the write cannot land after RunContext
	// returns (2026-09-12: 130 post-shutdown writes in one CI run, one of
	// them mid-TempDir-RemoveAll). Close aborts the fetch and returns once its
	// goroutine has exited.
	if s.liveLimits != nil && !stopTurnSchedulers(ctx, s.liveLimits) {
		slog.Warn("shutdown: live model-limits close did not finish within the shutdown budget; continuing")
	}
	// The provider-catalog refresh loop is the other post-boot background
	// writer (providers_catalog.json, 2.4 MB, pulled from a GitHub release).
	// Cancel it and wait for the goroutine to exit — bounded, because a wedged
	// transport must not hold shutdown hostage.
	if s.catalogCancel != nil {
		s.catalogCancel()
	}
	if s.catalogDone != nil {
		timer := time.NewTimer(catalogRefreshStopTimeout)
		defer timer.Stop()
		select {
		case <-s.catalogDone:
		case <-timer.C:
			slog.Warn("shutdown: catalog refresh loop did not exit in time; continuing",
				"timeout", catalogRefreshStopTimeout)
		case <-ctx.Done():
			slog.Warn("shutdown: catalog refresh loop did not exit within the shutdown budget; continuing")
		}
	}
}

// stopTurnSchedulers runs the given Stop functions side by side (nil entries
// are skipped) and waits until all return or ctx ends. It reports whether all
// finished. A Stop still running at the deadline keeps tearing down in the
// background; its goroutine exits when the Stop returns.
func stopTurnSchedulers(ctx context.Context, stops ...func()) bool {
	var wg sync.WaitGroup
	for _, stop := range stops {
		if stop == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			stop()
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}
