// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEvidence_RedactBeforeTruncate asserts SD-A13: redaction MUST precede
// truncation so a secret straddling the size-cap boundary is still fully
// scrubbed. The cap is set small enough that the secret spans across it —
// if truncation ran first, a raw fragment of the secret would survive in the
// persisted Output.
func TestEvidence_RedactBeforeTruncate(t *testing.T) {
	const secret = "SECRETVALUE1234567890XY" // 23 bytes
	redact := func(s string) string {
		return strings.ReplaceAll(s, secret, "[FILTERED]")
	}
	es := NewEvidenceStore(t.TempDir(), redact)
	es.OutputCap = 20 // smaller than prefix(15)+secret(23) so the secret straddles the cut

	prefix := strings.Repeat("A", 15)
	output := prefix + secret + strings.Repeat("B", 10)

	rec, err := es.Record("task-1", "crit-1", 1, "cat secret.txt", output, 0, false, false)
	require.NoError(t, err)

	assert.NotContains(t, rec.Output, secret, "no raw fragment of the secret may survive truncation")
	assert.True(t, rec.Truncated, "output over the cap must be marked truncated")

	// Also verify the ON-DISK record (not just the in-memory return value).
	stored, err := es.List("task-1")
	require.NoError(t, err)
	require.Len(t, stored, 1)
	assert.NotContains(t, stored[0].Output, secret, "on-disk record must never contain the raw secret")
}

// TestEvidence_RedactBeforeTruncate_CommandAlsoScrubbed verifies Command
// (not just Output) is redacted before being persisted (FR-020).
func TestEvidence_RedactBeforeTruncate_CommandAlsoScrubbed(t *testing.T) {
	const secret = "topsecrettoken"
	redact := func(s string) string { return strings.ReplaceAll(s, secret, "[FILTERED]") }
	es := NewEvidenceStore(t.TempDir(), redact)

	rec, err := es.Record("task-2", "crit-1", 1, "curl -H 'Authorization: "+secret+"'", "200 OK", 0, false, false)
	require.NoError(t, err)
	assert.NotContains(t, rec.Command, secret)
}

// TestEvidence_TimeoutAndPolicyDeniedSentinels verifies the ExitCode -1
// sentinel is applied whenever TimedOut or PolicyDenied is true (spec Part A
// §C EvidenceRecord contract).
func TestEvidence_TimeoutAndPolicyDeniedSentinels(t *testing.T) {
	es := NewEvidenceStore(t.TempDir(), nil)

	timedOut, err := es.Record("task-3", "crit-1", 1, "sleep 100", "", 0, true, false)
	require.NoError(t, err)
	assert.Equal(t, -1, timedOut.ExitCode)
	assert.True(t, timedOut.TimedOut)

	denied, err := es.Record("task-3", "crit-2", 1, "rm -rf /", "", 0, false, true)
	require.NoError(t, err)
	assert.Equal(t, -1, denied.ExitCode)
	assert.True(t, denied.PolicyDenied)
}

// TestEvidence_DeletedWithTask verifies FR-023/SD-A10: deleting a task
// removes its entire evidence directory.
func TestEvidence_DeletedWithTask(t *testing.T) {
	home := t.TempDir()
	s := New(filepath.Join(home, "tasks"))
	tk := mkTask("with-evidence", "ws-1")
	require.NoError(t, s.Create(tk))

	es := NewEvidenceStore(home, nil)
	_, err := es.Record(tk.ID, "crit-1", 1, "echo hi", "hi", 0, false, false)
	require.NoError(t, err)
	_, err = es.Record(tk.ID, "crit-2", 1, "echo bye", "bye", 0, false, false)
	require.NoError(t, err)

	before, err := es.List(tk.ID)
	require.NoError(t, err)
	require.Len(t, before, 2, "both evidence records must be persisted before delete")

	_, err = s.Delete(tk.ID)
	require.NoError(t, err)

	after, err := es.List(tk.ID)
	require.NoError(t, err)
	assert.Empty(t, after, "evidence must be gone once the task is deleted")

	_, statErr := os.Stat(filepath.Join(home, "tasks_evidence", tk.ID))
	assert.True(t, os.IsNotExist(statErr), "evidence directory itself must be removed, not just emptied")
}

// TestEvidence_DeletedWithTask_UnrelatedTaskUnaffected differentiates the
// cascade: deleting one task's evidence must not touch another task's.
func TestEvidence_DeletedWithTask_UnrelatedTaskUnaffected(t *testing.T) {
	home := t.TempDir()
	s := New(filepath.Join(home, "tasks"))
	tk1 := mkTask("task-one", "ws-1")
	require.NoError(t, s.Create(tk1))
	tk2 := mkTask("task-two", "ws-1")
	require.NoError(t, s.Create(tk2))

	es := NewEvidenceStore(home, nil)
	_, err := es.Record(tk1.ID, "crit-1", 1, "echo a", "a", 0, false, false)
	require.NoError(t, err)
	_, err = es.Record(tk2.ID, "crit-1", 1, "echo b", "b", 0, false, false)
	require.NoError(t, err)

	_, err = s.Delete(tk1.ID)
	require.NoError(t, err)

	remaining, err := es.List(tk2.ID)
	require.NoError(t, err)
	assert.Len(t, remaining, 1, "the other task's evidence must survive")
}
