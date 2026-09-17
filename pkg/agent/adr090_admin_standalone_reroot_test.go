// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// adr090_admin_standalone_reroot_test.go — pins the ADR-090 Admin standalone
// operator on the TURN-ADMISSION side (resolveTurnWorkDirOrRefuse, the single
// shared gate for native and external-cli dispatch):
//
//   - Admin (§2.2 / FR-001: "Yes / no team membership") must have REAL,
//     executable turns rooted at its own agent home — chat-able core, but
//     never a workspace teammate;
//   - the rooting must hold EVEN WHEN a workspace record lists admin in its
//     core_team. The workspace record is writable by the sandboxed child
//     (the reason the delegation store lives in entities/, see
//     pkg/workspace/delegationstore.go), so a membership-driven Admin branch
//     would let a forged core_team entry re-root the operator's turns into a
//     child-writable workspace. Admin's home rooting must be checked BEFORE
//     workspace resolution, unconditionally;
//   - an empty/unusable agent home still refuses (ErrAgentHomeUnavailable) —
//     standalone is not a license to fall back to the process CWD;
//   - ordinary unassigned agents stay refused (ErrAgentNotWorkspaceMember) —
//     the standalone route is Admin's alone, not a general relaxation;
//   - malformed/unsafe workspace ids still fail the traversal guard for the
//     agents that DO go through workspace resolution.
//
// Like workspace_team_reroot_test.go, the E2E cases drive runTurn through
// ProcessDirect with a scripted provider and assert purely from where the
// file landed on disk. They deliberately construct the loop via NewAgentLoop
// (NOT mustNewAgentLoop) — the harness's ensureTestWorkspaceMembership would
// otherwise seed Admin onto a workspace and defeat the standalone case.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// adr090StandaloneLoopConfig builds the minimal loop config for the reroot
// E2E cases: one agent (the given id), sandboxed (os.Root-relative) file
// resolution so a relative write_file lands under the turn's resolved root.
func adr090StandaloneLoopConfig(agentID, homeDir string) *config.Config {
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:                homeDir,
				DefaultModel:        config.DefaultModel{Model: "scripted-model"},
				MaxTokens:           4096,
				MaxToolIterations:   10,
				RestrictToWorkspace: true,
			},
			List: []config.AgentConfig{{ID: agentID}},
		},
	}
}

// seedForgedAdminWorkspace persists a workspace record that CLAIMS admin on
// its core_team. Under ADR-090 no legitimate writer can produce this file
// (REST, tools, and boot seed all exclude Admin) — but the file is on the
// child-writable side of the boundary, so admission must not trust it.
func seedForgedAdminWorkspace(t *testing.T, home string) {
	t.Helper()
	wsDir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	forged := `{"id":"forged-ws","core_team":["admin","mia"]}`
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "forged-ws.json"), []byte(forged), 0o644))
}

// TestResolveTurnWorkDirOrRefuse_AdminRootsAtOwnHome_IgnoresForgedCoreTeam is
// the unit oracle for the admission gate: with a forged membership record on
// disk, Admin still resolves to its own agent home — and the forge must not
// even cause the workspace's work/ directory to be created (the branch must
// return before workspace resolution runs at all).
func TestResolveTurnWorkDirOrRefuse_AdminRootsAtOwnHome_IgnoresForgedCoreTeam(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	seedForgedAdminWorkspace(t, home)

	adminHome := filepath.Join(home, "agents", "admin") // deliberately NOT pre-created
	dir, err := resolveTurnWorkDirOrRefuse(context.Background(), "admin", adminHome, "")
	require.NoError(t, err, "an Admin turn must be admissible without any workspace membership (ADR-090 §2.2 standalone operator)")
	assert.Equal(t, adminHome, dir,
		"Admin's turn must root at its own agent home, not at any workspace — even one whose record claims admin")

	_, statErr := os.Stat(filepath.Join(home, "workspaces", "forged-ws", "work"))
	assert.True(t, os.IsNotExist(statErr),
		"resolving an Admin turn must not materialize any workspace work/ directory — the Admin branch must precede workspace resolution")

	_, homeStatErr := os.Stat(adminHome)
	require.NoError(t, homeStatErr, "the Admin home rooting must materialize its own directory (0700)")
}

