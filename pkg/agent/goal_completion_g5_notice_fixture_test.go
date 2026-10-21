package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// assertG5NoticeStable preserves the original exact notice-id/note oracle and
// checks the exact ring count for the explicit delivery passes in its caller.
// PW-D13 (founder 2026-10-15): a Stop notice never rings, including replay.
// Callers keep exact zero-ring checks and the single durable message identity.
func assertG5NoticeStable(t *testing.T, al *AgentLoop, parentID string, original *session.LifecycleRecord, cause, actor, noticeID, stopNote string, wakeCount func(string) int, wantRings int) {
	t.Helper()
	id, note := assertU1StoppedChildNotice(t, al, parentID, original, cause, actor)
	if id != noticeID || note != stopNote {
		t.Errorf("retry/replay changed notice identity or stop event: id %q -> %q; note %s -> %s (D6)", noticeID, id, stopNote, note)
	}
	if got := wakeCount(noticeID); got != wantRings {
		t.Errorf("stopped-child notice rings = %d, want exactly %d across untaken/taken delivery passes (PW-D13)", got, wantRings)
	}
}
