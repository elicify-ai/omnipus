package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
)

// TestBuildEnabledRefMap_IncludesNonChannelRefsWhenEnabled pins Task 2: the
// non-channel categories credentials.ResolveAll can produce a bundle error
// for (voice, web-search tools, skill marketplaces — see
// pkg/credentials/inject.go's nonChannelRefs) must be included in
// buildEnabledRefMap's "in use" set when the owning feature is enabled, and
// excluded when it is disabled — mirroring the channel Enabled gate that
// already existed.
func TestBuildEnabledRefMap_IncludesNonChannelRefsWhenEnabled(t *testing.T) {
	cfg := &config.Config{
		Channels: map[string]config.ChannelInstanceConfig{
			"telegram": {Enabled: true, TokenRef: "TELEGRAM_REF"},
			"discord":  {Enabled: false, TokenRef: "DISCORD_REF"},
		},
	}
	cfg.Voice.ElevenLabsAPIKeyRef = "ELEVENLABS_REF"
	cfg.Voice.GroqAPIKeyRef = "GROQ_VOICE_REF"
	cfg.Tools.Web.Brave = config.BraveConfig{Enabled: true, APIKeyRef: "BRAVE_REF"}
	cfg.Tools.Web.Tavily = config.TavilyConfig{Enabled: false, APIKeyRef: "TAVILY_REF"}
	cfg.Tools.Skills.Marketplaces = []config.MarketplaceConfig{
		{Name: "clawhub", Type: "clawhub", Enabled: true, AuthTokenRef: "CLAWHUB_REF"},
		{Name: "github", Type: "github", Enabled: false, TokenRef: "GITHUB_REF"},
	}
	// BUG 4 (architect finding): MCP server env-var refs must be gated at
	// BOTH the per-server level (srv.Enabled) and the global kill-switch
	// level (cfg.Tools.MCP.Enabled) — a server marked Enabled under a
	// globally-disabled tools.mcp.enabled never actually connects, so its
	// ref must not read as "in use" any more than a disabled channel's does.
	cfg.Tools.MCP.Enabled = true
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"mcp-enabled":  {Enabled: true, Type: "stdio", Command: "npx", EnvRefs: map[string]string{"TOKEN": "MCP_ENABLED_REF"}},
		"mcp-disabled": {Enabled: false, Type: "stdio", Command: "npx", EnvRefs: map[string]string{"TOKEN": "MCP_DISABLED_REF"}},
	}

	m := buildEnabledRefMap(cfg)

	assert.True(t, m["TELEGRAM_REF"], "enabled channel ref must be in the map")
	assert.False(t, m["DISCORD_REF"], "disabled channel ref must NOT be in the map")

	assert.True(t, m["ELEVENLABS_REF"], "voice refs have no separate enabled toggle — ref presence IS in-use")
	assert.True(t, m["GROQ_VOICE_REF"], "voice refs have no separate enabled toggle — ref presence IS in-use")

	assert.True(t, m["BRAVE_REF"], "enabled web-search tool ref must be in the map")
	assert.False(t, m["TAVILY_REF"], "disabled web-search tool ref must NOT be in the map")

	assert.True(t, m["CLAWHUB_REF"], "enabled marketplace ref must be in the map")
	assert.False(t, m["GITHUB_REF"], "disabled marketplace ref must NOT be in the map")

	assert.True(t, m["MCP_ENABLED_REF"], "an enabled MCP server's env ref must be in the map")
	assert.False(t, m["MCP_DISABLED_REF"], "a disabled MCP server's env ref must NOT be in the map")
}

// TestBuildEnabledRefMap_MCPGlobalKillSwitchGatesAllServers proves the second
// half of the MCP gate: even a per-server Enabled=true entry must NOT read as
// "in use" when the global tools.mcp.enabled kill-switch is off, mirroring
// ReconcileMCP's own desired-set gating (pkg/agent/loop_mcp.go: mcpCfg.Enabled
// gates the whole loop before any per-server Enabled check).
func TestBuildEnabledRefMap_MCPGlobalKillSwitchGatesAllServers(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.MCP.Enabled = false
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"mcp-enabled": {Enabled: true, Type: "stdio", Command: "npx", EnvRefs: map[string]string{"TOKEN": "MCP_REF"}},
	}

	m := buildEnabledRefMap(cfg)

	assert.False(t, m["MCP_REF"],
		"an MCP server's ref must not be 'in use' when the global tools.mcp.enabled kill-switch is off")
}
