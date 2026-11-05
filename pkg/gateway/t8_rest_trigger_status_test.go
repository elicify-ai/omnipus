// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Round 7, CK-4 / CK-5. Chief ruling on r17 TK-30 (3): a changed FUTURE
// trigger on a task that would end up `next` is stored `inbox` whatever the
// request's status says; an explicit `inbox` or `blocked` request stays as
// asked; running and terminal behave as today; an unchanged-trigger re-save
// keeps `next`.

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// t8TriggerWithStatus builds a PATCH body with a status and the once trigger.
func t8TriggerWithStatus(status string, at time.Time) string {
	return `{"status":"` + status + `","trigger":{"type":"once","config":{"at_ms":` +
		strconv.FormatInt(at.UnixMilli(), 10) + `}}}`
}

func t8AssertStoredOnce(t *testing.T, api *restAPI, id string, at time.Time) *task.Task {
	t.Helper()
	got, err := api.taskStore.Get(id)
	require.NoError(t, err)
	require.NotNil(t, got.Trigger, "instrument: the trigger persisted")
	require.Equal(t, task.TriggerOnce, got.Trigger.Type)
	require.NotNil(t, got.Trigger.Config.AtMs)
	require.Equal(t, at.UnixMilli(), *got.Trigger.Config.AtMs, "the exact trigger is stored")
	return got
}

// CK-4: PATCH status:"next" together with a changed future trigger is stored inbox.
func TestT8RestTriggerStatus_ExplicitNextWithFutureTrigger_StoredInbox_TK30_3(t *testing.T) {
	api, wsID := t8TriggerStateAPI(t)
	id := t8NextAssignedTask(t, api, wsID)
	at := time.Now().Add(time.Hour)

	w := patchTask(t, api, id, t8TriggerWithStatus("next", at))
	require.Equal(t, http.StatusOK, w.Code, "PATCH status next + future trigger: %s", w.Body.String())
	got := t8AssertStoredOnce(t, api, id, at)
	assert.Equal(t, task.StatusInbox, got.Status, "CK-4: a changed future trigger stores inbox whatever the status says")
}

// CK-4 from inbox: inbox -> next with a future trigger in the same write ends inbox.
func TestT8RestTriggerStatus_InboxToNextWithFutureTrigger_StoredInbox_TK30_3(t *testing.T) {
	api, wsID := t8TriggerStateAPI(t)
	created := createTaskViaAPI(t, api, "t8 inbox to next", wsID)
	at := time.Now().Add(time.Hour)

	w := patchTask(t, api, created.Id, `{"agent_id":"mia","description":"ready","status":"next","trigger":{"type":"once","config":{"at_ms":`+
		strconv.FormatInt(at.UnixMilli(), 10)+`}}}`)
	require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
	got := t8AssertStoredOnce(t, api, created.Id, at)
	assert.Equal(t, task.StatusInbox, got.Status, "CK-4: inbox + next + future trigger ends inbox")
}

// CK-4 control: an explicit inbox request stays inbox.
func TestT8RestTriggerStatus_ExplicitInbox_StaysInbox_TK30_3(t *testing.T) {
	api, wsID := t8TriggerStateAPI(t)
	id := t8NextAssignedTask(t, api, wsID)
	at := time.Now().Add(time.Hour)

	w := patchTask(t, api, id, t8TriggerWithStatus("inbox", at))
	require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
	got := t8AssertStoredOnce(t, api, id, at)
	assert.Equal(t, task.StatusInbox, got.Status, "explicit inbox stays as asked")
}

// CK-4 control: an explicit blocked request is refused on REST (r17 TK-14:
// blocked is engine-only; handler text "blocked is a derived side-state") and
// nothing is stored.
func TestT8RestTriggerStatus_ExplicitBlocked_RefusedNothingStored_TK14(t *testing.T) {
	api, wsID := t8TriggerStateAPI(t)
	id := t8NextAssignedTask(t, api, wsID)
	at := time.Now().Add(time.Hour)

	w := patchTask(t, api, id, t8TriggerWithStatus("blocked", at))
	require.Equal(t, http.StatusBadRequest, w.Code, "%s", w.Body.String())
	got, err := api.taskStore.Get(id)
	require.NoError(t, err)
	assert.Equal(t, task.StatusNext, got.Status, "a refused request changes no status")
	assert.Nil(t, got.Trigger, "a refused request stores no trigger")
}

// CK-4 positive control: an unchanged-trigger re-save keeps next.
func TestT8RestTriggerStatus_UnchangedTriggerResave_KeepsNext_TK30_3(t *testing.T) {
	api, wsID := t8TriggerStateAPI(t)
	id := t8NextAssignedTask(t, api, wsID)
	at := time.Now().Add(time.Hour)

	ms := at.UnixMilli()
	trig := &task.Trigger{Type: task.TriggerOnce}
	trig.Config.AtMs = &ms
	// Instrument, two writes: (1) the trigger alone (a changed future trigger
	// lands inbox under the CK-4 ruling), then (2) status next WITHOUT a
	// trigger. The task ends next with a future trigger stored.
	_, err := api.taskStore.Update(id, task.Patch{Trigger: &trig})
	require.NoError(t, err, "instrument: write the trigger alone")
	next := task.StatusNext
	_, err = api.taskStore.Update(id, task.Patch{Status: &next})
	require.NoError(t, err, "instrument: set status next without a trigger")
	seeded, err := api.taskStore.Get(id)
	require.NoError(t, err)
	require.Equal(t, task.StatusNext, seeded.Status, "instrument: seeded next with a trigger")

	w := patchTask(t, api, id, t8TriggerWithStatus("next", at))
	require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
	got := t8AssertStoredOnce(t, api, id, at)
	assert.Equal(t, task.StatusNext, got.Status, "an unchanged trigger re-save keeps next")
}
