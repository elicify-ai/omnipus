package sandbox_test

// H55 v2 (#1960 S1), the guard-level rows of the architect's second ruling
// (/workspace/logs/1960/spec-s1/ruling-H55-v2.md, section (3)) and of the second
// security review (/workspace/logs/1960/security-s1-3/h55-review-2.md, finding F5):
// which commands the evidence guard refuses when a protected work folder is
// registered and the working directory is an approved mount beside it. Same layout
// as the first ruling's rows (h55Setup in gitguard_h55_allowed_test.go).
//
// Oracles: ruling-H55-v2 section (2) items 1-5 and section (3) rows R11, R12 and
// R13; h55-review-2 F5 and its fix paragraph. Nothing is taken from a run of the
// guard. The ruling names no reason text for Rule B, so the Rule B rows assert the
// refusal only; the Rule A rows assert the reason the first ruling names, and the
// accepted refusal of R13 asserts that the message names the marker (section (2)
// item 1). The allowed rows are in gitguard_h55_v2_allowed_test.go.
//
// Every remote in the Rule A rows is a plain name, so Rule B cannot be what
// refuses them; no Rule B row contains an override, so Rule A cannot be what
// refuses those.
import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// h55RuleAReason is the reason the first ruling names for Rule A.
const h55RuleAReason = "receive-side override (H55)"

// h55RequireRuleA fails unless the decision is a refusal that names Rule A.
func h55RequireRuleA(t *testing.T, d sandbox.ExecDecision, what string) {
	t.Helper()
	if d.Allowed {
		t.Fatalf("ruling-H55-v2 Rule A: %s must be refused, got allowed", what)
	}
	if !strings.Contains(d.PolicyRule+" | "+d.Reason, h55RuleAReason) {
		t.Fatalf("ruling-H55-v2 Rule A: the refusal of %s must name %q; got rule %q reason %q", what, h55RuleAReason, d.PolicyRule, d.Reason)
	}
}

// h55RequireRuleANaming is h55RequireRuleA plus the ruling's visibility clause
// (ruling-H55-v2 section (2) item 1, "The refusal message must name the marker";
// ruling-check-delta.md item 1 for the accepted over-refusal): the message names the
// marker, or its prefix where the marker is a word prefix such as --ex.
func h55RequireRuleANaming(t *testing.T, d sandbox.ExecDecision, what, marker string) {
	t.Helper()
	h55RequireRuleA(t, d, what)
	if !strings.Contains(strings.ToLower(d.PolicyRule+" | "+d.Reason), marker) {
		t.Fatalf("ruling-H55-v2 Rule A: the refusal of %s must name the marker %q; got rule %q reason %q", what, marker, d.PolicyRule, d.Reason)
	}
}

// R11 (F2), by script: git's unambiguous abbreviations of the override options,
// quotes and backslashes that split a marker, an alias that carries the
// abbreviation, and the --config-env marker. Rule A normalises (lower-case, quotes
// and backslashes deleted) before it scans; values that are not markers themselves
// are used wherever the row is about a prefix.
func TestGitGuard_H55_V2_R11_AbbreviatedAndSplitOverridesRefusedByRuleA(t *testing.T) {
	l := h55Setup(t)
	rows := []struct{ name, script string }{
		{"R11_rece_abbreviation_glued", `git push --rece=./wrapper.sh ev master`},
		{"R11_rece_abbreviation_spaced", `git push --rece ./wrapper.sh ev master`},
		{"R11_ex_abbreviation_glued", `git push --ex=x ev master`},
		{"R11_exe_abbreviation_spaced", `git push --exe x ev master`},
		{"R11_ex_abbreviation_on_send_pack", `git send-pack --ex=x ev master`},
		{"R11_quote_split_value", `git push --rece='git -c rec""eive.denyCurrentBranch=ignore receive-pa""ck' ev master`},
		{"R11_double_quote_split_option_name", `git push --rec""eive-pack=x ev master`},
		{"R11_single_quote_split_option_name", `git push --rec'eive-pa'ck=x ev master`},
		{"R11_backslash_split_option_name", `git push --rec\eive-pa\ck=x ev master`},
		{"R11_quote_split_config_key", `git -c rec""eive.denyCurrentBranch=ignore push ev master`},
		{"R11_alias_carries_the_rece_abbreviation", `git -c alias.p='push --rece=x' p`},
		{"R11_alias_carries_the_ex_abbreviation", `git -c alias.p='push --ex=x' p`},
		{"R11_config_env_names_a_receive_setting", `git --config-env=receive.denyCurrentBranch=SETTING push ev master`},
		// Item 2 of the ruling collapses blank runs before the scan: the shell reads
		// the same words whatever the run of blanks between them is.
		{"R11_blank_run_before_the_receive_setting", `git -c  receive.denyCurrentBranch=ignore push ev master`},
		{"R11_tab_before_the_receive_setting", "git -c\treceive.denyCurrentBranch=ignore push ev master"},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			h55RequireRuleA(t, l.guard.InspectExec([]string{"sh", "-c", tc.script}, l.mount), tc.script)
		})
	}
}

