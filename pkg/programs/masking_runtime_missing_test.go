package programs

import "testing"

// The real S103 and D9row7 integration oracles now live in agent's tagged
// TestProgramMasking_ActualCaptureSeam and TestProgramMasking_ActualReadFaultSeam.
// These retained rows guard mandatory build/name selection only; they do not
// claim product runtime coverage. Untagged omission remains loudly blocked.
func TestProgramMasking_RuntimeMissing(t *testing.T) {
	for _, row := range []struct{ name, seam string }{
		// S102 and S104's timed first/completed reads migrated intact to
		// pkg/agent::TestProgramMasking_ActualBackground. The cap observation
		// is separate below; neither the unit writer nor timed reads imply it.
		{"S103_Dev_exec_reaches_instrumented_cap", "H44"},
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
		{"D9_row7_readerror_stdout10_background_read", "H45"},
	} {
		t.Run(row.name, func(t *testing.T) {
			requireS1TaggedLeaf(t, row.seam)
		})
	}
}
