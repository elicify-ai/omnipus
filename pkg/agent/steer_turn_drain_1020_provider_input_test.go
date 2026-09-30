package agent

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// steerRevivalInputProvider1020 records requests at the external provider
// boundary without changing mockProvider's response. Completion, revival,
// prompt construction and the registered delegate sink remain real.
type steerRevivalInputProvider1020 struct {
	mockProvider
	mu       sync.Mutex
	requests [][]providers.Message
}

func (p *steerRevivalInputProvider1020) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	p.mu.Unlock()
	return p.mockProvider.Chat(ctx, messages, tools, model, opts)
}

func (p *steerRevivalInputProvider1020) Requests() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	requests := make([][]providers.Message, len(p.requests))
	for i := range p.requests {
		requests[i] = append([]providers.Message(nil), p.requests[i]...)
	}
	return requests
}

func recordSteerRevivalInput1020(t *testing.T, al *AgentLoop) *steerRevivalInputProvider1020 {
	t.Helper()
	agent, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: default test agent is not registered")
	}
	provider := &steerRevivalInputProvider1020{}
	agent.Provider = provider
	return provider
}

// The caller's accepted texts are the oracle (CHECK-2 Finding 1), never the
// fixed provider answer. Check both first delivery order and conservation in
// the last actual request: a later history replay cannot conceal reordering,
// and separate appearances in different requests do not prove one full input.
func assertSteerRevivalInput1020(t *testing.T, requests [][]providers.Message, accepted []string) {
	t.Helper()
	if len(requests) == 0 {
		t.Fatal("revived generation made no provider request; accepted late instructions never reached the provider")
	}
	type arrival struct {
		text   string
		offset int
	}
	seen := make(map[string]bool)
	var firstDelivery []string
	var lastInput string
	for _, messages := range requests {
		var userContents []string
		for _, message := range messages {
			if message.Role == "user" {
				userContents = append(userContents, message.Content)
			}
		}
		lastInput = strings.Join(userContents, "\n")
		var arrivals []arrival
		for _, text := range accepted {
			if offset := strings.Index(lastInput, text); offset >= 0 && !seen[text] {
				arrivals = append(arrivals, arrival{text: text, offset: offset})
				seen[text] = true
			}
		}
		sort.Slice(arrivals, func(i, j int) bool { return arrivals[i].offset < arrivals[j].offset })
		for _, item := range arrivals {
			firstDelivery = append(firstDelivery, item.text)
		}
	}
	if !slices.Equal(firstDelivery, accepted) {
		t.Errorf("accepted late-steer first delivery in revived provider input = %q, want all caller instructions in order %q; last user input = %q", firstDelivery, accepted, lastInput)
	}
	previousEnd := 0
	for _, text := range accepted {
		if count := strings.Count(lastInput, text); count != 1 {
			t.Errorf("accepted late steer %q occurs %d times in revived provider input, want exactly 1 unchanged caller instruction; input = %q", text, count, lastInput)
		}
		offset := strings.Index(lastInput, text)
		if offset < previousEnd {
			t.Errorf("accepted late steer %q is missing or out of order in revived provider input; offset=%d previousEnd=%d input=%q", text, offset, previousEnd, lastInput)
		}
		previousEnd = offset + len(text)
	}
	t.Logf("recorded %d revived provider request(s); caller instruction oracle: %q", len(requests), accepted)
}

// CHECK-2 Finding 1: every text accepted by one finishing handoff belongs
// to one revived generation, in arrival order, not just its first text.
func TestSteeredTurnDrain1020_MultipleLateSteersReachOneRevivedGenerationInOrder(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	provider := recordSteerRevivalInput1020(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	child := launchRunningChild(t, al, parentID, "check2-same-handoff-multiple-steers")
	accepted := []string{
		"CHECK2-FIRST-LATE-STEER: inspect the draft",
		"CHECK2-SECOND-LATE-STEER: keep the caller's wording\nand the line break",
		"CHECK2-THIRD-LATE-STEER: report the final result",
	}

	var hookCalls atomic.Int32
	var statuses []EnqueueStatus
	var enqueueErrors []error
	var bufferedTexts []string
	completeStateWriteTestHook = func(sessionID string) {
		if hookCalls.Add(1) != 1 {
			return // Do not create a new handoff during g+1's completion.
		}
		for i, text := range accepted {
			_, status, err := al.EnqueueSteeringMessageWithStatus(sessionID, testDefaultAgentID,
				providers.Message{Role: "user", Content: text}, fmt.Sprintf("check2-late-steer-%d", i))
			statuses = append(statuses, status)
			enqueueErrors = append(enqueueErrors, err)
		}
		al.steering.mu.Lock()
		if transition := al.steering.terminalizing[sessionID]; transition != nil {
			for _, item := range transition.finishingItems {
				bufferedTexts = append(bufferedTexts, item.message.Content)
			}
		}
		al.steering.mu.Unlock()
	}
	t.Cleanup(func() { completeStateWriteTestHook = nil })

	if err := al.completeSteeredTurn(context.Background(), child,
		turnResult{finalContent: "original answer before all late steers"}, nil); err != nil {
		t.Fatalf("completeSteeredTurn: %v", err)
	}
	al.drainSteeredTurns(10 * time.Second)
	if len(statuses) != len(accepted) || len(enqueueErrors) != len(accepted) {
		t.Fatalf("finishing hook accepted %d statuses and %d results, want %d late steers", len(statuses), len(enqueueErrors), len(accepted))
	}
	for i, text := range accepted {
		if enqueueErrors[i] != nil || statuses[i] != EnqueueStatusPostFinish {
			t.Fatalf("late steer %d %q: status=%v error=%v, want accepted into this handoff's PostFinish buffer", i, text, statuses[i], enqueueErrors[i])
		}
	}
	if !slices.Equal(bufferedTexts, accepted) {
		t.Fatalf("same-handoff finishing buffer = %q, want all accepted texts in arrival order %q before revival", bufferedTexts, accepted)
	}
	// One terminal write for g, one for g+1: no second revival may carry
	// a straggler that should have belonged to the original handoff.
	if got := hookCalls.Load(); got != 2 {
		t.Errorf("terminal writes = %d, want exactly 2 (original and one revived generation)", got)
	}
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if rec.Generation != child.Generation+1 || rec.State != session.LifecycleCompleted {
		t.Errorf("same-handoff revival ended at generation=%d state=%s, want exactly generation=%d state=completed", rec.Generation, rec.State, child.Generation+1)
	}
	if got := al.pendingSteeringCountForScope(child.SessionID); got != 0 {
		t.Errorf("pending steering after same-handoff revival = %d, want 0", got)
	}
	assertSteerRevivalInput1020(t, provider.Requests(), accepted)

	msgs, _, _, err := al.GetMessageInboxStore().Drain(parentID, child.SessionID, "", 10)
	if err != nil {
		t.Fatalf("Drain(parent): %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("same-handoff final count = %d, want exactly 2 (unchanged original and one revived final)", len(msgs))
	}
	gotFinals := make(map[string]string)
	for _, message := range msgs {
		handback, err := message.AsSessionMessageHandback()
		if err != nil {
			t.Fatalf("decode final %q: %v", messageIDOf(message), err)
		}
		gotFinals[messageIDOf(message)] = handback.ResultSoFar
	}
	wantFinals := map[string]string{
		fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation):   "original answer before all late steers",
		fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation+1): "Follow-up after a late instruction: Mock response",
	}
	if !reflect.DeepEqual(gotFinals, wantFinals) {
		t.Errorf("same-handoff finals = %v, want original and one prefixed revived final %v", gotFinals, wantFinals)
	}
}
