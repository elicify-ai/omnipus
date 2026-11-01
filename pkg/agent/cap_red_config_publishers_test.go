package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestCapRED_ConfigPublication_AllCapacityPublishers(t *testing.T) {
	for _, publisher := range []string{"SwapConfig", "MutateConfig", "ReloadProviderAndConfig", "SwapWrittenConfig", "SwapReadConfig"} {
		t.Run(publisher, func(t *testing.T) {
			high, ok := false, true
			capRedMemory(t, &high, &ok)
			al, _ := newSteerAL(t)
			old := al.GetConfig()
			before := al.configGen.Load()
			next, err := old.Clone()
			if err != nil {
				t.Fatal(err)
			}
			next.Performance.MaxParallelAgents = 2
			next.Planning.GlobalActiveLoopCap = 4
			switch publisher {
			case "SwapConfig":
				al.SwapConfig(next)
			case "MutateConfig":
				err = al.MutateConfig(func(c *config.Config) error {
					c.Performance.MaxParallelAgents = 2
					c.Planning.GlobalActiveLoopCap = 4
					return nil
				})
			case "ReloadProviderAndConfig":
				inst, yes := al.GetRegistry().GetAgent(testDefaultAgentID)
				if !yes {
					t.Fatal("missing real harness provider")
				}
				err = al.ReloadProviderAndConfig(context.Background(), inst.Provider, next)
			case "SwapWrittenConfig":
				al.SwapWrittenConfig(next)
			case "SwapReadConfig":
				err = al.SwapReadConfig(next)
			}
			if err != nil {
				t.Fatal(err)
			}
			capRedAssert(t, "capacity publisher increments generation exactly once", al.configGen.Load(), before+1)
			live := al.GetConfig()
			capRedAssert(t, "both capacity values published together", []int{live.Performance.MaxParallelAgents, live.Planning.GlobalActiveLoopCap}, []int{2, 4})
			if live == old {
				t.Error("capacity publisher mutated original snapshot instead of replacing it")
			}
			source, yes := any(al).(capRedConfigSource)
			if !yes {
				t.Error("CAP-B12 ConfigSnapshot missing for real publisher; coherent pointer/generation read not available")
			} else {
				cfg, gen := source.ConfigSnapshot()
				capRedAssert(t, "coherent real publisher pair", []any{cfg, gen}, []any{live, before + 1})
			}
			te := GetTaskExecutor(al)
			te.syncDispatchCapacity()
			capRedAssert(t, "dispatch re-read applies published configured capacity", te.DispatchSemaCap(), 2)
			if _, yes := any(te).(capRedCapacityApplier); !yes {
				t.Error("CAP-B12 generation-aware ApplyConfiguredCapacity missing; unversioned sync is not acceptance")
			}
		})
	}
}

func TestCapRED_ConfigPublication_FailedMutationDoesNotPublish(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	before, gen := al.GetConfig(), al.configGen.Load()
	rejected := errors.New("test config mutation rejected")
	err := al.MutateConfig(func(c *config.Config) error {
		c.Performance.MaxParallelAgents = 2
		return rejected
	})
	if !errors.Is(err, rejected) {
		t.Fatalf("failed config mutation must propagate original error: %v", err)
	}
	capRedAssert(t, "failed mutation preserves live pointer and generation", []any{al.GetConfig(), al.configGen.Load()}, []any{before, gen})
}
