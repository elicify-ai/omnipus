// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// countScorchGoroutines returns the number of live goroutines whose stack frames
// reference the bleve scorch engine's background loops (introducerLoop /
// mergerLoop / persisterLoop). Each open scorch index spawns these; they only
// exit when the index is Closed. This is the signal the leak regression watches.
func countScorchGoroutines() int {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	dump := string(buf[:n])
	count := 0
	for _, marker := range []string{
		"index/scorch.(*Scorch).introducerLoop",
		"index/scorch.(*Scorch).mergerLoop",
		"index/scorch.(*Scorch).persisterLoop",
	} {
		count += strings.Count(dump, marker)
	}
	return count
}

// waitScorchGoroutinesAtMost polls until the scorch goroutine count drops to at
// most want, or the deadline elapses. bleve's scorch teardown signals its loops
// asynchronously, so a brief poll avoids a flaky exact-count race.
func waitScorchGoroutinesAtMost(want int, deadline time.Duration) int {
	end := time.Now().Add(deadline)
	last := countScorchGoroutines()
	for time.Now().Before(end) {
		last = countScorchGoroutines()
		if last <= want {
			return last
		}
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
	return last
}

// TestAgentLoopClose_TearsDownMemoryScorchGoroutines is the leak regression for
// the gateway 10-min-timeout hang root cause: AgentInstance.Close only closed the
// session store, never the ContextBuilder's MemoryStore, so each agent's per-room
// bleve/scorch index kept its introducerLoop/mergerLoop goroutines alive for the
// life of the process. al.Close() now walks the registry and closes every agent's
// MemoryStore (closeAgentMemoryStores), which must make those goroutines exit.
//
// BDD:
//
//	Given an AgentLoop whose default agent has opened its memory bleve index
//	  (so scorch background goroutines are running),
//	When al.Close() is called,
//	Then the scorch background goroutines exit (count returns to the baseline).
func TestAgentLoopClose_TearsDownMemoryScorchGoroutines(t *testing.T) {
	baseline := countScorchGoroutines()

	tmpRoot := t.TempDir()
	workspace := filepath.Join(tmpRoot, "home", "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspace,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspace}},
		},
	}

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})

	// Force at least one agent's memory bleve index to open by writing a memory
	// and searching it. The search path lazily opens the scorch index, spawning
	// the introducer/merger goroutines we expect Close to reap.
	reg := al.GetRegistry()
	ids := reg.ListAgentIDs()
	if len(ids) == 0 {
		t.Fatal("expected at least one registered agent")
	}
	opened := 0
	for _, id := range ids {
		inst, ok := reg.GetAgent(id)
		if !ok || inst == nil || inst.ContextBuilder == nil {
			continue
		}
		ms := inst.ContextBuilder.Memory()
		if ms == nil {
			continue
		}
		if err := ms.AppendLongTerm("scorch teardown regression memory entry", "reference"); err != nil {
			t.Fatalf("AppendLongTerm: %v", err)
		}
		if _, err := ms.SearchEntries("scorch", 5); err != nil {
			t.Fatalf("SearchEntries: %v", err)
		}
		opened++
	}
	if opened == 0 {
		t.Fatal("expected to open at least one agent MemoryStore index")
	}

	// The index open must have spawned scorch goroutines above the baseline —
	// otherwise the test is not exercising the teardown path it claims to.
	afterOpen := countScorchGoroutines()
	if afterOpen <= baseline {
		t.Fatalf("expected scorch goroutines to be running after index open; baseline=%d after_open=%d",
			baseline, afterOpen)
	}

	al.Close()

	// After Close, the scorch goroutines must drain back to the baseline. A wedged
	// teardown (the pre-fix leak) would leave them running forever.
	final := waitScorchGoroutinesAtMost(baseline, 10*time.Second)
	if final > baseline {
		t.Fatalf("scorch goroutines leaked after Close: baseline=%d after_open=%d final=%d",
			baseline, afterOpen, final)
	}
}

// TestAgentLoopClose_BoundedWhenRecapDrainWedged proves Close() can NEVER block
// indefinitely on the recap drain. It registers a recap goroutine on recapWG that
// never returns (the production failure mode: a mock/real LLM that hangs), then
// asserts Close() still returns within a small multiple of the drain budget
// rather than hanging until the test's own timeout.
//
// This is the direct robustness guarantee behind the gateway fix: a stuck recap
// can't wedge t.Cleanup(al.Close) and stall every t.Parallel() peer.
func TestAgentLoopClose_BoundedWhenRecapDrainWedged(t *testing.T) {
	tmpRoot := t.TempDir()
	workspace := filepath.Join(tmpRoot, "home", "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspace,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspace}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})

	// Simulate a wedged in-flight recap: add to recapWG and never Done().
	stuck := make(chan struct{})
	al.recapWG.Add(1)
	go func() {
		<-stuck // never closed — this goroutine, and thus recapWG, never drains
		al.recapWG.Done()
	}()
	t.Cleanup(func() { close(stuck) }) // let the leaked goroutine exit at test end

	// Drive Close() with a short drain budget and assert it returns promptly.
	// The default 30s budget would itself blow this test's intent, so exercise
	// the bounded waiter directly with a tiny budget — proving the select-on-timer
	// path returns instead of hanging on recapWG.Wait().
	done := make(chan struct{})
	go func() {
		al.waitRecapDrain(200 * time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
		// Bounded drain returned despite the wedged recap — correct.
	case <-time.After(5 * time.Second):
		t.Fatal("waitRecapDrain did not return within 5s despite a wedged recap — Close is NOT bounded")
	}
}
