package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChatHistoryRetention_UnlinkPublicationCrashDetectsNextZeroSweep(t *testing.T) {
	s, sid, addresses := historyRetentionFixture(t, 0)
	before := historyState(t, s, sid)
	require.NoError(t, s.Close())
	historyRunCrash(t, s.BaseDir(), sid, "partition_unlinked", 1, 86)
	reopened, err := NewUnifiedStore(s.BaseDir())
	require.NoError(t, err)
	defer func() { require.NoError(t, reopened.Close()) }()
	a, err := reopened.archiveStore(sid)
	require.NoError(t, err)
	notice, err := a.SweepExpired(90)
	require.NoError(t, err)
	require.Zero(t, notice.RemovedPartitions, "already absent file is not counted by this sweep")
	_, err = reopened.reconcileChatHistoryIndex(context.Background(), sid)
	require.NoError(t, err)
	after := historyState(t, reopened, sid)
	require.NotEqual(t, before.RetentionGeneration, after.RetentionGeneration)
	p := historyRead(t, reopened, sid, historyOptions(100))
	require.Len(t, p.Entries, 60)
	require.Equal(t, addresses[20].EntryID, p.Entries[0].Entry.ID)
	require.False(t, p.HasEarlier)
	_, found, err := reopened.lookupChatHistoryTombstone(context.Background(), sid, historyExpectedPosition(sid, addresses[0]))
	require.NoError(t, err)
	require.True(t, found)
}

func TestChatHistoryRetention_EffectTargetExpiryVersusRequiredEffectDamage(t *testing.T) {
	for _, expired := range []bool{true, false} {
		name := "retained_target_unreadable_effect_is_storage_error"
		if expired {
			name = "effect_retained_target_proven_expired_no_orphan"
		}
		t.Run(name, func(t *testing.T) {
			s, sid := newEffectsSession(t)
			old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			first := chatHistoryFixtureDay{Day: "2026-01-01", Entries: []TranscriptEntry{{ID: "old-call-owner", Type: EntryTypeToolCall, AgentID: "mia", ToolCalls: []ToolCall{{ID: "old-call", Tool: "read_file", Status: "pending"}}}}}
			if expired {
				first.MTime = &old
			}
			a := buildChatHistoryDayFixture(t, s, sid, []chatHistoryFixtureDay{first, historyDayRows("2026-01-02", "retained", 1, nil)})
			effect, err := s.SettleToolCall(sid, a[0], "pending", ToolCall{ID: "old-call", Tool: "read_file", Status: "success", Result: map[string]any{"text": "real settled result"}})
			require.NoError(t, err)
			if expired {
				_, err = s.RetentionSweep(90)
				require.NoError(t, err)
				p := historyRead(t, s, sid, historyOptions(100))
				require.Equal(t, []string{"retained-001"}, historyIDs(p), "retained effect is metadata, never an orphan public result")
			} else {
				// Prove the real dependency projects correctly before damaging it;
				// a generic not-implemented failure is not an integrity refusal.
				historyRows(t, s, sid, "later-anchor", 1)
				healthy := historyRead(t, s, sid, historyOptions(100))
				var settled bool
				for _, entry := range healthy.Entries {
					if entry.Entry.ID == "old-call-owner" {
						require.Len(t, entry.Entry.ToolCalls, 1)
						require.Equal(t, "success", entry.Entry.ToolCalls[0].Status)
						settled = true
					}
				}
				require.True(t, settled, "healthy correction source is actually eligible before corruption")
				// Keep a later saved anchor intact; damage only correction bytes.
				path := filepath.Join(s.BaseDir(), sid, "transcript.jsonl")
				f, err := os.OpenFile(path, os.O_WRONLY, 0)
				require.NoError(t, err)
				_, err = f.WriteAt([]byte{'!'}, effect.ByteOffset)
				require.NoError(t, err)
				require.NoError(t, f.Close())
				_, err = s.ReadChatHistoryPage(context.Background(), sid, historyOptions(100))
				require.Error(t, err, "required unreadable correction must never silently become a stale successful page")
			}
		})
	}
}
