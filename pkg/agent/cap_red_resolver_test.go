package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestCapRED_AdmissionView_CoherentClaimRevalidated(t *testing.T) {
	var reads atomic.Int32
	restore := config.SetMemoryProviderForTest(func() (bool, bool) { reads.Add(1); return false, true }, func() (uint64, bool) { return 1 << 30, true })
	t.Cleanup(restore)
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	al.GetConfig().Performance.MaxParallelAgents = 8
	parent := newTestSteeringSession(t, al, testHarnessWorkspaceMembershipID)
	child := capRedLaunchChild(t, al, parent, "replacement")
	old := executionClaim{SessionID: child.SessionID, Generation: child.Generation, RunID: "old-claim", BootSeq: al.bootEpochFor()}
	fresh := old
	fresh.RunID = "replacement-claim"
	if err := al.GetSessionLifecycleStore().Mutate(child.SessionID, func(r *session.LifecycleRecord) error {
		r.State = session.LifecycleRunning
		r.ExecutionID = &session.ExecutionIdentity{RunID: old.RunID, BootSeq: old.BootSeq}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Real caller reads old claim. Its first gate snapshot is legitimately absent.
	capRedAssert(t, "S49b old gate snapshot absent", capRedAdmissionView(t, al, old, false).present, false)
	publishReplacement := func() {
		d := capRedSteer(t, al.steerAdmission(), fresh.SessionID, fresh.Generation, fresh.RunID, fresh.BootSeq)
		capRedAssert(t, "real replacement reservation granted", d.granted, true)
		if err := al.GetSessionLifecycleStore().Mutate(child.SessionID, func(r *session.LifecycleRecord) error {
			r.State = session.LifecycleRunning
			r.ExecutionID = &session.ExecutionIdentity{RunID: fresh.RunID, BootSeq: fresh.BootSeq}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	dt := capRedDelegateTool(t, al)
	dt.SetLifecycleStore(&capRedLifecycleReadBoundary{LifecycleStore: al.GetSessionLifecycleStore(), target: child.SessionID, after: publishReplacement})
	ctx := tools.WithTranscriptSessionID(context.Background(), parent)
	result := dt.Execute(ctx, map[string]any{"action": "status", "session_id": child.SessionID})
	if result == nil || result.IsError {
		t.Fatalf("S49b status failed: %+v", result)
	}
	if !strings.HasPrefix(result.ForLLM, "starting,") || strings.Contains(result.ForLLM, "Admission:") || strings.Contains(result.ForLLM, "recorded session state") {
		t.Errorf("S49b full status must lead starting with no capacity clause, got %q", result.ForLLM)
	}
	before := reads.Load()
	v := capRedAdmissionView(t, al, old, true)
	capRedAssert(t, "S49b resolver follows replacement with pending handoff", []any{v.state, v.present, v.err, v.position, v.limit, v.occupancy}, []any{"starting", true, nil, 0, 0, 0})
	capRedAssert(t, "resolver makes no memory read", reads.Load(), before)
	capRedAssert(t, "resolver takes no new slot", al.steerAdmission().activeCount(), 1)
	capRedAssert(t, "old identity never owns replacement", capRedAdmissionView(t, al, old, false).present, false)
}

func TestCapRED_AdmissionView_ResolverErrorAfterFirstLoad(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	parent := newTestSteeringSession(t, al, testHarnessWorkspaceMembershipID)
	child := capRedLaunchChild(t, al, parent, "read-failure")
	claim := executionClaim{SessionID: child.SessionID, Generation: child.Generation, RunID: "read-before-fault", BootSeq: al.bootEpochFor()}
	ls := al.GetSessionLifecycleStore()
	if err := ls.Mutate(child.SessionID, func(r *session.LifecycleRecord) error {
		r.State = session.LifecycleRunning
		r.ExecutionID = &session.ExecutionIdentity{RunID: claim.RunID, BootSeq: claim.BootSeq}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(ls.Dir(), child.SessionID+".jsonl")
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A directory at the record path produces a real read failure even as root.
	defer func() {
		if err := os.Remove(path); err != nil {
			t.Errorf("remove injected directory: %v", err)
		}
		if err := os.WriteFile(path, bytes, 0o600); err != nil {
			t.Errorf("restore lifecycle bytes: %v", err)
		}
	}()
	faulted := false
	fault := func() {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		faulted = true
	}
	dt := capRedDelegateTool(t, al)
	dt.SetLifecycleStore(&capRedLifecycleReadBoundary{LifecycleStore: ls, target: child.SessionID, after: fault})
	ctx := tools.WithTranscriptSessionID(context.Background(), parent)
	result := dt.Execute(ctx, map[string]any{"action": "status", "session_id": child.SessionID})
	if !faulted {
		t.Fatal("SETUP consumer never completed its first successful lifecycle load")
	}
	if result == nil || !result.IsError || !strings.Contains(result.ForLLM, "could not be read") || strings.Contains(result.ForLLM, "already running") {
		t.Errorf("S49e resolver failure must propagate, no guessed state: %+v", result)
	}
	v := capRedAdmissionView(t, al, claim, true)
	if v.err == nil || !strings.Contains(v.err.Error(), "could not be read") || v.present {
		t.Errorf("S49e real resolver must return unreadable-state error, got %+v", v)
	}
	capRedAssert(t, "S49e no dispatch, reservation or queue on read failure", []int{al.steerAdmission().activeCount(), al.steerAdmission().queueLen()}, []int{0, 0})
}

func TestCapRED_AdmissionView_GateOnlyFullClaimNoIO(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	claim := executionClaim{SessionID: "gate-only", Generation: 3, RunID: "real-reservation", BootSeq: al.bootEpochFor()}
	d := capRedSteer(t, al.steerAdmission(), claim.SessionID, claim.Generation, claim.RunID, claim.BootSeq)
	capRedAssert(t, "reservation actually held", d.granted, true)
	// No lifecycle record exists. Gate-only lookup must still see starting.
	v := capRedAdmissionView(t, al, claim, false)
	capRedAssert(t, "gate view does not require disk", []any{v.state, v.present, v.err}, []any{"starting", true, nil})
	for _, field := range []string{"session", "generation", "run", "boot"} {
		other := claim
		switch field {
		case "session":
			other.SessionID += "-other"
		case "generation":
			other.Generation++
		case "run":
			other.RunID += "-other"
		case "boot":
			other.BootSeq++
		}
		capRedAssert(t, "full-claim mismatch "+field, capRedAdmissionView(t, al, other, false).present, false)
	}
}