// R11, direct git argv: the same markers on the arguments joined with spaces.
func TestGitGuard_H55_V2_R11_DirectGitArgvRefusedByRuleA(t *testing.T) {
	l := h55Setup(t)
	rows := []struct {
		name string
		argv []string
	}{
		{"R11_argv_rece_abbreviation", []string{"git", "push", "--rece=x", "ev", "m"}},
		{"R11_argv_exe_abbreviation_spaced", []string{"git", "push", "--exe", "x", "ev"}},
		{"R11_argv_ex_abbreviation_glued", []string{"git", "push", "--ex=x", "ev", "m"}},
		{"R11_argv_ex_abbreviation_on_send_pack", []string{"git", "send-pack", "--ex=x", "ev", "m"}},
		{"R11_argv_git_as_an_absolute_path", []string{"/usr/bin/git", "push", "--rece=x", "ev", "m"}},
		{"R11_argv_alias_carries_the_rece_abbreviation", []string{"git", "-c", "alias.p=push --rece=x", "p"}},
		{"R11_argv_alias_carries_the_ex_abbreviation", []string{"git", "-c", "alias.p=push --ex=x", "p"}},
		{"R11_argv_config_env_names_a_receive_setting", []string{"git", "--config-env=receive.denyCurrentBranch=SETTING", "push", "ev", "m"}},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			h55RequireRuleA(t, l.guard.InspectExec(tc.argv, l.mount), strings.Join(tc.argv, " "))
		})
	}
}

// Ruling section (2) item 1 (scope widened by ruling-check-delta.md item 2, which
// reads the word git anywhere): the command-word spellings below - at the start,
// after ; & | ( or a newline, after command or env, as a path ending in /git - are
// the narrow subset of that scope and must stay refused. The scope that spares
// non-git commands must not spare a git command spelled one of these ways. The
// marker is the abbreviation of the override option with a value that is not itself
// a marker.
func TestGitGuard_H55_V2_R11_GitCommandWordFormsKeepRuleAOn(t *testing.T) {
	l := h55Setup(t)
	rows := []struct{ name, script string }{
		{"R11_git_at_the_start", `git push --rece=x ev master`},
		{"R11_git_after_a_semicolon", `echo start; git push --rece=x ev master`},
		{"R11_git_after_and_and", `cd .. && git push --rece=x ev master`},
		{"R11_git_after_a_pipe", `echo go | git push --rece=x ev master`},
		{"R11_git_after_a_background_ampersand", `sleep 1 & git push --rece=x ev master`},
		{"R11_git_after_an_open_parenthesis", `(git push --rece=x ev master)`},
		{"R11_git_after_a_newline", "echo start\ngit push --rece=x ev master"},
		{"R11_git_after_command", `command git push --rece=x ev master`},
		{"R11_git_after_env", `env git push --rece=x ev master`},
		{"R11_git_as_an_absolute_path", `/usr/bin/git push --rece=x ev master`},
		{"R11_git_after_an_assignment_prefix", `GIT_TRACE=1 git push --rece=x ev master`},
		{"R11_git_after_env_and_an_assignment", `env GIT_TRACE=1 git push --rece=x ev master`},
		// The ruling says "after ;&|(": no blank is needed between the separator
		// and the command word, so the rows above (which all have one) do not
		// show that each separator is read by itself.
		{"R11_git_directly_after_a_semicolon", `echo start;git push --rece=x ev master`},
		{"R11_git_directly_after_and_and", `cd ..&&git push --rece=x ev master`},
		{"R11_git_directly_after_a_pipe", `echo go|git push --rece=x ev master`},
		{"R11_git_directly_after_a_background_ampersand", `sleep 1&git push --rece=x ev master`},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			h55RequireRuleA(t, l.guard.InspectExec([]string{"sh", "-c", tc.script}, l.mount), tc.script)
		})
	}
}

