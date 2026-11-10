package session

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
)

// Oracle: accepted I1 option B and the founder's sixth display value. A root
// restart is Interrupted; a human Stop and the existing helper Stop stay Stopped.
func TestI1RestartProjection(t *testing.T) {
	cases := []struct {
		name string
		rec  LifecycleRecord
		want generated.SessionLifecycleState
	}{
		{"root_restart", LifecycleRecord{State: LifecycleStopped, StopNote: &StopNote{Cause: StopCauseRestart}}, generated.SessionLifecycleStateInterrupted},
		{"human_stop", LifecycleRecord{State: LifecycleStopped, StopNote: &StopNote{Cause: StopCauseStop}}, generated.SessionLifecycleStateStopped},
		{"helper_restart", LifecycleRecord{State: LifecycleStopped, SteeredBy: &SteeredBy{SteeringSessionID: "parent"}, StopNote: &StopNote{Cause: StopCauseRestart}}, generated.SessionLifecycleStateStopped},
		{"genuine_failure", LifecycleRecord{State: LifecycleFailed, FailedReason: "provider_error"}, generated.SessionLifecycleStateFailed},
		{"legacy_restart_failure", LifecycleRecord{State: LifecycleFailed, FailedReason: FailedReasonInterrupted}, generated.SessionLifecycleStateInterrupted},
		{"completed_with_old_note", LifecycleRecord{State: LifecycleCompleted, StopNote: &StopNote{Cause: StopCauseRestart}}, generated.SessionLifecycleStateDone},
		{"running_with_old_note", LifecycleRecord{State: LifecycleRunning, StopNote: &StopNote{Cause: StopCauseRestart}}, generated.SessionLifecycleStateWorking},
		{"stopped_without_note", LifecycleRecord{State: LifecycleStopped}, generated.SessionLifecycleStateStopped},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := generated.SessionLifecycleState(LifecycleRecordToDisplay(&tc.rec))
			assert.Equal(t, tc.want, got, "I1: display derives from the saved outcome, not a failure guess")
			assert.True(t, got.Valid(), "display must remain a generated, contract-valid enum value")
		})
	}
}
