package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MUST 3: a real non-NotFound read fault after classification reaches the
// recovery reader, is returned/noticed, and cannot strand a readable dead chat
// displaying Working once the transient filesystem fault has cleared.
func TestI1R1RecoveryLoadFailure(t *testing.T) {
	f := newI1R1BootFixture(t)
	id := f.root(t, session.LifecycleRunning, f.oldEpoch)
	before := f.journal(t, id)
	path := filepath.Join(f.ls.Dir(), id+".jsonl")
	backup := path + ".saved"
	r := f.recovery()
	r.Classifier = i1R1FilesystemClassify{base: r.Classifier, after: func(classified string) {
		if classified != id {
			return
		}
		require.NoError(t, os.Rename(path, backup))
		require.NoError(t, os.Mkdir(path, 0700))
	}}
	err := r.Run(context.Background())
	require.Error(t, err, "non-NotFound lifecycle load failure must reach Run's joined error")
	var pathErr *os.PathError
	require.ErrorAs(t, err, &pathErr)
	assert.Contains(t, err.Error(), "ordinary-root recovery load failed")
	require.Len(t, f.notices, 1)
	assert.Contains(t, f.notices[0], id)
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Rename(backup, path))
	assert.Equal(t, before, f.journal(t, id), "failed load cannot alter the old execution")
	f.display(t, id, generated.SessionLifecycleStateInterrupted)
}

// The directory fault is independent of user identity. It proves a storage-
// path/read refusal after real classification, NOT a failed append syscall.
func i1R2BlockJournalPath(t *testing.T, path string) func() {
	t.Helper()
	backup := path + ".path-refusal-saved"
	require.NoError(t, os.Rename(path, backup))
	require.NoError(t, os.Mkdir(path, 0700))
	restored := false
	restore := func() {
		if restored {
			return
		}
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Rename(backup, path))
		restored = true
	}
	t.Cleanup(restore)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0)
	if file != nil {
		require.NoError(t, file.Close())
	}
	var pathErr *os.PathError
	require.ErrorAs(t, err, &pathErr, "directory cannot be opened as an appendable journal, even under root")
	assert.Equal(t, "open", pathErr.Op)
	assert.Equal(t, path, pathErr.Path)
	return restore
}

func TestI1R2RecoveryStoragePathRefusal(t *testing.T) {
	f := newI1R1BootFixture(t)
	id := f.root(t, session.LifecycleRunning, f.oldEpoch)
	before := f.journal(t, id)
	r := f.recovery()
	var restore func()
	r.Classifier = i1R1FilesystemClassify{base: r.Classifier, after: func(classified string) {
		if classified == id {
			restore = i1R2BlockJournalPath(t, filepath.Join(f.ls.Dir(), id+".jsonl"))
		}
	}}
	err := r.Run(context.Background())
	var pathErr *os.PathError
	require.ErrorAs(t, err, &pathErr, "real journal path/read refusal must reach the boot error channel")
	assert.Contains(t, err.Error(), "ordinary-root recovery load failed")
	require.Len(t, f.notices, 1)
	assert.Contains(t, f.notices[0], id)
	require.NotNil(t, restore, "instrument must reach the actual classifier boundary")
	restore()
	assert.Equal(t, before, f.journal(t, id), "no lifecycle write was possible")
	f.display(t, id, generated.SessionLifecycleStateInterrupted)
}

// A malformed control ledger reaches UnfinishedStopIntentsLocked INSIDE the
// real lifecycle Mutate callback. The returned syntax error aborts persistence;
// it is not an injected store result or a raw append/IO failure claim.
func TestI1R2RecoveryMutateControlLedgerRefusal(t *testing.T) {
	f := newI1R1BootFixture(t)
	id := f.root(t, session.LifecycleRunning, f.oldEpoch)
	before := f.journal(t, id)
	controls := filepath.Join(f.ls.Dir(), "controls")
	require.NoError(t, os.MkdirAll(controls, 0700))
	ledger := filepath.Join(controls, id+".jsonl")
	require.NoError(t, os.WriteFile(ledger, []byte("{\n"), 0600))
	err := f.recovery().Run(context.Background())
	var syntaxErr *json.SyntaxError
	require.ErrorAs(t, err, &syntaxErr, "real Mutate callback refusal must reach the boot error channel")
	assert.Contains(t, err.Error(), "accepted Stop controls")
	assert.Contains(t, err.Error(), "unreadable")
	require.Len(t, f.notices, 1)
	assert.Contains(t, f.notices[0], id)
	assert.Equal(t, before, f.journal(t, id), "Mutate refusal must abort lifecycle persistence")
	ledgerAfter, readErr := os.ReadFile(ledger)
	require.NoError(t, readErr)
	assert.Equal(t, "{\n", string(ledgerAfter), "refusal cannot append a fabricated stop into the broken ledger")
	f.display(t, id, generated.SessionLifecycleStateInterrupted)
}

// Two independent refused records plus one healthy one prove both joined error
// identities and continued scanning. Order is irrelevant; no bad record may
// suppress the good restart stop or another record's actual error.
func TestI1R1RecoveryJoinsErrorsAndContinues(t *testing.T) {
	f := newI1R1BootFixture(t)
	badWrite := f.root(t, session.LifecycleRunning, f.oldEpoch)
	badLedger := f.root(t, session.LifecycleQueued, f.oldEpoch)
	good := f.root(t, session.LifecycleRunning, f.oldEpoch)
	badWriteBefore := f.journal(t, badWrite)
	r := f.recovery()
	var restore func()
	r.Classifier = i1R1FilesystemClassify{base: r.Classifier, after: func(classified string) {
		if classified == badWrite {
			restore = i1R2BlockJournalPath(t, filepath.Join(f.ls.Dir(), badWrite+".jsonl"))
		}
	}}
	controls := filepath.Join(f.ls.Dir(), "controls")
	require.NoError(t, os.MkdirAll(controls, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(controls, badLedger+".jsonl"), []byte("{\n"), 0600))
	err := r.Run(context.Background())
	var pathErr *os.PathError
	require.ErrorAs(t, err, &pathErr, "the storage-path error must remain in errors.Join")
	assert.Equal(t, filepath.Join(f.ls.Dir(), badWrite+".jsonl"), pathErr.Path)
	var syntaxErr *json.SyntaxError
	require.ErrorAs(t, err, &syntaxErr, "both actual error types must remain in errors.Join")
	assert.Contains(t, err.Error(), badWrite)
	assert.Contains(t, err.Error(), badLedger)
	require.Len(t, f.notices, 2)
	joinedNotices := strings.Join(f.notices, "\n")
	assert.Contains(t, joinedNotices, badWrite)
	assert.Contains(t, joinedNotices, badLedger)
	recovered, loadErr := f.ls.Load(good)
	require.NoError(t, loadErr)
	require.Equal(t, session.LifecycleStopped, recovered.State, "a healthy neighbour must still be recovered")
	require.NotNil(t, recovered.StopNote)
	assert.Equal(t, session.StopCauseRestart, recovered.StopNote.Cause)
	require.NotNil(t, restore, "bad storage path must have been installed after actual classification")
	restore()
	assert.Equal(t, badWriteBefore, f.journal(t, badWrite), "failed storage path must retain its exact old journal")
	for _, id := range []string{badWrite, badLedger, good} {
		f.display(t, id, generated.SessionLifecycleStateInterrupted)
	}
}
