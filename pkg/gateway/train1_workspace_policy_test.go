package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder 2026-10-18: "Create yes, delete asks". Only the model and browser
// transport are scripted. Seed, ToolSearch, loop policy gates, executable
// tools, workspace storage and the gateway approval adapter are all real.
func train1WorkspacePolicyHarness(t *testing.T, provider *testutil.ScenarioProvider) (*restAPI, *approvalRegistryV2, *wsConn, *bus.MessageBus, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = home
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	coreagent.SeedConfig(cfg)
	require.Empty(t, repairAndValidateToolPolicyCoverage(cfg))
	seedSystemAgentEagerSouls(cfg)
	require.NoError(t, ensureDefaultWorkspace(home, "tab", cfg))
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	loop, err := agent.NewAgentLoop(cfg, msgBus, provider)
	require.NoError(t, err)
	t.Cleanup(loop.Close)
	api := &restAPI{agentLoop: loop, homePath: home}
	rc := &runContextWithOptions{
		homePath: home, configPath: api.configPath(), cfg: cfg, agentLoop: loop,
		runningServices: &services{restAPIRef: api}, centralMCPReg: tools.NewMCPRegistry(),
	}
	rc.wireSystemTools()
	omnipus, found := loop.GetRegistry().GetAgent("omnipus")
	require.True(t, found)
	for _, name := range []string{"create_workspace", "delete_workspace"} {
		_, registered := omnipus.Tools.Get(name)
		require.True(t, registered, "exercise the actual boot-registered executable for %s", name)
		_, catalogued := rc.centralBuiltinReg.Get(name)
		require.True(t, catalogued, "%s must also be in the live metadata catalogue", name)
	}
	reg := newApprovalRegistryV2(64, time.Minute)
	reg.terminalRetention = 0
	ws := &WSHandler{agentLoop: loop, approvalRegV2: reg}
	loop.SetToolApprover(newPolicyApproverAdapter(reg, ws))
	defaultID, defaultErr := workspace.ResolveDefaultID(home)
	require.NoError(t, defaultErr)
	main, mainErr := loop.GetSessionStore().GetOrCreateMainSession(defaultID, "omnipus")
	require.NoError(t, mainErr)
	return api, reg, apprResAttachConns(t, ws, 1)[0], msgBus, main.ID
}

// Web chat carries a real transcript session. ProcessDirectWithChannelAs is
// a CLI-style entry point and does not supply that identity; use the actual
// inbound bus so the approval is reachable in Omnipus's canonical main chat.
func train1StartMainTurn(t *testing.T, api *restAPI, msgBus *bus.MessageBus, mainID, content string) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	loopDone := make(chan error, 1)
	go func() { loopDone <- api.agentLoop.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-loopDone)
	})
	require.NoError(t, msgBus.PublishInbound(ctx, bus.InboundMessage{
		Channel: "webchat", ChatID: "direct", SessionID: mainID,
		Sender: bus.SenderInfo{CanonicalID: "webchat_user"}, GatewayUserID: "tab",
		Content: content, UserInitiated: true, OperatorPrompt: true,
		Metadata: map[string]string{"agent_id": "omnipus"},
	}))
	return ctx
}

func TestTrain1OmnipusWorkspaceTools_CreateWithoutApprovalPersistsWorkspace(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCall("ToolSearch", `{"names":["create_workspace"]}`).
		WithToolCall("create_workspace", `{"name":"Train 1 created workspace"}`).
		WithText("Workspace created.")
	api, reg, tab, msgBus, mainID := train1WorkspacePolicyHarness(t, provider)
	ctx := train1StartMainTurn(t, api, msgBus, mainID, "Create a workspace")
	select {
	case reply := <-msgBus.OutboundChan():
		require.Equal(t, "Workspace created.", reply.Content)
		require.Equal(t, mainID, reply.SessionID)
	case <-ctx.Done():
		t.Fatal("an unexpected approval must not stall workspace creation")
	}
	require.NoError(t, ctx.Err(), "an unexpected approval must not stall workspace creation")
	assert.Equal(t, 3, provider.CallCount(), "ToolSearch, creation and the final model response must all run")
	reg.mu.Lock()
	requests := len(reg.entries)
	reg.mu.Unlock()
	assert.Zero(t, requests, "Allow must never enter the approval registry")
	assert.Empty(t, tab.sendCh, "creation must not emit an approval frame")
	records, readErr := listWorkspaceFiles(api.homePath)
	require.NoError(t, readErr)
	require.Len(t, records, 2, "the default workspace plus exactly one real tool-created workspace")
	created := 0
	for _, record := range records {
		if record.Name == "Train 1 created workspace" {
			created++
			assert.False(t, record.IsDefault)
			assert.Empty(t, record.CoreTeam)
		}
	}
	assert.Equal(t, 1, created, "a provider's scripted success text is not proof of a workspace write")
}

