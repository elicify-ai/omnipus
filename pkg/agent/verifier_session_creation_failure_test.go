package agent

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// P3 oracles: founder-authorized Option A; provisioning-ruling.md section 3 P3;
// autonomous-agent-plan-execution-spec.md FR-011/036/037; ADR-049 D7. A failed
// real verifier-session prerequisite is not a concurrency collision, a review
// that already made progress, or a verdict against the worker's finished work.
func TestVerifierProvisioningP3(t *testing.T) {
	for _, mode := range []struct {
		name     string
		external bool
	}{{"native", false}, {"external", true}} {
		t.Run(mode.name+"_healthy_dispatch_control", func(t *testing.T) {
			clock := p3StopAfterFirstBackoff(t)
			f := newP3VerifierFixture(t, mode.external, "c1")
			input := p3TaskInput("p3-healthy-" + mode.name)
			f.unitID = verifierUnitForTask(input.TaskID)
			result := f.al.JudgeCriteria(context.Background(), input)
			p3AssertHealthyDispatch(t, f, result, mode.external)
			if waits := clock.durations(); len(waits) != 0 {
				t.Errorf("healthy verifier backoffs = %v, want none", waits)
			}
		})
		t.Run(mode.name+"_creation_failure_is_d7_without_dispatch", func(t *testing.T) {
			clock := p3StopAfterFirstBackoff(t)
			f := newP3VerifierFixture(t, mode.external, "c1")
			cause := p3FaultVerifierCreation(t, f.al)
			input := p3TaskInput("p3-fault-" + mode.name)
			f.unitID = verifierUnitForTask(input.TaskID)
			result := f.al.JudgeCriteria(context.Background(), input)
			if !result.Unavailable || result.Verdict != nil || result.ConcurrencyBackoff {
				t.Errorf("provisioning failure result = %+v, want Unavailable=true, Verdict=nil, ConcurrencyBackoff=false", result)
			}
			p3AssertNoDispatch(t, f)
			p3AssertStorageReason(t, result.Reason, cause)
			// ADR-049 D7 specifies 60s/120s/300s; the clock ends the first wait.
			if waits := clock.durations(); !reflect.DeepEqual(waits, []time.Duration{60 * time.Second}) {
				t.Errorf("provisioning D7 backoffs = %v, want exactly [1m0s]", waits)
			}
		})
	}
	t.Run("creator_returns_no_id_and_original_storage_error", func(t *testing.T) {
		f := newP3VerifierFixture(t, false, "c1")
		fault := p3FaultVerifierCreation(t, f.al)
		creator := p3SessionCreator(t, f.al)
		id, err := creator("agent:judge:verify:p3-storage", "task:p3-storage")
		if id != "" {
			t.Errorf("failed creator session ID = %q, want empty, never a synthetic verify: ID", id)
		}
		var cause *os.PathError
		if err == nil || !errors.As(err, &cause) {
			t.Fatalf("creator error = %v, want the original storage PathError in its error chain", err)
		}
		if cause.Op != fault.Op || cause.Path != fault.Path || !errors.Is(err, fault.Err) {
			t.Errorf("creator storage cause = %+v, want op=%q path=%q and wrapped cause %v", cause, fault.Op, fault.Path, fault.Err)
		}
		p3AssertNoDispatch(t, f)
	})
	t.Run("repeated_creation_outage_preserves_goal_round_and_criteria", p3GoalRoundConservation)
}

func p3TaskInput(taskID string) JudgeCriteriaInput {
	return JudgeCriteriaInput{
		Scope: task.VerdictScopeTask, TaskID: taskID, AssigneeAgentID: "native-agent",
		Criteria: []task.AcceptanceCriterion{proseCriterion("c1", "the proof artifact was reviewed")},
		Attempt:  1, ClaimText: "read " + p3ProofFile + ": " + p3ProofText,
	}
}

func p3AssertStorageReason(t *testing.T, reason string, cause *os.PathError) {
	t.Helper()
	for _, part := range []string{cause.Path, cause.Err.Error()} {
		if !strings.Contains(reason, part) {
			t.Errorf("provisioning reason = %q, want original filesystem cause component %q", reason, part)
		}
	}
	if reason == VerifierConcurrencyBackoffReason || strings.HasPrefix(reason, JudgeUnfinishedReasonPrefix) || strings.Contains(reason, "judge_not_configured") {
		t.Errorf("configured-Judge provisioning outage was misclassified: %q", reason)
	}
}

