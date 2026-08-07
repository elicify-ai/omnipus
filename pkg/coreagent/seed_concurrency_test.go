// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package coreagent

import (
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestSeedConfig_ConcurrentCalls_NoDuplicateSeed is the regression guard for
// ADR-054 D6 rule 4 / M-7: SeedConfig is a read-all-then-append over
// cfg.Agents.List (build an `existing` set, then append any missing core
// agent). Without an owning lock, two concurrent callers — "two concurrent
// boots (or a boot racing a create)" per the ADR — can each observe "Mia
// missing" and both append her, producing two "mia" entries (and, being an
// unsynchronized slice append, a data race to boot). Run with -race: this
// test's real assertion is "no data race", with "no duplicate core agents"
// as the functional corollary.
func TestSeedConfig_ConcurrentCalls_NoDuplicateSeed(t *testing.T) {
	cfg := &config.Config{}

	const n = 16
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			SeedConfig(cfg)
		}()
	}
	wg.Wait()

	counts := make(map[string]int)
	for _, a := range cfg.Agents.List {
		counts[a.ID]++
	}
	for id, count := range counts {
		if count != 1 {
			t.Errorf("agent %q appears %d times after %d concurrent SeedConfig calls, want exactly 1", id, count, n)
		}
	}
	miaCount := counts[string(IDMia)]
	if miaCount != 1 {
		t.Fatalf("mia appears %d times after concurrent SeedConfig calls, want exactly 1 (this is the observed double-seed bug ADR-054 D6.4/M-7 names)", miaCount)
	}
}

// TestSeedConfig_ConcurrentCalls_DistinctConfigs verifies the lock does not
// serialize unrelated work more than necessary — SeedConfig on N distinct
// *config.Config values still completes correctly and without a race when
// run concurrently. This is the counterpart to
// TestSeedConfig_ConcurrentCalls_NoDuplicateSeed's shared-config race: the
// process-wide seedMu is coarser than per-config (SeedConfig's own contract
// only requires closing the shared-config race, not fine-grained per-config
// parallelism — the ADR's real concurrency goal is per-AGENT parallelism at
// the entity-store layer, not at core-agent seeding, which runs once per
// boot), but it must still be correct, not merely safe.
func TestSeedConfig_ConcurrentCalls_DistinctConfigs(t *testing.T) {
	const n = 8
	cfgs := make([]*config.Config, n)
	for i := range cfgs {
		cfgs[i] = &config.Config{}
	}

	var wg sync.WaitGroup
	for i := range cfgs {
		wg.Add(1)
		go func(cfg *config.Config) {
			defer wg.Done()
			SeedConfig(cfg)
		}(cfgs[i])
	}
	wg.Wait()

	for i, cfg := range cfgs {
		found := false
		for _, a := range cfg.Agents.List {
			if a.ID == string(IDMia) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("cfgs[%d]: expected mia to be seeded", i)
		}
	}
}
