// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: TestProgramEnv_PerModeAndTool (order 22) — S-11, S-18, S-25,
// S-28, S-29, S-31, S-89; TestProgramGitHelper_ResetChain (order 23) —
// S-19, S-20, S-88; TestProgramGitVersion_ChildExecutable (order 24) —
// S-27, S-87; TestProgramNetwork_AutoAsk (order 25) — S-21, S-22, S-23,
// S-24, S-26, S-84.
//
// The runtime attachment, git helper and version probe now exist. Executable
// migrated rows below name the real agent/tool tests that replaced their original
// unconditional markers. Remaining rows are incomplete author fixtures, not a
// claim that every production seam is still absent. Each stays loud until its
// actual scenario is driven; a passing lower boundary does not discharge it.
//
// Oracle: US-3 scenario texts, the message table, dataset D-3 (S-26's
// program-word spellings). Expected values come from the spec, never from
// the harness.

package programs

import "testing"

// These remaining scenarios have no complete mode/grant/receiver fixture yet.
// This is unfinished test-author work, not an accepted requirement deferral.
const s1EnvSeam = "this row's full mode/network/child fixture is not yet implemented in the test harness; existing Attach and child tests do not prove it"

func TestProgramEnv_PerModeAndTool(t *testing.T) {
	// S11_only_switched_on_agent_gets_the_token moved, without removing its
	// oracle, to pkg/agent::TestProgramEnv_PerModeAndTool. It now runs the
	// actual loop-wired child and asserts both exact byte counts.

	// S18_gh_uses_the_token is also executable in pkg/agent's real-loop
	// TestProgramEnv_PerModeAndTool; its oracle remains exactly octocat.

	t.Run("S25_god_mode_token_available_no_egress_token", func(t *testing.T) {
		// GREEN asserts here (spec S-25): God Mode, GitHub on for Dev →
		// printenv is 16 and no egress token is created.
		t.Fatal("BLOCKED: S25's actual God Mode count16 is exercised in pkg/agent::TestProgramRuntime_OngoingOriginSetting; an independent real-proxy token-creation observation fixture is still missing for the mandatory no-egress-token clause — required by S-25")
	})

	t.Run("S28_unmatched_program_word_no_grant_token_still_available", func(t *testing.T) {
		// GREEN asserts here (spec S-28): no entry has the word aws → the
		// network approval is requested exactly as before and names no
		// GitHub site; GH_TOKEN is in that command's environment.
		t.Fatalf("BLOCKED: %s — required by S-28's approval-and-env assertions", s1EnvSeam)
	})

	// S29_environment_setup_children_get_the_token moved intact to
	// pkg/agent::TestProgramEnvironmentSetup_ActualChild: real run/poll/read,
	// an executed-child instrument and exact byte count, not a marker.

	// S31_store_unavailable_notice_echo_still_works migrated intact to
	// pkg/agent::TestProgramStoreFailure_ActualChildren: real locked-store
	// failure after a positive runtime control, actual echo and count0, exact
	// unavailable notice. A service-only read assertion does not replace it.

	t.Run("S89_script_calling_gh_internally", func(t *testing.T) {
		// GREEN asserts here (spec S-89): ./deploy.sh runs gh api user →
		// octocat in God Mode; in Auto the GitHub sites are NOT auto-granted
		// and the network is handled as today.
		t.Fatalf("BLOCKED: %s (and the per-run network grants) — required by S-89's two-mode assertion", s1EnvSeam)
	})
}

func TestProgramGitHelper_ResetChain(t *testing.T) {
	// S19_git_uses_stored_token_ignores_existing_helper migrated intact to
	// pkg/agent::TestProgramGitHelper_ActualChild: real git and global helper,
	// positive old-helper control, exact credential fields and no operations.

	t.Run("S20_no_token_in_config_or_arguments", func(t *testing.T) {
		// GREEN asserts here (spec S-20): git push to a local HTTPS receiver
		// that only accepts x-access-token:tok_valid_12345 → the push
		// succeeds, the receiver recorded x-access-token, and no git config
		// file, remote URL or observed argument list contains the token.
		t.Fatalf("BLOCKED: %s — required by S-20's no-token-anywhere assertion", s1EnvSeam)
	})

	// S88_new_git_gets_the_helper migrated intact to
	// pkg/tools::TestProgramGitEnv_RealExecBoundary: actual ExecTool/Resolve/
	// probe/Attach/child boundary, explicitly not full-loop evidence-guard
	// admission. Chief-authorized boundary; the push-through-guard row is UAT.
}

