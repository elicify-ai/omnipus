package systools_test

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder ruling 2026-10-14T17:10Z: IDs random UUIDs; names free labels.
func TestWU3AgentCreationUsesRandomUUIDAndPreservesNameVerbatim(t *testing.T) {
	d, _ := wu3Deps(t)
	label := "MiXeD Case Partner"
	tool := systools.NewAgentCreateTool(d)
	first := wu3Success(t, tool.Execute(context.Background(), wu3CreateArgs(label)))
	second := wu3Success(t, tool.Execute(context.Background(), wu3CreateArgs(label)))
	firstID, ok := first["id"].(string)
	require.True(t, ok)
	secondID, ok := second["id"].(string)
	require.True(t, ok)
	for _, id := range []string{firstID, secondID} {
		parsed, err := uuid.Parse(id)
		require.NoError(t, err, "new identity must be UUID, never slugged label")
		assert.Equal(t, uuid.Version(4), parsed.Version(), "UUID is randomly generated")
		assert.Equal(t, label, wu3AgentRead(t, d, id)["name"], "case and spacing are preserved")
	}
	assert.NotEqual(t, firstID, secondID, "same label creates two separate agents")
	body := wu3Error(t, systools.NewAgentGetToolsTool(d).Execute(context.Background(), map[string]any{"name": label}), "AMBIGUOUS")
	assert.Contains(t, body["message"], label)
}

func TestWU3UUIDCreationDoesNotRenameBuiltinIdentities(t *testing.T) {
	d, cfg := wu3Deps(t)
	coreagent.SeedConfig(cfg)
	fixed := map[string]string{"omnipus": "Omnipus", "mia": "Mia", "jim": "Jim"}
	for id, name := range fixed {
		found := false
		for _, ac := range cfg.Agents.List {
			if ac.ID == id {
				found = true
				assert.Equal(t, name, ac.Name)
			}
		}
		assert.True(t, found, "fixed built-in %s must remain present", id)
	}
	wu3Success(t, systools.NewAgentCreateTool(d).Execute(context.Background(), wu3CreateArgs("User label")))
	for id := range fixed {
		found := false
		for _, ac := range cfg.Agents.List {
			if ac.ID == id {
				found = true
			}
		}
		assert.True(t, found, "creating custom UUID must not replace built-in identity %s", id)
	}
}
