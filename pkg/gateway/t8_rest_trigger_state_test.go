// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Round 6, CK-2. r17 Table T-TASK TK-30, scheduling states (3) and (4):
// adding or changing a FUTURE trigger on a task that is not running and not
// terminal moves it next -> inbox in the SAME write; a blocked task stays
// blocked, a terminal task stays terminal, a manual trigger changes no state,
// and a first occurrence already due follows the shared path (stays next).
// The REST PATCH here supplies only the trigger, never `status`.

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// t8NextAssignedTask returns a Next task assigned to the real member "mia".
func t8NextAssignedTask(t *testing.T, api *restAPI, wsID string) string {
	t.Helper()
	created := createTaskWithStatusViaAPI(t, api, "t8 trigger state", wsID, "next")
	w := patchTask(t, api, created.Id, `{"agent_id":"mia"}`)
	require.Equal(t, http.StatusOK, w.Code, "instrument: assign a workspace member: %s", w.Body.String())
	got, err := api.taskStore.Get(created.Id)
	require.NoError(t, err)
	require.Equal(t, task.StatusNext, got.Status, "instrument: the task starts in Next")
	return created.Id
}

func t8TriggerStateAPI(t *testing.T) (*restAPI, string) {
	t.Helper()
	api := newTestRestAPIWithHomeAndAgent(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"mia"})
	return api, wsID
}

func t8OnceBody(at time.Time) string {
	return fmt.Sprintf(`{"trigger":{"type":"once","config":{"at_ms":%d}}}`, at.UnixMilli())
}

// TK-30 (3), CK-2: a future once trigger alone moves an assigned Next task to
// Inbox in the same write, and the trigger itself persists.
func TestT8RestTriggerState_FutureOnce_MovesNextToInbox_TK30_3(t *testing.T) {
	api, wsID := t8TriggerStateAPI(t)
	id := t8NextAssignedTask(t, api, wsID)
	at := time.Now().Add(time.Hour)

	w := patchTask(t, api, id, t8OnceBody(at))
	require.Equal(t, http.StatusOK, w.Code, "PATCH with only a future once trigger: %s", w.Body.String())
	got, err := api.taskStore.Get(id)
	require.NoError(t, err)
	require.NotNil(t, got.Trigger, "instrument: the trigger persisted")
	require.Equal(t, task.TriggerOnce, got.Trigger.Type)
	require.NotNil(t, got.Trigger.Config.AtMs)
	assert.Equal(t, at.UnixMilli(), *got.Trigger.Config.AtMs)
	assert.Equal(t, task.StatusInbox, got.Status, "TK-30 (3): the same write moves next to inbox")
}

// TK-30 (3), CK-2: a future recurring trigger behaves the same way.
func TestT8RestTriggerState_FutureRecurring_MovesNextToInbox_TK30_3(t *testing.T) {
	api, wsID := t8TriggerStateAPI(t)
	id := t8NextAssignedTask(t, api, wsID)
	dtstart := time.Now().Add(2 * time.Hour).UnixMilli()
	body := fmt.Sprintf(`{"trigger":{"type":"recurring","config":{"rrule":"FREQ=DAILY","dtstart_ms":%d,"tz":"UTC"}}}`, dtstart)

	w := patchTask(t, api, id, body)
	require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
	got, err := api.taskStore.Get(id)
	require.NoError(t, err)
	require.NotNil(t, got.Trigger)
	require.Equal(t, task.TriggerRecurring, got.Trigger.Type)
	assert.Equal(t, task.StatusInbox, got.Status, "TK-30 (3): a future recurring trigger waits in inbox")
}

// TK-30 (4) control: a manual trigger changes no state.
func TestT8RestTriggerState_Manual_KeepsStatus_TK30_4(t *testing.T) {
	api, wsID := t8TriggerStateAPI(t)
	id := t8NextAssignedTask(t, api, wsID)

	w := patchTask(t, api, id, `{"trigger":{"type":"manual","config":{}}}`)
	require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
	got, err := api.taskStore.Get(id)
	require.NoError(t, err)
	assert.Equal(t, task.StatusNext, got.Status, "TK-30 (4): manual changes no state")
}

// TK-30 (5) control: a first occurrence already due follows the shared path
// and does not wait in inbox.
func TestT8RestTriggerState_AlreadyDue_KeepsToday_TK30_5(t *testing.T) {
	api, wsID := t8TriggerStateAPI(t)
	id := t8NextAssignedTask(t, api, wsID)

	at := time.Now().Add(-time.Hour)
	w := patchTask(t, api, id, t8OnceBody(at))
	_, err := api.taskStore.Get(id)
	require.NoError(t, err)
	t.Logf("already-due PATCH answered %d", w.Code)
	require.Equal(t, http.StatusOK, w.Code, "CK-9: the already-due PATCH must succeed: %s", w.Body.String())
	got := t8AssertStoredOnce(t, api, id, at)
	assert.NotEqual(t, task.StatusInbox, got.Status, "TK-30 (5): an already-due trigger never waits in inbox")
	assert.Equal(t, task.StatusNext, got.Status, "CK-9: the already-due write keeps exactly next")
}

