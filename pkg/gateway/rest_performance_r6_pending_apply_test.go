// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_performance_r6_pending_apply_test.go — #904 gate round 6,
// security-lead round-5 note: the provenance counter
// reloadOutcomeTracker.configReads is stamped via markConfigRead() by the
// file-watcher poller (gateway_reload.go::setupConfigWatcherPolling) and by
// runReloadCycle's own loadNext calls — but NOT by the swap-time re-read
// (handleConfigReload -> reloadConfigForSwap -> services.loadConfigForSwap,
// wired in gateway.go's serveReloadLoop to newReloadConfigLoader), which is
// the config that is ACTUALLY applied (handleConfigReload assigns
// `newCfg = swapCfg` immediately after reloadConfigForSwap returns, and
// every later step — createStartupProvider, ReloadProviderAndConfig,
// restartServices — acts on that swapCfg, never on the pre-swap snapshot).
//
// Consequence: a reload cycle that started from a STALE watcher snapshot
// (read before an admin's PUT /performance mark) but whose swap-time
// re-read picks up the marked write — because that write already landed on
// disk by the time the swap runs — applies the new value, yet
// runReloadCycle still reports the STALE outer readSeq to
// reloadOutcomeTracker.finish/onSuccess -> clearAfterReload. clearAfterReload
// then withholds the clear even though the change it warns about is already
// in force: too conservative, not a false clear (it self-heals on some later
// reload), but wrong on THIS reload.
//
// Oracle: rest_performance_pending_apply.go's own doc comment — "The state
// is cleared only by a refresh AND a registry reload that both succeeded
// after it was marked" — the config a reload cycle actually APPLIES is
// whatever handleConfigReload swapped in, not whatever `first`/loadNext
// handed runReloadCycle before the swap. clearAfterReload's readSeq
// parameter must describe that applied config's own read, not a stale
// outer one.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// r6DiskConfigJSON is the on-disk config.json content for this test, with
// maxTokens as this version's distinguishing marker
// (config.AgentDefaults.MaxTokens — a plain int that round-trips unchanged
// through config.LoadConfigWithStoreAndSelfHealHook) so the test can prove
// WHICH on-disk version the swap-time re-read actually picked up, not merely
// its read-sequence bookkeeping.
func r6DiskConfigJSON(homePath string, maxTokens int) string {
	return fmt.Sprintf(`{
  "version": 1,
  "agents": {"defaults": {"home": %q, "max_tokens": %d}},
  "providers": []
}`, homePath, maxTokens)
}

