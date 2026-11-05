package agent

// r17 18.7–18.8: real loop policy handling and the shared writer's real audit
// sink. Provider replies, clock and person's decision are process-edge fixtures.
import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT8Trigger_CreateAllowHasNoAdditionalApproval(t *testing.T) {
	// A chat-capable custom agent, not the native worker-only fixture: worker
	// refusal at the routing boundary is not the scheduling policy under test.
	f := t8ReadLoop(t)
	al := f.al
	clock := newTriggerFakeClock()
	sched := NewTaskTriggerScheduler(filepath.Join(f.home, "tasks_triggers", "jobs.json"), al.taskStore, al.taskExecutor)
	sched.SetClock(clock)
	require.NoError(t, sched.Start())
	al.SetTaskTriggerScheduler(sched)
	t.Cleanup(sched.Stop)
	inst, ok := al.GetRegistry().GetAgent("reader-a")
	require.True(t, ok)
	args := t8CreateSchedulingArgs("allow policy scheduled create")
	args["agent_id"] = "reader-a"
	at := clock.Now().Add(24 * time.Hour).UnixMilli()
	args["trigger"] = map[string]any{"type": "once", "config": map[string]any{"at_ms": at}}
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	provider := testutil.NewScenario().WithToolCall("ToolSearch", `{"names":["create_task"]}`).WithToolCall("create_task", string(raw)).WithText("scheduled create checked")
	inst.Provider = provider
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{GlobalPolicies: map[string]config.ToolPolicy{"ToolSearch": "allow", "create_task": "allow", "goal_claim": "allow"}, Policies: map[string]config.ToolPolicy{"create_task": "allow", "goal_claim": "allow"}})
	decider := &t8DecisionRecorder{allow: true}
	al.SetToolApprover(decider)
	_, _, err = al.processMessage(context.Background(), bus.InboundMessage{Channel: "cli", ChatID: "t8-allow-trigger", Sender: bus.SenderInfo{CanonicalID: "person"}, Content: "create this scheduled task", Metadata: map[string]string{"agent_id": "reader-a", "workspace_id": testHarnessWorkspaceMembershipID}})
	require.NoError(t, err)
	assert.Empty(t, decider.snapshot(), "18.7 allow has no ordinary or scheduling-only approval")
	tk := t8CreatedTask(t, al, "allow policy scheduled create")
	assert.Equal(t, task.StatusInbox, tk.Status)
	require.NotNil(t, tk.Trigger)
	assert.Equal(t, &at, tk.Trigger.Config.AtMs)
	jobs := jobsForTask(sched, tk.ID)
	require.Len(t, jobs, 1, "instrument: real writer actually armed the scheduled task")
	runs, err := al.taskStore.ListRuns(tk.ID)
	require.NoError(t, err)
	assert.Empty(t, runs)
}

func TestT8Trigger_UpdateAuditContainsActorTriggersAndStateMove(t *testing.T) {
	al, sched, clock := t8NativeSchedulingFixture(t)
	dir := t.TempDir()
	lg, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, MaxSizeBytes: 1024 * 1024, RetentionDays: 1})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lg.Close()) })
	inst, ok := al.GetRegistry().GetAgent("native-agent")
	require.True(t, ok)
	inst.Tools.SetAuditLogger(lg)
	require.NoError(t, lg.Log(&audit.Entry{Event: "t8.audit.instrument", Decision: audit.DecisionAllow}))
	require.NotEmpty(t, readAuditEvents(t, filepath.Join(dir, "audit.jsonl")), "instrument: actual audit logger writes and reader sees its entry")
	manual := &task.Trigger{Type: task.TriggerManual}
	tk := &task.Task{Title: "audit state move", Prompt: "do and verify the work", Action: task.ActionLLM, AgentID: "native-agent", CreatedByAgentID: "native-agent", CreatedBy: "native-agent", WorkspaceID: "default", Status: task.StatusNext, Trigger: manual}
	require.NoError(t, al.taskStore.Create(tk))
	at := clock.Now().Add(time.Hour).UnixMilli()
	newTrigger := map[string]any{"type": "recurring", "config": map[string]any{"rrule": "FREQ=WEEKLY;BYDAY=TU", "dtstart_ms": at, "tz": "UTC"}}
	r := t8RegisteredCall(t, inst, t8NativeTaskCallContext(), "update_task", map[string]any{"task_id": tk.ID, "trigger": newTrigger})
	require.False(t, r.IsError, "18.8 valid real trigger update: %s", r.ForLLM)
	after, err := al.taskStore.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusInbox, after.Status)
	require.Len(t, jobsForTask(sched, tk.ID), 1)
	var changed []map[string]any
	for _, entry := range readAuditEvents(t, filepath.Join(dir, "audit.jsonl")) {
		if entry["event"] == "task.trigger.recurrence_changed" {
			changed = append(changed, entry)
		}
	}
	require.Len(t, changed, 1, "18.8 exactly one existing trigger audit event through the shared tool writer")
	assert.Equal(t, "native-agent", changed[0]["agent_id"])
	details, ok := changed[0]["details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"type": "manual", "config": map[string]any{}}, details["prior_trigger"])
	assert.Equal(t, map[string]any{"type": "recurring", "config": map[string]any{"rrule": "FREQ=WEEKLY;BYDAY=TU", "dtstart_ms": float64(at), "tz": "UTC"}}, details["new_trigger"])
	encoded, err := json.Marshal(details)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"next"`, "18.8 semantic audit includes the prior state")
	assert.Contains(t, string(encoded), `"inbox"`, "18.8 semantic audit includes the resulting state")
}
