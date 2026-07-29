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

	"github.com/elicify-ai/omnipus/pkg/agentstore"
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

	// Durably create gamma's entity record BEFORE racing, mirroring
	// production ordering: pkg/gateway/rest.go's createAgent always calls
	// agentstore.Store.Create synchronously, and it completes, BEFORE
	// fastAgentUpsert ever calls UpsertAgentFast. This is what makes the
	// "not yet in the rebased/live config" branch below a genuine create
	// race rather than the DEFECT 1 resurrection case (registry.go's
	// UpsertAgentFast now confirms the entity record exists via
	// agentstore.Store.Get before appending an absent agent back in, exactly
	// to tell those two cases apart).
	require.NoError(t, agentstore.New(al.homePath).Create("gamma", &config.AgentConfig{
		ID: "gamma", Name: "Gamma", Type: config.AgentTypeCustom,
	}))

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

// TestUpsertAgentFast_ConcurrentDelete_DoesNotResurrect is a regression test
// for DEFECT 1 (concurrency review): UpsertAgentFast's lost-race rebase used
// to append `wantAC` back into the rebased config UNCONDITIONALLY whenever
// `id` was absent from it — with no check for WHY it was absent. A genuine
// concurrent DELETE of the very agent being upserted (deleteAgent's
// agentstore.Store.Delete + triggerReloadAndWait, which legitimately drops
// the id from both cfg.Agents.List and the registry via
// ReloadProviderAndConfig's swap) produces exactly that same shape — id
// absent from the post-reload config — and got silently resurrected.
//
// The interleaving is forced deterministically via upsertAgentFastTestHook,
// same seam as TestUpsertAgentFast_ConcurrentFullReload_CfgNotReverted:
// goroutine A (updating gamma) is paused right after its first-attempt
// snapshot, while the test deletes gamma's entity record and runs the full
// reload a real deleteAgent would trigger, dropping gamma from both cfg and
// registry. Goroutine A is therefore guaranteed to lose its CAS race and
// hit the "id absent from rebased config" branch.
//
// MUTATION-SENSITIVE: removing the agentstore.Store.Get existence check
// before the `rebased.Agents.List = append(...)` line in registry.go (i.e.
// reverting to the unconditional append) makes this test fail: fastErr
// would be nil and GetAgent("gamma") would report true, resurrecting an
// agent a concurrent request just deleted.
func TestUpsertAgentFast_ConcurrentDelete_DoesNotResurrect(t *testing.T) {
	al := buildFastUpsertTestLoop(t, []config.AgentConfig{
		{ID: "alpha", Name: "Alpha", Type: config.AgentTypeCustom},
		{ID: "gamma", Name: "Gamma", Type: config.AgentTypeCustom},
	})

	// gamma's entity record already exists on disk, as any agent present in
	// the seeded roster would.
	require.NoError(t, agentstore.New(al.homePath).Create("gamma", &config.AgentConfig{
		ID: "gamma", Name: "Gamma", Type: config.AgentTypeCustom,
	}))

	// cfgFast: goroutine A's own snapshot — an update to gamma (e.g. a rename
	// via PUT /api/v1/agents/gamma), taken BEFORE the concurrent delete below
	// ever runs.
	cfgFast := cloneCfg(t, al.GetConfig())
	for i := range cfgFast.Agents.List {
		if cfgFast.Agents.List[i].ID == "gamma" {
			cfgFast.Agents.List[i].Name = "Gamma Renamed By A"
		}
	}

	// cfgAfterDelete: what deleteAgent's triggerReloadAndWait actually
	// reloads — gamma removed from the roster, mirroring
	// populateAgentsListFromEntityStore re-reading the entity store AFTER
	// agentstore.Store.Delete("gamma") already ran (ADR-054 D6 rule 5: the
	// entity record is removed FIRST).
	cfgAfterDelete := cloneCfg(t, al.GetConfig())
	survivors := make([]config.AgentConfig, 0, len(cfgAfterDelete.Agents.List))
	for _, ac := range cfgAfterDelete.Agents.List {
		if ac.ID != "gamma" {
			survivors = append(survivors, ac)
		}
	}
	cfgAfterDelete.Agents.List = survivors

	snapshotTaken := make(chan struct{})
	deleteReloadDone := make(chan struct{})

	upsertAgentFastTestHook = func(attempt int) {
		if attempt != 0 {
			return // only force the race on the FIRST attempt
		}
		close(snapshotTaken)
		<-deleteReloadDone
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

	// Simulate a real DELETE /api/v1/agents/gamma: remove the durable entity
	// record FIRST, then run the full reload that drops gamma from both
	// cfg.Agents.List and the registry.
	require.NoError(t, agentstore.New(al.homePath).Delete("gamma"))
	reloadErr := al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, cfgAfterDelete)
	close(deleteReloadDone) // release goroutine A regardless of reloadErr, before any t.Fatal below
	require.NoError(t, reloadErr, "the racing delete-triggered reload must itself succeed")

	wg.Wait()

	// --- The core DEFECT 1 assertion: the upsert must fail, not resurrect. ---
	require.Error(t, fastErr,
		"UpsertAgentFast must NOT report success for an agent deleted by a concurrent request "+
			"(a nil error here means gamma was silently resurrected)")
	require.Contains(t, fastErr.Error(), "deleted by a concurrent request",
		"the error must identify the resurrection refusal, not some other failure")

	_, ok := al.GetRegistry().GetAgent("gamma")
	require.False(t, ok, "gamma must stay deleted in the registry — it must NOT be resurrected")

	liveCfg := al.GetConfig()
	for _, ac := range liveCfg.Agents.List {
		require.NotEqual(t, "gamma", ac.ID,
			"gamma must stay deleted in cfg.Agents.List — it must NOT be resurrected")
	}

	// --- Unrelated pre-existing agent must be unaffected. ---
	_, ok = al.GetRegistry().GetAgent("alpha")
	require.True(t, ok, "unrelated pre-existing agent alpha must survive")
}

