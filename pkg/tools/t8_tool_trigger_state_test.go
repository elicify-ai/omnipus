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
			// CK-9: the exact requested trigger is stored, every supplied
			// configuration field and nothing else.
			cfg, _ := tc.trig["config"].(map[string]any)
			wantType, typeOK := tc.trig["type"].(string)
			require.True(t, typeOK, "instrument: the case names its trigger type")
			require.Equal(t, task.TriggerType(wantType), got.Trigger.Type, "the exact trigger type is stored")
			t8RequireStoredField(t, cfg, "at_ms", got.Trigger.Config.AtMs)
			t8RequireStoredField(t, cfg, "rrule", got.Trigger.Config.Rrule)
			t8RequireStoredField(t, cfg, "dtstart_ms", got.Trigger.Config.DtstartMs)
			t8RequireStoredField(t, cfg, "tz", got.Trigger.Config.Tz)
			assert.Equal(t, tc.want, got.Status)
		})
	}
}

// t8RequireStoredField asserts one stored trigger configuration field equals
// the value the request supplied, and is unset when the request omitted it.
func t8RequireStoredField[T comparable](t *testing.T, cfg map[string]any, key string, stored *T) {
	t.Helper()
	supplied, ok := cfg[key]
	if !ok {
		require.Nil(t, stored, "no %s invented", key)
		return
	}
	want, typeOK := supplied.(T)
	require.True(t, typeOK, "instrument: %s has the expected Go type", key)
	require.NotNil(t, stored, "%s is stored", key)
	require.Equal(t, want, *stored, "the exact %s is stored", key)
}
