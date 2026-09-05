// Omnipus — ADR-074 D2/D3b tool-layer inference tests (sysagent half)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// criteria_infer_adr074_test.go — ADR-074 D2/D3b, judgment-first-criteria-
// spec tests #3 (the ADR-049 D2-rule-5 all-check bash-policy gate fires on
// INFERRED kinds in create_task_in_workspace) and #2b (behavior round-trips
// end-to-end with kind omitted), plus test #5's required-relaxed half.
// External-package test: everything here drives the public tool surface (the
// unexported parser twin's own decode tests live in the internal
// criteria_behavior_adr074_test.go).

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// kindOmittedCheckCriteriaWS is an all-check criteria set that never says
// "check": the kind must be INFERRED for the D2-rule-5 gate to see it.
func kindOmittedCheckCriteriaWS() []any {
	return []any{
		map[string]any{
			"text": "tests pass",
			"check": map[string]any{
				"command":            "go test ./...",
				"expected_exit_code": float64(0),
			},
		},
	}
}

// TestCreateTaskInWorkspace_Gate_FiresOnInferredKind is spec test #3
// (create_task_in_workspace half, ADR required-test #1): a kind-OMITTED
// all-check criteria set against an assignee whose bash policy is deny must
// be rejected by the D2-rule-5 gate — inference runs in the parser twin,
// BEFORE the gate.
func TestCreateTaskInWorkspace_Gate_FiresOnInferredKind(t *testing.T) {
	baseArgs := func(criteria []any) map[string]any {
		return map[string]any{
			"name":         "gated",
			"workspace_id": testWorkspaceID,
			"agent_id":     "assignee",
			"criteria":     criteria,
		}
	}
	ctx := tools.WithAgentID(context.Background(), "caller-agent")

	t.Run("deny_rejected", func(t *testing.T) {
		deps, home := newTestDepsWithHome(t)
		seedWorkspace(t, home, testWorkspaceID)
		deps.ResolveBashPolicy = func(string) (string, bool) { return "deny", true }
		create := systools.NewTaskCreateTool(deps)

		res := create.Execute(ctx, baseArgs(kindOmittedCheckCriteriaWS()))
		require.True(t, res.IsError,
			"kind-omitted all-check vs bash:deny must be rejected — the gate must fire on INFERRED kinds")
		assert.Contains(t, res.ForLLM, "D2 rule 5")
	})

	t.Run("allow_accepted_and_persists_inferred_check", func(t *testing.T) {
		deps, home := newTestDepsWithHome(t)
		seedWorkspace(t, home, testWorkspaceID)
		deps.ResolveBashPolicy = func(string) (string, bool) { return "allow", true }
		create := systools.NewTaskCreateTool(deps)

		res := create.Execute(ctx, baseArgs(kindOmittedCheckCriteriaWS()))
		require.False(t, res.IsError, "kind-omitted check vs bash:allow must succeed; got: %s", res.ForLLM)

		store := task.New(filepath.Join(home, "tasks"))
		tasks, err := store.List(task.Filter{WorkspaceID: testWorkspaceID})
		require.NoError(t, err)
		require.Len(t, tasks, 1)
		require.Len(t, tasks[0].Criteria, 1)
		assert.Equal(t, task.KindCheck, tasks[0].Criteria[0].Kind,
			"persisted kind must be the INFERRED check")
	})

	t.Run("mixed_inferred_not_gated", func(t *testing.T) {
		deps, home := newTestDepsWithHome(t)
		seedWorkspace(t, home, testWorkspaceID)
		deps.ResolveBashPolicy = func(string) (string, bool) { return "deny", true }
		create := systools.NewTaskCreateTool(deps)

		criteria := append(kindOmittedCheckCriteriaWS(), map[string]any{"text": "reads well"})
		res := create.Execute(ctx, baseArgs(criteria))
		require.False(t, res.IsError,
			"mixed inferred check+prose must not hit the all-check gate; got: %s", res.ForLLM)
	})
}

// TestBehavior_EndToEnd_CreateTaskInWorkspace is spec test #2b for
// create_task_in_workspace: a kind-omitted behavior criterion round-trips
// schema -> parser twin -> task store with kind inferred and the explicit
// {0,0} "never call" pointer pair intact.
func TestBehavior_EndToEnd_CreateTaskInWorkspace(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	create := systools.NewTaskCreateTool(deps)
	ctx := tools.WithAgentID(context.Background(), "caller-agent")

	res := create.Execute(ctx, map[string]any{
		"name":         "no shell",
		"workspace_id": testWorkspaceID,
		"agent_id":     "assignee",
		"criteria": []any{map[string]any{
			"text": "never shells out",
			"behavior": map[string]any{
				"tool":      "bash",
				"min_count": float64(0),
				"max_count": float64(0),
			},
		}},
	})
	require.False(t, res.IsError, "create with kind-omitted behavior criterion failed: %s", res.ForLLM)

	store := task.New(filepath.Join(home, "tasks"))
	tasks, err := store.List(task.Filter{WorkspaceID: testWorkspaceID})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Len(t, tasks[0].Criteria, 1)
	c := tasks[0].Criteria[0]
	assert.Equal(t, task.KindBehavior, c.Kind, "kind must be the INFERRED behavior")
	require.NotNil(t, c.Behavior)
	assert.Equal(t, "bash", c.Behavior.Tool)
	require.NotNil(t, c.Behavior.MinCount, "EXPLICIT min_count 0 must survive")
	assert.Equal(t, 0, *c.Behavior.MinCount)
	require.NotNil(t, c.Behavior.MaxCount, "EXPLICIT max_count 0 must survive")
	assert.Equal(t, 0, *c.Behavior.MaxCount)
}

// TestWorkspaceCriteriaSchema_RequiredRelaxedToText is spec test #5's
// required half (D3b) for create_task_in_workspace.
func TestWorkspaceCriteriaSchema_RequiredRelaxedToText(t *testing.T) {
	params := (&systools.TaskCreateTool{}).Parameters()
	props, ok := params["properties"].(map[string]any)
	require.True(t, ok, "properties is %T", params["properties"])
	criteria, ok := props["criteria"].(map[string]any)
	require.True(t, ok, "criteria is %T", props["criteria"])
	items, ok := criteria["items"].(map[string]any)
	require.True(t, ok, "criteria.items is %T", criteria["items"])
	req, ok := items["required"].([]string)
	require.True(t, ok, "criterion items required is %T", items["required"])
	assert.Equal(t, []string{"text"}, req, "ADR-074 D3b: criterion required must relax to [text]")
}
