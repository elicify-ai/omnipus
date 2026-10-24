package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/daemon"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func repairFinalGuardDamageDerivedCheckpoint(t *testing.T, batch repairLateBatch) {
	t.Helper()
	path := filepath.Join(repairLateSessionDir(batch, batch.ids[0]), session.ChatHistoryIndexDirectory, "checkpoint.json")
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &fields))
	fields["root"] = json.RawMessage(`-1`) // Only disposable derived state; not canonical/source/backup evidence.
	body, err = json.Marshal(fields)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, body, 0o600))
	require.Error(t, session.VerifyChatHistoryIndex(context.Background(), repairLateSessionDir(batch, batch.ids[0])),
		"instrument: the actual shared verifier rejects the damaged first index before any later guard fault")
}

// D13 aggregate mapping: an earlier index verification failure1 must not erase
// a DISTINCT later source/native precondition failure2. V counts recorded
// failures, not unattempted sessions: actual published1/unchanged0/refused2.
func TestHistoryRepairCommand_FinalGuardTwoOverridesEarlierOne(t *testing.T) {
	tool, helper := repairBinary(t), repairNativeBinary(t)
	for _, fault := range []string{"source_changed", "instance_running"} {
		t.Run(fault, func(t *testing.T) { repairFinalGuardCase(t, tool, helper, fault) })
	}
}

func repairFinalGuardCase(t *testing.T, tool, helper, fault string) {
	t.Helper()
	batch := repairLateBatchFixture(t, tool, helper)
	canonical, backup := repairCanonicalFacts(t, batch.source), repairTree(t, batch.backup)
	secondBefore := repairLateCheckpoint(t, batch, batch.ids[1])
	require.Nil(t, secondBefore)
	work := repairStartFinalGuardApply(t, batch)
	require.NotEmpty(t, repairLateCheckpoint(t, batch, batch.ids[0]), "actual first publication exists before fault")
	repairFinalGuardDamageDerivedCheckpoint(t, batch)
	require.Equal(t, canonical, repairCanonicalFacts(t, batch.source), "earlier integrity fault affects only disposable derived index")
	damagedFirst := repairLateCheckpoint(t, batch, batch.ids[0])
	repairAwaitFinalGuardVerification(t, work)

	var child *repairNativeInstance
	switch fault {
	case "source_changed":
		require.NoError(t, os.WriteFile(filepath.Join(batch.source, "uploads", "évidence.txt"),
			[]byte("F7 private late canonical drift canary"), 0o600))
	case "instance_running":
		child = repairStartNative(t, helper, batch.source) // Actual registered live process, not a fake liveness result.
		inspection, err := daemon.InspectProcess(child.cmd.Process.Pid)
		require.NoError(t, err)
		require.Equal(t, "running", inspection.State)
		require.Equal(t, helper, inspection.Executable)
	default:
		t.Fatalf("unknown F7 late fault %s", fault)
	}
	faultState := repairCanonicalFacts(t, batch.source)
	require.NotEqual(t, canonical, faultState, "instrument: the DISTINCT later source/native fault really happened")
	result := repairJoinFinalGuardApply(t, work)
	assert.Equal(t, 2, result.exit, "D13 late precondition2 takes precedence over earlier integrity1")
	assert.Len(t, regexp.MustCompile(`class=index_verification(?:\s|$)`).FindAllString(result.output, -1), 1,
		"exactly one actual earlier verification failure is reported")
	assert.Len(t, regexp.MustCompile(`class=`+regexp.QuoteMeta(fault)+`(?:\s|$)`).FindAllString(result.output, -1), 1,
		"the DISTINCT final guard failure must also be reported, never suppressed after an earlier refusal")
	t.Run("actual_partial_counts", func(t *testing.T) {
		repairAssertPublicationCounts(t, result.output, 1, 0, 2)
	})
	assert.NotContains(t, result.output, "F7 private late canonical drift canary", "content-free fixed failure diagnostics")
	require.Equal(t, damagedFirst, repairLateCheckpoint(t, batch, batch.ids[0]), "CLI never automatically rolls back/repairs deliberate derived damage")
	require.Equal(t, secondBefore, repairLateCheckpoint(t, batch, batch.ids[1]), "no second publication or fictional third refusal")
	require.Equal(t, faultState, repairCanonicalFacts(t, batch.source), "actual failed batch does not auto-restore source or clear/adopt native PID")
	require.Equal(t, backup, repairTree(t, batch.backup), "full count-verified backup stays immutable")
	if child != nil {
		inspection, err := daemon.InspectProcess(child.cmd.Process.Pid)
		require.NoError(t, err)
		require.Equal(t, "running", inspection.State, "repair command must not stop the unexpected real instance")
		repairStopNative(t, child, "stop", 0) // Test operator explicitly reaps its own process AFTER observations.
	}
}
