// Omnipus — ADR-090 TEST007 (gateway half), amended for the Omnipus system
// agent (FR-001/FR-003/FR-008): the default workspace's boot fixture carries
// the six ordinary team roles — Omnipus included, the hidden System Agents and
// the retired Ava/Admin excluded — and the seeded graph keeps Jim's role edges
// plus Omnipus's depth-1 self-row (§6.2).
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
// (three core — Omnipus an ordinary hidden member of the default workspace,
// omnipus-system-agent FR-003 — plus three workers) is a default-workspace
// teammate. Stated from the spec, not read back from coreagent.All(), so a
// seeding regression fails here rather than echoing the seed.
var adr090WorkspaceTeam = []string{
	"mia", "jim", "omnipus", "planner", "researcher", "worker",
}

// adr090WorkspaceBannedFromTeam lists identities that must never appear on a
// default-workspace team: the two hidden System Agents (judge,
// plansupervisor — never team targets) and the retired identities (ava,
// admin — omnipus-system-agent FR-008; ray, explorer, max — not seeded).
var adr090WorkspaceBannedFromTeam = []string{
	"ava", "admin", "judge", "plansupervisor", "ray", "explorer", "max",
}

// TestADR090_DefaultWorkspaceTeam_ExcludesRetiredAndHidden pins the roster
// derivation (rest_workspace_delegation.go::defaultWorkspaceTeam) on the REAL
// boot composition — config.DefaultConfig() + coreagent.SeedConfig, the same
// composition pkg/gateway's boot performs — rather than a hand-built list: the
// seeded roster minus the hidden System Agents must yield exactly the six
// ordinary team roles.
//
// The config-level roster shape is pinned in pkg/coreagent; this adds the
// gateway-side consequence: whoever All() returns, the default team may only
// ever be the ordinary roles.
func TestADR090_DefaultWorkspaceTeam_ExcludesRetiredAndHidden(t *testing.T) {
	team := defaultWorkspaceTeam(seededBootConfig(t))
	assert.ElementsMatch(t, adr090WorkspaceTeam, team,
		"the default workspace team is exactly the six ordinary ADR-090 roles")
	for _, banned := range adr090WorkspaceBannedFromTeam {
		assert.NotContainsf(t, team, banned,
			"defaultWorkspaceTeam must exclude %q (judge/plansupervisor: hidden System Agents; ava/admin/ray/explorer/max: retired)", banned)
	}
}

// TestADR090_DefaultWorkspaceSeeder_BootTeamAndJimToWorkerEdge runs the actual
// boot fixture (rest_workspaces.go::ensureDefaultWorkspace) on a fresh home
// with the real boot composition and verifies what lands on disk:
//
//   - exactly one workspace, flagged default;
//   - its core_team is the six ordinary roles — the hidden System Agents and
//     the retired ids excluded, a retired id never even an edge endpoint;
//   - Jim's role edge to General Purpose is on the delegation store's seeded
//     graph, with Jim's seed modes collapsed and deduped to the graph's
//     two-value vocabulary [task, direct] (agent.EdgeModeCategory);
//   - Omnipus's self-row carries depth 1 (omnipus-system-agent §6.2).
//
// Edges are read from the delegation STORE ($OMNIPUS_HOME/entities/delegation/
// <id>.json) via the same helper the pre-existing delegation tests use — the
// workspace record itself never carries the graph, so asserting there would
// pass on a hostile edge and fail on a legitimate one.
func TestADR090_DefaultWorkspaceSeeder_BootTeamAndJimToWorkerEdge(t *testing.T) {
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
		for _, retired := range []string{"ava", "admin"} {
			assert.NotEqualf(t, retired, e.FromAgent, "retired %s must never be a delegation edge source", retired)
			assert.NotEqualf(t, retired, e.ToAgent, "retired %s must never be a delegation edge target", retired)
		}
	}

	jimToWorker, ok := byPair["jim->worker"]
	require.Truef(t, ok,
		"the default-workspace seed must carry Jim's role edge to General Purpose; got edges %v", edges)
	assert.ElementsMatch(t,
		[]workspace.DelegationMode{workspace.ModeTask, workspace.ModeDirect},
		jimToWorker.Modes,
		"jim->worker must carry Jim's seeded modes, collapsed+deduped to [task, direct] via agent.EdgeModeCategory")

	omnipusSelf, ok := byPair["omnipus->omnipus"]
	require.Truef(t, ok, "Omnipus must keep its ordinary self-row (OSA-8); got edges %v", edges)
	require.NotNil(t, omnipusSelf.Depth)
	assert.Equal(t, 1, *omnipusSelf.Depth, "Omnipus's self-row depth is exactly 1 (omnipus-system-agent §6.2)")
}
