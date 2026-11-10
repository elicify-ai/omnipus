package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/shellrule"
	"github.com/stretchr/testify/require"
)

// Amendment R4's ordering: a refusal is only the final fallback, not a ban
// on work which an operator rule or existing exact standing grant settles.
func TestL11263B_RuleAndGrantSettledRuns(t *testing.T) {
	for _, route := range []string{"board", "plan"} {
		t.Run(route+"/operator_rule", func(t *testing.T) {
			// A literal, fully classifiable command is required by ADR-092 D3.
			// Shell expansion is deliberately absent: a blind spot still asks.
			call := l11263Call("approval-shell", "bash", map[string]any{"command": "echo 1263-rule"})
			worker := l11263Script(call, l11263Claim())
			h := newL11263Harness(t, false, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: worker}, func(cfg *config.Config) {
				cfg.Sandbox.CommandRules = []shellrule.Rule{{Action: shellrule.ActionAllow, Binary: "echo"}}
			})
			id := h.startRoute(t, route)
			h.awaitTask(t, id)
			result, ok := worker.result("approval-shell")
			require.True(t, ok, "worker must receive the real shell result")
			require.Equal(t, "1263-rule", strings.TrimSpace(result), "operator-settled command must actually execute, not return a refusal quoting its command")
			require.NotContains(t, result, "permission_denied")
		})
		t.Run(route+"/standing_grant", func(t *testing.T) {
			gate := newL11263Latch()
			worker := l11263Script(l11263Write(), l11263Claim())
			worker.steps[0].wait = gate.ch
			h := newL11263Harness(t, false, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: worker})
			t.Cleanup(gate.release)
			id := h.startRoute(t, route)
			current, err := h.api.taskStore.Get(id)
			require.NoError(t, err)
			require.NotEmpty(t, current.SessionID)
			// Seed the real exact-grant store in the task's own session. The
			// barrier models an existing authorization before its first call;
			// neither the policy resolver nor grant lookup is replaced.
			require.True(t, h.api.agentLoop.ApprovalGrants().Record(current.SessionID, l11263Agent, "write_file", l11263Write().Arguments))
			gate.release()
			h.awaitTask(t, id)
			data, err := os.ReadFile(filepath.Join(h.workDir, l11263File))
			require.NoError(t, err)
			require.Equal(t, l11263Content, string(data), "standing grant must be consulted before unattended refusal")
		})
	}
}

// R3/R4: Auto on is not approval for an ask-list tool. Use the actual
// destructive tool against a real disposable task; denial must preserve it.
func TestL11263B_AutoDoesNotApproveAskListTaskAndPlan(t *testing.T) {
	for _, route := range []string{"board", "plan"} {
		t.Run(route, func(t *testing.T) {
			gate := newL11263Latch()
			worker := l11263Script()
			h := newL11263Harness(t, true, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: worker}, func(cfg *config.Config) {
				cfg.Sandbox.ToolPolicies["delete_task"] = "ask"
			})
			t.Cleanup(gate.release)
			victim := h.createTask(t, "")
			worker.steps = []l11263ModelStep{
				{call: l11263Call("ask-list", "delete_task", map[string]any{"task_id": victim.Id}), wait: gate.ch},
				{call: l11263Claim()},
			}
			id := h.startRoute(t, route)
			gate.release()
			h.awaitTask(t, id)
			result, ok := worker.result("ask-list")
			require.True(t, ok)
			var denial map[string]any
			require.NoError(t, json.Unmarshal([]byte(result), &denial), "%s", result)
			require.Equal(t, map[string]any{"error": "permission_denied", "tool": "delete_task", "reason": l11263Reason, "message": l11263Model, "permanent": true}, denial)
			preserved, err := h.api.taskStore.Get(victim.Id)
			require.NoError(t, err, "always-ask destructive tool must not execute under Auto")
			require.Equal(t, victim.Id, preserved.ID)
		})
	}
}
