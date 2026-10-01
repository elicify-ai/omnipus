package agent

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// Drive the real accounting owner, not only JudgeCriteria's disposition flag.
// D7 and P3 require conservation of a work-quality round on repeated outages.
func p3GoalRoundConservation(t *testing.T) {
	resetGoalTriggerStateForTest()
	t.Cleanup(resetGoalTriggerStateForTest)
	clock := p3StopAfterFirstBackoff(t)
	f := newP3VerifierFixture(t, false, recordedGoalCriterionID)
	worker, exists := f.al.GetRegistry().GetAgent("native-agent")
	if !exists || worker == nil {
		t.Fatal("P3 goal premise: native worker missing")
	}
	store, sid := newGoalTestSession(t, f.al, worker.ID)
	f.al.recordGoalRouting(sid, "", "webchat", "p3-goal-chat", "p3-goal-history", worker.ID)
	const initialRound = 1 // Deliberately nonzero: resetting accounting to zero is not preservation.
	setGoalRoundsArmedRecorded(t, store, sid, "the proof artifact was reviewed", initialRound, time.Now())
	rec := goalRecordForSession(t, sid)
	beforeCriteria := append([]task.AcceptanceCriterion(nil), rec.Criteria...)
	beforeDoD := append([]task.AcceptanceCriterion(nil), rec.DoD...)
	if rec.Round != initialRound || rec.LatestVerdict != nil || len(beforeCriteria) == 0 {
		t.Fatalf("P3 goal premise: expected a recorded active goal at round 1 with no verdict: %+v", rec)
	}
	f.unitID = verifierUnitForGoal(sid)
	cause := p3FaultVerifierCreation(t, f.al)
	collector, stopCollector := newEventCollector(t, f.al)
	t.Cleanup(stopCollector)
	steers := 0
	// Two consecutive outage adjudications must preserve exactly the same round;
	// this is a conservation check, not a new production retry-count limit.
	for occurrence := 1; occurrence <= 2; occurrence++ {
		met, unavailable := f.al.runGoalAdjudication(context.Background(), worker, "", sid, store, rec,
			"read "+p3ProofFile+": "+p3ProofText, func(string) { steers++ })
		if met || !unavailable {
			t.Errorf("outage %d: met=%v unavailable=%v, want false/true with no scored review", occurrence, met, unavailable)
		}
		p3AssertNoDispatch(t, f)
		after := goalRecordForSession(t, sid)
		if after.Round != initialRound || after.LatestVerdict != nil {
			t.Errorf("outage %d: Round=%d LatestVerdict=%+v, want exactly Round=1 and nil verdict", occurrence, after.Round, after.LatestVerdict)
		}
		if !reflect.DeepEqual(after.Criteria, beforeCriteria) || !reflect.DeepEqual(after.DoD, beforeDoD) {
			t.Errorf("outage %d changed unjudged criteria: Criteria=%+v DoD=%+v, want original Criteria=%+v DoD=%+v", occurrence, after.Criteria, after.DoD, beforeCriteria, beforeDoD)
		}
	}
	stopCollector()
	if steers != 0 {
		t.Errorf("provisioning outage worker steers = %d, want exactly 0", steers)
	}
	if waits := clock.durations(); !reflect.DeepEqual(waits, []time.Duration{60 * time.Second, 60 * time.Second}) {
		t.Errorf("two fresh outage adjudications used backoffs %v, want [1m0s 1m0s] (ADR-049 D7)", waits)
	}
	payloads := goalStatusPayloadsFor(collector, sid)
	if len(payloads) == 0 {
		t.Fatal("provisioning outage emitted no visible goal status")
	}
	last := payloads[len(payloads)-1]
	if last.State != goalPillJudgeUnavailable {
		t.Errorf("provisioning outage visible state = %q, want %q", last.State, goalPillJudgeUnavailable)
	}
	p3AssertStorageReason(t, last.LatestReason, cause)
}
