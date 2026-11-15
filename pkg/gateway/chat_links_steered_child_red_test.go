package gateway

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// SEC-14 / test 10b. The session ids come from a real DelegationTree, not from
// two hand-written strings. The worker sentence is BS-6.9. The user's hub
// must not receive open_in_library.
func TestShowToUser_SteeredChildRefused(t *testing.T) {
	home := t.TempDir()
	sessions, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	t.Cleanup(func() { _ = sessions.Close() })
	life := session.NewLifecycleStore(filepath.Join(home, "lifecycle"))
	tree := testutil.DelegationTree(t, steer.Deps{
		LifecycleStore: life,
		SessionStore:   sessions,
	}, 1)
	rec, err := life.Load(tree.A.SessionID)
	if err != nil {
		t.Fatalf("load child lifecycle: %v", err)
	}
	if rec.SteeredBy == nil || rec.SteeredBy.RootSessionID != tree.Root.SessionID || tree.A.SessionID == tree.Root.SessionID {
		t.Fatalf("child is not a steered descendant of the root: child %q root %q record %+v", tree.A.SessionID, tree.Root.SessionID, rec.SteeredBy)
	}

	tool := tools.NewShowToUserTool(home, false, tools.NewShowToUserRateLimiter(nil))
	ctx := tools.WithWorkspaceID(context.Background(), tree.Root.WorkspaceID)
	ctx = tools.WithToolContext(ctx, "webchat", tree.Root.SessionID)
	ctx = tools.WithTranscriptSessionID(ctx, tree.A.SessionID)
	ctx = tools.WithToolRootChatSessionID(ctx, rec.SteeredBy.RootSessionID)
	const ref = "notes/plan.md"
	res := tool.Execute(ctx, map[string]any{"ref": ref})
	const want = "show_to_user: only the chat the user is in can open the Library."
	if res == nil || !res.IsError || res.ForLLM != want || strings.Contains(res.ForLLM, ref) || res.OpenInLibrary != nil {
		t.Fatalf("child result = %+v, want the worker refusal and no open target (BS-6.9 / SEC-14)", res)
	}

	reg := newHubRegistry("chat-links-steered")
	handler := &WSHandler{hubs: reg}
	rootHub := reg.getOrCreate(tree.Root.SessionID)
	rootConn := &wsConn{sendCh: make(chan []byte, 4), doneCh: make(chan struct{})}
	if _, ok := rootHub.bindLive(rootConn); !ok {
		t.Fatal("bindLive root")
	}
	handler.hubToolExecEnd(agent.Event{
		Kind: agent.EventKindToolExecEnd,
		Payload: agent.ToolExecEndPayload{
			ToolCallID:    "call-child",
			SessionID:     tree.Root.SessionID,
			Tool:          tools.ShowToUserToolName,
			Result:        res.ForLLM,
			IsError:       res.IsError,
			OpenInLibrary: res.OpenInLibrary,
		},
	})
	if got := chatLinksOpenFrames(chatLinksFrames(rootConn)); len(got) != 0 {
		t.Fatalf("root hub received %d open_in_library frames, want 0 (SEC-14)", len(got))
	}
}
