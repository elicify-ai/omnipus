package agentstore

// Oracle: tool-parity r17 §7.5 AG-05/AG-14, §7.10, scenarios
// 7.3/7.4 and 21.11. Do not derive the revision algorithm in the test.
import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentWrite_AgentMdChangesRevision(t *testing.T) {
	home := t.TempDir()
	s := New(home)
	_, err := s.CreateState("r1", &config.AgentConfig{ID: "r1", Name: "Original", Type: config.AgentTypeCustom}, "Soul")
	require.NoError(t, err)
	dir := filepath.Join(home, "agents", "r1")
	require.NoError(t, os.MkdirAll(dir, 0700))
	md := filepath.Join(dir, "AGENT.md")
	require.NoError(t, os.WriteFile(md, []byte("Alpha"), 0600))
	before, err := s.ReadState("r1")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(md, []byte("Beta"), 0600))
	after, err := s.ReadState("r1")
	require.NoError(t, err)
	assert.NotEqual(t, before.Revision, after.Revision, "7.3 AGENT.md alone is part of revisionFor")
	result, err := s.MutateState("r1", before.Revision, func(a *config.AgentConfig) error { a.Name = "Stale edit"; return nil }, nil)
	require.ErrorIs(t, err, ErrRevisionConflict, "7.4 stale AGENT.md revision refuses the mutation")
	assert.Equal(t, PersistenceNone, result.PersistenceStatus)
	fresh, err := s.ReadState("r1")
	require.NoError(t, err)
	assert.Equal(t, "Original", fresh.Agent.Name, "CONFLICT changes no entity")
}

func TestAgentStore_StrayRateLimitsIgnoredThenDropped(t *testing.T) {
	home := t.TempDir()
	s := New(home)
	_, err := s.CreateState("w", &config.AgentConfig{ID: "w", Name: "Worker", Type: config.AgentTypeWorker}, "")
	require.NoError(t, err)
	path := filepath.Join(home, "entities", "agents", "w.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var object map[string]any
	require.NoError(t, json.Unmarshal(raw, &object))
	object["rate_limits"] = map[string]any{"requests_per_minute": 17}
	raw, err = json.Marshal(object)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0600))
	state, err := s.ReadState("w")
	require.NoError(t, err, "21.11 plain load ignores the stray key")
	_, err = s.MutateState("w", state.Revision, func(a *config.AgentConfig) error { a.Name = "Changed"; return nil }, nil)
	require.NoError(t, err)
	raw, err = os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &object))
	var after map[string]any
	require.NoError(t, json.Unmarshal(raw, &after))
	_, exists := after["rate_limits"]
	assert.False(t, exists, "21.11 next write drops raw rate_limits")
	assert.Equal(t, "Changed", after["name"])
}
