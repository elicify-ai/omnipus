package sandbox_test

// H55 v2 (#1960 S1), the guard-level rows of the architect's second ruling
// (/workspace/logs/1960/spec-s1/ruling-H55-v2.md, section (3)) that must stay
// ALLOWED: R13 (F4, the narrowed scope of Rule A), the extra idioms of R7, the scope
// of the third marker of item 3 (a --ex word with no push or send-pack word anywhere
// in the script) and, from the CHECK delta, the twin of the accepted git-word refusal
// and the config writer that names no protected .git. They are the other half of
// gitguard_h55_v2_refusals_test.go: a fix that refuses too much turns these red, a
// fix that refuses too little turns the refusal leaves red.
//
// Oracles: ruling-H55-v2 section (2) item 1 (scope: Rule A runs only for a script
// with a git word; "Non-git commands are no longer refused: grep -rn receive-pack
// docs, echo receivepack >> notes.txt, wc -c receive.txt, cat GIT_CONFIG_KEY_notes.md"),
// item 5(a) (the remote operand is a run-time path only when it holds $( or a
// backtick AND a slash inside the balanced word), item 3 as amended by
// ruling-check-delta.md item 1 (2026-11-25: the --ex word is a marker when the script
// holds the word push or send-pack anywhere; there is no segment), ruling-check-delta.md
// item 2 (the git word may stand anywhere: "echo git receive-pack" is the accepted
// refusal and "echo receive-pack" is allowed) and item 4 (the config-writer refusal is
// scoped to a path at or under a protected .git), section (3) rows R13 and R7, and the
// re-review's F4 row (h55-review-2) for grep -c receive.go. Nothing here is taken from
// a run of the guard.
import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// R13: a command that is not a git command is not refused for the words it
// mentions. Every script below holds a marker of Rule A as text (receive-pack,
// receivepack, "-c receive.", GIT_CONFIG_KEY_) and no git command word.
func TestGitGuard_H55_V2_R13_NonGitCommandsNamingAMarkerStayAllowed(t *testing.T) {
	l := h55Setup(t)

	// Instrument: Rule A is armed in this very layout. The accepted refusal of the
	// ruling (a git command that only mentions a marker) is refused here, so an
	// allowed decision below comes from the narrowed scope and not from a guard
	// that never looks for markers.
	if d := l.guard.InspectExec([]string{"sh", "-c", "git log --grep=receive-pack"}, l.mount); d.Allowed {
		t.Fatal("instrument (ruling-H55-v2 R13): the git command that mentions a marker must be refused in this layout, or an allowed decision below proves nothing about the scope")
	}

	rows := []struct{ name, script string }{
		{"R13_grep_recursive_receive_pack", `grep -rn receive-pack docs`},
		{"R13_echo_receivepack_append", `echo receivepack >> notes.txt`},
		{"R13_wc_receive_txt", `wc -c receive.txt`},
		{"R13_cat_git_config_key_file", `cat GIT_CONFIG_KEY_notes.md`},
		{"R13_grep_count_receive_go", `grep -c receive.go f`},
	}
	for _, tc := range rows {
		t.Run("shell/"+tc.name, func(t *testing.T) {
			if d := l.guard.InspectExec([]string{"sh", "-c", tc.script}, l.mount); !d.Allowed {
				t.Fatalf("ruling-H55-v2 R13: %q holds no git command word and must be allowed, got refused: %s (%s)", tc.script, d.Reason, d.PolicyRule)
			}
		})
	}
}

// R7, the extra idioms of the second ruling and the short spellings of its
// H54 list (the review's table): ordinary pushes and commits from the mount stay
// allowed. The substitution rows are the ones the F3 operand scanner could break:
// a $( ) operand with no slash inside it is not a path.
func TestGitGuard_H55_V2_R7_ExtraIdiomsStayAllowed(t *testing.T) {
	l := h55Setup(t)
	scripts := []struct{ name, script string }{
		{"R7_push_quoted_glob_refspec", `git push origin 'feat/*'`},
		{"R7_push_remote_default_expansion", `git push "${REMOTE:-origin}" main`},
		{"R7_push_remote_from_get_url", `git push "$(git remote get-url origin)" main`},
		{"R7_push_remote_from_get_url_with_a_slashed_refspec", `git push "$(git remote get-url origin)" HEAD:refs/heads/s20`},
		{"R7_push_remote_from_get_url_unquoted", `git push $(git remote get-url origin) main`},
		{"R7_push_remote_from_backticks_without_a_slash", "git push `git remote get-url origin` main"},
		{"R7_commit_message_single_quoted_question_mark", `git commit -m 'why?'`},
		{"R7_push_short_https_url", `git push https://x/y main`},
		{"R7_push_remote_variable_after_options", `git push -u "$REMOTE" "$BRANCH"`},
	}
	for _, tc := range scripts {
		t.Run("shell/"+tc.name, func(t *testing.T) {
			if d := l.guard.InspectExec([]string{"sh", "-c", tc.script}, l.mount); !d.Allowed {
				t.Fatalf("ruling-H55-v2 R7: %q from the mount must stay allowed, got refused: %s (%s)", tc.script, d.Reason, d.PolicyRule)
			}
		})
	}

	direct := []struct {
		name string
		argv []string
	}{
		{"R7_argv_push_glob_refspec", []string{"git", "push", "origin", "feat/*"}},
		{"R7_argv_push_short_https_url", []string{"git", "push", "https://x/y", "main"}},
		{"R7_argv_commit_message_question_mark", []string{"git", "commit", "-m", "why?"}},
	}
	for _, tc := range direct {
		t.Run("argv/"+tc.name, func(t *testing.T) {
			if d := l.guard.InspectExec(tc.argv, l.mount); !d.Allowed {
				t.Fatalf("ruling-H55-v2 R7: %q from the mount must stay allowed, got refused: %s (%s)", strings.Join(tc.argv, " "), d.Reason, d.PolicyRule)
			}
		})
	}
}

