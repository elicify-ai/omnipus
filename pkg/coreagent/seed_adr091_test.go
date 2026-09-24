package coreagent

import "testing"

func TestSeeds_NoAwait(t *testing.T) {
	for _, id := range []CoreAgentID{IDJim, IDPlanner, IDWorker} {
		seed := SeedDelegationEdges(id)
		if seed == nil {
			continue
		}
		for _, mode := range seed.Modes {
			if string(mode) == "await" {
				t.Fatalf("%s seed still advertises retired await mode", id)
			}
		}
	}
}
