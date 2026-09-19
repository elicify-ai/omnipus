// Omnipus — ADR-090 Admin standalone operator: workspace-team surface tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// adr090_admin_standalone_team_test.go — pins the ADR-090 Admin gap on every
// gateway workspace-team surface:
//
//   - the BOOT default team seed (defaultWorkspaceTeam /
//     ensureDefaultWorkspace) must exclude Admin while retaining the six
//     ordinary team roles (FR-001: "admin … Yes / no team membership");
//   - REST team writes (POST/PUT core_team) must reject an Admin membership
//     BEFORE any write lands (FR-006: "Admin and hidden agents cannot be
//     added as ordinary teammates to bypass role boundaries");
//   - the delegation PUT must refuse edges with an Admin endpoint (§3: the
//     seeded conversation graph has no Admin edges — Admin neither delegates
//     nor is delegated);
//   - the boot workspaceless-agent diagnostic must not flag Admin (it is
//     deliberately teamless — a WARN would be a false alarm every boot).
//
// The expected rosters below are transcribed from the ADR-090 / FR-001
// identity table, NOT derived from coreagent.All(), so a regression in the
// roster itself (e.g. re-adding a retired role or dropping a team role)
// fails here rather than round-tripping silently.

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

// adr090RosterSixOrdinaryTeamRoles is the FR-001 expected default-team set:
// the three chat colleagues plus the three staff roles. Admin ("no team
// membership") and the two hidden engine roles ("engine-owned only") are
// absent BY SPEC, not by observation of coreagent.All().
var adr090RosterSixOrdinaryTeamRoles = []string{"mia", "jim", "ava", "planner", "researcher", "worker"}

// adr090FullRosterConfig builds a config carrying the complete ADR-090
// fresh-install roster (§2.0): 4 core chat targets (incl. Admin), 3 staff
// workers, 2 hidden system agents. Used both directly (unit tests) and via
// the handler fixture below.
func adr090FullRosterConfig() *config.Config {
	return &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCore, Default: true},
				{ID: "jim", Name: "Jim", Type: config.AgentTypeCore},
				{ID: "ava", Name: "Ava", Type: config.AgentTypeCore},
				{ID: "admin", Name: "Admin", Type: config.AgentTypeCore},
				{ID: "planner", Name: "Planner", Type: config.AgentTypeWorker},
				{ID: "researcher", Name: "Researcher", Type: config.AgentTypeWorker},
				{ID: "worker", Name: "General Purpose", Type: config.AgentTypeWorker},
				{ID: "judge", Name: "Judge", Type: config.AgentTypeSystem, Locked: true},
				{ID: "plansupervisor", Name: "Plan Supervisor", Type: config.AgentTypeSystem, Locked: true},
			},
		},
	}
}

// TestDefaultWorkspaceTeam_ADR090_ExcludesAdminKeepsSixOrdinaryRoles is the
// seed-side spec oracle (FR-001). It must fail if defaultWorkspaceTeam ever
// includes Admin (standalone operator — "no team membership") or loses one
// of the six ordinary team roles.
func TestDefaultWorkspaceTeam_ADR090_ExcludesAdminKeepsSixOrdinaryRoles(t *testing.T) {
	team := defaultWorkspaceTeam(adr090FullRosterConfig())
	assert.ElementsMatch(t, adr090RosterSixOrdinaryTeamRoles, team,
		"the boot default team must be exactly the six ordinary FR-001 team roles")
	assert.NotContains(t, team, "admin", "Admin is the standalone operator (ADR-090 §2.2) and must never seed onto a workspace team")
	assert.NotContains(t, team, "judge", "hidden engine roles must never seed onto a workspace team")
	assert.NotContains(t, team, "plansupervisor", "hidden engine roles must never seed onto a workspace team")
}

