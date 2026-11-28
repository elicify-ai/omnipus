package sandbox_test

// H55 (#1960 S1), rows R3, R4, R5 and R8 of the architect's ruling
// (/workspace/logs/1960/spec-s1/ruling-H55.md), at the level of the guard's
// decision: which commands the evidence guard refuses when a protected work
// folder is registered and the working directory is an approved mount beside it.
//
// EXPECTED RED until the guard fix of that ruling (its Rule A, the receive-side
// override, and its Rule B, the push target) lands. Four leaves here are RED, and
// each of their rows fails on what the guard decides today (allowed), never on a
// setup error: h55Setup shows in the same layout that the guard is armed, and
// gitguard_h55_allowed_test.go shows the ordinary idioms are still allowed:
//   - TestGitGuard_H55_ReceiveSideOverrideRefusedInScripts (Rule A, R1 and R3);
//   - TestGitGuard_H55_ReceiveSideOverrideRefusalNamesTheRule (Rule A's reason);
//   - TestGitGuard_H55_PushTargetInsideWorkFolderRefusedInScripts (Rule B, R4/R5);
//   - TestGitGuard_H55_DirectArgvRefusedLikeTheScriptForm (R8).
//
// The fifth leaf, TestGitGuard_H55_PushTargetLiteralWorkPathStaysRefusedInScripts,
// is NOT red: a script that names the work folder by a literal absolute or relative
// path is already refused today, so those rows are regression pins and sit in a
// leaf of their own. The round-8 entry of the test disputes names every row with
// its saved run log. The same rows through the real loop, with the evidence
// repository read back, are in pkg/agent/programs_git_mount_h55_refusals_test.go.
//
// Oracles: ruling-H55 section (1) (Rule A and Rule B, including the trigger list
// and the "plain names and network URLs pass" clause) and section (3) rows R3, R4,
// R5 and R8. Nothing here is taken from a run of the guard.
import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// h55RequireRefused fails unless the decision is a refusal.
func h55RequireRefused(t *testing.T, d sandbox.ExecDecision, what string) {
	t.Helper()
	if d.Allowed {
		t.Fatalf("ruling-H55: %s must be refused by the evidence guard (the file header names the leaves that are expected RED until the ruling's fix lands), got allowed", what)
	}
}

// R3 (Rule A), by script. The remote is a plain name wherever the row is about
// the override, so Rule B cannot be what refuses it.
func TestGitGuard_H55_ReceiveSideOverrideRefusedInScripts(t *testing.T) {
	l := h55Setup(t)
	rows := []struct{ name, script string }{
		{"R1_override_after_the_refspec", `git push --force "$(dirname $PWD)/work" HEAD:master --receive-pack='` + h55Program + `'`},
		{"R3_receive_pack_glued", `git push --receive-pack='` + h55Program + `' ev master`},
		{"R3_receive_pack_spaced", `git push --receive-pack '` + h55Program + `' ev master`},
		{"R3_receive_pack_after_the_refspec", `git push ev master --receive-pack='` + h55Program + `'`},
		{"R3_exec_glued", `git push --exec='` + h55Program + `' ev master`},
		{"R3_exec_spaced", `git push --exec '` + h55Program + `' ev master`},
		{"R3_remote_receivepack_on_the_command_line", `git -c remote.ev.receivepack='` + h55Program + `' push ev master`},
		{"R3_remote_receivepack_set_by_its_own_config_call", `git config remote.ev.receivepack '` + h55Program + `'`},
		{"R3_send_pack_receive_pack", `git send-pack --receive-pack='` + h55Program + `' ev master`},
		// The remaining triggers of Rule A's list: any receive.* setting on the
		// command line, and the three environment-config markers in the script text.
		{"A_receive_setting_on_the_command_line", `git -c receive.unpackLimit=1 push ev master`},
		{"A_git_config_parameters_marker", `GIT_CONFIG_PARAMETERS= git push ev master`},
		{"A_git_config_count_marker", `GIT_CONFIG_COUNT=0 git push ev master`},
		{"A_git_config_key_marker", `GIT_CONFIG_KEY_0=x git push ev master`},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			h55RequireRefused(t, l.guard.InspectExec([]string{"sh", "-c", tc.script}, l.mount), tc.script)
		})
	}
}

// Rule A names its reason: the refusal says it is a receive-side override. One
// canonical form carries the check, so the wording is pinned once, not per row.
func TestGitGuard_H55_ReceiveSideOverrideRefusalNamesTheRule(t *testing.T) {
	l := h55Setup(t)
	script := `git push --receive-pack='` + h55Program + `' ev master`
	d := l.guard.InspectExec([]string{"sh", "-c", script}, l.mount)
	h55RequireRefused(t, d, script)
	if !strings.Contains(d.PolicyRule+" | "+d.Reason, "receive-side override (H55)") {
		t.Fatalf("ruling-H55 Rule A: the refusal must name the receive-side override (H55); got rule %q reason %q", d.PolicyRule, d.Reason)
	}
}

