package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// The admitted-result copy (tool_result_events.go::emitAdmittedToolResult)
// is the handoff. The event target is not filled in by the test.
func TestShowToUser_AdmittedResultCopiesOpenTarget(t *testing.T) {
	home := realHome(t)
	t.Setenv("OMNIPUS_HOME", home)
	work := mustLibrary(t, home, "W")
	if err := os.MkdirAll(filepath.Join(work, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "notes", "plan.md"), []byte("plan"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := tools.WithWorkspaceID(tools.WithToolContext(context.Background(), "webchat", "root-show"), "W")
	ctx = tools.WithTurnWorkspaceDir(ctx, work)
	ctx = tools.WithTranscriptSessionID(ctx, "root-show")
	ctx = tools.WithToolRootChatSessionID(ctx, "root-show")
	res := tools.NewShowToUserTool(home, false, tools.NewShowToUserRateLimiter(nil)).Execute(ctx, map[string]any{"ref": "notes/plan.md"})
	if res == nil || res.IsError || res.OpenInLibrary == nil {
		t.Fatalf("tool result = %+v, want a successful open target", res)
	}
	payload := emitShowToUser(t, "root-show", res)
	if payload.IsError || payload.OpenInLibrary == nil || payload.OpenInLibrary.WorkspaceID != "W" || payload.OpenInLibrary.Path != "notes/plan.md" {
		t.Fatalf("admitted event = %+v, want the tool result's Library target (D-seam)", payload.OpenInLibrary)
	}
	if payload.OpenInLibrary.RequestID != res.OpenInLibrary.RequestID {
		t.Fatalf("event request %q, tool request %q", payload.OpenInLibrary.RequestID, res.OpenInLibrary.RequestID)
	}
}

// The child ids come from DelegationTree. The context is stamped the way
// runTurn stamps it: transcript from the turn options, root from
// withBrowserRootChatSessionID.
func TestShowToUser_SteeredTurnStampRefuses(t *testing.T) {
	home := realHome(t)
	sessions, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Close() })
	life := session.NewLifecycleStore(filepath.Join(home, "lifecycle"))
	tree := testutil.DelegationTree(t, steer.Deps{LifecycleStore: life, SessionStore: sessions}, 1)
	ts := &turnState{}
	ts.opts.TranscriptSessionID = tree.A.SessionID
	ts.transcriptSessionID = tree.A.SessionID
	ts.routingSessionID = session.RoutingSessionID(tree.Root.SessionID)
	ctx := tools.WithToolContext(context.Background(), "webchat", tree.Root.SessionID)
	ctx = tools.WithWorkspaceID(ctx, tree.Root.WorkspaceID)
	ctx = tools.WithTranscriptSessionID(ctx, ts.opts.TranscriptSessionID)
	ctx = withBrowserRootChatSessionID(ctx, ts)
	res := tools.NewShowToUserTool(home, false, tools.NewShowToUserRateLimiter(nil)).Execute(ctx, map[string]any{"ref": "notes/plan.md"})
	const want = "show_to_user: only the chat the user is in can open the Library."
	if res == nil || !res.IsError || res.ForLLM != want || res.OpenInLibrary != nil {
		t.Fatalf("stamped child result = %+v, want the worker refusal (SEC-14)", res)
	}
	payload := emitShowToUser(t, tree.A.SessionID, res)
	if !payload.IsError || payload.OpenInLibrary != nil || payload.SessionID != tree.A.SessionID {
		t.Fatalf("child event session %q target %+v, want the child session and no open (SEC-14)", payload.SessionID, payload.OpenInLibrary)
	}
}

func emitShowToUser(t *testing.T, sessionID string, res *tools.ToolResult) ToolExecEndPayload {
	t.Helper()
	bus := NewEventBus()
	sub := bus.Subscribe(4)
	al := &AgentLoop{eventBus: bus}
	ts := &turnState{transcriptSessionID: sessionID}
	ex := &agentLoopRunTurnToolsExecute{
		toolName:      tools.ShowToUserToolName,
		toolCallID:    "call-show",
		contentForLLM: res.ForLLM,
		toolResult:    res,
		rx: &agentLoopRunTurnTools{rr: &agentLoopRunTurnResponse{rq: &agentLoopRunTurnRequest{ri: &agentLoopRunTurnIteration{rf: &agentLoopRunTurnFallbacks{rt: &agentLoopRunTurn{
			al: al,
			ts: ts,
		}}}}}},
	}
	ex.emitAdmittedToolResult()
	select {
	case evt := <-sub.C:
		payload, ok := evt.Payload.(ToolExecEndPayload)
		if !ok || evt.Kind != EventKindToolExecEnd {
			t.Fatalf("event = %v %+v, want tool exec end", evt.Kind, evt.Payload)
		}
		return payload
	default:
		t.Fatal("emitAdmittedToolResult published no event")
		return ToolExecEndPayload{}
	}
}

func realHome(t *testing.T) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func mustLibrary(t *testing.T, home, id string) string {
	t.Helper()
	work, err := workspace.SafeWorkDir(home, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	return work
}
