package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Gate F-1/architect M-2, squad-lead section W: THREE total attempts, not
// 64 suffix-recovery items. Finite controlled churn lets the bad unbounded
// implementation finish on attempt5, proving the defect without a hang timer.
func TestChatHistoryRead_RetentionChurnStopsAfterThreeAttempts(t *testing.T) {
	for _, churn := range []int{0, 1, 2, 4} {
		t.Run(fmt.Sprintf("actual_retention_moves_%d", churn), func(t *testing.T) {
			s, sid := newEffectsSession(t)
			days := make([]chatHistoryFixtureDay, 0, 5)
			for i := 1; i <= 4; i++ {
				days = append(days, historyDayRows(fmt.Sprintf("2026-01-%02d", i), fmt.Sprintf("expiring-%d", i), 1, nil))
			}
			days = append(days, historyDayRows("2026-01-05", "retained", 80, nil))
			addresses := buildChatHistoryDayFixture(t, s, sid, days)
			require.Len(t, historyRead(t, s, sid, historyOptions(100)).Entries, 84, "all actual sources healthy before controlled retention churn")
			currentPath := filepath.Join(s.BaseDir(), sid, archiveCurrentFile)
			current, err := os.ReadFile(currentPath)
			require.NoError(t, err)
			attempts, moves := 0, 0
			oldHook := chatHistoryReadHook
			chatHistoryReadHook = func(_ context.Context, event chatHistoryReadEvent) error {
				if event.SessionID != sid || event.Scope != "page" || event.Phase != "cut_captured" {
					return nil
				}
				attempts++
				if moves >= churn {
					return nil // bad implementation gets a stable fifth cut, never spins this fixture forever
				}
				path := filepath.Join(s.BaseDir(), sid, addresses[moves].PartitionKey+".jsonl")
				old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
				if touchErr := os.Chtimes(path, old, old); touchErr != nil {
					return touchErr
				}
				removed, sweepErr := s.RetentionSweep(90) // real sweep/compaction/generation move, not a forged checkpoint value
				if sweepErr != nil {
					return sweepErr
				}
				require.Equal(t, 1, removed, "one actual newly eligible unselected old partition per cut")
				moves++
				return nil
			}
			t.Cleanup(func() { chatHistoryReadHook = oldHook })
			metrics := &ChatHistoryReadMetrics{}
			ctx := WithChatHistoryReadMetrics(context.Background(), metrics)
			page, readErr := s.ReadChatHistoryPage(ctx, sid, historyOptions(40))
			chatHistoryReadHook = oldHook
			if churn < 3 {
				require.NoError(t, readErr, "T48: a cut stabilizing on attempt1,2 OR3 succeeds, not blanket third-attempt refusal")
				require.Equal(t, churn+1, attempts)
				require.Equal(t, churn, moves)
				require.EqualValues(t, churn, metrics.RetentionRetries)
				require.Len(t, page.Entries, 40)
				wantIDs := make([]string, 40)
				for i := range wantIDs {
					wantIDs[i] = fmt.Sprintf("retained-%03d", 41+i)
					// Four rolled entries precede the80 current entries; latest40
					// are canonical addresses44..83, independent of reselect count.
					require.Equal(t, historyExpectedPosition(sid, addresses[44+i]), page.Entries[i].Position)
				}
				require.Equal(t, wantIDs, historyIDs(page), "all exact retained entries, no omitted/duplicated/substituted middle row")
			} else {
				assert.Equal(t, 3, attempts, "W: at most initial+2 reselects; a fourth cut is forbidden")
				assert.Equal(t, 3, moves, "normal sweep fixture must not be chased past third moved cut")
				assert.EqualValues(t, 3, metrics.RetentionRetries, "every discarded moved cut stays counted, including exhaustion")
				if assert.ErrorIs(t, readErr, ErrChatHistoryUnavailable, "W: exhaustion must fail visibly, never return a successful eventually-stable page") {
					var unavailable *ChatHistoryUnavailableError
					require.ErrorAs(t, readErr, &unavailable)
					assert.Equal(t, "index_inconsistent", unavailable.Reason, "existing reason/enum, no new wire value")
				}
			}
			after, err := os.ReadFile(currentPath)
			require.NoError(t, err)
			require.Equal(t, current, after, "retry exhaustion never modifies retained canonical content")
		})
	}
}
