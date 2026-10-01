package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// P3 successful-backoff branch (#1081 category 3, CHECK finding): every
// existing P3 outage clock (p3StopAfterFirstBackoff) returns an error on the
// FIRST D7 wait, so registerVerifierSession's wait-succeeded → retry-creation
// branch (agentLoopRunVerifierAdjudicationContinue) never executed in the
// existing suite. The CHECK mutant that falls through to dispatch on a
// successful wait survived the whole pack there. These rows are the first to
// walk that branch: the first backoff wait SUCCEEDS, and every expectation
// below comes from ADR-049 D7 (schedule 60s/120s/300s; a zero-progress failure
// waits, then retries the SAME failed step) and the P3 oracles the shared
// helpers already encode (FR-011/036/037, provisioning-ruling.md §3: no
// dispatch before a real registered session, never a synthetic id).
func TestVerifierProvisioningP3_SuccessfulBackoffWait(t *testing.T) {
	for _, mode := range []struct {
		name     string
		external bool
	}{{"native", false}, {"external", true}} {
		t.Run(mode.name+"_outage_retries_creation_after_successful_wait", func(t *testing.T) {
			f := newP3VerifierFixture(t, mode.external, "c1")
			cause := p3FaultVerifierCreation(t, f.al) // persistent outage: never heals
			clock := p3SucceedFirstBackoffClock(t, nil)
			input := p3TaskInput("p3-successful-wait-outage-" + mode.name)
			f.unitID = verifierUnitForTask(input.TaskID)
			result := f.al.JudgeCriteria(context.Background(), input)
			if !result.Unavailable || result.Verdict != nil || result.ConcurrencyBackoff {
				t.Errorf("persistent outage result = %+v, want Unavailable=true, Verdict=nil, ConcurrencyBackoff=false", result)
			}
			p3AssertNoDispatch(t, f)
			p3AssertStorageReason(t, result.Reason, cause)
			// Two waits on the D7 schedule's first two legs prove the failed
			// creation step was RE-ATTEMPTED after the successful wait: with zero
			// dispatches (asserted above) neither wait can be a turn-failure
			// wait, so both waits are creation-failure waits — creation was
			// attempted at least twice. The CHECK mutant (successful wait falls
			// through to dispatch) cannot produce this sequence: it stops after
			// [60s] when its bogus dispatch verdict resolves, or shows a 60s
			// turn-failure wait as the second leg — never [60s 2m0s].
			if waits := clock.durations(); !reflect.DeepEqual(waits, []time.Duration{60 * time.Second, 120 * time.Second}) {
				t.Errorf("successful-wait outage backoffs = %v, want exactly [1m0s 2m0s] (ADR-049 D7: creation retried after the successful wait)", waits)
			}
		})
		t.Run(mode.name+"_recovery_dispatches_only_after_real_session", func(t *testing.T) {
			f := newP3VerifierFixture(t, mode.external, "c1")
			_, heal := p3HealableCreationFault(t, f.al)
			// The successful first wait repairs the real storage fault, so the
			// retried creation attempt finds a healthy store — creation then
			// succeeds and recovery dispatches.
			clock := p3SucceedFirstBackoffClock(t, heal)
			input := p3TaskInput("p3-successful-wait-recovery-" + mode.name)
			f.unitID = verifierUnitForTask(input.TaskID)
			result := f.al.JudgeCriteria(context.Background(), input)
			// The recovered adjudication must be indistinguishable from the
			// healthy control: a met verdict, exactly one registration of a
			// real (never synthetic) session id, exactly one review dispatch,
			// and every dispatch observation made against the REAL PERSISTED
			// verifier session (the observer reads the actual store before any
			// provider/driver output is accepted). The CHECK mutant dispatches
			// its first turn right after the successful wait — against the
			// fabricated "task:<sessionKey>" chatID dispatchVerifierTurn
			// substitutes for an empty one — so its first observation carries
			// "no real verifier session registered before dispatch" and its
			// registry history is empty.
			p3AssertHealthyDispatch(t, f, result, mode.external)
			// ...except it went through exactly ONE successful D7 wait first.
			if waits := clock.durations(); !reflect.DeepEqual(waits, []time.Duration{60 * time.Second}) {
				t.Errorf("recovery backoffs = %v, want exactly [1m0s] (ADR-049 D7) before the successful retry", waits)
			}
		})
	}
}

