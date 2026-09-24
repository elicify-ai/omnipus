package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// newPersistentTestSession was deleted 2026-09-24 as an unreachable
// ADR-091 leftover (golangci unused): grep found no caller anywhere in
// the repo.

type eventCollector struct {
	mu     sync.Mutex
	events []Event
}

func newEventCollector(t *testing.T, al *AgentLoop) (*eventCollector, func()) {
	t.Helper()
	c := &eventCollector{}
	sub := al.SubscribeEvents(16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for evt := range sub.C {
			c.mu.Lock()
			c.events = append(c.events, evt)
			c.mu.Unlock()
		}
	}()
	return c, func() {
		al.UnsubscribeEvents(sub.ID)
		<-done
	}
}

// hasEventOfKind was deleted 2026-09-24 as an unreachable ADR-091
// leftover (golangci unused): grep found no caller anywhere in the repo.

type blockingExternalDriver struct {
	startOnce   sync.Once
	started     chan struct{}
	ctxCanceled atomic.Bool
}

func newBlockingExternalDriver() *blockingExternalDriver {
	return &blockingExternalDriver{started: make(chan struct{})}
}

func (d *blockingExternalDriver) Run(ctx context.Context, _ runner.RunOptions) (<-chan runner.RunEvent, error) {
	d.startOnce.Do(func() { close(d.started) })
	events := make(chan runner.RunEvent)
	go func() {
		<-ctx.Done()
		d.ctxCanceled.Store(true)
		close(events)
	}()
	return events, nil
}

func (d *blockingExternalDriver) Decide(runner.PermissionDecision) {}
func (d *blockingExternalDriver) Cancel()                          {}
func (d *blockingExternalDriver) Input(string) error               { return nil }
func (d *blockingExternalDriver) Resume(ctx context.Context, _ string) (<-chan runner.RunEvent, error) {
	return d.Run(ctx, runner.RunOptions{})
}
func (d *blockingExternalDriver) Test(context.Context) runner.ConnectionTestResult {
	return runner.ConnectionTestResult{OK: true}
}

// waitCtxCanceled and withBlockingDriver were deleted 2026-09-24 as
// unreachable ADR-091 leftovers (golangci unused): grep found no caller
// of either anywhere in the repo. blockingExternalDriver itself and its
// other methods stay — cancel_stress_test.go's driverRegistry still
// constructs it directly via newBlockingExternalDriver, and the interface
// assertion below keeps Run/Decide/Cancel/Input/Resume/Test reachable.

var _ runner.ExternalAgentRunner = (*blockingExternalDriver)(nil)

type simpleMockProviderAPI struct {
	response string
}

func (m *simpleMockProviderAPI) Chat(
	context.Context,
	[]providers.Message,
	[]providers.ToolDefinition,
	string,
	map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: m.response}, nil
}

func (*simpleMockProviderAPI) GetDefaultModel() string { return "gpt-4o-mini" }