// TK-30 (3) control: a blocked task stays blocked.
func TestT8RestTriggerState_Blocked_StaysBlocked_TK30_3(t *testing.T) {
	api, wsID := t8TriggerStateAPI(t)
	dep := createTaskViaAPI(t, api, "t8 dependency", wsID)
	seedBlockedTask(t, api, "t8-blocked-1", "t8 blocked", wsID, []string{dep.Id})
	before, err := api.taskStore.Get("t8-blocked-1")
	require.NoError(t, err)
	require.Equal(t, task.StatusBlocked, before.Status, "instrument: seeded blocked")

	// A scheduled task needs an assignee: assign "mia" first (instrument),
	// then PATCH only the trigger.
	_, aerr := api.taskStore.Update("t8-blocked-1", task.Patch{AgentID: ptrString("mia")})
	require.NoError(t, aerr, "instrument: assign a member to the blocked task")
	at := time.Now().Add(time.Hour)
	w := patchTask(t, api, "t8-blocked-1", t8OnceBody(at))
	t.Logf("blocked PATCH answered %d", w.Code)
	require.Equal(t, http.StatusOK, w.Code, "blocked PATCH: %s", w.Body.String())
	stored, serr := api.taskStore.Get("t8-blocked-1")
	require.NoError(t, serr)
	require.NotNil(t, stored.Trigger, "instrument: the trigger persisted on the blocked task")
	t8AssertStoredOnce(t, api, "t8-blocked-1", at)
	got, err := api.taskStore.Get("t8-blocked-1")
	require.NoError(t, err)
	assert.Equal(t, task.StatusBlocked, got.Status, "TK-30 (3): a blocked task stays blocked")
}

// TK-30 (3) control: a terminal (done) task stays done.
func TestT8RestTriggerState_Terminal_StaysDone_TK30_3(t *testing.T) {
	api, wsID := t8TriggerStateAPI(t)
	done := createTaskWithStatusViaAPI(t, api, "t8 done", wsID, "done")

	// A scheduled task needs an assignee: assign "mia" on the store (instrument).
	_, aerr := api.taskStore.Update(done.Id, task.Patch{AgentID: ptrString("mia")})
	require.NoError(t, aerr, "instrument: assign a member to the done task")
	at := time.Now().Add(time.Hour)
	w := patchTask(t, api, done.Id, t8OnceBody(at))
	t.Logf("terminal PATCH answered %d", w.Code)
	require.Equal(t, http.StatusOK, w.Code, "terminal PATCH: %s", w.Body.String())
	stored, serr := api.taskStore.Get(done.Id)
	require.NoError(t, serr)
	require.NotNil(t, stored.Trigger, "instrument: the trigger persisted on the terminal task")
	t8AssertStoredOnce(t, api, done.Id, at)
	got, err := api.taskStore.Get(done.Id)
	require.NoError(t, err)
	assert.Equal(t, task.StatusDone, got.Status, "TK-30 (3): a terminal task stays terminal")
}

// Round 8, CK-8 control. An explicit standalone restart by a person
// (POST /tasks/{id}/restart on a task they stopped) is NOT an automatic
// promotion: it stays allowed to publish `next` while the future trigger is
// kept exactly. Must stay green whatever the automatic-reset fix looks like.
func TestT8RestTriggerState_PersonRestart_FutureTrigger_StaysNext_TK30(t *testing.T) {
	api, wsID := t8TriggerStateAPI(t)
	created := createTaskViaAPI(t, api, "t8 stopped by person", wsID)
	failed := task.StatusFailed
	reason := task.CancelReasonStoppedByUser
	_, err := api.taskStore.Update(created.Id, task.Patch{AgentID: ptrString("mia"), Status: &failed, CancelReason: &reason})
	require.NoError(t, err, "instrument: a stopped, assigned task")
	at := time.Now().Add(time.Hour)
	w := patchTask(t, api, created.Id, t8OnceBody(at))
	require.Equal(t, http.StatusOK, w.Code, "instrument: add the future trigger to the terminal task: %s", w.Body.String())
	pre := t8AssertStoredOnce(t, api, created.Id, at)
	require.Equal(t, task.StatusFailed, pre.Status, "instrument: a terminal task keeps its status when a trigger is added")

	rw := postTaskAction(t, api, created.Id, "restart")
	require.Equal(t, http.StatusOK, rw.Code, "restart: %s", rw.Body.String())
	got := t8AssertStoredOnce(t, api, created.Id, at)
	assert.Equal(t, task.StatusNext, got.Status, "an explicit person restart stays allowed to run the task now")
}
