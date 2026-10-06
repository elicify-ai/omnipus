package gateway

// Frozen D2/D7 and October4 Precedence/C6. Unlike the scheduled U2 fixture,
// these roots enter through actual first HUMAN MessageFrame persistence and
// AgentLoop.Run. All lifecycle/execution/control IDs are production-minted.
import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func TestFirstHumanChatRoot_WebSocketStopScopesPreserveOriginalOwners(t *testing.T) {
	for _, scope := range []string{"session", "tree"} {
		t.Run(scope, func(t *testing.T) {
			f, boot := newFirstHumanStopFixture(t)
			f.parent = firstHumanRootIntoProvider(t, f, "first-human-main")
			// Materialize the ordinary root DURING that actual human turn by
			// using the real launcher, never a manually persisted root record.
			child := f.startHelper(t, f.parent)
			grandchild := f.startHelper(t, child)
			sibling := f.startHelper(t, f.parent)
			f.descendants = []string{child, grandchild, sibling}
			f.unrelated = firstHumanRootIntoProvider(t, f, "first-human-unrelated")
			unrelatedChild := f.startHelper(t, f.unrelated)
			ids := []string{f.parent, child, grandchild, sibling, f.unrelated, unrelatedChild}
			gstore := goal.NewStore(config.OmnipusHomeDir())
			goals := make(map[string]*goal.Goal)
			for _, id := range ids {
				goals[id] = f.seedActiveGoal(t, gstore, id)
				var err error
				f.before[id], err = f.lifecycle.Load(id)
				require.NoError(t, err)
				require.Equal(t, session.LifecycleRunning, f.before[id].State)
				require.NotNil(t, f.before[id].ExecutionID, "actual first admission must be durable: %s", id)
				require.Equal(t, boot.Current(), f.before[id].ExecutionID.BootSeq)
				require.Equal(t, 1, f.before[id].Generation, "initial real chat/helper generation")
				require.Nil(t, f.before[id].Stop)
				require.NoError(t, f.contexts[id].Err())
			}
			for _, root := range []string{f.parent, f.unrelated} {
				require.Nil(t, f.before[root].SteeredBy)
				require.Equal(t, session.OwnerScopeHuman, f.before[root].OwnerScopeKind)
				require.NotNil(t, f.before[root].Origin)
				require.Equal(t, session.OriginKindChat, f.before[root].Origin.Kind)
				meta, err := f.al.GetSessionStore().GetMeta(root)
				require.NoError(t, err)
				require.Equal(t, f.reader.wc.userID, meta.Owner, "gateway must stamp the human intake owner")
				active := f.al.GetActiveTurnBySession("agent:mia:session:" + root)
				require.NotNil(t, active, "real FIRST HUMAN root remains in its original provider during mid-turn delegation")
				require.Equal(t, agent.TurnPhaseRunning, active.Phase)
				require.Equal(t, "webchat", active.Channel)
			}
			f.cancelFrame(t, &scope) // production generated CancelFrame decoder
			require.Equal(t, context.Canceled, f.contexts[f.parent].Err(), "Stop must cancel actual first-human root provider")
			f.requireStopped(t, f.parent)
			for _, id := range f.descendants {
				if scope == "tree" {
					f.requireStopped(t, id)
				} else {
					f.assertUntouched(t, id)
				}
			}
			f.assertUntouched(t, f.unrelated)
			f.assertUntouched(t, unrelatedChild)
			for _, id := range ids {
				after, err := gstore.Get(goals[id].GoalID)
				require.NoError(t, err)
				require.Equal(t, generated.GoalStateActive, after.State, "no Stop scope ends a goal")
				require.Equal(t, goals[id], after, "goal stays exactly unchanged after selected owner settlement: %s", id)
				rec, err := f.lifecycle.Load(id)
				require.NoError(t, err)
				require.Nil(t, rec.FinalDelivery, "Stop cannot manufacture a terminal outbox: %s", id)
				require.Equal(t, f.before[id].ExecutionID, rec.ExecutionID, "no replacement/fake admission on Stop: %s", id)
			}
		})
	}
}

