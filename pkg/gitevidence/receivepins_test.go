package gitevidence

// H55 v2 (ruling-H55-v2.md): the evidence repository pins its receive
// settings in its own local config on every Open and after every engine
// Commit, and real git then refuses the F1 bypass of the security review
// (h55-review.md): a global receive.denyCurrentBranch=ignore (and
// core.bare=true) set by the pushing side, by HOME, by GIT_CONFIG_GLOBAL or
// reached through url.<work>.insteadOf. The real-git rows use the installed
// git binary; the control row shows the same push moves HEAD of a repo
// without the pins, so a refusal cannot come from something else.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// realGit runs the installed git with a clean, explicit environment. home is
// the HOME the global config comes from; extra adds variables.
func realGit(t *testing.T, dir, home string, extra []string, args ...string) (string, error) {
	t.Helper()
	bin, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("these rows need the installed git binary: %v", err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0", "LC_ALL=C",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
	}, extra...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func mustGit(t *testing.T, dir, home string, args ...string) string {
	t.Helper()
	out, err := realGit(t, dir, home, nil, args...)
	if err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
	}
	return out
}

// localConfigValues reads every value of key from the repo's own config file
// with real git (an instrument independent of go-git).
func localConfigValues(t *testing.T, dir, key string) []string {
	t.Helper()
	out, err := realGit(t, dir, t.TempDir(), nil, "config", "--file", filepath.Join(dir, ".git", "config"), "--get-all", key)
	if err != nil {
		return nil
	}
	return strings.Split(out, "\n")
}

func assertPins(t *testing.T, dir, when string) {
	t.Helper()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"core.hookspath":              filepath.ToSlash(filepath.Join(abs, ".git", noHooksDirName)),
		"core.alternaterefscommand":   "true",
		"core.bare":                   "false",
		"receive.denycurrentbranch":   "refuse",
		"receive.denynonfastforwards": "true",
		"receive.denydeletes":         "true",
	}
	for key, value := range want {
		got := localConfigValues(t, dir, key)
		if len(got) != 1 || got[0] != value {
			t.Errorf("%s: local %s = %q, want exactly [%s]", when, key, got, value)
		}
	}
	if got := localConfigValues(t, dir, "include.path"); len(got) != 0 {
		t.Errorf("%s: local include.path must be gone, got %q", when, got)
	}
}

func TestGitEvidence_ReceivePins_WrittenOnOpenOfANewRepo(t *testing.T) {
	_, dir := newTestRepo(t)
	assertPins(t, dir, "after Open of a new repo")
}

func TestGitEvidence_ReceivePins_RestoredOnReopenAndAfterCommit(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))
	cfgPath := filepath.Join(dir, ".git", "config")
	home := t.TempDir()
	tamper := func() {
		t.Helper()
		mustGit(t, dir, home, "config", "--file", cfgPath, "--unset-all", "receive.denyNonFastForwards")
		mustGit(t, dir, home, "config", "--file", cfgPath, "receive.denyCurrentBranch", "ignore")
		mustGit(t, dir, home, "config", "--file", cfgPath, "--add", "receive.denyDeletes", "false")
		mustGit(t, dir, home, "config", "--file", cfgPath, "core.bare", "true")
		mustGit(t, dir, home, "config", "--file", cfgPath, "include.path", "/tmp/elsewhere")
		mustGit(t, dir, home, "config", "--file", cfgPath, "--unset-all", "core.hooksPath")
		mustGit(t, dir, home, "config", "--file", cfgPath, "core.alternateRefsCommand", "touch /tmp/x")
		mustGit(t, dir, home, "config", "--file", cfgPath, "--add", "core.hooksPath", "/tmp/hk")
		if got := localConfigValues(t, dir, "receive.denycurrentbranch"); len(got) != 1 || got[0] != "ignore" {
			t.Fatalf("instrument: the tamper did not take: %q", got)
		}
	}

	tamper()
	if _, err := Open(dir, WithRedactor(noOpRedactor)); err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	assertPins(t, dir, "after re-Open of a tampered repo")

	tamper()
	writeFile(t, dir, "a.txt", "evidence")
	res, err := r.Commit(BoundaryAttempt, CommitMeta{TaskID: "t1", AttemptID: "1", AgentID: "a"}, []string{"a.txt"})
	if err != nil || res.Skipped {
		t.Fatalf("Commit: err=%v skipped=%v", err, res != nil && res.Skipped)
	}
	assertPins(t, dir, "after an engine Commit")
}

