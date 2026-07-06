//go:build !cgo

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
	"github.com/elicify-ai/omnipus/pkg/session"
)

// retentionSweepMu serializes nightly-sweep ticks against the on-demand
// POST /api/v1/security/retention/sweep endpoint. Both paths acquire
// this mutex before calling the sweep function so they never run concurrently.
//

var retentionSweepMu sync.Mutex

// retentionSweepFn is the function called on each enabled tick. The default
// delegates directly to (*session.UnifiedStore).RetentionSweep. Tests replace
// this variable with a mock to observe call counts without touching the
// filesystem.
//

var retentionSweepFn func(store *session.UnifiedStore, days int) (int, error) = func(
	store *session.UnifiedStore, days int,
) (int, error) {
	return store.RetentionSweep(days)
}

// retentionLoopStarted ensures the goroutine is launched at most once per
// gateway process (sync.Once is reset only at process exit, which is correct
// for a singleton worker).
//

var retentionLoopStarted sync.Once

// startRetentionSweepLoop launches the nightly retention sweep goroutine.
//
// The goroutine is guarded by retentionLoopStarted so it is launched exactly
// once per process even if the caller is invoked more than once (e.g. during
// integration tests that call the gateway multiple times).
//
// Parameters:
//   - ctx: canceled by gateway shutdown; the goroutine exits within the next
//     ticker interval (at most tickInterval) after cancellation.
//   - store: the shared UnifiedStore whose sessions are swept.
//   - getCfg: thunk that returns the current config on each call; the goroutine
//     re-evaluates it on every tick so hot-reload changes to retention config
//     are picked up without a restart.
//   - tickInterval: normally 24*time.Hour; pass a smaller value in tests.
//

func startRetentionSweepLoop(
	ctx context.Context,
	store *session.UnifiedStore,
	getCfg func() *config.Config,
	tickInterval time.Duration,
) {
	retentionLoopStarted.Do(func() {
		go runRetentionSweepLoop(ctx, store, getCfg, tickInterval)
	})
}

func runRetentionSweepLoop(
	ctx context.Context,
	store *session.UnifiedStore,
	getCfg func() *config.Config,
	tickInterval time.Duration,
) {
	// Boot-time sweep — drop sessions already past retention before the first
	// tick so an operator restarting after a long downtime does not have to
	// wait up to tickInterval for stale data to clear. The ticker timeline is
	// otherwise identical to before.
	executeSweepTick(store, getCfg)

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			executeSweepTick(store, getCfg)
		}
	}
}

var retentionToolResultSweepFn func(days int) (int, error)

func executeSweepTick(store *session.UnifiedStore, getCfg func() *config.Config) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("retention_sweep: tick panic recovered",
				"event", "retention_sweep_panic",
				"panic", r,
			)
		}
	}()

	cfg := getCfg()
	if cfg == nil {
		slog.Warn("retention_sweep: getCfg returned nil, skipping tick")
		return
	}

	ret := cfg.Storage.Retention
	if ret.IsDisabled() {
		slog.Info("retention_sweep: skipping tick",
			"event", "retention_sweep_skipped",
			"reason", "disabled",
		)
		return
	}

	days := ret.RetentionSessionDays()

	retentionSweepMu.Lock()
	defer retentionSweepMu.Unlock()

	start := time.Now()
	removed, err := retentionSweepFn(store, days)
	durationMs := time.Since(start).Milliseconds()

	if err != nil {
		slog.Error("retention_sweep: sweep failed",
			"event", "retention_sweep_failed",
			"error", err,
			"duration_ms", durationMs,
		)
		return
	}

	// Sweep tool-result files (offloaded > 50 KiB bodies) on the same schedule
	// and retention window. When the gateway didn't wire the hook (tests with
	// disabled toolStore), this is a no-op.
	toolResultRemoved := 0
	if retentionToolResultSweepFn != nil {
		if n, terr := retentionToolResultSweepFn(days); terr != nil {
			slog.Warn("retention_sweep: tool_results sweep failed",
				"event", "retention_sweep_tool_results_failed",
				"error", terr,
			)
		} else {
			toolResultRemoved = n
		}
	}

	slog.Info("retention_sweep: completed",
		"event", "retention_sweep",
		"removed", removed,
		"tool_result_removed", toolResultRemoved,
		"duration_ms", durationMs,
	)
}