// p3SucceedFirstBackoffClock is the one clock double this file adds: the FIRST
// D7 wait succeeds (returns nil) — the branch every existing P3 outage clock
// skips — and every later wait fails, so a scenario that should have resolved
// by then cannot spin (JudgeCriteria retries forever on a deadline-less ctx).
// onFirstWait runs synchronously on the adjudication's goroutine
// (judgeBackoffWait calls judgeSleepFn inline), so it can repair a healable
// creation fault before the retried creation attempt runs. Waits are recorded
// on the shared p3BackoffObserver shape, so assertions reuse durations().
func p3SucceedFirstBackoffClock(t *testing.T, onFirstWait func()) *p3BackoffObserver {
	t.Helper()
	previous := judgeSleepFn
	t.Cleanup(func() { judgeSleepFn = previous })
	o := &p3BackoffObserver{}
	judgeSleepFn = func(_ context.Context, duration time.Duration) error {
		o.mu.Lock()
		first := len(o.waits) == 0
		o.waits = append(o.waits, duration)
		o.mu.Unlock()
		if first {
			if onFirstWait != nil {
				onFirstWait()
			}
			return nil
		}
		return errors.New("p3 test clock: stop after the successful first wait")
	}
	return o
}

// p3HealableCreationFault installs the same real storage fault as
// p3FaultVerifierCreation (the Judge's owned verifier store base dir becomes a
// regular file, so NewVerifierSession fails with the real PathError), but
// returns heal so the fault can be repaired MID-adjudication — from the
// successful backoff wait — and its cleanup is idempotent about the
// already-healed state, which the existing helper's cleanup (blocker removed +
// backup renamed back at test end) does not tolerate.
func p3HealableCreationFault(t *testing.T, al *AgentLoop) (cause *os.PathError, heal func()) {
	t.Helper()
	store := al.GetAgentStore(string(coreagent.IDJudge))
	if store == nil {
		t.Fatal("P3 premise: real Judge UnifiedStore missing")
	}
	meta, err := store.NewVerifierSession(string(coreagent.IDJudge))
	if err != nil || meta == nil || meta.ID == "" || meta.Type != session.SessionTypeVerifier {
		t.Fatalf("P3 premise: healthy real verifier creation failed: meta=%+v err=%v", meta, err)
	}
	base := store.BaseDir()
	backup := filepath.Join(t.TempDir(), "healable-verifier-store")
	if err := os.Rename(base, backup); err != nil {
		t.Fatalf("P3 premise: isolate owned verifier directory: %v", err)
	}
	healed := false
	t.Cleanup(func() {
		if healed {
			return
		}
		if err := os.Remove(base); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("P3 cleanup: remove directory blocker: %v", err)
		}
		if err := os.Rename(backup, base); err != nil {
			t.Errorf("P3 cleanup: restore real verifier store: %v", err)
		}
	})
	if err := os.WriteFile(base, []byte("not a session directory"), 0o600); err != nil {
		t.Fatalf("P3 premise: install directory-as-file fault: %v", err)
	}
	failedMeta, creationErr := store.NewVerifierSession(string(coreagent.IDJudge))
	var pathErr *os.PathError
	if failedMeta != nil || creationErr == nil || !errors.As(creationErr, &pathErr) {
		t.Fatalf("P3 fault not established: meta=%+v error=%v; want nil metadata and a real PathError", failedMeta, creationErr)
	}
	if pathErr.Path != base {
		t.Fatalf("P3 fault path = %q, want the blocked real store %q", pathErr.Path, base)
	}
	t.Logf("P3 healable storage fault: configured Judge=%s op=%s path=%s cause=%v", coreagent.IDJudge, pathErr.Op, pathErr.Path, pathErr.Err)
	return pathErr, func() {
		if healed {
			return
		}
		if err := os.Remove(base); err != nil {
			t.Errorf("P3 heal: remove directory blocker: %v", err)
			return
		}
		if err := os.Rename(backup, base); err != nil {
			t.Errorf("P3 heal: restore real verifier store: %v", err)
			return
		}
		healed = true
	}
}
