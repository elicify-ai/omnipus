//go:build goolm && stdjson

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

const crashRoomDemand = "RETIRED_G_DEMAND_append_G2"
const crashRoomNarrative = "RETIRED_G_UNFINISHED_NARRATIVE"

type crashRoomProvider struct {
	mu      sync.Mutex
	calls   int
	initial bool
	path    string
	entered chan []providers.Message
	release chan struct{}
	once    sync.Once
}

func newCrashRoomProvider(initial bool, path string) *crashRoomProvider {
	return &crashRoomProvider{initial: initial, path: path, entered: make(chan []providers.Message, 8), release: make(chan struct{})}
}
func (p *crashRoomProvider) open()                 { p.once.Do(func() { close(p.release) }) }
func (*crashRoomProvider) GetDefaultModel() string { return "room-crash-recovery" }
func (p *crashRoomProvider) Chat(ctx context.Context, m []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	i := p.calls
	p.calls++
	p.mu.Unlock()
	if p.initial && i == 0 {
		return &providers.LLMResponse{Content: crashRoomNarrative, ToolCalls: []providers.ToolCall{{ID: "g-settled", Name: "append_file", Arguments: map[string]any{"path": p.path, "content": "G1\n"}}}, FinishReason: "tool_calls"}, nil
	}
	p.entered <- append([]providers.Message(nil), m...)
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if p.initial {
		return nil, context.Canceled
	}
	if i == 0 {
		stale := false
		for _, v := range m {
			if strings.Contains(v.Content, crashRoomDemand) || strings.Contains(v.Content, crashRoomNarrative) {
				stale = true
			}
		}
		name := "read_file"
		args := map[string]any{"path": p.path}
		if stale {
			name = "append_file"
			args["content"] = "G2\n"
		}
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: "later-decision", Name: name, Arguments: args}}, FinishReason: "tool_calls"}, nil
	}
	return &providers.LLMResponse{Content: "new request only", FinishReason: "stop"}, nil
}

func gatewayCrashRoomPolicies(t *testing.T, rh *roomsHarness, p providers.LLMProvider) {
	t.Helper()
	inst, ok := rh.api.agentLoop.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	inst.Provider = p
	work, err := workspace.EnsureWorkDir(rh.api.homePath, "sales")
	require.NoError(t, err)
	inst.Tools.RegisterReplacing(tools.NewAppendFileTool(work, true))
	inst.Tools.RegisterReplacing(tools.NewReadFileTool(work, true, tools.MaxReadFileSize))
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"append_file": config.ToolPolicyAllow, "read_file": config.ToolPolicyAllow, "send_message": config.ToolPolicyAllow, "answer_request": config.ToolPolicyAllow}})
}
func gatewayCrashRun(t *testing.T, rh *roomsHarness) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rh.api.agentLoop.Run(ctx) }()
	return cancel, done
}
func gatewayCrashStopLoop(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	err := gatewayCrashAwait(t, done)
	require.True(t, err == nil || err == context.Canceled, "fixture loop must join, not leak a surviving writer: %v", err)
}

