package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestCapRED_DelegateStatus_SessionBusyNumberFree(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	al.GetConfig().Performance.MaxParallelAgents = 8
	parent := newTestSteeringSession(t, al, testHarnessWorkspaceMembershipID)
	child := capRedLaunchChild(t, al, parent, "serialization")
	gate := al.steerAdmission()
	first := capRedSteer(t, gate, child.SessionID, child.Generation, "old-body", al.bootEpochFor())
	if !first.granted {
		t.Fatal("SETUP first turn did not acquire")
	}
	if err := al.GetSessionLifecycleStore().Mutate(child.SessionID, func(r *session.LifecycleRecord) error {
		r.State = session.LifecycleRunning
		r.ExecutionID = &session.ExecutionIdentity{RunID: "next-body", BootSeq: al.bootEpochFor()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	second := capRedSteer(t, gate, child.SessionID, child.Generation, "next-body", al.bootEpochFor())
	capRedAssert(t, "same session is serialization", []any{second.granted, second.position, second.cause}, []any{false, 1, "session_busy"})
	text := capRedStatus(t, al, parent, child.SessionID)
	want := "Admission: this child has a turn waiting to start. This session already has another turn running, so the new turn waits its turn. Queue position 1. list_jobs shows current state."
	index := strings.Index(text, "Admission:")
	if index < 0 {
		t.Errorf("S50 missing M24s, got %q; want %q", text, want)
	} else {
		capRedAssert(t, "complete number-free M24s", strings.TrimSpace(text[index:]), want)
	}
	capRedAssert(t, "S35 session_busy has no notice", len(capRedInbox(t, al, parent, child.SessionID)), 0)
	capRedAssert(t, "serialization took no second reservation", gate.activeCount(), 1)
}

func TestCapRED_RunTask_FreeAndExecutingRepeat(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, inst, _ := newPlanToolWiringTestLoop(t)
	p := &parkedProvider{entered: make(chan string, 8), release: make(chan struct{})}
	inst.Provider = p
	t.Cleanup(func() { close(p.release) })
	tk := &task.Task{ID: "live-repeat", Title: "live", AgentID: "planner-agent", WorkspaceID: testHarnessWorkspaceMembershipID, Status: task.StatusNext, RunIsolated: true}
	if err := GetTaskStore(al).Create(tk); err != nil {
		t.Fatal(err)
	}
	ctx := tools.WithAgentID(tools.WithWorkspaceID(context.Background(), testHarnessWorkspaceMembershipID), "planner-agent")
	first := capRedToolJSON(t, inst.Tools.Execute(ctx, "run_task", map[string]any{"task_id": tk.ID}))
	sid, _ := first["session_id"].(string)
	if sid == "" {
		t.Fatal("fresh launch must return its actual session")
	}
	capRedAssert(t, "S12c complete unqueued shape", first, map[string]any{"task_id": tk.ID, "status": "in_progress", "session_id": sid})
	select {
	case <-p.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("real task worker never entered model edge")
	}
	before := al.steerAdmission().activeCount()
	second := capRedToolJSON(t, inst.Tools.Execute(ctx, "run_task", map[string]any{"task_id": tk.ID}))
	capRedAssert(t, "S12e complete running repeat", second, map[string]any{"task_id": tk.ID, "status": "in_progress", "session_id": sid, "note": "already running — no action taken"})
	capRedAssert(t, "repeat does not take another slot", al.steerAdmission().activeCount(), before)
	select {
	case <-p.entered:
		t.Error("repeat entered a second worker body")
	default:
	}
}

func TestCapRED_RunTask_UnreadableLifecycleIsAnError(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, inst, _ := newPlanToolWiringTestLoop(t)
	al.GetConfig().Performance.MaxParallelAgents = 1
	capRedFillSteer(t, al.steerAdmission(), 1)
	tk := &task.Task{ID: "read-error", Title: "unreadable", AgentID: "planner-agent", WorkspaceID: testHarnessWorkspaceMembershipID, Status: task.StatusNext, RunIsolated: true}
	if err := GetTaskStore(al).Create(tk); err != nil {
		t.Fatal(err)
	}
	ctx := tools.WithAgentID(context.Background(), "planner-agent")
	first := capRedToolJSON(t, inst.Tools.Execute(ctx, "run_task", map[string]any{"task_id": tk.ID}))
	sid := first["session_id"].(string)
	path := filepath.Join(al.GetSessionLifecycleStore().Dir(), sid+".jsonl")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{broken-json}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.WriteFile(path, original, 0o600); err != nil {
			t.Errorf("restore injected lifecycle damage: %v", err)
		}
	}()
	before := al.steerAdmission().queueLen()
	result := inst.Tools.Execute(ctx, "run_task", map[string]any{"task_id": tk.ID})
	if result == nil {
		t.Fatal("missing unreadable-state error")
	}
	if !result.IsError || !strings.Contains(result.ForLLM, "could not be read") {
		t.Errorf("S12f must report unreadable state, got error=%t, text=%q", result.IsError, result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "already running") {
		t.Errorf("unreadable state guessed running: %s", result.ForLLM)
	}
	capRedAssert(t, "read error does not dispatch/requeue", al.steerAdmission().queueLen(), before)
}

type capRedBodyProvider struct {
	entered chan string
	release chan struct{}
}

func (p *capRedBodyProvider) GetDefaultModel() string { return "cap-body-edge" }
func (p *capRedBodyProvider) Chat(ctx context.Context, m []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"CAP_BODY_older_a", "CAP_BODY_older_b", "CAP_BODY_new"} {
		if strings.Contains(string(raw), name) {
			p.entered <- name
			break
		}
	}
	select {
	case <-p.release:
		return &providers.LLMResponse{Content: "finished"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestCapRED_SteerQueue_NewcomerDispatchesOlderRealBodies(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	p := &capRedBodyProvider{entered: make(chan string, 8), release: make(chan struct{})}
	inst, _ := al.GetRegistry().GetAgent(testDefaultAgentID)
	inst.Provider = p
	t.Cleanup(func() { close(p.release) })
	al.GetConfig().Performance.MaxParallelAgents = 2
	capRedFillSteer(t, al.steerAdmission(), 2)
	parent := newTestSteeringSession(t, al, testHarnessWorkspaceMembershipID)
	launcher := NewSteerLauncher(al)
	launch := func(label string) steer.LaunchResult {
		r, err := launcher.Launch(context.Background(), steer.LaunchRequest{SteeringSessionID: parent, TargetAgentID: testDefaultAgentID, Task: label, Label: label, Origin: steer.Origin{Kind: steer.OriginKindDelegate}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	older := []steer.LaunchResult{launch("CAP_BODY_older_a"), launch("CAP_BODY_older_b")}
	for _, child := range older {
		d, err := launcher.Dispatch(context.Background(), child.SessionID, child.Generation)
		if err != nil {
			t.Fatal(err)
		}
		capRedAssert(t, "older entry actually queued", d.State, steer.DispatchQueued)
	}
	al.GetConfig().Performance.MaxParallelAgents = 8
	fresh := launch("CAP_BODY_new")
	if _, err := launcher.Dispatch(context.Background(), fresh.SessionID, fresh.Generation); err != nil {
		t.Fatal(err)
	}
	capRedAssert(t, "new decision drained both older queue entries", al.steerAdmission().queueLen(), 0)
	seen := map[string]int{}
	for i := 0; i < 3; i++ {
		select {
		case label := <-p.entered:
			seen[label]++
		case <-time.After(3 * time.Second):
			t.Fatalf("S32 older queued bodies were not dispatched by newcomer's grants: got %#v", seen)
		}
	}
	capRedAssert(t, "every granted body actually entered exactly once", seen, map[string]int{"CAP_BODY_older_a": 1, "CAP_BODY_older_b": 1, "CAP_BODY_new": 1})
}
