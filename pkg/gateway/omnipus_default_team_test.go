// Omnipus — default-workspace team surface tests (omnipus-system-agent)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// omnipus_default_team_test.go — pins the team surfaces after Ava and Admin
// were retired (omnipus-system-agent FR-001/FR-008) and Omnipus became an
// ordinary hidden member of the DEFAULT workspace only (FR-003):
//
//   - the BOOT default team seed (defaultWorkspaceTeam / ensureDefaultWorkspace)
//     carries the six ordinary roles, Omnipus included, and never the hidden
//     engine roles or the retired ids;
//   - REST team writes refuse Omnipus outside the default workspace BEFORE
//     any write lands (FR-003, §6.2: 422);
//   - the delegation PUT refuses an edge whose endpoint is not on the team;
//   - the boot workspaceless-agent diagnostic does not flag Omnipus once the
//     default workspace exists, and still flags an ordinary orphan.
//
// The expected rosters below are transcribed from the spec, NOT derived from
// coreagent.All(), so a regression in the roster itself fails here.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// omnipusDefaultTeamSixRoles is the expected default-team set: the three chat
// colleagues (Omnipus hidden in the UI but an ordinary member, FR-003/FR-004)
// plus the three staff roles. The two hidden engine roles are absent by spec.
var omnipusDefaultTeamSixRoles = []string{"mia", "jim", "omnipus", "planner", "researcher", "worker"}

// omnipusFullRosterConfig builds a config carrying the complete fresh-install
// roster: 3 core chat targets (incl. Omnipus), 3 staff workers, 2 hidden
// system agents.
func omnipusFullRosterConfig() *config.Config {
	return &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCore},
				{ID: "jim", Name: "Jim", Type: config.AgentTypeCore},
				{ID: "omnipus", Name: "Omnipus", Type: config.AgentTypeCore, Locked: true},
				{ID: "planner", Name: "Planner", Type: config.AgentTypeWorker},
				{ID: "researcher", Name: "Researcher", Type: config.AgentTypeWorker},
				{ID: "worker", Name: "General Purpose", Type: config.AgentTypeWorker},
				{ID: "judge", Name: "Judge", Type: config.AgentTypeSystem, Locked: true},
				{ID: "plansupervisor", Name: "Plan Supervisor", Type: config.AgentTypeSystem, Locked: true},
			},
		},
	}
}

// TestDefaultWorkspaceTeam_IncludesOmnipusKeepsSixOrdinaryRoles is the
// seed-side spec oracle (FR-003).
func TestDefaultWorkspaceTeam_IncludesOmnipusKeepsSixOrdinaryRoles(t *testing.T) {
	team := defaultWorkspaceTeam(omnipusFullRosterConfig())
	assert.ElementsMatch(t, omnipusDefaultTeamSixRoles, team,
		"the boot default team must be exactly the six ordinary roles, Omnipus included")
	for _, absent := range []string{"judge", "plansupervisor", "ava", "admin"} {
		assert.NotContains(t, team, absent, "%s must never seed onto the default team", absent)
	}
}

// TestDefaultWorkspaceSeeder_BootsWithOmnipusMembership drives the FULL boot
// pipeline and asserts both persisted artifacts: the default team holds the
// six ordinary roles, and no seeded edge names a retired id.
func TestDefaultWorkspaceSeeder_BootsWithOmnipusMembership(t *testing.T) {
	home := t.TempDir()
	cfg := omnipusFullRosterConfig()
	require.NoError(t, ensureDefaultWorkspace(home, "alice", cfg))

	wss, err := listWorkspaceFiles(home)
	require.NoError(t, err)
	require.Len(t, wss, 1, "boot must seed exactly one default workspace")
	ws := wss[0]
	assert.ElementsMatch(t, omnipusDefaultTeamSixRoles, ws.CoreTeam,
		"the persisted default workspace team must be the six ordinary roles")

	seeded := loadStoredDelegationEdges(t, home, ws.ID)
	require.NotEmpty(t, seeded, "sanity: the ordinary roster's seed edges must still exist")
	for _, e := range seeded {
		for _, retired := range []string{"ava", "admin"} {
			assert.NotEqual(t, retired, e.FromAgent, "no seeded delegation edge may originate from retired %s", retired)
			assert.NotEqual(t, retired, e.ToAgent, "no seeded delegation edge may target retired %s", retired)
		}
	}
}

