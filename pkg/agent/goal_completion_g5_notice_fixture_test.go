package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// assertG5NoticeStable preserves the original exact notice-id/note oracle and
// checks the exact ring count for the explicit delivery passes in its caller.
// ADR-20261004 locked decision 1 supersedes one ring across untaken replay:
// "A stop notice rings until the parent takes it" and "A second ring does not
// make the parent do the work twice." The durable message stays singular.
func assertG5NoticeStable(t *testing.T, al *AgentLoop, parentID string, original *session.LifecycleRecord, cause, actor, noticeID, stopNote string, wakeCount func(string) int, wantRings int) {
	t.Helper()
	id, note := assertU1StoppedChildNotice(t, al, parentID, original, cause, actor)
	if id != noticeID || note != stopNote {
		t.Errorf("retry/replay changed notice identity or stop event: id %q -> %q; note %s -> %s (D6)", noticeID, id, stopNote, note)
	}
	if got := wakeCount(noticeID); got != wantRings {
		t.Errorf("stopped-child notice rings = %d, want exactly %d for the explicit untaken/taken delivery passes (ADR-20261004 decision 1)", got, wantRings)
	}
}