func TestProgramGitVersion_ChildExecutable(t *testing.T) {
	t.Run("S27_old_git_in_child_path_gets_the_old_git_message", func(t *testing.T) {
		// GREEN asserts here (spec S-27): gateway git 2.43, workspace git
		// 2.30.2 first on the child's PATH → no GIT_CONFIG_COUNT in the
		// environment, the result contains the old-git message with 2.30.2,
		// and the probe ran inside Dev's sandbox (recorded by the fake git).
		t.Fatalf("BLOCKED: %s (and the version probe, FR-014) — required by S-27's old-git message", s1EnvSeam)
	})

	// S87_explicit_git_path_is_the_one_probed migrated intact to
	// pkg/agent::TestProgramGitVersion_ActualChild: distinct absolute old/git
	// and PATH git, real recorded probe before command, exact old-git notice.
}

func TestProgramNetwork_AutoAsk(t *testing.T) {
	t.Run("S21_auto_grants_profile_sites_revokes_after_run", func(t *testing.T) {
		// GREEN asserts here (spec S-21): gh api user → no approval; the
		// proxy allows api.github.com for that run's token; the token is
		// revoked after the run.
		t.Fatalf("BLOCKED: %s (and the egress proxy grants, FR-015) — required by S-21", s1EnvSeam)
	})

	t.Run("S22_other_hosts_still_asked", func(t *testing.T) {
		// GREEN asserts here (spec S-22): `gh api user && curl
		// https://example.org` → exactly one approval, naming example.org
		// only.
		t.Fatalf("BLOCKED: %s — required by S-22's single-approval assertion", s1EnvSeam)
	})

	t.Run("S23_deny_wins", func(t *testing.T) {
		// GREEN asserts here (spec S-23): rule {action: deny, binary: gh} →
		// refused with the existing deny message; not run.
		t.Fatalf("BLOCKED: %s — required by S-23's deny-first assertion", s1EnvSeam)
	})

	t.Run("S24_ask_grants_after_approval", func(t *testing.T) {
		// GREEN asserts here (spec S-24): Ask mode, the person approves
		// `gh api user` → no second prompt; GitHub sites allowed for that
		// run only.
		t.Fatalf("BLOCKED: %s — required by S-24's after-approval assertion", s1EnvSeam)
	})

	t.Run("S26_program_word_spellings", func(t *testing.T) {
		// GREEN asserts here (spec S-26, dataset D-3): each row's command
		// spelling decides the SITE GRANT — spellings that name the program
		// word grant the entry's network sites for the run (rows 1–5, 9),
		// spellings that do not, do not (rows 6, 7, 8, 10). CORRECTED (CHECK
		// F17): the grant is never about token availability — GH_TOKEN is in
		// the command's environment regardless of a word match (S-28's
		// `aws s3 ls` carries it; S-89's script does too); only the
		// auto-approval is gated on the matcher.
		t.Fatalf("BLOCKED: %s (and the program-word matcher) — required by S-26's D-3 rows", s1EnvSeam)
	})

	t.Run("S84_shared_program_word_per_site_helper", func(t *testing.T) {
		// GREEN asserts here (spec S-84): custom GitLab (word git) and
		// GitHub both on for Dev → `git ls-remote https://gitlab.com/o/r`
		// needs no approval; the managed helper for gitlab.com answers with
		// the GitLab token and the one for github.com with the GitHub token.
		t.Fatalf("BLOCKED: %s — required by S-84's per-site helpers", s1EnvSeam)
	})
}
