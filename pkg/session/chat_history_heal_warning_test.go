package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const healFixUnavailableLog = "chat history: derived index unavailable"

// FIX-ROUND-1 row 7 / pta F2: P3 itself, not just rebuild logging, must be
// visible once per unchanged failure episode and reset after healthy recovery.
func TestChatHistoryHeal_DerivedUnavailableWarningOncePerEpisode(t *testing.T) {
	t.Run("canonical_truncation_then_recovery_then_same_failure", func(t *testing.T) {
		logs := selfHealCaptureLogs(t)
		f := selfHealSeed(t)
		require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid))
		path := filepath.Join(f.store.BaseDir(), f.sid, archiveCurrentFile)
		canonical, err := os.ReadFile(path)
		require.NoError(t, err)
		for episode := 1; episode <= 2; episode++ {
			require.NoError(t, os.Truncate(path, int64(len(canonical)-1)))
			for i := 0; i < 5; i++ {
				selfHealRequireUnavailable(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "index_inconsistent")
			}
			healFixRequireP3Warnings(t, logs, f.sid, episode, "index_inconsistent", "canonical archive truncated")
			require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealOKLog))
			require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealBadLog))
			require.NoError(t, os.WriteFile(path, canonical, 0o600))
			require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "an actual healthy reconcile resets the warning episode")
		}
		require.NotContains(t, logs.text(), selfHealBody)
	})
	t.Run("accepted_canonical_save_and_failed_derived_publication", healFixPublicationWarningEpisode)
}

func healFixPublicationWarningEpisode(t *testing.T) {
	logs := selfHealCaptureLogs(t)
	f := selfHealSeed(t)
	require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid))
	old := chatHistoryPublicationHook
	fault := errors.New("test-derived-publication-unavailable")
	broken := func(e chatHistoryPublicationEvent) error {
		if e.SessionID == f.sid && (e.Operation == "append" && e.Point == "canonical_synced" || e.Operation == "reconcile" && e.Point == "before_checkpoint_publish") {
			return fault
		}
		if old != nil {
			return old(e)
		}
		return nil
	}
	t.Cleanup(func() { chatHistoryPublicationHook = old })
	for episode := 1; episode <= 2; episode++ {
		chatHistoryPublicationHook = broken
		a, err := f.store.AppendTranscriptAddressed(f.sid, historyUserEntry(fmt.Sprintf("warning-episode-%d", episode), selfHealBody, episode))
		require.NoError(t, err, "a successful canonical fsync must never be relabeled unsaved by a derived failure")
		f.addresses = append(f.addresses, a)
		healFixRequireP3Warnings(t, logs, f.sid, episode, "storage_error", fault.Error()) // acceptance path, before any readiness logger can mask its absence
		for i := 0; i < 5; i++ {
			err = f.store.CheckChatHistoryReadiness(context.Background(), f.sid)
			selfHealRequireUnavailable(t, err, "storage_error")
			require.ErrorIs(t, err, fault)
		}
		healFixRequireP3Warnings(t, logs, f.sid, episode, "storage_error", fault.Error())
		chatHistoryPublicationHook = old
		require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "actual recovery, not direct mutation of the logger's episode fields")
	}
	healFixRequireAddresses(t, f.store, f.sid, f.addresses)
	require.NotContains(t, logs.text(), selfHealBody)
}

func healFixRequireP3Warnings(t testing.TB, logs *selfHealLogs, sid string, count int, reason, cause string) {
	t.Helper()
	records := selfHealEvents(t, logs, sid, healFixUnavailableLog)
	require.Len(t, records, count, "one P3 warning per failure episode, neither silent nor repeated per check")
	for _, record := range records {
		require.Equal(t, "WARN", record["level"])
		require.Equal(t, sid, record["session_id"])
		require.Equal(t, reason, record["reason"])
		require.Equal(t, cause, record["cause"])
	}
}
