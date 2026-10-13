package systools_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WU-25/#1303 exercises the real tool registry (including argument validation),
// the seeded Omnipus policy, and the entity/SOUL store. Optional model/runtime
// activation hooks are absent; no network, CLI, or agent turn is claimed here.
// No existing agent*_test.go helper is used: those files have concurrent owners.
type wu25Harness struct {
	home     string
	cfg      *config.Config
	deps     *systools.Deps
	registry *tools.ToolRegistry
}

func newWU25Harness(t *testing.T) *wu25Harness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	cfg := config.DefaultConfig()
	coreagent.SeedConfig(cfg)
	configPath := filepath.Join(home, "config.json")
	require.NoError(t, config.SaveConfig(configPath, cfg))
	h := &wu25Harness{home: home, cfg: cfg}
	h.deps = &systools.Deps{
		Home: home, ConfigPath: configPath,
		GetCfg:           func() *config.Config { return cfg },
		SaveConfigLocked: func(c *config.Config) error { return config.SaveConfig(configPath, c) },
	}
	h.reopen(t)
	return h
}

func (h *wu25Harness) reopen(t *testing.T) {
	t.Helper()
	var caller *config.AgentConfig
	for i := range h.cfg.Agents.List {
		if h.cfg.Agents.List[i].ID == "omnipus" {
			caller = &h.cfg.Agents.List[i]
			break
		}
	}
	if caller == nil || caller.Tools == nil {
		t.Fatal("BLOCKED: seeded Omnipus role not implemented — required by WU-25 real caller path and FR-001/FR-007")
	}
	global := make(map[string]config.ToolPolicy, len(h.cfg.Sandbox.ToolPolicies))
	for name, policy := range h.cfg.Sandbox.ToolPolicies {
		global[name] = config.ToolPolicy(policy)
	}
	// BuildRegistry uses AllTools, the same tools WireSysagentDeps installs.
	// FilterToolsByPolicy is the real turn's sent-definition filter; registration
	// then mirrors wireSysagentDepsLocked's RegisterReplacing on a fresh registry.
	all := systools.BuildRegistry(h.deps)
	allowed, policies := tools.FilterToolsByPolicy(all.GetAll(), string(caller.Type), &tools.ToolPolicyCfg{
		Policies: caller.Tools.Builtin.Policies, GlobalPolicies: global,
	})
	h.registry = tools.NewToolRegistry()
	for _, tool := range allowed {
		h.registry.RegisterReplacing(tool)
	}
	for _, name := range []string{"create_agent", "update_agent", "get_agent"} {
		_, found := h.registry.Get(name)
		if !found {
			t.Fatalf("BLOCKED: callable %s for Omnipus not implemented — required by WU-25/#1303", name)
		}
		require.Equal(t, "allow", policies[name], "Omnipus must really have the visual API, not merely metadata")
	}
}

func (h *wu25Harness) call(t *testing.T, name string, args map[string]any) *tools.ToolResult {
	t.Helper()
	ctx := tools.WithAgentID(context.Background(), "omnipus")
	result := h.registry.ExecuteWithContext(ctx, name, args, "webchat", "wu25-visuals", nil)
	require.NotNil(t, result, "%s must return a model-visible result", name)
	return result
}

func wu25Success(t *testing.T, result *tools.ToolResult) map[string]any {
	t.Helper()
	require.False(t, result.IsError, "WU-25: valid visual request must be accepted: %s", result.ForLLM)
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &body), "successful result must be JSON")
	require.NotNil(t, body)
	return body
}

func (h *wu25Harness) read(t *testing.T, id string) map[string]any {
	t.Helper()
	return wu25Success(t, h.call(t, "get_agent", map[string]any{"id": id}))
}

func (h *wu25Harness) state(t *testing.T, id string) *agentstore.State {
	t.Helper()
	state, err := agentstore.New(h.home).ReadState(id)
	require.NoError(t, err)
	return state
}

// The named literals are from the existing contracts, not observed tool output:
// Robot / writer / Bronze are deliberately nondefault visual values.
func wu25CreateArgs(name, runtimeType string) map[string]any {
	return map[string]any{
		"name": name, "description": "Keep the original mandate.",
		"soul": "Only prepare drafts; never change permissions.", "model": "test/model",
		"agent_type": runtimeType, "color": "#A97142",
	}
}

func wu25SeedAgent(t *testing.T, h *wu25Harness, runtimeType string) string {
	t.Helper()
	id := "visual-subject"
	ac := config.AgentConfig{
		ID: id, Name: "Visual Subject", Description: "Keep the original mandate.",
		Type: config.AgentTypeCustom, Figure: "Robot", Role: "writer", Color: "#A97142",
		Model:  &config.AgentModelConfig{Primary: "test/model", Provider: "openrouter"},
		Skills: []string{"unchanged-skill"}, Tools: &config.AgentToolsCfg{
			Builtin: config.AgentBuiltinToolsCfg{Policies: map[string]config.ToolPolicy{
				"bash": config.ToolPolicyDeny, "read_file": config.ToolPolicyAllow,
			}},
			MCP: config.AgentMCPToolsCfg{Servers: []config.AgentMCPServerBinding{
				{ID: "unchanged-connector", Tools: []string{"read"}, ToolsSpecified: true},
			}},
		},
	}
	if runtimeType != "Main" {
		ac.Type = config.AgentTypeWorker
	}
	if runtimeType == "subagent_3p" {
		// Stored runtime descriptor only. This fixture never launches this program.
		ac.Subagents = &config.SubagentsConfig{Executor: &config.ExecutorConfig{
			Kind: config.ExecutorKindExternalCLI, CLI: "codex", CLIPath: filepath.Join(h.home, "unused-cli"),
		}}
	}
	wu25StoreAgent(t, h, ac)
	return id
}

func wu25StoreAgent(t *testing.T, h *wu25Harness, ac config.AgentConfig) {
	t.Helper()
	_, err := agentstore.New(h.home).CreateState(ac.ID, &ac, "Only prepare drafts; never change permissions.")
	require.NoError(t, err)
	heartbeat := filepath.Join(h.home, "agents", ac.ID, "HEARTBEAT.md")
	require.NoError(t, os.WriteFile(heartbeat, []byte("Keep the original heartbeat mandate.\n"), 0o600))
}

func wu25AssertVisuals(t *testing.T, body map[string]any, want map[string]string) {
	t.Helper()
	for _, field := range []string{"figure", "role", "color"} {
		got, exists := body[field]
		assert.True(t, exists, "#1303: get_agent omits saved %s; omission is not a default", field)
		assert.Equal(t, want[field], got, "#1303: exact saved %s readback", field)
	}
}

func wu25StoredVisuals(a *config.AgentConfig) map[string]string {
	return map[string]string{"figure": a.Figure, "role": a.Role, "color": a.Color}
}

func wu25StableConfig(a *config.AgentConfig) config.AgentConfig {
	stable := *a
	stable.Figure, stable.Role, stable.Color = "", "", ""
	stable.UpdatedAt = nil // A successful edit may update this bookkeeping timestamp.
	return stable
}

// Compare every durable file, not only the selected visual: a rejected mixed
// write must not modify a prompt, config, heartbeat, or other entity. Sidecar
// locks are coordination artifacts, not durable product data.
func wu25Snapshot(t *testing.T, home string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".lock") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		files[path] = string(data)
		return nil
	})
	require.NoError(t, err, "zero-write check must not hide unreadable files")
	require.NotEmpty(t, files, "snapshot instrument must see the saved config/fixture")
	return files
}
