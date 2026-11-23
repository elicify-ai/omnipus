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

	// S28_unmatched_program_word_no_grant_token_still_available migrated intact
	// to pkg/agent::TestProgramEnv_ActualUnmatchedAndScript: actual ordinary and
	// connected-program aws children, exact unchanged approval arguments, token
	// verification inside the real child, and real proxy's denied GitHub host.

	// S29_environment_setup_children_get_the_token moved intact to
	// pkg/agent::TestProgramEnvironmentSetup_ActualChild: real run/poll/read,
	// an executed-child instrument and exact byte count, not a marker.

	// S31_store_unavailable_notice_echo_still_works migrated intact to
	// pkg/agent::TestProgramStoreFailure_ActualChildren: real locked-store
	// failure after a positive runtime control, actual echo and count0, exact
	// unavailable notice. A service-only read assertion does not replace it.

	// S89_script_calling_gh_internally migrated intact to
	// pkg/agent::TestProgramEnv_ActualUnmatchedAndScript: actual deploy.sh in
	// explicitly active God Mode gives octocat; Auto reaches the ordinary
	// script approval and the real proxy observes no GitHub auto-grant.
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
	// S21/S22/S23/S24 migrated intact to
	// pkg/agent::TestProgramNetwork_ActualAutoAsk. The actual loop, modes,
	// approval adapter, child environment, public EgressProxy policy and
	// foreground revocation execute. Canceled upstream requests distinguish
	// allowed from denied hosts without public calls; this is not a claim of
	// a successful live network request. A separate grant/revoke instrument
	// proves that observer sees both policy outcomes. S23 retains the ordinary
	// deny control; S24 holds actual approval before admitting the real child.

	t.Run("S26_program_word_spellings", func(t *testing.T) {
		// GREEN asserts here (spec S-26, dataset D-3): each row's command
		// spelling decides the SITE GRANT — spellings that name the program
		// word grant the entry's network sites for the run (rows 1–5, 9),
		// spellings that do not, do not (rows 6, 7, 8, 10). CORRECTED (CHECK
		// F17): the grant is never about token availability — GH_TOKEN is in
		// the command's environment regardless of a word match (S-28's
		// `aws s3 ls` carries it; S-89's script does too); only the
		// auto-approval is gated on the matcher.
		t.Fatal("BLOCKED: S26 D3 rows1-10 now execute the actual matcher, child, proxy grants/revocation and ordinary approval controls in pkg/agent::TestProgramNetwork_ActualSpellingMatrix, but that fixture lacks the BDD Background's enforcing kernel sandbox; three no_sandbox_ask mismatches cannot certify the no-prompt clause. Full sandboxed acceptance remains unverified; individual dispute H41 — required by S-26/FR-015")
	})

	// S84_shared_program_word_per_site_helper migrated intact to
	// pkg/agent::TestProgramNetwork_ActualSharedGitWord: actual Auto loop and
	// child with the exact git ls-remote command, both checked saved programs,
	// real installed git credential fill for both hosts, actual run-token
	// proxy grants for the union, zero approval and revocation after the run.
	// Only the external remote executable/model edges are controlled; canceled
	// upstream probes certify policy authorization, not successful live fetches.
}
