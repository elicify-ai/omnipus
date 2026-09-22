package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestTaskCreate_DepthBoundedByConfiguredCap proves ADR-091 D9's "one limit
// surface": the task-mode delegation-generation ceiling is the SAME
// performance.max_delegation_depth every other reader uses (resolved through
// resolveEffectiveDelegationDepth), not a separate hardcoded constant (the
// former `maxTaskDepth = 10`).
//
// The discriminator is a configured cap of 2: a create at generation 1 (child
// becomes generation 2, at the ceiling) must be allowed, while a create at
// generation 2 (child would become 3) must be rejected. Undern the old
// hardcoded 10 every such nested create passed, so this test fails against
// that regression. Both cases are self-assignment (agent_id == caller), which
// is exempt from the delegation deny checker, so the depth bound is the ONLY
// gate in play.
func TestTaskCreate_DepthBoundedByConfiguredCap(t *testing.T) {
	const (
		agentID = "jim"
		wsID    = "01JWTASKDEPTH0000000001"
	)
	seedWorkspaceGraph(t, wsID, true, []graphEdge{
		edge("jim", "worker", []string{"task"}, nil),
	})

	al, _ := wireTestLoopWithGraphAndMaxDepth(t, agentID, 2) // cap = 2, not 10

	inst, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || inst == nil {
		t.Fatalf("agent %q not registered", agentID)
	}
	tool, ok := inst.Tools.Get("create_task")
	if !ok {
		t.Fatal("create_task tool not registered on agent")
	}

	execute := func(depth int, title string) *tools.ToolResult {
		ctx := tools.WithAgentID(context.Background(), agentID)
		ctx = tools.WithDelegationDepth(ctx, depth)
		return tool.Execute(ctx, map[string]any{
			"title":    title,
			"prompt":   "delegated work",
			"agent_id": agentID, // SELF — exempt from the deny checker
			"criteria": []any{map[string]any{"kind": "prose", "text": "the thing is done"}},
			"dod":      []any{map[string]any{"kind": "prose", "text": "nothing else broke"}},
		})
	}

	// Child at the ceiling (generation 2) is allowed.
	if res := execute(1, "within-cap"); res.IsError {
		t.Fatalf("create at generation 2 (<= cap 2) must be allowed, got: %s", res.ForLLM)
	}

	// Child past the ceiling (generation 3) is rejected.
	res := execute(2, "past-cap")
	if !res.IsError {
		t.Fatal("create past the configured depth cap must be rejected")
	}
	if !strings.Contains(res.ForLLM, "maximum task delegation depth") {
		t.Fatalf("expected depth-ceiling rejection, got: %s", res.ForLLM)
	}

	// The rejected task must not have been persisted.
	rows, err := GetTaskStore(al).List(task.Filter{WorkspaceID: wsID})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	for _, tk := range rows {
		if tk.Title == "past-cap" {
			t.Fatal("a rejected over-cap task must not be persisted")
		}
	}
}