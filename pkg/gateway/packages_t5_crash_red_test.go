// packages_t5_crash_red_test.go — RED regressions for crash seams, recovery
// cleanup and the apply audit (epic #1094, S1a T-5, spec revision 7).
//
// Expected values are the spec, not the current apply output. These fail while
// the crash steps are missing, recovery keeps the staging folder, or the apply
// audit uses different labels.
package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

// TestPackages_CrashSeamsThenRecovery is D7-23a and D7-23c..23e.
// "membership" is the compensating fault before the join (D7-2/6/18) and is
// not reused here. after-membership, self-row and rename are the steps the
// seam comment names as stopping without compensation; only E1 is wired today.
func TestPackages_CrashSeamsThenRecovery(t *testing.T) {
	t.Run("D7-23a crash after E1 rolls back and deletes staging", func(t *testing.T) {
		api, got := crashApply(t, "E1")
		requireCrashStopped(t, got, "E1")
		dir, journal := t5OnlyJournal(t, api.homePath)
		require.Equal(t, omnpkg.JournalStarted, journal.State, "E1 leaves the journal started")
		require.NoError(t, api.recoverImportsBeforeRoster(packageCtx(t)))
		t5RequireAbsent(t, dir)
		require.Contains(t, readAudit(t, api.homePath), omnpkg.RecoveryRolledBackMessage)
	})

	t.Run("D7-23c crash after membership does not compensate", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		teamBefore := workspaceTeam(t, api.homePath, ws)
		edgesBefore := t5Edges(t, api.homePath, ws)
		packageT5.CrashAt = "after-membership"
		prev := stageUpload(t, api, validMainPackage(t))
		got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
		requireCrashStopped(t, got, "after-membership")
		_, journal := t5OnlyJournal(t, api.homePath)
		require.Equal(t, omnpkg.JournalStarted, journal.State)
		id := journal.AgentID
		require.NotEmpty(t, id)
		require.FileExists(t, filepath.Join(api.homePath, "entities", "agents", id+".pending"), "no compensation: the pending record stays")
		t5RequireAbsent(t, filepath.Join(api.homePath, "entities", "agents", id+".json"))
		require.Contains(t, workspaceTeam(t, api.homePath, ws), id, "membership was written")
		require.Zero(t, t5SelfRowCount(t, api.homePath, ws, id), "the self-row is after this crash point")
		require.NoError(t, api.recoverImportsBeforeRoster(packageCtx(t)))
		require.Equal(t, teamBefore, workspaceTeam(t, api.homePath, ws), "D7-23c: recovery rolls W back")
		require.ElementsMatch(t, edgesBefore, t5Edges(t, api.homePath, ws))
		t5RequireAbsent(t, filepath.Join(api.homePath, "entities", "agents", id+".pending"))
		t5RequireAbsent(t, filepath.Join(api.homePath, "entities", "agents", id+".json"))
		t5RequireAbsent(t, filepath.Join(api.homePath, "agents", id))
		t5RequireAbsent(t, filepath.Join(api.homePath, "backups", ".staging", prev.StagingId))
		require.Contains(t, readAudit(t, api.homePath), omnpkg.RecoveryRolledBackMessage)
	})

	t.Run("D7-23d crash after the self-row does not compensate", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		teamBefore := workspaceTeam(t, api.homePath, ws)
		packageT5.CrashAt = "self-row"
		prev := stageUpload(t, api, validMainPackage(t))
		got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
		requireCrashStopped(t, got, "self-row")
		_, journal := t5OnlyJournal(t, api.homePath)
		require.Equal(t, omnpkg.JournalStarted, journal.State)
		id := journal.AgentID
		require.FileExists(t, filepath.Join(api.homePath, "entities", "agents", id+".pending"))
		t5RequireAbsent(t, filepath.Join(api.homePath, "entities", "agents", id+".json"))
		require.Contains(t, workspaceTeam(t, api.homePath, ws), id)
		require.Equal(t, 1, t5SelfRowCount(t, api.homePath, ws, id), "the self-row was written")
		require.NoError(t, api.recoverImportsBeforeRoster(packageCtx(t)))
		require.Equal(t, teamBefore, workspaceTeam(t, api.homePath, ws), "D7-23d: recovery rolls W back")
		require.Zero(t, t5SelfRowCount(t, api.homePath, ws, id))
		t5RequireAbsent(t, filepath.Join(api.homePath, "entities", "agents", id+".pending"))
		t5RequireAbsent(t, filepath.Join(api.homePath, "agents", id))
		t5RequireAbsent(t, filepath.Join(api.homePath, "backups", ".staging", prev.StagingId))
	})

	t.Run("D7-23e crash after rename is finished at start-up", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		frames := agentCreatedRecorder(api)
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		packageT5.CrashAt = "rename"
		prev := stageUpload(t, api, validMainPackage(t))
		got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
		requireCrashStopped(t, got, "rename")
		require.Empty(t, frames(), "a crash before P1 sends no agent_created")
		_, journal := t5OnlyJournal(t, api.homePath)
		require.Equal(t, omnpkg.JournalStarted, journal.State, "journal committed is after the rename crash")
		id := journal.AgentID
		require.FileExists(t, filepath.Join(api.homePath, "entities", "agents", id+".json"), "the rename committed the record")
		t5RequireAbsent(t, filepath.Join(api.homePath, "entities", "agents", id+".pending"))
		require.Contains(t, workspaceTeam(t, api.homePath, ws), id)
		require.Equal(t, 1, t5SelfRowCount(t, api.homePath, ws, id))
		require.NoError(t, api.recoverImportsBeforeRoster(packageCtx(t)))
		require.FileExists(t, filepath.Join(api.homePath, "entities", "agents", id+".json"))
		require.Contains(t, workspaceTeam(t, api.homePath, ws), id, "D7-23e: a committed import stays on the team")
		require.Equal(t, 1, t5SelfRowCount(t, api.homePath, ws, id))
		require.Contains(t, readAudit(t, api.homePath), omnpkg.RecoveryFinishedMessage)
		require.Empty(t, frames(), "start-up recovery sends no agent_created")
		t5RequireAbsent(t, filepath.Join(api.homePath, "backups", ".staging", prev.StagingId))
	})
}

