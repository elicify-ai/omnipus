package systools_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The token is read from the public tool, never computed with the production
// hash algorithm. The nil baseline value is deliberately not fabricated.
func wu3SharedRow(t *testing.T, d *systools.Deps, resource, id string) map[string]any {
	t.Helper()
	var tool tools.Tool
	list, key := "", ""
	switch resource {
	case "provider":
		tool, list, key = systools.NewProviderListTool(d), "providers", "name"
	case "mcp":
		tool, list, key = systools.NewMCPListTool(d), "servers", "name"
	case "channel":
		tool, list, key = systools.NewChannelListTool(d), "channels", "id"
	default:
		t.Fatalf("unknown fixture resource %s", resource)
	}
	body := wu3Success(t, tool.Execute(context.Background(), nil))
	items, ok := body[list].([]any)
	require.True(t, ok, "public %s list must be an array", list)
	for _, raw := range items {
		item, valid := raw.(map[string]any)
		require.True(t, valid)
		if item[key] == id {
			return item
		}
	}
	t.Fatalf("fixture %s %q is missing from its public read tool", resource, id)
	return nil
}

// FR-088 / §6.6: exact nonsecret projection, stable token, secret value is not
// an input to the token. Each materially changed field must change the token.
func TestWU3ProviderRevisionProjectionIsCompleteStableAndSecretFree(t *testing.T) {
	d, cfg := wu3Deps(t)
	p := &config.ModelConfig{Provider: "openai", Model: "fixture-model", APIBase: "https://original.wu3.invalid/v1", Protocol: "openai-compatible", APIKeyRef: "projection-ref"}
	cfg.Providers = []*config.ModelConfig{p}
	require.NoError(t, d.CredStore.Set("projection-ref", "wu3-secret-sentinel-old"))
	first := wu3SharedRow(t, d, "provider", "openai")
	assert.Equal(t, p.APIBase, first["api_base"])
	assert.Equal(t, p.Protocol, first["protocol"])
	assert.Equal(t, p.APIKeyRef, first["api_key_ref"])
	token, ok := first["revision"].(string)
	require.True(t, ok, "FR-088 read must return revision token")
	require.NotEmpty(t, token)
	assert.NotEqual(t, "absent", token)
	assert.Equal(t, first, wu3SharedRow(t, d, "provider", "openai"), "identical read stable")
	require.NoError(t, d.CredStore.Set("projection-ref", "wu3-secret-sentinel-new"))
	assert.Equal(t, token, wu3SharedRow(t, d, "provider", "openai")["revision"], "resolved values never enter token")
	for field, change := range map[string]func(){
		"endpoint":  func() { p.APIBase = "https://changed.wu3.invalid/v1" },
		"protocol":  func() { p.Protocol = "anthropic" },
		"reference": func() { p.APIKeyRef = "replacement-ref" },
	} {
		t.Run(field, func(t *testing.T) {
			before := wu3SharedRow(t, d, "provider", "openai")["revision"]
			change()
			after := wu3SharedRow(t, d, "provider", "openai")
			assert.NotEqual(t, before, after["revision"], "nonsecret field change bumps token")
			encoded, err := json.Marshal(after)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "wu3-secret-sentinel")
		})
	}
}

func TestWU3MCPRevisionIncludesEveryNonsecretConnectionField(t *testing.T) {
	d, cfg := wu3Deps(t)
	cfg.Tools.MCP.Servers["notion"] = config.MCPServerConfig{Enabled: false, Type: "stdio", Command: "fixture-command", Args: []string{"--one"}, EnvRefs: map[string]string{"KEY": "first-ref"}}
	first := wu3SharedRow(t, d, "mcp", "notion")
	revision, ok := first["revision"].(string)
	require.True(t, ok)
	require.NotEmpty(t, revision)
	assert.Equal(t, first, wu3SharedRow(t, d, "mcp", "notion"))
	for _, field := range []string{"command", "arguments", "transport", "url", "enabled", "reference"} {
		t.Run(field, func(t *testing.T) {
			before := wu3SharedRow(t, d, "mcp", "notion")["revision"]
			server := cfg.Tools.MCP.Servers["notion"]
			switch field {
			case "command":
				server.Command = "other-command"
			case "arguments":
				server.Args = []string{"--two"}
			case "transport":
				server.Type = "http"
			case "url":
				server.URL = "https://mcp.wu3.invalid"
			case "enabled":
				server.Enabled = !server.Enabled
			case "reference":
				server.EnvRefs = map[string]string{"KEY": "second-ref"}
			}
			cfg.Tools.MCP.Servers["notion"] = server
			assert.NotEqual(t, before, wu3SharedRow(t, d, "mcp", "notion")["revision"])
		})
	}
}

func TestWU3ChannelRevisionIncludesBindingEnabledAndReference(t *testing.T) {
	d, cfg := wu3Deps(t)
	cfg.Channels = map[string]config.ChannelInstanceConfig{"telegram": {Type: "telegram", Enabled: false, TokenRef: "first-ref"}}
	first := wu3SharedRow(t, d, "channel", "telegram")
	revision, ok := first["revision"].(string)
	require.True(t, ok)
	require.NotEmpty(t, revision)
	assert.Equal(t, first, wu3SharedRow(t, d, "channel", "telegram"))
	for _, field := range []string{"enabled", "reference", "binding"} {
		t.Run(field, func(t *testing.T) {
			before := wu3SharedRow(t, d, "channel", "telegram")["revision"]
			channel := cfg.Channels["telegram"]
			switch field {
			case "enabled":
				channel.Enabled = !channel.Enabled
			case "reference":
				channel.TokenRef = "second-ref"
			case "binding":
				channel.WorkspaceID = "sales"
				channel.Identity = &config.ChannelIdentity{Kind: "agent", ID: "mia"}
			}
			cfg.Channels["telegram"] = channel
			assert.NotEqual(t, before, wu3SharedRow(t, d, "channel", "telegram")["revision"])
		})
	}
	cfg.Channels = map[string]config.ChannelInstanceConfig{}
	assert.Equal(t, "absent", wu3SharedRow(t, d, "channel", "telegram")["revision"], "missing resource has exact absent token")
}

