package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/rooms"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// NEW-SF-2: the existing updateWorkspace 500 contract, not an invented
// parallel error type. Revision/fields are compared to the actual saved state.
func TestRoomsFix4_SavedRoomSyncFailureUsesMutationEnvelope(t *testing.T) {
	h := wu15NewHarness(t)
	j := &fix4RoomJournal{Journal: rooms.NewFileJournal(rooms.DefaultDir(h.home))}
	fix4InstallJournal(t, h, j)
	room := h.create("squad", "Launch", "mia", "jim")
	status, body := h.http(http.MethodGet, "/api/v1/workspaces/sales", nil)
	require.Equal(t, http.StatusOK, status)
	var before gen.Workspace
	require.NoError(t, json.Unmarshal(body, &before))
	team := []string{"mia", "researcher2", "gp"}
	j.readFault.Store(true)
	status, body = h.http(http.MethodPut, "/api/v1/workspaces/sales", gen.WorkspaceUpdateRequest{Revision: before.Revision, CoreTeam: &team})
	require.Equal(t, http.StatusInternalServerError, status, "room-sync fault after saved membership: %s", body)
	require.Positive(t, j.hits.Load(), "instrument: real room synchronization read failed")
	var failure gen.ConfigurationMutationState
	require.NoError(t, json.Unmarshal(body, &failure))
	requireRoomContract(t, "ConfigurationMutationState", json.RawMessage(body))
	require.Equal(t, gen.ConfigurationMutationStatePersistenceStatusPartial, failure.PersistenceStatus)
	require.Equal(t, gen.ConfigurationMutationStateActivationStatusNotAttempted, failure.ActivationStatus)
	require.Equal(t, []string{"core_team"}, failure.ChangedFields)
	require.NotNil(t, failure.ErrorStage)
	require.Equal(t, "room_sync", *failure.ErrorStage)
	status, savedBody := h.http(http.MethodGet, "/api/v1/workspaces/sales", nil)
	require.Equal(t, http.StatusOK, status)
	var saved gen.Workspace
	require.NoError(t, json.Unmarshal(savedBody, &saved))
	require.NotNil(t, saved.CoreTeam)
	require.Equal(t, team, *saved.CoreTeam)
	require.NotEqual(t, before.Revision, saved.Revision)
	require.Equal(t, saved.Revision, failure.Revision, "reported revision must be the actual persisted workspace/graph, not the attempted state")
	j.readFault.Store(false)
	require.Equal(t, []string{"mia"}, h.room(room.Id).Members)
	// Healthy current-revision no-op remains usable after reading the partial save.
	status, body = h.http(http.MethodPut, "/api/v1/workspaces/sales", gen.WorkspaceUpdateRequest{Revision: saved.Revision, CoreTeam: &team})
	require.Equal(t, http.StatusOK, status, "healthy control: %s", body)
}

// New N2: only core_team changes; no independent delegation write and no
// member_configs-present retry is used to manufacture the no-op condition.
func TestRoomsFix4_PartialRoomSyncStillReleasesHeartbeat(t *testing.T) {
	api, cs := buildHeartbeatTestAPI(t)
	const wsID = "room-heartbeat-partial"
	hbWriteWorkspaceRecord(t, api, workspace.Workspace{ID: wsID, Name: "Team", Status: "active", CoreTeam: []string{"mia", "jim"}, MemberConfigs: map[string]workspace.MemberConfig{
		"jim": {Heartbeat: &workspace.MemberHeartbeat{Enabled: true, IntervalMinutes: 10, Body: "Check tasks."}},
	}, CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z"})
	// Jim remains eligible elsewhere; a stale job must not be masked by an
	// unrelated global removal or by the heartbeat runner's owner lookup.
	hbWriteWorkspaceRecord(t, api, workspace.Workspace{ID: "other-heartbeat-team", Name: "Other", Status: "active", CoreTeam: []string{"jim"}, CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z"})
	api.reconcileHeartbeatSchedules()
	require.Len(t, heartbeatJobsFor(cs), 1, "instrument: Jim's heartbeat really exists")
	before, err := workspace.ReadState(api.homePath, wsID)
	require.NoError(t, err)
	require.Empty(t, before.Delegation, "fixture: no independent graph change")
	h := newWSHandler(bus.NewMessageBus(), api.agentLoop, "http://localhost:3000")
	h.home = api.homePath
	hub := newRoomsHub(h, api.homePath)
	h.rooms = hub
	api.rooms.Store(hub)
	j := &fix4RoomJournal{Journal: rooms.NewFileJournal(rooms.DefaultDir(api.homePath))}
	hub.svc.Close()
	hub.svc = rooms.New(j, rooms.DepsFrom(&gatewayRoomDeps{h: h, home: api.homePath, hub: hub}))
	t.Cleanup(hub.svc.Close)
	_, err = hub.svc.List(wsID)
	require.NoError(t, err)
	j.readFault.Store(true)
	response := putWorkspaceBody(t, api, wsID, `{"core_team":["mia"]}`)
	require.Equal(t, http.StatusInternalServerError, response.Code, "partial save: %s", response.Body.String())
	require.Positive(t, j.hits.Load(), "instrument: room sync failed after the workspace commit")
	saved := readWorkspaceFromDisk(t, api, wsID)
	require.Equal(t, []string{"mia"}, saved.CoreTeam)
	_, hasJim := saved.MemberConfigs["jim"]
	require.False(t, hasJim)
	require.Empty(t, heartbeatJobsFor(cs), "saved membership removal must clean up the actual scheduler even when rooms fail")
	j.readFault.Store(false)
	response = putWorkspaceBody(t, api, wsID, `{"core_team":["mia"]}`)
	require.Equal(t, http.StatusOK, response.Code, "current-revision unchanged-team retry is healthy: %s", response.Body.String())
	require.Empty(t, heartbeatJobsFor(cs), "a successful no-op retry must not leave a removed member scheduled")
	// Normal user schedules are independent and remain intact.
	every := int64(60_000)
	userJob, err := cs.AddJobFull(cron.JobSpec{Name: "independent-user-job", Schedule: cron.CronSchedule{Kind: "every", EveryMS: &every}, Message: "User task", AgentID: "mia"})
	require.NoError(t, err)
	require.NotNil(t, userJob)
	response = putWorkspaceBody(t, api, wsID, `{"core_team":["mia"]}`)
	require.Equal(t, http.StatusOK, response.Code)
	jobs := cs.ListJobs(true)
	require.Len(t, jobs, 1)
	require.Equal(t, userJob.ID, jobs[0].ID)
}
