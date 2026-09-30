// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// Only the paid provider edge is controlled. Cancellation is observed on the
// context supplied by the real turn; holding its return lets assertions inspect
// the real fence and live handle before completion disposes of the turn.
type executionIdentityT27ProviderCall struct {
	ctx       context.Context
	messages  []providers.Message
	finish    chan struct{}
	cancelled chan struct{}
}

type executionIdentityT27Provider struct {
	calls      chan *executionIdentityT27ProviderCall
	releaseAll chan struct{}
}

func (p *executionIdentityT27Provider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	call := &executionIdentityT27ProviderCall{
		ctx: ctx, messages: append([]providers.Message(nil), messages...),
		finish: make(chan struct{}), cancelled: make(chan struct{}),
	}
	select {
	case p.calls <- call:
	case <-p.releaseAll:
		return &providers.LLMResponse{Content: "provider released for teardown"}, nil
	}
	select {
	case <-call.finish:
		return &providers.LLMResponse{Content: "provider turn finished"}, nil
	case <-p.releaseAll:
		return &providers.LLMResponse{Content: "provider released for teardown"}, nil
	case <-ctx.Done():
		close(call.cancelled)
		select {
		case <-call.finish:
		case <-p.releaseAll:
		}
		return nil, ctx.Err()
	}
}

func (p *executionIdentityT27Provider) GetDefaultModel() string { return "t27-provider-boundary" }

type executionIdentityT27Harness struct {
	al         *AgentLoop
	provider   *executionIdentityT27Provider
	childID    string
	generation int
	busyCall   *executionIdentityT27ProviderCall
	childCall  *executionIdentityT27ProviderCall
}

func newExecutionIdentityT27Harness(t *testing.T, queued bool) *executionIdentityT27Harness {
	t.Helper()
	al, _ := newSteerAL(t)
	// One slot is the ordering fixture, not a concurrency-limit oracle.
	al.GetConfig().Performance.MaxParallelAgents = 1
	provider := &executionIdentityT27Provider{
		calls: make(chan *executionIdentityT27ProviderCall), releaseAll: make(chan struct{}),
	}
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("test fixture: production agent registry has no test agent")
	}
	inst.Provider = provider
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(provider.releaseAll) })
		waitExecutionIdentityT27Turns(t, al)
	})
	lifecycle := al.GetSessionLifecycleStore()
	al.SetSteerAudienceDeps(
		NewSteerAudienceResolver(NewSteerRecordClassifier(lifecycle, al.GetSessionStore())),
		nil, NewSteerUpwardDeliverer(),
	)
	al.SetSteerCanceller(NewSteerCanceller(lifecycle, al.SteerGenerationCancel).
		SetRevivalStateWriter(al.WriteSteerRevivalState))
	f := &executionIdentityT27Harness{al: al, provider: provider}
	if queued {
		// The busy worker is on a different tree; stopping the selected child
		// must not free this slot and accidentally admit a queued replacement.
		busyParent := newTestSteeringSession(t, al, "ws-1")
		busyID, busyGen := launchSteeredChild(t, al, busyParent, "t27-busy", "hold the only slot")
		dispatchExecutionIdentityT27(t, al, busyID, busyGen, steer.DispatchRunning)
		f.busyCall = nextExecutionIdentityT27ProviderCall(t, provider)
	}
	parent := newTestSteeringSession(t, al, "ws-1")
	f.childID, f.generation = launchSteeredChild(t, al, parent, "t27-selected", "selected old instruction")
	want := steer.DispatchRunning
	if queued {
		want = steer.DispatchQueued
	}
	result := dispatchExecutionIdentityT27(t, al, f.childID, f.generation, want)
	if queued && result.QueuePosition != 1 {
		t.Fatalf("selected child's queue position = %d, want 1 behind the sole real provider turn", result.QueuePosition)
	}
	if !queued {
		f.childCall = nextExecutionIdentityT27ProviderCall(t, provider)
	}
	return f
}

func dispatchExecutionIdentityT27(t *testing.T, al *AgentLoop, id string, generation int, want steer.DispatchState) steer.DispatchResult {
	t.Helper()
	result, err := NewSteerLauncher(al).Dispatch(context.Background(), id, generation)
	if err != nil {
		t.Fatalf("production Dispatch(%s, %d): %v", id, generation, err)
	}
	if result.State != want || result.Generation != generation {
		t.Fatalf("production Dispatch = %+v, want state=%s generation=%d", result, want, generation)
	}
	return result
}

