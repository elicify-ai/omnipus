package session

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// T26/B32: the actual record handle is held at record_opened, never a mock
// reader or merely cut_captured. Deadlock deadlines are failure backstops only.
func TestChatHistoryReadCut_SlowReadDoesNotHoldAppendLocks(t *testing.T) {
	for _, writer := range []string{"chat", "settle_effect", "disposition", "rollover"} {
		t.Run(writer, func(t *testing.T) {
			s, sid := newEffectsSession(t)
			days := []chatHistoryFixtureDay{
				{Day: "2026-01-01", Entries: []TranscriptEntry{{ID: "user-before", Role: "user", Content: "before", AgentID: "mia", ClientMessageID: "client-before", Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}},
				{Day: "2026-01-02", Entries: []TranscriptEntry{{ID: "tool-before", Type: EntryTypeToolCall, Role: "assistant", Content: "call", AgentID: "mia", Timestamp: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), ToolCalls: []ToolCall{{ID: "pending", Tool: "read_file", Status: "pending"}}}}},
				historyDayRows("2026-01-03", "current", 1, nil),
			}
			addresses := buildChatHistoryDayFixture(t, s, sid, days)
			opened, release, done := make(chan chatHistoryReadEvent, 1), make(chan struct{}), make(chan struct{})
			var block, unblock sync.Once
			old := chatHistoryReadHook
			chatHistoryReadHook = func(ctx context.Context, e chatHistoryReadEvent) error {
				if e.SessionID == sid && e.Scope == "page" && e.Phase == "record_opened" && e.Stream == "archive" && e.Address != nil && e.Address.PartitionKey == "2026-01-02" {
					block.Do(func() {
						opened <- e
						select {
						case <-release:
						case <-ctx.Done():
						}
					})
				}
				return nil
			}
			var page ChatHistoryPage
			var readErr error
			opts := historyOptions(40)
			opts.WindowStart = historyExpectedPosition(sid, addresses[0])
			opts.WindowEnd = historyExpectedPosition(sid, addresses[1])
			go func() { defer close(done); page, readErr = s.ReadChatHistoryPage(context.Background(), sid, opts) }()
			t.Cleanup(func() {
				unblock.Do(func() { close(release) })
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Error("reader did not join after barrier release")
				}
				chatHistoryReadHook = old
			})
			var cut chatHistoryReadEvent
			select {
			case cut = <-opened:
			case <-done:
				require.NoError(t, readErr, "reader returned before actual record_opened barrier")
				t.Fatal("real content read never entered record_opened")
			case <-time.After(10 * time.Second):
				t.Fatal("actual content-open barrier was never reached")
			}
			writeDone := make(chan error, 1)
			go func() {
				var err error
				restoreClock := func() {}
				switch writer {
				case "settle_effect":
					_, err = s.SettleToolCall(sid, addresses[1], "pending", ToolCall{ID: "pending", Tool: "read_file", Status: "success", Result: map[string]any{"text": "after-cut"}})
				case "disposition":
					err = s.RecordInputDiscarded(sid, "user-before", "client-before")
				default:
					if writer == "rollover" {
						a, e := s.archiveStore(sid)
						if e != nil {
							writeDone <- e
							return
						}
						clock := a.now
						a.now = func() time.Time { return time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC) }
						restoreClock = func() { a.now = clock }
					}
					_, err = s.AppendTranscriptAddressed(sid, TranscriptEntry{ID: "after-cut", Role: "user", Content: "accepted after cut", AgentID: "mia", Timestamp: time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)})
				}
				restoreClock()
				writeDone <- err
			}()
			select {
			case err := <-writeDone:
				require.NoError(t, err, "real append must finish WHILE content handle is blocked")
			case <-time.After(10 * time.Second):
				unblock.Do(func() { close(release) })
				<-writeDone
				t.Fatal("append was held hostage by history content I/O")
			}
			unblock.Do(func() { close(release) })
			<-done
			require.NoError(t, readErr)
			require.Equal(t, []string{"user-before", "tool-before"}, historyIDs(page))
			require.Equal(t, cut.Cut.Generation(), page.Cut.Generation(), "returned page keeps the captured immutable root")
			require.Equal(t, "pending", page.Entries[1].Entry.ToolCalls[0].Status, "later settle excluded from old cut")
			require.Nil(t, page.Entries[0].Entry.InputDisposition, "later disposition excluded from old cut")
			require.NotContains(t, historyIDs(page), "after-cut", fmt.Sprintf("writer=%s", writer))
		})
	}
}
