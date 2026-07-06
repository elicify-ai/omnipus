package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testWorkspaceTeamRecord is the minimal on-disk workspace shape this file's
// tests need (id, is_default, core_team, name, description) — a sibling of
// delegation_graph_testhelper_test.go's testWorkspaceRecord, which does not
// carry core_team/name/description.
type testWorkspaceTeamRecord struct {
	ID          string   `json:"id"`
	IsDefault   bool     `json:"is_default,omitempty"`
	CoreTeam    []string `json:"core_team,omitempty"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
}

// seedWorkspaceTeam points OMNIPUS_HOME at a fresh temp dir (auto-restored)
// and writes workspaces/<id>.json carrying the given core_team/name/
// description. Returns the home dir.
func seedWorkspaceTeam(t *testing.T, wsID string, isDefault bool, coreTeam []string, name, description string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	writeWorkspaceTeamRecord(t, home, wsID, isDefault, coreTeam, name, description)
	return home
}

// writeWorkspaceTeamRecord writes/overwrites workspaces/<id>.json under an
// existing home.
func writeWorkspaceTeamRecord(
	t *testing.T,
	home, wsID string,
	isDefault bool,
	coreTeam []string,
	name, description string,
) {
	t.Helper()
	wsDir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir workspaces: %v", err)
	}
	rec := testWorkspaceTeamRecord{
		ID: wsID, IsDefault: isDefault, CoreTeam: coreTeam, Name: name, Description: description,
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal workspace record: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, wsID+".json"), data, 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}
}

// TestWorkingDirWiring_CoreTeamMember_BlockPresent proves a CoreTeam member's
// dynamic context carries the "## Working Directory" block naming the
// workspace's title/description and its shared work directory.
func TestWorkingDirWiring_CoreTeamMember_BlockPresent(t *testing.T) {
	const wsID = "01JWWORKDIRTEST0000000001"
	seedWorkspaceTeam(t, wsID, true, []string{"ava"}, "Launch Prep", "Everything for the Q3 launch")

	const agentID = "ava"
	al, cb := wireTestLoopWithGraph(t, agentID)
	_ = al

	got := cb.buildDynamicContext("", "", "", "", "")
	if !strings.Contains(got, "## Working Directory") {
		t.Fatalf("Working Directory block missing.\ngot: %s", got)
	}
	if !strings.Contains(got, "Launch Prep") {
		t.Errorf("expected workspace name %q in block.\ngot: %s", "Launch Prep", got)
	}
	if !strings.Contains(got, "Everything for the Q3 launch") {
		t.Errorf("expected workspace description in block.\ngot: %s", got)
	}
	if !strings.Contains(got, filepath.Join("workspaces", wsID, "work")) {
		t.Errorf("expected the resolved work dir path in block.\ngot: %s", got)
	}
	if !strings.Contains(got, "agents/ava/") {
		t.Errorf("expected an explicit warning against the private agents/ava/ dir.\ngot: %s", got)
	}
}

// TestWorkingDirWiring_NotCoreTeamMember_BlockAbsent proves an agent that is
// NOT a member of any workspace's core_team gets no Working Directory block
// at all — its file tools use its own private directory, exactly as before
// this feature existed, with nothing new to tell it.
func TestWorkingDirWiring_NotCoreTeamMember_BlockAbsent(t *testing.T) {
	const wsID = "01JWWORKDIRTEST0000000002"
	// "ava" is on the team; "jim" (the agent under test) is deliberately not.
	seedWorkspaceTeam(t, wsID, true, []string{"ava"}, "Some Workspace", "")

	const agentID = "jim"
	al, cb := wireTestLoopWithGraph(t, agentID)
	_ = al

	got := cb.buildDynamicContext("", "", "", "", "")
	if strings.Contains(got, "## Working Directory") {
		t.Fatalf("did not expect a Working Directory block for a non-CoreTeam agent.\ngot: %s", got)
	}
}

// TestWorkingDirWiring_PrefersCurrentSessionWorkspace proves that when an
// agent belongs to TWO workspaces' core teams, passing the current turn's
// workspaceID into buildDynamicContext selects THAT workspace's directory —
// not FindForAgent's arbitrary sorted-first pick.
func TestWorkingDirWiring_PrefersCurrentSessionWorkspace(t *testing.T) {
	// "ws-a-current" sorts before "ws-z-other", so a plain FindForAgent would
	// pick "ws-a-current" regardless — pick the OTHER one as the "current
	// session" workspace so the test only passes if the preference is real.
	const wsA = "ws-a-current"
	const wsZ = "ws-z-other"
	home := seedWorkspaceTeam(t, wsA, true, []string{"ray"}, "Workspace A", "")
	writeWorkspaceTeamRecord(t, home, wsZ, false, []string{"ray"}, "Workspace Z", "")

	al, cb := wireTestLoopWithGraph(t, "ray")
	_ = al

	got := cb.buildDynamicContext(wsZ, "", "", "", "")
	if !strings.Contains(got, "Workspace Z") {
		t.Errorf("expected the CURRENT session's workspace name %q in block.\ngot: %s", "Workspace Z", got)
	}
	if strings.Contains(got, "Workspace A") {
		t.Errorf("did not expect the OTHER (non-current) workspace's name in block.\ngot: %s", got)
	}
	if !strings.Contains(got, filepath.Join("workspaces", wsZ, "work")) {
		t.Errorf("expected the CURRENT session's work dir path in block.\ngot: %s", got)
	}
	if strings.Contains(got, filepath.Join("workspaces", wsA, "work")) {
		t.Errorf("did not expect the OTHER workspace's work dir path in block.\ngot: %s", got)
	}
}