// TestValidateCoreTeamMembers_RejectsUnregisteredAndHiddenRoles is the unit
// oracle for the write-path validator: a retired id is no longer registered
// and is rejected by name, hidden system ids stay rejected, and an ordinary
// clean team still passes.
func TestValidateCoreTeamMembers_RejectsUnregisteredAndHiddenRoles(t *testing.T) {
	cfg := omnipusFullRosterConfig()

	err := validateCoreTeamMembers(cfg, []string{"mia", "admin", "jim"})
	require.Error(t, err, "a retired, unregistered id must be rejected on write")
	assert.Contains(t, err.Error(), "admin", "the rejection must name the offending id")

	err = validateCoreTeamMembers(cfg, []string{"mia", "judge"})
	require.Error(t, err, "hidden system agents stay rejected (existing behavior must not regress)")

	assert.NoError(t, validateCoreTeamMembers(cfg, []string{"mia", "jim", "omnipus", "worker"}),
		"an ordinary team must still validate cleanly")
}

// newOmnipusTeamAPI builds a lean restAPI whose live config carries the full
// roster (see omnipusFullRosterConfig) so the real HTTP handlers can be
// exercised against an Omnipus-membership write.
func newOmnipusTeamAPI(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)
	cfg := omnipusFullRosterConfig()
	// The agent loop derives its task store from the agent home base; left
	// empty it resolved "./tasks" and wrote task-migration sentinels into the
	// package directory (TestWorkspacePUT_RejectsOmnipusOutsideDefaultBeforeWrites).
	cfg.Agents.Defaults.Home = tmpDir
	cfgJSON, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), cfgJSON, 0o600))

	store := agentstore.New(tmpDir)
	for i := range cfg.Agents.List {
		ac := cfg.Agents.List[i]
		require.NoError(t, store.Create(ac.ID, &ac), "seed agentstore entity record for %q", ac.ID)
	}

	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		homePath:      tmpDir,
		taskStore:     task.New(filepath.Join(tmpDir, "tasks")),
		taskLock:      task.TaskFileLock,
	}
}

// seedTeamWorkspaceOnDisk persists a minimal NON-default workspace record
// with the given team.
func seedTeamWorkspaceOnDisk(t *testing.T, api *restAPI, id string, team []string) {
	t.Helper()
	wsDir := filepath.Join(api.homePath, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o700))
	ws := workspace.Workspace{
		ID:        id,
		Name:      "Team WS",
		Status:    "active",
		CoreTeam:  team,
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}
	wsData, err := json.MarshalIndent(ws, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, id+".json"), wsData, 0o600))
}

// TestWorkspacePUT_RejectsOmnipusOutsideDefaultBeforeWrites proves the REST
// write path refuses to put Omnipus on a non-default team (FR-003, §6.2 422)
// and that the refusal is ZERO-WRITE.
func TestWorkspacePUT_RejectsOmnipusOutsideDefaultBeforeWrites(t *testing.T) {
	api := newOmnipusTeamAPI(t)
	const wsID = "01JXOMNIPUSPUTTEAM000001"
	seedTeamWorkspaceOnDisk(t, api, wsID, []string{"mia", "jim"})

	wsPath := filepath.Join(api.homePath, "workspaces", wsID+".json")
	before, err := os.ReadFile(wsPath)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+wsID,
		strings.NewReader(withWorkspaceRevisionJSON(t, api, wsID, `{"core_team":["mia","jim","omnipus"]}`)))
	r.Header.Set("Content-Type", "application/json")
	api.HandleWorkspaces(w, r)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"putting Omnipus on a non-default team must 422; body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "default workspace")

	after, err := os.ReadFile(wsPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after),
		"a refused team write must be zero-write — the workspace record must be unchanged")
}

