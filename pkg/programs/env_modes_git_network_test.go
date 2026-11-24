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

// Each remaining scenario names its own unresolved observation/fixture boundary
// and dated dispute. None is an accepted requirement deferral or a product PASS.

func TestProgramEnv_PerModeAndTool(t *testing.T) {
	// S11_only_switched_on_agent_gets_the_token moved, without removing its
	// oracle, to pkg/agent::TestProgramEnv_PerModeAndTool. It now runs the
	// actual loop-wired child and asserts both exact byte counts.

	// S18_gh_uses_the_token is also executable in pkg/agent's real-loop
	// TestProgramEnv_PerModeAndTool; its oracle remains exactly octocat.

	t.Run("S25_god_mode_token_available_no_egress_token", func(t *testing.T) {
		// Full unchanged oracle moved to agent's tagged actual runtime
		// TestProgramNetwork_ActualMintHistory: child16, all mint entries0,
		// same actual observer first sees Auto's mint before real revocation.
		// This retained row is only the mandatory build/name guard.
		requireS1TaggedLeaf(t, "H43")
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
		// Original-name runtime oracle moved intact to agent's exact same
		// top/scenario name in programs_git_https_push_test.go after actual
		// pre-fix05ce8 RED and fixedcfce GREEN. It drives the real loop/HTTPS
		// transfer/account/config/URL/argv and both cwd-refusal controls.
		// This retained row is ONLY a loud exact build/name-selection guard;
		// execute the agent leaf separately for S20 product acceptance.
		requireS20RealLoopLeaf(t)
	})

	// S88_new_git_gets_the_helper migrated intact to
	// pkg/tools::TestProgramGitEnv_RealExecBoundary: actual ExecTool/Resolve/
	// probe/Attach/child boundary, explicitly not full-loop evidence-guard
	// admission. Chief-authorized boundary; the push-through-guard row is UAT.
}

// TestProgramGitVersion_ChildExecutable's scenarios are actual runtime tests:
// S27_old_git_in_child_path_gets_the_old_git_message migrated intact to
// pkg/agent::TestProgramGitVersion_ActualRuntimePATH/
// S27_old_runtime_git_probe_is_confined: actual gateway2.43 and discovered
// workspace runtime2.30.2; no managed config; complete old-git notice; actual
// probe-before-command record and protected-file kernel-denial controls in both.
// S87_explicit_git_path_is_the_one_probed remains in
// pkg/agent::TestProgramGitVersion_ActualChild (explicit path, distinct PATH).
// No empty passing wrapper is retained here.

// TestProgramNetwork_AutoAsk's scenarios are mapped below to actual agent tests;
// its now-empty marker wrapper is not retained as a false passing test.
// S21/S22/S23/S24 migrated intact to
// pkg/agent::TestProgramNetwork_ActualAutoAsk. The actual loop, modes,
// approval adapter, child environment, public EgressProxy policy and
// foreground revocation execute. Canceled upstream requests distinguish
// allowed from denied hosts without public calls; this is not a claim of
// a successful live network request. A separate grant/revoke instrument
// proves that observer sees both policy outcomes. S23 retains the ordinary
// deny control; S24 holds actual approval before admitting the real child.

// S26_program_word_spellings migrated intact to
// pkg/agent::TestProgramNetwork_ActualSpellingMatrix: all ten D3 spellings
// now run under an actual enforcing boot Apply with real per-turn child
// kernel-denial controls, exact0 approvals for matching spellings, exact
// ordinary approval comparisons for unmatched spellings, child16/0,
// actual proxy grant/denial and revocation. Sites, not token availability,
// depend on program-word matching. No empty passing successor.

// S84_shared_program_word_per_site_helper migrated intact to
// pkg/agent::TestProgramNetwork_ActualSharedGitWord: actual Auto loop and
// child with the exact git ls-remote command, both checked saved programs,
// real installed git credential fill for both hosts, actual run-token
// proxy grants for the union, zero approval and revocation after the run.
// Only the external remote executable/model edges are controlled; canceled
// upstream probes certify policy authorization, not successful live fetches.