// TestHandleConfigReload_SwapTimeReReadAfterMark_ClearsPendingApply drives
// the REAL production swap-time re-read path — executeReload ->
// handleConfigReload -> reloadConfigForSwap -> services.loadConfigForSwap
// (wired here exactly as gateway.go's serveReloadLoop wires it, to
// newReloadConfigLoader) — through the real runReloadCycle, with a stale
// `first` config snapshot (read before the mark) and the admin's marked
// write already committed to disk by the time the swap-time re-read runs.
//
// Timeline (mirrors rest_performance_r5_pending_apply_test.go's
// staleReadSeq/adminReadSeq/adminCycleSeq shape, extended with the real
// executeReload/handleConfigReload machinery instead of a synthetic exec
// stub):
//  1. config.json holds V1 (max_tokens=4096). The file-watcher poller reads
//     it — config-read #1 — well before the admin's write.
//  2. The admin's PUT /performance commits V2 (max_tokens=8192) to disk —
//     "the marked write" — then its OWN reload attempt reads config fresh
//     (#2, V2) and fails (cycle #1). The failure marks pending_apply with
//     markedAtReload=1, markedAtConfigRead=2.
//  3. The stale watcher snapshot (V1, read #1 — BEFORE the mark) now reaches
//     the serve loop, exactly as a queued watchedConfigChange. Its cycle
//     number (#2) is greater than markedAtReload (#1). Its OWN readSeq (#1)
//     is NOT greater than markedAtConfigRead (#2) — so a check keyed only on
//     the outer readSeq must NOT clear. But the real swap-time re-read
//     inside handleConfigReload reads config.json fresh AGAIN (config-read
//     #3, V2) and that V2 config is what is actually applied
//     (asserted via al.GetConfig() below) — a read that happened strictly
//     after the mark.
func TestHandleConfigReload_SwapTimeReReadAfterMark_ClearsPendingApply(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	const maxTokensV1 = 4096
	const maxTokensV2 = 8192
	require.NoError(t, os.WriteFile(configPath, []byte(r6DiskConfigJSON(tmpDir, maxTokensV1)), 0o600))

	credStore := newUnlockedStore(t, tmpDir)
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 0},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: tmpDir, MaxTokens: maxTokensV1},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	builtinReg := tools.NewBuiltinRegistry()
	mcpReg := tools.NewMCPRegistry()
	provider := providers.LLMProvider(&restMockProvider{})

	rs, err := setupAndStartServices(
		context.Background(),
		cfg,
		credentials.SecretBundle{},
		al,
		msgBus,
		tmpDir,
		credStore,
		&SandboxApplyResult{},
		builtinReg,
		mcpReg,
		false, // allowGodMode
	)
	require.NoError(t, err, "setupAndStartServices must boot cleanly")
	t.Cleanup(func() { stopAndCleanupServices(rs, 5*time.Second, false) })
	require.NotNil(t, rs.restAPIRef, "instrument: boot must wire restAPIRef so configMu/pendingApply are reachable")

	// Wire the swap-time re-read loader exactly as production's
	// serveReloadLoop does (gateway.go:
	// rc.runningServices.loadConfigForSwap = loadReloadConfig), instrumented
	// only to COUNT calls — never to change what it returns.
	realSwapLoader := newReloadConfigLoader(configPath, tmpDir, credStore, rs)
	swapLoadCalls := 0
	rs.loadConfigForSwap = func() (*config.Config, error) {
		swapLoadCalls++
		return realSwapLoader()
	}

	// Step 1: the file-watcher poller's stale snapshot (V1) — read #1 —
	// sourced from the real on-disk file via the real loader, so the test
	// can tell V1 and V2 apart by content, not just by read-seq bookkeeping.
	staleCfg, err := realSwapLoader()
	require.NoError(t, err, "instrument: reading the initial on-disk config (V1) must succeed")
	staleReadSeq := rs.reloadOutcome.markConfigRead()
	require.EqualValues(t, 1, staleReadSeq, "instrument: the watcher's stale snapshot is read #1")
	require.Equal(t, maxTokensV1, staleCfg.Agents.Defaults.MaxTokens, "instrument: stale snapshot carries V1")

	// Step 2: the admin's PUT /performance commits its write (V2) to disk —
	// "the marked write" — then its OWN reload attempt runs (a fresh read,
	// #2, V2, and cycle #1) and fails, mirroring rest_performance.go's real
	// call: mark(..., startedCount(), configReadsCount()).
	require.NoError(t, os.WriteFile(configPath, []byte(r6DiskConfigJSON(tmpDir, maxTokensV2)), 0o600))
	adminReadSeq := rs.reloadOutcome.markConfigRead()
	require.EqualValues(t, 2, adminReadSeq, "instrument: the admin's own reload reads config fresh (#2, V2)")
	adminCycleSeq := rs.reloadOutcome.begin()
	require.EqualValues(t, 1, adminCycleSeq, "instrument: the admin's own reload is cycle #1")
	rs.reloadOutcome.finish(adminCycleSeq, errors.New("injected admin reload failure"), adminReadSeq)

	rs.restAPIRef.pendingApply.mark(gen.PerformanceReloadFailedDetailsStageReload,
		[]gen.PerformanceReloadFailedDetailsChangedFields{gen.PerformanceReloadFailedDetailsChangedFieldsMaxToolIterations},
		rs.reloadOutcome.startedCount(), rs.reloadOutcome.configReadsCount())
	require.NotNil(t, rs.restAPIRef.pendingApply.snapshot(), "instrument: the admin's failed reload left the save pending")

	// Step 3: the stale watcher snapshot reaches the serve loop through the
	// REAL executeReload -> handleConfigReload -> reloadConfigForSwap path.
	runOneReload := func(c *config.Config) error {
		return executeReload(context.Background(), al, c, &provider, rs, msgBus, true)
	}
	loadNext := func() (*config.Config, error) {
		t.Fatal("loadNext must not be called: first is non-nil and this cycle has no coalesced follow-up")
		return nil, errors.New("test bug: unreachable loadNext")
	}
	runReloadCycle(al, rs, staleCfg, staleReadSeq, runOneReload, loadNext)

	require.Equal(t, 1, swapLoadCalls, "instrument: the swap-time re-read must have run exactly once")
	require.False(t, rs.reloadOutcome.lastFailed(), "instrument: the stale-snapshot reload's real executeReload must have succeeded")
	require.Equal(t, maxTokensV2, al.GetConfig().Agents.Defaults.MaxTokens,
		"instrument: the config actually applied must be V2 (the marked write) — proving the "+
			"swap-time re-read, not the stale `first` snapshot, is what took effect")

	got := rs.restAPIRef.pendingApply.snapshot()
	if got != nil {
		t.Fatalf("expected pending_apply to be cleared: the config actually applied (V2, "+
			"max_tokens=%d) was read by the swap-time re-read strictly after the mark "+
			"(markedAtConfigRead=%d) even though the outer/first snapshot's own readSeq (%d) was "+
			"not; got pending_apply still set with stage=%q changed_fields=%v — the notice must not "+
			"outlive the change it warns about",
			maxTokensV2, adminReadSeq, staleReadSeq, got.Stage, got.ChangedFields)
	}
}