func TestGitEvidence_ReceivePins_KeepOtherReceiveSettings(t *testing.T) {
	_, dir := newTestRepo(t)
	home := t.TempDir()
	cfgPath := filepath.Join(dir, ".git", "config")
	mustGit(t, dir, home, "config", "--file", cfgPath, "receive.fsckObjects", "true")
	mustGit(t, dir, home, "config", "--file", cfgPath, "receive.denyCurrentBranch", "warn")
	if _, err := Open(dir, WithRedactor(noOpRedactor)); err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	assertPins(t, dir, "after re-Open")
	if got := localConfigValues(t, dir, "receive.fsckobjects"); len(got) != 1 || got[0] != "true" {
		t.Errorf("an unrelated receive setting must be kept, got %q", got)
	}
}

// pushFixture: an evidence repo with one engine commit, and a clone (the
// "mount") with one more commit on top, so a push of it is a fast-forward and
// only the checked-out-branch rule can stop it.
type pushFixture struct {
	work, mount, home, head, branch string
}

func newPushFixture(t *testing.T, pinned bool) pushFixture {
	t.Helper()
	base := t.TempDir()
	work := filepath.Join(base, "work")
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if pinned {
		r, err := Open(work, WithRedactor(noOpRedactor), WithClock(fixedClock()))
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		writeFile(t, work, "a.txt", "evidence")
		if res, err := r.Commit(BoundaryAttempt, CommitMeta{TaskID: "t1", AttemptID: "1", AgentID: "a"}, []string{"a.txt"}); err != nil || res.Skipped {
			t.Fatalf("Commit: %v", err)
		}
	} else {
		mustGit(t, base, home, "init", "-q", work)
		writeFile(t, work, "a.txt", "evidence")
		mustGit(t, work, home, "add", "a.txt")
		mustGit(t, work, home, "commit", "-q", "-m", "evidence")
	}
	branch := mustGit(t, work, home, "symbolic-ref", "--short", "HEAD")
	head := mustGit(t, work, home, "rev-parse", "HEAD")
	mount := filepath.Join(base, "m")
	mustGit(t, base, home, "clone", "-q", work, mount)
	mustGit(t, mount, home, "commit", "-q", "--allow-empty", "-m", "forged descendant")
	return pushFixture{work: work, mount: mount, home: home, head: head, branch: branch}
}

func (f pushFixture) workHead(t *testing.T) string {
	t.Helper()
	return mustGit(t, f.work, f.home, "rev-parse", "HEAD")
}

func (f pushFixture) workRefs(t *testing.T) string {
	t.Helper()
	return mustGit(t, f.work, f.home, "for-each-ref", "--format=%(refname) %(objectname)")
}

// The review's three calls (F1): add the remote, set the global switch, push.
func (f pushFixture) threeCallBypass(t *testing.T) (string, error) {
	t.Helper()
	mustGit(t, f.mount, f.home, "remote", "add", "ev", f.work)
	mustGit(t, f.mount, f.home, "config", "--global", "receive.denyCurrentBranch", "ignore")
	mustGit(t, f.mount, f.home, "config", "--global", "core.bare", "true")
	return realGit(t, f.mount, f.home, nil, "push", "ev", "HEAD:"+f.branch)
}

