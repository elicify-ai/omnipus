package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const issue1296Workspace = "policy-workspace"

// Every permission change below crosses a real HTTP socket into HandleAgents.
// The loop is running via Run, and the next peer send goes through the real
// sender send_message tool, AddressRouter and receiverSendPolicy. Only the
// external model edge is scripted. No fast-upsert or manual policy publication.
// Oracle: gh issue view 1296 and the chief's immediate-effect RED brief.
type issue1296Harness struct {
	home     string
	api      *restAPI
	loop     *agent.AgentLoop
	bus      *bus.MessageBus
	server   *httptest.Server
	events   agent.EventSubscription
	cancel   context.CancelFunc
	runDone  chan error
	senderID string
	recvID   string
}

func newIssue1296Harness(t *testing.T, initial string, setup func(*config.Config)) *issue1296Harness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	cfg.Agents.Defaults.MaxToolIterations = 10
	cfg.Gateway.ValidateInbound = true
	for _, id := range []string{"policy-sender", "policy-receiver"} {
		toolCfg := coreagent.NewCustomAgentToolsCfg()
		toolCfg.Builtin.Policies["send_message"] = config.ToolPolicyAllow
		toolCfg.Builtin.Policies["read_file"] = config.ToolPolicyAllow
		if id == "policy-receiver" {
			toolCfg.Builtin.Policies["send_message"] = config.ToolPolicy(initial)
			toolCfg.Builtin.Policies["read_file"] = config.ToolPolicy(initial)
		}
		ac := config.AgentConfig{ID: id, Name: id, Type: config.AgentTypeCustom, Tools: toolCfg}
		cfg.Agents.List = append(cfg.Agents.List, ac)
		require.NoError(t, agentstore.New(home).Create(id, &ac))
	}
	if setup != nil {
		setup(cfg)
		// The optional MCP fixture sets assignments before boot, not to
		// activate a REST change. Persist that initial state as boot does.
		store := agentstore.New(home)
		for _, ac := range cfg.Agents.List {
			state, stateErr := store.ReadState(ac.ID)
			require.NoError(t, stateErr)
			copyAgent := ac
			_, mutateErr := store.MutateState(ac.ID, state.Revision, func(target *config.AgentConfig) error {
				*target = copyAgent
				return nil
			}, nil)
			require.NoError(t, mutateErr)
		}
	}
	require.NoError(t, config.SaveConfig(filepath.Join(home, "config.json"), cfg))
	ws := storedWorkspace{ID: issue1296Workspace, Name: "Policy workspace", Status: "active", CoreTeam: []string{"policy-sender", "policy-receiver"}}
	require.NoError(t, writeWorkspaceFile(home, ws))
	h := &issue1296Harness{home: home}
	h.start(t, cfg)
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.api.HandleAgents(w, withAdminRole(r))
	}))
	t.Cleanup(func() { h.server.Close(); h.stop(t) })
	return h
}

func (h *issue1296Harness) start(t *testing.T, cfg *config.Config) {
	t.Helper()
	provider := testutil.NewScenario()
	// All responses are plain text, so a denied peer cannot be mistaken for
	// a fake receiver tool. More than one turn is needed for restart controls.
	for range 8 {
		provider.WithText("policy-probe acknowledgement")
	}
	h.bus = bus.NewMessageBus()
	loop, loopErr := agent.NewAgentLoop(cfg, h.bus, provider)
	require.NoError(t, loopErr)
	h.loop = loop
	installTestSessionLauncher(t, loop)
	wsHandler := newWSHandler(h.bus, loop, "")
	wsHandler.home = h.home
	loop.SetAddressDeps(gatewayAddressDeps{h: wsHandler})
	h.api = &restAPI{agentLoop: loop, homePath: h.home}
	h.api.builtinRegistry, _ = buildCentralBuiltinRegistry(nil)
	store := loop.GetSessionStore()
	sender, senderErr := store.GetOrCreateMainSession(issue1296Workspace, "policy-sender")
	require.NoError(t, senderErr)
	receiver, receiverErr := store.GetOrCreateMainSession(issue1296Workspace, "policy-receiver")
	require.NoError(t, receiverErr)
	h.senderID, h.recvID = sender.ID, receiver.ID
	h.events = loop.SubscribeEvents(128)
	runCtx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.runDone = make(chan error, 1)
	go func() { h.runDone <- loop.Run(runCtx) }()
	// A completed real bus-dispatched turn is the running-loop positive
	// instrument, not a sleep or an assertion on a private running flag.
	require.NoError(t, h.bus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel: "webchat", ChatID: "policy-probe", Content: "running-loop-control",
		SessionID: h.senderID, UserInitiated: true, Metadata: map[string]string{"agent_id": "policy-sender", "workspace_id": issue1296Workspace},
	}))
	h.awaitTurn(t, h.senderID)
}

