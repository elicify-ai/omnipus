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
// RED today: inheritSessionPermissions still copies every grant through
// ApprovalGrantStore.InheritFrom (pkg/agent/loop_policy.go), so leg 1 fails
// on the copy it must stop making — a real behavioural red. Leg 2 is the
// control C-26 keeps (the Auto-approve modifier inheritance is unchanged).
// Leg 3 rides order 52's controlled-clock seams (timed_allowance_scheduled_
// red_test.go) for the store halves; nil keeps the RED run loud (BLOCKED),
// never a skip.
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

	t.Run("C-26: the helper spawn copies no grants", func(t *testing.T) {
		al := newLoop(t)
		store := al.ApprovalGrants()
		args := map[string]any{"task_id": "t1"}
		require.True(t, store.Record("parent-sess", "mia", "delete_task", args),
			"setup: Mia's chat holds a grant")

		al.inheritSessionPermissions("parent-sess", "mia", "child-sess", "mia")

		require.False(t, store.IsAllowed("child-sess", "mia", "delete_task", args),
			"C-26: inheritSessionPermissions copies no grants — a self-helper looks up the "+
				"same agent-keyed allowances with no copy at spawn (nothing was recorded under "+
				"the child session)")
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
