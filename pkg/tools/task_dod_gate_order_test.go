package tools

// task_dod_gate_order_test.go — regression coverage for review finding 12.
//
// Root cause: the GOAL-FR-021/D-C "dod is required" gate was introduced
// immediately after the criteria gate, i.e. AHEAD of the per-field checks
// (the D2-rule-5 bash gate, the delegation-depth bound, priority, due). A
// gate placed first does not merely run first — it PRE-EMPTS everything
// behind it. A caller sending priority=6 with no dod was told about dod, so
// the actual defect in their call (an out-of-range priority) never surfaced,
// and TestTaskCreate_PriorityBoundaryMatrix — the M2(b) regression test that
// exists precisely to prove a bad priority is REPORTED rather than silently
// coerced — went red with a message about a different field entirely.
//
// The fix is ordering only: D-C keeps the gate unconditional. These tests
// pin BOTH halves, so a future re-hoist of the gate fails here rather than
// re-breaking the priority matrix:
//   - a bad field value still reports THAT field, even with no dod supplied;
//   - a call that is valid in every other respect and omits dod is still
//     rejected, with the dod message (the gate did not become conditional).

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/task"
)

func newOrderTestCreateTool(t *testing.T) (*TaskCreateTool, *task.Store) {
	t.Helper()
	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store)
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })
	tool.SetBashPolicyChecker(func(string) (string, bool) { return "allow", true })
	return tool, store
}

func orderTestCtx() context.Context {
	return WithWorkspaceID(WithAgentID(context.Background(), "caller"), "ws-order")
}

// TestTaskCreate_DoDGateDoesNotPreemptFieldValidation is the finding-12
// oracle. Every case omits "dod" entirely and supplies exactly one OTHER
// defect; the reported error must name that other defect, not dod.
func TestTaskCreate_DoDGateDoesNotPreemptFieldValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		mutate  func(args map[string]any)
		wantMsg string
	}{
		{
			name:    "bad_priority_reports_priority",
			mutate:  func(a map[string]any) { a["priority"] = float64(6) },
			wantMsg: "priority must be between 1 and 5",
		},
		{
			name:    "bad_due_reports_due",
			mutate:  func(a map[string]any) { a["due"] = "not-a-date" },
			wantMsg: "must be RFC 3339",
		},
		{
			name:    "missing_title_reports_title",
			mutate:  func(a map[string]any) { delete(a, "title") },
			wantMsg: "title is required",
		},
		{
			name:    "missing_prompt_reports_prompt",
			mutate:  func(a map[string]any) { delete(a, "prompt") },
			wantMsg: "prompt is required",
		},
		{
			name:    "missing_criteria_reports_criteria",
			mutate:  func(a map[string]any) { delete(a, "criteria") },
			wantMsg: "criteria is required",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tool, store := newOrderTestCreateTool(t)
			args := map[string]any{
				"title":    "order " + c.name,
				"prompt":   "do it",
				"agent_id": "agent-b",
				"criteria": validCriteriaArg(),
				// "dod" is deliberately ABSENT on every case.
			}
			c.mutate(args)

			res := tool.Execute(orderTestCtx(), args)

			require.True(t, res.IsError, "expected a rejection, got success: %s", res.ForLLM)
			assert.Contains(t, res.ForLLM, c.wantMsg,
				"the dod gate pre-empted the %s validation: %s", c.name, res.ForLLM)
			assert.NotContains(t, strings.ToLower(res.ForLLM), "dod is required",
				"the dod gate must not answer for another field's defect: %s", res.ForLLM)

			all, err := store.List(task.Filter{WorkspaceID: "ws-order"})
			require.NoError(t, err)
			assert.Empty(t, all, "a rejected create must not persist a task")
		})
	}
}

// TestTaskCreate_DoDGateStillFiresWhenEverythingElseIsValid is the other half
// of the ordering fix: moving the gate LATER must not make it conditional.
// D-C keeps criteria + definition-of-done mandatory at creation, so a call
// that is otherwise entirely well formed and omits dod is still rejected.
func TestTaskCreate_DoDGateStillFiresWhenEverythingElseIsValid(t *testing.T) {
	t.Parallel()
	tool, store := newOrderTestCreateTool(t)

	res := tool.Execute(orderTestCtx(), map[string]any{
		"title":    "valid but no dod",
		"prompt":   "do it",
		"agent_id": "agent-b",
		"criteria": validCriteriaArg(),
		"priority": float64(3),
	})

	require.True(t, res.IsError, "a create with no dod must be rejected (D-C): %s", res.ForLLM)
	assert.Contains(t, res.ForLLM, "dod is required",
		"expected the GOAL-FR-021/D-C dod message, got: %s", res.ForLLM)

	all, err := store.List(task.Filter{WorkspaceID: "ws-order"})
	require.NoError(t, err)
	assert.Empty(t, all, "a dod-rejected create must not persist a task")
}

// TestTaskCreate_EmptyDoDListRejected pins the empty-array case to the same
// message as the absent case — an explicit `"dod": []` is not a way around
// the mandatory gate.
func TestTaskCreate_EmptyDoDListRejected(t *testing.T) {
	t.Parallel()
	tool, _ := newOrderTestCreateTool(t)

	res := tool.Execute(orderTestCtx(), map[string]any{
		"title":    "empty dod",
		"prompt":   "do it",
		"agent_id": "agent-b",
		"criteria": validCriteriaArg(),
		"dod":      []any{},
	})

	require.True(t, res.IsError, "an empty dod array must be rejected: %s", res.ForLLM)
	assert.Contains(t, res.ForLLM, "dod is required")
}
