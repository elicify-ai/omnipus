// Omnipus — ADR-090 TEST007 (gateway half): the default workspace's boot
// fixture matches the nine-role roster — the six ordinary team roles only,
// Admin and the hidden System Agents excluded, and the founder's Jim→Ava
// delegation edge seeded on the default graph.
//
// This is a NEW dedicated file (sanctioned by the task brief: the runtime
// fixture lives in this package); the pre-existing
// rest_workspace_delegation_test.go::TestDefaultWorkspaceSeeder_TeamAndEdges
// still pins the pre-ADR-090 roster (ray/explorer) and is owned elsewhere.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// adr090WorkspaceTeam is the spec roster's team half: every ordinary role
// (four core + three workers) is a default-workspace teammate; admin is
// deliberately absent — the ADR-090 roster table gives Admin "no team
// membership", and FR-006 forbids Admin or hidden agents joining ordinary
// teams to bypass role boundaries. Stated from the spec, not read back from
// coreagent.All(), so a seeding regression fails here rather than echoing the
// seed.
var adr090WorkspaceTeam = []string{
	"mia", "jim", "ava", "planner", "researcher", "worker",
}

// adr090WorkspaceBannedFromTeam lists identities that must never appear on a
// default-workspace team: admin (spec: no team membership), the two hidden
// System Agents (judge, plansupervisor — never team targets), and the three
// retired identities (ray, explorer, max — not seeded at all).
var adr090WorkspaceBannedFromTeam = []string{
	"admin", "judge", "plansupervisor", "ray", "explorer", "max",
}

// TestADR090_DefaultWorkspaceTeam_ExcludesAdminAndHidden pins the roster
// derivation (rest_workspace_delegation.go::defaultWorkspaceTeam) on the REAL
// boot composition — config.DefaultConfig() + coreagent.SeedConfig, the same
// composition pkg/gateway's boot performs — rather than a hand-built list: the
// seeded nine-role roster minus admin must yield exactly the six ordinary team
// roles.
//
// The config-level roster shape is pinned in pkg/coreagent; this adds the
// gateway-side consequence: whoever All() returns, the default team may only
// ever be the ordinary roles.
func TestADR090_DefaultWorkspaceTeam_ExcludesAdminAndHidden(t *testing.T) {
	team := defaultWorkspaceTeam(seededBootConfig(t))
	assert.ElementsMatch(t, adr090WorkspaceTeam, team,
		"the default workspace team is exactly the six ordinary ADR-090 roles")
	for _, banned := range adr090WorkspaceBannedFromTeam {
		assert.NotContainsf(t, team, banned,
			"defaultWorkspaceTeam must exclude %q (admin: spec 'no team membership'; judge/plansupervisor: hidden System Agents; ray/explorer/max: retired)", banned)
	}
}

// TestADR090_DefaultWorkspaceSeeder_BootTeamAndJimToAvaEdge runs the actual
// boot fixture (rest_workspaces.go::ensureDefaultWorkspace) on a fresh home
// with the real boot composition and verifies what lands on disk:
//
//   - exactly one workspace, flagged default;
//   - its core_team is the six ordinary roles — admin and the hidden System
//     Agents excluded (FR-006), admin never even an edge endpoint;
//   - the founder delta's Jim→Ava edge is on the delegation store's seeded
//     graph, with Jim's three seed modes (task, background, await) collapsed
//     and deduped to the graph's two-value vocabulary [task, direct]
//     (agent.EdgeModeCategory).
//
// Edges are read from the delegation STORE ($OMNIPUS_HOME/entities/delegation/
// <id>.json) via the same helper the pre-existing delegation tests use — the
// workspace record itself never carries the graph, so asserting there would
// pass on a hostile edge and fail on a legitimate one.
func TestADR090_DefaultWorkspaceSeeder_BootTeamAndJimToAvaEdge(t *testing.T) {
	home := t.TempDir()
	cfg := seededBootConfig(t)
	cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
	require.NoError(t, ensureDefaultWorkspace(home, "alice", cfg))

	wss, err := listWorkspaceFiles(home)
	require.NoError(t, err)
	require.Len(t, wss, 1, "a fresh install seeds exactly one default workspace")
	ws := wss[0]
	assert.True(t, ws.IsDefault, "the seeded workspace must be the default workspace")

	assert.ElementsMatch(t, adr090WorkspaceTeam, ws.CoreTeam,
		"the seeded default workspace's core_team is the six ordinary ADR-090 roles")
	for _, banned := range adr090WorkspaceBannedFromTeam {
		assert.NotContainsf(t, ws.CoreTeam, banned,
			"the seeded default workspace's core_team must exclude %q (FR-006)", banned)
	}

	edges := loadStoredDelegationEdges(t, home, ws.ID)
	require.NotEmpty(t, edges,
		"the seeded default workspace must carry a non-empty delegation graph — an empty graph denies all delegation (ADR-037 fail-closed)")
	byPair := make(map[string]workspace.DelegationEdge, len(edges))
	for _, e := range edges {
		byPair[e.FromAgent+"->"+e.ToAgent] = e
		assert.NotEqualf(t, "admin", e.FromAgent,
			"admin must never be a delegation edge source (no team membership)")
		assert.NotEqualf(t, "admin", e.ToAgent,
			"admin must never be a delegation edge target (no team membership)")
	}

	jimToAva, ok := byPair["jim->ava"]
	require.Truef(t, ok,
		"founder delta: the default-workspace seed must carry the Jim→Ava delegation edge; got edges %v", edges)
	assert.ElementsMatch(t,
		[]workspace.DelegationMode{workspace.ModeTask, workspace.ModeDirect},
		jimToAva.Modes,
		"jim->ava must carry Jim's seeded modes (task, background, await), collapsed+deduped to [task, direct] via agent.EdgeModeCategory")
}
