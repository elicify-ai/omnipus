package wu15rooms_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/rooms"
)

type billingProvider struct {
	requestID string
	calls     atomic.Int64
}

func (*billingProvider) GetDefaultModel() string { return "wu15-unpriced" }
func (p *billingProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	if p.calls.Add(1) > 1 {
		return &providers.LLMResponse{Content: "Finished.", FinishReason: "stop"}, nil
	}
	return &providers.LLMResponse{FinishReason: "tool_calls", Usage: &providers.UsageInfo{PromptTokens: 6000, TotalTokens: 6000}, ToolCalls: []providers.ToolCall{{
		ID: "billing-answer", Type: "function", Name: "answer_request", Arguments: map[string]any{"request_id": p.requestID, "text": "priced response"},
	}}}, nil
}

func TestWU15Rooms_ExactThreeInputThirtyMilliDollarFixture(t *testing.T) {
	f := newFixture(t)
	billing := f.room("Pricer wiring control")
	require.NoError(t, f.post(billing.Id, "billing", "@Mia price this", "mia"))
	request := deliveryFor(t, entryFor(t, f.snapshot(billing.Id), "billing"), "mia")
	provider := &billingProvider{requestID: request.RequestId}
	instance, ok := f.loop.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	instance.Provider = provider
	instance.StoreProviderPool(map[string]providers.LLMProvider{})
	var prices atomic.Int64
	var pricedTokens atomic.Int64
	f.loop.SetRoomCallPricer(agent.RoomCallPricer(func(_ string, usage providers.UsageInfo) float64 {
		prices.Add(1)
		pricedTokens.Store(int64(usage.PromptTokens))
		return 0.03 // BDD-17.30 independent monetary input.
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.loop.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(30 * time.Second):
			t.Error("fixture: real loop did not exit")
		}
	})
	var lastErr error
	require.Eventually(t, func() bool {
		room, err := f.service.Get(billing.Id)
		lastErr = err
		return err == nil && room.AllocatedCostMicroUsd == 30000
	}, 30*time.Second, 10*time.Millisecond, "instrument: public RoomCallPricer feeds actual room-turn allocation: %v", lastErr)
	require.NoError(t, lastErr)
	require.Equal(t, int64(1), prices.Load(), "one billable model call, no fallback guessed price")
	require.Equal(t, int64(6000), pricedTokens.Load(), "pricing seam received actual provider usage")

	// Independent §6.14 / BDD-17.30 allocation fixture. Service inputs describe
	// one turn with A+B+web, not three separately charged turns. The wire value
	// is micro-USD: $0.03 × 1/3 × 1,000,000 = exactly 10000 per room.
	a, b := f.room("A"), f.room("B")
	for _, id := range []string{a.Id, b.Id} {
		cost := rooms.TurnCost{RoomID: id, TurnID: "three-input-turn", CostUSD: 0.03, Priced: true, Inputs: 3, RoomInputs: 1}
		f.service.AddTurnCost(cost)
		f.service.AddTurnCost(cost)
		got, err := f.service.Get(id)
		require.NoError(t, err)
		require.Equal(t, int64(10000), got.AllocatedCostMicroUsd, "BDD-17.30: exact one-cent allocated share, turn counted once")
		require.False(t, got.AllocatedCostUnpriced)
		f.service.AddTurnCost(rooms.TurnCost{RoomID: id, TurnID: "unknown-price", Inputs: 1, RoomInputs: 1})
		got, err = f.service.Get(id)
		require.NoError(t, err)
		require.Equal(t, int64(10000), got.AllocatedCostMicroUsd)
		require.True(t, got.AllocatedCostUnpriced, "unpriced adds a warning, not guessed money")
	}
}

func TestWU15Rooms_ExactPricingAndMicroDollarRoundingFixture(t *testing.T) {
	// Binding WU15-ROOM-RULINGS-20261014.md::Q-I, chief 2026-10-15:
	// nearest whole micro-dollar, exact positive halves round upward.
	for _, tc := range []struct {
		name               string
		cost               float64
		inputs, roomInputs int
		want               int64
	}{
		{"below_half_micro", 0.000001, 3, 1, 0},
		{"half_micro", 0.000001, 2, 1, 1},
		{"above_half_micro", 0.000002, 3, 1, 1},
		{"one_and_half_micro", 0.000003, 2, 1, 2},
		{"whole_micro", 0.000003, 3, 1, 1},
		{"cent_minus_micro", 0.009999, 1, 1, 9999},
		{"exact_cent", 0.01, 1, 1, 10000},
		{"cent_plus_micro", 0.010001, 1, 1, 10001},
		{"multiple_room_inputs", 0.03, 3, 2, 20000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			room := f.room("Rounding")
			cost := rooms.TurnCost{RoomID: room.Id, TurnID: "priced-turn", CostUSD: tc.cost, Priced: true, Inputs: tc.inputs, RoomInputs: tc.roomInputs}
			f.service.AddTurnCost(cost)
			f.service.AddTurnCost(cost)
			got, err := f.service.Get(room.Id)
			require.NoError(t, err)
			require.Equal(t, tc.want, got.AllocatedCostMicroUsd, "Q-I exact monetary oracle, not an implementation-derived value")
			f.reload()
			got, err = f.service.Get(room.Id)
			require.NoError(t, err)
			require.Equal(t, tc.want, got.AllocatedCostMicroUsd, "rounded share and turn id survive reload")
			f.service.AddTurnCost(cost)
			got, err = f.service.Get(room.Id)
			require.NoError(t, err)
			require.Equal(t, tc.want, got.AllocatedCostMicroUsd, "replay after reload does not double the rounded cost")
		})
	}
}