// CD1, ruling-check-delta.md item 1 (architect, 2026-11-25), which replaces the
// "segment" reading of ruling-H55-v2 section (2) item 3: a word starting with --ex is
// a Rule A marker whenever the script holds the word push or send-pack anywhere.
// There is no segment: segments are cut after quotes are deleted, so a quoted
// separator inside one real command would otherwise hide the marker. Rows 1 and 2
// are the ruling's own (the second is its "accepted refusal; pin it"); rows 3 to 5
// are the three rows of the earlier allowed leaf that read "segment" (round 10),
// which the ruling now makes refusals of the same kind; the rest are derived from the
// ruling's reason (a quoted separator of the ratified set, the word send-pack, the
// direct argv form: "apply the same to the arguments joined with spaces").
func TestGitGuard_H55_V2_CD1_ExWordWithAPushWordAnywhereIsRefusedByRuleA(t *testing.T) {
	l := h55Setup(t)

	// Instrument: each half of the condition alone is allowed in this layout, so the
	// refusals below come from the combination and not from a guard that refuses
	// either word by itself.
	for _, alone := range []string{`git ls-files --exclude-standard`, `git push origin main`} {
		if d := l.guard.InspectExec([]string{"sh", "-c", alone}, l.mount); !d.Allowed {
			t.Fatalf("instrument (ruling-check-delta item 1): %q holds only one half of the condition and must be allowed in this layout, got refused: %s (%s)", alone, d.Reason, d.PolicyRule)
		}
	}

	shell := []struct{ name, script string }{
		{"CD1_quoted_semicolon_inside_a_push_option", `git push -o 'a;b' --ex=x ev HEAD:master`},
		{"CD1_push_then_grep_with_an_exclude_option_accepted_refusal", `git push origin main; grep --exclude=x .`},
		{"CD1_ex_word_before_a_push_after_a_semicolon", `git ls-files --exclude-standard; git push origin main`},
		{"CD1_ex_word_before_a_push_after_and_and", `git ls-files --exclude-standard && git push origin main`},
		{"CD1_ex_word_after_a_push_after_a_semicolon", `git push origin main; git ls-files --exclude-standard`},
		{"CD1_quoted_and_and_inside_a_push_option", `git push -o "a&&b" --exe x ev HEAD:master`},
		{"CD1_quoted_pipe_inside_a_push_option", `git push -o 'a|b' --ex=x ev`},
		{"CD1_send_pack_in_another_command", `git ls-files --exclude-standard; git send-pack ev master`},
	}
	for _, tc := range shell {
		t.Run("shell/"+tc.name, func(t *testing.T) {
			h55RequireRuleANaming(t, l.guard.InspectExec([]string{"sh", "-c", tc.script}, l.mount), tc.script, "--ex")
		})
	}

	argv := []string{"git", "push", "-o", "a;b", "--ex=x", "ev", "HEAD:master"}
	t.Run("argv/CD1_argv_semicolon_inside_a_push_option", func(t *testing.T) {
		h55RequireRuleANaming(t, l.guard.InspectExec(argv, l.mount), strings.Join(argv, " "), "--ex")
	})
}

// CD2, ruling-check-delta.md item 2: Rule A's scope is any word git, or any word
// ending in /git, anywhere in the normalised text, not only in command position. The
// ruling's rows: "echo git receive-pack" is refused (accepted over-refusal) and
// "xargs git push --rece=x" is refused; its reasons name the prefixes sudo, xargs,
// env, then/do and assignments, which the derived rows use. The allowed twin ("echo
// receive-pack") is in gitguard_h55_v2_allowed_test.go.
func TestGitGuard_H55_V2_CD2_GitWordAnywhereKeepsRuleAOn(t *testing.T) {
	l := h55Setup(t)
	rows := []struct{ name, script, marker string }{
		{"CD2_echo_git_receive_pack_accepted_refusal", `echo git receive-pack`, "receive-pack"},
		{"CD2_xargs_git_push_with_an_abbreviation", `xargs git push --rece=x`, "--rece"},
		{"CD2_sudo_before_git", `sudo git push --rece=x ev master`, "--rece"},
		{"CD2_then_before_git", `if true; then git push --rece=x ev master; fi`, "--rece"},
		{"CD2_do_before_git", `for b in x; do git push --rece=x ev "$b"; done`, "--rece"},
		{"CD2_path_ending_in_git_as_an_argument", `echo /usr/bin/git receive-pack`, "receive-pack"},
	}
	for _, tc := range rows {
		t.Run("shell/"+tc.name, func(t *testing.T) {
			h55RequireRuleANaming(t, l.guard.InspectExec([]string{"sh", "-c", tc.script}, l.mount), tc.script, tc.marker)
		})
	}
}