func (h *issue1296Harness) stop(t *testing.T) {
	t.Helper()
	if h.cancel == nil {
		return
	}
	h.cancel()
	h.loop.Stop()
	select {
	case runErr := <-h.runDone:
		require.NoError(t, runErr, "running gateway loop must shut down without a hidden failure")
	case <-time.After(busDeliveryTimeout):
		t.Fatal("issue1296 fixture: running loop did not stop")
	}
	h.loop.WaitForActiveRequests()
	h.loop.UnsubscribeEvents(h.events.ID)
	h.loop.Close()
	h.bus.Close()
	h.cancel = nil
}

func (h *issue1296Harness) awaitTurn(t *testing.T, sessionID string) {
	t.Helper()
	deadline := time.NewTimer(busDeliveryTimeout)
	defer deadline.Stop()
	for {
		select {
		case event, ok := <-h.events.C:
			require.True(t, ok, "turn event stream must stay connected")
			if event.Kind != agent.EventKindTurnEnd {
				continue
			}
			end, typed := event.Payload.(agent.TurnEndPayload)
			require.True(t, typed)
			if end.SessionID == sessionID {
				require.Equal(t, agent.TurnEndStatusCompleted, end.Status, "real policy-probe turn must complete")
				// TurnEnd precedes runTurn's final cleanup. The worker publishes
				// its reply only after processMessage and its transcript writes return.
				h.awaitPublishedReply(t, sessionID, end.ChatID, deadline.C)
				return
			}
		case runErr := <-h.runDone:
			t.Fatalf("issue1296 fixture: loop exited before the next decision: %v", runErr)
		case <-deadline.C:
			t.Fatal("issue1296 fixture: no completed real turn; not RED evidence about permissions")
		}
	}
}

func (h *issue1296Harness) awaitPublishedReply(t *testing.T, sessionID, chatID string, deadline <-chan time.Time) {
	t.Helper()
	select {
	case reply, ok := <-h.bus.OutboundChan():
		require.True(t, ok, "policy-probe reply stream must stay connected")
		require.Equal(t, "webchat", reply.Channel)
		require.Equal(t, sessionID, reply.SessionID, "published reply must belong to the completed turn")
		require.Equal(t, chatID, reply.ChatID, "published reply must belong to the completed turn")
		require.Equal(t, "policy-probe acknowledgement", reply.Content)
	case runErr := <-h.runDone:
		t.Fatalf("issue1296 fixture: loop exited before publishing the completed turn: %v", runErr)
	case <-deadline:
		t.Fatal("issue1296 fixture: completed turn did not publish its reply")
	}
}

func (h *issue1296Harness) peerSend(t *testing.T, content string) *tools.ToolResult {
	t.Helper()
	sender, ok := h.loop.GetRegistry().GetAgent("policy-sender")
	require.True(t, ok)
	tool, present := sender.Tools.Get("send_message")
	require.True(t, present, "real executable sender tool must be wired")
	ctx := tools.WithAgentID(context.Background(), "policy-sender")
	ctx = tools.WithWorkspaceID(ctx, issue1296Workspace)
	ctx = tools.WithTranscriptSessionID(ctx, h.senderID)
	ctx = tools.WithToolContext(ctx, "webchat", "policy-probe")
	result := tool.Execute(ctx, map[string]any{"workspace_id": issue1296Workspace, "agent_id": "policy-receiver", "content": content})
	require.NotNil(t, result)
	if !result.IsError {
		h.awaitTurn(t, h.recvID)
		// This plain-text fixture never calls answer_request, so an admitted
		// request owes exactly one reminder turn. Drain its published reply
		// before a later refusal compares the receiver's transcript snapshots.
		h.awaitTurn(t, h.recvID)
	}
	return result
}

func (h *issue1296Harness) policy(t *testing.T, name string) string {
	t.Helper()
	receiver, ok := h.loop.GetRegistry().GetAgent("policy-receiver")
	require.True(t, ok)
	require.NotNil(t, receiver.LoadToolPolicy())
	return tools.ResolveEffectivePolicy(receiver.LoadToolPolicy(), name)
}

func (h *issue1296Harness) put(t *testing.T, fields map[string]any) *agentstore.State {
	t.Helper()
	store := agentstore.New(h.home)
	before, stateErr := store.ReadState("policy-receiver")
	require.NoError(t, stateErr)
	fields["revision"] = before.Revision
	raw, marshalErr := json.Marshal(fields)
	require.NoError(t, marshalErr)
	req, reqErr := http.NewRequest(http.MethodPut, h.server.URL+"/api/v1/agents/policy-receiver", bytes.NewReader(raw))
	require.NoError(t, reqErr)
	req.Header.Set("Content-Type", "application/json")
	response, callErr := h.server.Client().Do(req)
	require.NoError(t, callErr)
	defer response.Body.Close()
	body, readErr := io.ReadAll(response.Body)
	require.NoError(t, readErr)
	require.Equal(t, http.StatusOK, response.StatusCode, "UI REST PUT must reach the actual write path: %s", body)
	var updated gen.Agent
	require.NoError(t, json.Unmarshal(body, &updated))
	assert.Equal(t, "policy-receiver", updated.Id)
	after, persistedErr := store.ReadState("policy-receiver")
	require.NoError(t, persistedErr)
	require.NotEqual(t, before.Revision, after.Revision, "positive control: REST change must really be durable")
	return after
}

