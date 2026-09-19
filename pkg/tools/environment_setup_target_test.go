package tools

// Target-resolution tests for the generic environment_setup tool (ES-FR-02,
// ES-BDD-03/10; security-review MAJ-6). File owned by the tool-input-fix lane;
// environment_setup.go is the only production file under test here.
//
// Test plan (oracle: the spec + MAJ-6, never the implementation):
//
//	1. Admin WITHOUT membership targeting an existing workspace installs into
//	   that workspace's WORK dir (workspaces/<id>/work/) — the same root a
//	   member turn's tools are re-rooted to (workspace.WorkDir) — never the
//	   workspace record root (workspaces/<id>/), which no member turn reads.
//	2. The Admin cross-workspace root EQUALS the root a beta member's own
//	   default-context install resolves to (member view == Admin install).
//	3. Non-Admin targeting another workspace is refused BEFORE allocation.
//	4. Admin targeting a workspace with no record is refused (no host-path
//	   probing via workspaces/), BEFORE allocation.
//	5. Path-shaped targets never resolve, even for Admin, BEFORE allocation.
//	6. scope=shared + explicit target_workspace is refused BEFORE allocation
//	   (the target selects a workspace-scope destination).
//	7. Admin first-use: a workspace record with no work/ dir yet is prepared
//	   (sanctioned workspace.EnsureWorkDir) and the install targets it —
//	   Admin can install for a valid workspace before any member turn.
//	8. Real-storage leg: the resolved root, allocated through the REAL
//	   storage seam and written to, is visible to the member-side lookup
//	   (environmentsetup.RuntimeEnvPaths) a beta member turn would do.
//
// Unit boundary: real EnvironmentSetupTool via the public constructor; the
// store seam RECORDS the BeginInstall arguments and refuses, so a began
// counter of 1 proves validation+resolution passed and exposes the resolved
// root, while 0 proves rejection preceded any side effect. No process, no
// sandbox, no filesystem allocation in any case.
//
// Pre-registered RED predictions (against the pre-fix source): cases 1–2 RED
// (Admin branch resolved SafeWorkspaceDir = record root), 3–6 GREEN.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/environmentsetup"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// targetRecordingStore records the BeginInstall arguments of the (single)
// allocation attempt and refuses it, keeping the test process-free.
type targetRecordingStore struct {
	began         int
	appDataRoot   string
	workspaceRoot string
	scope         string
}

func (s *targetRecordingStore) BeginInstall(appDataRoot, workspaceRoot, scope string) (EnvironmentSetupTarget, error) {
	s.began++
	s.appDataRoot = appDataRoot
	s.workspaceRoot = workspaceRoot
	s.scope = scope
	return nil, fmt.Errorf("targetRecordingStore: allocation refused after successful resolution")
}

// newTargetTestTool builds the tool over an isolated home.
func newTargetTestTool(home string, admin bool, store *targetRecordingStore) *EnvironmentSetupTool {
	return NewEnvironmentSetupTool(EnvironmentSetupToolDeps{
		Home:         home,
		AgentWorkDir: filepath.Join(home, "agents", "fixture"),
		Admin:        admin,
		Store:        store,
	})
}

// newTargetTestContext layers the turn facts the loop normally carries.
func newTargetTestContext(wsID, wsDir string) context.Context {
	ctx := WithToolContext(context.Background(), "cli", "chat")
	ctx = WithAgentID(ctx, "fixture-agent")
	if wsID != "" {
		ctx = WithWorkspaceID(ctx, wsID)
	}
	if wsDir != "" {
		ctx = WithTurnWorkspaceDir(ctx, wsDir)
	}
	return WithTranscriptSessionID(ctx, "t-target")
}

// writeTargetTestWorkspaceRecord plants the workspace record the Admin
// cross-workspace path requires under <home>/workspaces/.
func writeTargetTestWorkspaceRecord(t *testing.T, home, id string) {
	t.Helper()
	record := filepath.Join(home, "workspaces", id+".json")
	require.NoError(t, os.MkdirAll(filepath.Dir(record), 0o755))
	require.NoError(t, os.WriteFile(record, []byte(`{"id":"`+id+`"}`), 0o644))
}

// targetRunArgs is the valid baseline a case mutates.
func targetRunArgs() map[string]any {
	return map[string]any{
		"command": "echo target-probe",
		"purpose": "target resolution fixture",
	}
}

