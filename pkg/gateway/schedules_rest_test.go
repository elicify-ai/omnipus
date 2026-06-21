//go:build !cgo

// /api/v1/schedules + /api/v1/notifications REST tests (#264, FR-015). Exercises
// create 201 + owner authz 403, unknown 404, invalid trigger 400, run-now, pause
// toggle, list projection, and notification list/read.

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/cron"
	"github.com/dapicom-ai/omnipus/pkg/notifications"
)

func newSchedulesTestAPI(t *testing.T) (*restAPI, *cron.CronService) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Type: config.AgentTypeCustom, OwnerUsername: "alice"},
		{ID: "max", Type: config.AgentTypeCustom, OwnerUsername: "bob"},
	}
	msgBus := bus.NewMessageBus()
	loop := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	cronPath := filepath.Join(t.TempDir(), "jobs.json")
	cs := cron.NewCronService(cronPath)
	require.NoError(t, cs.Start())
	t.Cleanup(cs.Stop)

	notifs := notifications.NewStore(t.TempDir())
	api := &restAPI{agentLoop: loop, notifStore: notifs}
	api.cronService.Store(cs)
	return api, cs
}

// withUser attaches an authenticated user to the request context.
func withUser(r *http.Request, username string, role config.UserRole) *http.Request {
	u := &config.UserConfig{Username: username, Role: role}
	return r.WithContext(context.WithValue(r.Context(), UserContextKey{}, u))
}

func createScheduleReq(t *testing.T, owner string) *bytes.Buffer {
	t.Helper()
	body := gen.ScheduleCreate{
		Name:         "daily report",
		Message:      "summarize PRs",
		OwnerAgentId: owner,
	}
	body.Trigger.Kind = "cron"
	expr := "0 18 * * *"
	body.Trigger.CronExpr = &expr
	buf, _ := json.Marshal(body)
	return bytes.NewBuffer(buf)
}

func TestSchedulesAPI_Create201WithOwner(t *testing.T) {
	api, cs := newSchedulesTestAPI(t)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/schedules", createScheduleReq(t, "mia"))
	r = withUser(r, "alice", config.UserRoleUser) // alice owns mia
	w := httptest.NewRecorder()
	api.HandleSchedules(w, r)

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var got gen.Schedule
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "mia", got.OwnerAgentId)
	require.NotNil(t, got.CreatedBy)
	assert.Equal(t, "alice", *got.CreatedBy)

	// Persisted in cron with the owner.
	job, ok := cs.GetJob(got.Id)
	require.True(t, ok)
	assert.Equal(t, "mia", job.AgentID)
	assert.Equal(t, "alice", job.CreatedBy)
}

func TestSchedulesAPI_Create403WrongOwner(t *testing.T) {
	api, _ := newSchedulesTestAPI(t)
	// alice (a non-admin user) tries to schedule for "max" which bob owns.
	r := httptest.NewRequest(http.MethodPost, "/api/v1/schedules", createScheduleReq(t, "max"))
	r = withUser(r, "alice", config.UserRoleUser)
	w := httptest.NewRecorder()
	api.HandleSchedules(w, r)
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
}

func TestSchedulesAPI_AdminCanOwnAnyAgent(t *testing.T) {
	api, _ := newSchedulesTestAPI(t)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/schedules", createScheduleReq(t, "max"))
	r = withUser(r, "root", config.UserRoleAdmin)
	w := httptest.NewRecorder()
	api.HandleSchedules(w, r)
	assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())
}

// TestSchedulesAPI_Create400WorkerOwner verifies the write-time guard that
// rejects creating a schedule whose owner is a worker. A worker is not a chat
// target and never runs on a schedule cadence (workers run only via
// delegation). The gate fires before any persistence.
func TestSchedulesAPI_Create400WorkerOwner(t *testing.T) {
	api, cs := newSchedulesTestAPI(t)

	// Inject a worker into the config so the worker-guard predicate fires.
	loop := api.agentLoop
	cf := loop.GetConfig()
	cf.Agents.List = append(cf.Agents.List, config.AgentConfig{
		ID:            "worker-test",
		Name:          "Worker",
		Type:          config.AgentTypeWorker,
		OwnerUsername: "alice",
	})

	r := httptest.NewRequest(http.MethodPost, "/api/v1/schedules", createScheduleReq(t, "worker-test"))
	r = withUser(r, "alice", config.UserRoleUser)
	w := httptest.NewRecorder()
	api.HandleSchedules(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "worker",
		"the rejection message must reference the worker tier")

	// Persisted: nothing — the schedule must not be in the cron store.
	for _, j := range cs.ListJobs(true) {
		if j.AgentID == "worker-test" {
			t.Fatalf("a worker-owned schedule leaked into the cron store: %+v", j)
		}
	}
}