func gatewayCrashRoomAfterBoot(t *testing.T) (*roomsHarness, gen.Room, string, string) {
	t.Helper()
	rh := newRoomsHarness(t)
	t.Setenv("HOME", rh.api.homePath)
	rh.hub.svc.SetClockForTest(func() time.Time { return time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC) })
	w := rh.do(http.MethodPost, "/api/v1/workspaces/sales/rooms", `{"kind":"squad","name":"Recovery","agent_ids":["mia","jim"]}`, rh.api.HandleWorkspaces)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var room gen.Room
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &room))
	work, err := workspace.EnsureWorkDir(rh.api.homePath, "sales")
	require.NoError(t, err)
	path := filepath.Join(work, "room-effects.txt")
	p := newCrashRoomProvider(true, path)
	gatewayCrashRoomPolicies(t, rh, p)
	w = rh.do(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", `{"client_message_id":"G","text":"`+crashRoomDemand+`","mentions":["mia"]}`, rh.api.HandleRooms)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	rh.hub.svc.WaitIdleForTest()
	mainID, err := session.MainSessionID("sales", "mia")
	require.NoError(t, err)
	entries, err := rh.api.agentLoop.GetSessionStore().ReadTranscript(mainID)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	requestID := entries[0].RoomRef.RequestId
	cancel, done := gatewayCrashRun(t, rh)
	gatewayCrashAwait(t, p.entered) // after G1's real tool result, before G's next model response.
	capture, found, err := rh.api.agentLoop.RequestLedger().Get(mainID, requestID)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, capture.Delivered)
	bytes, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "G1\n", string(bytes))
	snapshot := t.TempDir()
	require.NoError(t, gatewayCrashCopy(rh.api.homePath, snapshot))
	gatewayCrashStopLoop(t, cancel, done)
	p.open()
	cfg := rh.api.agentLoop.GetConfig()
	rh.hub.svc.Close()
	rh.api.agentLoop.Close()
	require.NoError(t, os.RemoveAll(rh.api.homePath))
	require.NoError(t, gatewayCrashCopy(snapshot, rh.api.homePath))
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, newCrashRoomProvider(false, path))
	boot := session.NewBootEpochStore(rh.api.homePath)
	_, err = boot.Mint()
	require.NoError(t, err)
	al.SetBootEpochStore(boot)
	al.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(agent.NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())
	r := &agent.SteerBootRecovery{Lifecycle: al.GetSessionLifecycleStore(), Sessions: al.GetSessionStore(), Inbox: al.GetMessageInboxStore(), BootEpoch: boot, Classifier: agent.NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore()), Deliverer: agent.NewSteerUpwardDeliverer()}
	require.NoError(t, r.Run(context.Background()))
	h := newWSHandler(msgBus, al, "http://localhost:3000")
	h.home = rh.api.homePath
	al.SetAddressDeps(gatewayAddressDeps{h: h})
	hub := newRoomsHub(h, rh.api.homePath)
	h.rooms = hub
	rh.h = h
	rh.hub = hub
	rh.api.agentLoop = al
	rh.api.rooms.Store(hub)
	gatewayCrashRoomPolicies(t, rh, newCrashRoomProvider(false, path))
	t.Cleanup(hub.svc.Close)
	_, err = hub.svc.List("sales")
	require.NoError(t, err)
	rec, err := al.GetSessionLifecycleStore().Load(mainID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleStopped, rec.State)
	require.NotNil(t, rec.StopNote)
	require.Equal(t, session.StopCauseRestart, rec.StopNote.Cause)
	capture, found, err = al.RequestLedger().Get(mainID, requestID)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, capture.Discarded, "real room recovery discards consumed G")
	var snap gen.RoomSnapshotFrame
	require.NoError(t, hub.svc.Attach(room.Id, 0, func(s gen.RoomSnapshotFrame) { snap = s }))
	var states []string
	for _, s := range snap.MemberStates {
		if s.RequestId == requestID {
			states = append(states, string(s.State))
		}
	}
	require.Equal(t, []string{"interrupted"}, states, "G is durably Interrupted before any new room message")
	return rh, room, requestID, path
}

