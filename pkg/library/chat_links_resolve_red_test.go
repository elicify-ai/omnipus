// RED rows for the resolver (BS-4.4, C2 B3). A real work-tree folder that
// carries a declared mount's name is outside, and the turn's work directory
// is never a stand-in for the Library when the workspace work directory is
// missing.
package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

func TestResolveHostPath_MountNameShadowsRealFolder(t *testing.T) {
	home := realTempDir(t)
	work, err := workspace.SafeWorkDir(home, "W")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(work, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(work, "docs", "x.md")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := realTempDir(t)
	if err := writeMount(t, home, "W", "docs", target); err != nil {
		t.Fatal(err)
	}

	ref := ResolveHostPath(ResolveScope{Home: home, WorkspaceID: "W"}, file)
	if ref.InLibrary() || ref.Kind != HostRefOutside {
		t.Fatalf("docs/x.md under a real folder named like mount docs = %+v, want outside and no link (BS-4.4 B3)", ref)
	}
}

func TestResolveHostPath_TurnWorkDirIsNotTheLibrary(t *testing.T) {
	home := realTempDir(t)
	// The workspace work directory is never created. The file lives only in
	// the turn directory the loop would have fallen back to.
	turn := realTempDir(t)
	if err := os.MkdirAll(filepath.Join(turn, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(turn, "notes", "plan.md")
	if err := os.WriteFile(file, []byte("plan"), 0o644); err != nil {
		t.Fatal(err)
	}

	// TurnWorkDir is not a resolver input. A file that is not under the
	// workspace work directory is outside even when a turn directory holds it.
	ref := ResolveHostPath(ResolveScope{Home: home, WorkspaceID: "W"}, file)
	if ref.InLibrary() || ref.Kind != HostRefOutside {
		t.Fatalf("file outside the workspace work dir = %+v, want outside (BS-4.4 B3)", ref)
	}
}

func realTempDir(t *testing.T) string {
	t.Helper()
	raw := t.TempDir()
	real, err := filepath.EvalSymlinks(raw)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func writeMount(t *testing.T, home, id, name, hostPath string) error {
	t.Helper()
	realHost, err := filepath.EvalSymlinks(hostPath)
	if err != nil {
		return err
	}
	recordPath, err := workspace.MountStorePath(home, id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(recordPath), 0o755); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"workspace_id": id,
		"mounts": []map[string]string{{
			"name":      name,
			"host_path": filepath.Clean(realHost),
		}},
	})
	if err != nil {
		return err
	}
	return os.WriteFile(recordPath, body, 0o600)
}
