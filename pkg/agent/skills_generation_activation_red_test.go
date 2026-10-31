package agent

// r15 SD-30 / S-6.12 / T-K2: real registered Skill tool, durable binding
// snapshot, old live instance, reconstruction from disk and a real store
// regrant. The initial raw row is fixture input (NOT proof of binding at
// persist). Only the model transport is replaced; no resolver is injected.
// Q-S3 answered, 2026-10-31: managed deletion removes grants best effort.
// Outside filesystem replacement bypasses that hygiene; SD-30 must still
// reject the surviving G1 binding independently of cleanup.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sd30LoopFromDurableRow(t *testing.T, home string) (*AgentLoop, *AgentInstance) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "entities", "agents", "menu-agent.json"))
	require.NoError(t, err)
	var a config.AgentConfig
	require.NoError(t, json.Unmarshal(raw, &a))
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.List = []config.AgentConfig{a}
	mb := bus.NewMessageBus()
	t.Cleanup(func() { mb.Close() })
	al := mustNewAgentLoop(t, cfg, mb, &mockProvider{})
	t.Cleanup(func() { al.Close() })
	inst, ok := al.GetRegistry().GetAgent("menu-agent")
	require.True(t, ok)
	_, ok = inst.Tools.Get("Skill")
	require.True(t, ok, "actual loop registers and wires Skill")
	return al, inst
}

func sd30ActualLoad(t *testing.T, inst *AgentInstance) *tools.ToolResult {
	t.Helper()
	tool, ok := inst.Tools.Get("Skill")
	require.True(t, ok)
	ctx := tools.WithTranscriptSessionID(tools.WithAgentID(context.Background(), inst.ID), "generation-activation-session")
	return tool.Execute(ctx, map[string]any{"name": "foo"})
}

func TestSD30_ActualSkillStaleInstanceRestartAndRealRegrant(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvBuiltinSkills, t.TempDir())
	root := filepath.Join(home, "skills")
	dir := menuSkill(t, root, "foo", "ORIGINAL-G1", menuG1)
	store := agentstore.New(home)
	require.NoError(t, store.Create("menu-agent", &config.AgentConfig{ID: "menu-agent", Name: "Menu Agent", Home: filepath.Join(home, "agents", "menu-agent"), Skills: []string{"foo"}}))
	path := filepath.Join(home, "entities", "agents", "menu-agent.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var row map[string]any
	require.NoError(t, json.Unmarshal(raw, &row))
	row["skill_grants"] = map[string]string{"foo": menuG1}
	before, err := json.Marshal(row)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, before, 0o600))
	al, old := sd30LoopFromDurableRow(t, home)
	first := sd30ActualLoad(t, old)
	require.False(t, first.IsError, "positive control: registered matching-generation Skill load succeeds: %s", first.ForLLM)
	require.Contains(t, first.ForLLM, "Body.")
	// This is an outside filesystem operation, not ApplyChange deletion:
	// no managed hygiene runs, so the durable G1 row remains a safety fixture.
	require.NoError(t, os.RemoveAll(dir))
	menuSkill(t, root, "foo", "RECREATED-G2", menuG2)
	const stale = "skill 'foo' is not available: your grant is for a previous skill with this name that no longer exists; ask the owner to re-grant the current 'foo'"
	res := sd30ActualLoad(t, old)
	assert.True(t, res.IsError, "old live instance cannot load the recreation")
	assert.Contains(t, res.ForLLM, "SKILL_GRANT_STALE", "SD-30 rejects an old binding even when no managed hygiene ran")
	assert.Contains(t, res.ForLLM, stale, "exact §5.10 stale-generation refusal text")
	assert.NotContains(t, old.ContextBuilder.BuildSystemPromptWithCache(), "RECREATED-G2")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after, "outside filesystem replacement and read-only activation do not rewrite durable grant rows")
	al.Close()
	_, restarted := sd30LoopFromDurableRow(t, home)
	res = sd30ActualLoad(t, restarted)
	assert.True(t, res.IsError, "restart reconstructs the inert G1 binding from durable bytes")
	assert.Contains(t, res.ForLLM, stale)
	_, err = store.Update("menu-agent", func(a *config.AgentConfig) error { a.Skills = []string{"foo"}; return nil })
	require.NoError(t, err, "real explicit skills persist is the only recovery used")
	raw, err = os.ReadFile(path)
	require.NoError(t, err)
	row = nil // Decode independently: absent keys must not survive from fixture input.
	require.NoError(t, json.Unmarshal(raw, &row))
	assert.Equal(t, map[string]any{"foo": menuG2}, row["skill_grants"], "real regrant binds the live G2")
	_, regranted := sd30LoopFromDurableRow(t, home)
	res = sd30ActualLoad(t, regranted)
	assert.False(t, res.IsError, "regrant to G2 recovers actual activation: %s", res.ForLLM)
	assert.Contains(t, res.ForLLM, "Body.")
}