// R12 (F3), by script: the remote operand is read with a scanner that balances $( )
// and backticks, nested included, and any word starting with --rep is --repo. The
// path is "<parent of the mount>/work", the work folder in this layout. Rule B
// refuses; the rows contain no override, so Rule A is not involved.
func TestGitGuard_H55_V2_R12_OperandParsingRefusedByRuleB(t *testing.T) {
	l := h55Setup(t)
	rows := []struct{ name, script string }{
		{"R12_nested_unquoted_substitution", `git push $(dirname $(pwd))/work HEAD:refs/heads/yy`},
		{"R12_substitution_nested_in_backticks", "git push `dirname $(pwd)`/work HEAD:refs/heads/x"},
		{"R12_rep_option_quoted_substitution", `git push --rep="$(dirname $PWD)/work" HEAD:refs/heads/x`},
		{"R12_rep_option_unquoted_substitution", `git push --rep=$(dirname $PWD)/work HEAD:refs/heads/x`},
		{"R12_rep_option_with_a_following_word", `git push --rep "$(dirname $PWD)/work" HEAD:refs/heads/x`},
		{"R12_repo_option_quoted_substitution", `git push --repo="$(dirname $PWD)/work" HEAD:refs/heads/x`},
		{"R12_rep_option_relative_work_path", `git push --rep=../work HEAD:refs/heads/x`},
		{"R12_rep_option_absolute_work_path", `git push --rep=` + l.work + ` HEAD:refs/heads/x`},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			h55RequireRefused(t, l.guard.InspectExec([]string{"sh", "-c", tc.script}, l.mount), tc.script)
		})
	}
}

// R12, direct git argv: the --rep abbreviation names the remote like --repo does.
func TestGitGuard_H55_V2_R12_RepoOptionRefusedByRuleBInDirectArgv(t *testing.T) {
	l := h55Setup(t)
	rows := []struct {
		name string
		argv []string
	}{
		{"R12_argv_rep_option_relative_work_path", []string{"git", "push", "--rep=../work", "HEAD:refs/heads/x"}},
		{"R12_argv_rep_option_with_a_following_word", []string{"git", "push", "--rep", "../work", "HEAD:refs/heads/x"}},
		{"R12_argv_repo_option_relative_work_path", []string{"git", "push", "--repo=../work", "HEAD:refs/heads/x"}},
		{"R12_argv_rep_option_absolute_work_path", []string{"git", "push", "--rep=" + l.work, "HEAD:refs/heads/x"}},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			h55RequireRefused(t, l.guard.InspectExec(tc.argv, l.mount), strings.Join(tc.argv, " "))
		})
	}
}

// R13 (F4), the accepted refusal: a git command that only mentions a marker is
// still refused, and the message names the marker (ruling section (2) item 1:
// "The refusal message must name the marker"). The ruling lists two spellings,
// receive-pack and receivepack; each has a row of its own, because the rows that
// carry the second spelling inside a setting name are refused without that marker
// too and so cannot pin it (round-10 probe K2).
func TestGitGuard_H55_V2_R13_GitCommandNamingAMarkerIsRefusedNamingIt(t *testing.T) {
	l := h55Setup(t)
	rows := []struct{ name, script, marker string }{
		{"R13_git_log_grep_names_receive_pack", `git log --grep=receive-pack`, "receive-pack"},
		{"R13_git_commit_message_names_receive_pack", `git commit -m 'fix receive-pack docs'`, "receive-pack"},
		{"R13_git_log_grep_names_receivepack", `git log --grep=receivepack`, "receivepack"},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			d := l.guard.InspectExec([]string{"sh", "-c", tc.script}, l.mount)
			if d.Allowed {
				t.Fatalf("ruling-H55-v2 R13: %q is the accepted refusal and must be refused, got allowed", tc.script)
			}
			if !strings.Contains(strings.ToLower(d.PolicyRule+" | "+d.Reason), tc.marker) {
				t.Fatalf("ruling-H55-v2 R13: the refusal of %q must name the marker %s; got rule %q reason %q", tc.script, tc.marker, d.PolicyRule, d.Reason)
			}
		})
	}
}

