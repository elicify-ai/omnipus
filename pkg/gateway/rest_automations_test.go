//go:build !cgo

// Tests for GET /api/v1/automations — the trigger→action display projection
// over schedules (W3-AC UI reframe).

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/cron"
)

// automationsResp decodes the not-wire-format automations response body.
type automationsResp struct {
	Automations []struct {
		Schedule       map[string]any `json:"schedule"`
		TriggerDisplay string         `json:"trigger_display"`
		ActionDisplay  string         `json:"action_display"`
		AgentName      string         `json:"agent_name"`
		NextRunDisplay string         `json:"next_run_display"`
	} `json:"automations"`
}

func TestAutomations_CronTrigger_HumanizedAndActionShown(t *testing.T) {
	api, cs := newSchedulesTestAPI(t)
	job, err := cs.AddJob(
		"weekly",
		cron.CronSchedule{Kind: "cron", Expr: "0 9 * * 1"}, // every Monday 09:00
		"summarize the week",
		false, // deliver=false → run agent
		"",
		"",
	)
	require.NoError(t, err)
	job.AgentID = "mia"
	require.NoError(t, cs.UpdateJob(job))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/automations", nil)
	r = withUser(r, "alice", config.UserRoleUser)
	w := httptest.NewRecorder()
	api.HandleAutomations(w, r)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp automationsResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Automations, 1)
	item := resp.Automations[0]
	assert.Equal(t, "Monday at 09:00", item.TriggerDisplay, "weekly cron must be humanized")
	assert.Equal(t, "Run agent: mia", item.ActionDisplay, "deliver=false runs the agent")
	assert.Equal(t, "mia", item.AgentName)
}

func TestAutomations_EveryTrigger_DeliverAction(t *testing.T) {
	api, cs := newSchedulesTestAPI(t)
	job, err := cs.AddJob(
		"frequent",
		cron.CronSchedule{Kind: "every", EveryMS: i64p(5 * 60 * 1000)}, // 5m
		"ping",
		true, // deliver=true → send to channel
		"telegram",
		"c1",
	)
	require.NoError(t, err)
	job.AgentID = "mia"
	require.NoError(t, cs.UpdateJob(job))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/automations", nil)
	r = withUser(r, "alice", config.UserRoleUser)
	w := httptest.NewRecorder()
	api.HandleAutomations(w, r)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp automationsResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Automations, 1)
	item := resp.Automations[0]
	assert.Equal(t, "Every 5m", item.TriggerDisplay)
	assert.Equal(t, "Send to telegram", item.ActionDisplay, "deliver=true sends to the channel")
}

func TestAutomations_AtTrigger_OneShot(t *testing.T) {
	api, cs := newSchedulesTestAPI(t)
	atMS := int64(1735689600000) // 2025-01-01 00:00 UTC
	job, err := cs.AddJob(
		"once",
		cron.CronSchedule{Kind: "at", AtMS: &atMS},
		"go",
		false,
		"",
		"",
	)
	require.NoError(t, err)
	job.AgentID = "mia"
	require.NoError(t, cs.UpdateJob(job))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/automations", nil)
	r = withUser(r, "alice", config.UserRoleUser)
	w := httptest.NewRecorder()
	api.HandleAutomations(w, r)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp automationsResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Automations, 1)
	assert.Contains(t, resp.Automations[0].TriggerDisplay, "Once at")
}

func TestAutomations_OwnerFilter_NonAdminSeesOnlyOwnAgents(t *testing.T) {
	api, cs := newSchedulesTestAPI(t)
	j1, err := cs.AddJob("a", cron.CronSchedule{Kind: "every", EveryMS: i64p(60000)}, "x", false, "", "")
	require.NoError(t, err)
	j1.AgentID = "mia" // alice owns mia
	require.NoError(t, cs.UpdateJob(j1))

	j2, err := cs.AddJob("b", cron.CronSchedule{Kind: "every", EveryMS: i64p(60000)}, "y", false, "", "")
	require.NoError(t, err)
	j2.AgentID = "max" // bob owns max
	require.NoError(t, cs.UpdateJob(j2))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/automations", nil)
	r = withUser(r, "alice", config.UserRoleUser) // alice, non-admin
	w := httptest.NewRecorder()
	api.HandleAutomations(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp automationsResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Automations, 1, "alice must only see her own agent's schedule")
	assert.Equal(t, "mia", resp.Automations[0].AgentName)
}

// TestAutomations_RoleAloneDoesNotBypassOwnership asserts a role of "admin"
// grants no extra visibility here either — same reasoning as
// TestSchedulesAPI_List_FilteredByOwner (this is the same authorization gate,
// applied to the /automations view of the schedule list). "admin" owns
// neither "mia" nor "max" in this fixture, so it sees zero, same as any other
// non-owning account.
func TestAutomations_RoleAloneDoesNotBypassOwnership(t *testing.T) {
	api, cs := newSchedulesTestAPI(t)
	j1, err := cs.AddJob("a", cron.CronSchedule{Kind: "every", EveryMS: i64p(60000)}, "x", false, "", "")
	require.NoError(t, err)
	j1.AgentID = "mia"
	require.NoError(t, cs.UpdateJob(j1))

	j2, err := cs.AddJob("b", cron.CronSchedule{Kind: "every", EveryMS: i64p(60000)}, "y", false, "", "")
	require.NoError(t, err)
	j2.AgentID = "max"
	require.NoError(t, cs.UpdateJob(j2))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/automations", nil)
	r = withUser(r, "admin", config.UserRoleAdmin)
	w := httptest.NewRecorder()
	api.HandleAutomations(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp automationsResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.Automations, "role alone must not grant visibility into another user's schedules")
}

func TestAutomations_MethodNotAllowed(t *testing.T) {
	api, _ := newSchedulesTestAPI(t)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/automations", nil)
	r = withUser(r, "admin", config.UserRoleAdmin)
	w := httptest.NewRecorder()
	api.HandleAutomations(w, r)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// Unit tests for the humanisers (no HTTP layer).

func TestHumanizeCron(t *testing.T) {
	cases := []struct{ expr, want string }{
		{"* * * * *", "Every minute"},
		{"*/5 * * * *", "Every 5 minutes"},
		{"0 */2 * * *", "Every 2 hours"},
		{"0 9 * * 1", "Monday at 09:00"},
		{"0 18 * * *", "Daily at 18:00"},
		{"0 0 15 * *", "Monthly on day 15 at 00:00"},
		{"30 8 * * 5", "Friday at 08:30"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, humanizeCron(c.expr, ""), "expr=%s", c.expr)
	}
}

func TestHumanizeDuration(t *testing.T) {
	assert.Equal(t, "30s", humanizeDuration(30*1000))
	assert.Equal(t, "5m", humanizeDuration(5*60*1000))
	assert.Equal(t, "2h", humanizeDuration(2*60*60*1000))
	assert.Equal(t, "3d", humanizeDuration(3*24*60*60*1000))
}
