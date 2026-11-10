package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder #1217: list and detail agree on the saved prior-boot lost prompt,
// even before recovery lands. A this-boot prompt still waits for its answer.
func TestI1R1NeedsInputREST(t *testing.T) {
	for _, name := range []string{"prior_boot_before_recovery", "prior_boot_recovered", "current_boot_control", "task_prompt_control", "plan_prompt_control"} {
		t.Run(name, func(t *testing.T) {
			f := newI1R1BootFixture(t)
			epoch := f.oldEpoch
			want := generated.SessionLifecycleStateInterrupted
			if name == "current_boot_control" {
				epoch, want = f.boot.Current(), generated.SessionLifecycleStateWaitingForAnswer
			}
			if name == "task_prompt_control" || name == "plan_prompt_control" {
				want = generated.SessionLifecycleStateWaitingForAnswer
			}
			id := f.root(t, session.LifecycleRunning, epoch)
			require.NoError(t, f.ls.Mutate(id, func(rec *session.LifecycleRecord) error {
				rec.State = session.LifecycleNeedsInput
				rec.NeedsInput = &session.NeedsInput{CorrelationID: "i1-lost-approval", Reconstructable: false, TTLDeadline: time.Now().Add(time.Hour)}
				if name == "task_prompt_control" {
					rec.Origin = &session.Origin{Kind: session.OriginKindTask, TaskID: "i1-task"}
				}
				if name == "plan_prompt_control" {
					rec.OwnsPlanID = "i1-owner-plan"
				}
				return nil
			}))
			before := f.journal(t, id)
			if name != "prior_boot_before_recovery" {
				require.NoError(t, f.recovery().Run(context.Background()))
			}
			f.display(t, id, want)
			if name == "current_boot_control" || name == "prior_boot_before_recovery" || name == "task_prompt_control" || name == "plan_prompt_control" {
				assert.Equal(t, before, f.journal(t, id), "current prompt and read-only display do not mutate records")
			} else {
				rec, err := f.ls.Load(id)
				require.NoError(t, err)
				assert.Equal(t, session.LifecycleStopped, rec.State)
				assert.Nil(t, rec.NeedsInput)
			}
		})
	}
}
