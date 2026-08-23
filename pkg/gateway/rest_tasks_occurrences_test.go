// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_tasks_occurrences_test.go — integration tests 12 and 13 of the
// Calendar Recurrence Redesign TDD plan
// (docs/internal/specs/calendar-recurrence-redesign-spec.md):
//
//	TestRestTasks_CreateRecurringRrule  (12) — POST/PUT rrule paths + the
//	  FR-022 recurrence-change audit hook + validation 400s.
//	TestRestTasks_OccurrencesEndpoint   (13) — GET /api/v1/tasks/occurrences:
//	  bucketed shape, the task-selection predicate, empty/zero-occurrence
//	  shapes, 400s, routing precedence, and the dedicated rate limiter.
//
// Per project CI discipline these are integration-level (link the full
// gateway test binary) — CI/ci-omnipus is authoritative; only ONE of these
// is smoke-tested locally per the implementer's task brief.

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newTestRestAPIWithHomeAndAgent is newTestRestAPIWithHome (rest_extra_test.go)
// plus one real, chat-target agent ("mia") seeded into cfg.Agents.List BEFORE
// the AgentLoop/AgentRegistry is constructed. newTestRestAPIWithHome itself
// seeds NO agents — the retired "main" sentinel used to be registered
// implicitly regardless of cfg (pkg/agent/registry.go's old always-on
// fallback), papering over that. That sentinel is gone with no back-compat,
// so any test using this file's createRecurringTaskViaAPI (which assigns
// agent_id="mia" — validateTaskAgentID/validateScheduledAgentAssignment
// require a real registered agent) needs one seeded from construction.
//
// Deliberately does NOT reuse the existing addAgentsToAPI helper
// (channel_routing_binding_test.go): that helper persists an entity record
// and calls refreshConfigAndRewireServices, which only swaps
// AgentLoop.cfg (SwapConfig) — it does NOT rebuild AgentLoop.registry (see
// SwapConfig's own doc comment: "al.registry is untouched"). validateTaskAgentID
// reads a.agentLoop.GetRegistry(), so a post-hoc addAgentsToAPI call is
// invisible to it; only an agent present in cfg.Agents.List at
// agent.NewAgentLoop construction time (which builds the registry fresh from
// that list) is resolvable here.
func newTestRestAPIWithHomeAndAgent(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[{"id":"mia"}]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
		taskLock:      task.TaskFileLock,
	}
}

// newTestRestAPIWithTaskTrigger extends newTestRestAPIAlignedStores with a
// live agent.TaskTriggerScheduler wired into the agent loop
// (agentLoop.SetTaskTriggerScheduler), mirroring gateway.go's production boot
// wiring (NewTaskTriggerScheduler -> Start -> SetTaskTriggerScheduler). REST
// create/PATCH calls flow through AgentLoop.NotifyTaskUpserted, so with this
// wired a real cron job is armed/no-op'd exactly as in production — required
// for the occurrences endpoint's `every` anchor (NextRunAtMSForTask,
// rest_tasks.go's everyAnchor closure) to resolve to anything other than
// ok=false. Returns the scheduler too, so a test can also drive it directly
// (RunDueJobs/WaitForLane) or read its armed state.
func newTestRestAPIWithTaskTrigger(t *testing.T) (*restAPI, *agent.TaskTriggerScheduler) {
	t.Helper()
	api := newTestRestAPIAlignedStores(t)
	storePath := filepath.Join(t.TempDir(), "triggers", "jobs.json")
	sched := agent.NewTaskTriggerScheduler(storePath, api.taskStore, api.taskExecutor)
	require.NoError(t, sched.Start())
	t.Cleanup(sched.Stop)
	api.agentLoop.SetTaskTriggerScheduler(sched)
	return api, sched
}