func TestCrashRecovery_FreshHumanRoomRequestRestartsCrashStoppedMain(t *testing.T) {
	rh, room, oldID, path := gatewayCrashRoomAfterBoot(t)
	w := rh.do(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", `{"client_message_id":"H","text":"NEW_H_ONLY","mentions":["mia"]}`, rh.api.HandleRooms)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	rh.hub.svc.WaitIdleForTest()
	mainID, _ := session.MainSessionID("sales", "mia")
	captures, err := rh.api.agentLoop.RequestLedger().Undelivered(mainID)
	require.NoError(t, err)
	var fresh []addressing.Capture
	for _, c := range captures {
		if c.RequestID != oldID {
			fresh = append(fresh, c)
		}
	}
	require.Len(t, fresh, 1, "exactly one new H capture")
	assert.False(t, fresh[0].Held, "Q13=A: a fresh human H must not be held behind Stopped(restart)")
	var wake bus.InboundMessage
	select {
	case wake = <-rh.h.msgBus.InboundChan():
	default:
		t.Fatal("Q13=A: fresh human room request published zero wakes; it must restart the main without J")
	}
	assert.Equal(t, fresh[0].RequestID, wake.TranscriptEntryID)
	p := newCrashRoomProvider(false, path)
	gatewayCrashRoomPolicies(t, rh, p)
	require.NoError(t, rh.h.msgBus.PublishInbound(context.Background(), wake))
	cancel, done := gatewayCrashRun(t, rh)
	defer func() { p.open(); gatewayCrashStopLoop(t, cancel, done) }()
	input := gatewayCrashAwait(t, p.entered)
	gatewayCrashAssertRetired(t, input)
	rec, err := rh.api.agentLoop.GetSessionLifecycleStore().Load(mainID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleRunning, rec.State, "H reaches a real new execution")
}

func gatewayCrashAssertRetired(t *testing.T, messages []providers.Message) {
	t.Helper()
	var settled int
	for _, m := range messages {
		assert.NotContains(t, m.Content, crashRoomDemand, "B: retired G demand is not active model input on a later entry")
		assert.NotContains(t, m.Content, crashRoomNarrative, "B: unfinished G-derived narrative is not active work")
		if m.Role == "tool" && m.ToolCallID == "g-settled" {
			settled++
		}
	}
	assert.Equal(t, 1, settled, "B: G's known completed effect is retained as settled history, not erased with its demand")
}

func TestCrashRecovery_MembersNextTurnCannotRerunInterruptedRoomWork(t *testing.T) {
	rh, _, oldID, path := gatewayCrashRoomAfterBoot(t)
	p := newCrashRoomProvider(false, path)
	gatewayCrashRoomPolicies(t, rh, p)
	mainID, _ := session.MainSessionID("sales", "mia")
	// Use the real human WebSocket intake, including its persisted input id.
	rh.h.handleChatMessage(context.Background(), mainID, mainID, "HARMLESS_J_ONLY", "mia", nil, "", "sales", makeTestConn())
	cancel, done := gatewayCrashRun(t, rh)
	defer func() { p.open(); gatewayCrashStopLoop(t, cancel, done) }()
	input := gatewayCrashAwait(t, p.entered)
	gatewayCrashAssertRetired(t, input)
	var j int
	for _, m := range input {
		if m.Role == "user" && m.Content == "HARMLESS_J_ONLY" {
			j++
		}
	}
	assert.Equal(t, 1, j, "retirement must not erase the independent new human input")
	p.open()
	gatewayCrashAwait(t, p.entered) // the next model boundary proves the chosen tool effect has settled.
	bytes, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "G1\n", string(bytes), "B: stale G demand must not cause a new G2 append on J")
	_, err = rh.api.agentLoop.NewAddressRouter().Reply(context.Background(), tools.ReplyRequest{ReplyTo: oldID, Content: "old work answer", Author: tools.SendOrigin{WorkspaceID: "sales", AgentID: "mia"}, ResponderSessionID: mainID})
	// ReplyRouter exposes the fixed user-facing refusal, not the ledger's
	// internal sentinel. Pin both boundaries rather than assuming wrapping.
	assert.EqualError(t, err, "that request was discarded before it was delivered and can no longer be answered", "old G answer remains refused")
	_, resolveErr := rh.api.agentLoop.RequestLedger().Resolve(mainID, addressing.Pair{WorkspaceID: "sales", AgentID: "mia"}, oldID)
	assert.ErrorIs(t, resolveErr, addressing.ErrDiscarded, "the underlying durable correlation remains retired")
}

func TestCrashRecovery_IntentionalStopStillHoldsRoomRequests(t *testing.T) {
	rh, room, _, _ := gatewayCrashRoomAfterBoot(t)
	mainID, _ := session.MainSessionID("sales", "mia")
	_, err := rh.api.agentLoop.StopSession(context.Background(), agent.StopRequest{SessionID: mainID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "owner"}})
	require.NoError(t, err)
	w := rh.do(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", `{"client_message_id":"intentional-stop-H","text":"NEW_H_ONLY","mentions":["mia"]}`, rh.api.HandleRooms)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	rh.hub.svc.WaitIdleForTest()
	pending, err := rh.api.agentLoop.RequestLedger().Undelivered(mainID)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.True(t, pending[0].Held, "Q13: intentional Stop remains binding")
	select {
	case msg := <-rh.h.msgBus.InboundChan():
		t.Fatalf("intentional Stop gained a room wake: %+v", msg)
	default:
	}
}