// TestSchedulesAPI_Update400WorkerOwner verifies the update-time guard that
// rejects reassigning a schedule's owner to a worker. Same fire-before-persist
// property as the create guard.
func TestSchedulesAPI_Update400WorkerOwner(t *testing.T) {
	api, cs := newSchedulesTestAPI(t)

	// Seed a base-owned schedule we will try to reassign to a worker.
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

	// Inject a worker so the update-guard predicate fires.
	cf := api.agentLoop.GetConfig()
	cf.Agents.List = append(cf.Agents.List, config.AgentConfig{
		ID:            "worker-test",
		Name:          "Worker",
		Type:          config.AgentTypeWorker,
		OwnerUsername: "alice",
	})

	updateBody := gen.ScheduleUpdate{OwnerAgentId: ptr("worker-test")}
	buf, _ := json.Marshal(updateBody)

	r := httptest.NewRequest(http.MethodPut, "/api/v1/schedules/"+job.ID, bytes.NewBuffer(buf))
	r.Header.Set("Content-Type", "application/json")
	r = withUser(r, "alice", config.UserRoleUser)
	w := httptest.NewRecorder()
	api.HandleSchedules(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "worker")

	// The stored job's owner must still be "mia" — the failed PUT must not
	// have side-effected the owner field.
	got, ok := cs.GetJob(job.ID)
	require.True(t, ok)
	assert.Equal(t, "mia", got.AgentID, "failed worker-owner PUT must not change stored owner")
}

// Note: ptr() helper is provided by rest_tasks.go as ptr[T any](v T) *T — use that.

