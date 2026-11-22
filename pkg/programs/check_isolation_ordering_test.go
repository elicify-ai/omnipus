// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: TestProgramCheck_Isolation (order 17) — S-07 (the check runs in
// a clean environment: the saved token, a temporary HOME, no ambient
// GH_TOKEN) and S-08 (a program in an agent-writable folder is not run);
// TestProgramCheck_Ordering (order 19) — S-90 (a stale-generation check
// never publishes), S-114 (two checks of one generation in reverse order),
// S-115 (restart while checking), S-116 (check-relevant edit starts a new
// check), S-131 (unrelated edits do not strand the row) and S-139 (a delayed
// launch cannot stop the newer check).
//
// Every scenario needs the account-check runner (the fake `gh` harness with
// its barriers, the process-group stop, the persisted checking state and the
// generation guard) — FR-005/FR-047, I-3's check seam. That runner does not
// exist at RED: each scenario reports BLOCKED naming it — never a skip —
// with the spec's verbatim expectations recorded beside it for the same
// test author's GREEN reconciliation. CHECK (a separate instance) owns the
// mutation probes against these.
//
// Oracle: US-1/US-4/US-7 scenario texts, the message table, FR-005/FR-047.
// Expected values come from the spec, never from the runner.

package programs

import "testing"

// s1CheckRunnerSeam is the missing seam every scenario in this file needs.
const s1CheckRunnerSeam = "the account check runner (fake gh harness with barriers, process-group stop, persisted checking state, generation guard — FR-005/FR-047) is not implemented"

func TestProgramCheck_Isolation(t *testing.T) {
	t.Run("S07_check_isolation_clean_environment", func(t *testing.T) {
		// GREEN asserts here (spec S-07): with GH_TOKEN=gateway_ambient_tok
		// and HOME=/home/person on the gateway environment, the recorded
		// check environment has GH_TOKEN=tok_valid_12345, a temporary HOME
		// that no longer exists afterwards, and no gateway_ambient_tok.
		t.Fatalf("BLOCKED: %s — required by S-07's recorded-environment assertion", s1CheckRunnerSeam)
	})

	t.Run("S08_program_in_agent_writable_folder_not_run", func(t *testing.T) {
		// GREEN asserts here (spec S-08): the only gh on the gateway PATH
		// inside a workspace folder → the state ends saved_not_checked with
		// a message about where the program is installed, and the program
		// was not started.
		t.Fatalf("BLOCKED: %s — required by S-08's not-started assertion (the PATH policy that refuses agent-writable folders, FR-005)", s1CheckRunnerSeam)
	})
}

func TestProgramCheck_Ordering(t *testing.T) {
	t.Run("S90_stale_generation_check_never_publishes", func(t *testing.T) {
		// GREEN asserts here (spec S-90): A's check held at a barrier after
		// printing account-a; B saved (generation 2) returns account-b; then
		// A releases → the row shows account-b, account-a is never stored,
		// and an audit row records A's check as superseded.
		t.Fatalf("BLOCKED: %s — required by S-90's never-publishes assertion", s1CheckRunnerSeam)
	})

	t.Run("S114_two_checks_reverse_order_older_stopped", func(t *testing.T) {
		// GREEN asserts here (spec S-114): check A held at a barrier after
		// printing octocat; check B starts (fake gh now exits 1 with
		// HTTP 401: Bad credentials) and finishes; A's barrier releases →
		// state check_failed from B, A's process group was stopped when B
		// started, A's result audited superseded and never published.
		t.Fatalf("BLOCKED: %s — required by S-114's stopped-process-group assertion", s1CheckRunnerSeam)
	})

	t.Run("S115_restart_while_checking_reruns_a_fresh_check", func(t *testing.T) {
		// GREEN asserts here (spec S-115): persisted state checking at the
		// kill; on restart a fresh check for the current generation runs and
		// the state ends connected within the check timeout + 5 s.
		t.Fatalf("BLOCKED: %s — required by S-115's restart-rerun assertion", s1CheckRunnerSeam)
	})

	t.Run("S116_check_relevant_edit_starts_new_check", func(t *testing.T) {
		// GREEN asserts here (spec S-116): custom Acme connected, check held
		// at a barrier; the person edits the account check args
		// (check_revision +1) → the old result is discarded, a new check
		// with the new args runs automatically, and the row ends in that
		// check's state.
		t.Fatalf("BLOCKED: %s (and the check_revision field) — required by S-116's automatic-new-check assertion", s1CheckRunnerSeam)
	})

	t.Run("S131_unrelated_edit_does_not_strand_the_row", func(t *testing.T) {
		// GREEN asserts here (spec S-131): editing only the display name,
		// then only the network sites, leaves check_revision unchanged, the
		// held result publishes and the row ends connected as acme-user with
		// no restart and no second check; editing token_variables instead
		// discards the held result and runs exactly one new check.
		t.Fatalf("BLOCKED: %s (and check_revision's edit-relevance rule) — required by S-131's publish-held-result assertion", s1CheckRunnerSeam)
	})

	t.Run("S139_delayed_launch_cannot_stop_the_newer_check", func(t *testing.T) {
		// GREEN asserts here (spec S-139): paste A commits generation 1 and
		// reserves its check, pauses between "commit and reserve" and
		// "launch" (the spec's barrier point); paste B commits generation 2,
		// reserves and launches; A's launch resumes → A's launch starts no
		// process and stops nothing, B's check runs to a terminal state, and
		// the audit shows A's reservation superseded once. (Test-disputes E:
		// the between-commit-and-launch barrier is the one scenario needing
		// a production-side hook; GREEN lands it with the check runner.)
		t.Fatalf("BLOCKED: %s (and the commit/reserve-vs-launch barrier) — required by S-139's no-process assertion", s1CheckRunnerSeam)
	})
}
