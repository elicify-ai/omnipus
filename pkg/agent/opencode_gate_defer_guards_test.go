package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TEST-ANALYSIS #4 / docs/agents.md Stop semantics: an accepted execution
// Stop, or no steering runtime, cannot promise a queued continuation. This
// unit boundary tests eligibility only, not admission or durable Stop landing.
// Queue, accepted stop binding and deferral are real; only the outgoing stream
// transport is a counting double. stopRequested stays false to isolate the
// distinct executionStopPending guard.
func TestOpenCodeGateB4_StopPendingOrAbsentSteeringNeverDefersDone(t *testing.T) {
	for _, tc := range []struct {
		name          string
		stopPending   bool
		absentRuntime bool
		wantDeferred  bool
		wantReleases  int
	}{
		{name: "eligible_control", wantDeferred: true, wantReleases: 1},
		{name: "accepted_execution_stop", stopPending: true},
		{name: "absent_steering", absentRuntime: true},
	} {
		for _, canceled := range []bool{false, true} {
			end := "natural_end"
			if canceled {
				end = "interrupted_end"
			}
			t.Run(tc.name+"/"+end, func(t *testing.T) {
				const sid = "gate-b4-eligible-queue"
				queue := newSteeringQueue()
				require.NoError(t, queue.pushScope(sid, providers.Message{Role: "user", Content: "queued follow-up"}))
				require.False(t, queue.scopeEmpty(sid), "instrument: an otherwise eligible continuation exists")
				al := &AgentLoop{steering: queue}
				selected := session.StopSelection{SessionID: sid}
				disposition := newExecutionDisposition(claimForStopEffect(sid, selected.Effect))
				if tc.stopPending {
					require.NoError(t, disposition.bindStop(selected, nil))
				}
				require.Equal(t, tc.stopPending, executionStopPending(disposition))
				ts := &turnState{opts: processOptions{executionDisposition: disposition}}
				require.False(t, ts.stopRequested.Load(), "the separate stopRequested guard must not mask this case")
				transport := &releaseTrackingMockStreamer{}
				ts.setLastStreamer(transport)
				if tc.absentRuntime {
					al.steering = nil
				}
				var runErr error
				if canceled {
					runErr = context.Canceled
				}
				got := al.deferExternalLiveDoneForResume(ts, sid, runErr)
				assert.Equal(t, tc.wantDeferred, got, "only an eligible active runtime may hold completion")
				assert.Equal(t, tc.wantDeferred, ts.externalLiveDoneDeferred.Load(), "a refused deferral must not leave a hidden held-done flag")
				assert.Equal(t, tc.wantReleases, transport.releaseCalled, "release ownership only when a continuation can follow")
				assert.Zero(t, transport.finalizeCalled, "eligibility itself does not emit or consume the completion")
				assert.False(t, queue.scopeEmpty(sid), "checking deferral never consumes the pending instruction")
			})
		}
	}
}