// R11, the scope of the third marker (ruling section (2) item 3, as amended by
// ruling-check-delta.md item 1): a word starting with --ex is a marker only when the
// script also holds the word push or send-pack somewhere; there is no segment. A
// script with such a word and no push or send-pack word is not refused, whatever its
// commands are. The first two rows and the argv row follow from the clause itself.
// The three two-command rows and the exit-code row keep the shapes of the round-10
// rows that read "segment" (those with a push are pinned as refusals, CD1_* in
// gitguard_h55_v2_refusals_test.go): the same two-command lines with a command that
// is not a push stay allowed. The pushed row follows from "the word push": a word
// that only contains the letters is not it.
//
// Renamed from ..._R11_ExAbbreviationOutsideAPushSegmentStaysAllowed when the ruling
// removed the segment (round 11; see the dispute file).
func TestGitGuard_H55_V2_R11_ExAbbreviationWithNoPushOrSendPackWordStaysAllowed(t *testing.T) {
	l := h55Setup(t)

	// Instrument: the same kind of word with a push in the script is refused by Rule A
	// in this layout, so an allowed decision below comes from the scope clause and not
	// from a guard that never reads the abbreviation.
	const inPush = `git push --ex=x ev master`
	h55RequireRuleA(t, l.guard.InspectExec([]string{"sh", "-c", inPush}, l.mount), inPush)

	shell := []struct{ name, script string }{
		{"R11_ex_word_in_a_listing_command", `git ls-files --exclude-standard`},
		{"R11_ex_word_in_a_log_command", `git log --exclude=refs/heads/tmp --all`},
		{"R11_ex_word_before_another_command_after_a_semicolon", `git ls-files --exclude-standard; git status`},
		{"R11_ex_word_before_another_command_after_and_and", `git ls-files --exclude-standard && git log --oneline`},
		{"R11_ex_word_after_another_command_after_a_semicolon", `git status; git ls-files --exclude-standard`},
		{"R11_exit_code_option_between_two_commands", `git log --exit-code; git status`},
		{"R11_ex_word_with_a_word_that_only_contains_push", `git ls-files --exclude-standard; echo pushed`},
	}
	for _, tc := range shell {
		t.Run("shell/"+tc.name, func(t *testing.T) {
			if d := l.guard.InspectExec([]string{"sh", "-c", tc.script}, l.mount); !d.Allowed {
				t.Fatalf("ruling-check-delta item 1: %q has an --ex word and no push or send-pack word and must be allowed, got refused: %s (%s)", tc.script, d.Reason, d.PolicyRule)
			}
		})
	}
	argv := []string{"git", "ls-files", "--exclude-standard"}
	t.Run("argv/R11_argv_ex_word_in_a_listing_command", func(t *testing.T) {
		if d := l.guard.InspectExec(argv, l.mount); !d.Allowed {
			t.Fatalf("ruling-check-delta item 1: %q has no push and must be allowed, got refused: %s (%s)", strings.Join(argv, " "), d.Reason, d.PolicyRule)
		}
	})
}

