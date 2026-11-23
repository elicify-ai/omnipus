// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: TestProgramMasking_Boundaries (order 29) — S-46, S-47, S-72,
// S-73, S-74; TestProgramSentinel_AllSinks (order 30) — S-48, S-50; and
// TestProgramExportImport (order 31) — S-49.
//
// These are the sink-side integration rows: masking at every boundary the
// runtime reaches (published chat messages, continuation input, transcript,
// persisted error notices, truncation at the notifier edge, audit files,
// export bundles) with the sentinel `sentinel_9f3c2a7e5b1d` connected and
// on for Dev and the general filter off. The runtime, background and export
// fixtures now exist for the migrated rows. S48's combined seven-sink campaign
// remains unfinished author work, not an assertion that no token can flow yet.
// A component test or static source guard is not whole-campaign evidence.
//
// Oracle: US-6 scenario texts, D-4/D-9 rows, FR-016/FR-020/FR-021/FR-022.

package programs

import "testing"

// One combined campaign is still needed even though individual sinks run.
const s1SinkSeam = "a combined real foreground/background/rotation/export fixture observing all seven specified sinks is not yet implemented in the test harness; individual sink receipts do not establish this campaign"

// TestProgramMasking_Boundaries and all its former marker rows were relocated,
// not removed: S46 is now pkg/agent::TestProgramMasking_Boundaries; S47/S74 are
// pkg/agent::TestProgramMasking_ActualBackgroundEdges; S72 is
// pkg/agent::TestProgramMasking_ActualBackground; S73 is
// pkg/agent::TestProgramMasking_ActualSuppressedError. The real loop, ongoing
// child, replacement/disconnect, continuation, provider requests, transcript,
// error notice and published events replace the original unconditional fatals.
// Do not emit a vacuous empty Test function in this package for the relocation.

func TestProgramSentinel_AllSinks(t *testing.T) {
	t.Run("S48_sentinel_absent_from_every_sink", func(t *testing.T) {
		// GREEN asserts here (spec S-48): foreground and background (success
		// and error), the token rotated, Dev exported → transcript, model
		// request record, published chat messages, gateway log, audit file,
		// account labels and export bundle contain no sentinel.
		t.Fatalf("BLOCKED: %s — required by S-48's seven sinks", s1SinkSeam)
	})

	// S50_audit_rows_value_free is now executable in pkg/gateway's
	// TestProgramSentinel_AllSinks: the same scenario needs the actual person
	// routes and shared audit logger, not a substitute program-service call.
}

func TestProgramExportImport(t *testing.T) {
	t.Run("S49_export_lists_program_import_arrives_off", func(t *testing.T) {
		// GREEN asserts here (spec S-49): Dev exported and imported as
		// "Dev2" → needs_secrets has {name: "github", kind: "program"} and
		// Dev2's programs is [] (the switch never travels with the bundle).
		assertProgramPackageRoundTrip(t)
	})
}