func TestEnvironmentSetupTarget_AdminCrossWorkspace_TargetsWorkDir(t *testing.T) {
	home := t.TempDir()
	writeTargetTestWorkspaceRecord(t, home, "beta")
	store := &targetRecordingStore{}
	tool := newTargetTestTool(home, true, store)

	// Admin's own turn context is admin-home: no beta membership anywhere.
	ctx := newTargetTestContext("admin-home", filepath.Join(home, "workspaces", "admin-home"))
	a := targetRunArgs()
	a["target_workspace"] = "beta"
	res := tool.Execute(ctx, a)

	// Resolution PASSED (began 1); the fake store then refused allocation —
	// the refusal message must be the allocation one, not a validation error.
	require.Equal(t, 1, store.began, "valid Admin target must reach allocation exactly once; result: %s", res.ForLLM)
	require.True(t, res.IsError)
	assert.Contains(t, res.ForLLM, "installation area could not be created")

	// MAJ-6 oracle: the Admin install must land in beta's WORK dir — where
	// beta member turns are rooted — not the workspace record root. Both an
	// independent literal path and the workspace.WorkDir lookup must agree.
	want := workspace.WorkDir(home, "beta")
	assert.Equal(t, want, store.workspaceRoot, "Admin target root must equal the member work-dir lookup")
	assert.Equal(t, filepath.Join(home, "workspaces", "beta", "work"), store.workspaceRoot)
	assert.Equal(t, "workspace", store.scope)
	assert.Equal(t, home, store.appDataRoot)
	assert.True(t, strings.HasPrefix(store.workspaceRoot, home+string(os.PathSeparator)),
		"resolved root must stay under OMNIPUS_HOME, got %q", store.workspaceRoot)
	// The record root is exactly what MAJ-6 forbids.
	assert.NotEqual(t, filepath.Join(home, "workspaces", "beta"), store.workspaceRoot,
		"Admin install must not land in the workspace record root")
}

func TestEnvironmentSetupTarget_AdminInstallMatchesMemberDefaultLookup(t *testing.T) {
	home := t.TempDir()
	writeTargetTestWorkspaceRecord(t, home, "beta")

	// Leg A: Admin (member of nothing) installs into beta explicitly.
	adminStore := &targetRecordingStore{}
	adminTool := newTargetTestTool(home, true, adminStore)
	adminCtx := newTargetTestContext("admin-home", filepath.Join(home, "workspaces", "admin-home"))
	adminRes := adminTool.Execute(adminCtx, func() map[string]any {
		a := targetRunArgs()
		a["target_workspace"] = "beta"
		return a
	}())
	require.Equal(t, 1, adminStore.began, "Admin leg must reach allocation; result: %s", adminRes.ForLLM)

	// Leg B: a beta MEMBER (CoreTeam re-rooting) installs with no target arg.
	memberStore := &targetRecordingStore{}
	memberTool := newTargetTestTool(home, false, memberStore)
	memberCtx := newTargetTestContext("beta", workspace.WorkDir(home, "beta"))
	memberRes := memberTool.Execute(memberCtx, targetRunArgs())
	require.Equal(t, 1, memberStore.began, "member leg must reach allocation; result: %s", memberRes.ForLLM)

	// The acceptance: both resolve to the SAME directory.
	assert.Equal(t, memberStore.workspaceRoot, adminStore.workspaceRoot,
		"Admin cross-workspace install must be visible where beta member turns look")
	assert.Equal(t, workspace.WorkDir(home, "beta"), adminStore.workspaceRoot)
}

func TestEnvironmentSetupTarget_NonAdmin_OtherWorkspace_BlockedBeforeAllocation(t *testing.T) {
	home := t.TempDir()
	writeTargetTestWorkspaceRecord(t, home, "beta")
	store := &targetRecordingStore{}
	tool := newTargetTestTool(home, false, store)

	ctx := newTargetTestContext("alpha", filepath.Join(home, "workspaces", "alpha"))
	a := targetRunArgs()
	a["target_workspace"] = "beta"
	res := tool.Execute(ctx, a)

	require.True(t, res.IsError)
	assert.Equal(t, 0, store.began, "unauthorized target must be refused BEFORE allocation")
	assert.Contains(t, res.ForLLM, "target_workspace")
	assert.Contains(t, res.ForLLM, "only Admin can target another workspace")
}

func TestEnvironmentSetupTarget_Admin_NonexistentWorkspace_Refused(t *testing.T) {
	home := t.TempDir()
	store := &targetRecordingStore{}
	tool := newTargetTestTool(home, true, store)

	ctx := newTargetTestContext("admin-home", filepath.Join(home, "workspaces", "admin-home"))
	a := targetRunArgs()
	a["target_workspace"] = "ghostws"
	res := tool.Execute(ctx, a)

	require.True(t, res.IsError)
	assert.Equal(t, 0, store.began, "nonexistent workspace must be refused BEFORE allocation")
	assert.Contains(t, res.ForLLM, "does not exist")
}