func p3AssertNoDispatch(t *testing.T, f *p3VerifierFixture) {
	t.Helper()
	registered, unregistered := f.registry.history()
	for _, counter := range []struct {
		name  string
		value int
	}{
		{"model/provider calls", f.provider.callCount()},
		{"real read_file executions", int(f.readFile.calls.Load())},
		{"external driver constructions", int(f.factoryCalls.Load())},
		{"external runner executions", len(f.runOptions())},
		{"registry registration attempts", len(registered)},
		{"registry unregister attempts", len(unregistered)},
	} {
		if counter.value != 0 {
			t.Errorf("provisioning failure: %s = %d, want exactly 0; registration history=%+v", counter.name, counter.value, registered)
		}
	}
	if id, exists := f.registry.Lookup(f.unitID); exists {
		t.Errorf("provisioning failure left live registry entry %q -> %q", f.unitID, id)
	}
}

func p3AssertHealthyDispatch(t *testing.T, f *p3VerifierFixture, result JudgeCriteriaResult, external bool) {
	t.Helper()
	if result.Unavailable || result.ConcurrencyBackoff || result.Verdict == nil || !result.Verdict.Met {
		t.Fatalf("healthy-control result = %+v, want a real met verdict", result)
	}
	criteria := result.Verdict.PerCriterion
	if len(criteria) != 1 || criteria[0].CriterionID != "c1" || !criteria[0].Met || criteria[0].Reason != "read proof artifact" || criteria[0].EvidenceQuote != p3ProofText {
		t.Errorf("healthy-control criteria = %+v, want exactly c1/met/read proof artifact/%q", criteria, p3ProofText)
	}
	registered, unregistered := f.registry.history()
	if len(registered) != 1 || registered[0].unitID != f.unitID || registered[0].sessionID == "" || strings.HasPrefix(registered[0].sessionID, "verify:") {
		t.Fatalf("healthy-control registry history = %+v, want one real ID for %q", registered, f.unitID)
	}
	if !reflect.DeepEqual(unregistered, []string{f.unitID}) {
		t.Errorf("healthy-control unregister history = %v, want [%s]", unregistered, f.unitID)
	}
	if id, exists := f.registry.Lookup(f.unitID); exists {
		t.Errorf("healthy-control registry still holds %q after completion", id)
	}
	wantDispatches := 2 // Scripted native response: one read_file request, then verdict.
	if external {
		wantDispatches = 1 // One actual external driver construction and Run.
		options := f.runOptions()
		if f.provider.callCount() != 0 || f.readFile.calls.Load() != 0 || f.factoryCalls.Load() != 1 || len(options) != 1 {
			t.Errorf("external control: provider=%d tool=%d factory=%d runs=%d, want 0/0/1/1", f.provider.callCount(), f.readFile.calls.Load(), f.factoryCalls.Load(), len(options))
		} else if options[0].WorkDir != f.workDir || options[0].MaxTurns != extTestEffectiveLimit || !strings.Contains(options[0].Input, p3ProofText) {
			t.Errorf("external Run arguments = %+v, want workspace=%q, turns=%d and actual claim marker", options[0], f.workDir, extTestEffectiveLimit)
		}
	} else {
		text, failed := f.readFile.outcome()
		if f.provider.callCount() != 2 || f.readFile.calls.Load() != 1 || failed || !strings.Contains(text, p3ProofText) || f.factoryCalls.Load() != 0 || len(f.runOptions()) != 0 {
			t.Errorf("native control: provider=%d tool=%d failed=%v text=%q factory=%d runs=%d, want two calls/one successful real read/no CLI", f.provider.callCount(), f.readFile.calls.Load(), failed, text, f.factoryCalls.Load(), len(f.runOptions()))
		}
	}
	f.mu.Lock()
	dispatches := append([]p3DispatchObservation(nil), f.dispatches...)
	f.mu.Unlock()
	if len(dispatches) != wantDispatches {
		t.Errorf("healthy-control dispatch observations = %d, want %d", len(dispatches), wantDispatches)
	}
	for _, seen := range dispatches {
		if seen.err != nil || seen.meta == nil || seen.sessionID != registered[0].sessionID || seen.meta.Type != session.SessionTypeVerifier || seen.meta.Title != "Verifier: "+f.unitID {
			t.Errorf("before healthy dispatch: ID=%q meta=%+v error=%v, want registered real verifier with exact title %q", seen.sessionID, seen.meta, seen.err, "Verifier: "+f.unitID)
		}
	}
}
