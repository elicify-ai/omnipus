package systools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

// WU-3 fixtures use real stores and tool entry points. No production data or
// paid network is reachable; only filesystem/network fault edges are doubled.
func wu3Deps(t *testing.T) (*systools.Deps, *config.Config) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_MASTER_KEY", "0000000000000000000000000000000000000000000000000000000000000000")
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = home
	cfg.Agents.List = []config.AgentConfig{}
	cfg.Providers = []*config.ModelConfig{}
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{}
	path := filepath.Join(home, "config.json")
	require.NoError(t, config.SaveConfig(path, cfg))
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, credentials.Unlock(store))
	var mu sync.Mutex
	d := &systools.Deps{
		Home: home, ConfigPath: path, CredStore: store,
		GetCfg: func() *config.Config { return cfg },
		MutateConfig: func(fn func(*config.Config) error) error {
			mu.Lock()
			defer mu.Unlock()
			return fn(cfg)
		},
		SaveConfigLocked: func(c *config.Config) error { return config.SaveConfig(path, c) },
	}
	d.SkillWriter = skills.NewSkillWriter(filepath.Join(home, "skills"))
	d.SkillsLoader = skills.NewSkillsLoader(home, filepath.Join(home, "skills"), "")
	installer, err := skills.NewSkillInstaller(home, "", "")
	require.NoError(t, err)
	d.SkillInstaller = installer
	return d, cfg
}

func wu3JSON(t *testing.T, result *tools.ToolResult) map[string]any {
	t.Helper()
	require.NotNil(t, result, "tool must return a result")
	var value map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &value), "tool must return structured JSON")
	require.NotNil(t, value, "null is not a tool result object")
	return value
}

func wu3Object(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	require.True(t, ok, "expected JSON object, got %T", value)
	return object
}

func wu3String(t *testing.T, value any) string {
	t.Helper()
	text, ok := value.(string)
	require.True(t, ok, "expected JSON string, got %T", value)
	return text
}

func wu3Success(t *testing.T, result *tools.ToolResult) map[string]any {
	t.Helper()
	value := wu3JSON(t, result)
	require.False(t, result.IsError, "expected tool success, got %s", result.ForLLM)
	return value
}

func wu3Error(t *testing.T, result *tools.ToolResult, code string) map[string]any {
	t.Helper()
	value := wu3JSON(t, result)
	require.True(t, result.IsError, "expected %s refusal, got %s", code, result.ForLLM)
	if nested, ok := value["error"].(map[string]any); ok {
		value = nested // canonical errorJSON envelope; save failures use a flat payload
	}
	require.Equal(t, code, value["code"], "specific refusal code")
	return value
}

func wu3CreateArgs(name string) map[string]any {
	return map[string]any{"name": name, "description": "WU-3 isolated native fixture", "soul": "Help with the requested task.", "model": "fixture-model", "color": "#6E8BA8"}
}

func wu3CreateAgent(t *testing.T, d *systools.Deps, cfg *config.Config, name string) string {
	t.Helper()
	value := wu3Success(t, systools.NewAgentCreateTool(d).Execute(context.Background(), wu3CreateArgs(name)))
	id, ok := value["id"].(string)
	require.True(t, ok, "creation returns identity")
	state, err := agentstore.New(d.Home).ReadState(id)
	require.NoError(t, err)
	cfg.Agents.List = append(cfg.Agents.List, *state.Agent)
	return id
}

// Seed a PRE-EXISTING, valid UUID with guaranteed letters for casing probes.
// A randomly generated all-digit UUID must not turn this test into a flake.
func wu3CaseAgent(t *testing.T, d *systools.Deps, cfg *config.Config) string {
	t.Helper()
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	ac := &config.AgentConfig{ID: id, Name: "Case fixture", Type: config.AgentTypeCustom}
	_, err := agentstore.New(d.Home).CreateState(id, ac, "Case fixture soul.")
	require.NoError(t, err)
	state, err := agentstore.New(d.Home).ReadState(id)
	require.NoError(t, err)
	cfg.Agents.List = append(cfg.Agents.List, *state.Agent)
	return id
}

func wu3AgentRead(t *testing.T, d *systools.Deps, id string) map[string]any {
	t.Helper()
	return wu3Success(t, systools.NewAgentGetTool(d).Execute(context.Background(), map[string]any{"id": id}))
}

func wu3Workspace(t *testing.T, d *systools.Deps, id string, members ...string) workspace.State {
	t.Helper()
	require.NoError(t, workspace.SaveRecord(d.Home, workspace.Workspace{ID: id, Name: id, Status: "active", IsDefault: id == "default", CoreTeam: members}))
	state, err := workspace.ReadState(d.Home, id)
	require.NoError(t, err)
	return state
}

func wu3ReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

func wu3Outcome(t *testing.T, result *tools.ToolResult, want string) map[string]any {
	t.Helper()
	value := wu3JSON(t, result)
	require.Equal(t, want, value["outcome"], "FR-030; binding WU3 tool-shape ruling Q2")
	_, scopeOK := value["check_scope"].(string)
	require.True(t, scopeOK, "check_scope must state what was actually checked")
	for _, field := range []string{"not_tested", "missing_parts"} {
		_, ok := value[field].([]any)
		require.True(t, ok, "%s must be an array, never null", field)
	}
	if want == "saved but not working" || want == "partly saved" {
		reason, ok := value["reason"].(string)
		require.True(t, ok)
		require.NotEmpty(t, reason, "a nonworking/partial result must explain why")
	}
	return value
}
