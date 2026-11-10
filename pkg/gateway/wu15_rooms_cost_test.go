package gateway

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Paid-model edge reports usage only. Allocation expectations come from §6.14,
// using the independent main-turn DoneStats.cost as the monetary input (Chief
// Q4 A). No private rate-table output is copied into a room oracle.
type wu15UsageProvider struct{ *wu15ScriptProvider }

func (p *wu15UsageProvider) Chat(ctx context.Context, msgs []providers.Message, defs []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	conversation, err := gatewayConversationBeforeVolatileTail(msgs)
	if err != nil {
		return nil, err
	}
	response, err := p.wu15ScriptProvider.Chat(ctx, msgs, defs, model, options)
	if err != nil {
		return nil, err
	}
	if len(conversation) == 0 || conversation[len(conversation)-1].Role != "tool" {
		response.Usage = &providers.UsageInfo{PromptTokens: 3000, TotalTokens: 3000}
	}
	return response, nil
}

func (h *wu15RoomHarness) pricedProvider(id string, provider providers.LLMProvider) {
	h.t.Helper()
	inst, ok := h.al.GetRegistry().GetAgent(id)
	require.True(h.t, ok)
	// Catalog pricing keys on BOTH provider and model. A model-only candidate
	// has provider="" and is unpriced; pin the catalog pair while keeping the
	// external transport scripted and the real accounting/allocation intact.
	const providerID, model = "openai", "gpt-4o"
	require.True(h.t, providers.ProviderCatalog().Resolve(providerID, model).Found(),
		"SETUP: the priced WU15 provider/model pair must exist in the catalog")
	inst.Provider = provider
	inst.Model = model // No price literal is used as an expected room cost.
	inst.Candidates = []providers.FallbackCandidate{{Provider: providerID, Model: model}}
	inst.StoreProviderPool(map[string]providers.LLMProvider{providerID: provider})
}

func TestWU15Rooms_AllocatedCostEqualsMainTurnInputAndIsNeverCountedTwice(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.post(room.Id, "priced", "@Mia priced request")
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "priced"), "mia")
	p := &wu15UsageProvider{&wu15ScriptProvider{requestID: request.RequestId, answer: "priced answer"}}
	h.pricedProvider("mia", p)
	conn := h.connect()
	require.NoError(t, conn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
	wu15ReadFrame(t, conn, "catch_up_complete")
	h.startLoop()
	done := wu15ReadTurnDone(t, conn)
	require.NotNil(t, done.Stats)
	require.NotNil(t, done.Stats.Cost)
	require.Greater(t, *done.Stats.Cost, float64(0), "instrument: real priced model turn")
	costInput := int64(math.Round(*done.Stats.Cost * 1_000_000))
	snap := h.waitState(room.Id, request.RequestId, "answered")
	require.Equal(t, costInput, snap.AllocatedCostMicroUsd, "§6.14: one newly consumed room input out of one total input = full turn cost")
	require.False(t, snap.AllocatedCostUnpriced)
	h.post(room.Id, "priced", "@Mia priced request")
	require.Equal(t, costInput, h.snapshot(room.Id).AllocatedCostMicroUsd, "§6.14/FR-216: Retry never counts the same turn twice")
	h = h.restart()
	require.Equal(t, costInput, h.snapshot(room.Id).AllocatedCostMicroUsd, "§6.14: reload/restart does not re-add turn cost")
}

