package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// These observers delegate to real registry/tool implementations. Registration
// history matters: an empty final registry would miss a fake ID registered and
// then unregistered before JudgeCriteria returned (P3, FR-037).
type p3Registration struct{ unitID, sessionID string }

type p3RegistryObserver struct {
	VerifierSessionRegistry
	mu           sync.Mutex
	registered   []p3Registration
	unregistered []string
}

func p3ObserveRegistry(t *testing.T) *p3RegistryObserver {
	t.Helper()
	previous := currentVerifierSessionRegistry()
	r := &p3RegistryObserver{VerifierSessionRegistry: NewVerifierSessionRegistry()}
	SetVerifierSessionRegistry(r)
	t.Cleanup(func() { SetVerifierSessionRegistry(previous) })
	return r
}

func (r *p3RegistryObserver) Register(unitID, sessionID string) error {
	r.mu.Lock()
	r.registered = append(r.registered, p3Registration{unitID, sessionID})
	r.mu.Unlock()
	return r.VerifierSessionRegistry.Register(unitID, sessionID)
}

func (r *p3RegistryObserver) Unregister(unitID string) {
	r.mu.Lock()
	r.unregistered = append(r.unregistered, unitID)
	r.mu.Unlock()
	r.VerifierSessionRegistry.Unregister(unitID)
}

func (r *p3RegistryObserver) history() ([]p3Registration, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]p3Registration(nil), r.registered...), append([]string(nil), r.unregistered...)
}

// Embed the real concrete tool so its policy/audit injection interfaces remain
// available; Execute forwards the original context/arguments without changes.
type p3ReadFileObserver struct {
	*tools.ReadFileTool
	calls  atomic.Int32
	mu     sync.Mutex
	text   string
	failed bool
}

func (o *p3ReadFileObserver) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	o.calls.Add(1)
	result := o.ReadFileTool.Execute(ctx, args)
	o.mu.Lock()
	if result != nil {
		o.text, o.failed = result.ForLLM, result.IsError
	} else {
		o.text, o.failed = "", true
	}
	o.mu.Unlock()
	return result
}

func (o *p3ReadFileObserver) outcome() (string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.text, o.failed
}

// The only clock double stops at the first real D7 wait. No real sleep, provider
// timeout or short outer deadline can hide whether provisioning used backoff.
type p3BackoffObserver struct {
	mu    sync.Mutex
	waits []time.Duration
}

func p3StopAfterFirstBackoff(t *testing.T) *p3BackoffObserver {
	t.Helper()
	previous := judgeSleepFn
	t.Cleanup(func() { judgeSleepFn = previous })
	o := &p3BackoffObserver{}
	judgeSleepFn = func(_ context.Context, duration time.Duration) error {
		o.mu.Lock()
		o.waits = append(o.waits, duration)
		o.mu.Unlock()
		return errors.New("p3 test clock: end the first retry wait")
	}
	return o
}

func (o *p3BackoffObserver) durations() []time.Duration {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]time.Duration(nil), o.waits...)
}

// Temporary RED compilation scaffolding, not a compatibility adapter. Old
// production must still compile so the independent live fault tests can run.
// GREEN may replace callers with direct two-result calls once P3 is implemented.
func p3SessionCreator(t *testing.T, al *AgentLoop) func(string, string) (string, error) {
	t.Helper()
	creator, ok := any(al.newVerifierSessionChatID).(func(string, string) (string, error))
	if !ok {
		t.Fatal("BLOCKED: newVerifierSessionChatID(string, string) (string, error) not implemented — required by provisioning ruling section 3 P3")
	}
	return creator
}
