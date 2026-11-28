package agent

// Round 13, CHECK finding F3 (/workspace/logs/1960/check-s1-delta3/check-report.md):
// the background-completion admission in pkg/agent/loop_run_turn_tools.go
// (asyncToolCallbackGate.callback, wired with `loop:` where the gate is built
// in the tool-dispatch stage) had no test that dies when it is removed.
// Oracle: the gate's own contract (its field doc) and the shutdown rule it
// serves: a completion that arrives after the turn is an ACTIVE REQUEST, so
// Close drains its transcript and bus writes before the stores are torn down,
// and a completion that arrives after intake closed is refused and performs no
// writes. Wrong implementation killed: the gate built without its loop (the
// CHECK mutant m7: `loop:` left nil), where the callback is never counted and
// Close does not wait for it.
//
// Synchronisation (no sleeps as oracle): a real tool completes after the turn
// through the real gate; a BoundaryObserver installed on the real loop blocks
// the admitted callback INSIDE its handler (the handler's first act is the
// async-tool-feedback audience observation), and the test then closes the loop,
// proves Close is waiting (intake closed, the callback counted as active,
// Close not returned), releases the callback, and proves Close returns.
import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// gateProbeTool starts "asynchronously" and hands the real gate callback to the
// test, which fires it when it chooses: a genuinely later completion.
type gateProbeTool struct {
	mu sync.Mutex
	cb tools.AsyncCallback
}

func (*gateProbeTool) Name() string        { return "gate_async_probe" }
func (*gateProbeTool) Description() string { return "F3 fixture: completes after the turn" }
func (*gateProbeTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (*gateProbeTool) Scope() tools.ToolScope       { return tools.ScopeGeneral }
func (*gateProbeTool) Category() tools.ToolCategory { return tools.CategoryCore }
func (*gateProbeTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	return tools.ErrorResult("F3 fixture requires async dispatch")
}
func (p *gateProbeTool) ExecuteAsync(_ context.Context, _ map[string]any, cb tools.AsyncCallback) *tools.ToolResult {
	p.mu.Lock()
	p.cb = cb
	p.mu.Unlock()
	return tools.AsyncResult("probe started")
}
func (p *gateProbeTool) callback() tools.AsyncCallback {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cb
}

// gateBlockingObserver blocks every async-tool-feedback observation until
// release is closed, and counts them. The handler of an admitted callback makes
// this observation before any write, so a callback parked here is "inside its
// handler".
type gateBlockingObserver struct {
	entered chan struct{} // buffered; one token per observation
	release chan struct{}
	count   atomic.Int32
}

func (o *gateBlockingObserver) Observe(boundary steer.Boundary, _ string, _ steer.Audience) {
	if boundary != steer.BoundaryAsyncToolFeedback {
		return
	}
	o.count.Add(1)
	o.entered <- struct{}{}
	<-o.release
}

func (al *AgentLoop) activeRequestCountForTest() int {
	al.activeRequests.mu.Lock()
	defer al.activeRequests.mu.Unlock()
	return al.activeRequests.active
}

func TestAsyncCallbackGate_CloseWaitsForAnAdmittedCompletionAndRefusesALateOne(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCall("gate_async_probe", `{}`).
		WithText("Started the probe.")
	al, msgBus, _ := newBashAsyncTestLoop(t, provider)
	probe := &gateProbeTool{}
	al.RegisterTool(probe)
	for _, id := range al.GetRegistry().ListAgentIDs() {
		if ag, ok := al.GetRegistry().GetAgent(id); ok {
			ag.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"bash": "allow", "gate_async_probe": "allow"}})
		}
	}
	observer := &gateBlockingObserver{entered: make(chan struct{}, 16), release: make(chan struct{})}
	al.SetSteerAudienceDeps(nil, observer, nil)

	resp, err := al.processDirectWithChannelPrimaryForTest(context.Background(), "start the probe", "f3-gate-session", "telegram", "4242")
	require.NoError(t, err)
	require.Contains(t, resp, "Started", "instrument: the turn ended after the async start")
	cb := probe.callback()
	require.NotNil(t, cb, "instrument: the real gate handed the tool its callback")
	require.Zero(t, al.activeRequestCountForTest(), "instrument: nothing is active once the turn has finished")
	require.Zero(t, observer.count.Load(), "instrument: no completion handled yet")

	// 1. An admitted completion, blocked inside its handler.
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		cb(context.Background(), &tools.ToolResult{ForLLM: "F3 completion"})
	}()
	select {
	case <-observer.entered:
	case <-firstDone:
		t.Fatal("the completion returned without entering its handler")
	case <-time.After(30 * time.Second):
		t.Fatal("the completion never reached its handler")
	}
	assert.Equal(t, 1, al.activeRequestCountForTest(),
		"a completion inside its handler is an active request, so shutdown must wait for it (gate admission)")

	// 2. Close starts and must wait for it.
	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		al.Close()
	}()
	require.Eventually(t, func() bool {
		if al.beginActiveRequest() {
			al.endActiveRequest()
			return false
		}
		return true
	}, 30*time.Second, 2*time.Millisecond, "Close must close request intake")
	select {
	case <-closeDone:
		t.Fatal("Close returned while an admitted completion was still inside its handler")
	default:
	}
	assert.Equal(t, 1, al.activeRequestCountForTest(), "Close waits on the admitted completion, it does not discard it")

	// 3. A completion arriving after intake closed is refused: it returns
	// without entering its handler (no observation, no write).
	lateDone := make(chan struct{})
	go func() {
		defer close(lateDone)
		cb(context.Background(), &tools.ToolResult{ForLLM: "F3 late completion"})
	}()
	select {
	case <-lateDone:
	case <-observer.entered:
		t.Fatal("a completion arriving after intake closed entered its handler")
	case <-time.After(30 * time.Second):
		t.Fatal("the late completion neither returned nor entered its handler")
	}
	assert.Equal(t, int32(1), observer.count.Load(), "only the admitted completion was handled")
	assert.Equal(t, 1, al.activeRequestCountForTest(), "the refused completion was not admitted")
	select {
	case m := <-msgBus.InboundChan():
		t.Fatalf("a completion published while the only admitted one was parked: %+v", m)
	default:
	}

	// 4. Release the admitted completion: Close finishes only after it.
	close(observer.release)
	select {
	case <-firstDone:
	case <-time.After(30 * time.Second):
		t.Fatal("the admitted completion never finished after release")
	}
	select {
	case <-closeDone:
	case <-time.After(60 * time.Second):
		t.Fatal("Close did not return after the admitted completion finished")
	}
	assert.Zero(t, al.activeRequestCountForTest(), "everything drained")
}
