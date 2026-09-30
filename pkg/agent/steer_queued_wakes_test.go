package agent

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// queuedWakeCaptureProvider replaces only the model boundary. Its first call
// holds the admission slot; all calls record the real prompts reaching the model.
type queuedWakeCaptureProvider struct {
	mu       sync.Mutex
	requests [][]providers.Message
	entered  chan struct{}
	release  chan struct{}
}

func (p *queuedWakeCaptureProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	firstCall := len(p.requests) == 1
	p.mu.Unlock()
	if firstCall {
		close(p.entered)
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &providers.LLMResponse{Content: "finished"}, nil
}

func (p *queuedWakeCaptureProvider) GetDefaultModel() string { return "queued-wake-capture" }

// Coverage-restoration brief, Finding 1: two distinct system wakes queued at
// the admission cap both reach the promoted turn exactly once, in arrival
// order. This is a generic steered-session property, independent of goals.
func TestSteeredSession_TwoQueuedSystemWakesReachPromotedTurnExactlyOnceInOrder(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	al.GetConfig().Performance.MaxParallelAgents = 1 // Smallest positive cap forces queueing with one busy turn.
	wireSteerCompletionDeps(t, al)
	provider := &queuedWakeCaptureProvider{entered: make(chan struct{}), release: make(chan struct{})}
	agentInst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("fixture: test agent is not registered")
	}
	agentInst.Provider = provider
	var releaseOnce sync.Once
	releaseBusy := func() { releaseOnce.Do(func() { close(provider.release) }) }
	t.Cleanup(releaseBusy)

	steerer := newTestSteeringSession(t, al, "ws-1")
	busyID, busyGen := launchSteeredChild(t, al, steerer, "queued-wakes-busy", "occupy the only admission slot")
	wokenID, wokenGen := launchSteeredChild(t, al, steerer, "queued-wakes-recipient", "original launch instruction")
	busy, err := NewSteerLauncher(al).Dispatch(context.Background(), busyID, busyGen)
	if err != nil || busy.State != steer.DispatchRunning {
		t.Fatalf("Dispatch(busy) = %+v, err=%v; want running", busy, err)
	}
	select {
	case <-provider.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("fixture: busy turn never reached its provider")
	}

	const first = "First wake: inspect the delegated report"
	const second = "Second wake: include the new evidence"
	wakes := []string{first, second}
	for i, content := range wakes {
		// System wakes need not have inbox entries. Distinct message IDs ensure
		// this tests two arrivals, not replay of a single idempotent wake.
		msg := bus.InboundMessage{
			Channel: "system", AsyncTranscriptSessionID: wokenID, Content: content,
			Metadata: map[string]string{
				"steer_message_id": "queued-system-wake-" + strconv.Itoa(i),
				"steer_generation": strconv.Itoa(wokenGen),
			},
		}
		if _, wakeErr := al.processSteeredSystemWake(context.Background(), msg); wakeErr != nil {
			t.Fatalf("queue wake %d: %v", i+1, wakeErr)
		}
		queued, loadErr := al.GetSessionLifecycleStore().Load(wokenID)
		if loadErr != nil {
			t.Fatalf("Load(queued recipient): %v", loadErr)
		}
		if queued.State != session.LifecycleQueued || !slices.Equal(queued.PendingUserMessages, wakes[:i+1]) {
			t.Fatalf("after wake %d: state=%q pending=%q; want queued and %q", i+1, queued.State, queued.PendingUserMessages, wakes[:i+1])
		}
		if ts := al.getActiveTurnState(wokenID); ts != nil {
			t.Fatal("recipient registered a turn while the admission cap was occupied")
		}
	}
	gate := al.steerAdmission()
	if got := gate.activeCount(); got != 1 {
		t.Fatalf("admission slots before promotion = %d, want exactly the busy turn's one slot", got)
	}

	releaseBusy()
	joined := make(chan struct{})
	go func() {
		gate.turns.Wait() // Join completion and queue promotion, not just the provider call.
		close(joined)
	}()
	select {
	case <-joined:
	case <-time.After(30 * time.Second):
		t.Fatal("busy and promoted turns did not finish after releasing the admission slot")
	}

	provider.mu.Lock()
	requests := append([][]providers.Message(nil), provider.requests...)
	provider.mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("model requests = %d, want 2: one busy turn and one promoted turn", len(requests))
	}
	promptParts := make([]string, 0, len(requests[1]))
	for _, message := range requests[1] {
		promptParts = append(promptParts, message.Content)
	}
	prompt := strings.Join(promptParts, "\n")
	firstAt, secondAt := strings.Index(prompt, first), strings.Index(prompt, second)
	if firstAt < 0 || secondAt <= firstAt || strings.Count(prompt, first) != 1 || strings.Count(prompt, second) != 1 {
		t.Fatalf("promoted model prompt = %q; want both wakes exactly once, first before second", prompt)
	}
	consumed, err := al.GetSessionLifecycleStore().Load(wokenID)
	if err != nil {
		t.Fatalf("Load(promoted recipient): %v", err)
	}
	if len(consumed.PendingUserMessages) != 0 {
		t.Fatalf("pending wakes after promotion = %q, want none so a later turn cannot replay them", consumed.PendingUserMessages)
	}
	if active, queued := gate.activeCount(), gate.queueLen(); active != 0 || queued != 0 {
		t.Fatalf("admission after completed turns: active=%d queued=%d, want both 0", active, queued)
	}
}