// TestPackages_ApplyAuditMatchesSpec is the confirmed-import audit (BDD
// "Confirmed import creates the agent"): exactly one entry, operation import,
// outcome success, the actor, the new id and the package SHA-256.
func TestPackages_ApplyAuditMatchesSpec(t *testing.T) {
	api := packageAPI(t)
	usePackageSeams(t)
	packageT5.Reload = func() (bool, error) { return true, nil }
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	pkg := validMainPackage(t)
	sum := sha256hex(pkg)
	prev := stageUpload(t, api, pkg)
	got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
	require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
	res := decodeResult(t, got.Body)
	require.Len(t, res.Created, 1)
	entries := readPackageAuditEntries(t, api.homePath)
	imports := make([]audit.Entry, 0, 1)
	seen := make([]string, 0, len(entries))
	for _, entry := range entries {
		op, _ := entry.Details["operation"].(string)
		seen = append(seen, op)
		if op == "import" {
			imports = append(imports, entry)
		}
	}
	require.Len(t, imports, 1, "exactly one operation=import entry; operations=%v", seen)
	entry := imports[0]
	require.Equal(t, "success", entry.Details["outcome"], "outcome must be the spec label success")
	require.Equal(t, "_env_token", entry.User, "the actor is the authenticated env-token principal")
	require.Equal(t, res.Created[0].NewId, entry.AgentID)
	require.Contains(t, auditDetailValues(entry.Details), sum, "the entry must carry the package SHA-256")
}

func crashApply(t *testing.T, step string) (*restAPI, packageHTTP) {
	t.Helper()
	api := packageAPI(t)
	usePackageSeams(t)
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	packageT5.CrashAt = step
	prev := stageUpload(t, api, validMainPackage(t))
	return api, callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
}

func requireCrashStopped(t *testing.T, got packageHTTP, step string) {
	t.Helper()
	require.Equal(t, http.StatusInternalServerError, got.Code, "crash seam %s must stop the apply; body=%s", step, got.Body)
	require.Contains(t, string(got.Body), errPackageCrashSeam.Error())
	require.Contains(t, string(got.Body), step)
	require.NotContains(t, string(got.Body), omnpkg.CodeImportRolledBack, "a crash does not run compensation")
	require.NotContains(t, string(got.Body), omnpkg.CodeImportRollbackIncomplete)
}

func t5OnlyJournal(t *testing.T, home string) (string, omnpkg.JournalFile) {
	t.Helper()
	base := filepath.Join(home, "backups", ".staging")
	entries, err := os.ReadDir(base)
	require.NoError(t, err)
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}
	require.Len(t, dirs, 1, "the crashed apply leaves exactly one staging folder, got %v", dirs)
	dir := filepath.Join(base, dirs[0])
	journal, err := omnpkg.ReadJournal(dir)
	require.NoError(t, err)
	return dir, journal
}

func t5Edges(t *testing.T, home, ws string) []workspace.DelegationEdge {
	t.Helper()
	edges, ok := workspace.LoadDelegation(home, ws)
	require.True(t, ok, "delegation store must be readable")
	return edges
}

func t5SelfRowCount(t *testing.T, home, ws, id string) int {
	t.Helper()
	n := 0
	for _, edge := range t5Edges(t, home, ws) {
		if edge.FromAgent == id && edge.ToAgent == id {
			n++
		}
	}
	return n
}

func readPackageAuditEntries(t *testing.T, home string) []audit.Entry {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "system", "audit.jsonl"))
	require.NoError(t, err)
	lines := splitAuditLines(raw)
	entries := make([]audit.Entry, 0, len(lines))
	for _, line := range lines {
		var entry audit.Entry
		require.NoError(t, json.Unmarshal(line, &entry), "audit line=%s", line)
		entries = append(entries, entry)
	}
	return entries
}

func splitAuditLines(raw []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range raw {
		if b == '\n' {
			if i > start {
				lines = append(lines, raw[start:i])
			}
			start = i + 1
		}
	}
	if start < len(raw) {
		lines = append(lines, raw[start:])
	}
	return lines
}

func auditDetailValues(details map[string]any) []string {
	out := make([]string, 0, len(details))
	for _, value := range details {
		if text, ok := value.(string); ok {
			out = append(out, text)
		}
	}
	return out
}
