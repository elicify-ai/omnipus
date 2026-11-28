package gitevidence

// H55 v2 (#1960 S1), finding F6 of the second security review
// (/workspace/logs/1960/security-s1-3/h55-review-2.md): a global core.hooksPath is
// not outranked by the receive pins, so a hook from an agent-writable folder ran
// inside the evidence repository on a push to a plain remote name (the review's hook
// created refs/heads/hooked there and left a marker file). The review's fix is one
// more local pin; "With core.hooksPath also pinned locally the hook did not run".
// The row below states what the pin is for, with real git: a push into the evidence
// repository runs no hook from the global folder and creates no ref of its own.
//
// Oracles: the review's F6 paragraph and its reproduction (global core.hooksPath,
// a post-receive hook, a push of HEAD to refs/heads/x); FR-152 (the evidence
// repository stays as the engine left it). The row does not ask for any particular
// pin value, only for the behaviour. The hooks are POSIX sh scripts that git runs
// through its own shell, so the row carries no build constraint (the backend's own
// receivepins_hooks_test.go dropped its Windows skip the same way, round 11). A control
// pushes the same commit into an ordinary clone under the same global config and the
// hooks must run there, so "no hook ran" cannot come from broken hooks, a global
// setting git ignores or a push that never reached a repository.
import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// h55v2Hooks writes receive-side hooks into a folder outside every repository. Each
// leaves a line in the file named by $H55V2_MARKER (hook name and working directory,
// which for a push is the receiving repository's git directory); post-receive also
// creates refs/heads/hooked in the repository that runs it.
func h55v2Hooks(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "hooks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pre-receive", "update", "post-receive", "post-update"} {
		body := "#!/bin/sh\ncat >/dev/null 2>&1\necho \"" + name + " $(pwd)\" >> \"$H55V2_MARKER\"\n"
		if name == "post-receive" {
			body += "git update-ref refs/heads/hooked HEAD </dev/null 2>/dev/null\n"
		}
		body += "exit 0\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// F6: with a global core.hooksPath that names a folder of agent-written hooks, a push
// into the evidence repository must not run any of them.
func TestGitEvidence_H55V2_F6_GlobalHooksPathHookDoesNotRunInTheEvidenceRepository(t *testing.T) {
	git := h55v2Git(t)
	repo, dir := newTestRepo(t)
	writeFile(t, dir, "h55v2-evidence.txt", "H55 v2 evidence\n")
	res, err := repo.Commit(BoundaryTask, CommitMeta{TaskID: "h55v2-f6", AgentID: "dev"}, []string{"h55v2-evidence.txt"})
	if err != nil || res.Skipped {
		t.Fatalf("fixture: engine Commit failed or was skipped (err=%v)", err)
	}
	head := res.Hash

	home := t.TempDir()
	if out, code := h55v2Run(t, git, "", home, nil, "config", "--global", "core.hooksPath", h55v2Hooks(t)); code != 0 {
		t.Fatalf("fixture: global core.hooksPath: exit %d: %s", code, out)
	}
	clone := func(name string) string {
		target := filepath.Join(t.TempDir(), name)
		if out, code := h55v2Run(t, git, "", home, nil, "clone", "-q", dir, target); code != 0 {
			t.Fatalf("fixture: clone %s: exit %d: %s", name, code, out)
		}
		return target
	}
	sender, control := clone("sender"), clone("control")
	push := func(target, marker string) (string, int) {
		return h55v2Run(t, git, sender, home, []string{"H55V2_MARKER=" + marker}, "push", target, "HEAD:refs/heads/x")
	}

	// Control: the hooks run in an ordinary repository and change it.
	controlMarker := filepath.Join(t.TempDir(), "control-ran")
	if out, code := push(control, controlMarker); code != 0 {
		t.Fatalf("instrument: the control push of a new ref must be received: exit %d: %s", code, out)
	}
	ran, readErr := os.ReadFile(controlMarker)
	if readErr != nil || !strings.Contains(string(ran), "post-receive") {
		t.Fatalf("instrument: the global hooks must run in an ordinary repository, or no-hook below proves nothing (marker=%q err=%v)", ran, readErr)
	}
	if out, code := h55v2Run(t, git, control, home, nil, "rev-parse", "--verify", "-q", "refs/heads/hooked"); code != 0 {
		t.Fatalf("instrument: the control's post-receive hook must have created refs/heads/hooked: exit %d: %s", code, out)
	}

	// The evidence repository: no hook runs, no ref appears, HEAD stays. Receiving a
	// new, not checked-out ref is still possible (the review's F7, parked by the
	// ruling); the row is about what runs while git receives it.
	marker := filepath.Join(t.TempDir(), "evidence-ran")
	if out, code := push(dir, marker); code != 0 {
		t.Fatalf("instrument: the push of a new ref into the evidence repository must be received, or no hook could have run: exit %d: %s", code, out)
	}
	if out, code := h55v2Run(t, git, dir, home, nil, "rev-parse", "--verify", "-q", "refs/heads/x"); code != 0 || out != head {
		t.Fatalf("instrument: refs/heads/x must exist in the evidence repository at %s after the push, got %q (exit %d)", head, out, code)
	}
	if body, readErr := os.ReadFile(marker); readErr == nil {
		t.Errorf("h55-review-2 F6: a hook from the global core.hooksPath ran in the evidence repository:\n%s", body)
	}
	if out, code := h55v2Run(t, git, dir, home, nil, "rev-parse", "--verify", "-q", "refs/heads/hooked"); code == 0 {
		t.Errorf("h55-review-2 F6: a hook created refs/heads/hooked (%s) in the evidence repository", out)
	}
	if out, code := h55v2Run(t, git, dir, home, nil, "rev-parse", "HEAD"); code != 0 || out != head {
		t.Errorf("FR-152: the evidence HEAD must be unchanged at %s, got %q (exit %d)", head, out, code)
	}
}
