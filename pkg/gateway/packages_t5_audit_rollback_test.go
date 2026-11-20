// T-5 rollback oracles: spec revision 7, C1/C2, AD-2/AD-3 and D7 rows 6/13/14/18.
package gateway

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

type t5RollbackSnapshot struct {
	entries    []string
	agentIDs   []string
	agentFiles map[string]string
	workspaces map[string]map[string]any
	edges      map[string][]workspace.DelegationEdge
	config     []byte
}

func t5AgentEntries(t *testing.T, home string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, "entities", "agents"))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}
	}
	require.NoError(t, err, "absence oracle must not swallow a read error")
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		require.False(t, entry.IsDir(), "unexpected agent entity directory: %s", entry.Name())
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

func t5RequireAbsent(t *testing.T, path string) {
	t.Helper()
	_, err := os.Lstat(path)
	require.ErrorIs(t, err, os.ErrNotExist, "path must be absent, not unreadable or a different file type: %s", path)
}

func t5RollbackAgentIDs(t *testing.T, home string) []string {
	t.Helper()
	agents, skipped, err := agentstore.New(home).List()
	require.NoError(t, err)
	require.Empty(t, skipped, "a corrupt leftover must not look like an absent agent")
	ids := make([]string, 0, len(agents))
	for _, agent := range agents {
		ids = append(ids, agent.ID)
	}
	sort.Strings(ids)
	return ids
}

func t5RollbackWorkspace(t *testing.T, path string) map[string]any {
	t.Helper()
	var record map[string]any
	require.NoError(t, json.Unmarshal(fileBytes(t, path), &record))
	// AD-3 permits only the workspace timestamp/revision to change.
	delete(record, "updated_at")
	delete(record, "revision")
	return record
}

func t5CaptureRollback(t *testing.T, home string) t5RollbackSnapshot {
	t.Helper()
	snap := t5RollbackSnapshot{
		entries: t5AgentEntries(t, home), agentIDs: t5RollbackAgentIDs(t, home),
		agentFiles: map[string]string{}, workspaces: map[string]map[string]any{},
		edges: map[string][]workspace.DelegationEdge{}, config: configBytes(t, home),
	}
	for path, value := range snapshotHome(t, home) {
		if strings.HasPrefix(path, filepath.Join("entities", "agents")+string(os.PathSeparator)) ||
			strings.HasPrefix(path, "agents"+string(os.PathSeparator)) {
			snap.agentFiles[path] = value
		}
	}
	entries, err := os.ReadDir(filepath.Join(home, "workspaces"))
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		snap.workspaces[id] = t5RollbackWorkspace(t, filepath.Join(home, "workspaces", entry.Name()))
		edges, ok := workspace.LoadDelegation(home, id)
		require.True(t, ok, "read baseline delegation for %s", id)
		snap.edges[id] = edges
	}
	require.NotEmpty(t, snap.workspaces, "workspace rollback oracle needs a positive control")
	return snap
}

func t5NewRollbackID(t *testing.T, home string, before []string) string {
	t.Helper()
	existing := map[string]bool{}
	for _, name := range before {
		existing[name] = true
	}
	var ids []string
	for _, name := range t5AgentEntries(t, home) {
		if !existing[name] && strings.HasSuffix(name, ".lock") {
			ids = append(ids, strings.TrimSuffix(name, ".lock"))
		}
	}
	require.Len(t, ids, 1, "AD-2: exactly one new permanent entity lock identifies X")
	require.NotEmpty(t, ids[0])
	require.NotEqual(t, specSourceID, ids[0], "import allocates a new ID")
	return ids[0]
}

