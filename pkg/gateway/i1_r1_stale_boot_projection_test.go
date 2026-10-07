package gateway

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracle: MUST 2. Old queued/running execution is not live after process death,
// even when boot recovery never reaches it. The fallback is read-only.
func TestI1R1StaleBootProjection(t *testing.T) {
	for _, name := range []string{"prior_running", "prior_queued", "run_early_return", "current_boot_control", "future_boot_control", "idle_control", "completed_control"} {
		t.Run(name, func(t *testing.T) {
			f := newI1R1BootFixture(t)
			state, epoch := session.LifecycleRunning, f.oldEpoch
			want := generated.SessionLifecycleStateInterrupted
			switch name {
			case "prior_queued":
				state = session.LifecycleQueued
			case "current_boot_control":
				epoch, want = f.boot.Current(), generated.SessionLifecycleStateWorking
			case "future_boot_control":
				epoch, want = f.boot.Current()+1, generated.SessionLifecycleStateWorking
			case "idle_control":
				want = generated.SessionLifecycleStateWorking
			case "completed_control":
				state, want = session.LifecycleCompleted, generated.SessionLifecycleStateDone
			}
			id := f.root(t, state, epoch)
			if name == "idle_control" {
				require.NoError(t, f.ls.Mutate(id, func(rec *session.LifecycleRecord) error { rec.ExecutionID = nil; return nil }))
			}
			before := f.journal(t, id)
			if name == "run_early_return" {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				require.ErrorIs(t, f.recovery().Run(ctx), context.Canceled, "actual early return, not a simulated stopped result")
			}
			f.display(t, id, want)
			assert.Equal(t, before, f.journal(t, id), "projection cannot dispatch or mutate lifecycle history")
		})
	}
}
