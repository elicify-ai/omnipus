package tools

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

func TestBuildTask_PersistsOriginCallID(t *testing.T) {
	store := task.New(t.TempDir())
	tool := newProvenanceTool(t, store)
	ctx := WithWorkspaceID(WithAgentID(context.Background(), "parent-agent"), "ws-1")
	ctx = WithTranscriptSessionID(ctx, "parent-session")
	ctx = WithToolCallID(ctx, "create-call-7")
	result := tool.Execute(ctx, map[string]any{
		"title": "delegated task", "prompt": "do it", "agent_id": "worker",
		"criteria": validCriteriaArg(), "dod": validDoDArg(),
	})
	if result.IsError {
		t.Fatalf("create_task: %s", result.ForLLM)
	}
	rows, err := store.List(task.Filter{WorkspaceID: "ws-1"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("list: rows=%d err=%v", len(rows), err)
	}
	if rows[0].OriginSessionID != "parent-session" || rows[0].OriginCallID != "create-call-7" {
		t.Fatalf("origin = (%q, %q)", rows[0].OriginSessionID, rows[0].OriginCallID)
	}
}