// TestDefaultWorkspaceSeeder_BootsWithoutAdminMembership drives the FULL boot
// pipeline (defaultWorkspaceTeam -> defaultWorkspaceDelegationEdges ->
// seedEdgesForTeam -> ensureDefaultWorkspace) and asserts both persisted
// artifacts: the workspace record's core_team has the six ordinary roles and
// no Admin, and the seeded delegation graph has no edge with an Admin
// endpoint (Admin cannot delegate or be delegated — ADR-090 §3).
func TestDefaultWorkspaceSeeder_BootsWithoutAdminMembership(t *testing.T) {
	home := t.TempDir()
	cfg := adr090FullRosterConfig()
	require.NoError(t, ensureDefaultWorkspace(home, "alice", cfg))

	wss, err := listWorkspaceFiles(home)
	require.NoError(t, err)
	require.Len(t, wss, 1, "boot must seed exactly one default workspace")
	ws := wss[0]
	assert.ElementsMatch(t, adr090RosterSixOrdinaryTeamRoles, ws.CoreTeam,
		"the persisted default workspace team must be the six ordinary FR-001 roles")
	assert.NotContains(t, ws.CoreTeam, "admin",
		"Admin must not be a member of the boot default workspace (ADR-090 §2.2: standalone operator)")

	seeded := loadStoredDelegationEdges(t, home, ws.ID)
	require.NotEmpty(t, seeded, "sanity: the ordinary roster's seed edges must still exist")
	for _, e := range seeded {
		assert.NotEqual(t, "admin", e.FromAgent, "no seeded delegation edge may originate from Admin (ADR-090 §3)")
		assert.NotEqual(t, "admin", e.ToAgent, "no seeded delegation edge may target Admin (ADR-090 §3)")
	}
}

// TestValidateCoreTeamMembers_ADR090_RejectsAdminAndHiddenRoles is the unit
// oracle for the write-path validator: an Admin id must be rejected with a
// message naming it (FR-006), hidden system ids keep their existing
// rejection, and an ordinary clean team still passes.
func TestValidateCoreTeamMembers_ADR090_RejectsAdminAndHiddenRoles(t *testing.T) {
	cfg := adr090FullRosterConfig()

	err := validateCoreTeamMembers(cfg, []string{"mia", "admin", "jim"})
	require.Error(t, err, "an Admin membership must be rejected on write")
	assert.Contains(t, err.Error(), "admin", "the rejection must name the offending id")
	assert.Contains(t, strings.ToLower(err.Error()), "admin", "the rejection must be attributable to Admin specifically")

	err = validateCoreTeamMembers(cfg, []string{"mia", "judge"})
	require.Error(t, err, "hidden system agents stay rejected (existing behavior must not regress)")

	assert.NoError(t, validateCoreTeamMembers(cfg, []string{"mia", "jim", "ava", "worker"}),
		"an ordinary team must still validate cleanly")
}

// newAdminStandaloneAPI builds a lean restAPI whose live config carries the
// full ADR-090 roster (see adr090FullRosterConfig) so the real HTTP handlers
// can be exercised against an Admin-membership write. Mirrors
// newJudgeRosterAPI's shape (judge_system_agent_test.go) minus the agent
// entity-store seeding: the workspace handlers under test never reach the
// agent entity write path.
func newAdminStandaloneAPI(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)

	cfg := adr090FullRosterConfig()
	cfgJSON, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), cfgJSON, 0o600))

	// Agent entity records are required only by handlers that hit
	// agentstore.Update; the workspace handlers here do not. Seeding them
	// anyway keeps the fixture one mental model with newJudgeRosterAPI.
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

// seedAdr090WorkspaceOnDisk persists a minimal workspace record with the
// given team and returns its id.
func seedAdr090WorkspaceOnDisk(t *testing.T, api *restAPI, id string, team []string) {
	t.Helper()
	wsDir := filepath.Join(api.homePath, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o700))
	ws := workspace.Workspace{
		ID:        id,
		Name:      "ADR-090 Team WS",
		Status:    "active",
		CoreTeam:  team,
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}
	wsData, err := json.MarshalIndent(ws, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, id+".json"), wsData, 0o600))
}

// TestWorkspacePUT_ADR090_RejectsAdminMembershipBeforeWrites proves the REST
// write path refuses to introduce Admin onto a team and that the rejection is
// ZERO-WRITE: the stored workspace bytes are byte-identical after the 400.
func TestWorkspacePUT_ADR090_RejectsAdminMembershipBeforeWrites(t *testing.T) {
	api := newAdminStandaloneAPI(t)
	const wsID = "01JXADR090PUTADMIN000001"
	seedAdr090WorkspaceOnDisk(t, api, wsID, []string{"mia", "jim"})

	wsPath := filepath.Join(api.homePath, "workspaces", wsID+".json")
	before, err := os.ReadFile(wsPath)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+wsID,
		strings.NewReader(withWorkspaceRevisionJSON(t, api, wsID, `{"core_team":["mia","jim","admin"]}`)))
	r.Header.Set("Content-Type", "application/json")
	api.HandleWorkspaces(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"introducing Admin onto a workspace team must 400; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "admin",
		"the 400 must name the offending member so the operator can act on it")

	after, err := os.ReadFile(wsPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after),
		"a rejected team write must be zero-write — the workspace record must be unchanged")
}

