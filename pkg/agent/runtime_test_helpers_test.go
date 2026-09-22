package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func newPersistentTestSession(t testing.TB) session.SessionStore {
	t.Helper()
	store, err := session.NewUnifiedStore(t.TempDir())
	if err != nil {
		t.Fatalf("new persistent test session store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

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

func (c *eventCollector) hasEventOfKind(kind EventKind) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, event := range c.events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}

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

func waitCtxCanceled(t *testing.T, driver *blockingExternalDriver, label string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if driver.ctxCanceled.Load() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Errorf("%s: external driver context was not cancelled", label)
}

func withBlockingDriver(t *testing.T) (*blockingExternalDriver, func()) {
	t.Helper()
	driver := newBlockingExternalDriver()
	previous := newExternalDriver
	newExternalDriver = func(string, runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		return driver, nil
	}
	return driver, func() { newExternalDriver = previous }
}

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
