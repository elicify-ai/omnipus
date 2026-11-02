// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Round 6, CK-2. The tool-path twin of the REST matrix in
// pkg/gateway/t8_rest_trigger_state_test.go (r17 TK-30 scheduling states
// (3), (4), (5)): REST and update_task must agree on the persisted state.

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT8ToolTriggerState_Matrix_TK30(t *testing.T) {
	future := time.Now().Add(time.Hour).UnixMilli()
	past := time.Now().Add(-time.Hour).UnixMilli()
	cases := []struct {
		name   string
		status task.Status
		trig   map[string]any
		want   task.Status
	}{
		{"future once on next moves to inbox (3)", task.StatusNext, triggerArg("once", map[string]any{"at_ms": future}), task.StatusInbox},
		{"future recurring on next moves to inbox (3)", task.StatusNext, triggerArg("recurring", map[string]any{
			"rrule": "FREQ=DAILY", "dtstart_ms": future, "tz": "UTC"}), task.StatusInbox},
		{"manual keeps next (4)", task.StatusNext, triggerArg("manual", map[string]any{}), task.StatusNext},
		{"already due keeps next (5)", task.StatusNext, triggerArg("once", map[string]any{"at_ms": past}), task.StatusNext},
		{"future once on done stays done (3)", task.StatusDone, triggerArg("once", map[string]any{"at_ms": future}), task.StatusDone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := parityStorePair(t)
			tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", tc.status, false)
			res := NewTaskUpdateTool(store).Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, "trigger": tc.trig})
			t.Logf("update_task result error=%v: %s", res.IsError, res.ForLLM)
			require.False(t, res.IsError, "update_task must succeed (CK-5): %s", res.ForLLM)
			got, err := store.Get(tk.ID)
			require.NoError(t, err)
			require.NotNil(t, got.Trigger, "instrument: the trigger persisted")
			assert.Equal(t, tc.want, got.Status)
		})
	}
}
