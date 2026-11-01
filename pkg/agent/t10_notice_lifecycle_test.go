//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Real scheduled lifecycle plus the real terminal CAS/deliverer. This does not
// call completeTaskWithResult with nil to simulate an actual scheduled run.
func t10CreatorScheduledLifecycle(t *testing.T, runNow bool) {
	f := t10T8(t)
	tk := f.create(t, "once")
	at := *tk.Trigger.Config.AtMs
	creator := "parent-agent"
	registerAgent(t, f.al, filepath.Dir(f.al.taskStore.Dir()), creator, &mockProvider{})
	provisionTestWorkspace(t, tk.WorkspaceID, "native-agent", creator)
	main := map[string]string{}
	resolver := &u6FakeMainResolver{elig: map[[2]string]string{}}
	for _, id := range []string{"native-agent", creator} {
		sid, e := session.MainSessionID(tk.WorkspaceID, id)
		require.NoError(t, e)
		main[id] = sid
		resolver.elig[[2]string{tk.WorkspaceID, id}] = sid
		meta, e := f.al.GetSessionStore().GetOrCreateMainSession(tk.WorkspaceID, id)
		require.NoError(t, e)
		require.Equal(t, sid, meta.ID)
	}
	f.al.SetSteerAudienceDeps(NewSteerAudienceResolver(NewSteerRecordClassifier(f.al.GetSessionLifecycleStore(), f.al.GetSessionStore())), nil, NewSteerUpwardDeliverer())
	f.al.taskExecutor.SetMainSessionResolver(resolver)
	f.al.SetAddressDeps(&fakeAddressDeps{eligible: map[string]bool{tk.WorkspaceID + "/native-agent": true, tk.WorkspaceID + "/" + creator: true}})
	// Seed attribution on this fresh test record, never a product migration. It
	// is server-only metadata, not a new tool/API argument.
	path := filepath.Join(f.al.taskStore.Dir(), tk.ID+".json")
	data, e := os.ReadFile(path)
	require.NoError(t, e)
	var disk map[string]any
	require.NoError(t, json.Unmarshal(data, &disk))
	disk["created_by_agent_id"] = creator
	data, e = json.Marshal(disk)
	require.NoError(t, e)
	require.NoError(t, os.WriteFile(path, data, 0600))
	var inbox *session.MessageInboxStore
	// Read the same real inbox installed by the native task fixture.
	inbox = f.al.GetMessageInboxStore()
	require.NotNil(t, inbox)
	notices := func(id string) []string {
		t.Helper()
		// The real idle-main wake may ACK before this assertion runs. Entries
		// retains all durable messages: ACK is a delivery cursor, not deletion.
		// Counting the raw stream catches duplicates even after acknowledgement.
		rows, err := inbox.Entries(main[id])
		require.NoError(t, err)
		out := []string{}
		for _, row := range rows {
			if row.Kind != session.InboxEntryMessage {
				continue
			}
			require.NotNil(t, row.Message)
			m := row.Message
			kind, err := m.Discriminator()
			require.NoError(t, err)
			// As* only unmarshals; it does not check the discriminant. A real
			// progress frame must not be mistaken for an empty terminal notice.
			switch kind {
			case "handback":
				hb, err := m.AsSessionMessageHandback()
				require.NoError(t, err)
				out = append(out, hb.ResultSoFar)
			case "error":
				fail, err := m.AsSessionMessageError()
				require.NoError(t, err)
				out = append(out, fail.Text)
			}
		}
		return out
	}
	if !runNow {
		blocker := newCompletionContractTask(t, f.al, "native-agent", "notice dependency")
		deps := []string{blocker.ID}
		next := task.StatusNext
		_, e = f.al.taskStore.Update(tk.ID, task.Patch{Status: &next, BlockedBy: &deps})
		require.NoError(t, e)
		f.fire(t, at)
		assert.Empty(t, notices(creator))
		assert.Empty(t, notices("native-agent"))
		rows, e := f.al.taskStore.ListRuns(tk.ID)
		require.NoError(t, e)
		assert.Empty(t, rows, "delivered-held is not a terminal skipped run")
		assert.Equal(t, float64(at), t10InternalMarker(t, f.al.taskStore, tk.ID))
		done := task.StatusDone
		_, e = f.al.taskStore.Update(blocker.ID, task.Patch{Status: &done})
		require.NoError(t, e)
		_, e = f.al.taskStore.AdvanceBlockedDependents(blocker.ID)
		require.NoError(t, e)
		f.al.taskExecutor.CheckQueuedTasks(context.Background())
	} else {
		inst, ok := f.al.registry.GetAgent("native-agent")
		require.True(t, ok)
		runTool, ok := inst.Tools.Get("run_task")
		require.True(t, ok)
		ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "native-agent"), tk.WorkspaceID)
		r := runTool.Execute(ctx, map[string]any{"task_id": tk.ID})
		require.False(t, r.IsError, r.ForLLM)
		fresh, e := f.al.taskStore.Get(tk.ID)
		require.NoError(t, e)
		raw, e := json.Marshal(fresh)
		require.NoError(t, e)
		var v map[string]any
		require.NoError(t, json.Unmarshal(raw, &v))
		assert.Equal(t, float64(at), v["consumed_occurrence_ms"])
	}
	f.terminal(t, tk.ID)
	assert.Eventually(t, func() bool { return len(notices("native-agent")) == 1 }, time.Second, time.Millisecond)
	assignee := notices("native-agent")
	rows, e := f.al.taskStore.ListRuns(tk.ID)
	require.NoError(t, e)
	require.Len(t, rows, 1)
	want := []string{fmt.Sprintf("Task run done: %q (run %s)\n\nT10 scheduled contract completed", tk.Title, rows[0].RunID)}
	assert.Equal(t, want, assignee, "assignee gets exactly one full durable notice, including after ACK")
	assert.Equal(t, want, notices(creator), "creator gets the same full notice, once, only after actual completion")
	assert.NotContains(t, rows[0].RecipientSessionIDs, main[creator], "creator must not become a third captured recipient")
	f.clk.Advance(time.UnixMilli(at).Sub(f.clk.Now()))
	f.s.RunDueJobs(f.clk.Now())
	f.s.WaitForLane()
	assert.Equal(t, assignee, notices(creator))
	rows, e = f.al.taskStore.ListRuns(tk.ID)
	require.NoError(t, e)
	assert.Len(t, rows, 1, "consumed/delivered occurrence never completes twice")
}
