package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// architect F04 (third row): when the store publishes the entity but cannot
// publish SOUL.md, create_agent is "partly saved" and its changed_fields carry
// the store's own durable parts instead of dropping them.
func TestWU3CreateAgentPartialStoreWriteNamesDurableParts(t *testing.T) {
	systools.UseNameKeyedAgentIDsForTest(t)
	d, _ := wu3Deps(t)
	// SOUL.md is a non-empty directory, so publishing the soul file fails
	// after the entity record is already published.
	blocker := filepath.Join(d.Home, "agents", "soul-fault", "SOUL.md")
	require.NoError(t, os.MkdirAll(blocker, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(blocker, "keep"), []byte("x"), 0o600))

	res := systools.NewAgentCreateTool(d).Execute(context.Background(), wu3CreateArgs("Soul Fault"))
	require.True(t, res.IsError, res.ForLLM)
	body := wu3Outcome(t, res, "partly saved")
	assert.Equal(t, "partial", body["persistence_status"])
	assert.Equal(t, []any{"entity"}, body["changed_fields"], "the published entity is named as a durable part")
	_, err := os.Stat(filepath.Join(d.Home, "entities", "agents", "soul-fault.json"))
	require.NoError(t, err, "the entity really was published")
}
