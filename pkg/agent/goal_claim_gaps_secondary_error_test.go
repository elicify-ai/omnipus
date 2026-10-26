// #1431 silent-failure finding 2: if the storage-error transcript note also
// cannot be written, the live notice must retain that secondary failure.
package agent

import (
	"errors"
	"strings"
	"syscall"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
)

func TestGoalAdjudication_FinalRoundFailureReportsSecondaryTranscriptError(t *testing.T) {
	g := newGap1431Chat(t)
	if _, err := resolveGoalRecordStore().Update(g.gid, func(cur *goal.Goal) error {
		cur.Round = cur.MaxRounds - 1
		return nil
	}); err != nil {
		t.Fatalf("arrange final round: %v", err)
	}
	work := g.emptyClaim(t, "gap1431-secondary-fault", "The report is ready for checking.")
	before := mustGoalRecord(t, g.gid)
	failGoalRecordWrites(t, g.gid)
	restore := func() {}
	judge := &gap1431Judge{met: false, reason: "the requested acceptance is missing"}
	judge.beforeReply = func(int) {
		// The Judge's evidence has already been assembled. Fail the session
		// filesystem now, not the initial claim scan, then prove the actual
		// system-note writer also fails. The helper preserves the transcript.
		restore = failTranscriptReads(t, g.store, g.sid)
		if err := g.al.writeGoalSystemTranscriptErr(g.store, g.sid, g.agent.ID, "fault probe"); !errors.Is(err, syscall.ENOTDIR) {
			t.Errorf("secondary write-fault instrument=%v, want ENOTDIR", err)
		}
	}
	g.judge.Provider = judge
	events, stop := recordAgentEvents(t, g.al)
	g.al.dispatchDeferredGoalAdjudication(work)
	stop()
	restore()

	if len(judge.snapshot()) != 1 {
		t.Fatalf("Judge calls=%d, want exactly one final-round adjudication", len(judge.snapshot()))
	}
	after := mustGoalRecord(t, g.gid)
	if after.State != generated.GoalStateActive || after.Round != before.Round {
		t.Errorf("double storage failure left state/round=%q/%d, want active/%d", after.State, after.Round, before.Round)
	}
	requireGap1431StorageFailureVisible(t, events, g.sid, before.Round)
	events.mu.Lock()
	defer events.mu.Unlock()
	for _, event := range events.events {
		if payload, ok := event.Payload.(ErrorPayload); ok && payload.SessionID == g.sid && payload.Code == string(CodeJudgeConclusionFailed) {
			if !strings.Contains(payload.Detail, "is a directory") || !strings.Contains(payload.Detail, "not a directory") {
				t.Errorf("live failure detail=%q, want both primary EISDIR goal error and secondary ENOTDIR transcript error", payload.Detail)
			}
		}
	}
}