// createRecurringTaskViaAPI POSTs a task with a `recurring` RRULE trigger and
// returns the wire struct. surface, when non-empty, is passed through
// (e.g. "heartbeat" for the selection-predicate tests).
//
// A recurring+llm task must carry an agent_id (store.validateScheduledAgentAssignment
// — a scheduled task with no agent is a dead task, rejected at Create). "mia"
// is newTestRestAPIAlignedStoresWithProvider's one explicitly seeded
// chat-target agent (the retired "main" sentinel used to be registered
// implicitly regardless of cfg — see that helper's comment); it is put on
// wsID's core team here (mirroring TestHandleTaskPatch_InProgress_WithKnownAgent's
// setWorkspaceCoreTeam precedent) so validateTaskAgentID's team-membership
// check accepts it.
func createRecurringTaskViaAPI(
	t *testing.T,
	api *restAPI,
	wsID, title, rrule string,
	dtstartMs int64,
	tz, surface string,
) gen.Task {
	t.Helper()
	setWorkspaceCoreTeam(t, api, wsID, []string{"mia"})
	surfaceField := ""
	if surface != "" {
		surfaceField = fmt.Sprintf(`,"surface":%q`, surface)
	}
	body := fmt.Sprintf(
		`{"title":%q,"action":"llm","workspace_id":%q,"agent_id":"mia","trigger":{"type":"recurring","config":{"rrule":%q,"dtstart_ms":%d,"tz":%q}}%s}`,
		title,
		wsID,
		rrule,
		dtstartMs,
		tz,
		surfaceField,
	)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks"
	api.HandleTasks(w, r)
	require.Equal(
		t,
		http.StatusCreated,
		w.Code,
		"createRecurringTaskViaAPI: POST must return 201; body=%s",
		w.Body.String(),
	)
	var tsk gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tsk))
	return tsk
}

// advanceTaskToDone walks id through the legal inbox->next->in_progress->done
// transition path (matches createTaskWithStatusViaAPI's sequence).
//
// When the in_progress transition has an assigned agent, handleTaskPatch
// synchronously calls StartTaskNow, which creates a session (its id comes
// back in THIS patch's own response) and launches the actual LLM turn in a
// detached goroutine (see task_trigger.go's "dispatch is asynchronous"
// precedent) — that goroutine keeps writing session/memory files under the
// test's t.TempDir() after this function would otherwise return. Forcing
// status=done immediately and only THEN polling for a terminal status would
// self-satisfy on the very first poll (we just wrote "done" ourselves) and
// wait zero real time — so this waits BEFORE the force-done patch, using the
// in_progress response's session_id as the "a background run was launched"
// signal, so the terminal-status poll genuinely observes the goroutine's own
// completion. Closes a TempDir-cleanup race ("TempDir RemoveAll cleanup: ...
// directory not empty") that pre-dates this helper's callers in this file —
// reproducible on unmodified pkg/gateway HEAD, not introduced by this fix's
// own new callers, but shared by all of them (see the 7-reviewer gate report
// for the recurring-task scheduler fix, C1a/C1b/FIX-2's proof tests, for the
// analysis; fixed once here in the shared helper rather than per test).
func advanceTaskToDone(t *testing.T, api *restAPI, id string) {
	t.Helper()
	require.Equal(t, http.StatusOK, patchTask(t, api, id, `{"status":"next","description":"x"}`).Code)

	wIP := patchTask(t, api, id, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, wIP.Code)
	var inProgress gen.Task
	require.NoError(t, json.Unmarshal(wIP.Body.Bytes(), &inProgress))
	if inProgress.SessionId != nil && *inProgress.SessionId != "" {
		require.Eventually(t, func() bool {
			s := getTaskStatus(t, api, id)
			return s == gen.TaskStatusDone || s == gen.TaskStatusFailed
		}, 10*time.Second, 20*time.Millisecond,
			"background run launched by the in_progress transition must reach a terminal state "+
				"before advanceTaskToDone forces status=done")
	}

	require.Equal(t, http.StatusOK, patchTask(t, api, id, `{"status":"done"}`).Code)
}

// getOccurrences calls HandleTaskOccurrences directly (bypassing auth/rate
// -limit wrapping — matches the direct-handler-call convention used
// throughout this package's task tests) and returns the recorder.
func getOccurrences(t *testing.T, api *restAPI, params url.Values) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/occurrences?"+params.Encode(), nil)
	api.HandleTaskOccurrences(w, r)
	return w
}

// --- Test 12: TestRestTasks_CreateRecurringRrule ----------------------------

