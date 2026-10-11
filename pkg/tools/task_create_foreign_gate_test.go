// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Final-gate fixes for create_task (S3 M5/M6/M8): a foreign delivery must not
// route its result back to the creator, must be authorized against the
// DESTINATION workspace's graph, and a failed paired-goal write must not leave
// the saved task behind.

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

const (
	tgOwnWS     = "01JXTGOWNWORKSPACE0000001"
	tgForeignWS = "01JXTGFOREIGNWORKSPACE00001"
)

// tgSeedWorkspace writes a workspace record with the given core team.
func tgSeedWorkspace(t *testing.T, home, id string, coreTeam ...string) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rec := map[string]any{"id": id, "name": id}
	if len(coreTeam) > 0 {
		rec["core_team"] = coreTeam
	}
	data, _ := json.Marshal(rec)
	if err := os.WriteFile(filepath.Join(dir, id+".json"), data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

type tgFixture struct {
	home  string
	store *task.Store
	tool  *TaskCreateTool
	seen  []string // workspace id the delegation gate was asked about, per call
}

func newTGFixture(t *testing.T) *tgFixture {
	t.Helper()
	f := &tgFixture{home: t.TempDir()}
	f.store = task.New(filepath.Join(f.home, "tasks"))
	f.tool = NewTaskCreateTool(f.store)
	f.tool.SetHome(f.home)
	f.tool.SetDelegationDenyChecker(func(ctx context.Context, _ string) *DelegationDenial {
		f.seen = append(f.seen, ToolWorkspaceID(ctx))
		return nil
	})
	f.tool.SetInitiatorFn(func(context.Context) *task.Initiator {
		return &task.Initiator{AgentID: "creator-a", SessionID: "sess-creator"}
	})
	return f
}

func (f *tgFixture) ctx() context.Context {
	ctx := WithAgentID(context.Background(), "creator-a")
	ctx = WithWorkspaceID(ctx, tgOwnWS)
	ctx = WithToolContext(ctx, "telegram", "chat-42")
	ctx = WithToolCallID(ctx, "call-77")
	ctx = WithTranscriptSessionID(ctx, "sess-creator")
	return ctx
}

func (f *tgFixture) args(agent, ws string) map[string]any {
	a := map[string]any{
		"title":    "work",
		"prompt":   "do it",
		"agent_id": agent,
		"criteria": validCriteriaArg(),
		"dod":      validDoDArg(),
	}
	if ws != "" {
		a["workspace_id"] = ws
	}
	return a
}

// onlyChild returns the single task other than the seeded parent: with
// parentID set, the one whose ParentTaskID is parentID; with "", the one that
// has no parent link and is not the seeded parent (the foreign delivery).
func (f *tgFixture) onlyChild(t *testing.T, parentID string) task.Task {
	t.Helper()
	all, err := f.store.List(task.Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var out []task.Task
	for _, tk := range all {
		switch {
		case parentID != "" && tk.ParentTaskID == parentID:
			out = append(out, tk)
		case parentID == "" && tk.WorkspaceID == tgForeignWS:
			out = append(out, tk)
		}
	}
	if len(out) != 1 {
		t.Fatalf("want exactly 1 matching task, got %d", len(out))
	}
	return out[0]
}

// S3 M5: a foreign delivery must carry no route back to the creator. The
// own-workspace create is the control: the same context DOES stamp the route,
// so an empty route on the foreign task is not an artefact of the fixture.
func TestTaskCreate_ForeignDeliveryCarriesNoCreatorRoute(t *testing.T) {
	f := newTGFixture(t)
	tgSeedWorkspace(t, f.home, tgOwnWS, "creator-a")
	tgSeedWorkspace(t, f.home, tgForeignWS, "assignee-b")

	parent := seedTask(t, f.store, "creator-a", "creator-a", tgOwnWS)
	ownArgs := f.args("assignee-b", "")
	ownArgs["parent_task_id"] = parent.ID
	own := f.tool.Execute(f.ctx(), ownArgs)
	if own.IsError {
		t.Fatalf("control create failed: %s", own.ForLLM)
	}
	ctl := f.onlyChild(t, parent.ID)
	if ctl.ParentTaskID != parent.ID || ctl.SourceChannel != "telegram" || ctl.SourceChatID != "chat-42" || ctl.OriginCallID != "call-77" || ctl.Initiator == nil {
		t.Fatalf("instrument: own-workspace create must stamp the route, got channel=%q chat=%q call=%q initiator=%v",
			ctl.SourceChannel, ctl.SourceChatID, ctl.OriginCallID, ctl.Initiator)
	}
	if _, err := f.store.Delete(ctl.ID); err != nil {
		t.Fatalf("cleanup control: %v", err)
	}

	a := f.args("assignee-b", tgForeignWS)
	a["parent_task_id"] = parent.ID
	res := f.tool.Execute(f.ctx(), a)
	if res.IsError {
		t.Fatalf("foreign create failed: %s", res.ForLLM)
	}
	got := f.onlyChild(t, "")
	if got.ParentTaskID != "" {
		t.Errorf("foreign task must not be parented under the creator's task; got parent=%q", got.ParentTaskID)
	}
	if got.SourceChannel != "" || got.SourceChatID != "" {
		t.Errorf("foreign task must not route its result to the creator's chat; got channel=%q chat=%q", got.SourceChannel, got.SourceChatID)
	}
	if got.OriginCallID != "" || got.OriginSessionID != "" {
		t.Errorf("foreign task must not carry the creator's call/session; got call=%q session=%q", got.OriginCallID, got.OriginSessionID)
	}
	if got.Initiator != nil {
		t.Errorf("foreign task must not carry the creator as initiator (a later automatic start would re-authorize the creator in the destination); got %+v", got.Initiator)
	}
}

// S3 M6: the delegation gate for a foreign delivery must be evaluated against
// the destination workspace's graph, not the caller's turn workspace.
func TestTaskCreate_ForeignDeliveryAuthorizedAgainstDestinationGraph(t *testing.T) {
	f := newTGFixture(t)
	tgSeedWorkspace(t, f.home, tgOwnWS, "creator-a")
	tgSeedWorkspace(t, f.home, tgForeignWS, "assignee-b")

	if res := f.tool.Execute(f.ctx(), f.args("assignee-b", tgForeignWS)); res.IsError {
		t.Fatalf("foreign create failed: %s", res.ForLLM)
	}
	if len(f.seen) != 1 || f.seen[0] != tgForeignWS {
		t.Fatalf("foreign delivery must be gated on the destination graph %q, gate saw %v", tgForeignWS, f.seen)
	}

	f.seen = nil
	if res := f.tool.Execute(f.ctx(), f.args("assignee-b", "")); res.IsError {
		t.Fatalf("own create failed: %s", res.ForLLM)
	}
	if len(f.seen) != 1 || f.seen[0] != tgOwnWS {
		t.Fatalf("own-workspace create must keep the turn workspace %q, gate saw %v", tgOwnWS, f.seen)
	}
}

// S3 M6: the same-agent exemption of the task gate ("reassigning to yourself
// is not delegation") must not let an agent drop work on a board whose team it
// does not belong to. The target is the CALLER, so only membership can refuse.
func TestTaskCreate_ForeignSelfAssignmentNeedsDestinationMembership(t *testing.T) {
	f := newTGFixture(t)
	tgSeedWorkspace(t, f.home, tgOwnWS, "creator-a")
	tgSeedWorkspace(t, f.home, tgForeignWS, "someone-else")

	res := f.tool.Execute(f.ctx(), f.args("creator-a", tgForeignWS))
	if !res.IsError {
		t.Fatalf("a non-member must not deliver a task to the destination board, got: %s", res.ForLLM)
	}
	if all, _ := f.store.List(task.Filter{}); len(all) != 0 {
		t.Fatalf("a refused delivery must write nothing, found %d task(s)", len(all))
	}

	tgSeedWorkspace(t, f.home, tgForeignWS, "someone-else", "creator-a")
	if res := f.tool.Execute(f.ctx(), f.args("creator-a", tgForeignWS)); res.IsError {
		t.Fatalf("a destination team member may deliver to its own board: %s", res.ForLLM)
	}
}

// S3 M8: when the paired goal cannot be written, the task already saved must
// not survive — a task without a goal can never finish via goal_claim, and a
// retry would duplicate it.
func TestTaskCreate_GoalWriteFailureRollsBackTask(t *testing.T) {
	f := newTGFixture(t)
	tgSeedWorkspace(t, f.home, tgOwnWS, "creator-a")
	// A regular file where the goal store needs its "entities" directory makes
	// every goal write fail while the task store (<home>/tasks) stays healthy.
	if err := os.WriteFile(filepath.Join(f.home, "entities"), []byte("x"), 0o600); err != nil {
		t.Fatalf("plant blocker: %v", err)
	}

	res := f.tool.Execute(f.ctx(), f.args("assignee-b", ""))
	if !res.IsError {
		t.Fatalf("instrument: the goal write was meant to fail, create returned: %s", res.ForLLM)
	}
	if all, err := f.store.List(task.Filter{}); err != nil || len(all) != 0 {
		t.Fatalf("a failed goal write must roll the task back; err=%v tasks=%d", err, len(all))
	}
	if strings.Contains(res.ForLLM, "could not be removed") {
		t.Errorf("the task WAS removed (asserted above); the error must not claim otherwise: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "was created") {
		t.Errorf("error must not claim the task exists once it is rolled back: %s", res.ForLLM)
	}
}
