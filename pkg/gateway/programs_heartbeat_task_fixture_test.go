package gateway

// S124's producer is the real heartbeat reconciler/cron/runner, not a constructed
// independent inbound message. No helper below writes task/turn provenance.
import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func programsHeartbeatCreateTask(t *testing.T, x *programsTaskFixture, prompt string) string {
	t.Helper()
	ws, err := readWorkspaceFile(x.home, "sales")
	require.NoError(t, err)
	ws.MemberConfigs = map[string]workspace.MemberConfig{"dev": {Heartbeat: &workspace.MemberHeartbeat{Enabled: true, IntervalMinutes: 15, Body: "Create the requested Ops task."}}}
	require.NoError(t, workspace.SaveRecord(x.home, ws))
	persisted, err := listWorkspaceFiles(x.home)
	require.NoError(t, err)
	cs := cron.NewCronService(filepath.Join(x.home, "cron", "s124-heartbeat.json"))
	t.Cleanup(cs.Stop)
	runner := newScheduledRunner(x.al, agentCheckerFunc(func(id string) bool {
		inst, ok := x.al.GetRegistry().GetAgent(id)
		return ok && inst != nil
	}), x.bus, nil, x.al.GetConfig)
	runner.home = x.home
	cs.SetRunner(runner)
	require.NoError(t, ReconcileHeartbeatSchedules(cs, persisted, configOnlyIsWorker(x.al.GetConfig())))
	jobs := cs.ListJobs(true)
	require.Len(t, jobs, 1, "instrument: persisted workspace heartbeat created exactly one real cron job")
	require.Equal(t, "heartbeat:sales:dev", jobs[0].Name)
	require.Equal(t, "heartbeat", jobs[0].Payload.Kind)
	require.Equal(t, "dev", jobs[0].AgentID)
	require.Equal(t, cron.SessionModeMain, jobs[0].SessionMode)
	const call = "s124-heartbeat-create"
	args := map[string]any{"title": "S124 heartbeat-created task", "prompt": prompt, "agent_id": "ops", "workspace_id": "sales", "criteria": []any{map[string]any{"text": "the requested command has actually executed"}}, "dod": []any{map[string]any{"text": "no secret value appears in the output"}}}
	x.provider.setScript(x.dev, []providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: call, Type: "function", Name: "create_task", Arguments: args}}},
		wu5StopGW("heartbeat task created"),
	})
	// RunNow synchronously uses the actual bounded cron lane. No timed loop,
	// fake executor/checker, direct turn call or test-stamped origin is involved.
	status, sid, err := cs.RunNow(jobs[0].ID)
	require.NoError(t, err, "instrument: real heartbeat completed, not a setup/admission failure")
	require.Equal(t, "ok", status)
	require.Equal(t, x.dev, sid, "instrument: the real heartbeat selected Sales/Dev's computed main")
	x.waitEnds(t, sid) // Consume THIS turn end before a later Telegram turn can wait.
	var receipt map[string]json.RawMessage
	raw := x.provider.result(sid, call)
	require.NoError(t, json.Unmarshal([]byte(raw), &receipt), "actual heartbeat create_task: %s", raw)
	var id string
	require.NoError(t, json.Unmarshal(receipt["task_id"], &id), "actual registered create_task returned its real identity: %s", raw)
	require.NotEmpty(t, id)
	stored, err := x.api.taskStore.Get(id)
	require.NoError(t, err)
	require.True(t, stored.CreatedByAgent("dev"), "S124 Given: Dev has actual agent-creator authority")
	require.Equal(t, "ops", stored.AgentID)
	require.Equal(t, prompt, stored.Prompt)
	origins := x.provider.observedOrigins(sid)
	require.GreaterOrEqual(t, len(origins), 2, "instrument: real heartbeat create and result-consumption model requests")
	for _, actual := range origins[:2] {
		assert.Equal(t, bus.NewProvenance(bus.OriginIndependent), actual, "S124: the actual heartbeat producer must establish independent")
	}
	programsAssertTaskDocument(t, programsReadTaskDocument(t, x, id), prompt, []string{"independent"}, "S124 actual heartbeat-created task")
	return id
}

// Switch only the actual conversation/provider address for subsequent Telegram
// turns. Main default-send reminder work must not consume another test script.
func programsTaskUseTelegramConversation(t *testing.T, x *programsTaskFixture, key string) {
	t.Helper()
	conversation, err := x.al.GetSessionStore().NewChannelSession("telegram", "telegram", key, "dev", "S124 task instruction edit")
	require.NoError(t, err)
	workspaceID := "sales"
	require.NoError(t, x.al.GetSessionStore().SetMeta(conversation.ID, session.MetaPatch{WorkspaceID: &workspaceID}))
	x.dev = conversation.ID
	provider := &programsTaskProvider{programsPeerProvider: x.provider, al: x.al, dev: x.dev, ops: x.ops, work: x.work}
	for _, actor := range []string{"dev", "ops"} {
		inst, ok := x.al.GetRegistry().GetAgent(actor)
		require.True(t, ok)
		inst.Provider = provider
	}
}