// TestWorkspacePOST_RejectsOmnipusInInitialTeam covers the create path: a
// created workspace is never the default one, so an initial team with
// Omnipus is 422 and leaves no record behind.
func TestWorkspacePOST_RejectsOmnipusInInitialTeam(t *testing.T) {
	api := newOmnipusTeamAPI(t)
	before := snapshotOutsideToolState(t, filepath.Join(api.homePath, "workspaces"))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
		strings.NewReader(`{"name":"Omnipus Team WS","core_team":["mia","omnipus"]}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleWorkspaces(w, r)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"creating a workspace with Omnipus on the initial team must 422; body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "default workspace")

	after := snapshotOutsideToolState(t, filepath.Join(api.homePath, "workspaces"))
	assert.Equal(t, before, after, "a refused create must leave existing workspace storage unchanged")
}

// TestWorkspaceDelegationPUT_RejectsEndpointOffTeam proves no delegation edge
// with an endpoint off the team can be written: on a non-default team without
// Omnipus, an edge to Omnipus fails endpoint validation.
func TestWorkspaceDelegationPUT_RejectsEndpointOffTeam(t *testing.T) {
	api := newOmnipusTeamAPI(t)
	const wsID = "01JXOMNIPUSDELEGTEAM0001"
	seedTeamWorkspaceOnDisk(t, api, wsID, []string{"jim", "mia"})

	body := `{"edges":[{"from_agent":"jim","to_agent":"omnipus","modes":["direct"]}]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+wsID+"/delegation",
		strings.NewReader(withWorkspaceRevisionJSON(t, api, wsID, body)))
	r.Header.Set("Content-Type", "application/json")
	api.handleWorkspaceDelegationPut(w, r, wsID)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"an edge targeting an off-team agent must 400 (endpoint not on team); body=%s", w.Body.String())

	storePath := filepath.Join(api.homePath, "entities", "delegation", wsID+".json")
	_, statErr := os.Stat(storePath)
	assert.True(t, errors.Is(statErr, os.ErrNotExist),
		"a refused delegation write must not create a delegation store record")
}

// capturingSlogHandler collects every record slog routes to the default
// logger, so logWorkspacelessAgents' boot diagnostic can be asserted without
// scraping stderr.
type capturingSlogHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *capturingSlogHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *capturingSlogHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *capturingSlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h // test-only: attribute cloning is not needed for the assertion below
}

func (h *capturingSlogHandler) WithGroup(name string) slog.Handler { return h }

// workspacelessIDs returns the joined agent_ids string of every captured
// WARN emitted by logWorkspacelessAgents, or "" when none fired.
func (h *capturingSlogHandler) workspacelessIDs() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var joined string
	for _, r := range h.records {
		if r.Level != slog.LevelWarn {
			continue
		}
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "agent_ids" {
				joined = a.Value.String()
			}
			return true
		})
	}
	return joined
}

// TestLogWorkspacelessAgents_OmnipusOnDefaultTeamNotFlagged pins the boot
// diagnostic: once the default workspace exists, Omnipus is one of its
// members and must not be flagged; an ordinary unassigned agent still is.
func TestLogWorkspacelessAgents_OmnipusOnDefaultTeamNotFlagged(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	coreagent.SeedConfig(cfg)
	require.NoError(t, ensureDefaultWorkspace(home, "alice", cfg))
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{ID: "orphan-bot", Type: config.AgentTypeCustom})

	capture := &capturingSlogHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(prev) })

	logWorkspacelessAgents(home, cfg)

	ids := capture.workspacelessIDs()
	require.NotEmpty(t, ids, "sanity: the ordinary workspaceless agent must still be flagged")
	assert.Contains(t, ids, "orphan-bot")
	assert.NotContains(t, ids, "omnipus",
		"Omnipus is a member of the default workspace — the boot diagnostic must not flag it")
}

// TestWorkspaceGET_TeamWire_FollowsCoreTeam is a small contract guard:
// workspaceDelegationToWire derives team[] from the workspace's own
// core_team ∪ edge endpoints.
func TestWorkspaceGET_TeamWire_FollowsCoreTeam(t *testing.T) {
	ws := storedWorkspace{ID: "ws-wire", CoreTeam: []string{"mia", "jim"}}
	wire := workspaceDelegationToWire(ws, nil, 3, strings.Repeat("0", 64))
	require.NotNil(t, wire.Team)
	assert.ElementsMatch(t, []string{"mia", "jim"}, *wire.Team)
}
