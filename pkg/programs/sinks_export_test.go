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
// fixtures now exist for the migrated rows. S48's combined real campaign lives
// in pkg/gateway::TestProgramMasking_ActualSevenSinks; S50's separate actual
// person-route audit campaign lives in pkg/gateway::TestProgramSentinel_AllSinks.
// A component test or static source guard is not whole-campaign evidence.
//
// Oracle: US-6 scenario texts, D-4/D-9 rows, FR-016/FR-020/FR-021/FR-022.

package programs

import "testing"

// TestProgramMasking_Boundaries and all its former marker rows were relocated,
// not removed: S46 is now pkg/agent::TestProgramMasking_Boundaries; S47/S74 are
// pkg/agent::TestProgramMasking_ActualBackgroundEdges; S72 is
// pkg/agent::TestProgramMasking_ActualBackground; S73 is
// pkg/agent::TestProgramMasking_ActualSuppressedError. The real loop, ongoing
// child, replacement/disconnect, continuation, provider requests, transcript,
// error notice and published events replace the original unconditional fatals.
// Do not emit a vacuous empty Test function in this package for the relocation.

// S48_sentinel_absent_from_every_sink is relocated, not removed:
// pkg/gateway::TestProgramMasking_ActualSevenSinks/S48_every_sink_value_free.
// One actual foreground/background success/error + rotation + registered export
// campaign observes transcript, model requests, published chat, gateway log,
// audit file, account labels and the decoded bundle with real reader controls.
// Its exact eight-byte window oracle includes both current and retired tokens.
// The neutral discovery name prevents the protected first eight bytes of the
// specified token from entering t.TempDir-derived command arguments as metadata.
// S50 separately stays in pkg/gateway::TestProgramSentinel_AllSinks; neither
// campaign is replaced by the other or by a vacuous empty test in this package.

func TestProgramExportImport(t *testing.T) {
	t.Run("S49_export_lists_program_import_arrives_off", func(t *testing.T) {
		// GREEN asserts here (spec S-49): Dev exported and imported as
		// "Dev2" → needs_secrets has {name: "github", kind: "program"} and
		// Dev2's programs is [] (the switch never travels with the bundle).
		assertProgramPackageRoundTrip(t)
	})
}
