package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func repairLateCheckpoint(t *testing.T, batch repairLateBatch, id string) map[string]repairFileFact {
	t.Helper()
	dir := filepath.Join(repairLateSessionDir(batch, id), session.ChatHistoryIndexDirectory)
	path := filepath.Join(dir, "checkpoint.json") // actual private checkpoint, read from historyLoadCheckpoint
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	facts := repairTree(t, dir)
	checkpoint, present := facts["checkpoint.json"]
	require.True(t, present)
	return map[string]repairFileFact{"checkpoint.json": checkpoint}
}

// C1, D13/G + architect section V (read BEFORE freezing these assertions):
// one actual publication + one recorded late failure =1/0/1; unattempted
// sessions add no fictional refusal. Their actual checkpoint is checked below.
func TestHistoryRepairCommand_GuardedCLIFailureAfterPublication(t *testing.T) {
	tool, helper := repairBinary(t), repairNativeBinary(t)
	for _, fault := range []string{"source_drift", "stopped_evidence_drift", "interruption", "archive_damage_operator_restore"} {
		t.Run(fault, func(t *testing.T) { repairLateFailureCase(t, tool, helper, fault) })
	}
}

func repairLateFailureCase(t *testing.T, tool, helper, fault string) {
	t.Helper()
	batch := repairLateBatchFixture(t, tool, helper)
	canonical, originalBackup := repairCanonicalFacts(t, batch.source), repairTree(t, batch.backup)
	secondBefore := repairLateCheckpoint(t, batch, batch.ids[1])
	require.Nil(t, secondBefore, "instrument: second session starts with no published checkpoint")
	work := repairStartLateApply(t, batch)
	firstPublished := repairLateCheckpoint(t, batch, batch.ids[0])
	require.NotEmpty(t, firstPublished, "committed first checkpoint independently observed, not inferred from printed counts")
	require.Equal(t, secondBefore, repairLateCheckpoint(t, batch, batch.ids[1]), "barrier occurs before any second publication")
	wantExit, wantClass, restore := 2, "source_changed", false
	damagePath := ""
	switch fault {
	case "source_drift":
		damagePath = filepath.Join(batch.source, "uploads", "évidence.txt")
		require.NoError(t, os.WriteFile(damagePath, []byte("C1 source drift after verified first publication"), 0o600))
		restore = true
	case "stopped_evidence_drift":
		body, err := os.ReadFile(batch.binding)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(batch.binding, append(body, '\n'), 0o600))
		wantClass = "stopped_evidence_unknown" // exact receipt hash no longer matches its actual captured binding bytes
	case "interruption":
		wantExit, wantClass = 1, "interrupted"
		work.cancel() // real context cancellation at the observed inventory checkpoint, never a fabricated repair result
	case "archive_damage_operator_restore":
		damagePath = repairLateDamageArchive(t, batch)
		restore = true
	default:
		t.Fatalf("unknown C1 fixture class %s", fault)
	}
	faultState := repairCanonicalFacts(t, batch.source)
	result := repairJoinLateApply(t, work)
	assert.Equal(t, wantExit, result.exit, "D13 exact late-failure exit remains cause-specific after publication")
	// A summary defect must not prevent the independent restoration rehearsal
	// from executing. The child subtest still records a loud assertion failure.
	t.Run("actual_partial_summary", func(t *testing.T) {
		repairRequireClass(t, result.output, wantClass)
		repairAssertPublicationCounts(t, result.output, 1, 0, 1)
	})
	require.Equal(t, firstPublished, repairLateCheckpoint(t, batch, batch.ids[0]), "completed first checkpoint is not silently rolled back")
	require.Equal(t, secondBefore, repairLateCheckpoint(t, batch, batch.ids[1]), "unattempted second session has no NEW checkpoint/generation publication")
	require.Equal(t, faultState, repairCanonicalFacts(t, batch.source), "repair tool never rewrites/auto-restores damaged canonical data or PID state")
	require.Equal(t, originalBackup, repairTree(t, batch.backup), "original count-verified full backup is immutable")
	if fault == "archive_damage_operator_restore" {
		repairLatePublicationState(t, batch, false)
	} else {
		repairLatePublicationState(t, batch, true)
	}
	if restore {
		require.NotEqual(t, canonical, faultState, "instrument: actual post-publication canonical damage/drift is present")
		require.NotEmpty(t, damagePath)
		require.NotContains(t, result.output, "C1 damaged source", "CLI failures do not disclose archive bodies")
		preserved := repairLateOperatorRestore(t, tool, batch)
		require.Equal(t, faultState, repairCanonicalFacts(t, preserved), "failed source/evidence retained for operator inspection")
		require.Equal(t, canonical, repairCanonicalFacts(t, batch.source), "full backup restoration recovers original canonical counts/bytes/mode/mtime")
	}
	renewed := repairLateRenewEvidence(t, tool, helper, batch)
	out := repairRun(t, tool, 0, renewed.applyArgs()...)
	if restore {
		repairAssertPublicationCounts(t, out, 2, 0, 0) // restored backup had neither derived index
	} else {
		repairAssertPublicationCounts(t, out, 1, 1, 0) // first completed generation is the matching unchanged one
		require.Equal(t, firstPublished, repairLateCheckpoint(t, renewed, renewed.ids[0]), "deliberate repeat never rewrites matching first generation bytes/mode/mtime")
	}
	repairRun(t, tool, 0, "verify", "--data-root", renewed.source, "--backup-root", renewed.backup, "--backup-manifest", renewed.manifest)
	require.Equal(t, batch.positions, repairLateReadBoth(t, renewed), "both sessions preserve original public positions and exact forty canonical entries through interruption/restoration/repeat")
	require.Equal(t, canonical, repairCanonicalFacts(t, renewed.source))
	require.Equal(t, originalBackup, repairTree(t, batch.backup))
}
