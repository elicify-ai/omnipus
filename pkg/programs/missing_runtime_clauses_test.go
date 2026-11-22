package programs

import "testing"

// Individually named missing clauses, not claimed cross-coverage by the
// provenance algebra, REST lifecycle state, or direct writer unit tests.
func TestProgramRuntime_MissingS1Clauses(t *testing.T) {
	for _, row := range []struct{ name, requirement string }{
		{"S05_failed_save_next_printenv_keeps_old_token", "failed store write followed by Dev printenv still uses the previous token"},
		{"S17_on_but_not_connected_next_printenv_is_empty", "switched-on disconnected GitHub gives Dev no GH_TOKEN"},
		{"S36_no_check_next_printenv_uses_saved_token", "custom entry without identity check supplies its token to the next eligible child"},
		{"S51_replace_next_printenv_uses_new_token", "generation 2 replacement supplies only the new token to the next command"},
		{"S53_disconnect_next_printenv_is_empty_switch_stays_on", "disconnect with Dev still on gives its next child no GH_TOKEN"},
		{"S44_running_environment_survives_setting_change_next_child_withheld", "hold a running child, switch the origin setting off, release it unchanged, then launch a second child with token withheld"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Fatalf("BLOCKED: production program eligibility/environment attachment is not wired beyond the Attach RED stub — required by %s: %s; REST state and pure decision rows do not execute this clause", row.name, row.requirement)
		})
	}
}
