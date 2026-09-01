// Omnipus — integration tests for ADR-072 R1 fix: wiring a real workspace's
// mounts through to a real, production-constructed ContextBuilder.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

// testMountRecord mirrors pkg/workspace/mountstore.go's unexported
// mountStoreRecord/Mount JSON shape ({"workspace_id":..., "mounts":[{"name":
// ...,"host_path":...}]}). Duplicated here (rather than imported) because the
// production shape is unexported — the same reasoning
// delegation_graph_testhelper_test.go's testWorkspaceRecord already applies
// to the workspace record.
type testMountRecord struct {
	WorkspaceID string      `json:"workspace_id"`
	Mounts      []testMount `json:"mounts"`
}

type testMount struct {
	Name     string `json:"name"`
	HostPath string `json:"host_path"`
}

// seedProjectShelfWorkspace writes a minimal on-disk workspace record (no
// AGENT.md needed for these tests) under home for wsID, and — when mountName
// is non-empty — a mount record pointing at mountRoot.
func seedProjectShelfWorkspace(t *testing.T, home, wsID, mountName, mountRoot string) {
	t.Helper()
	wsDir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	record := `{"id":"` + wsID + `","is_default":false}`
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, wsID+".json"), []byte(record), 0o644))

	if mountName == "" {
		return
	}
	mountsDir := filepath.Join(home, "entities", "mounts")
	require.NoError(t, os.MkdirAll(mountsDir, 0o700))
	rec := testMountRecord{
		WorkspaceID: wsID,
		Mounts:      []testMount{{Name: mountName, HostPath: mountRoot}},
	}
	data, err := json.Marshal(rec)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(mountsDir, wsID+".json"), data, 0o600))
}

// writeProjectSkillFixture creates <mountRoot>/.claude/skills/<slug>/SKILL.md
// with valid frontmatter, matching the fixture shape pkg/skills/project_test.go
// already uses for DiscoverProjectSkills' own unit tests.
func writeProjectSkillFixture(t *testing.T, mountRoot, slug, description string) {
	t.Helper()
	skillDir := filepath.Join(mountRoot, ".claude", "skills", slug)
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	content := "---\nname: " + slug + "\ndescription: " + description + "\n---\n\nBody.\n"
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644))
}

// buildLoopForProjectShelfTest constructs a real AgentLoop the same way
// production boot does (NewAgentLoop -> wireEnvProviders ->
// wireProjectShelfResolvers), and returns the named agent's ContextBuilder.
func buildLoopForProjectShelfTest(t *testing.T, agentID string) *ContextBuilder {
	t.Helper()
	cfg := minimalTestConfig(t)
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.Defaults.MaxTokens = 4096

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	require.NoError(t, err)
	t.Cleanup(func() { al.Close() })

	inst, ok := al.GetRegistry().GetAgent(agentID)
	require.True(t, ok, "agent %q must be in the registry after loop init", agentID)
	require.NotNil(t, inst.ContextBuilder)
	return inst.ContextBuilder
}

