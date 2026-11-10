package systools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Chief 18:05Z exact-ID rule: a file system alias is never another public
// identity. Every write must reject it before side effects, even on macOS.
func TestWU3AgentWritesRejectCaseAliasBeforeAnySideEffect(t *testing.T) {
	for _, verb := range []string{"update_agent", "set_agent_permissions", "delete_agent"} {
		t.Run(verb, func(t *testing.T) {
			d, cfg := wu3Deps(t)
			installLiveSessionStore(t, d, d.Home)
			id := wu3CaseAgent(t, d, cfg)
			before := wu3AgentRead(t, d, id)
			args := map[string]any{"id": strings.ToUpper(id), "revision": before["revision"]}
			var tool tools.Tool
			switch verb {
			case "update_agent":
				tool = systools.NewAgentUpdateTool(d)
				args["description"] = "Must not land"
			case "set_agent_permissions":
				tool = systools.NewAgentPermissionsTool(d)
				args["tool_policy_changes"] = map[string]any{"set": map[string]any{"read_file": "allow"}}
			case "delete_agent":
				tool = systools.NewAgentDeleteTool(d)
				args["confirm"] = true
			}
			result := tool.Execute(context.Background(), args)
			wu3Error(t, result, "AGENT_NOT_FOUND")
			assert.Equal(t, before, wu3AgentRead(t, d, id), "different spelling cannot modify/delete canonical identity")
		})
	}
}

// FR-085/088 resource is the CHANNEL INSTANCE, not its platform type. Two
// instances of Telegram must not share a token or mutate each other's enable.
func TestWU3ChannelInstanceRevisionsAndWritesAreIndependentlyScoped(t *testing.T) {
	d, cfg := wu3Deps(t)
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"telegram.eu": {Type: "telegram", Enabled: false, WorkspaceID: "sales", Identity: &config.ChannelIdentity{Kind: "agent", ID: "mia"}},
		"telegram.us": {Type: "telegram", Enabled: true, WorkspaceID: "sales", Identity: &config.ChannelIdentity{Kind: "agent", ID: "jim"}},
	}
	beforeEU := wu3SharedRow(t, d, "channel", "telegram.eu")
	beforeUS := wu3SharedRow(t, d, "channel", "telegram.us")
	require.NotEqual(t, beforeEU["revision"], beforeUS["revision"], "instance identity/binding enters token")
	result := systools.NewChannelEnableTool(d).Execute(context.Background(), map[string]any{"id": "telegram.eu", "expected_revision": beforeEU["revision"]})
	wu3Success(t, result)
	assert.True(t, cfg.Channels["telegram.eu"].Enabled)
	assert.True(t, cfg.Channels["telegram.us"].Enabled, "untouched sibling stays enabled")
	assert.Equal(t, beforeUS, wu3SharedRow(t, d, "channel", "telegram.us"), "sibling token does not bump")
	assert.NotEqual(t, beforeEU["revision"], wu3SharedRow(t, d, "channel", "telegram.eu")["revision"])
	stale := systools.NewChannelDisableTool(d).Execute(context.Background(), map[string]any{"id": "telegram.eu", "expected_revision": beforeEU["revision"]})
	wu3Error(t, stale, "CONFLICT")
	assert.True(t, cfg.Channels["telegram.eu"].Enabled, "stale disable cannot undo winner")
}
