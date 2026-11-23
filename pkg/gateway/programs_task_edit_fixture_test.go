package gateway

// Actual task write observation and gateway-domain runtime reconstruction.
// These helpers never write task provenance, join a carrier or replace dispatch.
import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func programsReadTaskDocument(t *testing.T, x *programsTaskFixture, id string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(x.api.taskStore.Dir(), id+".json"))
	require.NoError(t, err)
	return raw
}

func programsAssertTaskDocument(t *testing.T, raw []byte, prompt string, origins []string, boundary string) {
	t.Helper()
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &document), "instrument: real persisted task JSON")
	var actualPrompt string
	require.NoError(t, json.Unmarshal(document["prompt"], &actualPrompt))
	assert.Equal(t, prompt, actualPrompt, "%s: real instruction write landed", boundary)
	var actualOrigins []string
	if provenance, present := document["provenance"]; present {
		assert.NoError(t, json.Unmarshal(provenance, &actualOrigins), "%s: persisted provenance must be an origin set", boundary)
	}
	assert.ElementsMatch(t, origins, actualOrigins, "%s: I-2 exact saved origin set, not missing/unresolved, overwritten or a later write", boundary)
}

// Read the actual persisted bytes synchronously while the writer still holds its
// coordination lock, before goal/scheduler reconciliation. No substitute store.
func programsCaptureTaskWrite(t *testing.T, x *programsTaskFixture, id string, write func()) []byte {
	t.Helper()
	var mu sync.Mutex
	var snapshots [][]byte
	var readErrors []error
	restore := tools.SetAfterTaskPersistTestHook(func(taskID string) {
		if taskID != id {
			return
		}
		raw, err := os.ReadFile(filepath.Join(x.api.taskStore.Dir(), taskID+".json"))
		mu.Lock()
		defer mu.Unlock()
		snapshots = append(snapshots, raw)
		readErrors = append(readErrors, err)
	})
	defer restore()
	write()
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, snapshots, 1, "instrument: exactly one actual reconciled instruction write observed at its persist boundary")
	require.NoError(t, readErrors[0])
	return append([]byte(nil), snapshots[0]...)
}

func programsAgentEditTask(t *testing.T, x *programsTaskFixture, id, call, prompt string) {
	t.Helper()
	x.provider.setScript(x.dev, []providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: call, Type: "function", Name: "update_task", Arguments: map[string]any{"task_id": id, "prompt": prompt}}}},
		wu5StopGW("task prompt edited"),
	})
	input := bus.InboundMessage{Channel: "telegram", InstanceID: "telegram", SessionID: x.dev, ChatID: x.dev, Content: "edit the task prompt for Ops", Sender: bus.SenderInfo{CanonicalID: "telegram:s1", DisplayName: "S1 person"}, Metadata: map[string]string{"workspace_id": "sales", "agent_id": "dev"}, Provenance: bus.NewProvenance(bus.ConnectorOrigin("telegram"))}
	require.NoError(t, x.bus.PublishInbound(context.Background(), input))
	x.waitEnds(t, x.dev)
	var result map[string]json.RawMessage
	raw := x.provider.result(x.dev, call)
	require.NoError(t, json.Unmarshal([]byte(raw), &result), "actual update_task must accept Dev as this task's creator: %s", raw)
	var updatedID string
	require.NoError(t, json.Unmarshal(result["task_id"], &updatedID), "actual update_task must return its task identity: %s", raw)
	require.Equal(t, id, updatedID)
}

// A full teardown/reconstruction of the in-process gateway agent runtime, not
// just a new task.Store. OS re-exec, HTTP startup and UAT are separate boundaries.
func programsRestartTaskRuntime(t *testing.T, x *programsTaskFixture) {
	t.Helper()
	old := x.programsPeerFixture
	configPath := filepath.Join(old.home, "config.json")
	// The ordinary constructor fixture's roster is in memory. Persist those
	// configured actors through the real entity store before the restart;
	// config.json intentionally cannot carry agents.list (ADR-054).
	cfgBefore := old.al.GetConfig()
	for i := range cfgBefore.Agents.List {
		actor := cfgBefore.Agents.List[i]
		require.NoError(t, agentstore.New(old.home).Create(actor.ID, &actor))
	}
	require.NoError(t, config.SaveConfig(configPath, cfgBefore))
	old.shutdown()
	store := credentials.NewStore(filepath.Join(old.home, "credentials.json"))
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x39}, 32)))
	t.Cleanup(store.Close)
	cfg, err := config.LoadConfigWithStore(configPath, store)
	require.NoError(t, err)
	require.NoError(t, populateAgentsListFromEntityStoreStrict(cfg, old.home))
	provider := &programsPeerProvider{wu5GatewayProvider: newWU5GatewayProvider(), origins: map[string][]bus.Provenance{}}
	b := bus.NewMessageBus()
	t.Cleanup(b.Close)
	al, err := agent.NewAgentLoop(cfg, b, provider)
	require.NoError(t, err)
	t.Cleanup(al.Close)
	installTestSessionLauncher(t, al)
	al.WireSysagentDeps(&systools.Deps{Home: old.home, GetCfg: al.GetConfig, CredStore: store})
	h := newWSHandler(b, al, "")
	h.home = old.home
	al.SetAddressDeps(gatewayAddressDeps{h: h})
	taskProvider := &programsTaskProvider{programsPeerProvider: provider, al: al, dev: old.dev, ops: old.ops, work: old.work}
	for _, id := range []string{"dev", "ops"} {
		inst, ok := al.GetRegistry().GetAgent(id)
		require.True(t, ok)
		inst.Provider = taskProvider
	}
	reopened := &programsPeerFixture{al: al, bus: b, provider: provider, home: old.home, work: old.work, dev: old.dev, ops: old.ops, events: al.SubscribeEvents(256)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- al.Run(ctx) }()
	var shutdown sync.Once
	reopened.shutdown = func() {
		shutdown.Do(func() {
			cancel()
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(10 * time.Second):
				t.Fatal("reconstructed gateway runtime did not stop")
			}
			al.UnsubscribeEvents(reopened.events.ID)
			al.Close()
			store.Close()
			b.Close()
		})
	}
	t.Cleanup(reopened.shutdown)
	te := agent.GetTaskExecutor(al)
	require.NotNil(t, te)
	x.programsPeerFixture = reopened
	x.api = &restAPI{agentLoop: al, homePath: old.home, taskStore: agent.GetTaskStore(al), taskLock: task.TaskFileLock, taskExecutor: te}
	require.NotSame(t, old.al, al, "instrument: restart constructed a fresh real AgentLoop")
	require.NotSame(t, old.provider, provider, "instrument: no in-memory provider/session observations survive")
	t.Log("actual gateway-domain runtime stopped/joined/closed; fresh loop, bus, stores, credentials, boot epoch and task launcher reconstructed from the same home")
}
