// Copyright (c) 2026 Omnipus contributors
// License: MIT

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// Six seconds is the dispatch's 3s force + 3s detach budget. One additional
// second permits filesystem settlement after cancellation, not more work.
const plainStopUATSettlementBudget = 7 * time.Second

type plainStopUATFixture struct {
	al        *agent.AgentLoop
	lifecycle *session.LifecycleStore
	inbox     *session.MessageInboxStore
	provider  *plainStopUATProvider
	server    *httptest.Server
	conn      *websocket.Conn
	rootID    string
	helperIDs []string
	before    map[string]*session.LifecycleRecord
}

func newPlainStopUATFixture(t *testing.T, nested bool) *plainStopUATFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{
		Gateway: config.GatewayConfig{DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: home, DefaultAgentID: "mia", DefaultModel: config.DefaultModel{Model: "plain-stop-uat-provider"}, MaxTokens: 4096},
			List:     []config.AgentConfig{{ID: "mia", Home: home}, {ID: plainStopBranchAgent, Home: home}, {ID: plainStopLeafAgent, Home: home}},
		},
		// Root, branch, two leaves and an unrelated root can all genuinely run.
		Performance: config.PerformanceConfig{MaxParallelAgents: 8, MaxDelegationDepth: 3},
		Sandbox: config.OmnipusSandboxConfig{Mode: config.SandboxModeOff, ToolPolicies: map[string]string{
			"delegate": string(config.ToolPolicyAllow),
		}},
	}
	ensureTestWorkspaceMembership(t, cfg)
	unlock := workspace.LockID(testHarnessWorkspaceMembershipID)
	err := workspace.SaveDelegation(home, testHarnessWorkspaceMembershipID, []workspace.DelegationEdge{
		{FromAgent: "mia", ToAgent: plainStopLeafAgent, Modes: []workspace.DelegationMode{workspace.ModeDirect}},
		{FromAgent: "mia", ToAgent: plainStopBranchAgent, Modes: []workspace.DelegationMode{workspace.ModeDirect}},
		{FromAgent: plainStopBranchAgent, ToAgent: plainStopLeafAgent, Modes: []workspace.DelegationMode{workspace.ModeDirect}},
	})
	unlock()
	require.NoError(t, err, "SETUP: authorize actual helper delegation through the real workspace store")
	msgBus := bus.NewMessageBus()
	p := newPlainStopUATProvider(nested)
	al, err := agent.NewAgentLoop(cfg, msgBus, p)
	require.NoError(t, err)
	ls := session.NewLifecycleStore(home + "/session_lifecycle")
	inbox := session.NewMessageInboxStore(home + "/session_inbox")
	al.SetSessionMessagingStores(inbox, ls)
	h := newWSHandler(msgBus, al, "")
	msgBus.SetStreamDelegate(h)
	stg := &setupAndStartServicesState{ctx: context.Background(), cfg: cfg, homePath: home, agentLoop: al,
		lifecycleStore: ls, runningServices: &services{}, wsHandler: h}
	require.NoError(t, stg.mintBootEpoch(), "SETUP: mint a real boot epoch before first-human admission")
	stg.wireSteerDeps() // Real launcher, canceller, deliverer and audience wiring.
	initial, err := ls.List(session.LifecycleFilter{})
	require.NoError(t, err)
	require.Empty(t, initial, "SETUP: no manually seeded lifecycle/execution identity")
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(ctx) }()
	var once sync.Once
	t.Cleanup(func() {
		once.Do(func() { close(p.cleanup); cancel(); al.Close(); msgBus.Close() })
		select {
		case runErr := <-runDone:
			if !errors.Is(runErr, context.Canceled) {
				require.NoError(t, runErr, "SETUP cleanup: real agent loop joined")
			}
		case <-time.After(busDeliveryTimeout):
			t.Error("SETUP cleanup: real agent loop did not join")
		}
		gatewaySteerCancellers.Delete(al)
	})
	t.Cleanup(h.Wait)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	f := &plainStopUATFixture{al: al, lifecycle: ls, inbox: inbox, provider: p, server: srv, before: make(map[string]*session.LifecycleRecord)}
	f.conn, f.rootID = f.firstHuman(t, plainStopRootPrompt)
	wantDirect := 1
	if nested {
		wantDirect = 2
	}
	var direct []session.LifecycleRecord
	require.Eventually(t, func() bool {
		direct, err = ls.List(session.LifecycleFilter{SteeringSessionID: f.rootID})
		return err == nil && len(direct) == wantDirect && p.contextFor(f.rootID) != nil
	}, busDeliveryTimeout, 10*time.Millisecond, "SETUP: registered delegate tool must create the requested real helpers; root requests=%v", p.requestsFor(f.rootID))
	for _, child := range direct {
		f.helperIDs = append(f.helperIDs, child.SessionID)
		if child.AgentID == plainStopBranchAgent {
			var grandchildren []session.LifecycleRecord
			require.Eventually(t, func() bool {
				grandchildren, err = ls.List(session.LifecycleFilter{SteeringSessionID: child.SessionID})
				return err == nil && len(grandchildren) == 1
			}, busDeliveryTimeout, 10*time.Millisecond, "SETUP: real helper must delegate exactly one grandchild")
			f.helperIDs = append(f.helperIDs, grandchildren[0].SessionID)
		}
	}
	for _, id := range append([]string{f.rootID}, f.helperIDs...) {
		require.Eventually(t, func() bool { return p.contextFor(id) != nil }, busDeliveryTimeout, 10*time.Millisecond,
			"SETUP: each real admitted execution must actually enter the provider: %s", id)
		rec, loadErr := ls.Load(id)
		require.NoError(t, loadErr)
		require.Equal(t, session.LifecycleRunning, rec.State, "SETUP: real execution running before Stop: %s", id)
		require.NotNil(t, rec.ExecutionID, "SETUP: production-minted execution identity: %s", id)
		require.Equal(t, stg.bootEpoch.Current(), rec.ExecutionID.BootSeq)
		require.Equal(t, 1, rec.Generation, "SETUP: initial production generation")
		require.Nil(t, rec.Stop)
		require.NoError(t, p.contextFor(id).Err(), "SETUP: provider still doing work before Stop")
		f.before[id] = rec
	}
	t.Logf("REAL tree admitted via WS human + registered delegate: parent=%s helpers=%v", f.rootID, f.helperIDs)
	return f
}

