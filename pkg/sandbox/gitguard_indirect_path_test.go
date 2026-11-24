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
// quote splicing slipped past it. With a work dir registered, a script whose
// path operand (-C, --git-dir, --work-tree, cd, pushd, env -C) cannot be
// resolved statically, or that uses a bare or relative cd, is refused together
// with a mutating git verb; expansions in messages, branch names and refspecs
// do not count (H54). Runs of spaces or tabs no longer hide "git <verb>" from
// the patterns in either cwd case.
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
		{"two_spaces_in_work", work, "git  commit --allow-empty -m x"},
		{"tab_in_work", work, "git\tcommit --allow-empty -m x"},
		{"two_spaces_cd_work_from_mount", mount, "cd " + work + " && git  reset   --hard"},
		// H54 narrowing: an expansion in a path position still refuses.
		{"command_substitution_C_operand", mount, `git -C "$(dirname $PWD)/work" commit`},
		{"cd_variable_reset", mount, "cd $W && git reset --hard"},
		{"pushd_variable_commit", mount, "pushd $W && git commit -m x"},
		{"env_C_variable_commit", mount, "env -C $W git commit -m x"},
		{"git_dir_variable_commit", mount, "git --git-dir=$W/.git commit -m x"},
		{"work_tree_variable_commit", mount, "git --work-tree $W commit -m x"},
		{"glued_C_variable_commit", mount, "git -C$W commit -m x"},
		{"bare_cd_commit", mount, "cd && git commit -m x"},
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
		// H54: expansions outside path positions (messages, branch and remote
		// names, refspecs) must not trigger the refusal.
		{"commit_message_question_mark", mount, `git commit -m "why?"`},
		{"commit_message_variable", mount, `git commit -m "$MSG"`},
		{"push_branch_variable", mount, `git push origin "$BRANCH"`},
		{"push_glob_refspec", mount, `git push origin 'feat/*'`},
		{"push_remote_variable_tilde_ref", mount, `git push $REMOTE HEAD~0:refs/heads/x`},
		{"cd_absolute_mount_then_commit", mount, "cd " + mount + " && git commit -m \"$MSG\""},
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