// CD2 twin, ruling-check-delta.md item 2: the accepted refusal "echo git receive-pack"
// and its allowed pair "echo receive-pack" differ in one word, the word git, so the
// pair shows the scope of Rule A and nothing else. The refused half is also pinned,
// with its marker named, in gitguard_h55_v2_refusals_test.go (CD2_*).
func TestGitGuard_H55_V2_CD2_MarkerWithoutTheGitWordStaysAllowed(t *testing.T) {
	l := h55Setup(t)

	// Instrument: the twin with the word git is refused by Rule A in this very layout.
	const withGit = `echo git receive-pack`
	h55RequireRuleA(t, l.guard.InspectExec([]string{"sh", "-c", withGit}, l.mount), withGit)

	const without = `echo receive-pack`
	if d := l.guard.InspectExec([]string{"sh", "-c", without}, l.mount); !d.Allowed {
		t.Fatalf("ruling-check-delta item 2: %q holds no git word and must be allowed, got refused: %s (%s)", without, d.Reason, d.PolicyRule)
	}
}

// F5 twin, ruling-check-delta.md item 4: the refusal of a git config writer is scoped to
// a path at or under a protected .git. The same writers (--file, --file=, -f and the
// glued -f<path>) naming any other file are not refused. This is the over-refusal
// control of gitguard_h55_v2_refusals_test.go's F5 rows: a guard that refused every
// config --file would pass those rows and fail this one. The key is user.name, so no
// receive marker of Rule A is involved; the files are an ordinary file in the mount,
// the mount's own .git/config (a .git that is not protected) and, for the "at or
// under" boundary, a sibling folder whose name starts with the protected folder's name.
// Rows are derived from the ruling's scope sentence; the ruling lists no allowed row.
func TestGitGuard_H55_V2_F5_ConfigFileOutsideAProtectedGitStaysAllowed(t *testing.T) {
	l := h55Setup(t)
	protected := filepath.Join(l.work, ".git", "config")
	ordinary := filepath.Join(l.mount, "settings.cfg")
	ownGit := filepath.Join(l.mount, ".git", "config")
	sibling := filepath.Join(filepath.Dir(l.work), filepath.Base(l.work)+"2", ".git", "config")
	if err := os.MkdirAll(filepath.Dir(sibling), 0o755); err != nil {
		t.Fatal(err)
	}

	spellings := []struct {
		name string
		argv func(path string) []string
	}{
		{"file_spaced", func(p string) []string { return []string{"git", "config", "--file", p, "user.name", "tester"} }},
		{"file_glued", func(p string) []string { return []string{"git", "config", "--file=" + p, "user.name", "tester"} }},
		{"short_spaced", func(p string) []string { return []string{"git", "config", "-f", p, "user.name", "tester"} }},
		{"short_glued", func(p string) []string { return []string{"git", "config", "-f" + p, "user.name", "tester"} }},
	}

	// Instrument: the same four writers naming the protected file, and the shell form
	// of the first, are refused in this layout. Without it an allowed decision below
	// could come from a guard that has no config rule here.
	for _, sp := range spellings {
		if d := l.guard.InspectExec(sp.argv(protected), l.mount); d.Allowed {
			t.Fatalf("instrument (ruling-check-delta item 4): %s naming the protected .git/config must be refused in this layout, or an allowed decision below proves nothing", sp.name)
		}
	}
	if d := l.guard.InspectExec([]string{"sh", "-c", "git config --file " + protected + " user.name tester"}, l.mount); d.Allowed {
		t.Fatal("instrument (ruling-check-delta item 4): the shell form naming the protected .git/config must be refused in this layout")
	}

	paths := []struct{ name, path string }{
		{"ordinary_file_absolute", ordinary},
		{"ordinary_file_relative_to_the_mount", "settings.cfg"},
		{"mount_own_git_config", ownGit},
		{"sibling_folder_sharing_the_protected_name_prefix", sibling},
	}
	for _, sp := range spellings {
		for _, p := range paths {
			t.Run("argv/F5T_"+sp.name+"_"+p.name, func(t *testing.T) {
				argv := sp.argv(p.path)
				if d := l.guard.InspectExec(argv, l.mount); !d.Allowed {
					t.Fatalf("ruling-check-delta item 4: %q names no path at or under a protected .git and must be allowed, got refused: %s (%s)", strings.Join(argv, " "), d.Reason, d.PolicyRule)
				}
			})
		}
	}

	shell := []struct{ name, script string }{
		{"F5T_shell_file_spaced_ordinary_file", "git config --file " + ordinary + " user.name tester"},
		{"F5T_shell_short_spaced_ordinary_file", "git config -f " + ordinary + " user.name tester"},
		{"F5T_shell_file_spaced_mount_own_git_config", "git config --file " + ownGit + " user.name tester"},
	}
	for _, tc := range shell {
		t.Run("shell/"+tc.name, func(t *testing.T) {
			if d := l.guard.InspectExec([]string{"sh", "-c", tc.script}, l.mount); !d.Allowed {
				t.Fatalf("ruling-check-delta item 4: %q names no path at or under a protected .git and must be allowed, got refused: %s (%s)", tc.script, d.Reason, d.PolicyRule)
			}
		})
	}
}
