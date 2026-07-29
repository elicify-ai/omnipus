// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestUpsertAgentFast_ConcurrentFullReload_CfgNotReverted is a regression
// test for BUG 1 (concurrency review, commit 99d4e729): UpsertAgentFast's
// CAS retry loop refreshed `oldRegistry` on every attempt but never
// reassigned the `cfg` function parameter itself. A retry that lost the CAS
// race to a concurrent full ReloadProviderAndConfig therefore kept building
// the resolver and default-agent override from the PRE-reload cfg
// snapshot, and the eventual successful publish's `al.cfg = cfg` line
// silently reverted everything the reload had just installed — while
// UpsertAgentFast still reported success.
//
// Note this is a GENUINELY DIFFERENT race than
// TestUpsertAgentFast_ConcurrentDifferentAgents_NoLostUpdate (which only
// races fast-upsert against fast-upsert — already fully serialized by
// fastUpsertMu, so it can never reach the branch this test exercises): here
// the second writer is a full AgentLoop.ReloadProviderAndConfig, the ONE
// publisher fastUpsertMu does NOT serialize against (ReloadProviderAndConfig
// never takes fastUpsertMu).
//
// The interleaving is forced deterministically, not left to scheduler luck:
// upsertAgentFastTestHook (registry.go, test-only, nil in production) pauses
// goroutine A's UpsertAgentFast call immediately after it takes its FIRST
// attempt's oldRegistry/cfg snapshot — precisely the traced window ("A
// snapshots oldRegistry = R0, then spends time in
// registerSharedTools/wireTier13DepsLocked"). While paused there, the test
// runs a full ReloadProviderAndConfig to completion, installing a brand-new
// default-agent override ("delta") that cfgFast (goroutine A's own
// snapshot, taken before the reload) has never heard of. Goroutine A is
// therefore GUARANTEED to lose its first CAS attempt and must retry.
//
// MUTATION-SENSITIVE: reverting the cfg-rebase block inside
// UpsertAgentFast's lost-race branch (registry.go) — i.e. retrying with the
// same, now-stale `cfg` instead of rebasing onto the live al.cfg — makes
// every "must survive" assertion below fail: GetDefaultAgent resolves to
// the "main" sentinel instead of "delta", and
// al.GetConfig().Agents.Defaults.DefaultAgentID reverts to "".
func TestUpsertAgentFast_ConcurrentFullReload_CfgNotReverted(t *testing.T) {
	al := buildFastUpsertTestLoop(t, []config.AgentConfig{
		{ID: "alpha", Name: "Alpha", Type: config.AgentTypeCustom},
	})

	// cfgFast: goroutine A's own snapshot — adds "gamma" (the agent it is
	// upserting) but knows NOTHING about the reload that will race it; it is
	// taken from al.GetConfig() BEFORE the reload below ever runs.
	cfgFast := cloneCfg(t, al.GetConfig())
	cfgFast.Agents.List = append(cfgFast.Agents.List, config.AgentConfig{
		ID: "gamma", Name: "Gamma", Type: config.AgentTypeCustom,
	})

	// cfgReload: an INDEPENDENT full-reload snapshot — adds "delta" and sets
	// it as the configured default agent. Represents an unrelated admin
	// action (e.g. a Settings change) that completes entirely while
	// goroutine A is mid-flight, exactly per the bug's traced interleaving.
	cfgReload := cloneCfg(t, al.GetConfig())
	cfgReload.Agents.List = append(cfgReload.Agents.List, config.AgentConfig{
		ID: "delta", Name: "Delta", Type: config.AgentTypeCustom,
	})
	cfgReload.Agents.Defaults.DefaultAgentID = "delta"

	snapshotTaken := make(chan struct{})
	reloadDone := make(chan struct{})

	upsertAgentFastTestHook = func(attempt int) {
		if attempt != 0 {
			return // only force the race on the FIRST attempt
		}
		close(snapshotTaken)
		<-reloadDone
	}
	t.Cleanup(func() { upsertAgentFastTestHook = nil })

	var wg sync.WaitGroup
	var fastErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, fastErr = al.UpsertAgentFast(cfgFast, "gamma")
	}()

	select {
	case <-snapshotTaken:
	case <-time.After(5 * time.Second):
		t.Fatal("UpsertAgentFast never reached its first-attempt snapshot (test hook not firing?)")
	}

	reloadErr := al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, cfgReload)
	close(reloadDone) // release goroutine A regardless of reloadErr, before any t.Fatal below
	require.NoError(t, reloadErr, "the racing full reload must itself succeed")

	wg.Wait()
	require.NoError(t, fastErr, "UpsertAgentFast must still succeed after losing the CAS race and retrying")

	// --- The reload's own installed state must survive (BUG 1's core claim). ---
	def := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, def)
	require.Equal(t, "delta", def.ID,
		"GetDefaultAgent must resolve to the agent the concurrent reload installed as default (\"delta\") — "+
			"resolving to the \"main\" sentinel instead means UpsertAgentFast's retry silently reverted the "+
			"reload's default-agent override (BUG 1)")

	liveCfg := al.GetConfig()
	require.Equal(t, "delta", liveCfg.Agents.Defaults.DefaultAgentID,
		"al.cfg's default-agent override must still be \"delta\" after the race — reverting to \"\" is "+
			"exactly BUG 1's silent `al.cfg = cfg` revert of a stale snapshot")

	// --- Goroutine A's OWN request must not have been lost either — the fix
	// must not trade "don't revert the reload" for "drop the caller's own
	// upsert". ---
	_, ok := al.GetRegistry().GetAgent("gamma")
	require.True(t, ok, "the fast-upserted agent itself (gamma) must survive the rebase-and-retry")

	foundGamma := false
	for i := range liveCfg.Agents.List {
		if liveCfg.Agents.List[i].ID == "gamma" {
			foundGamma = true
		}
	}
	require.True(t, foundGamma, "al.cfg.Agents.List must still contain gamma after rebasing onto the reload's config")

	// --- Baseline agent and the reload's own new agent must both still be present. ---
	_, ok = al.GetRegistry().GetAgent("alpha")
	require.True(t, ok, "pre-existing agent alpha must survive")
	_, ok = al.GetRegistry().GetAgent("delta")
	require.True(t, ok, "the reload's own new agent delta must survive")
}
