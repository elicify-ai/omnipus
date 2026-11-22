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
// Every scenario here is runtime behaviour through the real agent exec
// harness: program variables attached to a child (FR-011), the managed git
// helper chain (FR-013), the git version probe (FR-014) and the per-run
// network grants (FR-015). None of those seams exists at RED — the agent
// environment builder cannot hand program variables to any harness — so
// each scenario reports BLOCKED naming its seam, never a skip, with the
// spec's verbatim expectations recorded beside it for the same test
// author's GREEN reconciliation.
//
// Oracle: US-3 scenario texts, the message table, dataset D-3 (S-26's
// program-word spellings). Expected values come from the spec, never from
// the harness.

package programs

import "testing"

// s1EnvSeam is the missing seam all four orders share.
const s1EnvSeam = "the agent environment builder cannot attach program variables to the exec harness (FR-011 runtime)"

func TestProgramEnv_PerModeAndTool(t *testing.T) {
	t.Run("S11_only_switched_on_agent_gets_the_token", func(t *testing.T) {
		// GREEN asserts here (spec S-11): GitHub connected, on for Dev only;
		// Dev's printenv GH_TOKEN | wc -c is 16, Mia's is 0.
		t.Fatalf("BLOCKED: %s — required by S-11's per-agent outputs", s1EnvSeam)
	})

	t.Run("S18_gh_uses_the_token", func(t *testing.T) {
		// GREEN asserts here (spec S-18): Dev runs `gh api user --jq .login`
		// → octocat (the fake gh answers from the program token).
		t.Fatalf("BLOCKED: %s — required by S-18's gh run", s1EnvSeam)
	})

	t.Run("S25_god_mode_token_available_no_egress_token", func(t *testing.T) {
		// GREEN asserts here (spec S-25): God Mode, GitHub on for Dev →
		// printenv is 16 and no egress token is created.
		t.Fatalf("BLOCKED: %s — required by S-25's God Mode run", s1EnvSeam)
	})

	t.Run("S28_unmatched_program_word_no_grant_token_still_available", func(t *testing.T) {
		// GREEN asserts here (spec S-28): no entry has the word aws → the
		// network approval is requested exactly as before and names no
		// GitHub site; GH_TOKEN is in that command's environment.
		t.Fatalf("BLOCKED: %s — required by S-28's approval-and-env assertions", s1EnvSeam)
	})

	t.Run("S29_environment_setup_children_get_the_token", func(t *testing.T) {
		// GREEN asserts here (spec S-29): environment_setup with a script
		// running printenv GH_TOKEN | wc -c → 16.
		t.Fatalf("BLOCKED: %s — required by S-29's script output", s1EnvSeam)
	})

	t.Run("S31_store_unavailable_notice_echo_still_works", func(t *testing.T) {
		// GREEN asserts here (spec S-31): store set to fail on read →
		// `echo ok` outputs ok plus the store-unavailable notice, and no
		// program variable is set.
		t.Fatalf("BLOCKED: %s (and the store-read failure injection) — required by S-31's notice", s1EnvSeam)
	})

	t.Run("S89_script_calling_gh_internally", func(t *testing.T) {
		// GREEN asserts here (spec S-89): ./deploy.sh runs gh api user →
		// octocat in God Mode; in Auto the GitHub sites are NOT auto-granted
		// and the network is handled as today.
		t.Fatalf("BLOCKED: %s (and the per-run network grants) — required by S-89's two-mode assertion", s1EnvSeam)
	})
}

func TestProgramGitHelper_ResetChain(t *testing.T) {
	t.Run("S19_git_uses_stored_token_ignores_existing_helper", func(t *testing.T) {
		// GREEN asserts here (spec S-19): a global git config helper for
		// https://github.com answering old-account/old_pw_123456 → git
		// credential fill receives x-access-token and the token (shown as
		// [FILTERED] in any record), and the old helper logged no get, store
		// or erase.
		t.Fatalf("BLOCKED: %s (and the managed helper chain, FR-013) — required by S-19's fill assertion", s1EnvSeam)
	})

	t.Run("S20_no_token_in_config_or_arguments", func(t *testing.T) {
		// GREEN asserts here (spec S-20): git push to a local HTTPS receiver
		// that only accepts x-access-token:tok_valid_12345 → the push
		// succeeds, the receiver recorded x-access-token, and no git config
		// file, remote URL or observed argument list contains the token.
		t.Fatalf("BLOCKED: %s — required by S-20's no-token-anywhere assertion", s1EnvSeam)
	})

	t.Run("S88_new_git_gets_the_helper", func(t *testing.T) {
		// GREEN asserts here (spec S-88): the child's PATH git reports 2.43
		// → GIT_CONFIG_COUNT=2 and the two managed entries are present.
		t.Fatalf("BLOCKED: %s — required by S-88's managed-entries assertion", s1EnvSeam)
	})
}

func TestProgramGitVersion_ChildExecutable(t *testing.T) {
	t.Run("S27_old_git_in_child_path_gets_the_old_git_message", func(t *testing.T) {
		// GREEN asserts here (spec S-27): gateway git 2.43, workspace git
		// 2.30.2 first on the child's PATH → no GIT_CONFIG_COUNT in the
		// environment, the result contains the old-git message with 2.30.2,
		// and the probe ran inside Dev's sandbox (recorded by the fake git).
		t.Fatalf("BLOCKED: %s (and the version probe, FR-014) — required by S-27's old-git message", s1EnvSeam)
	})

	t.Run("S87_explicit_git_path_is_the_one_probed", func(t *testing.T) {
		// GREEN asserts here (spec S-87): /opt/old/git reports 2.30.2, PATH
		// git 2.43 → Dev runs /opt/old/git push → no managed git entries are
		// set and the notice names 2.30.2.
		t.Fatalf("BLOCKED: %s — required by S-87's explicit-path probe", s1EnvSeam)
	})
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
		// GREEN asserts here (spec S-26, dataset D-3): every row's command
		// spelling that names the program word gets the token; spellings
		// that do not, do not.
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