// FR-085 / BDD-11.9: a stale token must gate credentials as well as config.
func TestWU3SharedWritesRejectStaleAndMissingRevisionsBeforeSideEffects(t *testing.T) {
	for _, verb := range []string{"configure_provider", "add_mcp_server", "remove_mcp_server", "configure_channel", "enable_channel", "disable_channel"} {
		for _, condition := range []string{"stale", "missing"} {
			t.Run(verb+"_"+condition, func(t *testing.T) {
				d, cfg := wu3Deps(t)
				cfg.Providers = []*config.ModelConfig{{Provider: "openai", Model: "fixture-model", APIBase: "https://winner.wu3.invalid/v1", APIKeyRef: "winner-ref"}}
				cfg.Channels = map[string]config.ChannelInstanceConfig{"telegram": {Type: "telegram", Enabled: true, TokenRef: "channel-ref"}}
				cfg.Tools.MCP.Servers["notion"] = config.MCPServerConfig{Type: "stdio", Command: "winner-command", Enabled: false}
				require.NoError(t, d.CredStore.Set("winner-ref", "wu3-winner-value"))
				require.NoError(t, d.CredStore.Set("channel-ref", "wu3-channel-value"))
				require.NoError(t, config.SaveConfig(d.ConfigPath, cfg))
				configBefore := wu3ReadFile(t, d.ConfigPath)
				credsBefore := wu3ReadFile(t, d.Home+"/credentials.json")
				memoryBefore, err := json.Marshal(cfg)
				require.NoError(t, err)
				var tool tools.Tool
				args := map[string]any{}
				switch verb {
				case "configure_provider":
					tool = systools.NewProviderConfigureTool(d)
					args = map[string]any{"name": "openai", "api_key": "wu3-loser-key", "api_base": "https://loser.wu3.invalid/v1"}
				case "add_mcp_server":
					tool = systools.NewMCPAddTool(d)
					args = map[string]any{"name": "fresh-mcp", "transport": "stdio", "command": "new-command", "env": map[string]any{"KEY": "wu3-loser-key"}}
				case "remove_mcp_server":
					tool = systools.NewMCPRemoveTool(d)
					args = map[string]any{"name": "notion", "confirm": true}
				case "configure_channel":
					tool = systools.NewChannelConfigureTool(d)
					args = map[string]any{"id": "telegram", "token": "wu3-loser-key", "bot_id": "loser-bot"}
				case "enable_channel":
					tool = systools.NewChannelEnableTool(d)
					args = map[string]any{"id": "telegram"}
					ch := cfg.Channels["telegram"]
					ch.Enabled = false
					cfg.Channels["telegram"] = ch
					require.NoError(t, config.SaveConfig(d.ConfigPath, cfg))
					configBefore = wu3ReadFile(t, d.ConfigPath)
					memoryBefore, err = json.Marshal(cfg)
					require.NoError(t, err)
				case "disable_channel":
					tool = systools.NewChannelDisableTool(d)
					args = map[string]any{"id": "telegram"}
				}
				if condition == "stale" {
					args["expected_revision"] = "stale-revision-from-confirmed-plan"
				}
				result := tool.Execute(context.Background(), args)
				assert.True(t, result.IsError, "FR-085 %s must reject %s expected_revision: %s", verb, condition, result.ForLLM)
				assert.Equal(t, configBefore, wu3ReadFile(t, d.ConfigPath), "stale/missing precondition must precede config publication")
				assert.Equal(t, credsBefore, wu3ReadFile(t, d.Home+"/credentials.json"), "stale/missing precondition must precede credential writes")
				memoryAfter, err := json.Marshal(cfg)
				require.NoError(t, err)
				assert.Equal(t, memoryBefore, memoryAfter, "winner live state survives")
				if condition == "stale" {
					body := wu3Error(t, result, "CONFLICT")
					resource, _ := args["name"].(string)
					if resource == "" {
						resource, _ = args["id"].(string)
					}
					assert.Contains(t, body["message"], resource, "conflict names stale resource")
				} else {
					assert.Contains(t, result.ForLLM, "expected_revision", "missing precondition names required field")
				}
				wu3Outcome(t, result, "failed: nothing saved")
			})
		}
	}
}

func TestWU3AbsentProviderTokenDoesNotOverwriteNewSettingsEntry(t *testing.T) {
	d, cfg := wu3Deps(t)
	// The plan correctly carried absent; another writer then added the entry.
	cfg.Providers = []*config.ModelConfig{{Provider: "ollama", Model: "winner-model", APIBase: "http://winner.wu3.invalid/v1"}}
	require.NoError(t, config.SaveConfig(d.ConfigPath, cfg))
	before := wu3ReadFile(t, d.ConfigPath)
	result := systools.NewProviderConfigureTool(d).Execute(context.Background(), map[string]any{"name": "ollama", "api_base": "http://loser.wu3.invalid/v1", "expected_revision": "absent"})
	assert.True(t, result.IsError, "absent is a real precondition, not a bypass")
	assert.Equal(t, before, wu3ReadFile(t, d.ConfigPath))
	assert.Equal(t, "http://winner.wu3.invalid/v1", cfg.Providers[0].APIBase)
	wu3Outcome(t, result, "failed: nothing saved")
}
