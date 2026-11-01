package agent

// Oracle: tool-parity r17 FR-200, 21.16–21.19. Real admission gates; only
// host-memory signal and the cap resolver are faked process edges.
import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSteerAdmission_ReleaseLockedRespectsLiveCap(t *testing.T) {
	for _, method := range []string{"release", "releaseExecution", "disposition-owner releaseLocked"} {
		t.Run(method, func(t *testing.T) {
			liveCap := 3
			g := newSteerAdmission(func() int { return liveCap })
			for i := 0; i < 3; i++ {
				yes, _, _ := g.tryAdmitRun(fmt.Sprint(i), 1, fmt.Sprint("run-", i), 1)
				require.True(t, yes)
			}
			liveCap = 2
			yes, pos, limit := g.tryAdmitRun("queued", 1, "run-q", 1)
			require.False(t, yes)
			assert.Equal(t, 1, pos)
			assert.Equal(t, 2, limit)
			var entry steerQueueEntry
			var promoted bool
			switch method {
			case "release":
				entry, promoted = g.release("0", 1)
			case "releaseExecution":
				entry, promoted = g.releaseExecution(executionClaim{SessionID: "0", Generation: 1, RunID: "run-0", BootSeq: 1})
			default:
				g.mu.Lock()
				entry, promoted = g.releaseLocked("0")
				g.mu.Unlock()
			}
			assert.False(t, promoted, "21.17 after first release two running already fill the live cap; must not promote %s", entry.sessionID)
			assert.Equal(t, 2, g.activeCount())
			assert.Equal(t, 1, g.queueLen())
			next, promoted := g.release("1", 1)
			require.True(t, promoted, "21.17 second release permits exactly one queued start")
			assert.Equal(t, "queued", next.sessionID)
			assert.Equal(t, 2, g.activeCount())
			assert.Zero(t, g.queueLen())
		})
	}
}

func TestSelfDelegate_MemoryPressure_AdmissionBoundary(t *testing.T) {
	for _, tc := range []struct {
		name           string
		high, ok       bool
		explicit, want int
	}{
		{"21.16 pressure", true, true, 0, 2}, {"21.19 unmeasurable", false, false, 0, 2}, {"21.19 operator lower cap", true, true, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubMemory(t, tc.high, tc.ok)
			al, cleanup := newAL(t)
			defer cleanup()
			cfg := al.GetConfig()
			cfg.Performance.MaxParallelAgents = tc.explicit
			g := al.steerAdmission()
			for i := 0; i < tc.want; i++ {
				admitted, _, limit := g.tryAdmit(fmt.Sprint(i), 1)
				require.True(t, admitted)
				assert.Equal(t, tc.want, limit, "FR-200 cap in effect must include memory")
			}
			admitted, pos, limit := g.tryAdmit("extra", 1)
			assert.False(t, admitted, "extra delegate must queue, not start")
			assert.Equal(t, 1, pos)
			assert.Equal(t, tc.want, limit)
		})
	}
}

func TestSelfDelegate_QueueDrainsUnderLiveCap(t *testing.T) {
	liveCap := 2
	g := newSteerAdmission(func() int { return liveCap })
	for _, id := range []string{"a", "b"} {
		ok, _, _ := g.tryAdmit(id, 1)
		require.True(t, ok)
	}
	for i, id := range []string{"q1", "q2", "q3"} {
		ok, pos, _ := g.tryAdmit(id, 1)
		require.False(t, ok)
		assert.Equal(t, i+1, pos)
	}
	for i, ending := range []string{"a", "b", "q1"} {
		next, ok := g.release(ending, 1)
		require.True(t, ok, "21.18 queue must not stall")
		assert.Equal(t, fmt.Sprintf("q%d", i+1), next.sessionID)
		assert.Equal(t, 2, g.activeCount())
	}
	assert.Zero(t, g.queueLen())
	// A resolver going lower while work is already running must never cancel
	// existing work and must not promote until capacity actually becomes free.
	liveCap = 1
	_, promoted := g.release("q2", 1)
	assert.False(t, promoted)
	assert.Equal(t, 1, g.activeCount())
}
