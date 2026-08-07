// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestMutateConfig_ConcurrentUpsertAgentFast_NoDataRace is the mutation test
// for the MutateConfig data-race fix.
//
// THE RACE (pre-fix): MutateConfig mutated al.cfg's fields IN PLACE under
// al.mu.Lock — no pointer swap, no configGen bump. But GetConfig() hands out
// that same live *config.Config pointer, and fastAgentUpsert/UpsertAgentFast
// passes it straight into its wiring pass (registerSharedTools,
// wireTier13DepsLocked, NewAgentInstance(&cfg.Agents.Defaults, ...), the route
// resolver, …) which reads it WITHOUT any lock during the slow wiring pass. A
// concurrent MutateConfig writing the very object the wiring pass reads is a
// genuine data race — and it was INVISIBLE to the configGen CAS guard, which
// only detects pointer SWAPS by SwapConfig/ReloadProviderAndConfig.
//
// THE FIX: MutateConfig now deep-copies (config.Clone), runs fn on the private
// copy, and publishes via pointer-swap + configGen bump — mirroring SwapConfig
// and ReloadProviderAndConfig. The live pointer GetConfig handed out is never
// touched, so the wiring pass's unlocked reads race nothing.
//
// PROOF MODEL: a hammer — one goroutine hammers MutateConfig (writing a field
// the wiring pass reads: cfg.Agents.Defaults.MaxTokens), several goroutines
// hammer UpsertAgentFast with the LIVE al.cfg pointer (exactly what production
// does at pkg/gateway/gateway.go's UpsertAgentFastFunc:
// `agentLoop.UpsertAgentFast(agentLoop.GetConfig(), agentID)`). The race
// detector flags any unsynchronized write/read pair with no happens-before
// edge between them; under the pre-fix code MutateConfig's in-place write
// (under al.mu) and the wiring pass's field read (NOT under al.mu) are exactly
// such a pair. Run under `-race`.
//
// MUTATION-SENSITIVE: reverting MutateConfig to `return fn(al.cfg)` (in-place,
// no clone/swap) makes `go test -race` report a DATA RACE on
// cfg.Agents.Defaults between MutateConfig's write and UpsertAgentFast's
// wiring-pass read. With the clone-then-swap fix in place, no race is reported.
func TestMutateConfig_ConcurrentUpsertAgentFast_NoDataRace(t *testing.T) {
	al := buildFastUpsertTestLoop(t, []config.AgentConfig{
		{ID: "alpha", Name: "Alpha", Type: config.AgentTypeCustom},
		{ID: "gamma", Name: "Gamma", Type: config.AgentTypeCustom},
	})

	// Seed gamma's durable entity record so UpsertAgentFast's lost-race rebase
	// branch (which asks the entity store via agentstore.Store.Get) does not
	// error out for an unrelated reason — mirrors production ordering where
	// agentstore.Store.Create completes before UpsertAgentFast is ever called.
	require.NoError(t, agentstore.New(al.homePath).Create("gamma", &config.AgentConfig{
		ID: "gamma", Name: "Gamma", Type: config.AgentTypeCustom,
	}))

	// Workload is intentionally small: the race detector flags the FIRST
	// unsynchronized write/read pair with no happens-before edge — it does not
	// need a large iteration count to fire, only genuine concurrency between
	// MutateConfig's write and one wiring-pass read. Each UpsertAgentFast call
	// runs the full wiring pass (registerSharedTools et al.), which is heavy,
	// so a handful of overlapping calls is both sufficient and pod-friendly.
	const readers = 2
	const itersPerReader = 3

	stop := make(chan struct{})
	var mutateWG, upsertWG sync.WaitGroup

	// Writer: hammer MutateConfig, mutating a field the wiring pass reads.
	// Pre-fix, each call wrote the LIVE al.cfg in place — the racing write.
	mutateWG.Add(1)
	go func() {
		defer mutateWG.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = al.MutateConfig(func(cfg *config.Config) error {
				// Write a Defaults field; UpsertAgentFast's wiring pass reads
				// cfg.Agents.Defaults (NewAgentInstance takes its address) with
				// no lock — the racing read.
				cfg.Agents.Defaults.MaxTokens = 1000 + (i % 500)
				i++
				return nil
			})
		}
	}()

	// Readers: hammer UpsertAgentFast with the LIVE al.cfg pointer, exactly as
	// pkg/gateway/gateway.go's UpsertAgentFastFunc does in production.
	for r := 0; r < readers; r++ {
		upsertWG.Add(1)
		go func() {
			defer upsertWG.Done()
			for j := 0; j < itersPerReader; j++ {
				_, _ = al.UpsertAgentFast(al.GetConfig(), "gamma")
			}
		}()
	}
	upsertWG.Wait()
	close(stop)
	mutateWG.Wait()
}

// TestMutateConfig_PreservesRegisteredSensitiveValues locks in the
// config.Clone change that carries registeredSensitive across the swap.
//
// MutateConfig now publishes a Clone() of the live config as the new al.cfg.
// config.Clone JSON-round-trips, which drops the unexported registeredSensitive
// slice (the resolved credential plaintexts registered at boot/reload via
// RegisterSensitiveValues). If the clone lost them, the post-swap live config
// would scrub NOTHING from LLM output/audit logs until the next full reload — a
// security regression the race fix must NOT introduce. config.Clone therefore
// carries registeredSensitive onto the clone.
//
// MUTATION-SENSITIVE: reverting config.Clone's registeredSensitive carry-over
// (while keeping MutateConfig's clone+swap) makes this test fail — the
// registered secret's plaintext appears unfiltered after MutateConfig returns.
func TestMutateConfig_PreservesRegisteredSensitiveValues(t *testing.T) {
	al := buildFastUpsertTestLoop(t, []config.AgentConfig{
		{ID: "alpha", Name: "Alpha", Type: config.AgentTypeCustom},
	})
	const secret = "supersecret-api-key-value-12345" // >3 chars so it is filtered
	al.GetConfig().RegisterSensitiveValues([]string{secret})

	// Sanity: scrubbing works on the pre-mutation live config.
	pre := al.GetConfig().FilterSensitiveData("token=" + secret + " ok")
	require.Containsf(t, pre, "[FILTERED]",
		"registered secret must be scrubbed BEFORE MutateConfig (sanity)")
	require.NotContains(t, pre, secret)

	// MutateConfig publishes a clone as the new al.cfg. That clone MUST still
	// scrub the registered secret — otherwise clone+swap regresses credential
	// scrubbing until the next full reload.
	require.NoError(t, al.MutateConfig(func(cfg *config.Config) error {
		cfg.Agents.Defaults.MaxTokens = 7777
		return nil
	}))

	post := al.GetConfig().FilterSensitiveData("token=" + secret + " ok")
	require.Containsf(t, post, "[FILTERED]",
		"registered secret must still be scrubbed after MutateConfig's clone+swap — "+
			"if this fails, config.Clone dropped registeredSensitive and the live config no longer scrubs credentials")
	require.NotContains(t, post, secret,
		"registered secret plaintext must not appear after MutateConfig")
}
