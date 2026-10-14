package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// F02: chief's exact-one oracle and amendment §4.2. Unlike the original
// continuation test, this reaches the REAL dispatcher after recordToolCalls
// reports its write failure. Removing the dispatcher's already-reported-error
// guard must produce two notices/two classified transcript errors and fail.
func TestWU22DispatcherContinuationFailureReportsExactlyOnce(t *testing.T) {
	h := wu22NewHarness(t, wu22HealthyProvider())
	rr := wu22ContinuationStage(h)
	cn := &agentLoopRunTurnConductorRunIterations{
		rc: &agentLoopRunTurnConductor{
			rx: &agentLoopRunTurnTools{ctx: context.Background(), rr: rr},
		},
	}
	restore := wu22BlockModelWrite(t, h.store, h.sid)
	flow := cn.dispatchToolCalls()
	assert.Equal(t, agentLoopRunTurnConductorRunIterationsReturn, flow, "failed flush must end the dispatcher")
	assert.Equal(t, agentLoopRunTurnConductorReturn, cn.ret0)
	assert.Equal(t, TurnEndStatusError, cn.rc.ret0.status)
	assert.Equal(t, TurnEndStatusError, rr.rq.ri.turnStatus)
	// These helpers count EXACTLY ONE live save error and EXACTLY ONE saved
	// classified error, and check the failure flag before any runTurn defer.
	h.assertSaveFailure(t, cn.rc.ret1)
	assert.Empty(t, rr.normalizedToolCalls, "no next tool-call group was admitted")
	assert.Empty(t, rr.rq.ri.messages, "no following tool request entered the window")
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, event := range h.events {
		assert.NotEqual(t, EventKindToolExecStart, event.Kind, "no tool execution after a failed continuation flush")
	}
	restore()
}

// Amendment §4.2 explicitly requires immediate markTurnFailed. Calling this
// stage directly excludes runTurn's existing error defer, which can otherwise
// conceal removal of reportSessionSaveFailure's mark (CHECK mutation M17).
func TestWU22FinalizeStageArchiveFailureMarksBeforeReturn(t *testing.T) {
	h := wu22NewHarness(t, wu22HealthyProvider())
	rr := h.responseStage()
	rz := &agentLoopRunTurnFinalize{
		rc: &agentLoopRunTurnConductor{
			rx: &agentLoopRunTurnTools{
				ctx: context.Background(), rr: rr, finalContent: "WU22 exact stage answer",
			},
		},
	}
	require.False(t, h.ts.turnFailed, "SETUP: stage begins as a healthy turn")
	restore := wu22BlockModelWrite(t, h.store, h.sid)
	_, err := rz.finalizeTurn() // no runTurn and no deferred failure-mark substitute
	assert.True(t, h.ts.turnFailed, "§4.2: finalization stage must mark failed before it returns")
	assert.Equal(t, TurnEndStatusError, rr.rq.ri.turnStatus)
	h.assertSaveFailure(t, err)
	restore()
}

func TestWU22FinalizeStageHealthyStoreCompletesWithoutFailure(t *testing.T) {
	h := wu22NewHarness(t, wu22HealthyProvider())
	rr := h.responseStage()
	rz := &agentLoopRunTurnFinalize{
		rc: &agentLoopRunTurnConductor{
			rx: &agentLoopRunTurnTools{
				ctx: context.Background(), rr: rr, finalContent: "WU22 exact stage answer",
			},
		},
	}
	result, err := rz.finalizeTurn()
	require.NoError(t, err)
	assert.Equal(t, "WU22 exact stage answer", result.finalContent)
	assert.Equal(t, TurnEndStatusCompleted, result.status)
	assert.False(t, h.ts.turnFailed, "same stage does not mark a healthy write failed")
	assert.Equal(t, TurnPhaseCompleted, h.ts.phase)
	assert.Equal(t, []providers.Message{{Role: "assistant", Content: "WU22 exact stage answer"}}, h.store.GetHistory(h.sid))
	h.ts.finalizeStreamer(context.Background())
	assert.Equal(t, []bool{false}, h.stream.failed)
	h.assertSiblingWritable(t)
}
