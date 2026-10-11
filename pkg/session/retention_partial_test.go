// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// threeDaySession builds a real session with a model slot on each of three UTC
// days (04-01, 04-02 rolled and aged; 04-10 live). The archive clock is left at
// 2026-04-10, so a 7-day sweep expires both rolled days.
func threeDaySession(t *testing.T) (*UnifiedStore, string, *ArchiveDayStore) {
	t.Helper()
	store := newUnifiedStoreForTest(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	day, err := store.backend.store(meta.ID)
	require.NoError(t, err)
	for _, d := range []string{"2026-04-01", "2026-04-02", "2026-04-10"} {
		atDay(day, d)
		_, _, err := store.AppendModelMessage(context.Background(), meta.ID, ModelAppend{
			Message:        providers.Message{Role: "user", Content: "message on " + d},
			ViewMembership: ViewMembershipBoth, Source: EntrySource{Kind: "user", Principal: "daniel"}, AgentID: "mia",
		})
		require.NoError(t, err)
	}
	for _, d := range []string{"2026-04-01", "2026-04-02"} {
		ageFileTo(t, filepath.Join(day.dir(), d+".jsonl"), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	}
	return store, meta.ID, day
}

// Delta review R1 #6: when removing one expired partition fails, the ones that
// WERE removed still moved the window and still count - the cursor must not be
// left pointing at deleted slots, and the failure is still reported.
func TestRetention_FailedRemovalStillAdvancesPastWhatWasRemoved(t *testing.T) {
	store, sid, day := threeDaySession(t)
	old := removePartitionFn
	t.Cleanup(func() { removePartitionFn = old })
	removePartitionFn = func(path string) error {
		if strings.HasSuffix(path, "2026-04-02.jsonl") {
			return errors.New("injected: cannot remove")
		}
		return os.Remove(path)
	}

	removed, err := store.RetentionSweep(7)
	require.Error(t, err, "the failed removal is reported")
	require.Equal(t, 1, removed, "the partition that WAS removed still counts")

	_, statErr := os.Stat(filepath.Join(day.dir(), "2026-04-01.jsonl"))
	require.True(t, os.IsNotExist(statErr), "instrument check: day one was removed")

	meta, loadErr := store.backend.loadMetaLocked(sid)
	require.NoError(t, loadErr)
	require.Equal(t, 1, meta.Skip, "the cursor moved past the removed day's slot")
	require.NotEmpty(t, meta.ExpiryNote, "the agent is told what expired")
}
