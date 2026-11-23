package programs

import "testing"

// These are missing integration oracles, not substitutes for runtime coverage.
// Unit Close/Write and Attach->real-cap composition do not drive the agent's
// background transport, retention, JSON encoding or interruption delivery.
func TestProgramMasking_RuntimeMissing(t *testing.T) {
	for _, row := range []struct{ name, boundary string }{
		// S102 and S104's timed first/completed reads migrated intact to
		// pkg/agent::TestProgramMasking_ActualBackground. The cap observation
		// is separate below; neither the unit writer nor timed reads imply it.
		{"S103_Dev_exec_reaches_instrumented_cap", "test-build 80-byte cap and recording sink at the actual foreground capture input; normal-cap companion is real in pkg/agent, but no injectable pre-cap recorder exists at this boundary"},
		// S104_background_retention_cap migrated intact to
		// pkg/agent::TestProgramMasking_ActualRetentionCap: actual loop-wired
		// background start and registered tool read before the agent's result
		// limit; exact prefix, real truncation marker and masked token position.
		// D9 rows1/2/3/8 migrated intact to
		// pkg/agent::TestProgramMasking_ActualInterruptedForeground: the real
		// seven/eight/ten-byte and middle-ten children, actual SIGKILL, a
		// child-ran instrument and no surviving eight-byte sentinel window.
		// D9 row4 migrated intact to pkg/agent::TestProgramMasking_ActualBackgroundEdges;
		// rows5/6 to pkg/agent::TestProgramMasking_ActualBackground. All use real
		// children and specified timeout/exit/kill plus the actual named sinks.
		{"D9_row7_readerror_stdout10_background_read", "injected pipe read error after ten stdout bytes and real background read"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Fatalf("BLOCKED: this integration fixture still needs %s — required by S1 FR-020/S-103/S-104/S-130; Attach is implemented, but unit masking and other child paths do not establish this boundary", row.boundary)
		})
	}
}