func TestEnvironmentSetupTarget_Admin_PathShapedTarget_NeverResolves(t *testing.T) {
	home := t.TempDir()
	store := &targetRecordingStore{}
	tool := newTargetTestTool(home, true, store)

	ctx := newTargetTestContext("admin-home", filepath.Join(home, "workspaces", "admin-home"))
	for _, bad := range []string{"/tmp/omnipus-evil", "../outside", "sub/dir", ".", ".."} {
		a := targetRunArgs()
		a["target_workspace"] = bad
		res := tool.Execute(ctx, a)
		require.True(t, res.IsError, "path-shaped target %q must be refused", bad)
		assert.Equal(t, 0, store.began, "path-shaped target %q must never reach allocation", bad)
		assert.Contains(t, res.ForLLM, "target_workspace", bad)
	}
}

func TestEnvironmentSetupTarget_Admin_FirstUse_WorkDirEnsured(t *testing.T) {
	// A freshly created workspace record has no work/ dir until a member turn
	// or mount creates one (WorkspaceCreateTool writes only the record), so
	// the Admin branch ensures it with the sanctioned facility. The bounded
	// first-use effect is a REAL filesystem assertion: the dir must exist
	// after resolution.
	home := t.TempDir()
	writeTargetTestWorkspaceRecord(t, home, "beta") // record exists, work/ absent
	store := &targetRecordingStore{}
	tool := newTargetTestTool(home, true, store)

	ctx := newTargetTestContext("admin-home", filepath.Join(home, "workspaces", "admin-home"))
	a := targetRunArgs()
	a["target_workspace"] = "beta"
	res := tool.Execute(ctx, a)

	require.Equal(t, 1, store.began, "Admin first-use must reach allocation; result: %s", res.ForLLM)
	want := workspace.WorkDir(home, "beta")
	assert.Equal(t, want, store.workspaceRoot)
	workStat, err := os.Stat(want)
	require.NoError(t, err, "Admin first-use must create the missing work dir")
	require.True(t, workStat.IsDir())
}

func TestEnvironmentSetupTarget_Admin_RealStorage_VisibleToMemberLookup(t *testing.T) {
	// Recording-store equality proves resolution, not usability. This leg
	// allocates the resolved root through the REAL storage seam, writes an
	// installed artifact, and checks the member-side lookup
	// (environmentsetup.RuntimeEnvPaths) a beta member turn would perform —
	// the Admin install is where beta turns actually look. No process spawn:
	// the tool's session start stays in the lifecycle lane's coverage.
	home := t.TempDir()
	writeTargetTestWorkspaceRecord(t, home, "beta")
	store := &targetRecordingStore{}
	tool := newTargetTestTool(home, true, store)

	ctx := newTargetTestContext("admin-home", filepath.Join(home, "workspaces", "admin-home"))
	a := targetRunArgs()
	a["target_workspace"] = "beta"
	res := tool.Execute(ctx, a)
	require.Equal(t, 1, store.began, "resolution must pass; result: %s", res.ForLLM)

	// Real storage allocates the area the tool resolved.
	area, err := storageSeamStore{}.BeginInstall(home, store.workspaceRoot, "workspace")
	require.NoError(t, err, "real storage must accept the resolved work dir")
	t.Cleanup(func() { _ = area.Abort() })

	// An "installed" artifact lands in the prefix...
	require.NoError(t, os.MkdirAll(filepath.Join(area.Prefix(), "bin"), 0o755))
	artifact := filepath.Join(area.Prefix(), "bin", "probe")
	require.NoError(t, os.WriteFile(artifact, []byte("admin-first-use-ok"), 0o755))

	// ...and the member lookup sees it: a beta member turn's RuntimeEnvPaths
	// with the SAME workspace root the loop would root them at.
	env, err := environmentsetup.RuntimeEnvPaths(home, workspace.WorkDir(home, "beta"))
	require.NoError(t, err)
	assert.Equal(t, area.Prefix(), env.WorkspacePrefix,
		"member lookup must see the Admin-installed env prefix")
	data, readErr := os.ReadFile(filepath.Join(env.WorkspacePrefix, "bin", "probe"))
	require.NoError(t, readErr, "member turn must see the Admin-installed artifact")
	assert.Equal(t, "admin-first-use-ok", string(data))
}

func TestEnvironmentSetupTarget_SharedScope_ExplicitTarget_Refused(t *testing.T) {
	home := t.TempDir()
	writeTargetTestWorkspaceRecord(t, home, "beta")
	store := &targetRecordingStore{}
	tool := newTargetTestTool(home, true, store)

	ctx := newTargetTestContext("admin-home", filepath.Join(home, "workspaces", "admin-home"))
	a := targetRunArgs()
	a["scope"] = "shared"
	a["target_workspace"] = "beta"
	res := tool.Execute(ctx, a)

	require.True(t, res.IsError)
	assert.Equal(t, 0, store.began, "shared scope with an explicit target must be refused BEFORE allocation")
	assert.Contains(t, res.ForLLM, "target_workspace")
}