func (h *issue1296Harness) restart(t *testing.T) {
	t.Helper()
	h.stop(t)
	cfg, loadErr := config.LoadConfig(filepath.Join(h.home, "config.json"))
	require.NoError(t, loadErr)
	require.NoError(t, populateAgentsListFromEntityStoreStrict(cfg, h.home))
	h.start(t, cfg) // real cold restart; no fast upsert or manual policy stamp
}

func issue1296AssertPeer(t *testing.T, h *issue1296Harness, want string, content string) {
	t.Helper()
	before, readErr := h.loop.GetSessionStore().ReadTranscript(h.recvID)
	require.NoError(t, readErr)
	result := h.peerSend(t, content)
	if want == "deny" {
		assert.True(t, result.IsError, "#1296 revoke: the very next peer must refuse, result=%s", result.ForLLM)
		assert.ErrorIs(t, result.Err, agent.ErrPeerRefused)
		assert.Contains(t, result.ForLLM, "receiver's send_message policy denies requests")
		after, afterErr := h.loop.GetSessionStore().ReadTranscript(h.recvID)
		require.NoError(t, afterErr)
		assert.Equal(t, before, after, "revoked receiver must admit no request or turn")
	} else {
		assert.False(t, result.IsError, "#1296 grant: the very next peer must be admitted, result=%s", result.ForLLM)
		assert.NoError(t, result.Err)
	}
}

func TestIssue1296RESTPermissionOnly_RevokeAndGrantApplyToNextPeerAndToolResolution(t *testing.T) {
	for _, direction := range []struct{ name, initial, next string }{{"revoke", "allow", "deny"}, {"grant", "deny", "allow"}} {
		t.Run(direction.name, func(t *testing.T) {
			h := newIssue1296Harness(t, direction.initial, nil)
			t.Run("initial_policy_control", func(t *testing.T) {
				issue1296AssertPeer(t, h, direction.initial, "before-put-control")
				assert.Equal(t, direction.initial, h.policy(t, "read_file"))
			})
			stored := h.put(t, map[string]any{"tool_policy_changes": map[string]any{"set": map[string]any{
				"send_message": direction.next, "read_file": direction.next,
			}}})
			assert.Equal(t, config.ToolPolicy(direction.next), stored.Agent.Tools.Builtin.Policies["send_message"])
			assert.Equal(t, config.ToolPolicy(direction.next), stored.Agent.Tools.Builtin.Policies["read_file"])
			t.Run("very_next_live_decision", func(t *testing.T) {
				issue1296AssertPeer(t, h, direction.next, "immediate-after-put")
				assert.Equal(t, direction.next, h.policy(t, "read_file"), "#1296: actual live registry tool resolution must match the successful PUT before a restart")
			})
			t.Run("restart_positive_control", func(t *testing.T) {
				h.restart(t)
				issue1296AssertPeer(t, h, direction.next, "after-restart-control")
				assert.Equal(t, direction.next, h.policy(t, "read_file"))
			})
		})
	}
}

func TestIssue1296RESTToolsConfigOnly_RevokeAndGrantApplyWithoutRestart(t *testing.T) {
	for _, direction := range []struct{ name, initial, next string }{{"revoke", "allow", "deny"}, {"grant", "deny", "allow"}} {
		t.Run(direction.name, func(t *testing.T) {
			h := newIssue1296Harness(t, direction.initial, nil)
			t.Run("initial_policy_control", func(t *testing.T) {
				issue1296AssertPeer(t, h, direction.initial, "before-tools-cfg-control")
			})
			policies := coreagent.NewCustomAgentToolsCfg().Builtin.Policies
			policies["send_message"] = config.ToolPolicy(direction.next)
			policies["read_file"] = config.ToolPolicy(direction.next)
			stored := h.put(t, map[string]any{"tools_cfg": map[string]any{"builtin": map[string]any{"policies": policies}}})
			assert.Equal(t, config.ToolPolicy(direction.next), stored.Agent.Tools.Builtin.Policies["send_message"])
			t.Run("very_next_live_decision", func(t *testing.T) {
				issue1296AssertPeer(t, h, direction.next, "immediate-after-tools-cfg-put")
				assert.Equal(t, direction.next, h.policy(t, "read_file"))
			})
			t.Run("restart_positive_control", func(t *testing.T) {
				h.restart(t)
				issue1296AssertPeer(t, h, direction.next, "tools-cfg-restart-control")
				assert.Equal(t, direction.next, h.policy(t, "read_file"))
			})
		})
	}
}
