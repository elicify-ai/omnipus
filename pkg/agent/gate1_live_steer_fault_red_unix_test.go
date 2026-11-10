//go:build linux || darwin

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// F4: amended ADR-20260928 D-D/D4. Observe a REAL successful builtin tool
// result after its transcript/checkpoint have landed, then make only the
// recipient transcript readable-only. A normal event observer performs that
// filesystem permission change without waiting or replacing an effect. No
// production *TestHook global is set, and no tool or injection code is faked.
func TestGate1LiveSteer_ToolBoundaryReportsDurableInjectionFailure(t *testing.T) {
	f := newQAReceiptFixture(t)
	readPath := filepath.Join(f.al.GetConfig().Agents.Defaults.Home, "gate1-read-control.txt")
	if err := os.WriteFile(readPath, []byte(gate1ReadContents), 0o600); err != nil {
		t.Fatalf("SETUP real read_file source: %v", err)
	}
	p := newGate1LiveSteerProvider(readPath)
	instance, ok := f.al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP registered real child agent missing")
	}
	instance.Provider = p
	// newAL's minimal fixture omits policy coverage. Use the shipped ceiling
	// through the real policy builder, before admission; no hand-made allow,
	// God Mode, policy checker replacement or mid-turn policy change.
	instance.StoreToolPolicy(agentToolsCfgToPolicy(config.DefaultConfig(), nil))
	t.Cleanup(func() { p.open(0); p.open(1) })
	dispatched, err := NewSteerLauncher(f.al).Dispatch(context.Background(), f.child.SessionID, f.child.Generation)
	if err != nil || dispatched.State != steer.DispatchRunning {
		t.Fatalf("SETUP actual child admission=%+v err=%v, want running", dispatched, err)
	}
	gate1AwaitLiveProvider(t, p, 0)
	f.child = rootReopenedRecord(t, f.al, f.child.SessionID)
	active := f.al.getActiveTurnState(f.child.SessionID)
	if active == nil || f.child.ExecutionID == nil || f.al.tsExecutionClaim(active, f.child.SessionID) != f.al.executionClaimFor(f.child) {
		t.Fatal("SETUP real running child has no matching immutable admission")
	}
	const text = "This accepted live steer must stay queued if durable injection is refused."
	ctx := tools.WithTranscriptSessionID(context.Background(), f.parentID)
	acceptedResult := delegateToolFor(t, f.al).Execute(ctx, map[string]any{
		"action": "steer", "session_id": f.child.SessionID, "text": text, "correlation_id": "gate1-live-refusal",
	})
	if acceptedResult == nil || acceptedResult.IsError {
		t.Fatalf("SETUP actual parent delegate steer refused: %+v", acceptedResult)
	}
	accepted := qaReceiptAcceptedSteer(t, f.al, f.child.SessionID, text)
	path := filepath.Join(f.store.BaseDir(), f.child.SessionID, "transcript.jsonl")
	// Calibrate the exact permission seam while the external provider is held;
	// restore it so the real tool call/result can first persist successfully.
	restore := qaReceiptDenyAppend(t, path)
	restore()
	faulted := make(chan struct {
		payload ToolExecEndPayload
		err     error
	}, 1)
	f.al.SetEventSyncTap(func(event Event) {
		if event.Kind != EventKindToolExecEnd {
			return
		}
		payload, ok := event.Payload.(ToolExecEndPayload)
		if !ok || string(payload.ToolCallID) != gate1ReadCallID || payload.SessionID != f.child.SessionID {
			return
		}
		// This is the only injected fault: an actual OS permission change at
		// the transcript append seam, after the real tool result's persistence.
		faulted <- struct {
			payload ToolExecEndPayload
			err     error
		}{payload, os.Chmod(path, 0o400)}
	})
	t.Cleanup(func() { f.al.SetEventSyncTap(nil) })
	p.open(0)
	select {
	case observation := <-faulted:
		if observation.err != nil || observation.payload.IsError || observation.payload.Tool != "read_file" || !strings.Contains(observation.payload.Result, gate1ReadContents) {
			t.Fatalf("SETUP fault must follow a genuine successful persisted read_file: payload=%+v permission err=%v", observation.payload, observation.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP real read_file did not successfully persist/emit its result before the injection fault")
	}
	// A fixed implementation may report and pause/end instead of calling the
	// model again. Accept either boundary exit, not a particular repair design.
	runDone := make(chan struct{})
	go func() { f.al.steerAdmission().turns.Wait(); close(runDone) }()
	select {
	case index := <-p.entered:
		if index != 1 {
			t.Fatalf("SETUP next real provider boundary=%d, want 1", index)
		}
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP neither live provider nor execution disposal reached a boundary after the tool")
	}
	gate1RequireQueuedItems(t, f.al, f.child.SessionID, accepted)
	for _, request := range p.requestsSnapshot() {
		for _, message := range request {
			if message.Role == "user" && message.Content == text {
				t.Error("F4: refused durable injection nevertheless reached a model request as delivered user input")
			}
		}
	}
	entries := qaReceiptReopenTranscript(t, f.al.GetSessionStore(), f.parentID)
	visibleErrors := 0
	for _, entry := range entries {
		frame := entry.SubagentMessage
		if frame == nil || frame.ChildSessionId == nil || *frame.ChildSessionId != f.child.SessionID || frame.Kind != "error" || frame.Text == nil {
			continue
		}
		message := strings.ToLower(*frame.Text)
		if strings.Contains(message, "steer") || strings.Contains(message, "instruction") || strings.Contains(message, "follow-up") {
			visibleErrors++
		}
	}
	if visibleErrors == 0 {
		t.Errorf("F4: successful real tool boundary refused accepted steer %q but produced 0 visible parent steering-error entries; want a visible refusal while the original item/receipt stay queued", accepted.ControlID)
	}
	f.al.SetEventSyncTap(nil)
	restore()
	p.open(1)
	joinGoalFixtureRuns(t, f.al)
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not join the actual execution observer")
	}
}