// TestResolveTurnWorkDirOrRefuse_AdminEmptyHomeRefused pins the failure side
// of the Admin route: with no usable agent home the turn is refused with
// ErrAgentHomeUnavailable — never a silent fall-back to the process CWD or a
// workspace.
func TestResolveTurnWorkDirOrRefuse_AdminEmptyHomeRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	// No workspaces at all and an empty home — nothing to root at.
	_, err := resolveTurnWorkDirOrRefuse(context.Background(), "admin", "", "")
	require.Error(t, err, "an Admin turn with no agent home must be refused")
	assert.True(t, errors.Is(err, ErrAgentHomeUnavailable),
		"refusal must wrap ErrAgentHomeUnavailable, got: %v", err)
}

// TestResolveTurnWorkDirOrRefuse_OrdinaryUnassignedAgentStillRefused is the
// control for the standalone route's scope: an ordinary agent that is a
// member of nothing keeps the hard ADR-046 refusal. Standalone operation is
// Admin's role property (FR-001), not a general relaxation.
func TestResolveTurnWorkDirOrRefuse_OrdinaryUnassignedAgentStillRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	// No workspaces directory at all.
	_, err := resolveTurnWorkDirOrRefuse(context.Background(), "jim", filepath.Join(home, "agents", "jim"), "")
	require.Error(t, err, "an ordinary unassigned agent's turn must stay refused")
	assert.True(t, errors.Is(err, ErrAgentNotWorkspaceMember),
		"refusal must wrap ErrAgentNotWorkspaceMember, got: %v", err)
}

// TestResolveTurnWorkDirOrRefuse_UnsafeWorkspaceIDStillFails guards the
// traversal half of the return contract for agents that DO resolve through a
// workspace: a record whose content id fails the traversal guard
// (workspace.SafeWorkDir) refuses the turn rather than escaping home.
func TestResolveTurnWorkDirOrRefuse_UnsafeWorkspaceIDStillFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	wsDir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	// The id comes from the record CONTENT (find_for_agent.go's teamRecord),
	// so this reaches EnsureWorkDir unsanitized by the filename.
	evil := `{"id":"../evil","core_team":["jim"]}`
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "bad.json"), []byte(evil), 0o644))

	_, err := resolveTurnWorkDirOrRefuse(context.Background(), "jim", filepath.Join(home, "agents", "jim"), "")
	require.Error(t, err, "an unsafe workspace id must refuse the turn")
	assert.True(t, errors.Is(err, ErrWorkspaceWorkDirUnavailable),
		"refusal must wrap ErrWorkspaceWorkDirUnavailable, got: %v", err)

	_, statErr := os.Stat(filepath.Join(filepath.Dir(home), "evil"))
	assert.True(t, os.IsNotExist(statErr),
		"an unsafe workspace id must not materialize a directory outside home")
}

