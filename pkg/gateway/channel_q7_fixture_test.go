package gateway

// Q7 integration uses the real boot wiring, dedicated writer, registry, REST,
// persistence and consent. No test-side outbound callback or surrogate tool.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

const q7AlwaysAskText = "set_channel_outbound always asks for approval; it can be set to ask or deny, not allow"

func q7RequireOutboundTool(t *testing.T) {
	t.Helper()
	var target, control int
	for _, tool := range systools.AllTools(nil) {
		switch tool.Name() {
		case "connect_channel":
			control++
		case "set_channel_outbound":
			target++
		}
	}
	require.Equal(t, 1, control, "instrument: actual AllTools discovery sees the existing connection tool")
	if target == 0 {
		t.Fatal("BLOCKED: set_channel_outbound is not implemented/registered in AllTools — required by FR-030/FR-032/T-22/T-35")
	}
	require.Equal(t, 1, target, "FR-032: one canonical executable outbound tool")
}

func q7WireRealTools(t *testing.T, f *wu6Fixture) {
	t.Helper()
	// The method below is the actual production boot seam; the test never
	// constructs the missing writer closure or translates its errors itself.
	rc := &runContextWithOptions{
		ctx: context.Background(), homePath: f.f1277.home, configPath: f.f1277.configPath,
		cfg: f.al.GetConfig(), credStore: f.store, agentLoop: f.al,
		runningServices: f.svc,
	}
	rc.wireSystemTools()
	require.NotNil(t, f.api.builtinRegistry, "instrument: production wiring publishes the live central registry")
	_, control := f.api.builtinRegistry.Get("list_channels")
	require.True(t, control, "instrument: production-wired read tool exists")
	_, present := f.api.builtinRegistry.Get("set_channel_outbound")
	if !present {
		t.Fatal("BLOCKED: production wireSystemTools does not expose set_channel_outbound — required by FR-030/FR-032")
	}
	f.api.approvalReg = f.reg
}

func q7Fixture(t *testing.T, id, platform, boundAgent string) *wu6Fixture {
	t.Helper()
	q7RequireOutboundTool(t) // Missing feature is assertion-level RED before any unrelated fixture work.
	f := newWU6Fixture(t)
	t.Setenv("OMNIPUS_BEARER_TOKEN", t14Bearer)
	require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
		cfg.Gateway.DevModeBypass = false
		cfg.Sandbox.AutoApprove = false
		cfg.Tools.Manifest.Compressed = false
		cfg.Sandbox.ToolPolicies["set_channel_outbound"] = "ask" // T-10 stage-1 input; shipped default is checked separately.
		cfg.Channels[id] = config.ChannelInstanceConfig{
			Type: platform, Enabled: false, WorkspaceID: "q7-workspace",
			Identity: &config.ChannelIdentity{Kind: "agent", ID: boundAgent},
			Outbound: &config.ChannelOutboundConfig{Policy: "reply_only", Targets: []string{}},
		}
		return config.SaveConfig(f.f1277.configPath, cfg)
	}))
	q7WireRealTools(t, f)
	control := t14Request(t, f, http.MethodGet, "/api/v1/channels", "", true)
	require.Equal(t, http.StatusOK, control.Code, "instrument: real authenticated channel route")
	return f
}

func q7Args(id, policy, revision string, targets []string) map[string]any {
	// Model tool arguments are JSON values. Keep their array representation
	// JSON-native so exact card-argument assertions compare values, not a
	// test-only []string type that disappears at the real transport boundary.
	items := make([]any, len(targets))
	for i, target := range targets {
		items[i] = target
	}
	return map[string]any{"id": id, "policy": policy, "targets": items, "expected_revision": revision}
}

func q7ExecuteAs(t *testing.T, f *wu6Fixture, caller string, args map[string]any) *tools.ToolResult {
	t.Helper()
	tool, present := f.api.builtinRegistry.Get("set_channel_outbound")
	require.True(t, present, "FR-032: real production-wired tool remains registered")
	ctx := tools.WithAgentID(context.Background(), caller)
	ctx = tools.WithTranscriptSessionID(ctx, "q7-direct-tool-session")
	result := tool.Execute(ctx, args)
	require.NotNil(t, result, "FR-030: execution returns a structured tool result")
	return result
}

func q7Execute(t *testing.T, f *wu6Fixture, args map[string]any) *tools.ToolResult {
	t.Helper()
	return q7ExecuteAs(t, f, string(coreagent.IDOmnipus), args)
}

func q7Error(t *testing.T, result *tools.ToolResult, code string) map[string]any {
	t.Helper()
	require.True(t, result.IsError, "FR-030: expected %s, got %s", code, result.ForLLM)
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &body))
	require.Equal(t, false, body["success"], "FR-030: errorJSON envelope")
	failure, ok := body["error"].(map[string]any)
	require.True(t, ok, "FR-030: structured error, not a raw generic failure")
	require.Equal(t, code, failure["code"], "FR-030: exact error class")
	return failure
}

func q7ConfigBytes(t *testing.T, f *wu6Fixture) []byte {
	t.Helper()
	raw, err := os.ReadFile(f.f1277.configPath)
	require.NoError(t, err)
	return raw
}
