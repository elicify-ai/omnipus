package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// A provider that does not promptly honor cancellation keeps the actual owner
// in flight until the fixture releases it. This is an external process fault,
// not a forged fence or a test-owned execution/disposition.
type r5RootOwnerProvider struct{ *r1CompletionProvider }

func (p *r5RootOwnerProvider) Chat(_ context.Context, messages []providers.Message, definitions []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	return p.r1CompletionProvider.Chat(context.Background(), messages, definitions, model, options)
}

func r5AdmitRoot(t *testing.T) (*AgentLoop, *session.LifecycleRecord, *r1CompletionProvider) {
	t.Helper()
	return r5AdmitRootWithAnswers(t, "partial root work", "root resumed exactly once")
}

// r5AdmitRootWithAnswers is r5AdmitRoot with the provider's full answer script
// staged BEFORE the first turn starts. A test that needs more than the default
// two answers must stage them here: appending to answers/release or replacing
// entered after Launch races the running turn's Chat call (the provider reads
// those fields without a lock once the turn is in flight).
func r5AdmitRootWithAnswers(t *testing.T, answers ...string) (*AgentLoop, *session.LifecycleRecord, *r1CompletionProvider) {
	t.Helper()
	// Existing callers intentionally exercise an unfiled ordinary root.
	return r5AdmitScopedRootWithAnswers(t, "", answers...)
}

// r5AdmitScopedRootWithAnswers binds scope at genuine root creation, before
// task history or the first provider call exists. A held request needs this
// option: a later SetMeta cannot adopt an already-used unfiled conversation.
func r5AdmitScopedRootWithAnswers(t *testing.T, workspaceID string, answers ...string) (*AgentLoop, *session.LifecycleRecord, *r1CompletionProvider) {
	t.Helper()
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	provider := &r1CompletionProvider{answers: answers, entered: make(chan int, len(answers))}
	for range answers {
		provider.release = append(provider.release, make(chan struct{}))
	}
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("registered root agent missing")
	}
	inst.Provider = &r5RootOwnerProvider{provider}
	t.Cleanup(provider.openAll)
	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		TargetAgentID: testDefaultAgentID, Task: "keep this ordinary conversation open",
		Origin: steer.Origin{Kind: steer.OriginKindChat}, Owner: "r5-owner",
		WorkspaceID: workspaceID,
	})
	if err != nil {
		t.Fatalf("real root Launch: %v", err)
	}
	dispatched, err := NewSteerLauncher(al).Dispatch(context.Background(), res.SessionID, res.Generation)
	if err != nil || dispatched.State != steer.DispatchRunning {
		t.Fatalf("real root Dispatch=%+v error=%v", dispatched, err)
	}
	r1AwaitProvider(t, provider, 0)
	rec := rootReopenedRecord(t, al, res.SessionID)
	ts := al.getActiveTurnState(rec.SessionID)
	if rec.SteeredBy != nil || rec.ExecutionID == nil || ts == nil || al.tsExecutionClaim(ts, rec.SessionID) != al.executionClaimFor(rec) {
		t.Fatalf("ordinary root has no real admission: %+v", rec)
	}
	meta, metaErr := al.GetSessionStore().GetMeta(rec.SessionID)
	if metaErr != nil {
		t.Fatalf("real root metadata: %v", metaErr)
	}
	if meta == nil || meta.WorkspaceID != workspaceID || rec.WorkspaceID != workspaceID {
		t.Fatalf("root creation scope: meta=%+v lifecycle=%+v, want workspace %q", meta, rec, workspaceID)
	}
	return al, rec, provider
}

func r5StopRoot(t *testing.T, al *AgentLoop, rec *session.LifecycleRecord) *session.LifecycleRecord {
	t.Helper()
	res, err := al.StopSession(context.Background(), StopRequest{
		SessionID: rec.SessionID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "r5-owner"},
		Channel: "webchat", HooksFor: func(string) CancelHooks { return CancelHooks{} },
	})
	if err != nil || res.RootErr != nil || len(res.Report.Unreachable) != 0 || !res.Fired {
		t.Fatalf("real root Stop=%+v error=%v", res, err)
	}
	fenced := rootReopenedRecord(t, al, rec.SessionID)
	if fenced.State != session.LifecycleRunning || fenced.Stop == nil || fenced.Stop.Generation != rec.Generation || fenced.StopEffect == nil || fenced.StopEffect.Target.RunID != rec.ExecutionID.RunID {
		t.Fatalf("real owner's in-flight fence=%+v, want selected original run", fenced)
	}
	return fenced
}