// Control: without the pins the three calls really move HEAD, so the
// instrument can see the attack.
func TestGitEvidence_ReceivePins_ControlUnpinnedRepoIsMovedByTheBypass(t *testing.T) {
	f := newPushFixture(t, false)
	out, err := f.threeCallBypass(t)
	if err != nil {
		t.Fatalf("control: the unpinned push should succeed, got %v: %s", err, out)
	}
	if f.workHead(t) == f.head {
		t.Fatalf("control: HEAD of the unpinned repo should have moved: %s", out)
	}
}

func TestGitEvidence_ReceivePins_GitRefusesTheF1Bypass(t *testing.T) {
	t.Run("global_config_three_calls", func(t *testing.T) {
		f := newPushFixture(t, true)
		refsBefore := f.workRefs(t)
		out, err := f.threeCallBypass(t)
		if err == nil {
			t.Fatalf("git must refuse the push into the pinned evidence repo: %s", out)
		}
		if f.workHead(t) != f.head || f.workRefs(t) != refsBefore {
			t.Fatalf("FR-152: HEAD and refs must be unchanged after the refused push: %s", out)
		}
	})
	t.Run("GIT_CONFIG_GLOBAL_file", func(t *testing.T) {
		f := newPushFixture(t, true)
		refsBefore := f.workRefs(t)
		g := filepath.Join(t.TempDir(), "g")
		if err := os.WriteFile(g, []byte("[receive]\n\tdenyCurrentBranch = ignore\n[core]\n\tbare = true\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := realGit(t, f.mount, f.home, []string{"GIT_CONFIG_GLOBAL=" + g}, "push", f.work, "HEAD:"+f.branch)
		if err == nil {
			t.Fatalf("git must refuse the push into the pinned evidence repo: %s", out)
		}
		if f.workHead(t) != f.head || f.workRefs(t) != refsBefore {
			t.Fatalf("FR-152: HEAD and refs must be unchanged: %s", out)
		}
	})
	t.Run("global_insteadOf_alias", func(t *testing.T) {
		f := newPushFixture(t, true)
		refsBefore := f.workRefs(t)
		mustGit(t, f.mount, f.home, "config", "--global", "url."+f.work+".insteadOf", "zz")
		mustGit(t, f.mount, f.home, "config", "--global", "receive.denyCurrentBranch", "ignore")
		out, err := realGit(t, f.mount, f.home, nil, "push", "zz", "HEAD:"+f.branch)
		if err == nil {
			t.Fatalf("git must refuse the push into the pinned evidence repo: %s", out)
		}
		if f.workHead(t) != f.head || f.workRefs(t) != refsBefore {
			t.Fatalf("FR-152: HEAD and refs must be unchanged: %s", out)
		}
	})
	t.Run("delete_and_force_are_refused", func(t *testing.T) {
		f := newPushFixture(t, true)
		// Adding a new, not checked-out ref stays possible (the ruling parks it).
		mustGit(t, f.mount, f.home, "push", f.work, "HEAD:refs/heads/side")
		refsBefore := f.workRefs(t)
		mustGit(t, f.mount, f.home, "config", "--global", "receive.denyDeletes", "false")
		mustGit(t, f.mount, f.home, "config", "--global", "receive.denyNonFastForwards", "false")
		if out, err := realGit(t, f.mount, f.home, nil, "push", f.work, ":refs/heads/side"); err == nil {
			t.Fatalf("git must refuse a ref deletion in the pinned repo: %s", out)
		}
		// A sibling of the pushed commit: updating side to it is not a fast-forward.
		mustGit(t, f.mount, f.home, "commit", "-q", "--amend", "--allow-empty", "-m", "rewritten")
		if out, err := realGit(t, f.mount, f.home, nil, "push", "--force", f.work, "HEAD:refs/heads/side"); err == nil {
			t.Fatalf("git must refuse a forced non-fast-forward update in the pinned repo: %s", out)
		}
		if after := f.workRefs(t); after != refsBefore {
			t.Fatalf("refs must be unchanged by the refused delete and force push: before %q after %q", refsBefore, after)
		}
		if f.workHead(t) != f.head {
			t.Fatalf("FR-152: HEAD must be unchanged")
		}
	})
}