// retentionRetroSweepFn is the function called to sweep retro files per agent.
// Tests replace this variable with a mock.
//

var retentionRetroSweepFn func(al *agent.AgentLoop, retentionDays int) int = func(
	al *agent.AgentLoop, retentionDays int,
) int {
	return executeRetroSweep(al, retentionDays)
}

// retentionRetroLoopStarted ensures the retro sweep goroutine is launched at most once.
//

var retentionRetroLoopStarted sync.Once

// startRetentionRetroSweepLoop launches the nightly retro sweep goroutine (FR-031).
// The goroutine iterates all agents in the registry and calls SweepRetros on
// each agent's MemoryStore. It is guarded by retentionRetroLoopStarted so it
// runs exactly once per process.
//

func startRetentionRetroSweepLoop(
	ctx context.Context,
	agentLoop *agent.AgentLoop,
	getCfg func() *config.Config,
	tickInterval time.Duration,
) {
	if agentLoop == nil {
		return
	}
	retentionRetroLoopStarted.Do(func() {
		go runRetentionRetroSweepLoop(ctx, agentLoop, getCfg, tickInterval)
	})
}

func runRetentionRetroSweepLoop(
	ctx context.Context,
	agentLoop *agent.AgentLoop,
	getCfg func() *config.Config,
	tickInterval time.Duration,
) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			executeRetroSweepTick(agentLoop, getCfg)
		}
	}
}

func executeRetroSweepTick(agentLoop *agent.AgentLoop, getCfg func() *config.Config) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("retention_retro_sweep: tick panic recovered",
				"event", "retention_retro_sweep_panic",
				"panic", r,
			)
		}
	}()

	cfg := getCfg()
	if cfg == nil {
		return
	}

	ret := cfg.Storage.Retention
	if ret.IsDisabled() {
		return
	}

	retentionDays := ret.RetentionMemoryRetrosDays()

	retentionSweepMu.Lock()
	defer retentionSweepMu.Unlock()

	start := time.Now()
	deleted := retentionRetroSweepFn(agentLoop, retentionDays)
	durationMs := time.Since(start).Milliseconds()

	slog.Info("retention_retro_sweep: completed",
		"event", "retention_retro_sweep",
		"deleted_files", deleted,
		"duration_ms", durationMs,
		"retention_days", retentionDays,
	)
}

// executeRetroSweep iterates all agents and calls SweepRetros on each agent's MemoryStore.
// Returns the total count of deleted retro files.
//

func executeRetroSweep(agentLoop *agent.AgentLoop, retentionDays int) int {
	registry := agentLoop.GetRegistry()
	if registry == nil {
		slog.Debug("retention_retro_sweep: no registry available, skipping",
			"event", "retention_retro_sweep_noop",
		)
		return 0
	}

	agentIDs := registry.ListAgentIDs()
	if len(agentIDs) == 0 {
		slog.Debug("retention_retro_sweep: no agents registered, nothing to sweep",
			"event", "retention_retro_sweep_noop",
		)
		return 0
	}

	totalDeleted := 0
	for _, agentID := range agentIDs {
		agentInst, ok := registry.GetAgent(agentID)
		if !ok || agentInst == nil || agentInst.ContextBuilder == nil {
			continue
		}
		memory := agentInst.ContextBuilder.Memory()
		if memory == nil {
			continue
		}
		deleted, err := memory.SweepRetros(retentionDays)
		if err != nil {
			slog.Warn("retention_retro_sweep: sweep failed for agent",
				"agent_id", agentID,
				"error", err,
			)
			continue
		}
		if deleted > 0 {
			slog.Info("retention_retro_sweep: swept retros for agent",
				"agent_id", agentID,
				"deleted", deleted,
				"retention_days", retentionDays,
			)
		}
		totalDeleted += deleted
	}
	return totalDeleted
}