func TestRestTasks_CreateRecurringRrule(t *testing.T) {
	t.Run("POST rrule creates 201 and round-trips a valid RRULE with no cron_expr", func(t *testing.T) {
		api := newTestRestAPIWithHomeAndAgent(t)
		wsID := ensureTestWorkspace(t, api)
		dtstart := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC).UnixMilli()

		tsk := createRecurringTaskViaAPI(
			t,
			api,
			wsID,
			"Biweekly",
			"FREQ=WEEKLY;INTERVAL=2;BYDAY=MO;COUNT=10",
			dtstart,
			"Europe/Berlin",
			"",
		)
		require.NotNil(t, tsk.Trigger)
		assert.EqualValues(t, "recurring", tsk.Trigger.Type)
		require.NotNil(t, tsk.Trigger.Config.Rrule)
		assert.Equal(t, "FREQ=WEEKLY;INTERVAL=2;BYDAY=MO;COUNT=10", *tsk.Trigger.Config.Rrule)
		assert.Nil(t, tsk.Trigger.Config.CronExpr)
		require.NotNil(t, tsk.Trigger.Config.DtstartMs)
		assert.Equal(t, dtstart, *tsk.Trigger.Config.DtstartMs)
		require.NotNil(t, tsk.Trigger.Config.Tz)
		assert.Equal(t, "Europe/Berlin", *tsk.Trigger.Config.Tz)

		// Round-trip via GET /tasks/{id}.
		wGet := httptest.NewRecorder()
		rGet := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+tsk.Id, nil)
		rGet.URL.Path = "/api/v1/tasks/" + tsk.Id
		api.HandleTasks(wGet, rGet)
		require.Equal(t, http.StatusOK, wGet.Code)
		var fetched gen.Task
		require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &fetched))
		require.NotNil(t, fetched.Trigger)
		require.NotNil(t, fetched.Trigger.Config.Rrule)
		assert.Equal(t, "FREQ=WEEKLY;INTERVAL=2;BYDAY=MO;COUNT=10", *fetched.Trigger.Config.Rrule)
	})

	t.Run("PATCH legacy cron_expr -> rrule updates to 200 and emits an FR-022 audit entry", func(t *testing.T) {
		api := newTestRestAPIWithHomeAndAgent(t)
		auditDir := t.TempDir()
		logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
		require.NoError(t, err)
		api.auditor = logger

		wsID := ensureTestWorkspace(t, api)
		setWorkspaceCoreTeam(t, api, wsID, []string{"mia"})
		body := fmt.Sprintf(
			`{"title":"Legacy","action":"llm","workspace_id":%q,"agent_id":"mia","trigger":{"type":"recurring","config":{"cron_expr":"0 9 * * MON"}}}`,
			wsID,
		)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/tasks"
		api.HandleTasks(w, r)
		require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
		var tsk gen.Task
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tsk))

		dtstart := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC).UnixMilli()
		patchBody := fmt.Sprintf(
			`{"trigger":{"type":"recurring","config":{"rrule":"FREQ=WEEKLY;INTERVAL=2;BYDAY=MO","dtstart_ms":%d,"tz":"Europe/Berlin"}}}`,
			dtstart,
		)
		wp := patchTask(t, api, tsk.Id, patchBody)
		require.Equal(t, http.StatusOK, wp.Code, "body=%s", wp.Body.String())
		var updated gen.Task
		require.NoError(t, json.Unmarshal(wp.Body.Bytes(), &updated))
		require.NotNil(t, updated.Trigger)
		assert.Nil(t, updated.Trigger.Config.CronExpr, "cron_expr must be gone after replacement")
		require.NotNil(t, updated.Trigger.Config.Rrule)

		require.NoError(t, logger.Close())
		found := filterAuditEvents(readAuditLog(t, auditDir), "task.trigger.recurrence_changed")
		require.Len(t, found, 1, "expected exactly one recurrence-change audit entry for the legacy->rrule replacement")
		details, ok := found[0]["details"].(map[string]any)
		require.True(t, ok, "audit entry must carry details")
		assert.Equal(t, tsk.Id, details["task_id"])
		prior, ok := details["prior_trigger"].(map[string]any)
		require.True(t, ok, "audit entry must carry the prior trigger")
		priorConfig, ok := prior["config"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "0 9 * * MON", priorConfig["cron_expr"])
		newT, ok := details["new_trigger"].(map[string]any)
		require.True(t, ok, "audit entry must carry the new trigger")
		newConfig, ok := newT["config"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "FREQ=WEEKLY;INTERVAL=2;BYDAY=MO", newConfig["rrule"])
	})

	t.Run("PATCH RRULE -> different RRULE audits; a title-only edit does not", func(t *testing.T) {
		api := newTestRestAPIWithHomeAndAgent(t)
		auditDir := t.TempDir()
		logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
		require.NoError(t, err)
		api.auditor = logger

		wsID := ensureTestWorkspace(t, api)
		dtstart := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC).UnixMilli()
		tsk := createRecurringTaskViaAPI(t, api, wsID, "Series", "FREQ=WEEKLY;BYDAY=MO", dtstart, "Europe/Berlin", "")

		// Title-only edit: resend the byte-identical trigger.
		titlePatch := fmt.Sprintf(
			`{"title":"Series (renamed)","trigger":{"type":"recurring","config":{"rrule":"FREQ=WEEKLY;BYDAY=MO","dtstart_ms":%d,"tz":"Europe/Berlin"}}}`,
			dtstart,
		)
		wTitle := patchTask(t, api, tsk.Id, titlePatch)
		require.Equal(t, http.StatusOK, wTitle.Code, "body=%s", wTitle.Body.String())

		// Rule change: different weekday.
		rulePatch := fmt.Sprintf(
			`{"trigger":{"type":"recurring","config":{"rrule":"FREQ=WEEKLY;BYDAY=TU","dtstart_ms":%d,"tz":"Europe/Berlin"}}}`,
			dtstart,
		)
		wRule := patchTask(t, api, tsk.Id, rulePatch)
		require.Equal(t, http.StatusOK, wRule.Code, "body=%s", wRule.Body.String())

		require.NoError(t, logger.Close())
		found := filterAuditEvents(readAuditLog(t, auditDir), "task.trigger.recurrence_changed")
		require.Len(t, found, 1,
			"the title-only edit must NOT audit (FR-024 byte-identical trigger); only the weekday rule change must")
	})

	t.Run("invalid rrule variants are rejected with 400 and a message", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		wsID := ensureTestWorkspace(t, api)
		dtstart := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC).UnixMilli()

		cases := []struct {
			name    string
			trigger string
		}{
			{
				"COUNT exceeds the 100,000 cap",
				fmt.Sprintf(
					`{"type":"recurring","config":{"rrule":"FREQ=DAILY;COUNT=100001","dtstart_ms":%d,"tz":"UTC"}}`,
					dtstart,
				),
			},
			{
				"both rrule and cron_expr present",
				fmt.Sprintf(
					`{"type":"recurring","config":{"rrule":"FREQ=DAILY","cron_expr":"0 9 * * MON","dtstart_ms":%d,"tz":"UTC"}}`,
					dtstart,
				),
			},
			{
				"FREQ=SECONDLY rejected outright",
				fmt.Sprintf(
					`{"type":"recurring","config":{"rrule":"FREQ=SECONDLY","dtstart_ms":%d,"tz":"UTC"}}`,
					dtstart,
				),
			},
			{
				"never-matching rule (Feb 31) rejected via the liveness bound",
				fmt.Sprintf(
					`{"type":"recurring","config":{"rrule":"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=31","dtstart_ms":%d,"tz":"UTC"}}`,
					dtstart,
				),
			},
			{
				"unloadable tz rejected",
				fmt.Sprintf(
					`{"type":"recurring","config":{"rrule":"FREQ=DAILY","dtstart_ms":%d,"tz":"Not/AZone"}}`,
					dtstart,
				),
			},
			{
				"neither rrule nor cron_expr present",
				`{"type":"recurring","config":{}}`,
			},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				body := fmt.Sprintf(`{"title":"Bad","action":"llm","workspace_id":%q,"trigger":%s}`, wsID, c.trigger)
				w := httptest.NewRecorder()
				r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				r.URL.Path = "/api/v1/tasks"
				api.HandleTasks(w, r)
				require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
				var errResp gen.ErrorResponse
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
				assert.NotEmpty(t, errResp.Error, "400 must carry a human-readable message")
			})
		}
	})
}