// R4 and R5 (Rule B), by script: the remote is a path built at run time, or a path
// into the work folder that the guard's literal-path check does not read (a
// file:// URL, a send-pack target).
func TestGitGuard_H55_PushTargetInsideWorkFolderRefusedInScripts(t *testing.T) {
	l := h55Setup(t)
	rows := []struct{ name, script string }{
		{"R4_computed_path_command_substitution", `git push "$(dirname $PWD)/work" HEAD:refs/heads/x`},
		{"R4_computed_path_backticks", "git push `dirname $PWD`/work HEAD:refs/heads/x"},
		{"B_run_time_variable_with_a_slash", `git push "$W/work" HEAD:refs/heads/x`},
		{"B_tilde_path", `git push ~/work HEAD:refs/heads/x`},
		{"B_glob_path_starting_with_a_dot", `git push ../w* HEAD:refs/heads/x`},
		{"B_backslash_path_starting_with_a_dot", `git push ../wo\rk HEAD:refs/heads/x`},
		{"R5_file_url", "git push file://" + l.work + " HEAD:refs/heads/x"},
		{"B_send_pack_absolute_work_path", "git send-pack " + l.work + " HEAD:refs/heads/x"},
		{"B_send_pack_computed_path", `git send-pack "$(dirname $PWD)/work" HEAD:refs/heads/x`},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			h55RequireRefused(t, l.guard.InspectExec([]string{"sh", "-c", tc.script}, l.mount), tc.script)
		})
	}
}

// R5, the literal absolute and relative paths of the work folder, by script. NOT
// red: the guard's literal-path and relative-resolution check already refuses
// every one of these today. They are regression pins that must still hold once
// Rule B lands (the ruling lists them under Rule B); a fix that replaced that check
// and missed them would turn this leaf red.
func TestGitGuard_H55_PushTargetLiteralWorkPathStaysRefusedInScripts(t *testing.T) {
	l := h55Setup(t)
	rows := []struct{ name, script string }{
		{"R5_absolute_work_path", "git push " + l.work + " HEAD:refs/heads/x"},
		{"R5_absolute_path_inside_the_work_folder", "git push " + l.work + "/.git HEAD:refs/heads/x"},
		{"R5_relative_work_path", "git push ../work HEAD:refs/heads/x"},
		{"B_option_before_the_remote", "git push --force-with-lease " + l.work + " HEAD:refs/heads/x"},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			h55RequireRefused(t, l.guard.InspectExec([]string{"sh", "-c", tc.script}, l.mount), tc.script)
		})
	}
}

// R8: the same decisions for a direct git argv, as for the same command through
// "bash -c".
func TestGitGuard_H55_DirectArgvRefusedLikeTheScriptForm(t *testing.T) {
	l := h55Setup(t)
	rows := []struct {
		name string
		argv []string
	}{
		{"R8_override_option_with_the_work_path", []string{"git", "push", "--force", "--receive-pack=" + h55Program, l.work, "HEAD:master"}},
		{"R8_work_path_only", []string{"git", "push", l.work, "HEAD:refs/heads/x"}},
		{"R8_receive_pack_spaced", []string{"git", "push", "--receive-pack", h55Program, "ev", "master"}},
		{"R8_exec_glued", []string{"git", "push", "--exec=" + h55Program, "ev", "master"}},
		{"R8_remote_receivepack_on_the_command_line", []string{"git", "-c", "remote.ev.receivepack=" + h55Program, "push", "ev", "master"}},
		{"R8_receive_setting_on_the_command_line", []string{"git", "-c", "receive.unpackLimit=1", "push", "ev", "master"}},
		{"R8_remote_receivepack_set_by_its_own_config_call", []string{"git", "config", "remote.ev.receivepack", h55Program}},
		{"R8_send_pack_receive_pack", []string{"git", "send-pack", "--receive-pack=" + h55Program, "ev", "master"}},
		{"R8_send_pack_work_path", []string{"git", "send-pack", l.work, "HEAD:refs/heads/x"}},
		{"R8_relative_work_path", []string{"git", "push", "../work", "HEAD:refs/heads/x"}},
		{"R8_file_url", []string{"git", "push", "file://" + l.work, "HEAD:refs/heads/x"}},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			h55RequireRefused(t, l.guard.InspectExec(tc.argv, l.mount), strings.Join(tc.argv, " "))
		})
	}
}
