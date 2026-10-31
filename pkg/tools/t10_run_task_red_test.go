package tools

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Traces to: schedules-t10-spec-r6.md lines 875–899, 943–948; TDD 21, D-6.1–3.
// Real tool boundary with a refusing executor seam: it never pretends to launch
// a run. The callback observes ordering and returns a distinct diagnostic so a
// noop/done-gate shortcut cannot pass. Full fire/marker behavior is tested in
// the gateway/agent lanes, not simulated by this refusal.
func TestT10_RunTaskRoutesScheduledBeforeStatusAndDoneGate(t *testing.T) {
	for _, tc := range []struct {
		name       string
		typ        task.TriggerType
		status     task.Status
		wantRoute  bool
		wantStatus task.Status
	}{
		{"manual-inbox-control", task.TriggerManual, task.StatusInbox, true, task.StatusInProgress},
		{"once-inbox", task.TriggerOnce, task.StatusInbox, true, task.StatusInbox},
		{"recurring-inbox", task.TriggerRecurring, task.StatusInbox, true, task.StatusInbox},
		{"recurring-done-advances-series", task.TriggerRecurring, task.StatusDone, true, task.StatusDone},
		{"ordinary-done-still-refused", task.TriggerManual, task.StatusDone, false, task.StatusDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := task.New(filepath.Join(t.TempDir(), "tasks"))
			at := time.Now().Add(48 * time.Hour).UnixMilli()
			rule, tz := "FREQ=DAILY;COUNT=3", "UTC"
			trigger := &task.Trigger{Type: tc.typ, Config: task.TriggerConfig{}}
			if tc.typ == task.TriggerOnce {
				trigger.Config.AtMs = &at
			}
			if tc.typ == task.TriggerRecurring {
				trigger.Config.Rrule = &rule
				trigger.Config.DtstartMs = &at
				trigger.Config.Tz = &tz
			}
			tk := &task.Task{ID: "route-order", Title: "route order", AgentID: "mia", WorkspaceID: "default", Status: tc.status, Trigger: trigger}
			require.NoError(t, store.Create(tk))
			tool := NewTaskRunTool(store)
			tool.SetDelegationDenyChecker(func(context.Context, string, string) *DelegationDenial { return nil })
			calls := 0
			tool.SetStartTaskNow(func(_ context.Context, id string) (string, error) {
				calls++
				fresh, err := store.Get(id)
				require.NoError(t, err)
				assert.Equal(t, tc.wantStatus, fresh.Status, "trigger route must happen BEFORE any scheduled-task status mutation; manual control keeps its existing pre-write")
				return "", errors.New("T10 observable executor refusal " + tc.name)
			})
			result := tool.Execute(context.Background(), map[string]any{"task_id": tk.ID})
			require.NotNil(t, result)
			require.True(t, result.IsError)
			if tc.wantRoute {
				assert.Equal(t, 1, calls)
				assert.Contains(t, result.ForLLM, "T10 observable executor refusal "+tc.name)
			} else {
				assert.Zero(t, calls)
				assert.Contains(t, result.ForLLM, "already done")
			}
		})
	}
}
