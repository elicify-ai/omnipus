package programs

import "testing"

// These are missing integration oracles, not substitutes for runtime coverage.
// Unit Close/Write and Attach->real-cap composition do not drive the agent's
// background transport, retention, JSON encoding or interruption delivery.
func TestProgramMasking_RuntimeMissing(t *testing.T) {
	for _, row := range []struct{ name, boundary string }{
		{"S102_background_JSON_read", "real background read before JSON encoding, quote/backslash token and its JSON spelling"},
		{"S103_Dev_exec_reaches_instrumented_cap", "Dev foreground ExecTool path with injected program eligibility and cap instrument; separate Attach composition is not runtime wiring"},
		{"S104_background_retention_and_timed_reads", "real background retention cap and first/completed reads while the child is held"},
		{"D9_row1_exit0_stdout7_foreground", "real foreground exit with seven stdout bytes"},
		{"D9_row2_exit0_stdout8_foreground", "real foreground exit with eight stdout bytes"},
		{"D9_row3_SIGKILL_stderr10_foreground", "real SIGKILL after ten stderr bytes and foreground delivery"},
		{"D9_row4_timeout_stdout10_partial_result", "real foreground timeout after ten stdout bytes and Partial output before timeout"},
		{"D9_row5_exit1_stdout20_background_completion", "real exit 1 after twenty stdout bytes, published/continuation/transcript sinks"},
		{"D9_row6_sessionkill_stderr20_background_read", "real bash session kill after twenty stderr bytes and read after kill"},
		{"D9_row7_readerror_stdout10_background_read", "injected pipe read error after ten stdout bytes and real background read"},
		{"D9_row8_middle10_exit0_foreground", "real exit 0 after middle bytes 5 through 14 and foreground result"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Fatalf("BLOCKED: eligibility/Attach runtime is not bound to %s — required by S1 FR-020/S-102/S-103/S-104/S-130; direct masking-writer tests are not this integration", row.boundary)
		})
	}
}
