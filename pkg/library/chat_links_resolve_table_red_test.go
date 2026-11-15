package library

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// BS-4.4 rows other than the real-folder shadow and the missing work directory,
// which have their own tests. A real folder named like a mount is outside
// (C2 B3), not "declared mount wins".
func TestResolveHostPath_SpecRows(t *testing.T) {
	home := realTempDir(t)
	work := mustWork(t, home, "W")
	mustWrite(t, filepath.Join(work, "a", "b.md"), "b")
	if err := os.MkdirAll(filepath.Join(work, "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	parent := realTempDir(t)
	target := filepath.Join(parent, "mount-host")
	sibling := filepath.Join(parent, "sibling")
	mustWrite(t, filepath.Join(target, "c.md"), "c")
	mustWrite(t, filepath.Join(sibling, "side.md"), "s")
	if err := os.Symlink(target, filepath.Join(work, "docs")); err != nil {
		t.Fatal(err)
	}
	if err := writeMount(t, home, "W", "docs", target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(sibling, "side.md"), filepath.Join(work, "rogue")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(home, "agents", "x", "mem.md"), "m")
	mustWrite(t, filepath.Join(home, "agents", "omnipus", "notes.md"), "n")
	outside := realTempDir(t)
	mustWrite(t, filepath.Join(outside, "x.txt"), "x")
	if err := os.Symlink(filepath.Join(outside, "x.txt"), filepath.Join(work, "leak")); err != nil {
		t.Fatal(err)
	}
	linkIn := filepath.Join(outside, "into-w")
	if err := os.Symlink(filepath.Join(work, "a", "b.md"), linkIn); err != nil {
		t.Fatal(err)
	}
	w2 := mustWork(t, home, "W2")
	mustWrite(t, filepath.Join(w2, "setup", "x.md"), "s")
	if err := os.MkdirAll(filepath.Join(home, "workspaces"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "workspaces", "W2.json"), []byte(`{"id":"W2"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		path string
		kind HostRefKind
		ws   string
		rel  string
		all  bool
	}{
		{"workspace file", filepath.Join(work, "a", "b.md"), HostRefWorkspace, "W", "a/b.md", false},
		{"absent file under an existing parent", filepath.Join(work, "new", "not-yet.md"), HostRefWorkspace, "W", "new/not-yet.md", false},
		{"mount host file", filepath.Join(target, "c.md"), HostRefMount, "W", "docs/c.md", false},
		{"in-tree path through the mount symlink", filepath.Join(work, "docs", "c.md"), HostRefMount, "W", "docs/c.md", false},
		{"agent home", filepath.Join(home, "agents", "x", "mem.md"), HostRefOutside, "", "", false},
		{"relative", "a.md", HostRefOutside, "", "", false},
		{"NUL", filepath.Join(work, "a\x00b.md"), HostRefOutside, "", "", false},
		{"CR", filepath.Join(work, "a\rb.md"), HostRefOutside, "", "", false},
		{"undeclared symlink out", filepath.Join(work, "leak"), HostRefOutside, "", "", false},
		{"rogue symlink beside a mount", filepath.Join(work, "rogue"), HostRefOutside, "", "", false},
		{"symlink outside pointing in", linkIn, HostRefWorkspace, "W", "a/b.md", false},
		{"mia cannot link another workspace", filepath.Join(w2, "setup", "x.md"), HostRefOutside, "", "", false},
		{"omnipus links another workspace", filepath.Join(w2, "setup", "x.md"), HostRefWorkspace, "W2", "setup/x.md", true},
		{"omnipus agent home stays outside", filepath.Join(home, "agents", "omnipus", "notes.md"), HostRefOutside, "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveHostPath(ResolveScope{Home: home, WorkspaceID: "W", AllWorkspaces: tc.all}, tc.path)
			if got.Kind != tc.kind || got.WorkspaceID != tc.ws || got.Rel != tc.rel {
				t.Fatalf("got %+v, want kind %v ws %q rel %q (BS-4.4)", got, tc.kind, tc.ws, tc.rel)
			}
		})
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Run("case-folded rel", func(t *testing.T) {
			mustWrite(t, filepath.Join(work, "Plan.md"), "p")
			got := ResolveHostPath(ResolveScope{Home: home, WorkspaceID: "W"}, filepath.Join(work, "plan.md"))
			if got.Kind != HostRefWorkspace || got.WorkspaceID != "W" || got.Rel != "Plan.md" {
				t.Fatalf("case-folded rel = %+v, want workspace Plan.md (BS-4.4)", got)
			}
		})
	}
}

// Windows drive and UNC spellings are classified per OS. On other systems they
// are not absolute paths, so they are outside. A live UNC lookup is not used:
// it can block on the network.
func TestResolveHostPath_DriveAndUNCForms(t *testing.T) {
	home := realTempDir(t)
	work := mustWork(t, home, "W")
	scope := ResolveScope{Home: home, WorkspaceID: "W"}
	if runtime.GOOS != "windows" {
		for _, p := range []string{`C:\W\a.md`, `\\server\share\a.md`} {
			got := ResolveHostPath(scope, p)
			if got.Kind != HostRefOutside {
				t.Fatalf("%q on %s = %+v, want outside (BS-4.4)", p, runtime.GOOS, got)
			}
		}
		return
	}
	other := filepath.Join(filepath.VolumeName(work)+string(filepath.Separator), "no-such-chat-links", "a.md")
	got := ResolveHostPath(scope, other)
	if got.Kind != HostRefOutside {
		t.Fatalf("drive path outside the Library %q = %+v, want outside (BS-4.4)", other, got)
	}
}

func mustWork(t *testing.T, home, id string) string {
	t.Helper()
	work, err := workspace.SafeWorkDir(home, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	return work
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