func TestWU15Rooms_MixedInputsAllocateByConsumedEndOfTurnWeights(t *testing.T) {
	h := wu15NewHarness(t)
	roomA := h.create("squad", "A", "mia", "jim")
	roomB := h.create("squad", "B", "mia", "jim")
	h.stopMain("mia")
	h.post(roomA.Id, "input-a", "@Mia INPUT_FROM_A")
	h.post(roomB.Id, "input-b", "@Mia INPUT_FROM_B")
	aInput := wu15Delivery(t, wu15FindEntry(t, h.snapshot(roomA.Id), "input-a"), "mia")
	bInput := wu15Delivery(t, wu15FindEntry(t, h.snapshot(roomB.Id), "input-b"), "mia")
	for _, id := range []string{aInput.RequestId, bInput.RequestId} {
		capture, found, err := h.al.RequestLedger().Get(h.main("mia"), id)
		require.NoError(t, err)
		require.True(t, found)
		require.False(t, capture.Delivered, "instrument: room input not previously consumed")
	}
	p := &wu15UsageProvider{&wu15ScriptProvider{answer: "mixed-input completion"}}
	h.pricedProvider("mia", p)
	conn := h.connect()
	require.NoError(t, conn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
	wu15ReadFrame(t, conn, "catch_up_complete")
	h.startLoop()
	h.sendMain(conn, "mia", "WEB_INPUT_C")
	done := wu15ReadTurnDone(t, conn)
	wu15AssertProviderSaw(t, p.wu15ScriptProvider, "INPUT_FROM_A")
	wu15AssertProviderSaw(t, p.wu15ScriptProvider, "INPUT_FROM_B")
	wu15AssertProviderSaw(t, p.wu15ScriptProvider, "WEB_INPUT_C")
	// Logical input identities, not occurrences of quoted prose, determine
	// weight. Both captures were undelivered before this turn; both must be
	// delivered below, and the sole model call sees those inputs plus the one
	// fresh web message. Repeated room context text is not a second input.
	require.Len(t, p.inputs(), 1, "instrument: all three inputs reached the same real turn")
	require.NotNil(t, done.Stats)
	require.NotNil(t, done.Stats.Cost)
	require.Greater(t, *done.Stats.Cost, float64(0))
	monetaryInput := int64(math.Round(*done.Stats.Cost * 1_000_000))
	require.Equal(t, int64(0), monetaryInput%3, "instrument: this fixture has an exact whole-micro-dollar third; Q-I fractional rounding is tested separately")
	for _, id := range []string{aInput.RequestId, bInput.RequestId} {
		capture, found, err := h.al.RequestLedger().Get(h.main("mia"), id)
		require.NoError(t, err)
		require.True(t, found)
		require.True(t, capture.Delivered, "instrument: same real turn consumed both room inputs")
	}
	h.waitMainSettled("mia")
	a, b := h.snapshot(roomA.Id), h.snapshot(roomB.Id)
	require.Equal(t, monetaryInput/3, a.AllocatedCostMicroUsd, "§6.14: A consumed one of three inputs")
	require.Equal(t, monetaryInput/3, b.AllocatedCostMicroUsd, "§6.14: B consumed one of three inputs")
	require.Equal(t, 2*monetaryInput/3, a.AllocatedCostMicroUsd+b.AllocatedCostMicroUsd, "web input share is not assigned to either room")
}

func TestWU15Rooms_UnpricedTurnAddsWarningButNoMoney(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.post(room.Id, "unpriced", "@Mia unknown-priced request")
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "unpriced"), "mia")
	inst, ok := h.al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	inst.Provider = &wu15UsageProvider{&wu15ScriptProvider{requestID: request.RequestId, answer: "unknown-priced answer"}}
	inst.StoreProviderPool(map[string]providers.LLMProvider{})
	h.startLoop()
	snap := h.waitState(room.Id, request.RequestId, "answered")
	require.Equal(t, int64(0), snap.AllocatedCostMicroUsd, "§6.14: fallback guessed price is not a known price")
	require.True(t, snap.AllocatedCostUnpriced, "§6.14: plus unpriced turns, not silently zero-priced")
	h = h.restart()
	require.True(t, h.snapshot(room.Id).AllocatedCostUnpriced, "warning survives reload")
}

// BDD-17.5's fifty exchanges plus one boundary probe catches a cap at fifty.
const wu15RelayExchanges = 51

type wu15RelayProvider struct {
	targetName string
	counter    *atomic.Int64
}

