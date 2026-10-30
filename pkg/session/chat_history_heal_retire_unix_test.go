//go:build linux || darwin

package session

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

const selfHealRetireLog = "session: chat history index old generations not retired"

// 6a gate fix round (silent H2): a rebuild that PUBLISHED its index and then
// failed to remove the old generation file is a completed heal. The cleanup
// failure is logged with its cause, the heal never latches for it, and the
// old file is removed later by the normal reconcile (deferred retirement).
// The fault is a real OS edge: the generations folder is made read-only
// right after the new checkpoint is published.
func TestChatHistoryHeal_PublishedIndexWithFailedCleanupIsNotALatchedFailure(t *testing.T) {
	t.Run("cleanup_fails_once_then_the_proof_retires", func(t *testing.T) { healFixRetireFault(t, false) })
	t.Run("cleanup_keeps_failing_until_permissions_return", func(t *testing.T) { healFixRetireFault(t, true) })
}

func healFixRetireFault(t *testing.T, persistent bool) {
	if os.Geteuid() == 0 {
		t.Fatal("BLOCKED: root bypasses the generation-folder permission fault; run as a non-root user")
	}
	logs := selfHealCaptureLogs(t)
	f := newSelfHealFixture(t)
	generations := filepath.Join(historyIndexDir(f.store, f.sid), "generations")
	oldFile := filepath.Join(generations, historyCheckpointOf(t, f.store, f.sid).File)
	require.FileExists(t, oldFile)
	info, err := os.Stat(generations)
	require.NoError(t, err)
	var once sync.Once
	restore := func() { once.Do(func() { require.NoError(t, os.Chmod(generations, info.Mode().Perm())) }) }
	t.Cleanup(restore)
	oldRead, oldPublication := chatHistoryReadHook, chatHistoryPublicationHook
	t.Cleanup(func() { chatHistoryReadHook, chatHistoryPublicationHook = oldRead, oldPublication })
	published := ""
	chatHistoryPublicationHook = func(e chatHistoryPublicationEvent) error {
		if e.SessionID == f.sid && e.Operation == "repair" && e.Point == "checkpoint_published" {
			published = e.IndexGeneration
			require.NoError(t, os.Chmod(generations, 0o500))
			probe := os.Remove(oldFile)
			require.ErrorIs(t, probe, fs.ErrPermission, "instrument: removal must really fail before the heal retires")
		}
		if oldPublication != nil {
			return oldPublication(e)
		}
		return nil
	}
	proofs := 0
	chatHistoryReadHook = func(ctx context.Context, e chatHistoryReadEvent) error {
		if oldRead != nil {
			if err := oldRead(ctx, e); err != nil {
				return err
			}
		}
		if published != "" && e.SessionID == f.sid && e.Scope == "recovery" && e.Purpose == "anchor" && e.Phase == "record_opened" && e.Cut.generation == published {
			proofs++
			if !persistent {
				restore() // the fault clears before the readiness proof retries cleanup
			}
		}
		return nil
	}

	err = f.store.CheckChatHistoryReadiness(context.Background(), f.sid)
	require.NotEmpty(t, published, "positive control: the rebuild published")
	require.Equal(t, 1, proofs, "positive control: the readiness proof read the published index")
	require.Equal(t, published, historyCheckpointOf(t, f.store, f.sid).Generation, "the published index stays live")
	require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealBadLog), "a published index is not a failed rebuild")
	ok := selfHealEvents(t, logs, f.sid, selfHealOKLog)
	require.Len(t, ok, 1)
	selfHealRequireLogBase(t, ok[0], f.sid, 1)
	cleanup := selfHealEvents(t, logs, f.sid, selfHealRetireLog)
	require.Len(t, cleanup, 1)
	require.Equal(t, "WARN", cleanup[0]["level"])
	require.Equal(t, "catchup_limit", cleanup[0]["trigger"])
	require.Equal(t, "remove <path>: permission denied", cleanup[0]["cause"], "the cleanup failure is logged with its sanitized cause")
	owner := f.store.historyOwner(f.sid)
	owner.mu.Lock()
	state := owner.heal
	owner.mu.Unlock()
	require.Equal(t, chatHistoryHealState{}, state, "the heal succeeded: no attempt is spent and nothing latches")
	require.Zero(t, len(chatHistoryHealSlot))

	if persistent {
		// The store's own deferred retirement keeps failing on this read:
		// the truthful state is a storage failure, not a heal failure.
		selfHealRequireUnavailable(t, err, "storage_error")
		require.ErrorIs(t, err, fs.ErrPermission)
		require.FileExists(t, oldFile)
		restore()
		chatHistoryReadHook = oldRead
		require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "the next read retires the old file")
	} else {
		require.NoError(t, err)
	}
	require.NoFileExists(t, oldFile, "the old generation is retired later, not leaked")
	require.Len(t, selfHealEvents(t, logs, f.sid, selfHealOKLog), 1, "no second rebuild was needed")
	healFixRequireAddresses(t, f.store, f.sid, f.addresses)
	selfHealRequirePrivateLogs(t, logs, f)
	for _, record := range cleanup {
		require.NotContains(t, record["cause"], f.store.BaseDir())
	}
}
