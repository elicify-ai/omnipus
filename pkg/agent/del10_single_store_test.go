//go:build goolm && stdjson

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

func del10Cfg(t *testing.T) *config.Config {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	return &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096, MaxToolIterations: 10},
		List:     []config.AgentConfig{{ID: "mia", Home: home}, {ID: "ava", Home: home}},
	}}
}

// DEL-10 / A3: every agent, the loop and a reloaded registry hold the ONE shared
// store. Before, each registry build opened fresh per-agent stores over the same
// directories (two lock-shard sets, a leaked flusher per reload).
func TestSingleSessionStore_AgentsLoopAndReloadedRegistryShareOneStore(t *testing.T) {
	cfg := del10Cfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &simpleMockProvider{response: "ok"})
	shared := al.GetSessionStore()
	require.NotNil(t, shared)
	for _, id := range []string{"mia", "ava"} {
		inst, ok := al.GetRegistry().GetAgent(id)
		require.True(t, ok)
		assert.True(t, inst.Sessions == session.SessionStore(shared), "agent %s holds the shared store, not a store of its own", id)
	}
	reloaded := NewAgentRegistry(cfg, &simpleMockProvider{response: "ok"})
	for _, id := range []string{"mia", "ava"} {
		inst, ok := reloaded.GetAgent(id)
		require.True(t, ok)
		assert.True(t, inst.Sessions == session.SessionStore(shared), "a reloaded registry reuses the shared store (A3)")
	}
	// A discarded duplicate instance must not close the live store.
	dup, ok := reloaded.GetAgent("mia")
	require.True(t, ok)
	require.NoError(t, dup.Close())
	meta, err := shared.NewSession(session.SessionTypeChat, "test", "mia")
	require.NoError(t, err, "the shared store is still writable after a discarded instance was closed")
	require.NotEmpty(t, meta.ID)
}

// DEL-10 / A2: a task run's error note is written into the store that holds the
// run's session (the shared one), whichever agent the task is assigned to.
func TestEndTaskAssigneeCannotFinish_WritesIntoTheSessionsOwnStore(t *testing.T) {
	cfg := del10Cfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &simpleMockProvider{response: "ok"})
	shared := al.GetSessionStore()
	require.NotNil(t, shared)
	meta, err := shared.NewSession(session.SessionTypeTask, "system", "mia")
	require.NoError(t, err)

	store := task.New(filepath.Join(t.TempDir(), "tasks"))
	te := newTaskExecutor(al, store)
	tk := &task.Task{ID: "t-a2", AgentID: "mia", Title: "x"}
	te.endTaskAssigneeCannotFinish(tk, meta.ID, "cannot finish", nil)

	entries, err := shared.ReadTranscript(meta.ID)
	require.NoError(t, err)
	require.Len(t, entries, 1, "the error entry landed in the session's own store")
	assert.Equal(t, "cannot finish", entries[0].Content)
}
