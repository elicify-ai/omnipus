package session

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 6a gate fix round (tests item 1, design §1 "post-heal readiness still
// failing: latch"): a non-deadline failure of the post-publication readiness
// proof is class readiness, latched per process, logged with its cause, and
// returns the readiness error itself. The published index is kept: a later
// healthy read uses it without another rebuild.
func TestChatHistoryHeal_PostPublicationReadinessFailureLatchesAndKeepsIndex(t *testing.T) {
	logs := selfHealCaptureLogs(t)
	f := newSelfHealFixture(t)
	base := time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC)
	now := base
	oldClock := f.store.historyHealNow
	f.store.historyHealNow = func() time.Time { return now }
	oldRead, oldPublication := chatHistoryReadHook, chatHistoryPublicationHook
	t.Cleanup(func() {
		chatHistoryReadHook, chatHistoryPublicationHook = oldRead, oldPublication
		f.store.historyHealNow = oldClock
	})
	canonical := partitionBytes(t, f.store, f.sid)
	published := ""
	publications, faults := 0, 0
	chatHistoryPublicationHook = func(e chatHistoryPublicationEvent) error {
		if e.SessionID == f.sid && e.Operation == "repair" && e.Point == "checkpoint_published" {
			publications++
			published = e.IndexGeneration
		}
		if oldPublication != nil {
			return oldPublication(e)
		}
		return nil
	}
	private := filepath.Join(f.store.BaseDir(), f.sid, archiveCurrentFile)
	chatHistoryReadHook = func(ctx context.Context, e chatHistoryReadEvent) error {
		if oldRead != nil {
			if err := oldRead(ctx, e); err != nil {
				return err
			}
		}
		if published != "" && e.SessionID == f.sid && e.Scope == "recovery" && e.Purpose == "anchor" && e.Phase == "record_opened" && e.Cut.generation == published {
			faults++
			return &fs.PathError{Op: "open", Path: private, Err: fs.ErrPermission}
		}
		return nil
	}

	err := f.store.CheckChatHistoryReadiness(context.Background(), f.sid)
	require.Equal(t, 1, publications, "positive control: the rebuild really published")
	require.Equal(t, 1, faults, "positive control: the fault hit the post-publication proof read")
	selfHealRequireUnavailable(t, err, "index_inconsistent")
	require.ErrorIs(t, err, fs.ErrPermission, "the caller gets the readiness failure itself, not the old trigger")
	records := selfHealEvents(t, logs, f.sid, selfHealBadLog)
	require.Len(t, records, 1)
	selfHealRequireLogBase(t, records[0], f.sid, 1)
	require.Equal(t, "readiness", records[0]["class"])
	require.Equal(t, "after_restart", records[0]["retry"])
	require.Equal(t, "index_inconsistent: open <path>: permission denied", records[0]["cause"])
	require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealOKLog), "a failed proof is not a completed heal")
	owner := f.store.historyOwner(f.sid)
	owner.mu.Lock()
	state := owner.heal
	owner.mu.Unlock()
	require.True(t, state.latched, "design §1: post-heal readiness failure latches for the process lifetime")
	require.Equal(t, published, historyCheckpointOf(t, f.store, f.sid).Generation, "the published index is kept, not rolled back")
	require.Equal(t, canonical, partitionBytes(t, f.store, f.sid))
	require.Zero(t, len(chatHistoryHealSlot), "the failure path must free the rebuild slot")

	// The read fault is gone: the kept index serves a healthy read with no
	// further rebuild and no further warning.
	chatHistoryReadHook = oldRead
	now = base.Add(24 * time.Hour)
	require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid))
	require.Equal(t, 1, publications, "the latch holds: no second rebuild")
	require.Len(t, selfHealEvents(t, logs, f.sid, selfHealBadLog), 1)
	healFixRequireAddresses(t, f.store, f.sid, f.addresses)
	selfHealRequirePrivateLogs(t, logs, f)
}
