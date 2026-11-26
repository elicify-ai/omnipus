package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const opencodeR4GWWorker = "r4-task-worker"
const opencodeR4GWPerson = "r4-task-person"

// Gateway-unit boundary: seed a genuine task-origin, non-delegated run in the
// real stores; do not execute a paid task to test its chat admission gate.
// Transport/auth handshake are outside this unit: the real read-loop message
// decoder receives the principal a successful handshake would have installed.
// Target resolution, worker gate, bus admission, persistence and replay are real.
type opencodeR4GWFixture struct {
	handler *WSHandler
	bus     *bus.MessageBus
	conn    *wsConn
	sid     string
	record  *session.LifecycleRecord
	task    *task.Task
}

func opencodeR4GatewayFixture(t *testing.T, cli string) *opencodeR4GWFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		List: []config.AgentConfig{
			{ID: "r4-native", Type: config.AgentTypeCore},
			{ID: opencodeR4GWWorker, Type: config.AgentTypeWorker, Subagents: &config.SubagentsConfig{
				Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: cli},
			}},
		},
	}}
	cfg.Sandbox = config.DefaultConfig().Sandbox
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	al := mustAgentLoop(t, cfg, mb, &restMockProvider{})
	h := newWSHandler(mb, al, "")
	t.Cleanup(h.Wait)
	f := &opencodeR4GWFixture{handler: h, bus: mb, conn: makeTestConn()}
	f.conn.userID = opencodeR4GWPerson

	// Prove this same decoder/handler/bus sees an accepted native frame.
	// No loop consumer runs: any unintended admission remains observable.
	controlConn := makeTestConn()
	controlConn.userID = opencodeR4GWPerson
	controlAgent, controlClient := "r4-native", "r4-native-control"
	opencodeR4GatewaySend(t, h, controlConn, "r4-control-chat", generated.MessageFrame{
		Type: string(generated.WsFrameTypeMessage), Content: "native admission instrument",
		AgentId: &controlAgent, ClientMessageId: &controlClient,
	})
	select {
	case admitted := <-mb.InboundChan():
		require.Equal(t, "native admission instrument", admitted.Content)
		require.Equal(t, opencodeR4GWPerson, admitted.GatewayUserID)
		require.Equal(t, controlAgent, admitted.Metadata["agent_id"])
	default:
		t.Fatal("instrument: the real gateway must admit the native control to this observed bus")
	}
	controlErrors, _ := opencodeR4GatewayFrames(t, controlConn)
	require.Empty(t, controlErrors, "instrument: native control must not be a refused frame")

	store := al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeTask, "system", opencodeR4GWWorker)
	require.NoError(t, err)
	f.sid = meta.ID
	taskStore := agent.GetTaskStore(al)
	require.NotNil(t, taskStore)
	tk := &task.Task{Title: "External worker Board task", Prompt: "Original Board task instruction.",
		AgentID: opencodeR4GWWorker, WorkspaceID: testHarnessWorkspaceMembershipID,
		Action: task.ActionLLM, Status: task.StatusInProgress, SessionID: f.sid}
	require.NoError(t, taskStore.Create(tk))
	owner, workspaceID := opencodeR4GWPerson, tk.WorkspaceID
	require.NoError(t, store.SetMeta(f.sid, session.MetaPatch{TaskID: &tk.ID, WorkspaceID: &workspaceID, Owner: &owner}))
	require.NoError(t, al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID: f.sid, Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, Is3P: true, AgentID: opencodeR4GWWorker,
		WorkspaceID: workspaceID, Origin: &session.Origin{Kind: session.OriginKindTask, TaskID: tk.ID},
	}))
	require.NoError(t, store.AppendTranscriptStrict(f.sid, session.TranscriptEntry{
		ID: "r4-task-original-prompt", Role: "user", Content: tk.Prompt, Timestamp: time.Now().UTC(),
	}))
	meta, err = store.GetMeta(f.sid)
	require.NoError(t, err)
	require.Equal(t, session.SessionTypeTask, meta.Type, "instrument: not an ordinary chat or a delegated helper")
	require.Equal(t, tk.ID, meta.TaskID)
	require.True(t, isWorkerAgentID(cfg, meta.AgentID))
	f.record, err = al.GetSessionLifecycleStore().Load(f.sid)
	require.NoError(t, err)
	require.Nil(t, f.record.SteeredBy, "instrument: this is a Board task-run root, not the existing-helper exception")
	require.Equal(t, session.OriginKindTask, f.record.Origin.Kind)
	f.task, err = taskStore.Get(tk.ID)
	require.NoError(t, err)
	seed := qa2ReopenHelperTranscript(t, store, f.sid)
	require.Len(t, seed, 1, "instrument: the fresh-store reader must see the durable task prompt")
	require.Equal(t, tk.Prompt, seed[0].Content)
	bindTestConnToSession(h, "r4-worker-chat", f.sid, f.conn)
	return f
}

func opencodeR4GatewaySend(t *testing.T, h *WSHandler, wc *wsConn, chatID string, frame generated.MessageFrame) {
	t.Helper()
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	reader := &wsHandlerReadLoop{h: h, ctx: context.Background(), wc: wc, chatID: chatID}
	require.Equal(t, wsHandlerReadLoopNext, reader.handleMessageFrame(raw), "instrument: real message-frame decoding reached the chat pipeline")
}

func (f *opencodeR4GWFixture) send(t *testing.T, sid string, explicitAgent bool) {
	t.Helper()
	clientID := "r4-refused-task-input"
	frame := generated.MessageFrame{Type: string(generated.WsFrameTypeMessage),
		Content: "Q13-B: refuse this task-run chat input without starting a turn.", ClientMessageId: &clientID}
	if sid != "" {
		frame.SessionId = &sid
	}
	if explicitAgent {
		worker := opencodeR4GWWorker
		frame.AgentId = &worker
	}
	opencodeR4GatewaySend(t, f.handler, f.conn, "r4-worker-chat", frame)
}

func opencodeR4GatewayFrames(t *testing.T, wc *wsConn) ([]generated.ErrorFrame, []string) {
	t.Helper()
	var errors []generated.ErrorFrame
	var types []string
	for {
		select {
		case raw := <-wc.sendCh:
			var envelope wsTypeOnly
			require.NoError(t, json.Unmarshal(raw, &envelope))
			types = append(types, envelope.Type)
			if envelope.Type == string(generated.WsFrameTypeError) {
				var refusal generated.ErrorFrame
				require.NoError(t, json.Unmarshal(raw, &refusal))
				errors = append(errors, refusal)
			}
		default:
			return errors, types // This handler and its bus publication are synchronous.
		}
	}
}

func (f *opencodeR4GWFixture) assertNoAdmission(t *testing.T) {
	t.Helper()
	select {
	case admitted := <-f.bus.InboundChan():
		assert.Fail(t, "Q13 B: a refused worker task chat must not admit or steer a turn", "admitted session=%s content=%q", admitted.SessionID, admitted.Content)
	default:
	}
	record, err := f.handler.agentLoop.GetSessionLifecycleStore().Load(f.sid)
	require.NoError(t, err)
	assert.Equal(t, f.record, record, "Q13 B: the refusal must not change the task-run state, generation or execution")
	tk, err := agent.GetTaskStore(f.handler.agentLoop).Get(f.task.ID)
	require.NoError(t, err)
	assert.Equal(t, f.task, tk, "Q13 B: the task itself must remain intact")
}
