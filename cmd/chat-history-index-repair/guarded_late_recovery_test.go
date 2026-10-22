package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func repairLateRenewEvidence(t *testing.T, tool, helper string, batch repairLateBatch) repairLateBatch {
	t.Helper()
	// A deliberate operator retry, not a repair-tool loop or invented dead PID.
	before := repairTree(t, batch.source)
	repairRun(t, tool, 0, "dry-run", "--data-root", batch.source)
	require.Equal(t, before, repairTree(t, batch.source))
	child := repairStartNative(t, helper, batch.source)
	binding, identity := repairCapture(t, tool, helper, batch.source, child)
	repairStopNative(t, child, "stop", 0)
	backup, manifest := repairCountVerifiedBackup(t, tool, batch.source)
	receipt := repairProduceReceipt(t, tool, batch.source, binding, identity, "absent")
	batch.binding, batch.receipt, batch.backup, batch.manifest = binding, receipt, backup, manifest
	return batch
}

func repairLateDamageArchive(t *testing.T, batch repairLateBatch) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repairLateSessionDir(batch, batch.ids[0]), "transcript*.jsonl"))
	require.NoError(t, err)
	for _, path := range files {
		body, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		if !bytes.Contains(body, []byte(`"id":"entry-001"`)) {
			continue
		}
		// Changing the original identity changes D2's position/reference fact,
		// so this is proven index damage, not merely a body edit that a
		// deliberately body-free index could still describe correctly.
		changed := bytes.Replace(body, []byte(`"id":"entry-001"`), []byte(`"id":"other-001"`), 1)
		require.Equal(t, len(body), len(changed), "same-size valid JSON identity drift, not a torn-file/setup error")
		require.NotEqual(t, body, changed)
		require.NoError(t, os.WriteFile(path, changed, 0o600))
		return path
	}
	t.Fatal("C1 fixture has no real canonical content to damage after publication")
	return ""
}

func repairLateOperatorRestore(t *testing.T, tool string, batch repairLateBatch) string {
	t.Helper()
	// Preserve the entire failed source as evidence. Restoration below is an
	// explicit TEST OPERATOR action; the repair executable must never do it.
	oldRoot, err := os.Stat(batch.source)
	require.NoError(t, err)
	preserved := filepath.Join(t.TempDir(), "preserved-failed-source")
	require.NoError(t, os.Rename(batch.source, preserved))
	repairCopy(t, batch.backup, batch.source)
	newRoot, err := os.Stat(batch.source)
	require.NoError(t, err)
	require.False(t, os.SameFile(oldRoot, newRoot), "actual full backup restoration replaces the bound native root object")
	require.Equal(t, repairFullCopyFacts(t, batch.backup), repairFullCopyFacts(t, batch.source),
		"independent full count/byte/mode/mtime restoration rehearsal, not sessions-only restore")
	before := repairTree(t, batch.source)
	out := repairRun(t, tool, 2, batch.applyArgs()...)
	// D13/G explicitly preserves source_changed/2 for root-object
	// replacement; this is not malformed evidence's unknown/2 class.
	repairRequireClass(t, out, "source_changed")
	require.Equal(t, before, repairTree(t, batch.source), "restoration-invalidated old binding/receipt cannot authorize a hidden rerun")
	return preserved
}

func repairLateReadBoth(t *testing.T, batch repairLateBatch) map[string][]string {
	t.Helper()
	positions := make(map[string][]string, len(batch.ids))
	for _, id := range batch.ids {
		one := batch.repairGuardedFixture
		one.sid = id
		positions[id] = repairReadAppliedPage(t, one)
	}
	return positions
}