// TestUpsertAgentFast_ConcurrentSwapConfig_NotReverted is a regression test
// for DEFECT 2 (concurrency review): the CAS check `al.registry != oldRegistry`
// assumed the ONLY writer that can ever win this race is a full
// ReloadProviderAndConfig, which swaps al.cfg and al.registry TOGETHER. That
// premise is false — AgentLoop.SwapConfig (the path EVERY REST-initiated
// config write goes through per refreshConfigAndRewireServices, e.g. an
// unrelated tool-policy tightening or a god-mode toggle) replaces al.cfg
// ALONE and never touches al.registry. A registry-pointer-only CAS check is
// blind to that: it reads "no race" and the eventual publish's
// `al.cfg = cfg` silently reverts whatever the concurrent SwapConfig just
// installed.
//
// The interleaving is forced deterministically via upsertAgentFastTestHook:
// goroutine A (creating gamma) is paused right after its first-attempt
// snapshot, while the test calls al.SwapConfig with an independent config
// that changes an unrelated field (Agents.Defaults.MaxTokens) — deliberately
// NOT touching the agent roster, to isolate whether the bug is "any
// concurrent cfg write gets lost" rather than something roster-specific.
// al.registry is NEVER swapped in this test, so a registry-pointer-only CAS
// check would never detect this race at all.
//
// MUTATION-SENSITIVE: reverting registry.go's CAS condition from
// `al.registry != oldRegistry || al.configGen.Load() != oldGen` back to
// `al.registry != oldRegistry` alone makes this test fail: MaxTokens reverts
// to its pre-swap value because UpsertAgentFast never notices the SwapConfig
// happened and publishes its own stale `cfg` on top of it.
func TestUpsertAgentFast_ConcurrentSwapConfig_NotReverted(t *testing.T) {
	al := buildFastUpsertTestLoop(t, []config.AgentConfig{
		{ID: "alpha", Name: "Alpha", Type: config.AgentTypeCustom},
	})

	// gamma's entity record already exists on disk before the race starts,
	// mirroring production ordering (agentstore.Store.Create always runs,
	// synchronously, before fastAgentUpsert calls UpsertAgentFast).
	require.NoError(t, agentstore.New(al.homePath).Create("gamma", &config.AgentConfig{
		ID: "gamma", Name: "Gamma", Type: config.AgentTypeCustom,
	}))

	// cfgFast: goroutine A's own snapshot — the create request for gamma,
	// taken BEFORE the concurrent SwapConfig below ever runs.
	cfgFast := cloneCfg(t, al.GetConfig())
	cfgFast.Agents.List = append(cfgFast.Agents.List, config.AgentConfig{
		ID: "gamma", Name: "Gamma", Type: config.AgentTypeCustom,
	})

	// cfgSwapped: an INDEPENDENT config write landing via a bare SwapConfig
	// (never a full reload) — e.g. an unrelated Settings save. Touches only
	// Agents.Defaults.MaxTokens, nothing about the agent roster, so any
	// revert of it is unambiguously this bug and not some roster mixup.
	const swappedMaxTokens = 99999
	cfgSwapped := cloneCfg(t, al.GetConfig())
	cfgSwapped.Agents.Defaults.MaxTokens = swappedMaxTokens

	snapshotTaken := make(chan struct{})
	swapDone := make(chan struct{})

	upsertAgentFastTestHook = func(attempt int) {
		if attempt != 0 {
			return // only force the race on the FIRST attempt
		}
		close(snapshotTaken)
		<-swapDone
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

	al.SwapConfig(cfgSwapped)
	close(swapDone)

	wg.Wait()
	require.NoError(t, fastErr, "UpsertAgentFast must still succeed after losing the CAS race to a bare SwapConfig")

	// --- The core DEFECT 2 assertion: the concurrent SwapConfig's own
	// change must survive UpsertAgentFast's eventual publish. ---
	liveCfg := al.GetConfig()
	require.Equal(t, swappedMaxTokens, liveCfg.Agents.Defaults.MaxTokens,
		"a concurrent SwapConfig's change must survive — reverting to the pre-swap value means "+
			"UpsertAgentFast's registry-pointer-only CAS check missed a bare SwapConfig (DEFECT 2)")

	// --- Goroutine A's own request must not have been lost either. ---
	_, ok := al.GetRegistry().GetAgent("gamma")
	require.True(t, ok, "the fast-upserted agent itself (gamma) must survive the rebase-and-retry")

	foundGamma := false
	for _, ac := range liveCfg.Agents.List {
		if ac.ID == "gamma" {
			foundGamma = true
		}
	}
	require.True(t, foundGamma, "al.cfg.Agents.List must still contain gamma after rebasing onto the swapped config")

	_, ok = al.GetRegistry().GetAgent("alpha")
	require.True(t, ok, "pre-existing agent alpha must survive")
}
