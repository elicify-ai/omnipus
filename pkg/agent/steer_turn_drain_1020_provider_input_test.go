package agent

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
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
	// Capacity is len(accepted), not len(arrivals)*len(requests): `seen` caps
	// each accepted text to exactly one append across all request iterations,
	// so the final size can never exceed len(accepted) regardless of how many
	// requests or per-request arrivals there are.
	firstDelivery := make([]string, 0, len(accepted))
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
// R1: all input in this pre-commit handoff belongs to the original admission.
// The historical name does not authorize forced revival or a candidate final.
func TestSteeredTurnDrain1020_MultipleLateSteersReachOneRevivedGenerationInOrder(t *testing.T) {
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	child, provider := r1AdmitChild(t, al, parentID, "check2-same-handoff-multiple-steers", "original answer before all late steers", "Mock response", "Mock response", "Mock response")
	accepted := []string{
		"CHECK2-FIRST-LATE-STEER: inspect the draft",
		"CHECK2-SECOND-LATE-STEER: keep the caller's wording\nand the line break",
		"CHECK2-THIRD-LATE-STEER: report the final result",
	}
	var statuses []EnqueueStatus
	var enqueueErrors []error
	var bufferedTexts, bufferedIDs []string
	r1InjectBeforeCommit(t, al, child, func() {
		for i, text := range accepted {
			_, status, err := al.EnqueueSteeringMessageWithStatus(child.SessionID, testDefaultAgentID,
				providers.Message{Role: "user", Content: text}, fmt.Sprintf("check2-late-steer-%d", i))
			statuses = append(statuses, status)
			enqueueErrors = append(enqueueErrors, err)
		}
		al.steering.mu.Lock()
		if transition := al.steering.terminalizing[child.SessionID]; transition != nil {
			for _, item := range transition.finishingItems {
				bufferedTexts = append(bufferedTexts, item.message.Content)
				bufferedIDs = append(bufferedIDs, item.correlationID)
			}
		}
		al.steering.mu.Unlock()
	})
	provider.open(0)
	r1AwaitProvider(t, provider, 1)
	if len(statuses) != len(accepted) || len(enqueueErrors) != len(accepted) {
		t.Fatalf("handoff accepted %d statuses/%d results, want %d", len(statuses), len(enqueueErrors), len(accepted))
	}
	for i := range accepted {
		if enqueueErrors[i] != nil || statuses[i] != EnqueueStatusPostFinish {
			t.Fatalf("late steer %d: status=%v error=%v, want accepted PostFinish", i, statuses[i], enqueueErrors[i])
		}
	}
	if !slices.Equal(bufferedTexts, accepted) || !slices.Equal(bufferedIDs, []string{"check2-late-steer-0", "check2-late-steer-1", "check2-late-steer-2"}) {
		t.Fatalf("finishing text/identity=%q/%q, want unchanged caller text and ordered correlation IDs", bufferedTexts, bufferedIDs)
	}
	r1AssertNoFinal(t, al, child)
	// The drain may present the three instructions at successive model
	// boundaries. Stage enough external replies, then assert conservation in
	// the final actual request, not prematurely after only the first item.
	provider.openAll()
	joinGoalFixtureRuns(t, al)
	assertSteerRevivalInput1020(t, provider.Requests()[1:], accepted)
	r1RequireSameGenerationFinal(t, al, child, "Mock response")
}