func (f *plainStopUATFixture) firstHuman(t *testing.T, prompt string) (*websocket.Conn, string) {
	t.Helper()
	conn := dialTestWS(t, f.server)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)
	awaitStopScopeWireFrame(t, conn, "session_state", "SETUP: real WebSocket authentication")
	require.NoError(t, conn.WriteJSON(generated.MessageFrame{Type: "message", Content: prompt, AgentId: strPtr("mia"),
		ClientMessageId: strPtr("plain-stop-" + prompt), Metadata: map[string]any{"workspace_id": testHarnessWorkspaceMembershipID}}))
	raw := awaitStopScopeWireFrame(t, conn, "session_started", "SETUP: first human message must mint an actual chat")
	var started generated.SessionStartedFrame
	require.NoError(t, json.Unmarshal(raw, &started))
	require.NotEmpty(t, started.SessionId)
	return conn, started.SessionId
}

func (f *plainStopUATFixture) plainWebStop(t *testing.T) {
	t.Helper()
	// Exactly the ordinary Stop button envelope: scope is OMITTED, never tree.
	require.NoError(t, f.conn.WriteJSON(generated.CancelFrame{Type: "cancel", SessionId: f.rootID}))
	raw := f.awaitStopFrame(t, "cancel_stage")
	var stage generated.CancelStageFrame
	require.NoError(t, json.Unmarshal(raw, &stage))
	require.Equal(t, f.rootID, stage.SessionId)
	require.Equal(t, "graceful", stage.Stage, "ONE Stop asks politely before forced cancellation")
	// A second frame is a read-loop barrier: selection from the preceding Stop
	// has returned before this real attach is handled. Not a sleep or test hook.
	require.NoError(t, f.conn.WriteJSON(generated.AttachSessionFrame{Type: "attach_session", SessionId: f.rootID}))
	raw = f.awaitStopFrame(t, "catch_up_complete")
	var caughtUp generated.CatchUpCompleteFrame
	require.NoError(t, json.Unmarshal(raw, &caughtUp))
	require.Equal(t, f.rootID, caughtUp.SessionId, "socket barrier must attach to this exact stopped chat")
}