// TestWorkspacePOST_ADR090_RejectsAdminInInitialTeam covers the create path:
// a POST whose initial core_team contains Admin is 400 and must not leave a
// workspace record behind.
func TestWorkspacePOST_ADR090_RejectsAdminInInitialTeam(t *testing.T) {
	api := newAdminStandaloneAPI(t)
	before := snapshotOutsideToolState(t, filepath.Join(api.homePath, "workspaces"))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
		strings.NewReader(`{"name":"Admin Team WS","core_team":["mia","admin"]}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleWorkspaces(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"creating a workspace with Admin on the initial team must 400; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "admin")

	after := snapshotOutsideToolState(t, filepath.Join(api.homePath, "workspaces"))
	assert.Equal(t, before, after, "a rejected create must leave existing workspace storage unchanged")
}

// TestWorkspaceDelegationPUT_ADR090_RejectsAdminEndpoints proves no
// delegation edge with an Admin endpoint can be written: on a clean team the
// edge fails endpoint validation (Admin is not a team member), so Admin can
// neither delegate nor be delegated through the REST graph write.
func TestWorkspaceDelegationPUT_ADR090_RejectsAdminEndpoints(t *testing.T) {
	api := newAdminStandaloneAPI(t)
	const wsID = "01JXADR090DELEGADMIN0001"
	seedAdr090WorkspaceOnDisk(t, api, wsID, []string{"jim", "ava"})

	body := `{"edges":[{"from_agent":"jim","to_agent":"admin","modes":["direct"]}]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+wsID+"/delegation",
		strings.NewReader(withWorkspaceRevisionJSON(t, api, wsID, body)))
	r.Header.Set("Content-Type", "application/json")
	api.handleWorkspaceDelegationPut(w, r, wsID)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"an edge targeting Admin must 400 (endpoint not on team); body=%s", w.Body.String())

	// Zero-write on the delegation store too: no store file may appear.
	storePath := filepath.Join(api.homePath, "entities", "delegation", wsID+".json")
	_, statErr := os.Stat(storePath)
	assert.True(t, errors.Is(statErr, os.ErrNotExist),
		"a rejected delegation write must not create a delegation store record")
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

// TestLogWorkspacelessAgents_ADR090_DoesNotFlagAdmin pins the diagnostic
// half of the gap: Admin is DELIBERATELY a member of no workspace (standalone
// operator), so the boot WARN that names workspaceless agents must exclude
// it — otherwise every boot cries wolf about the designed state. An ordinary
// unassigned agent must still be flagged (the diagnostic keeps its value).
func TestLogWorkspacelessAgents_ADR090_DoesNotFlagAdmin(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	coreagent.SeedConfig(cfg)
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{ID: "orphan-bot", Type: config.AgentTypeCustom})

	capture := &capturingSlogHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(prev) })

	logWorkspacelessAgents(home, cfg)

	ids := capture.workspacelessIDs()
	require.NotEmpty(t, ids, "sanity: the ordinary workspaceless agent must still be flagged")
	assert.Contains(t, ids, "orphan-bot")
	assert.NotContains(t, ids, "admin",
		"Admin is teamless by design (ADR-090 §2.2) — the boot diagnostic must not flag it")
}

// TestWorkspaceGET_TeamWire_UnaffectedByAdminExclusion is a small contract
// guard: workspaceDelegationToWire derives team[] from the workspace's own
// core_team ∪ edge endpoints, so excluding Admin from SEEDS must not change
// how an ordinary workspace renders.
func TestWorkspaceGET_TeamWire_UnaffectedByAdminExclusion(t *testing.T) {
	ws := storedWorkspace{ID: "ws-wire", CoreTeam: []string{"mia", "jim"}}
	wire := workspaceDelegationToWire(ws, nil, 3, strings.Repeat("0", 64))
	require.NotNil(t, wire.Team)
	assert.ElementsMatch(t, []string{"mia", "jim"}, *wire.Team)
}
