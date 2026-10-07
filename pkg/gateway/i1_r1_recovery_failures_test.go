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

// Permission is a process-edge fault, not a mocked store success/failure. The
// independent append probe proves the environment enforces it; do not skip or
// call a non-enforcing root environment a green persist-failure test.
func i1R1DenyJournalAppend(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.Chmod(path, 0400))
	t.Cleanup(func() { assert.NoError(t, os.Chmod(path, 0600)) })
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if file != nil {
		require.NoError(t, file.Close())
	}
	require.ErrorIs(t, err, os.ErrPermission, "fault instrument must refuse actual journal append before testing recovery")
}

func TestI1R1RecoveryPersistFailure(t *testing.T) {
	f := newI1R1BootFixture(t)
	id := f.root(t, session.LifecycleRunning, f.oldEpoch)
	before := f.journal(t, id)
	i1R1DenyJournalAppend(t, filepath.Join(f.ls.Dir(), id+".jsonl"))
	err := f.recovery().Run(context.Background())
	require.ErrorIs(t, err, os.ErrPermission, "real Mutate/persist failure must reach the boot error channel")
	require.Len(t, f.notices, 1)
	assert.Contains(t, f.notices[0], id)
	assert.Equal(t, before, f.journal(t, id), "no lifecycle write was possible")
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
	i1R1DenyJournalAppend(t, filepath.Join(f.ls.Dir(), badWrite+".jsonl"))
	controls := filepath.Join(f.ls.Dir(), "controls")
	require.NoError(t, os.MkdirAll(controls, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(controls, badLedger+".jsonl"), []byte("{\n"), 0600))
	err := f.recovery().Run(context.Background())
	require.ErrorIs(t, err, os.ErrPermission)
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
	for _, id := range []string{badWrite, badLedger, good} {
		f.display(t, id, generated.SessionLifecycleStateInterrupted)
	}
}
