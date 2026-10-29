//go:build goolm && stdjson

package agent

import (
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestL1WU1GoalFollowUp_MissingCanonicalOwnerEndsFailedWithVisibleReason(t *testing.T) {
	// FR-123 / §6.4 / BDD-13.4. Chief ruling 2026-10-14: spec's
	// "Goal status error" means the existing failed pill, not a new wire
	// state, with exactly "owner agent X no longer exists" as its reason.
	al, provider := l1WU1Runtime(t)
	// Supply the real execution runtime, not a goal route or a running stamp.
	installTestSessionLauncher(t, al)
	pwWireRuntime(t, al)
	gs := goal.NewStore(config.OmnipusHomeDir())
	for _, owner := range []string{"mia", "deleted"} {
		t.Run(owner, func(t *testing.T) {
			_, sid := newGoalTestSession(t, al, owner)
			gid := activateTestGoalRecord(t, sid, "do not reassign this goal")
			_, err := gs.Update(gid, func(g *goal.Goal) error { g.SetRoute("webchat", "operator"); return nil })
			require.NoError(t, err)
			al.recordGoalRouting(sid, gid, "webchat", "operator", "agent:"+owner+":session:"+sid, owner)
			t.Cleanup(func() {
				state := goalTriggers()
				state.mu.Lock()
				delete(state.routing, sid)
				state.mu.Unlock()
			})
			collector, stop := newEventCollector(t, al)
			al.dispatchGoalAsyncFollowUp(sid, gid, "goal_continue_push", "Continue only under your canonical owner.")
			stop() // synchronise the event reader, not a timed wait
			if owner == "mia" {
				select {
				case msg := <-al.bus.InboundChan():
					assert.Equal(t, "mia", msg.AsyncOriginAgentID, "healthy owner positive control")
					assert.Equal(t, sid, msg.AsyncTranscriptSessionID)
				default:
					t.Fatal("healthy canonical goal owner must reach the actual follow-up dispatch bus")
				}
				assert.Equal(t, gen.GoalStateActive, readGoalRecord(t, gid).State)
				return
			}
			select {
			case msg := <-al.bus.InboundChan():
				t.Errorf("FR-123: stale-owner follow-up must not dispatch at all: origin=%q session=%q", msg.AsyncOriginAgentID, msg.AsyncTranscriptSessionID)
			default:
			}
			after := readGoalRecord(t, gid)
			assert.NotEqual(t, gen.GoalStateActive, after.State, "a missing owner must not leave an orphaned active goal")
			assert.Equal(t, "owner agent deleted no longer exists", after.TerminalReason)
			statuses := goalStatusPayloadsFor(collector, sid)
			require.Len(t, statuses, 1, "FR-123: the user must receive exactly one visible terminal failure, not a warning-only return")
			assert.Equal(t, "failed", statuses[0].State)
			assert.Equal(t, "owner agent deleted no longer exists", statuses[0].LatestReason)
			assert.Empty(t, provider.AllRequests(), "the follow-up cannot run under another agent")
		})
	}
}