func (f *plainStopUATFixture) awaitSettled(t *testing.T, ids []string, requireStopped bool) bool {
	t.Helper()
	deadline := time.NewTimer(plainStopUATSettlementBudget)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	var diagnostics []string
	for {
		settled := true
		diagnostics = nil
		for _, id := range ids {
			rec, err := f.lifecycle.Load(id)
			if err != nil {
				settled = false
				diagnostics = append(diagnostics, fmt.Sprintf("session=%s load_error=%v", id, err))
				continue
			}
			providerErr := f.provider.contextFor(id).Err()
			ready := rec.State == session.LifecycleStopped && rec.Stop == nil && rec.StopNote != nil && providerErr == context.Canceled
			if ready && rec.SteeredBy != nil {
				ready = f.stoppedNoticeStored(t, rec)
			}
			if !requireStopped && rec.Terminal() && rec.FinalDelivery != nil {
				progress, _, _, progressErr := f.lifecycle.FinalDeliveryState(id, rec.Generation, rec.FinalDelivery.CommitID)
				// publishCommittedFinal writes InboxAppended only AFTER the real
				// deliverer returns (append, frames, wake-or-suppression complete).
				// FramesPersisted is not written by that publisher; it cannot be
				// used as its completion latch. WakeRecorded may correctly stay
				// false when a stopped parent is not woken.
				ready = progressErr == nil && progress.InboxAppended
			}
			key := id
			if rec.SteeredBy == nil {
				key = "agent:" + rec.AgentID + ":session:" + id
			}
			ready = ready && f.al.GetActiveTurnBySession(key) == nil
			settled = settled && ready
			diagnostics = append(diagnostics, fmt.Sprintf("session=%s state=%s provider_context_error=%v stop_fence=%+v stop_note=%+v final=%t", id, rec.State, providerErr, rec.Stop, rec.StopNote, rec.FinalDelivery != nil))
		}
		if settled {
			return true
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			if requireStopped {
				t.Errorf("PLAIN STOP REGRESSION: pressing ordinary web Stop must stop this chat AND its helper tree within the 3s force + 3s detach settlement budget; actual:\n%s", strings.Join(diagnostics, "\n"))
			} else {
				t.Errorf("SETUP: helper owner/publication tail did not settle before the FIFO barrier; actual:\n%s", strings.Join(diagnostics, "\n"))
			}
			return false
		}
	}
}

func (f *plainStopUATFixture) stoppedNoticeStored(t *testing.T, rec *session.LifecycleRecord) bool {
	t.Helper()
	parentID := rec.SteeredBy.SteeringSessionID
	entries, err := f.inbox.Entries(parentID)
	require.NoError(t, err, "read the real stopped-helper publication, not a quiet-time guess")
	wantID := fmt.Sprintf("stopped-notice:%s:%s:%d:%d", parentID, rec.SessionID, rec.Generation, rec.StopNote.Seq)
	for _, entry := range entries {
		if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
			continue
		}
		notice, decodeErr := entry.Message.AsSessionMessageError()
		if decodeErr == nil && notice.MessageId == wantID {
			return true
		}
	}
	return false
}
