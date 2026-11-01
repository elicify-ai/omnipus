package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestCapRED_Fold_AlternatingThresholdKeepsThreeReservations(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	al.GetConfig().Performance.MaxParallelAgents = 8
	g := al.steerAdmission()
	capRedFillSteer(t, g, 3)
	// D9: occupancy is fixed at three; this is a sequence of memory-policy
	// evaluations, never an acquisition/release hidden in the observation.
	for i, available := range []uint64{16, 14, 16, 14, 16, 14} {
		threshold := config.MemoryPressureHighFromSignalsForTest(available, 100)
		restore := config.SetMemoryProviderForTest(threshold, func() (uint64, bool) { return available, true })
		limit, cause := foldMemory(8)
		restore()
		wantLimit, wantCause := 8, ""
		if i%2 == 1 {
			wantLimit, wantCause = 2, "memory_pressure"
		}
		capRedAssert(t, "D9 strict threshold has no hysteresis", []any{limit, cause}, []any{wantLimit, wantCause})
		capRedAssert(t, "D9 observations never revoke or grant", []int{g.activeCount(), g.queueLen()}, []int{3, 0})
	}
}
