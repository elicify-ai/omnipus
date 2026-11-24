package sandbox_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// #1960 S1 security round 2, F1-a: from a cwd outside the protected work dir,
// the token check judges literal paths against the starting cwd only, so a
// script that reaches the work dir through a relative cd, a variable, a glob or
// quote splicing slipped past it. With a work dir registered, such a script
// together with a mutating git verb is refused, and runs of spaces or tabs no
// longer hide "git <verb>" from the patterns in either cwd case.
func TestGitGuard_IndirectPathToProtectedWorkdir(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	work := filepath.Join(ws, "work")
	mount := filepath.Join(ws, "m")
	for _, dir := range []string{filepath.Join(work, ".git"), filepath.Join(mount, ".git")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	g := sandbox.NewGitGuard()
	if err := g.Protect(work); err != nil {
		t.Fatal(err)
	}
	prefix := work[:len(work)-2] // ".../ws/wo"

	denied := []struct{ name, cwd, script string }{
		{"cd_parent_then_cd_work_reset", mount, "cd .. && cd work && git reset --hard"},
		{"variable_spliced_path_commit", mount, "W=" + prefix + "; git -C ${W}rk commit --allow-empty -m x"},
		{"quote_spliced_path_commit", mount, "git -C " + prefix + `""rk commit --allow-empty -m x`},
		{"glob_path_commit", mount, "git -C " + filepath.Join(ws, "w*") + " commit --allow-empty -m x"},
		{"tilde_path_commit", mount, "git -C ~/work commit -m x"},
		{"command_substitution_commit", mount, "git -C \"$(dirname $PWD)/work\" commit -m x"},
		{"backtick_commit", mount, "git -C `dirname $PWD`/work commit -m x"},
		{"backslash_path_commit", mount, "git -C " + prefix + "\\rk commit -m x"},
		{"question_glob_commit", mount, "git -C " + prefix + "?k commit -m x"},
		{"push_with_variable_from_mount", mount, "git push origin $BRANCH"},
		{"two_spaces_in_work", work, "git  commit --allow-empty -m x"},
		{"tab_in_work", work, "git\tcommit --allow-empty -m x"},
		{"two_spaces_cd_work_from_mount", mount, "cd " + work + " && git  reset   --hard"},
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
		{"plain_push_from_mount", mount, "git push origin HEAD:refs/heads/s20"},
		{"status_from_mount", mount, "git status"},
		{"status_in_work", work, "git status"},
		{"variable_without_mutating_git", mount, "echo $HOME && git status && git log --oneline"},
	}
	for _, tc := range allowed {
		t.Run("allowed/"+tc.name, func(t *testing.T) {
			d := g.InspectExec([]string{"sh", "-c", tc.script}, tc.cwd)
			if !d.Allowed {
				t.Fatalf("cwd=%s script=%q: want allowed, got refused: %s", tc.cwd, tc.script, d.Reason)
			}
		})
	}

	// Without a registered work dir the extra trigger stays off.
	free := sandbox.NewGitGuard()
	if d := free.InspectExec([]string{"sh", "-c", "git commit -m \"$MSG\""}, mount); !d.Allowed {
		t.Fatalf("no protected work dir: want allowed, got refused: %s", d.Reason)
	}
}