func nextExecutionIdentityT27ProviderCall(t *testing.T, p *executionIdentityT27Provider) *executionIdentityT27ProviderCall {
	t.Helper()
	select {
	case call := <-p.calls:
		return call
	case <-time.After(30 * time.Second): // Deadlock safeguard, not a timing oracle.
		t.Fatal("production turn never reached the controlled provider dependency")
		return nil
	}
}

func waitExecutionIdentityT27Signal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(30 * time.Second): // Deadlock safeguard, not a timing oracle.
		t.Fatalf("production operation never signalled %s", description)
	}
}

func waitExecutionIdentityT27Turns(t *testing.T, al *AgentLoop) {
	t.Helper()
	joined := make(chan struct{})
	go func() {
		al.steerAdmission().turns.Wait()
		close(joined)
	}()
	waitExecutionIdentityT27Signal(t, joined, "all steered dispatch/disposal goroutines joined")
}

func cancelExecutionIdentityT27Child(t *testing.T, f *executionIdentityT27Harness, actor string) {
	t.Helper()
	report, err := f.al.steerCanceller().CancelSubtree(context.Background(), f.childID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: actor})
	if err != nil {
		t.Fatalf("production Stop cascade: %v", err)
	}
	if !reflect.DeepEqual(report.Reached, []string{f.childID}) || len(report.Unreachable) != 0 ||
		len(report.SkippedNewerGeneration) != 0 || len(report.SkippedTerminal) != 0 {
		t.Fatalf("Stop cascade = %+v, want only selected child reached, no partial/stale/terminal result", report)
	}
}

func loadExecutionIdentityT27Record(t *testing.T, f *executionIdentityT27Harness) *session.LifecycleRecord {
	t.Helper()
	rec, err := f.al.GetSessionLifecycleStore().Load(f.childID)
	if err != nil {
		t.Fatalf("real LifecycleStore.Load(selected child): %v", err)
	}
	return rec
}

// Read the real journal, not a constructed record or a parallel fake store.
func executionIdentityT27JournalTail(t *testing.T, f *executionIdentityT27Harness) map[string]json.RawMessage {
	t.Helper()
	path := filepath.Join(f.al.GetSessionLifecycleStore().Dir(), f.childID+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read production lifecycle journal: %v", err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(lines[len(lines)-1], &snapshot); err != nil {
		t.Fatalf("decode persisted lifecycle snapshot: %v", err)
	}
	var sessionID string
	if err := json.Unmarshal(snapshot["session_id"], &sessionID); err != nil || sessionID != f.childID {
		t.Fatalf("journal reader control: session_id=%q, error=%v, want selected child %q", sessionID, err, f.childID)
	}
	return snapshot
}

// not-wire-format: assertions inspect the ADR D4 internal disk shape only.
func requireExecutionIdentityT27Identity(t *testing.T, snapshot map[string]json.RawMessage, sessionID string, generation int) map[string]json.RawMessage {
	t.Helper()
	raw, exists := snapshot["execution_id"]
	if !exists {
		t.Fatal("BLOCKED: durable execution_id before admission not implemented — required by ADR sub-agent control plane D2/D4/T27")
	}
	var identity map[string]json.RawMessage
	if err := json.Unmarshal(raw, &identity); err != nil {
		t.Fatalf("persisted execution_id is invalid JSON: %v", err)
	}
	keys := make([]string, 0, len(identity))
	for key := range identity {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	wantKeys := []string{"boot_seq", "generation", "run_id", "session_id"} // ADR D4 exact tuple.
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Fatalf("execution_id keys=%v, want %v (D2/D4/T27)", keys, wantKeys)
	}
	var gotSession, runID string
	var gotGeneration int
	if err := json.Unmarshal(identity["session_id"], &gotSession); err != nil || gotSession != sessionID {
		t.Fatalf("execution_id.session_id=%q, error=%v, want %q", gotSession, err, sessionID)
	}
	if err := json.Unmarshal(identity["generation"], &gotGeneration); err != nil || gotGeneration != generation {
		t.Fatalf("execution_id.generation=%d, error=%v, want %d", gotGeneration, err, generation)
	}
	if err := json.Unmarshal(identity["run_id"], &runID); err != nil || runID == "" {
		t.Fatalf("execution_id.run_id=%q, error=%v, want non-empty identity for this admission", runID, err)
	}
	if bytes.Equal(bytes.TrimSpace(identity["boot_seq"]), []byte("null")) {
		t.Fatal("execution_id.boot_seq is null; D2 requires the current boot identity")
	}
	return identity
}
