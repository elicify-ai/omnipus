package session

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type archiveMatrixCut struct {
	step      string
	phase     string
	published bool
}

// Expectations follow the actual successful-operation order, not observations
// copied from a run. A complete framed ordinal row is repaired on reopen even
// when its derived offset was not written yet.
var archiveMatrixWriterCuts = []archiveMatrixCut{
	{"append.prepared", "old", false},
	{"rollover.directory_ready", "old", false},
	{"rollover.marker_read", "old", false},
	{"rollover.current_stated", "old", false},
	{"rollover.destination_checked", "old", false},
	{"rollover.renamed", "renamed", false},
	{"marker.directory_ready", "renamed", false},
	{"marker.write_complete", "marked", false},
	{"append.partition_selected", "marked", false},
	{"append.current_opened", "created", false},
	{"append.end_located", "created", false},
	{"append.payload_written", "written", false},
	{"append.payload_synced", "written", false},
	{"append.current_closed", "written", false},
	{"ordinal.previous_read", "written", false},
	{"ordinal.directory_ready", "written", false},
	{"ordinal.rows_opened", "written", false},
	{"ordinal.end_located", "written", false},
	{"ordinal.row_written", "written", true},
	{"ordinal.rows_synced", "written", true},
	{"ordinal.offsets_opened", "written", true},
	{"ordinal.offset_written", "written", true},
	{"ordinal.offsets_closed", "written", true},
	{"ordinal.rows_closed", "written", true},
	{"indexed.returning", "written", true},
}

func TestArchiveRolloverCrashMatrix(t *testing.T) {
	for _, cut := range archiveMatrixWriterCuts {
		t.Run("writer_"+cut.step, func(t *testing.T) {
			for _, operation := range []string{"lookup", "retry_save"} {
				t.Run(operation, func(t *testing.T) {
					f := newArchiveMatrixFixture(t)
					f.runCut(t, cut, false)
					f.assertDiskPhase(t, cut.phase)
					f.reopenAndCheck(t, operation, cut.published, false)
				})
			}
		})
	}

	for _, equivalent := range []string{"old_marker_with_stray_temp", "new_marker_without_current"} {
		t.Run("constructed_atomic_equivalent_"+equivalent, func(t *testing.T) {
			for _, operation := range []string{"lookup", "retry_save"} {
				t.Run(operation, func(t *testing.T) {
					f := newArchiveMatrixFixture(t)
					f.runCut(t, archiveMatrixCut{step: "rollover.renamed", phase: "renamed"}, false)
					phase := f.constructAtomicEquivalent(t, equivalent)
					f.assertDiskPhase(t, phase)
					f.reopenAndCheck(t, operation, false, false)
				})
			}
		})
	}

	t.Run("renamed_same_mark_followup_write", func(t *testing.T) {
		f := newArchiveMatrixFixture(t)
		f.runCut(t, archiveMatrixCut{step: "rollover.renamed", phase: "renamed"}, false)
		f.nextDay = f.oldDay // recovery must not depend on the wall clock advancing
		f.assertDiskPhase(t, "renamed")
		f.reopenAndCheck(t, "lookup", false, false)
	})

	t.Run("conditional_existing_current_tail_read", func(t *testing.T) {
		f := newArchiveMatrixFixture(t)
		f.runCut(t, archiveMatrixCut{step: "append.tail_read", phase: "old"}, true)
		f.assertDiskPhase(t, "old")
		f.reopenAndCheck(t, "lookup", false, false)
	})

	t.Run("real_writer_trace_covers_all_injected_steps", func(t *testing.T) {
		f := newArchiveMatrixFixture(t)
		trace := f.runCut(t, archiveMatrixCut{phase: "written", published: true}, false)
		expected := make([]string, 0, len(archiveMatrixWriterCuts))
		for _, cut := range archiveMatrixWriterCuts {
			expected = append(expected, cut.step)
		}
		require.ElementsMatch(t, expected, trace, "every matrix stop is reached by the real writer; no dead instrumentation")
		f.reopenAndCheck(t, "retry_save", true, false)
	})

	for _, loss := range []string{"missing_renamed_partition", "renamed_record_identity_changed", "ambiguous_current_and_dated"} {
		t.Run("loss_"+loss, func(t *testing.T) {
			for _, operation := range []string{"lookup", "retry_save"} {
				t.Run(operation, func(t *testing.T) {
					f := newArchiveMatrixFixture(t)
					f.runCut(t, archiveMatrixCut{step: "rollover.renamed", phase: "renamed"}, false)
					f.constructLoss(t, loss)
					f.reopenAndCheck(t, operation, false, true)
				})
			}
		})
	}

	// Reuse the unchanged real loss/reopen assertions; do not fork a weaker
	// copy of the round-3 ordinal-backed and pending-address-backed negatives.
	t.Run("retained_round3_published_loss", TestSavedInputMissingPublishedCurrent)
	t.Run("retained_SFH3_missing_indexed_source", archiveMatrixMissingSourceControl)
}