func newFirstHumanStopFixture(t *testing.T) (*u2ScopeFixture, *session.BootEpochStore) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{
		Gateway:     config.GatewayConfig{DevModeBypass: true},
		Agents:      config.AgentsConfig{Defaults: config.AgentDefaults{Home: home, DefaultModel: config.DefaultModel{Model: "first-human-provider"}}, List: []config.AgentConfig{{ID: "mia", Home: home}}},
		Performance: config.PerformanceConfig{MaxParallelAgents: 8, MaxDelegationDepth: 3},
		Sandbox:     config.OmnipusSandboxConfig{Mode: config.SandboxModeOff},
	}
	ensureTestWorkspaceMembership(t, cfg)
	msgBus := bus.NewMessageBus()
	p := &u2ScopeProvider{entered: make(chan context.Context, 16), release: make(chan struct{})}
	al, err := agent.NewAgentLoop(cfg, msgBus, p)
	require.NoError(t, err)
	ls := session.NewLifecycleStore(t.TempDir())
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), ls)
	h := newWSHandler(msgBus, al, "")
	msgBus.SetStreamDelegate(h)
	stg := &setupAndStartServicesState{ctx: context.Background(), cfg: cfg, homePath: home, agentLoop: al, lifecycleStore: ls, runningServices: &services{}, wsHandler: h}
	require.NoError(t, stg.mintBootEpoch(), "genuine production mint before any human admission")
	stg.wireSteerDeps()
	initial, err := ls.List(session.LifecycleFilter{})
	require.NoError(t, err)
	require.Empty(t, initial, "FIRST HUMAN setup must have NO preexisting lifecycle records")
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(ctx) }()
	var closeOnce sync.Once
	closeLoop := func() { closeOnce.Do(func() { cancel(); close(p.release); al.Close(); msgBus.Close() }) }
	t.Cleanup(func() {
		closeLoop()
		select {
		case err := <-runDone:
			require.True(t, err == nil || errors.Is(err, context.Canceled), "real AgentLoop shutdown: %v", err)
		case <-time.After(cancelTestTurnStartDeadline):
			t.Error("actual human loop failed to join cleanup")
		}
	})
	t.Cleanup(func() { gatewaySteerCancellers.Delete(al) })
	wc, _ := makeForwarderTestConn(256)
	// The authenticated-connection process edge is simulated, not an execution
	// or a lifecycle identity. Gateway itself writes its trusted user record.
	wc.userID = "u2-scope-user"
	f := &u2ScopeFixture{al: al, lifecycle: ls, provider: p, closeLoop: closeLoop, reader: &wsHandlerReadLoop{h: h, wc: wc, ctx: ctx}, contexts: make(map[string]context.Context), before: make(map[string]*session.LifecycleRecord)}
	return f, stg.bootEpoch
}

func firstHumanRootIntoProvider(t *testing.T, f *u2ScopeFixture, chat string) string {
	t.Helper()
	f.reader.chatID = chat
	frame := generated.MessageFrame{Type: string(generated.WsFrameTypeMessage), Content: "actual first human input " + chat, AgentId: strPtr("mia"), ClientMessageId: strPtr("client-" + chat), Metadata: map[string]any{"workspace_id": testHarnessWorkspaceMembershipID}}
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	require.Nil(t, frame.SessionId, "FIRST HUMAN frame must not identify a preexisting conversation")
	require.Equal(t, wsHandlerReadLoopNext, f.reader.dispatchFrame(raw, wsTypeOnly{Type: string(generated.WsFrameTypeMessage)}))
	var id string
	deadline := time.NewTimer(cancelTestTurnStartDeadline)
	defer deadline.Stop()
	for id == "" {
		select {
		case data := <-f.reader.wc.sendCh:
			var frame generated.SessionStartedFrame
			if uerr := json.Unmarshal(data, &frame); uerr != nil {
				t.Fatalf("decode actual server frame: %v", uerr)
			}
			if frame.Type == string(generated.WsFrameTypeSessionStarted) {
				id = frame.SessionId
			}
		case <-deadline.C:
			t.Fatal("FIRST HUMAN gateway did not mint session_started")
		}
	}
	bindTestConnToSession(f.reader.h, chat, id, f.reader.wc)
	contexts := f.contexts
	select {
	case providerCtx := <-f.provider.entered:
		contexts[id] = providerCtx
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatal("FIRST HUMAN real bus admission did not enter provider")
	}
	entries, err := f.al.GetSessionStore().ReadTranscript(id)
	require.NoError(t, err)
	var human []string
	for _, entry := range entries {
		if entry.Role == "user" {
			human = append(human, entry.Content)
		}
	}
	require.Equal(t, []string{"actual first human input " + chat}, human, "gateway must persist exact HUMAN input, not a scheduled/system stand-in")
	meta, err := f.al.GetSessionStore().GetMeta(id)
	require.NoError(t, err)
	require.Equal(t, f.reader.wc.userID, meta.Owner)
	t.Logf("FIRST HUMAN entered provider: actual session=%s transcript=%q", id, human)
	return id
}
