package gateway

// UAT D2b: the chat row must stop saying Working when its turn has ended.
// Oracle: frozen ADR-20260928 Vocabulary/F0929-1/F0929-2: a final answer is
// done (stored completed); Stop all is stopped (D2/D7/D9). D8 preserves these
// outcomes after restart. No idle enum is invented. All records and execution
// identities are written by real first-human admission, not seeded by tests.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

const uatD2bFinalAnswer = "The requested root chat work is complete."

// The sole controlled boundary is the external provider. Reuse the first-human
// fixture's entry channel; unlike its stopped-provider seam, this one returns
// an actual final answer when released.
type uatD2bFinalProvider struct {
	entered chan context.Context
	release chan struct{}
	calls   atomic.Int32
}

func (p *uatD2bFinalProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	select {
	case p.entered <- ctx:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-p.release:
		return &providers.LLMResponse{Content: uatD2bFinalAnswer}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (*uatD2bFinalProvider) GetDefaultModel() string { return "uat-d2b-final-answer" }

func TestUATD2b_RootFinalAnswerShowsDoneIncludingAfterRestart(t *testing.T) {
	f, _ := newFirstHumanStopFixture(t)
	p := &uatD2bFinalProvider{entered: f.provider.entered, release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(p.release) }) }
	t.Cleanup(release)
	inst, ok := f.al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	inst.Provider = p
	root := firstHumanRootIntoProvider(t, f, "uat-d2b-normal-final")
	before, err := f.lifecycle.Load(root)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleRunning, before.State, "positive control: actual first human turn is working while its provider is blocked")
	state, _, _ := computeSessionLifecycle(f.lifecycle, root)
	require.NotNil(t, state)
	require.Equal(t, generated.SessionLifecycleStateWorking, *state, "the actual UI-state consumer must see Working during the running turn")
	release()
	// Observe actual response publication before Close joins the real worker,
	// including its output and owned execution-disposition tails. A transcript
	// value alone is not completion and must not make shutdown abort the reply.
	select {
	case reply := <-f.reader.h.msgBus.OutboundChan():
		require.Equal(t, root, reply.SessionID)
		require.Equal(t, uatD2bFinalAnswer, reply.Content, "the real root final answer must be published before testing lifecycle settlement")
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatal("SETUP: first human turn never published its final answer")
	}
	f.closeLoop()
	entries, err := f.al.GetSessionStore().ReadTranscript(root)
	require.NoError(t, err)
	var answers []string
	for _, entry := range entries {
		if entry.Role == "assistant" {
			answers = append(answers, entry.Content)
		}
	}
	require.Equal(t, []string{uatD2bFinalAnswer}, answers, "persisted final answer must exactly match the published root response")
	uatD2bRequireRootDisplay(t, session.NewLifecycleStore(f.lifecycle.Dir()), root, session.LifecycleCompleted, generated.SessionLifecycleStateDone, "after the root final answer and joined disposal")
	reopened := uatD2bRecoverReopenedStores(t, f)
	uatD2bRequireRootDisplay(t, reopened, root, session.LifecycleCompleted, generated.SessionLifecycleStateDone, "after restarting the finished root chat")
}

func TestUATD2b_StopAllRootShowsStoppedIncludingAfterRestart(t *testing.T) {
	f, _ := newFirstHumanStopFixture(t)
	f.parent = firstHumanRootIntoProvider(t, f, "uat-d2b-stop-all")
	child := f.startHelper(t, f.parent)
	for _, id := range []string{f.parent, child} {
		var err error
		f.before[id], err = f.lifecycle.Load(id)
		require.NoError(t, err)
	}
	scope := "tree"
	f.cancelFrame(t, &scope)
	f.requireStopped(t, f.parent)
	f.requireStopped(t, child)
	f.closeLoop() // Full producer/worker joins, not just a lifecycle-value poll.
	uatD2bRequireRootDisplay(t, session.NewLifecycleStore(f.lifecycle.Dir()), f.parent, session.LifecycleStopped, generated.SessionLifecycleStateStopped, "after Stop all lands and all producers join")
	reopened := uatD2bRecoverReopenedStores(t, f)
	uatD2bRequireRootDisplay(t, reopened, f.parent, session.LifecycleStopped, generated.SessionLifecycleStateStopped, "after restarting the stopped root chat")
}

func uatD2bRequireRootDisplay(t *testing.T, ls *session.LifecycleStore, id string, wantRecord session.LifecycleState, wantDisplay generated.SessionLifecycleState, stage string) {
	t.Helper()
	rec, err := ls.Load(id)
	require.NoError(t, err, "read actual durable root at %s", stage)
	state, _, _ := computeSessionLifecycle(ls, id) // The real REST/UI-state boundary.
	require.NotNil(t, state, "a root that ran has an authoritative lifecycle record")
	if rec.State != wantRecord || *state != wantDisplay {
		t.Errorf("UAT D2b: %s: root durable state=%q, computed UI lifecycle=%q; want %q/%q — the chat row must not say Working after this turn has ended", stage, rec.State, *state, wantRecord, wantDisplay)
	}
	require.Nil(t, rec.Stop, "no active stop fence may survive a landed final/Stop-all outcome")
}

func uatD2bRecoverReopenedStores(t *testing.T, f *u2ScopeFixture) *session.LifecycleStore {
	t.Helper()
	ls := session.NewLifecycleStore(f.lifecycle.Dir())
	p := &uatD2bFinalProvider{entered: make(chan context.Context, 16), release: make(chan struct{})}
	close(p.release)
	msgBus := bus.NewMessageBus()
	fresh, err := agent.NewAgentLoop(f.al.GetConfig(), msgBus, p)
	require.NoError(t, err)
	t.Cleanup(func() { fresh.Close(); msgBus.Close(); gatewaySteerCancellers.Delete(fresh) })
	require.Equal(t, f.al.GetSessionStore().BaseDir(), fresh.GetSessionStore().BaseDir(), "restart must reopen the original durable transcript store")
	fresh.SetSessionMessagingStores(f.al.GetMessageInboxStore(), ls)
	// The actual gateway boot composition mints the next persistent epoch,
	// installs real delivery/stop dependencies and executes its real recovery
	// hook, including the unfinished-Stop finisher. No test writer changes state.
	stg := &setupAndStartServicesState{ctx: context.Background(), cfg: fresh.GetConfig(),
		homePath: fresh.GetConfig().Agents.Defaults.Home, agentLoop: fresh,
		lifecycleStore: ls, runningServices: &services{}}
	require.NoError(t, stg.mintBootEpoch())
	stg.wireSteerDeps()
	require.NoError(t, stg.runningServices.SteerDeps.BootHook(context.Background()))
	require.Equal(t, int32(0), p.calls.Load(), "restart recovery must not start a finished/stopped root turn")
	return ls
}
