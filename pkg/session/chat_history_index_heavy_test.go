//go:build history_heavy

package session

import (
	"fmt"
	"testing"
)

// G3/T06/T18: this100k-prefix matrix belongs to the explicit heavy history
// measurement CI job, not the ordinary10-minute session-package shard. Same
// shared fixture and assertions as the ordinary1k control, no skipped cases.
// Run with goolm,stdjson,history_heavy and select this exact test entry.
func TestChatHistoryIndex_HealthyCutAndBoundedReads100K(t *testing.T) {
	for _, limit := range []int{40, 100} {
		t.Run(fmt.Sprintf("prefix100000_limit%d", limit), func(t *testing.T) {
			historyHealthyCutAndBoundedReads(t, 100000, limit)
		})
	}
}
