package clicredentialss1_test

import "testing"

// Rev-8 S-136 explicitly requires the actual credentials CLI AND a running
// gateway on the same home. The re-exec'd credentials test process is useful
// lower-level lock coverage, but cannot prove this path or a real CLI exit.
// No production init/seam is added by the test author to pretend otherwise.
func TestProgramsRealCLIAndGateway(t *testing.T) {
	for _, row := range []struct{ name, missing string }{
		{"S136_actual_CLI_held_read_gateway_paste_release", "test-built actual omnipus credentials set OTHER y, running gateway, synchronized inside-read/signal-file barrier on one home; paste must remain pending, then token/generation/metadata/receipt and OTHER survive"},
		{"S146_actual_CLI_busy_nonzero_no_late_write", "actual credentials CLI under the six-second cross-process holder; typed busy result must become a real nonzero CLI exit after five seconds, no late write two seconds after release; repeat with a one-second holder for success"},
		{"S146_crossprocess_gateway_503_no_check_no_late_write", "running gateway paste while a separate actual CLI owns the store transaction; HTTP 503 in 4.5–5.5 seconds, same token/generation/metadata/receipt, zero extra checks and no late publication; one-second-release positive"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Fatalf("BLOCKED: %s is not connected to the real CLI/gateway test-build harness — required by FR-049/S-136/S-146; Store-only child tests and H-17 are not substitutes", row.missing)
		})
	}
}
