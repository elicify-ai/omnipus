package sandbox_test

// H55 v2 (#1960 S1), the guard-level rows of the architect's second ruling
// (/workspace/logs/1960/spec-s1/ruling-H55-v2.md, section (3)) that must stay
// ALLOWED: R13 (F4, the narrowed scope of Rule A), the extra idioms of R7 and the
// scope of the third marker of item 3 (a --ex word outside a push segment). They
// are the other half of gitguard_h55_v2_refusals_test.go: a fix that refuses too
// much turns these red, a fix that refuses too little turns the refusal leaves red.
//
// Oracles: ruling-H55-v2 section (2) item 1 (scope: Rule A runs only for a script
// with a git command word; "Non-git commands are no longer refused: grep -rn
// receive-pack docs, echo receivepack >> notes.txt, wc -c receive.txt, cat
// GIT_CONFIG_KEY_notes.md"), item 5(a) (the remote operand is a run-time path only
// when it holds $( or a backtick AND a slash inside the balanced word), item 3 (the
// third marker is judged only "in a segment that also contains the word push or
// send-pack"), section (3) rows R13 and R7, and the re-review's F4 row
// (h55-review-2) for grep -c receive.go. Nothing here is taken from a run of the
// guard.
import (
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

// R11, the scope of the third marker (ruling section (2) item 3): a word starting
// with --ex is a marker only "in a segment that also contains the word push or
// send-pack". A git command that carries such a word and has no push of its own is
// not refused, whatever the other commands of the script do. The first two rows and
// the argv row follow from the clause itself; the three rows with a second command
// read "segment" as the ruling's item 1 reads command positions (the text between
// ; & | ( or a newline) - a derived reading, because the ruling does not define the
// word.
func TestGitGuard_H55_V2_R11_ExAbbreviationOutsideAPushSegmentStaysAllowed(t *testing.T) {
	l := h55Setup(t)

	// Instrument: the same kind of word inside a push segment is refused by Rule A in
	// this layout, so an allowed decision below comes from the scope clause and not
	// from a guard that never reads the abbreviation.
	const inPush = `git push --ex=x ev master`
	h55RequireRuleA(t, l.guard.InspectExec([]string{"sh", "-c", inPush}, l.mount), inPush)

	shell := []struct{ name, script string }{
		{"R11_ex_word_in_a_listing_command", `git ls-files --exclude-standard`},
		{"R11_ex_word_in_a_log_command", `git log --exclude=refs/heads/tmp --all`},
		{"R11_ex_word_before_a_push_after_a_semicolon", `git ls-files --exclude-standard; git push origin main`},
		{"R11_ex_word_before_a_push_after_and_and", `git ls-files --exclude-standard && git push origin main`},
		{"R11_ex_word_after_a_push_after_a_semicolon", `git push origin main; git ls-files --exclude-standard`},
	}
	for _, tc := range shell {
		t.Run("shell/"+tc.name, func(t *testing.T) {
			if d := l.guard.InspectExec([]string{"sh", "-c", tc.script}, l.mount); !d.Allowed {
				t.Fatalf("ruling-H55-v2 R11 scope: %q has no push in the segment of its --ex word and must be allowed, got refused: %s (%s)", tc.script, d.Reason, d.PolicyRule)
			}
		})
	}
	argv := []string{"git", "ls-files", "--exclude-standard"}
	t.Run("argv/R11_argv_ex_word_in_a_listing_command", func(t *testing.T) {
		if d := l.guard.InspectExec(argv, l.mount); !d.Allowed {
			t.Fatalf("ruling-H55-v2 R11 scope: %q has no push and must be allowed, got refused: %s (%s)", strings.Join(argv, " "), d.Reason, d.PolicyRule)
		}
	})
}