func (*wu15RelayProvider) GetDefaultModel() string { return "gpt-4o" }
func (p *wu15RelayProvider) Chat(_ context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	conversation, err := gatewayConversationBeforeVolatileTail(msgs)
	if err != nil {
		return nil, err
	}
	if len(conversation) > 0 && conversation[len(conversation)-1].Role == "tool" {
		return &providers.LLMResponse{Content: "Finished.", FinishReason: "stop"}, nil
	}
	// Real request ids are disclosed to the receiver model by the existing
	// answer_request instruction. The scripted model copies the newest id just
	// as a provider would; this is input decoding, never a room oracle.
	var ids []string
	pattern := regexp.MustCompile(`request_id="([^"]+)"`)
	for _, msg := range conversation {
		if msg.Role != "system" {
			for _, match := range pattern.FindAllStringSubmatch(msg.Content, -1) {
				ids = append(ids, match[1])
			}
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("wu15 scripted model received no answerable request id")
	}
	n := p.counter.Add(1)
	text := fmt.Sprintf("exchange-%d", n)
	if n < wu15RelayExchanges {
		text += " @" + p.targetName
	}
	return &providers.LLMResponse{FinishReason: "tool_calls", Usage: &providers.UsageInfo{PromptTokens: 3000, TotalTokens: 3000}, ToolCalls: []providers.ToolCall{{
		ID: fmt.Sprintf("relay-%d", n), Type: "function", Name: "answer_request", Arguments: map[string]any{"request_id": ids[len(ids)-1], "text": text},
	}}}, nil
}

func TestWU15Rooms_FiftyPlusOneAgentExchangesHaveNoReplyCapAndAccumulateCost(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	var count atomic.Int64
	h.pricedProvider("mia", &wu15RelayProvider{targetName: "Jim", counter: &count})
	h.pricedProvider("jim", &wu15RelayProvider{targetName: "Mia", counter: &count})
	conn := h.connect()
	h.attach(conn, room.Id, 0)
	mainConns := make(map[string]*websocket.Conn)
	for _, id := range []string{"mia", "jim"} {
		mainConns[id] = h.connect()
		require.NoError(t, mainConns[id].WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main(id)}))
		wu15ReadFrame(t, mainConns[id], "catch_up_complete")
	}
	h.startLoop()
	h.post(room.Id, "first", "@Jim begin relay")
	var costs []int64
	var expectedTotal int64
	seenTurns := map[string]bool{}
	for n := 1; n <= wu15RelayExchanges; n++ {
		agentID := "jim"
		if n%2 == 0 {
			agentID = "mia"
		}
		text := fmt.Sprintf("exchange-%d", n)
		if n < wu15RelayExchanges {
			if agentID == "jim" {
				text += " @Mia"
			} else {
				text += " @Jim"
			}
		}
		answer := wu15ReadAgentRoomEntry(t, conn, agentID, text)
		require.False(t, answer.AfterPause)
		done := wu15ReadTurnDone(t, mainConns[agentID])
		require.NotNil(t, done.Stats.Cost)
		require.Greater(t, *done.Stats.Cost, float64(0), "instrument: every exchange has a priced producing turn")
		require.False(t, seenTurns[*done.TurnId], "one real producing turn per answer")
		seenTurns[*done.TurnId] = true
		expectedTotal += int64(math.Round(*done.Stats.Cost * 1_000_000))
		costs = append(costs, h.snapshot(room.Id).AllocatedCostMicroUsd)
	}
	require.Equal(t, int64(wu15RelayExchanges), count.Load(), "BDD-17.5 + boundary probe: fifty-one exchanges, no human and no automatic cap")
	require.Equal(t, expectedTotal, h.snapshot(room.Id).AllocatedCostMicroUsd, "§6.14: every real producing turn counted once, no lost or doubled turn")
	require.Greater(t, costs[len(costs)-1], costs[0], "BDD-17.5: real room-origin turns accumulate cost")
	for i := 1; i < len(costs); i++ {
		require.GreaterOrEqual(t, costs[i], costs[i-1], "§6.14: allocated cost never decreases")
	}
}
