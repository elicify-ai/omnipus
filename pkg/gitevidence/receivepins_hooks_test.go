package gitevidence

// Re-review F6 (h55-review-2.md): a global core.hooksPath must not make the
// receiving git run an agent-written hook inside the pinned evidence
// repository. Real git; the unpinned control shows the same hooks do run, so
// "no hook ran" cannot come from a broken instrument.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// globalHooks writes receive-side hooks into a folder outside the repo, sets
// that folder as the global core.hooksPath, and returns the marker file each
// hook touches. Each hook also tries to create refs/heads/hooked in the
// receiving repository.
//
// The hooks are "#!/bin/sh" scripts on every platform: git runs a hook through
// the shell it ships with (Git for Windows runs them with its bundled sh), so
// the same instrument works on Linux, macOS and Windows. The marker path is
// written with forward slashes, which that sh reads on Windows too. There is
// no platform skip: if a platform's git cannot run these hooks, the control
// row below fails loudly instead of the evidence row passing unseen.
func (f pushFixture) globalHooks(t *testing.T) string {
	t.Helper()
	hooks := filepath.Join(t.TempDir(), "hk")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "hook-ran")
	// Every receive-side hook leaves a marker; only post-receive also writes a
	// ref (a ref write from reference-transaction would re-enter that hook).
	// Each line names the hook and the repository it ran in (GIT_DIR is set
	// for hooks), so a failure shows which side of the push ran it.
	note := "#!/bin/sh\ncat >/dev/null 2>&1\necho \"$0 $1 in $(pwd) GIT_DIR=$GIT_DIR\" >> '" + filepath.ToSlash(marker) + "'\n"
	for name, body := range map[string]string{
		"pre-receive":           note + "exit 0\n",
		"update":                note + "exit 0\n",
		"post-update":           note + "exit 0\n",
		"reference-transaction": note + "exit 0\n",
		"post-receive":          note + "git update-ref refs/heads/hooked HEAD </dev/null 2>/dev/null\nexit 0\n",
	} {
		if err := os.WriteFile(filepath.Join(hooks, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustGit(t, f.mount, f.home, "config", "--global", "core.hooksPath", hooks)
	return marker
}

// hookRunsIn returns the marker lines of hooks that ran inside repoDir (its
// working tree or its .git). The pushing repository also runs hooks of its
// own (git 2.56 runs reference-transaction on the sending side of a push), so
// a line is only evidence about repoDir when the hook's directory is there.
func hookRunsIn(t *testing.T, marker, repoDir string) []string {
	t.Helper()
	data, err := os.ReadFile(marker)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read hook marker: %v", err)
	}
	roots := []string{filepath.ToSlash(repoDir)}
	if resolved, rerr := filepath.EvalSymlinks(repoDir); rerr == nil {
		roots = append(roots, filepath.ToSlash(resolved))
	}
	var runs []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		_, dir, ok := strings.Cut(line, " in ")
		if !ok {
			t.Fatalf("hook marker line without its directory: %q", line)
		}
		dir, _, _ = strings.Cut(dir, " GIT_DIR=")
		for _, root := range roots {
			if dir == root || strings.HasPrefix(dir, root+"/") {
				runs = append(runs, line)
				break
			}
		}
	}
	return runs
}

func TestGitEvidence_ReceivePins_ControlGlobalHooksRunInAnUnpinnedRepo(t *testing.T) {
	f := newPushFixture(t, false)
	marker := f.globalHooks(t)
	if out, err := realGit(t, f.mount, f.home, nil, "push", f.work, "HEAD:refs/heads/x"); err != nil {
		t.Fatalf("control push: %v: %s", err, out)
	}
	runs := hookRunsIn(t, marker, f.work)
	received := false
	for _, line := range runs {
		received = received || strings.Contains(line, "/pre-receive ")
	}
	if !received {
		data, _ := os.ReadFile(marker)
		t.Fatalf("control: the global pre-receive hook should have run in the unpinned receiving repo; marker: %q", data)
	}
}

func TestGitEvidence_ReceivePins_GlobalHooksPathDoesNotRunInTheEvidenceRepo(t *testing.T) {
	f := newPushFixture(t, true)
	marker := f.globalHooks(t)
	// Adding a new, not checked-out ref is still possible (ruling: parked); the
	// point is that no hook runs while git receives it.
	out, err := realGit(t, f.mount, f.home, nil, "push", f.work, "HEAD:refs/heads/x")
	if err != nil {
		t.Fatalf("push of a new ref: %v: %s", err, out)
	}
	if runs := hookRunsIn(t, marker, f.work); len(runs) > 0 {
		t.Fatalf("F6: a hook from the global core.hooksPath ran in the evidence repo: %q", runs)
	}
	if _, err := realGit(t, f.work, f.home, nil, "rev-parse", "--verify", "-q", "refs/heads/hooked"); err == nil {
		t.Fatalf("F6: a hook created refs/heads/hooked in the evidence repo")
	}
	if f.workHead(t) != f.head {
		t.Fatalf("FR-152: HEAD must be unchanged")
	}
}

func TestGitEvidence_ReceivePins_PlantedHookInThePinnedFolderIsCleared(t *testing.T) {
	_, dir := newTestRepo(t)
	planted := filepath.Join(dir, ".git", noHooksDirName)
	if err := os.MkdirAll(planted, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planted, "post-receive"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, WithRedactor(noOpRedactor)); err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	if _, err := os.Stat(planted); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the pinned hooks folder must be removed on Open, stat err = %v", err)
	}
	assertPins(t, dir, "after re-Open with a planted hook")
}