// TestRunTurn_AdminStandalone_WritesToOwnHomeNotForgedWorkspace is the E2E
// proof through the real dispatch path: a full Admin turn (scripted
// write_file) executes, lands in agents/admin/, and neither touches nor
// creates the forged workspace's tree. Pre-fix this is RED twice over: the
// turn would re-root into workspaces/forged-ws/work (membership claim honored).
func TestRunTurn_AdminStandalone_WritesToOwnHomeNotForgedWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	seedForgedAdminWorkspace(t, home)

	adminHome := filepath.Join(home, "agents", "admin")
	require.NoError(t, os.MkdirAll(adminHome, 0o755))

	provider := testutil.NewScenario().
		WithToolCall("write_file", `{"path":"proof.txt","content":"hello-from-standalone-admin","overwrite":true}`).
		WithText("done")

	cfg := adr090StandaloneLoopConfig("admin", adminHome)

	al, err := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	require.NoError(t, err, "NewAgentLoop must succeed")
	defer al.Close()
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	// This minimal fixture supplies the write permission explicitly.
	defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"write_file": "allow"},
	})

	finalContent, err := al.ProcessDirect(context.Background(), "please write proof.txt for me", "test-session-admin-standalone")
	require.NoError(t, err, "an Admin turn must be admissible with zero workspace memberships (ADR-090 standalone operator)")
	assert.Equal(t, "done", finalContent)
	requests := provider.AllRequests()
	require.NotEmpty(t, requests)
	for _, messages := range requests {
		for _, message := range messages {
			assert.NotContains(t, message.Content, filepath.Join(home, "workspaces", "forged-ws", "work"), "Admin instructions must not advertise a workspace it does not belong to")
		}
	}

	got, readErr := os.ReadFile(filepath.Join(adminHome, "proof.txt"))
	require.NoError(t, readErr,
		"write_file must land in Admin's own home (%s) — the standalone operator roots there", adminHome)
	assert.Equal(t, "hello-from-standalone-admin", string(got))

	// The forged membership must not have re-rooted the turn…
	forgedWork := filepath.Join(home, "workspaces", "forged-ws", "work", "proof.txt")
	_, forgedErr := os.Stat(forgedWork)
	assert.True(t, os.IsNotExist(forgedErr),
		"write_file must NOT land in a workspace that merely claims admin on core_team (%s)", forgedWork)
	// …and must not even have had its work/ tree materialized.
	_, forgedDirErr := os.Stat(filepath.Join(home, "workspaces", "forged-ws", "work"))
	assert.True(t, os.IsNotExist(forgedDirErr),
		"an Admin turn must not materialize any workspace work/ directory")

	// No spontaneous workspace creation or join: exactly the one seeded
	// (forged) record may exist — nothing new, nothing renamed.
	entries, lsErr := os.ReadDir(filepath.Join(home, "workspaces"))
	require.NoError(t, lsErr)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.ElementsMatch(t, []string{"forged-ws.json"}, names,
		"an Admin turn must not create, join, or otherwise mutate any workspace record")
}

// TestRunTurn_OrdinaryUnassignedAgentRefused is the E2E control adjacent to
// the Admin E2E above: the same loop shape for an ordinary team-role agent
// with no memberships is refused (ErrAgentNotWorkspaceMember) and writes
// nothing anywhere. Keeping it in the same file means no future edit can
// widen the standalone route without failing its control first.
func TestRunTurn_OrdinaryUnassignedAgentRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	jimHome := filepath.Join(home, "agents", "jim")
	require.NoError(t, os.MkdirAll(jimHome, 0o755))

	provider := testutil.NewScenario().
		WithToolCall("write_file", `{"path":"proof.txt","content":"should-never-land","overwrite":true}`).
		WithText("done")

	cfg := adr090StandaloneLoopConfig("jim", jimHome)

	al, err := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	require.NoError(t, err, "NewAgentLoop must succeed")
	defer al.Close()
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"write_file": "allow"},
	})

	_, procErr := al.ProcessDirect(context.Background(), "please write proof.txt for me", "test-session-jim-unassigned")
	require.Error(t, procErr, "an ordinary unassigned agent's turn must be refused")
	assert.True(t, errors.Is(procErr, ErrAgentNotWorkspaceMember),
		"refusal must wrap ErrAgentNotWorkspaceMember, got: %v", procErr)

	_, statErr := os.Stat(filepath.Join(jimHome, "proof.txt"))
	assert.True(t, os.IsNotExist(statErr), "a refused turn must write nothing")
	_, wsErr := os.Stat(filepath.Join(home, "workspaces"))
	assert.True(t, os.IsNotExist(wsErr), "a refused turn must not create any workspace directory")
}
