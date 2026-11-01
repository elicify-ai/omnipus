package agent

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

type capRedConfigSource interface {
	ConfigSnapshot() (*config.Config, uint64)
}
type capRedCapacityApplier interface{ ApplyConfiguredCapacity(uint64, int) }

func TestCapRED_ConfigPublication_GenerationMonotonic(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	source, exists := any(al).(capRedConfigSource)
	if !exists {
		t.Fatal("CAP-B12 RED: AgentLoop.ConfigSnapshot is absent; cannot obtain a coherent cfg/generation pair")
	}
	before, gen := source.ConfigSnapshot()
	next, err := before.Clone()
	if err != nil {
		t.Fatal(err)
	}
	next.Performance.MaxParallelAgents = 2
	al.SwapConfig(next)
	got, g2 := source.ConfigSnapshot()
	capRedAssert(t, "published pointer belongs to generation", got, next)
	capRedAssert(t, "capacity-changing SwapConfig advances once", g2, gen+1)
	// Same-value generation is still a publication; old readers stay older.
	again, err := next.Clone()
	if err != nil {
		t.Fatal(err)
	}
	al.SwapConfig(again)
	_, g3 := source.ConfigSnapshot()
	capRedAssert(t, "second publication monotonic", g3, g2+1)
}

func TestCapRED_DispatchSema_ConfiguredCapacityGeneration(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	cfg, err := al.GetConfig().Clone()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Performance.MaxParallelAgents = 8
	al.SwapConfig(cfg)
	te := GetTaskExecutor(al)
	te.syncDispatchCapacity()
	capRedHold(t, te.dispatchSema, 2)
	source, hasSource := any(al).(capRedConfigSource)
	applier, hasApplier := any(te).(capRedCapacityApplier)
	if !hasSource || !hasApplier {
		t.Fatalf("CAP-B12 RED: coherent snapshot=%t, generation applier=%t; stale-G ordering cannot be executed", hasSource, hasApplier)
	}
	old, g := source.ConfigSnapshot()
	latest, err := old.Clone()
	if err != nil {
		t.Fatal(err)
	}
	latest.Performance.MaxParallelAgents = 2
	al.SwapConfig(latest)
	current, g2 := source.ConfigSnapshot()
	applier.ApplyConfiguredCapacity(g2, current.Performance.MaxParallelAgents)
	applier.ApplyConfiguredCapacity(g, old.Performance.MaxParallelAgents) // S42 order B.
	capRedAssert(t, "stale generation cannot restore 8", te.DispatchSemaCap(), 2)
	d := capRedAcquire(t, te.dispatchSema)
	if d.release != nil {
		defer d.release()
	}
	capRedAssert(t, "third refuses at newest configured cap", d.granted, false)
	applier.ApplyConfiguredCapacity(g2, 8) // Equal generation is stale too.
	capRedAssert(t, "equal generation cannot overwrite capacity", te.DispatchSemaCap(), 2)
}

func TestCapRED_DispatchSema_GenerationRandomizedInterleavings(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	// Fixed seeds make the randomized acquisition order reproducible under -race.
	for seed := int64(0); seed < 24; seed++ {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			te := &TaskExecutor{dispatchSema: newDispatchSemaphore(8)}
			applier, exists := any(te).(capRedCapacityApplier)
			if !exists {
				t.Fatal("CAP-B12 RED: ApplyConfiguredCapacity missing; randomized -race ordering not yet executable")
			}
			order := rand.New(rand.NewSource(seed)).Perm(8)
			var wg sync.WaitGroup
			start := make(chan struct{})
			for _, i := range order {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					value := 8
					if i == 7 {
						value = 2
					}
					applier.ApplyConfiguredCapacity(uint64(i+1), value)
				}(i)
			}
			close(start)
			wg.Wait()
			capRedAssert(t, "highest published generation wins", te.DispatchSemaCap(), 2)
		})
	}
}

func TestCapRED_FastUpsertDerivedFromLiveConfig(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	old, err := al.GetConfig().Clone()
	if err != nil {
		t.Fatal(err)
	}
	old.Performance.MaxParallelAgents = 8
	old.Planning.GlobalActiveLoopCap = 16
	old.Gateway.Port = 18001
	al.SwapConfig(old)
	stale, err := old.Clone()
	if err != nil {
		t.Fatal(err)
	}
	stale.Gateway.Port = 19999 // Not an agent entry: must never be published.
	for i := range stale.Agents.List {
		if stale.Agents.List[i].ID == testDefaultAgentID {
			stale.Agents.List[i].Name = "CAP requested agent name"
		}
	}
	live, err := old.Clone()
	if err != nil {
		t.Fatal(err)
	}
	live.Performance.MaxParallelAgents = 2
	live.Planning.GlobalActiveLoopCap = 4
	live.Gateway.Port = 28001
	al.SwapConfig(live) // S42b: BEFORE upsert's very first generation sample.
	te := GetTaskExecutor(al)
	te.syncDispatchCapacity()
	beforeGen := al.configGen.Load()
	_, err = al.UpsertAgentFast(stale, testDefaultAgentID)
	if err != nil {
		t.Fatalf("real fast upsert: %v", err)
	}
	got := al.GetConfig()
	capRedAssert(t, "live agent-turn capacity survives stale caller", got.Performance.MaxParallelAgents, 2)
	capRedAssert(t, "live planning capacity survives stale caller", got.Planning.GlobalActiveLoopCap, 4)
	capRedAssert(t, "all non-agent fields come from live config", got.Gateway.Port, 28001)
	capRedAssert(t, "derived upsert has no generation bump", al.configGen.Load(), beforeGen)
	te.syncDispatchCapacity()
	capRedAssert(t, "no later sync can restore stale capacity", te.DispatchSemaCap(), 2)
	if got == stale {
		t.Error("caller config was published instead of a live-derived clone")
	}
	found := false
	for _, a := range got.Agents.List {
		if a.ID == testDefaultAgentID {
			found = true
			capRedAssert(t, "requested agent entry actually applied", a.Name, "CAP requested agent name")
		}
	}
	if !found {
		t.Fatal("requested agent disappeared from published config")
	}
}

func TestCapRED_FastUpsertRejectsMissingLiveConfig(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	stale, err := al.GetConfig().Clone()
	if err != nil {
		t.Fatal(err)
	}
	al.mu.Lock()
	live := al.cfg
	al.cfg = nil
	al.mu.Unlock()
	defer func() { al.mu.Lock(); al.cfg = live; al.mu.Unlock() }()
	_, err = al.UpsertAgentFast(stale, testDefaultAgentID)
	if err == nil {
		t.Error("CAP-B12(5): missing live configuration must fail upsert, allowing caller's full-reload fallback")
	}
}