func TestSchedulesAPI_InvalidTrigger400(t *testing.T) {
	api, _ := newSchedulesTestAPI(t)
	body := gen.ScheduleCreate{Name: "n", Message: "m", OwnerAgentId: "mia"}
	body.Trigger.Kind = "cron"
	bad := "not a cron expr at all"
	body.Trigger.CronExpr = &bad
	buf, _ := json.Marshal(body)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/schedules", bytes.NewBuffer(buf))
	r = withUser(r, "alice", config.UserRoleUser)
	w := httptest.NewRecorder()
	api.HandleSchedules(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

func TestSchedulesAPI_NotFound404(t *testing.T) {
	api, _ := newSchedulesTestAPI(t)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/schedules/nope", nil)
	r = withUser(r, "alice", config.UserRoleUser)
	w := httptest.NewRecorder()
	api.HandleSchedules(w, r)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
}

func TestSchedulesAPI_ListMapsTriggerStateRuns(t *testing.T) {
	api, cs := newSchedulesTestAPI(t)
	// Seed a job directly with history + state.
	job, err := cs.AddJob(
		"nightly",
		cron.CronSchedule{Kind: "every", EveryMS: i64p(60000)},
		"go",
		false,
		"telegram",
		"c1",
	)
	require.NoError(t, err)
	job.AgentID = "mia"
	job.SessionMode = cron.SessionModeContinue
	job.State.LastStatus = "error"
	job.State.LastError = "boom"
	job.State.ConsecutiveFailures = 2
	job.State.History = []cron.CronRunRecord{
		{RanAtMs: 1, Status: "ok", DurationMs: 10, SessionID: "s1"},
		{RanAtMs: 2, Status: "error", Error: "boom", DurationMs: 20},
	}
	require.NoError(t, cs.UpdateJob(job))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/schedules", nil)
	r = withUser(r, "alice", config.UserRoleUser)
	w := httptest.NewRecorder()
	api.HandleSchedules(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var list gen.ScheduleList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	require.Len(t, list.Schedules, 1)
	s := list.Schedules[0]
	assert.Equal(t, "mia", s.OwnerAgentId)
	assert.Equal(t, gen.ScheduleListSchedulesTriggerKind("every"), s.Trigger.Kind)
	require.NotNil(t, s.Trigger.EveryMs)
	assert.Equal(t, int64(60000), *s.Trigger.EveryMs)
	require.NotNil(t, s.State.LastStatus)
	assert.Equal(t, "error", *s.State.LastStatus)
	require.NotNil(t, s.State.ConsecutiveFailures)
	assert.Equal(t, 2, *s.State.ConsecutiveFailures)
	require.NotNil(t, s.Runs)
	require.Len(t, *s.Runs, 2)
	// Newest first.
	assert.Equal(t, int64(2), (*s.Runs)[0].RanAtMs)
}

func TestSchedulesAPI_PauseToggles(t *testing.T) {
	api, cs := newSchedulesTestAPI(t)
	job, err := cs.AddJob("j", cron.CronSchedule{Kind: "every", EveryMS: i64p(60000)}, "m", false, "", "")
	require.NoError(t, err)
	job.AgentID = "mia"
	require.NoError(t, cs.UpdateJob(job))
	assert.True(t, job.Enabled)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/schedules/"+job.ID+"/pause", nil)
	r = withUser(r, "alice", config.UserRoleUser)
	w := httptest.NewRecorder()
	api.HandleSchedules(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got gen.Schedule
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.False(t, got.Enabled, "pause should disable an enabled schedule")
}

func TestSchedulesAPI_RunNow(t *testing.T) {
	api, cs := newSchedulesTestAPI(t)
	// deliver=true so RunNow completes deterministically without an agent turn.
	// Wire a runner that direct-sends.
	msgBus := bus.NewMessageBus()
	go func() {
		for range msgBus.OutboundChan() {
		}
	}()
	cs.SetRunner(newScheduledRunner(
		&fakeExecutor{cfg: api.agentLoop.GetConfig()},
		fakeChecker{registered: map[string]bool{"mia": true}},
		msgBus, api.notifStore, api.agentLoop.GetConfig,
	))

	job, err := cs.AddJob("rn", cron.CronSchedule{Kind: "every", EveryMS: i64p(60000)}, "hi", true, "telegram", "c1")
	require.NoError(t, err)
	job.AgentID = "mia"
	require.NoError(t, cs.UpdateJob(job))

	r := httptest.NewRequest(http.MethodPost, "/api/v1/schedules/"+job.ID+"/run", nil)
	r = withUser(r, "alice", config.UserRoleUser)
	w := httptest.NewRecorder()
	api.HandleSchedules(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var res gen.ScheduleRunResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, job.ID, res.ScheduleId)
	assert.Equal(t, gen.ScheduleRunResultStatus("ok"), res.Status)
}

func TestSchedulesAPI_DeleteAndGet(t *testing.T) {
	api, cs := newSchedulesTestAPI(t)
	job, err := cs.AddJob("d", cron.CronSchedule{Kind: "every", EveryMS: i64p(60000)}, "m", false, "", "")
	require.NoError(t, err)
	job.AgentID = "mia"
	require.NoError(t, cs.UpdateJob(job))

	// DELETE.
	rd := withUser(
		httptest.NewRequest(http.MethodDelete, "/api/v1/schedules/"+job.ID, nil),
		"alice",
		config.UserRoleUser,
	)
	wd := httptest.NewRecorder()
	api.HandleSchedules(wd, rd)
	assert.Equal(t, http.StatusNoContent, wd.Code)

	// GET now 404.
	rg := withUser(httptest.NewRequest(http.MethodGet, "/api/v1/schedules/"+job.ID, nil), "alice", config.UserRoleUser)
	wg := httptest.NewRecorder()
	api.HandleSchedules(wg, rg)
	assert.Equal(t, http.StatusNotFound, wg.Code)
}

// ---- notifications ----

func TestNotificationsAPI_ListAndRead(t *testing.T) {
	api, _ := newSchedulesTestAPI(t)
	_, _ = api.notifStore.Create(notifications.Notification{
		Recipient: "alice", Type: notifications.TypeScheduleFailed, Title: "x",
		Severity: notifications.SeverityError, ScheduleID: "s1",
	})

	// List.
	rl := withUser(httptest.NewRequest(http.MethodGet, "/api/v1/notifications", nil), "alice", config.UserRoleUser)
	wl := httptest.NewRecorder()
	api.HandleNotifications(wl, rl)
	require.Equal(t, http.StatusOK, wl.Code, wl.Body.String())

	var list gen.NotificationList
	require.NoError(t, json.Unmarshal(wl.Body.Bytes(), &list))
	require.Len(t, list.Notifications, 1)
	assert.Equal(t, 1, list.UnreadCount)
	id := list.Notifications[0].Id

	// Mark read.
	rr := withUser(
		httptest.NewRequest(http.MethodPost, "/api/v1/notifications/"+id+"/read", nil),
		"alice",
		config.UserRoleUser,
	)
	wr := httptest.NewRecorder()
	api.HandleNotifications(wr, rr)
	assert.Equal(t, http.StatusNoContent, wr.Code)

	c, _ := api.notifStore.UnreadCount("alice")
	assert.Equal(t, 0, c)
}

func TestNotificationsAPI_ReadAll(t *testing.T) {
	api, _ := newSchedulesTestAPI(t)
	_, _ = api.notifStore.Create(notifications.Notification{Recipient: "alice", ScheduleID: "a", Title: "1"})
	_, _ = api.notifStore.Create(notifications.Notification{Recipient: "alice", ScheduleID: "b", Title: "2"})

	rr := withUser(
		httptest.NewRequest(http.MethodPost, "/api/v1/notifications/read-all", nil),
		"alice",
		config.UserRoleUser,
	)
	wr := httptest.NewRecorder()
	api.HandleNotifications(wr, rr)
	assert.Equal(t, http.StatusNoContent, wr.Code)

	c, _ := api.notifStore.UnreadCount("alice")
	assert.Equal(t, 0, c)
}

func i64p(v int64) *int64 { return &v }

// seedJob creates a persisted schedule owned by ownerAgent via AddJobFull, so it
// carries the owner atomically (no empty-owner intermediate window).
func seedJob(t *testing.T, cs *cron.CronService, ownerAgent, createdBy string) *cron.CronJob {
	t.Helper()
	job, err := cs.AddJobFull(cron.JobSpec{
		Name:      "seeded",
		Schedule:  cron.CronSchedule{Kind: "every", EveryMS: i64p(60000)},
		Message:   "m",
		AgentID:   ownerAgent,
		CreatedBy: createdBy,
	})
	require.NoError(t, err)
	return job
}

// TestSchedulesAPI_Authz_PerOperation asserts B3: a non-owner non-admin gets 403
// on get/update/delete/run/pause for a schedule they do not own; the owner and
// admins are allowed. "mia" is owned by alice; "bob" is a non-owner non-admin.
func TestSchedulesAPI_Authz_PerOperation(t *testing.T) {
	for _, op := range []struct {
		name   string
		method string
		path   string // relative to /api/v1/schedules/{id}
		body   func(id string) *bytes.Buffer
	}{
		{"get", http.MethodGet, "", nil},
		{"delete", http.MethodDelete, "", nil},
		{"run", http.MethodPost, "/run", nil},
		{"pause", http.MethodPost, "/pause", nil},
		{"update", http.MethodPut, "", func(string) *bytes.Buffer {
			b, _ := json.Marshal(gen.ScheduleUpdate{})
			return bytes.NewBuffer(b)
		}},
	} {
		t.Run(op.name, func(t *testing.T) {
			api, cs := newSchedulesTestAPI(t)
			job := seedJob(t, cs, "mia", "alice")

			var body *bytes.Buffer = bytes.NewBuffer(nil)
			if op.body != nil {
				body = op.body(job.ID)
			}
			url := "/api/v1/schedules/" + job.ID + op.path

			// bob (non-owner, non-admin) → 403.
			r := withUser(httptest.NewRequest(op.method, url, body), "bob", config.UserRoleUser)
			w := httptest.NewRecorder()
			api.HandleSchedules(w, r)
			assert.Equal(
				t, http.StatusForbidden, w.Code,
				"%s by non-owner must be 403; body=%s", op.name, w.Body.String(),
			)

			// alice (owner) → not 403/404.
			var ab *bytes.Buffer = bytes.NewBuffer(nil)
			if op.body != nil {
				ab = op.body(job.ID)
			}
			ra := withUser(httptest.NewRequest(op.method, url, ab), "alice", config.UserRoleUser)
			wa := httptest.NewRecorder()
			api.HandleSchedules(wa, ra)
			assert.NotEqual(t, http.StatusForbidden, wa.Code, "%s by owner must not be 403", op.name)
			assert.NotEqual(t, http.StatusNotFound, wa.Code, "%s by owner must not be 404", op.name)
		})
	}
}

// TestSchedulesAPI_Authz_AdminAllOperations asserts admins pass every gate.
func TestSchedulesAPI_Authz_AdminAllOperations(t *testing.T) {
	api, cs := newSchedulesTestAPI(t)
	job := seedJob(t, cs, "max", "bob") // owned by bob/max; root is admin

	r := withUser(httptest.NewRequest(http.MethodGet, "/api/v1/schedules/"+job.ID, nil), "root", config.UserRoleAdmin)
	w := httptest.NewRecorder()
	api.HandleSchedules(w, r)
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

// TestSchedulesAPI_List_FilteredByOwner asserts the list is filtered to
// schedules a non-admin may access; an admin sees all (B3).
func TestSchedulesAPI_List_FilteredByOwner(t *testing.T) {
	api, cs := newSchedulesTestAPI(t)
	_ = seedJob(t, cs, "mia", "alice") // alice's
	_ = seedJob(t, cs, "max", "bob")   // bob's

	// alice (non-admin) sees only mia's schedule.
	ra := withUser(httptest.NewRequest(http.MethodGet, "/api/v1/schedules", nil), "alice", config.UserRoleUser)
	wa := httptest.NewRecorder()
	api.HandleSchedules(wa, ra)
	require.Equal(t, http.StatusOK, wa.Code, wa.Body.String())
	var aliceList gen.ScheduleList
	require.NoError(t, json.Unmarshal(wa.Body.Bytes(), &aliceList))
	require.Len(t, aliceList.Schedules, 1)
	assert.Equal(t, "mia", aliceList.Schedules[0].OwnerAgentId)

	// admin sees both.
	rad := withUser(httptest.NewRequest(http.MethodGet, "/api/v1/schedules", nil), "root", config.UserRoleAdmin)
	wad := httptest.NewRecorder()
	api.HandleSchedules(wad, rad)
	require.Equal(t, http.StatusOK, wad.Code)
	var adminList gen.ScheduleList
	require.NoError(t, json.Unmarshal(wad.Body.Bytes(), &adminList))
	assert.Len(t, adminList.Schedules, 2)
}

// TestSchedulesAPI_Create_AtomicOwner asserts the create path persists the owner
// atomically via AddJobFull — the stored job has the owner set with no
// empty-owner intermediate (M3).
func TestSchedulesAPI_Create_AtomicOwner(t *testing.T) {
	api, cs := newSchedulesTestAPI(t)
	r := withUser(
		httptest.NewRequest(http.MethodPost, "/api/v1/schedules", createScheduleReq(t, "mia")),
		"alice", config.UserRoleUser,
	)
	w := httptest.NewRecorder()
	api.HandleSchedules(w, r)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var got gen.Schedule
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))

	// Every persisted job in the store must carry a non-empty owner — proving no
	// owner-less intermediate write ever landed in the store.
	jobs := cs.ListJobs(true)
	require.Len(t, jobs, 1)
	assert.Equal(t, "mia", jobs[0].AgentID, "owner must be set atomically on create")
	assert.Equal(t, "alice", jobs[0].CreatedBy)
	assert.Equal(t, got.Id, jobs[0].ID)
}

// TestSchedulesAPI_Update_InvalidSessionMode400 asserts an invalid session_mode
// on update is rejected with 400 before persisting (M5b).
func TestSchedulesAPI_Update_InvalidSessionMode400(t *testing.T) {
	api, cs := newSchedulesTestAPI(t)
	job := seedJob(t, cs, "mia", "alice")

	bad := gen.ScheduleUpdateSessionMode("bogus-mode")
	body, _ := json.Marshal(gen.ScheduleUpdate{SessionMode: &bad})
	r := withUser(
		httptest.NewRequest(http.MethodPut, "/api/v1/schedules/"+job.ID, bytes.NewBuffer(body)),
		"alice", config.UserRoleUser,
	)
	w := httptest.NewRecorder()
	api.HandleSchedules(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())

	// Stored mode unchanged.
	stored, ok := cs.GetJob(job.ID)
	require.True(t, ok)
	assert.NotEqual(t, cron.SessionMode("bogus-mode"), stored.SessionMode)
}
