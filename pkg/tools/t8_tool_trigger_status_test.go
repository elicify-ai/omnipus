// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Round 7, CK-4. Tool-path twin of pkg/gateway/t8_rest_trigger_status_test.go
// (chief ruling on r17 TK-30 (3)): a changed FUTURE trigger on a task that
// would end up `next` is stored `inbox` whatever the request's status says;
// explicit `inbox` stays; explicit `blocked` is engine-only (TK-14) and is
// refused with nothing stored; an unchanged-trigger re-save keeps `next`.

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func t8ToolOnce(at int64) map[string]any {
	return triggerArg("once", map[string]any{"at_ms": at})
}

func TestT8ToolTriggerStatus_ExplicitNextWithFutureTrigger_StoredInbox_TK30_3(t *testing.T) {
	store, _ := parityStorePair(t)
	tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusNext, false)
	at := time.Now().Add(time.Hour).UnixMilli()

	res := NewTaskUpdateTool(store).Execute(parityCtx("agent-a"), map[string]any{
		"task_id": tk.ID, "status": "next", "trigger": t8ToolOnce(at)})
	require.False(t, res.IsError, "update_task status next + future trigger: %s", res.ForLLM)
	got, err := store.Get(tk.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Trigger)
	require.NotNil(t, got.Trigger.Config.AtMs)
	require.Equal(t, at, *got.Trigger.Config.AtMs, "the exact trigger is stored")
	assert.Equal(t, task.StatusInbox, got.Status, "CK-4: a changed future trigger stores inbox whatever the status says")
}

func TestT8ToolTriggerStatus_ExplicitInbox_StaysInbox_TK30_3(t *testing.T) {
	store, _ := parityStorePair(t)
	tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusNext, false)
	at := time.Now().Add(time.Hour).UnixMilli()

	res := NewTaskUpdateTool(store).Execute(parityCtx("agent-a"), map[string]any{
		"task_id": tk.ID, "status": "inbox", "trigger": t8ToolOnce(at)})
	require.False(t, res.IsError, "%s", res.ForLLM)
	got, err := store.Get(tk.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Trigger)
	require.Equal(t, at, *got.Trigger.Config.AtMs)
	assert.Equal(t, task.StatusInbox, got.Status, "explicit inbox stays as asked")
}

func TestT8ToolTriggerStatus_ExplicitBlocked_RefusedNothingStored_TK14(t *testing.T) {
	store, _ := parityStorePair(t)
	tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusNext, false)
	at := time.Now().Add(time.Hour).UnixMilli()

	res := NewTaskUpdateTool(store).Execute(parityCtx("agent-a"), map[string]any{
		"task_id": tk.ID, "status": "blocked", "trigger": t8ToolOnce(at)})
	t.Logf("update_task blocked result error=%v: %s", res.IsError, res.ForLLM)
	require.True(t, res.IsError, "blocked is engine-only (TK-14): the call is refused")
	got, err := store.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusNext, got.Status, "a refused call changes no status")
	assert.Nil(t, got.Trigger, "a refused call stores no trigger")
}

func TestT8ToolTriggerStatus_UnchangedTriggerResave_KeepsNext_TK30_3(t *testing.T) {
	store, _ := parityStorePair(t)
	tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusNext, false)
	at := time.Now().Add(time.Hour).UnixMilli()

	trig := &task.Trigger{Type: task.TriggerOnce}
	trig.Config.AtMs = &at
	next := task.StatusNext
	_, err := store.Update(tk.ID, task.Patch{Trigger: &trig, Status: &next})
	require.NoError(t, err, "instrument: seed a trigger on a next task (explicit status skips the auto move)")
	seeded, err := store.Get(tk.ID)
	require.NoError(t, err)
	require.Equal(t, task.StatusNext, seeded.Status, "instrument: seeded next with a trigger")

	res := NewTaskUpdateTool(store).Execute(parityCtx("agent-a"), map[string]any{
		"task_id": tk.ID, "status": "next", "trigger": t8ToolOnce(at)})
	require.False(t, res.IsError, "%s", res.ForLLM)
	got, err := store.Get(tk.ID)
	require.NoError(t, err)
	require.Equal(t, at, *got.Trigger.Config.AtMs)
	assert.Equal(t, task.StatusNext, got.Status, "an unchanged trigger re-save keeps next")
}