// TestWireProjectShelfResolvers_MountedProjectSkillResolvesEndToEnd is the R1
// regression test: before this fix, ContextBuilder.WithProjectShelfResolver
// (and WithProjectShelf) were never called anywhere in production
// (pkg/agent/instance.go's agent-construction path never called either), so
// cb.projectShelf was permanently nil and every project-shelf-aware code path
// behaved as if no mount ever existed — confirmed live in
// docs/internal/qa/uat-report-skill-activation-batch2-groupD-2026-09-02.md
// (S17/S18/S18b: Skill(name="deploy-helper") -> skill_not_found, the "#
// Skills" menu never showed it, and /deploy-helper fell through to ordinary
// chat text).
//
// This builds a REAL AgentLoop via NewAgentLoop (the exact production
// construction path, including wireEnvProviders -> wireProjectShelfResolvers)
// against a workspace with a real mount holding a real project skill, and
// exercises the SAME method the live Skill tool calls
// (ContextBuilder.ResolveSkillFullForWorkspace — see pkg/agent/loop.go's
// Skill-tool dispatch around ResolveSkillFullForWorkspace/
// ProjectShelfForWorkspace) plus the menu-building path
// (BuildSystemPromptForWorkspace), not just the underlying pure functions
// (skills.MergeProjectSkills, skills.ResolveSkillName) those already have
// their own unit tests for.
func TestWireProjectShelfResolvers_MountedProjectSkillResolvesEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	const wsID = "01PSHELFWIRING000000001"
	mountRoot := t.TempDir()
	writeProjectSkillFixture(t, mountRoot, "deploy-helper", "Use when the user asks to deploy the service")
	seedProjectShelfWorkspace(t, home, wsID, "acme", mountRoot)

	cb := buildLoopForProjectShelfTest(t, "ava")

	// 1. The project shelf itself resolves and contains the mounted skill.
	shelf := cb.ProjectShelfForWorkspace(wsID)
	require.NotNil(t, shelf, "project shelf must not be nil once a mount with a real project skill is wired")
	ps, ok := shelf["deploy-helper"]
	require.True(t, ok, "shelf must contain the mounted skill by its (lower-cased) slug")
	assert.Equal(t, "acme", ps.MountName)

	// 2. The exact call the live Skill tool's load path makes
	// (pkg/agent/loop.go's dispatch to ResolveSkillFullForWorkspace) resolves
	// the mounted skill — this is the concrete fix for S18's
	// "skill_not_found" and S18b's "/deploy-helper falls through to chat".
	resolved, ok := cb.ResolveSkillFullForWorkspace(wsID, "deploy-helper")
	require.True(t, ok, "ResolveSkillFullForWorkspace must resolve a mounted project skill")
	assert.Equal(t, "deploy-helper", resolved.Slug)
	assert.Equal(t, skills.ShelfProject, resolved.Shelf)
	assert.Equal(t, "acme", resolved.MountName)

	// 3. The "# Skills" menu (BuildSystemPromptForWorkspace, the D8
	// (agent x workspace) cache-aware build the static system prompt uses)
	// actually lists it — the concrete fix for S17's "menu never rendered".
	prompt := cb.BuildSystemPromptForWorkspace(wsID)
	assert.Contains(t, prompt, "deploy-helper", "the assembled system prompt's Skills menu must list the mounted project skill")
}

// TestWireProjectShelfResolvers_WorkspaceScoped_NoLeakToOtherWorkspace proves
// D4.1's "workspace-scoped, not global" bound: the SAME agent asking about a
// DIFFERENT workspace (no mount of its own) must not see the first
// workspace's project skill. This is also what makes S19 (from the UAT plan)
// meaningful once R1 is fixed, rather than "confounded" by every workspace
// looking identically empty.
func TestWireProjectShelfResolvers_WorkspaceScoped_NoLeakToOtherWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	const mountedWsID = "01PSHELFWIRING000000002"
	const bareWsID = "01PSHELFWIRING000000003"
	mountRoot := t.TempDir()
	writeProjectSkillFixture(t, mountRoot, "deploy-helper", "Use when the user asks to deploy the service")
	seedProjectShelfWorkspace(t, home, mountedWsID, "acme", mountRoot)
	seedProjectShelfWorkspace(t, home, bareWsID, "", "") // no mount at all

	cb := buildLoopForProjectShelfTest(t, "ava")

	_, ok := cb.ResolveSkillFullForWorkspace(bareWsID, "deploy-helper")
	assert.False(t, ok, "a workspace with no mounts must not resolve another workspace's project skill")

	shelf := cb.ProjectShelfForWorkspace(bareWsID)
	assert.Empty(t, shelf, "an unmounted workspace's project shelf must be empty")

	// Sanity: the mounted workspace still resolves it (proves the emptiness
	// above is real scoping, not a wiring failure that would make every
	// workspace look empty).
	_, ok = cb.ResolveSkillFullForWorkspace(mountedWsID, "deploy-helper")
	assert.True(t, ok, "the mounted workspace must still resolve its own project skill")
}

// TestWireProjectShelfResolvers_NoMounts_NoRegression proves the resolver is
// a true superset of the pre-fix behaviour for a workspace with no mounts at
// all: no panic, no error, an empty/nil shelf, and unresolved lookups behave
// exactly as they did when the resolver was never wired.
func TestWireProjectShelfResolvers_NoMounts_NoRegression(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	const wsID = "01PSHELFWIRING000000004"
	seedProjectShelfWorkspace(t, home, wsID, "", "")

	cb := buildLoopForProjectShelfTest(t, "ava")

	shelf := cb.ProjectShelfForWorkspace(wsID)
	assert.Empty(t, shelf)

	_, ok := cb.ResolveSkillFullForWorkspace(wsID, "anything")
	assert.False(t, ok)
}