// F5 (h55-review-2): git config --file naming the evidence repository's own config
// file is how the local receive pins could be edited away. The shell form was
// already refused through the git config rule; the direct argv form judged only
// the working directory and allowed it. The fix paragraph: refuse the config verb
// when --file or -f (or any word) names a protected .git.
func TestGitGuard_H55_V2_F5_ConfigFileOnTheEvidenceConfigRefused(t *testing.T) {
	l := h55Setup(t)
	pinFile := filepath.Join(l.work, ".git", "config")
	const mountRelative = "../work/.git/config"

	// Instrument: h55-review-2 F5 says the shell form of this very edit was already
	// refused (through the git config rule) while the argv form was allowed. If the
	// shell form is allowed in this layout the premise of the rows below is gone,
	// and an allowed or refused decision below would say nothing about the F5 fix.
	script := "git config --file " + pinFile + " receive.denyCurrentBranch ignore"
	if d := l.guard.InspectExec([]string{"sh", "-c", script}, l.mount); d.Allowed {
		t.Fatal("instrument (h55-review-2 F5): the shell form of the config --file edit must be refused in this layout")
	}

	rows := []struct {
		name string
		argv []string
	}{
		{"F5_argv_file_option_absolute", []string{"git", "config", "--file", pinFile, "receive.denyCurrentBranch", "ignore"}},
		{"F5_argv_file_option_glued", []string{"git", "config", "--file=" + pinFile, "receive.denyCurrentBranch", "ignore"}},
		{"F5_argv_short_file_option", []string{"git", "config", "-f", pinFile, "core.bare", "true"}},
		// Installed git also accepts the value glued to the short option (checked
		// with git itself: -f<file> writes the file), so the review's "-f" is read as
		// every spelling git accepts for it. A derived row: the review lists -f and
		// --file, not the glued spelling.
		{"F5_argv_short_file_option_glued", []string{"git", "config", "-f" + pinFile, "core.bare", "true"}},
		{"F5_argv_file_option_relative_to_the_mount", []string{"git", "config", "--file", mountRelative, "receive.denyCurrentBranch", "ignore"}},
		{"F5_argv_file_option_unsets_a_pin", []string{"git", "config", "--file", pinFile, "--unset", "receive.denyCurrentBranch"}},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			h55RequireRefused(t, l.guard.InspectExec(tc.argv, l.mount), strings.Join(tc.argv, " "))
		})
	}
}

// The writers h55-review-2 lists as refused before the F5 fix (guard results,
// "Refused:") stay refused: regression pins, not new behaviour. They are in a leaf
// of their own so a failure there is not mistaken for the F5 gap.
func TestGitGuard_H55_V2_F5_PinFileWritersStayRefused(t *testing.T) {
	l := h55Setup(t)
	pinFile := filepath.Join(l.work, ".git", "config")
	rows := []struct{ name, script string }{
		{"F5_pin_echo_append", "echo x >> " + pinFile},
		{"F5_pin_printf_append", "printf x >> " + pinFile},
		{"F5_pin_cp_over", "cp ./other " + pinFile},
		{"F5_pin_tee", "echo x | tee " + pinFile},
		{"F5_pin_git_dir_config", "git --git-dir=" + filepath.Join(l.work, ".git") + " config receive.denyCurrentBranch ignore"},
	}
	for _, tc := range rows {
		t.Run("shell/"+tc.name, func(t *testing.T) {
			h55RequireRefused(t, l.guard.InspectExec([]string{"sh", "-c", tc.script}, l.mount), tc.script)
		})
	}
	// The review does not say which spelling of the --git-dir form it probed; the
	// direct argv spelling is the one the F5 gap was in, so it is pinned too.
	argv := []string{"git", "--git-dir=" + filepath.Join(l.work, ".git"), "config", "receive.denyCurrentBranch", "ignore"}
	t.Run("argv/F5_pin_git_dir_config", func(t *testing.T) {
		h55RequireRefused(t, l.guard.InspectExec(argv, l.mount), strings.Join(argv, " "))
	})
}
