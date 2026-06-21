//go:build !cgo

// Regression tests for the schedule worker-guard order fix.
//
// Background:
//   Before this fix, handleCreateSchedule and handleUpdateSchedule called
//   authorizeScheduleOwner BEFORE the worker-ownership write-time guard.
//   A non-admin who named a worker owner got 403 (a permissions problem),
//   not 400 (a worker-validity problem). The 400 is the actionable
//   response — the operator cannot schedule for this agent at all,
//   regardless of who owns it.
//
// These tests prove the worker guard now fires before the owner-authz
// check: a non-admin who names a worker they do not own gets 400 (not 403)
// on both create and update.

package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/cron"
)

// TestSchedulesAPI_Create400WorkerOwner_NonAdminNonOwner is the create-path
// regression. Alice is not the owner of "max-worker" (bob is), so the old
// order (authz first) returned 403; the new order (worker guard first)
// returns 400 with a message that names the worker rule.
func TestSchedulesAPI_Create400WorkerOwner_NonAdminNonOwner(t *testing.T) {
	api, cs := newSchedulesTestAPI(t)

	// Inject a worker owned by bob, not alice. The old guard order fired
	// authorizeScheduleOwner first (alice is not bob → 403); the new order
	// fires the worker-guard first (any worker → 400 regardless of caller).
	cf := api.agentLoop.GetConfig()
	cf.Agents.List = append(cf.Agents.List, config.AgentConfig{
		ID:            "max-worker",
		Name:          "Max Worker",
		Type:          config.AgentTypeWorker,
		OwnerUsername: "bob",
	})

	r := httptest.NewRequest(http.MethodPost, "/api/v1/schedules",
		createScheduleReq(t, "max-worker"))
	r = withUser(r, "alice", config.UserRoleUser)
	w := httptest.NewRecorder()
	api.HandleSchedules(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"non-admin picking a worker owner must get 400 (worker guard fires before authz), got: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "worker",
		"the rejection must reference the worker tier, not 'forbidden'")

	// Persisted: nothing — the schedule must not be in the cron store.
	for _, j := range cs.ListJobs(true) {
		if j.AgentID == "max-worker" {
			t.Fatalf("a worker-owned schedule leaked into the cron store: %+v", j)
		}
	}
}

// TestSchedulesAPI_Update400WorkerOwner_NonAdminNonOwner is the parallel
// regression for the update path. Alice tries to reassign a schedule she
// already owns (mia) to a worker owned by bob. With the old order
// (owner re-authz first) this was 403; with the new order (worker guard
// first) it is 400. The existing job's access check still gates the
// schedule itself first (alice owns mia → permitted to mutate), so the
// only thing in play at the owner-change step is the worker-vs-authz
// ordering.
func TestSchedulesAPI_Update400WorkerOwner_NonAdminNonOwner(t *testing.T) {
	api, cs := newSchedulesTestAPI(t)

	// Seed a schedule owned by alice (the caller) so the existing-job
	// authorizeScheduleAccess check passes.
	job, err := cs.AddJob(
		"daily",
		cron.CronSchedule{Kind: "every", EveryMS: i64p(60000)},
		"go",
		false,
		"",
		"",
	)
	require.NoError(t, err)
	job.AgentID = "mia"
	job.CreatedBy = "alice"
	require.NoError(t, cs.UpdateJob(job))

	// Inject a worker owned by bob.
	cf := api.agentLoop.GetConfig()
	cf.Agents.List = append(cf.Agents.List, config.AgentConfig{
		ID:            "max-worker",
		Name:          "Max Worker",
		Type:          config.AgentTypeWorker,
		OwnerUsername: "bob",
	})

	updateBody := gen.ScheduleUpdate{OwnerAgentId: ptr("max-worker")}
	buf, _ := json.Marshal(updateBody)

	r := httptest.NewRequest(http.MethodPut, "/api/v1/schedules/"+job.ID, bytes.NewBuffer(buf))
	r.Header.Set("Content-Type", "application/json")
	r = withUser(r, "alice", config.UserRoleUser)
	w := httptest.NewRecorder()
	api.HandleSchedules(w, r)

	assert.Equal(
		t,
		http.StatusBadRequest,
		w.Code,
		"non-admin owner-reassigning to a worker must get 400 (worker guard before owner re-authz), got: %s",
		w.Body.String(),
	)
	assert.Contains(t, w.Body.String(), "worker")

	// Stored owner must still be "mia" — the failed PUT must not have
	// side-effected the owner field.
	got, ok := cs.GetJob(job.ID)
	require.True(t, ok)
	assert.Equal(t, "mia", got.AgentID,
		"failed worker-owner PUT must not change stored owner")
}
