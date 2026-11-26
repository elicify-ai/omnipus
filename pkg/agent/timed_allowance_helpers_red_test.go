//go:build goolm && stdjson

package agent

// Order 60 — TestTimedAllowance_HelpersAndOtherAgents (C-26 helper rule;
// S-120). Oracle: skills-tools-redesign-spec.md —
//   C-26 "How a timed allowance reaches a helper run": because the key is
//        the agent, not the session, a helper the agent starts for itself
//        looks up the same allowances with no copy at spawn.
//        inheritSessionPermissions therefore stops copying grants
//        (ApprovalGrantStore.InheritFrom is removed or reduced to
//        egress-token bookkeeping) and keeps copying only the per-chat
//        Auto-approve modifier (SessionModes().InheritFrom, unchanged).
//   C-26 Covers: helpers the agent starts for itself are covered (same
//        agent id); a helper of another agent, or a delegation to another
//        agent, is not covered and asks as usual.
//
// Legs 1 and 2 pin that the same agent shares one allowance with no copy at
// spawn (CHECK round 1 finding 8's corrected oracle); leg 3 is the control
// C-26 keeps (the Auto-approve modifier inheritance is unchanged); leg 4
// uses real inheritance/store effects for same and other agents. F17
// reconciliation moved order 52 to the gateway package and removed its
// whole-store callbacks.
//
// Part H (2026-11-26) replaced the last placeholder, the loud BLOCKED leg
// "actual timed self-helper lifetime", with a real test: a timed answer
// (RecordFor, 3 hours) on the loop's own store, the loop's own clock input
// (SetAllowanceClock), the real spawn path (inheritSessionPermissions) for
// Mia's own helper, for a helper of Jim and for a helper spawned after the
// end, and the lookups before, one nanosecond before and at the end. The
// legacy no-end Record calls in legs 1, 2 and 4 stay as the scope probes
// they always were: they are not a timed answer.
import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimedAllowance_HelpersAndOtherAgents(t *testing.T) {
	newLoop := func(t *testing.T) *AgentLoop {
		t.Helper()
		tmp := t.TempDir()
		cfg := &config.Config{
			Agents: config.AgentsConfig{
				Defaults: config.AgentDefaults{
					Home:              tmp,
					DefaultModel:      config.DefaultModel{Model: "allowance-helper-test-model"},
					MaxTokens:         4096,
					MaxToolIterations: 2,
				},
				List: []config.AgentConfig{{ID: "mia", Home: tmp}},
			},
		}
		return mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &capturingProvider{})
	}

	// CHECK round 1 finding 8: "no copy at spawn" is a storage/operation
	// truth, not a negative lookup — C-26's key is the agent, so Mia's
	// self-helper and every other Mia session see the SAME allowance with no
	// copy, and the corrected red is the fresh-session lookup (today's
	// session-keyed store needs the spawn copy to cover anyone). The real
	// spawn path stays driven below (leg 1a), and the Covers rule
	// (self-helper covered, another agent's helper asks) stays in leg 3.
	t.Run("C-26: a self-helper spawn drives the real spawn path and the child is covered", func(t *testing.T) {
		al := newLoop(t)
		store := al.ApprovalGrants()
		args := map[string]any{"task_id": "t1"}
		require.True(t, store.Record("parent-sess", "mia", "delete_task", args),
			"setup: Mia's chat holds a grant")

		al.inheritSessionPermissions("parent-sess", "mia", "child-sess", "mia")

		require.True(t, store.IsAllowed("child-sess", "mia", "delete_task", args),
			"C-26 Covers: helpers the agent starts for itself are covered — Mia's helper "+
				"runs with no card")
	})

	t.Run("C-26: the same agent shares one allowance — a fresh Mia session sees it with no copy", func(t *testing.T) {
		al := newLoop(t)
		store := al.ApprovalGrants()
		args := map[string]any{"task_id": "t1"}
		require.True(t, store.Record("parent-sess", "mia", "delete_task", args),
			"setup: Mia's chat holds a grant")

		// No spawn, no inheritance, no copy: a second Mia session looks up
		// the same agent-keyed allowance directly (C-26 "because the key is
		// the agent, not the session"). Today's session-keyed store misses
		// here — that miss is the red, and GREEN's agent-keyed store covers
		// it without any per-session record.
		require.True(t, store.IsAllowed("fresh-mia-sess", "mia", "delete_task", args),
			"C-26: the allowance key is the agent — every Mia session shares it with no "+
				"copy at spawn; today's session-keyed store hides it from any session that "+
				"did not inherit")
	})

	t.Run("control: the per-chat Auto-approve modifier is still inherited", func(t *testing.T) {
		al := newLoop(t)
		al.SessionModes().Set("parent-sess-2", true)

		al.inheritSessionPermissions("parent-sess-2", "mia", "child-sess-2", "mia")

		v, ok := al.SessionModes().Get("child-sess-2")
		require.True(t, ok, "C-26: SessionModes().InheritFrom is unchanged — the child resolves Auto like its parent")
		require.True(t, v, "C-26: the copied modifier is the parent's on value")
	})

	t.Run("C-26: real inheritance never gives Mia's grant to Jim", func(t *testing.T) {
		al := newLoop(t)
		store := al.ApprovalGrants()
		args := map[string]any{"task_id": "mia-task"}
		require.True(t, store.Record("source-mia", "mia", "delete_task", args), "legacy scope control, not a timed answer")
		require.True(t, store.IsAllowed("source-mia", "mia", "delete_task", args), "actual source grant exists")
		al.inheritSessionPermissions("source-mia", "mia", "child-jim", "jim")
		require.False(t, store.IsAllowed("child-jim", "jim", "delete_task", args), "C-26: actual other-agent inheritance must not copy a consent Mia received")
		require.True(t, store.IsAllowed("source-mia", "mia", "delete_task", args), "the source agent keeps its own record")
	})
	// S-120 and C-26 "Covers"/"Length"/"End": Mia's answer "Allow for 3 hours" covers her own helper (same
	// agent id) for exactly those three hours; a helper of another agent is not covered; a helper started after
	// the end is not covered. Ends are literals from C-26's length (3 hours on the injected clock).
	t.Run("C-26 actual timed self-helper lifetime", func(t *testing.T) {
		al := newLoop(t)
		clock := &allowanceTestClock{start: time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)}
		al.SetAllowanceClock(clock.wallNow, clock.elapsedNow)
		store := al.ApprovalGrants()
		answered := map[string]any{"task_id": "t1"}
		other := map[string]any{"task_id": "t2"}
		end, recorded := store.RecordFor("mia", "delete_task", answered, 3*time.Hour)
		require.True(t, recorded, "the store takes Mia's timed answer for an ordinary tool")
		require.Equal(t, time.Date(2026, time.November, 22, 15, 0, 0, 0, time.UTC), end, "instrument: three hours on the injected clock")

		// The real spawn path, once for Mia's own helper and once for a helper of Jim.
		al.inheritSessionPermissions("parent-sess", "mia", "self-helper", "mia")
		al.inheritSessionPermissions("parent-sess", "mia", "jim-helper", "jim")

		require.True(t, store.IsAllowed("self-helper", "mia", "delete_task", other),
			"S-120: Mia's own helper runs the whole tool, other arguments too, with no card")
		require.True(t, store.IsAllowed("fresh-mia-chat", "mia", "delete_task", answered), "a fresh Mia chat shares the allowance")
		require.False(t, store.IsAllowed("jim-helper", "jim", "delete_task", answered), "a helper of another agent asks as usual")

		clock.advance(3*time.Hour - time.Nanosecond)
		assert.True(t, store.IsAllowed("self-helper", "mia", "delete_task", other), "one nanosecond before the end the helper is still covered")
		assert.False(t, store.IsAllowed("jim-helper", "jim", "delete_task", answered), "Jim's helper is never covered")

		clock.advance(time.Nanosecond)
		assert.False(t, store.IsAllowed("self-helper", "mia", "delete_task", other), "at the end the running helper is no longer covered")
		al.inheritSessionPermissions("parent-sess", "mia", "late-helper", "mia")
		assert.False(t, store.IsAllowed("late-helper", "mia", "delete_task", answered), "a helper started after the end asks")
	})
}
