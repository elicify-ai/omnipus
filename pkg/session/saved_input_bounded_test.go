package session

// Gate-1 fix round 1 for PR #1345: cr F5 / ARCH-4 / PW-SEC-03. The per-turn
// pending-input check must not read lifetime history: with nothing pending it
// reads no archive record at all, and with inputs pending it reads only their
// own addresses — never a whole-archive scan. The archive's own read
// instrument (ArchiveDayStore.onRead) observes every scan and record read.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type archiveReadLog struct {
	mu    sync.Mutex
	kinds []string
	addrs []ArchiveAddress
}

func (l *archiveReadLog) observe(kind string, addr ArchiveAddress) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.kinds = append(l.kinds, kind)
	l.addrs = append(l.addrs, addr)
}

func (l *archiveReadLog) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.kinds, l.addrs = nil, nil
}

func (l *archiveReadLog) snapshot() ([]string, []ArchiveAddress) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.kinds...), append([]ArchiveAddress(nil), l.addrs...)
}

func TestPendingChatInputs_ReadsOnlyPendingRecordsNotLifetimeHistory(t *testing.T) {
	s := newTestStore(t)
	day := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	setArchiveDay(s, &day)

	withPending, err := s.NewSession(SessionTypeChat, "", "agent")
	require.NoError(t, err)
	withoutPending, err := s.NewSession(SessionTypeChat, "", "agent")
	require.NoError(t, err)
	for _, sid := range []string{withPending.ID, withoutPending.ID} {
		for i := 0; i < 50; i++ { // lifetime history the per-turn check must not read
			_, err = s.AppendTranscriptAddressed(sid, TranscriptEntry{
				ID: fmt.Sprintf("old-history-%s-%d", sid, i), Role: "user",
				Content: "old lifetime history", Timestamp: day,
			})
			require.NoError(t, err)
		}
	}
	day = day.AddDate(0, 0, 1)
	const body = "pending helper report"
	saved, err := s.SaveChatInput(withPending.ID, "saved-input-bounded-1", "agent", body)
	require.NoError(t, err)

	logs := map[string]*archiveReadLog{}
	for _, sid := range []string{withPending.ID, withoutPending.ID} {
		archive, storeErr := s.archiveStore(sid)
		require.NoError(t, storeErr)
		log := &archiveReadLog{}
		archive.onRead = log.observe
		logs[sid] = log
	}
	// Positive control: the instrument sees a lifetime scan when one happens.
	control, err := s.archiveStore(withoutPending.ID)
	require.NoError(t, err)
	require.NoError(t, control.ScanAll(func(ArchiveAddress, ArchiveRecord) bool { return true }))
	kinds, _ := logs[withoutPending.ID].snapshot()
	require.Contains(t, kinds, "scan", "SETUP: the read instrument must observe a scan")
	logs[withoutPending.ID].reset()

	none, err := s.PendingChatInputs(context.Background(), withoutPending.ID)
	require.NoError(t, err)
	require.Empty(t, none)
	kinds, _ = logs[withoutPending.ID].snapshot()
	require.Empty(t, kinds, "nothing pending: the per-turn check reads no archive record at all")

	pending, err := s.PendingChatInputs(context.Background(), withPending.ID)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, body, pending[0].Content)
	kinds, addrs := logs[withPending.ID].snapshot()
	require.Equal(t, []string{"record"}, kinds, "one pending input: exactly its own record is read, no scan")
	require.Equal(t, saved, addrs[0])
}
