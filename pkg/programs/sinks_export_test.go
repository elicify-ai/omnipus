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
// on for Dev and the general filter off. They need the same FR-011 runtime
// attach seam plus the background-completion and export harnesses; none
// exists at RED, so each scenario reports BLOCKED naming its seam — never a
// skip — with the spec's verbatim expectations recorded beside it. (The
// masking writer itself is order 7's unit suite, already RED:
// TestProgramMaskingWriter_Fragmentation.)
//
// Oracle: US-6 scenario texts, D-4/D-9 rows, FR-016/FR-020/FR-021/FR-022.

package programs

import "testing"

// s1SinkSeam is the missing runtime the sink rows share.
const s1SinkSeam = "the agent environment builder cannot attach program variables to the exec harness (FR-011 runtime), so no program token flows to any sink yet"

func TestProgramMasking_Boundaries(t *testing.T) {
	t.Run("S46_masking_regardless_of_filter_settings_D4", func(t *testing.T) {
		// GREEN asserts here (spec S-46, dataset D-4): every row's command
		// with the row's token and filter settings produces the row's
		// expected masked output.
		t.Fatalf("BLOCKED: %s — required by S-46's D-4 rows", s1SinkSeam)
	})

	t.Run("S47_replaced_token_stays_masked", func(t *testing.T) {
		// GREEN asserts here (spec S-47): a background command printing
		// $GH_TOKEN every second, started under tok_valid_12345 → after the
		// replace and the later disconnect, every later line in read output
		// and in the completion shows [FILTERED] (retired tokens stay
		// protected).
		t.Fatalf("BLOCKED: %s — required by S-47's retired-token masking", s1SinkSeam)
	})

	t.Run("S72_background_success_filter_off", func(t *testing.T) {
		// GREEN asserts here (spec S-72): filter_sensitive_data=false;
		// `sleep 1; echo $GH_TOKEN` in the background → the published chat
		// message, the continuation input and the transcript contain
		// [FILTERED] and not the sentinel.
		t.Fatalf("BLOCKED: %s — required by S-72's three sinks", s1SinkSeam)
	})

	t.Run("S73_background_error_notice_persisted", func(t *testing.T) {
		// GREEN asserts here (spec S-73): S-72's setup with tool feedback
		// suppressed; `sleep 1; echo $GH_TOKEN; exit 3` → the persisted
		// error notice and its event payload contain [FILTERED] and not the
		// sentinel.
		t.Fatalf("BLOCKED: %s — required by S-73's persisted notice", s1SinkSeam)
	})

	t.Run("S74_truncation_at_the_notifier_edge", func(t *testing.T) {
		// GREEN asserts here (spec S-74): 1 MB of `a` minus 10 bytes, then
		// the sentinel → the continuation input contains no substring of 8
		// or more characters of the sentinel.
		t.Fatalf("BLOCKED: %s — required by S-74's truncation boundary", s1SinkSeam)
	})
}

func TestProgramSentinel_AllSinks(t *testing.T) {
	t.Run("S48_sentinel_absent_from_every_sink", func(t *testing.T) {
		// GREEN asserts here (spec S-48): foreground and background (success
		// and error), the token rotated, Dev exported → transcript, model
		// request record, published chat messages, gateway log, audit file,
		// account labels and export bundle contain no sentinel.
		t.Fatalf("BLOCKED: %s — required by S-48's seven sinks", s1SinkSeam)
	})

	t.Run("S50_audit_rows_value_free", func(t *testing.T) {
		// GREEN asserts here (spec S-50): connect, re-check, switch on for
		// Dev, change the Telegram setting, disconnect → exactly one audit
		// row per action, none containing the token.
		t.Fatalf("BLOCKED: %s — required by S-50's value-free rows", s1SinkSeam)
	})
}

func TestProgramExportImport(t *testing.T) {
	t.Run("S49_export_lists_program_import_arrives_off", func(t *testing.T) {
		// GREEN asserts here (spec S-49): Dev exported and imported as
		// "Dev2" → needs_secrets has {name: "github", kind: "program"} and
		// Dev2's programs is [] (the switch never travels with the bundle).
		t.Fatalf("BLOCKED: %s — required by S-49 (the export/import bundle seam, FR-022)", s1SinkSeam)
	})
}
