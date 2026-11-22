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
// RED today: the store keys grants by SESSION, so a fresh Mia session
// misses the allowance her parent chat recorded (leg 2 — CHECK round 1
// finding 8's corrected oracle: the same agent shares one allowance, no
// copy at spawn). Leg 1 drives the real spawn path and stays green today
// through the very copy C-26 removes; leg 3 is the control C-26 keeps (the
// Auto-approve modifier inheritance is unchanged). Leg 4 rides order 52's
// controlled-clock seams (timed_allowance_scheduled_red_test.go) for the
// store halves; nil keeps the RED run loud (BLOCKED), never a skip.
//
// GREEN reconciliation note: C-26 re-keys ApprovalGrantStore and may drop
// the session parameter from Record/IsAllowed; leg 1's calls pin the
// copy-no-grants behaviour and are reconciled with the re-keyed store,
// never deleted.
import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"

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

	t.Run("C-26: the self-helper shares the timed allowance; another agent's helper asks", func(t *testing.T) {
		if grantTimedAllowance == nil || allowanceActiveFor == nil {
			t.Fatal("BLOCKED: the C-26 timed-allowance store is not implemented — required " +
				"by the C-26 helper rule (Covers: helpers the agent starts for itself are " +
				"covered; a helper of another agent, or a delegation to another agent, is " +
				"not covered and asks as usual)")
		}
		home := t.TempDir()
		grantTimedAllowance(t, home, "mia", "delete_task", "1h")

		require.True(t, allowanceActiveFor(t, home, "mia", "delete_task", time.Now()),
			"C-26: Mia's own helper runs with no card — same agent id, no copy at spawn")
		require.False(t, allowanceActiveFor(t, home, "jim", "delete_task", time.Now()),
			"C-26: a helper Mia delegates to Jim runs as Jim and asks as usual")
	})
}