// filterAuditEvents narrows entries to those matching event.
func filterAuditEvents(entries []map[string]any, event string) []map[string]any {
	var out []map[string]any
	for _, e := range entries {
		if e["event"] == event {
			out = append(out, e)
		}
	}
	return out
}

// --- Test 13: TestRestTasks_OccurrencesEndpoint -----------------------------

func TestRestTasks_OccurrencesEndpoint(t *testing.T) {
	t.Run("200 bucketed shape for a dense recurring rule over an overview-mode range", func(t *testing.T) {
		api := newTestRestAPIWithHomeAndAgent(t)
		wsID := ensureTestWorkspace(t, api)
		dtstart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
		tsk := createRecurringTaskViaAPI(t, api, wsID, "Hourly", "FREQ=HOURLY", dtstart, "UTC", "")

		from := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 0, 10) // 10 days -> overview mode
		params := url.Values{
			"workspace_id": {wsID},
			"from_ms":      {strconv.FormatInt(from.UnixMilli(), 10)},
			"to_ms":        {strconv.FormatInt(to.UnixMilli(), 10)},
			"tz":           {"UTC"},
		}
		w := getOccurrences(t, api, params)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		var sets []gen.TaskOccurrenceSet
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sets))
		require.Len(t, sets, 1)
		assert.Equal(t, tsk.Id, sets[0].TaskId)
		assert.False(t, sets[0].Truncated)
		assert.Empty(t, sets[0].OccurrencesMs, "every day is dense (24/day) -> all bucketed, no raw instants")
		assert.Len(t, sets[0].DayBuckets, 10)
	})

	t.Run("done recurring task (non-exhausted) still returns future occurrences", func(t *testing.T) {
		// Regression coverage for "a recurring/every task fires once then
		// dies": a per-run `done` status must NOT hide a repeating series
		// from the calendar — the scheduler itself keeps arming this task's
		// next occurrence (pkg/agent/task_trigger.go OnTaskUpserted), so the
		// occurrences endpoint's selection predicate must keep rendering it
		// too. newTestRestAPIAlignedStores (not newTestRestAPIWithHome): the
		// recurring task carries agent_id="mia"
		// (store.validateScheduledAgentAssignment requires it), so
		// advanceTaskToDone's walk through in_progress goes through the real
		// StartTaskNow launch path (rest_tasks.go ~L1000), which 503s with a
		// nil taskExecutor — this harness wires one up (same precedent as
		// TestHandleTaskPatch_InProgress_WithKnownAgent).
		api := newTestRestAPIAlignedStores(t)
		wsID := ensureTestWorkspace(t, api)
		dtstart := time.Date(2020, 1, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
		tsk := createRecurringTaskViaAPI(t, api, wsID, "WillBeDoneButKeepsFiring", "FREQ=DAILY", dtstart, "UTC", "")
		advanceTaskToDone(t, api, tsk.Id)

		from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 0, 5)
		params := url.Values{
			"workspace_id": {wsID},
			"from_ms":      {strconv.FormatInt(from.UnixMilli(), 10)},
			"to_ms":        {strconv.FormatInt(to.UnixMilli(), 10)},
			"tz":           {"UTC"},
		}
		w := getOccurrences(t, api, params)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		var sets []gen.TaskOccurrenceSet
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sets))
		require.Len(t, sets, 1,
			"a done recurring task with a non-exhausted rule must still render its future occurrences")
		assert.Equal(t, tsk.Id, sets[0].TaskId)
		// A 5-day span is within the 8x24h detail-mode threshold (D6), so
		// instants land in OccurrencesMs (raw), not DayBuckets (overview-mode
		// only) — see the "the_occurrences_sub-path..." bucketed-shape
		// subtest above for the >8-day overview-mode/DayBuckets case.
		assert.NotEmpty(t, sets[0].OccurrencesMs, "FREQ=DAILY over 5 days (detail mode) must yield raw instants")
	})

	t.Run("exhausted recurring task (done, COUNT reached) returns none", func(t *testing.T) {
		api := newTestRestAPIAlignedStores(t)
		wsID := ensureTestWorkspace(t, api)
		dtstart := time.Date(2020, 1, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
		tsk := createRecurringTaskViaAPI(t, api, wsID, "ExhaustedAndDone", "FREQ=DAILY;COUNT=1", dtstart, "UTC", "")
		advanceTaskToDone(t, api, tsk.Id)

		from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 0, 5)
		params := url.Values{
			"workspace_id": {wsID},
			"from_ms":      {strconv.FormatInt(from.UnixMilli(), 10)},
			"to_ms":        {strconv.FormatInt(to.UnixMilli(), 10)},
			"tz":           {"UTC"},
		}
		w := getOccurrences(t, api, params)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		var sets []gen.TaskOccurrenceSet
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sets))
		assert.Empty(t, sets,
			"an exhausted (COUNT reached) recurring series has no future occurrences even though it "+
				"is now selection-eligible as a repeating trigger — buildOccurrenceSets omits it naturally")
	})

	t.Run("done every task still returns future occurrences (live NextRunAtMSForTask anchor)", func(t *testing.T) {
		// Companion to "done recurring task ... still returns future
		// occurrences" above, for the `every` flavor: unlike `recurring`,
		// `every`'s occurrence projection is STATE-DEPENDENT (FR-008a) — it
		// reads the live armed job's NextRunAtMS via
		// agent.TaskTriggerScheduler.NextRunAtMSForTask, which needs a real
		// scheduler wired (newTestRestAPIWithTaskTrigger, not
		// newTestRestAPIAlignedStores). This also doubles as an end-to-end
		// proof of FIX 1's idempotency guard: every PATCH along
		// next->in_progress->done calls NotifyTaskUpserted, and each one must
		// be a no-op (job stays armed, unchanged) rather than re-anchoring the
		// `every` job's NextRunAtMS to that PATCH's own wall-clock time.
		api, sched := newTestRestAPIWithTaskTrigger(t)
		wsID := ensureTestWorkspace(t, api)
		setWorkspaceCoreTeam(t, api, wsID, []string{"mia"})

		body := fmt.Sprintf(
			`{"title":"EveryDoneStillFires","action":"llm","workspace_id":%q,"agent_id":"mia",`+
				`"trigger":{"type":"every","config":{"every_ms":60000}}}`,
			wsID,
		)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/tasks"
		api.HandleTasks(w, r)
		require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
		var tsk gen.Task
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tsk))

		armedBefore, ok := sched.NextRunAtMSForTask(tsk.Id)
		require.True(t, ok, "an every task must be armed immediately after creation")

		advanceTaskToDone(t, api, tsk.Id)

		armedAfter, ok := sched.NextRunAtMSForTask(tsk.Id)
		require.True(t, ok, "an every task must remain armed after reaching done (repeating series survives)")
		assert.Equal(t, armedBefore, armedAfter,
			"OnTaskUpserted must not re-anchor an already-armed every job on unrelated status PATCHes (FIX 1)")

		from := time.UnixMilli(armedAfter).Add(-time.Minute)
		to := time.UnixMilli(armedAfter).Add(5 * time.Minute)
		params := url.Values{
			"workspace_id": {wsID},
			"from_ms":      {strconv.FormatInt(from.UnixMilli(), 10)},
			"to_ms":        {strconv.FormatInt(to.UnixMilli(), 10)},
			"tz":           {"UTC"},
		}
		wOcc := getOccurrences(t, api, params)
		require.Equal(t, http.StatusOK, wOcc.Code, "body=%s", wOcc.Body.String())
		var sets []gen.TaskOccurrenceSet
		require.NoError(t, json.Unmarshal(wOcc.Body.Bytes(), &sets))
		require.Len(t, sets, 1,
			"a done every task with a live armed job must still render its future occurrences")
		assert.Equal(t, tsk.Id, sets[0].TaskId)
		assert.NotEmpty(t, sets[0].OccurrencesMs)
	})

	t.Run("done once task still omitted (non-repeating trigger)", func(t *testing.T) {
		api := newTestRestAPIAlignedStores(t)
		wsID := ensureTestWorkspace(t, api)
		setWorkspaceCoreTeam(t, api, wsID, []string{"mia"})
		atMs := time.Date(2020, 1, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
		body := fmt.Sprintf(
			`{"title":"OnceDone","action":"llm","workspace_id":%q,"agent_id":"mia","trigger":{"type":"once","config":{"at_ms":%d}}}`,
			wsID,
			atMs,
		)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/tasks"
		api.HandleTasks(w, r)
		require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
		var tsk gen.Task
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tsk))
		advanceTaskToDone(t, api, tsk.Id)

		from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 0, 5)
		params := url.Values{
			"workspace_id": {wsID},
			"from_ms":      {strconv.FormatInt(from.UnixMilli(), 10)},
			"to_ms":        {strconv.FormatInt(to.UnixMilli(), 10)},
			"tz":           {"UTC"},
		}
		wOcc := getOccurrences(t, api, params)
		require.Equal(t, http.StatusOK, wOcc.Code, "body=%s", wOcc.Body.String())
		var sets []gen.TaskOccurrenceSet
		require.NoError(t, json.Unmarshal(wOcc.Body.Bytes(), &sets))
		assert.Empty(t, sets,
			"a done `once` task must stay omitted — its single occurrence IS its whole series, "+
				"unlike a recurring/every trigger")
	})

	t.Run(
		"heartbeat-surface task omitted (selection predicate); a user-surface sibling still renders",
		func(t *testing.T) {
			api := newTestRestAPIWithHomeAndAgent(t)
			wsID := ensureTestWorkspace(t, api)
			dtstart := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
			hb := createRecurringTaskViaAPI(t, api, wsID, "Heartbeat", "FREQ=DAILY", dtstart, "UTC", "heartbeat")
			visible := createRecurringTaskViaAPI(t, api, wsID, "Visible", "FREQ=DAILY", dtstart, "UTC", "")

			from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			to := from.AddDate(0, 0, 5)
			params := url.Values{
				"workspace_id": {wsID},
				"from_ms":      {strconv.FormatInt(from.UnixMilli(), 10)},
				"to_ms":        {strconv.FormatInt(to.UnixMilli(), 10)},
				"tz":           {"UTC"},
			}
			w := getOccurrences(t, api, params)
			require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
			var sets []gen.TaskOccurrenceSet
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sets))
			ids := make([]string, 0, len(sets))
			for _, s := range sets {
				ids = append(ids, s.TaskId)
			}
			assert.NotContains(
				t,
				ids,
				hb.Id,
				"surface:heartbeat task must never render occurrences (heartbeat service owns its fires)",
			)
			assert.Contains(t, ids, visible.Id, "a plain user-surface sibling with the same rule must still render")
		},
	)

	t.Run("zero-occurrence task omitted; empty result is [] never null", func(t *testing.T) {
		api := newTestRestAPIWithHomeAndAgent(t)
		wsID := ensureTestWorkspace(t, api)
		dtstart := time.Date(2020, 1, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
		createRecurringTaskViaAPI(t, api, wsID, "Exhausted", "FREQ=DAILY;COUNT=1", dtstart, "UTC", "")

		from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 0, 5)
		params := url.Values{
			"workspace_id": {wsID},
			"from_ms":      {strconv.FormatInt(from.UnixMilli(), 10)},
			"to_ms":        {strconv.FormatInt(to.UnixMilli(), 10)},
			"tz":           {"UTC"},
		}
		w := getOccurrences(t, api, params)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		assert.Equal(
			t,
			"[]",
			strings.TrimSpace(w.Body.String()),
			"an empty result must serialize as [] on the wire, never null",
		)
	})

	t.Run("400 on invalid range/span/tz", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		wsID := ensureTestWorkspace(t, api)
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()

		cases := []struct {
			name   string
			params url.Values
		}{
			{
				"missing workspace_id",
				url.Values{"from_ms": {"0"}, "to_ms": {"1000"}, "tz": {"UTC"}},
			},
			{
				"missing from_ms",
				url.Values{"workspace_id": {wsID}, "to_ms": {"1000"}, "tz": {"UTC"}},
			},
			{
				"missing to_ms",
				url.Values{"workspace_id": {wsID}, "from_ms": {"0"}, "tz": {"UTC"}},
			},
			{
				"missing tz",
				url.Values{"workspace_id": {wsID}, "from_ms": {"0"}, "to_ms": {"1000"}},
			},
			{
				"from_ms == to_ms (empty half-open range)",
				url.Values{
					"workspace_id": {wsID},
					"from_ms":      {strconv.FormatInt(base, 10)},
					"to_ms":        {strconv.FormatInt(base, 10)},
					"tz":           {"UTC"},
				},
			},
			{
				"from_ms > to_ms",
				url.Values{
					"workspace_id": {wsID},
					"from_ms":      {strconv.FormatInt(base+1000, 10)},
					"to_ms":        {strconv.FormatInt(base, 10)},
					"tz":           {"UTC"},
				},
			},
			{
				"span exceeds 400 days",
				url.Values{
					"workspace_id": {wsID},
					"from_ms":      {strconv.FormatInt(base, 10)},
					"to_ms":        {strconv.FormatInt(base+maxOccurrenceRangeSpanMs+1, 10)},
					"tz":           {"UTC"},
				},
			},
			{
				"unloadable tz",
				url.Values{
					"workspace_id": {wsID},
					"from_ms":      {strconv.FormatInt(base, 10)},
					"to_ms":        {strconv.FormatInt(base+1000, 10)},
					"tz":           {"Not/AZone"},
				},
			},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				w := getOccurrences(t, api, c.params)
				require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
				var errResp gen.ErrorResponse
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
				assert.NotEmpty(t, errResp.Error, "400 must carry a human-readable message")
			})
		}
	})

	t.Run("the occurrences sub-path wins over ID parsing on the real registered mux", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		mux := http.NewServeMux()
		api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})

		wsID := ensureTestWorkspace(t, api)
		bypassCfg := &config.Config{}
		bypassCfg.Gateway.DevModeBypass = true

		q := url.Values{
			"workspace_id": {wsID},
			"from_ms":      {"0"},
			"to_ms":        {"1000"},
			"tz":           {"UTC"},
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/occurrences?"+q.Encode(), nil)
		req.Header.Set("Authorization", "Bearer dev-mode-bypass-sentinel")
		req.RemoteAddr = "203.0.113.201:1111" // dedicated IP, isolates taskReadLimiter's window
		req = req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, bypassCfg))

		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code,
			"GET /api/v1/tasks/occurrences must reach HandleTaskOccurrences (200 []), NOT HandleTasks' "+
				"ID-parsing branch (which would 400/404 treating \"occurrences\" as a task id); body=%s", w.Body.String())
		assert.Equal(t, "[]", strings.TrimSpace(w.Body.String()))
	})

	t.Run("taskReadLimiter returns 429 past the 240/min ceiling", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		mux := http.NewServeMux()
		api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})

		wsID := ensureTestWorkspace(t, api)
		bypassCfg := &config.Config{}
		bypassCfg.Gateway.DevModeBypass = true

		const ip = "203.0.113.202:2222" // dedicated IP so the window is not shared
		q := url.Values{
			"workspace_id": {wsID},
			"from_ms":      {"0"},
			"to_ms":        {"1000"},
			"tz":           {"UTC"},
		}
		newReq := func() *http.Request {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/occurrences?"+q.Encode(), nil)
			req.Header.Set("Authorization", "Bearer dev-mode-bypass-sentinel")
			req.RemoteAddr = ip
			return req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, bypassCfg))
		}
		fire := func() int {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, newReq())
			return w.Code
		}
		for i := 0; i < 240; i++ {
			code := fire()
			require.Equalf(t, http.StatusOK, code, "request %d of 240 must pass the limiter", i+1)
		}
		lw := httptest.NewRecorder()
		mux.ServeHTTP(lw, newReq())
		assert.Equal(t, http.StatusTooManyRequests, lw.Code, "the 241st request must be rate-limited")
		assert.NotEmpty(t, lw.Header().Get("Retry-After"), "429 must carry Retry-After")
	})
}
