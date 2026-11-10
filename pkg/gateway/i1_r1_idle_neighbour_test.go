package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MUST 3 / healthy control: an idle chat at the same real gateway boot as an
// actually killed first-human execution has no run to interrupt. Its lifecycle
// and every persisted JSON metadata group must remain byte-identical.
func TestI1R1IdleNeighbourBoot(t *testing.T) {
	m := i1KillAdmittedRoot(t)
	t.Setenv("OMNIPUS_HOME", m.Config.Agents.Defaults.Home)
	us, err := session.NewUnifiedStore(m.SessionsDir)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, us.Close()) })
	idle, err := us.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	ls := session.NewLifecycleStore(m.LifecycleDir)
	require.NoError(t, ls.Persist(&session.LifecycleRecord{SessionID: idle.ID, Generation: 1, State: session.LifecycleRunning, Origin: &session.Origin{Kind: session.OriginKindChat}, OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: "ws", AgentID: "mia"}))
	journalPath := filepath.Join(m.LifecycleDir, idle.ID+".jsonl")
	before, err := os.ReadFile(journalPath)
	require.NoError(t, err)
	metadataBefore := i1R1MetadataJSON(t, filepath.Join(m.SessionsDir, idle.ID))
	_, stg, p, _ := i1ReopenAndBoot(t, m)
	crashed, err := stg.lifecycleStore.Load(m.Root)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleStopped, crashed.State, "positive neighbour: actual killed execution must be recovered")
	idleAfter, err := stg.lifecycleStore.Load(idle.ID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleRunning, idleAfter.State, "idle/no-execution is not a crashed run")
	assert.Nil(t, idleAfter.ExecutionID)
	after, err := os.ReadFile(journalPath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "idle lifecycle history must be byte-identical through real BootHook")
	assert.Equal(t, metadataBefore, i1R1MetadataJSON(t, filepath.Join(m.SessionsDir, idle.ID)), "idle transcript metadata must be byte-identical")
	assert.Zero(t, p.calls.Load(), "boot may not dispatch either conversation")
}

func i1R1MetadataJSON(t *testing.T, dir string) map[string]string {
	t.Helper()
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	out := map[string]string{}
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, file.Name()))
		require.NoError(t, err)
		out[file.Name()] = string(data)
	}
	require.NotEmpty(t, out, "instrument: real metadata JSON groups must exist")
	return out
}
