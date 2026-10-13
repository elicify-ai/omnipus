// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// create_task stores the creating agent as the task's Initiator (with the
// budget it had inherited) so later automatic starts are authorized against it;
// without a wired producer (a person's creation path) nothing is stored.
func TestTaskCreateTool_StoresInitiator(t *testing.T) {
	t.Parallel()
	zero := 0
	for _, wired := range []bool{true, false} {
		store := task.New(t.TempDir())
		tool := NewTaskCreateTool(store)
		tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })
		if wired {
			tool.SetInitiatorFn(func(context.Context) *task.Initiator {
				return &task.Initiator{AgentID: "caller", Depth: 2, Inherited: &zero}
			})
		}
		ctx := WithWorkspaceID(WithAgentID(context.Background(), "caller"), "ws-ini")
		res := tool.Execute(ctx, map[string]any{
			"title": "t", "prompt": "do it", "agent_id": "agent-b",
			"criteria": validCriteriaArg(), "dod": validDoDArg(),
		})
		if res.IsError {
			t.Fatalf("create_task: %s", res.ForLLM)
		}
		tasks, err := store.List(task.Filter{WorkspaceID: "ws-ini"})
		if err != nil || len(tasks) != 1 {
			t.Fatalf("list: %v / %d", err, len(tasks))
		}
		got := tasks[0].Initiator
		if !wired {
			if got != nil {
				t.Fatalf("unwired producer must store no initiator, got %+v", got)
			}
			continue
		}
		if got == nil || got.AgentID != "caller" || got.Depth != 2 || got.Inherited == nil || *got.Inherited != 0 {
			t.Fatalf("stored Initiator = %+v, want caller/depth 2/inherited 0", got)
		}
	}
}
