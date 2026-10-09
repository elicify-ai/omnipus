// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// U5b security-finding regression pack: F2 (a hard Stop before the external
// run's cancel funcs exist), F3 (a resolver failure must still run the turn
// teardown), and F4 = Property B (a worker's fixed Is3P classification that
// disagrees with its live executor must VISIBLY refuse, never silently flip).
//
// Provenance: the F2 and F3 proofs began as read-only security-pass candidates
// (omnipus-investigations/u5b-security-pass-8b9f6611a), compiled, run and
// mutation-checked by qa-lead's u5b-proofs lane against 8b9f6611a, and are
// landed here as the durable regression guards for backend-lead's fixes on the
// U5b stack. F4's own two-direction test is added by this lane.
package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// ---------------------------------------------------------------------------
// F3 — a resolver error must still run the turn teardown
// ---------------------------------------------------------------------------

// Oracle: the review brief requires clearActiveTurn -> Finish on every admitted
// body exit, including resolver failures; Finish must settle a claimed cancel.
// Real: Launch, Dispatch, reconstruction, resolver, completion, and disposition.
// Instrument: the existing post-registration hook captures the admitted handle
// and installs a nonblocking cancel-report callback before its goroutine runs.
// A supported worker-runtime update to reserved remote-a2a is represented by
// replacing the registry instance's executor BEFORE Dispatch (no live mutation).
func TestU5BSecurity_ResolverFailureFinishesAdmittedTurn(t *testing.T) {
	provider := &delegateDispatchProvider{}
	al := newDelegateDispatchLoop(t, provider)
	res := launchDelegateTo(t, al, delegateExtCLIAgentID, "resolver failure proof")

	agent, ok := al.GetRegistry().GetAgent(delegateExtCLIAgentID)
	if !ok || agent == nil {
		t.Fatal("fixture external target missing")
	}
	agent.Subagents = &config.SubagentsConfig{
		Executor: &config.ExecutorConfig{Kind: config.ExecutorKindRemoteA2A},
	}

	var admitted *turnState
	var callbackCalls atomic.Int64
	previousHook := turnRegisteredTestHook
	turnRegisteredTestHook = func(id string, ts *turnState) {
		if id != res.SessionID {
			return
		}
		admitted = ts
		ts.SetOnCancelFinish(func(string) { callbackCalls.Add(1) })
		if !ts.ClaimCancel() {
			t.Fatal("fixture could not claim the newly admitted cancel")
		}
	}
	defer func() { turnRegisteredTestHook = previousHook }()

	dispatch, err := NewSteerLauncher(al).Dispatch(context.Background(), res.SessionID, res.Generation)
	if err != nil {
		t.Fatalf("Dispatch refused before the body could be tested: %v", err)
	}
	if dispatch.State != steer.DispatchRunning {
		t.Fatalf("Dispatch state = %q, want running", dispatch.State)
	}
	if admitted == nil || admitted.opts.executionDisposition == nil {
		t.Fatal("fixture failed to capture the admitted execution")
	}
	// The outer disposition is the completion barrier, not map disappearance:
	// a fixed body legitimately deletes its map entry before Finish runs.
	select {
	case <-admitted.opts.executionDisposition.done:
	case <-time.After(5 * time.Second): // harness watchdog, not a timing oracle
		t.Fatal("resolver-error execution did not settle")
	}

	if _, exists := al.activeTurnStates.Load(res.SessionID); exists {
		t.Error("settled resolver-error execution retains an active map entry")
	}
	select {
	case <-admitted.Finished():
	default:
		t.Error("resolver-error execution settled but its Finished channel is still open")
	}
	if admitted.IsAlive() {
		t.Error("resolver-error execution settled but IsAlive remains true")
	}
	if got := callbackCalls.Load(); got != 1 {
		t.Errorf("cancel-finish callback calls = %d, want exactly 1", got)
	}
	if got := provider.calls.Load(); got != 0 {
		t.Errorf("unresolvable target invoked native provider %d times, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// F2 — a pre-start hard Stop must prevent the CLI from launching
// ---------------------------------------------------------------------------

// Oracle: a previously forced Stop must prevent starting external work. The
// un-aborted control must invoke the same runner exactly once, proving the
// fixture can reach the external boundary. Only the paid/OS runner boundary is
// replaced; launch, admission, abort latch, and body dispatch remain real.
// No sleep creates the race: the existing registration hook forces the abort
// before the external body has installed either context-cancel slot.
func TestU5BSecurity_PreStartHardAbortPreventsCLI(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hardAbort bool
		wantRuns  int
	}{
		{name: "unaborted_control", wantRuns: 1},
		{name: "already_hard_aborted", hardAbort: true, wantRuns: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &delegateDispatchProvider{}
			al := newDelegateDispatchLoop(t, provider)
			fr, restore := withFakeDriver(t)
			defer restore()
			// FakeRunner.Run records even on this closed event stream, so an
			// incorrect launch terminates promptly instead of hanging the proof.
			fr.Cancel()
			res := launchDelegateTo(t, al, delegateExtCLIAgentID, "pre-start Stop proof")

			var admitted *turnState
			previousHook := turnRegisteredTestHook
			turnRegisteredTestHook = func(id string, ts *turnState) {
				if id != res.SessionID {
					return
				}
				admitted = ts
				if tc.hardAbort && !ts.requestHardAbort() {
					t.Fatal("fixture could not latch the first hard abort")
				}
			}
			defer func() { turnRegisteredTestHook = previousHook }()

			if _, err := NewSteerLauncher(al).Dispatch(context.Background(), res.SessionID, res.Generation); err != nil {
				t.Fatalf("Dispatch: %v", err)
			}
			if admitted == nil || admitted.opts.executionDisposition == nil {
				t.Fatal("fixture failed to capture the admitted execution")
			}
			select {
			case <-admitted.opts.executionDisposition.done:
			case <-time.After(5 * time.Second): // harness watchdog only
				t.Fatal("pre-start Stop execution did not settle")
			}
			if got := len(fr.RecordedRunOpts()); got != tc.wantRuns {
				t.Errorf("external runner starts = %d, want %d (hard abort=%v)", got, tc.wantRuns, tc.hardAbort)
			}
			if got := provider.calls.Load(); got != 0 {
				t.Errorf("external target invoked native provider %d times, want 0", got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// F4 = Property B — a fixed worker classification that disagrees with the live
// executor must VISIBLY refuse, in BOTH directions
// ---------------------------------------------------------------------------

// TestU5BSecurity_RuntimeChangeRefused_LaunchNativeNowExternal covers the
// SUPPORTED direction: a worker launched native (LifecycleRecord.Is3P=false)
// whose agent is then updated to an external-CLI executor. The trigger is the
// supported executor update
// (pkg/gateway/rest_agents_update.go::restAPIUpdateAgentFlow.validateTarget —
// a worker may change executor.kind to external-cli, proven by
// TestUpdateAgent_WorkerAllowsExternalCLIExecutor). Dispatch must refuse,
// leaving the record's classification untouched.
func TestU5BSecurity_RuntimeChangeRefused_LaunchNativeNowExternal(t *testing.T) {
	provider := &delegateDispatchProvider{}
	al := newDelegateDispatchLoop(t, provider)
	lifecycle := al.GetSessionLifecycleStore()

	res := launchDelegateTo(t, al, delegateNativeWorkerAgentID, "native worker, then external")
	if rec, err := lifecycle.Load(res.SessionID); err != nil {
		t.Fatalf("load native worker record: %v", err)
	} else if rec.Is3P {
		t.Fatalf("native worker record Is3P = true, want false (stamped native at launch)")
	}

	agent, ok := al.GetRegistry().GetAgent(delegateNativeWorkerAgentID)
	if !ok || agent == nil {
		t.Fatal("native worker target missing from registry")
	}
	// The executor update a person makes through the supported REST path.
	agent.Subagents = &config.SubagentsConfig{
		Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
	}

	fr, restore := withFakeDriver(t)
	defer restore()

	assertRuntimeChangeRefused(t, al, provider, fr, res, "native", "external-CLI")
}

// TestU5BSecurity_RuntimeChangeRefused_LaunchExternalNowNative covers the
// REVERSE direction: a worker launched external-CLI (Is3P=true) whose agent is
// then set to a native executor. The security pass could not establish a
// supported REST trigger for external→native, so the record/executor mismatch
// is set directly at the dispatch layer here (as the brief directs).
func TestU5BSecurity_RuntimeChangeRefused_LaunchExternalNowNative(t *testing.T) {
	provider := &delegateDispatchProvider{}
	al := newDelegateDispatchLoop(t, provider)
	lifecycle := al.GetSessionLifecycleStore()

	res := launchDelegateTo(t, al, delegateExtCLIAgentID, "external worker, then native")
	if rec, err := lifecycle.Load(res.SessionID); err != nil {
		t.Fatalf("load external worker record: %v", err)
	} else if !rec.Is3P {
		t.Fatalf("external worker record Is3P = false, want true (stamped external at launch)")
	}

	agent, ok := al.GetRegistry().GetAgent(delegateExtCLIAgentID)
	if !ok || agent == nil {
		t.Fatal("external worker target missing from registry")
	}
	// Reverse mismatch, set directly (no established REST trigger).
	agent.Subagents = &config.SubagentsConfig{
		Executor: &config.ExecutorConfig{Kind: config.ExecutorKindNative},
	}

	fr, restore := withFakeDriver(t)
	defer restore()

	assertRuntimeChangeRefused(t, al, provider, fr, res, "external-CLI", "native")
}

// assertRuntimeChangeRefused drives the real Dispatch for a worker whose fixed
// classification now disagrees with its live executor, then asserts the four
// obligations of F4: the turn is torn down through the F3 path, NO runtime
// starts (neither native nor external), and the refusal is durable and
// ACTIONABLE (it tells the person to start a new delegation).
func assertRuntimeChangeRefused(
	t *testing.T,
	al *AgentLoop,
	provider *delegateDispatchProvider,
	fr *runner.FakeRunner,
	res steer.LaunchResult,
	wasRuntime, nowRuntime string,
) {
	t.Helper()

	// Close the fake's event stream so a WRONG (unrefused) dispatch on the
	// pre-fix code terminates promptly, recording the run instead of hanging.
	fr.Cancel()

	var admitted *turnState
	previousHook := turnRegisteredTestHook
	turnRegisteredTestHook = func(id string, ts *turnState) {
		if id == res.SessionID {
			admitted = ts
		}
	}
	defer func() { turnRegisteredTestHook = previousHook }()

	if _, err := NewSteerLauncher(al).Dispatch(context.Background(), res.SessionID, res.Generation); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if admitted == nil {
		t.Fatal("turn was not admitted; nothing to observe")
	}

	select {
	case <-admitted.Finished():
	case <-time.After(5 * time.Second):
		t.Fatal("refused turn did not settle")
	}

	// F3 teardown path: the live turn is fully retired, not leaked.
	if admitted.IsAlive() {
		t.Error("refused turn still reports IsAlive")
	}
	if _, exists := al.activeTurnStates.Load(res.SessionID); exists {
		t.Error("refused turn leaked an active-map entry")
	}
	// No runtime switched under the worker's feet.
	if got := provider.calls.Load(); got != 0 {
		t.Errorf("native provider called %d times, want 0", got)
	}
	if got := len(fr.RecordedRunOpts()); got != 0 {
		t.Errorf("external runner started %d times, want 0", got)
	}

	// The refusal is durable and actionable.
	lifecycle := al.GetSessionLifecycleStore()
	waitUntil(t, 5*time.Second, "the refused worker record to be marked failed", func() bool {
		rec, loadErr := lifecycle.Load(res.SessionID)
		return loadErr == nil && rec.State == session.LifecycleFailed
	})
	rec, err := lifecycle.Load(res.SessionID)
	if err != nil {
		t.Fatalf("load refused record: %v", err)
	}
	if !strings.Contains(rec.FailedReason, "start a new delegation") {
		t.Errorf("refusal not actionable: %q", rec.FailedReason)
	}
	if !strings.Contains(rec.FailedReason, wasRuntime) || !strings.Contains(rec.FailedReason, nowRuntime) {
		t.Errorf("refusal %q should name both runtimes (%s -> %s)", rec.FailedReason, wasRuntime, nowRuntime)
	}
}