func t5RequireRollbackState(t *testing.T, home, ws, id, fault string, before t5RollbackSnapshot) {
	t.Helper()
	pending := fault == "D7-6 DiscardPending fails"
	hasHome := pending || fault == "D7-18 partial C2 leaves the folder"
	wantEntries := append(append([]string{}, before.entries...), id+".lock")
	if pending {
		wantEntries = append(wantEntries, id+".pending")
	}
	sort.Strings(wantEntries)
	require.Equal(t, wantEntries, t5AgentEntries(t, home), "only AD-2 and the specified incomplete C2 pending file may remain")
	t5RequireAbsent(t, filepath.Join(home, "entities", "agents", id+".json"))
	if pending {
		require.FileExists(t, filepath.Join(home, "entities", "agents", id+".pending"))
	} else {
		t5RequireAbsent(t, filepath.Join(home, "entities", "agents", id+".pending"))
	}
	if hasHome {
		require.DirExists(t, filepath.Join(home, "agents", id), "the selected C2 failure must really leave the home")
	} else {
		t5RequireAbsent(t, filepath.Join(home, "agents", id))
	}
	require.Equal(t, before.agentIDs, t5RollbackAgentIDs(t, home), "store ID set must be unchanged")
	require.Equal(t, before.config, configBytes(t, home), "FR-034: config.json must be byte-identical")
	afterFiles := snapshotHome(t, home)
	for path, value := range before.agentFiles {
		require.Equal(t, value, afterFiles[path], "pre-existing agent file changed: %s", path)
	}
	for wsID, record := range before.workspaces {
		actual := t5RollbackWorkspace(t, filepath.Join(home, "workspaces", wsID+".json"))
		if wsID == ws && fault == "D7-13 C1 membership removal fails and C2 still runs" {
			team, _ := record["core_team"].([]any)
			want := append(append([]any{}, team...), id)
			require.Equal(t, want, actual["core_team"], "D7-13 must retain the real membership after C1 write failure")
			// Compare every other workspace property without mutating the baseline.
			if original, present := record["core_team"]; present {
				actual["core_team"] = original
			} else {
				delete(actual, "core_team")
			}
		}
		require.Equal(t, record, actual, "workspace logical state changed: %s", wsID)
		edges, ok := workspace.LoadDelegation(home, wsID)
		require.True(t, ok, "delegation must remain readable: %s", wsID)
		if wsID == ws && fault == "D7-14 partial C1" {
			var other []workspace.DelegationEdge
			selfRows := 0
			for _, edge := range edges {
				if edge.FromAgent == id || edge.ToAgent == id {
					require.Equal(t, id, edge.FromAgent)
					require.Equal(t, id, edge.ToAgent)
					selfRows++
				} else {
					other = append(other, edge)
				}
			}
			require.Equal(t, 1, selfRows, "D7-14 seed must persist and C1's second save must really fail")
			require.ElementsMatch(t, before.edges[wsID], other, "pre-existing edges must survive partial C1")
		} else {
			require.ElementsMatch(t, before.edges[wsID], edges, "no workspace may acquire an unexpected X edge")
		}
	}
}

// t5RequireOtherFiles is rollback oracle 6. Bytes must match except AD-1..AD-4:
// a skill generation stamp, X's permanent entity lock, W's workspace record,
// the audit log, the gateway log, and the staging tree (journal included).
func t5RequireOtherFiles(t *testing.T, before, after map[string]string, id, ws string) {
	t.Helper()
	allowed := func(rel string) bool {
		switch rel {
		case filepath.Join("system", "audit.jsonl"),
			filepath.Join("logs", "gateway.log"),
			filepath.Join("logs", "gateway_panic.log"),
			filepath.Join("entities", "agents", id+".lock"),
			filepath.Join("workspaces", ws+".json"):
			return true
		}
		if strings.HasPrefix(rel, filepath.Join("backups", ".staging")+string(os.PathSeparator)) {
			return true
		}
		return strings.HasSuffix(rel, string(os.PathSeparator)+".omnipus-identity")
	}
	hash := func(value string) string {
		if cut := strings.LastIndex(value, "|"); cut >= 0 {
			return value[:cut]
		}
		return value
	}
	for rel, value := range before {
		if allowed(rel) {
			continue
		}
		got, ok := after[rel]
		require.True(t, ok, "file disappeared outside AD-1..AD-4: %s", rel)
		require.Equal(t, hash(value), hash(got), "oracle 6: file bytes changed: %s", rel)
	}
	for rel := range after {
		if allowed(rel) {
			continue
		}
		_, ok := before[rel]
		require.True(t, ok, "oracle 6: new file outside AD-1..AD-4: %s", rel)
	}
}
