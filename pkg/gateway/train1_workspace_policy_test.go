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

// Founder 2026-10-18: creation stays allowed; deletion is denied pending
// #1363. Only model/browser transport is scripted. Seed, discovery, policy,
// registered tool bodies, workspace storage and the approval adapter are real.
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

// Founder 2026-10-18: "delete later, track" (#1363). A fresh Omnipus
// cannot turn deletion into an approval request; the Deny refuses it first.
func TestTrain1OmnipusWorkspaceTools_DeleteDeniedWithoutApproval(t *testing.T) {
	for _, defaultTarget := range []bool{false, true} {
		t.Run(map[bool]string{false: "non_default", true: "default"}[defaultTarget], func(t *testing.T) {
			provider := testutil.NewScenario()
			api, reg, tab, msgBus, mainID := train1WorkspacePolicyHarness(t, provider)
			targetID, err := workspace.ResolveDefaultID(api.homePath)
			require.NoError(t, err)
			if !defaultTarget {
				targetID = train1ChannelDeleteTarget
				train1SeedBoundDeletionChannels(t, api)
				unlock := workspace.LockID(targetID)
				err = workspace.SaveDelegation(api.homePath, targetID, []workspace.DelegationEdge{
					{FromAgent: "mia", ToAgent: "mia", Modes: []workspace.DelegationMode{workspace.ModeDirect}},
				})
				unlock()
				require.NoError(t, err)
			}
			before, err := workspace.ReadState(api.homePath, targetID)
			require.NoError(t, err)
			configBefore, err := api.agentLoop.GetConfig().Clone()
			require.NoError(t, err)
			files := train1DefaultDeleteSentinels(t, api.homePath, targetID)
			args, err := json.Marshal(map[string]any{"id": targetID, "revision": before.Revision, "confirm": true})
			require.NoError(t, err)
			provider.WithToolCall("ToolSearch", `{"names":["delete_workspace"]}`).
				WithToolCall("delete_workspace", string(args)).WithText("Use workspace Settings to delete it.")
			ctx := train1StartMainTurn(t, api, msgBus, mainID, "Delete the workspace")
			select {
			case raw := <-tab.sendCh:
				var unexpected gen.ToolApprovalRequiredFrame
				require.NoError(t, json.Unmarshal(raw, &unexpected))
				reg.resolve(unexpected.ApprovalId, ApprovalActionCancel, false)
				t.Fatalf("#1363: a denied deletion must not ask for approval: %s", raw)
			case reply := <-msgBus.OutboundChan():
				require.Equal(t, "Use workspace Settings to delete it.", reply.Content)
				require.Equal(t, mainID, reply.SessionID)
			case <-ctx.Done():
				t.Fatal("denied deletion did not return")
			}
			require.Equal(t, 3, provider.CallCount())
			assert.Empty(t, tab.sendCh, "no deferred approval may be emitted")
			assert.Empty(t, reg.pendingApprovals())
			reg.mu.Lock()
			requests := len(reg.entries)
			reg.mu.Unlock()
			assert.Zero(t, requests, "Deny must not enter the approval registry")
			var denial gen.PermissionDenied
			found := 0
			for _, message := range provider.LastMessages() {
				if message.Role == "tool" && message.ToolCallID == "delete_workspace-0" {
					found++
					require.NoError(t, json.Unmarshal([]byte(message.Content), &denial))
				}
			}
			require.Equal(t, 1, found, "the actual policy refusal must reach the model")
			assert.Equal(t, "permission_denied", denial.Error)
			assert.Equal(t, "policy_denied", denial.Reason)
			assert.Equal(t, "delete_workspace", denial.Tool)
			assert.True(t, denial.Permanent)
			after, err := workspace.ReadState(api.homePath, targetID)
			require.NoError(t, err)
			assert.Equal(t, before, after, "no workspace mutation on a denied tool call")
			assert.Equal(t, configBefore.Channels, api.agentLoop.GetConfig().Channels, "denied deletion cannot unbind or disable channels")
			train1AssertDefaultDeleteFilesUnchanged(t, files)
		})
	}
}
