package systools_test

// Oracle: r17 §7.5 / §7.10 / 7.1–7.2. These use the real tool registry,
// protected entity store, revision gate and AGENT.md file, not mock writes.
import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateAgent_StoresAllParityFieldsAndRevision(t *testing.T) {
	d, store := t8ReadFixture(t)
	before, err := store.ReadState("r1")
	require.NoError(t, err)
	reg := tools.NewToolRegistry()
	reg.Register(systools.NewAgentUpdateTool(d))
	result := reg.Execute(tools.WithAgentID(context.Background(), "omnipus"), "update_agent", map[string]any{
		"id": "r1", "revision": before.Revision, "name": "Revised R1",
		"model": "new-primary", "provider": "openrouter", "max_tool_iterations": 41,
		"fallback_models": []any{map[string]any{"provider": "openrouter", "model": "new-first"}, map[string]any{"provider": "missing-provider", "model": "new-second"}},
		"agent_md":        "T8 instructions: verify the changed agent",
	})
	require.False(t, result.IsError, "7.1 every supported parity field is saved: %s", result.ForLLM)
	after, err := store.ReadState("r1")
	require.NoError(t, err)
	assert.NotEqual(t, before.Revision, after.Revision)
	assert.Equal(t, "Revised R1", after.Agent.Name)
	require.NotNil(t, after.Agent.Model)
	assert.Equal(t, "new-primary", after.Agent.Model.Primary)
	assert.Equal(t, "openrouter", after.Agent.Model.Provider)
	assert.Equal(t, 41, after.Agent.MaxToolIterations)
	assert.Equal(t, config.FallbackModelSlice{{Provider: "openrouter", Model: "new-first"}, {Provider: "missing-provider", Model: "new-second"}}, after.Agent.FallbackModels)
	md, err := os.ReadFile(filepath.Join(d.Home, "agents", "r1", "AGENT.md"))
	require.NoError(t, err)
	assert.Equal(t, "T8 instructions: verify the changed agent", string(md))
}

func TestUpdateAgent_StaleRevisionLeavesFilesUnchanged(t *testing.T) {
	d, store := t8ReadFixture(t)
	v1, err := store.ReadState("r1")
	require.NoError(t, err)
	_, err = store.MutateState("r1", v1.Revision, func(a *config.AgentConfig) error { a.Name = "Other writer V2"; return nil }, nil)
	require.NoError(t, err)
	v2, err := store.ReadState("r1")
	require.NoError(t, err)
	require.NotEqual(t, v1.Revision, v2.Revision, "instrument: stale revision is genuinely stale")
	entity := filepath.Join(d.Home, "entities", "agents", "r1.json")
	before, err := os.ReadFile(entity)
	require.NoError(t, err)
	reg := tools.NewToolRegistry()
	reg.Register(systools.NewAgentUpdateTool(d))
	result := reg.Execute(tools.WithAgentID(context.Background(), "omnipus"), "update_agent", map[string]any{"id": "r1", "revision": v1.Revision, "name": "Must never land"})
	require.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "CONFLICT", "7.2 stale edit returns CONFLICT")
	after, err := os.ReadFile(entity)
	require.NoError(t, err)
	assert.Equal(t, before, after, "7.2 no entity bytes changed")
	fresh, err := store.ReadState("r1")
	require.NoError(t, err)
	assert.Equal(t, v2.Revision, fresh.Revision)
	assert.Equal(t, "Other writer V2", fresh.Agent.Name)
}