func TestTrain1OmnipusWorkspaceTools_DeleteWaitsForApproval(t *testing.T) {
	for _, action := range []ApprovalAction{ApprovalActionApprove, ApprovalActionDeny} {
		t.Run(string(action), func(t *testing.T) {
			provider := testutil.NewScenario()
			api, reg, tab, msgBus, mainID := train1WorkspacePolicyHarness(t, provider)
			const targetID = "train1-delete-target"
			require.NoError(t, workspace.SaveRecord(api.homePath, workspace.Workspace{
				ID: targetID, Name: "Delete only after approval", Status: "active",
			}))
			before, readErr := workspace.ReadState(api.homePath, targetID)
			require.NoError(t, readErr)
			args := map[string]any{"id": targetID, "revision": before.Revision, "confirm": true}
			encoded, encodeErr := json.Marshal(args)
			require.NoError(t, encodeErr)
			provider.WithToolCall("ToolSearch", `{"names":["delete_workspace"]}`).
				WithToolCall("delete_workspace", string(encoded)).
				WithText("Deletion request handled.")

			ctx := train1StartMainTurn(t, api, msgBus, mainID, "Delete the target workspace")

			var frame gen.ToolApprovalRequiredFrame
			select {
			case raw := <-tab.sendCh:
				require.NoError(t, json.Unmarshal(raw, &frame))
			case reply := <-msgBus.OutboundChan():
				t.Fatalf("delete_workspace finished without raising an approval: %s", reply.Content)
			case <-ctx.Done():
				t.Fatal("delete_workspace neither raised an approval nor finished")
			}
			assert.Equal(t, string(gen.WsFrameTypeToolApprovalRequired), frame.Type)
			assert.Equal(t, "delete_workspace", frame.ToolName)
			assert.Equal(t, "delete_workspace-0", frame.ToolCallId)
			assert.Equal(t, "omnipus", frame.AgentId)
			assert.Equal(t, args, frame.Args)
			pending := reg.pendingApprovals()
			require.Len(t, pending, 1, "the frame must correspond to one real pending approval")
			assert.Equal(t, pending[0].ApprovalID, frame.ApprovalId)
			assert.Equal(t, mainID, frame.SessionId, "approval must be reachable in Omnipus's real main chat")
			assert.NotEmpty(t, frame.TurnId)
			assert.Equal(t, 2, provider.CallCount(), "the model must wait for approval, not receive a fake completion")
			waiting, waitingErr := workspace.ReadState(api.homePath, targetID)
			require.NoError(t, waitingErr)
			assert.Equal(t, before, waiting, "nothing may be deleted before the user decides")

			resolved, gone := reg.resolve(frame.ApprovalId, action, false)
			require.True(t, resolved)
			require.False(t, gone)
			select {
			case reply := <-msgBus.OutboundChan():
				assert.Equal(t, "Deletion request handled.", reply.Content)
				assert.Equal(t, mainID, reply.SessionID)
			case <-ctx.Done():
				t.Fatal("the turn did not continue after the approval decision")
			}
			assert.Equal(t, 3, provider.CallCount())
			assert.Empty(t, reg.pendingApprovals())
			if action == ApprovalActionApprove {
				assert.False(t, workspace.Exists(api.homePath, targetID), "approval must execute the real delete tool")
			} else {
				after, afterErr := workspace.ReadState(api.homePath, targetID)
				require.NoError(t, afterErr)
				assert.Equal(t, before, after, "a refused deletion must leave the complete workspace state unchanged")
			}
		})
	}
}
