// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Oracle: #1026's owner exception fails closed on a real store read error.
// This complements the tool test's caller-matching value+error result with
// production wiring and an actual filesystem failure (no permission/root trap).
func TestGoalClaim_TaskSessionReadErrorFromStoreFailsClosed(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	worker, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native worker is not registered")
	}
	_, sessionID := newGoalTestSession(t, al, worker.ID)
	seedActiveGoalRecord(t, sessionID, "ship the report", nil, nil)
	tk := &task.Task{
		Title: "read-failure control", Prompt: "Ship the report.", Action: task.ActionLLM,
		AgentID: worker.ID, Priority: 3, WorkspaceID: "default", Status: task.StatusInProgress,
		SessionID: sessionID,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create a real task with a matching binding: %v", err)
	}
	access := agentLoopGoalRecordAccess{al: al}
	if bound, err := access.ReadTaskSessionID(tk.ID); err != nil || bound != sessionID {
		t.Fatalf("healthy production access = %q / %v, want durable binding %q", bound, err, sessionID)
	}
	claim, ok := worker.Tools.Get(tools.GoalClaimToolName)
	if !ok {
		t.Fatal("goal_claim is not registered on the real worker")
	}
	args := map[string]any{"status": "met", "evidence": "report checked"}
	for _, depth := range []int{1, 2} {
		ctx := tools.WithAgentID(context.Background(), worker.ID)
		ctx = tools.WithTranscriptSessionID(ctx, sessionID)
		ctx = tools.WithRunningTaskID(tools.WithDelegationDepth(ctx, depth), tk.ID)
		res := claim.Execute(ctx, args)
		if res.IsError || res.Err != nil {
			t.Fatalf("depth %d healthy production binding must claim: %q / %v", depth, res.ForLLM, res.Err)
		}
	}

	path := filepath.Join(al.taskStore.Dir(), tk.ID+".json")
	if err := os.Remove(path); err != nil {
		t.Fatalf("replace task file with unreadable directory: %v", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("create directory at task file path: %v", err)
	}
	_, readErr := os.ReadFile(path)
	var wantPathErr *os.PathError
	if !errors.As(readErr, &wantPathErr) {
		t.Fatalf("filesystem fault instrument = %v, want a real PathError", readErr)
	}

	for _, depth := range []int{1, 2} {
		t.Run(fmt.Sprintf("depth_%d", depth), func(t *testing.T) {
			ctx := tools.WithAgentID(context.Background(), worker.ID)
			ctx = tools.WithTranscriptSessionID(ctx, sessionID)
			ctx = tools.WithRunningTaskID(tools.WithDelegationDepth(ctx, depth), tk.ID)
			res := claim.Execute(ctx, args)
			if !res.IsError {
				t.Fatalf("actual binding read failure must refuse, got success %q", res.ForLLM)
			}
			var gotPathErr *os.PathError
			if !errors.As(res.Err, &gotPathErr) {
				t.Fatalf("refusal cause = %v, want the retained filesystem PathError", res.Err)
			}
			if gotPathErr.Path != path || gotPathErr.Op != wantPathErr.Op || !errors.Is(res.Err, wantPathErr.Err) {
				t.Errorf("filesystem cause = %+v, want same path, operation and underlying cause as %+v", gotPathErr, wantPathErr)
			}
			if !strings.Contains(res.ForLLM, wantPathErr.Error()) {
				t.Errorf("visible refusal = %q, want the real filesystem cause %q", res.ForLLM, wantPathErr.Error())
			}
		})
	}
}
