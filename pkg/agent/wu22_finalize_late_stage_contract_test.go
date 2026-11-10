package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The unit is finalizeTurn, not gateway. Its external finalizer storage edge
// performs an actual append-open/write; a directory target returns a REAL
// filesystem error. No turn status/flag/error is fabricated or returned by a
// mocked engine. There are no outer runTurn defers to repair the stage state.
type wu22StageAppendStreamer struct {
	path  string
	calls int
}

func (*wu22StageAppendStreamer) Update(context.Context, string) error { return nil }
func (*wu22StageAppendStreamer) Cancel(context.Context)               {}
func (s *wu22StageAppendStreamer) Finalize(_ context.Context, content string) error {
	s.calls++
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(content)
	return errors.Join(writeErr, file.Close())
}

func wu22LateFinalizeStage(h *wu22Harness) *agentLoopRunTurnFinalize {
	rr := h.responseStage()
	return &agentLoopRunTurnFinalize{
		rc: &agentLoopRunTurnConductor{
			rx: &agentLoopRunTurnTools{ctx: context.Background(), rr: rr, finalContent: "WU22 late-stage exact answer"},
		},
	}
}

// Chief round 3 / amendment §4.1–§4.2: pin EACH immediate stage assignment.
// CHECK R05 removes only markTurnFailed; R06 changes only iteration.turnStatus.
// The returned hardcoded failed bool and runTurn defers must not mask either.
func TestWU22FinalizeLateWriteStageImmediatelyFailsFlagAndEventStatus(t *testing.T) {
	h := wu22NewHarness(t, wu22HealthyProvider())
	path := filepath.Join(t.TempDir(), "final-transcript.jsonl")
	require.NoError(t, os.Mkdir(path, 0o700))
	stream := &wu22StageAppendStreamer{path: path}
	h.ts.setLastStreamer(stream)
	rz := wu22LateFinalizeStage(h)
	require.False(t, h.ts.turnFailed)
	require.Equal(t, TurnEndStatusCompleted, rz.rc.rx.rr.rq.ri.turnStatus)
	result, err := rz.finalizeTurn() // DIRECT stage, no runTurn unwind
	var cause *os.PathError
	require.ErrorAs(t, err, &cause, "actual late filesystem error must be propagated")
	assert.Equal(t, path, cause.Path)
	assert.Equal(t, "open", cause.Op)
	assert.Equal(t, 1, stream.calls)
	assert.True(t, h.ts.turnFailed, "late-write finalization must call markTurnFailed before stage return; no defer may supply it")
	assert.Equal(t, TurnEndStatusError, rz.rc.rx.rr.rq.ri.turnStatus, "finalizeTurn itself must assign error event status before return")
	assert.Equal(t, TurnEndStatusError, result.status, "returned turnResult.status is part of the failed tuple")
	assert.True(t, result.turnFailed)
	assert.Equal(t, []providers.Message{{Role: "assistant", Content: "WU22 late-stage exact answer"}}, h.store.GetHistory(h.sid),
		"instrument: prior model append succeeded; this is the late finalizer branch")
}

func TestWU22FinalizeLateWriteStageHealthyFilesystemCompletes(t *testing.T) {
	h := wu22NewHarness(t, wu22HealthyProvider())
	path := filepath.Join(t.TempDir(), "final-transcript.jsonl")
	stream := &wu22StageAppendStreamer{path: path}
	h.ts.setLastStreamer(stream)
	rz := wu22LateFinalizeStage(h)
	result, err := rz.finalizeTurn()
	require.NoError(t, err)
	assert.Equal(t, 1, stream.calls)
	assert.Equal(t, "WU22 late-stage exact answer", result.finalContent)
	assert.Equal(t, TurnEndStatusCompleted, result.status)
	assert.Equal(t, TurnEndStatusCompleted, rz.rc.rx.rr.rq.ri.turnStatus)
	assert.False(t, result.turnFailed)
	assert.False(t, h.ts.turnFailed)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "WU22 late-stage exact answer", string(data))
}

var _ bus.Streamer = (*wu22StageAppendStreamer)(nil)
