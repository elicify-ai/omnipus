package sandbox_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// #1960 S1 security review F1 (H48 B1 follow-up): bash may now start in a
// write mount outside the protected work dir. A shell script run from there
// that points git (or cd) at the protected work dir must still be refused;
// a push from the mount itself must still be allowed.
func TestGitGuard_MountCwdScriptNamingProtectedWorkdir(t *testing.T) {
	base := t.TempDir()
	work := filepath.Join(base, "home", "workspaces", "ws", "work")
	mount := filepath.Join(base, "projects", "repo")
	for _, dir := range []string{filepath.Join(work, ".git"), filepath.Join(mount, ".git"), filepath.Join(mount, "sub")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	g := sandbox.NewGitGuard()
	if err := g.Protect(work); err != nil {
		t.Fatal(err)
	}
	relFromMount, err := filepath.Rel(mount, work)
	if err != nil {
		t.Fatal(err)
	}
	relFromSub, err := filepath.Rel(filepath.Join(mount, "sub"), work)
	if err != nil {
		t.Fatal(err)
	}

	denied := []struct{ name, cwd, script string }{
		{"git_C_abs_commit", mount, "git -C " + work + " commit --allow-empty -m forged"},
		{"git_C_abs_reset_hard", mount, "git -C " + work + " reset --hard HEAD~1"},
		{"git_C_abs_update_ref", mount, "git -C " + work + " update-ref refs/heads/main HEAD~1"},
		{"git_C_abs_rebase", mount, "git -C " + work + " rebase -i HEAD~2"},
		{"git_C_quoted_abs_commit", mount, "git -C \"" + work + "\" commit -m forged"},
		{"git_C_glued_abs_commit", mount, "git -C" + work + " commit -m forged"},
		{"cd_abs_reset_hard", mount, "cd " + work + " && git reset --hard"},
		{"cd_abs_semicolon_commit", mount, "cd " + work + "; git commit -m forged"},
		{"git_C_relative_commit", mount, "git -C " + relFromMount + " commit -m forged"},
		{"git_C_relative_from_sub", filepath.Join(mount, "sub"), "git -C " + relFromSub + " reset --hard"},
		{"cd_relative_update_ref", mount, "cd " + relFromMount + " && git update-ref -d refs/heads/main"},
		{"work_tree_option_commit", mount, "git --work-tree=" + work + " commit -m forged"},
	}
	for _, tc := range denied {
		t.Run("denied/"+tc.name, func(t *testing.T) {
			d := g.InspectExec([]string{"sh", "-c", tc.script}, tc.cwd)
			if d.Allowed {
				t.Fatalf("cwd=%s script=%q: want refused, got allowed", tc.cwd, tc.script)
			}
		})
	}

	allowed := []struct{ name, cwd, script string }{
		{"push_from_mount", mount, "git push origin HEAD:refs/heads/s20"},
		{"commit_in_mount", mount, "git add -A && git commit -m work"},
		{"git_C_mount_push", filepath.Join(mount, "sub"), "git -C " + mount + " push origin HEAD"},
		{"read_only_log_of_work", mount, "git -C " + work + " log --oneline"},
	}
	for _, tc := range allowed {
		t.Run("allowed/"+tc.name, func(t *testing.T) {
			d := g.InspectExec([]string{"sh", "-c", tc.script}, tc.cwd)
			if !d.Allowed {
				t.Fatalf("cwd=%s script=%q: want allowed, got refused: %s", tc.cwd, tc.script, d.Reason)
			}
		})
	}
}
