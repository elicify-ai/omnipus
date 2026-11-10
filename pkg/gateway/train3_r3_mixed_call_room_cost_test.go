package gateway

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/stretchr/testify/require"
)

// Founder B1 / §6.14 / BDD-17.30: a provider call with ANY unpriced tokens
// marks the room allocation unpriced and adds no dollars. The turn still
// retains the priced portions of that call. This cohere/cache-model catalog
// row is the same mixed fixture as pkg/agent's backendCallCatalog: 3 input,
// 15 output, .3 cache-read and 3.75 short-write USD/million, no 1-hour rate.
// It is a catalog input, NOT a replacement for pricing or room allocation.
const train3R3MixedCatalog = `{
 "schema_version":"2.0.0","version":"v2026.10.16","updated_at":"2026-10-16T00:00:00Z","source":"fixture",
 "default_resize_limits":{"long_edge_px":7680,"max_bytes":10485760},
 "providers":[{"id":"cohere","name":"Mixed fixture","api":"https://mixed.example/v1","protocol":"openai-compatible","tier":"standard","auth_methods":["api_key"],"models":[{"id":"cache-model","name":"Fixture","tool_call":true,"context_window":200000,"max_output_tokens":4096,"input_modalities":["text"],"status":"active","cost":{"input":3,"output":15,"cache_read":0.3,"cache_write":3.75}}]}]
}`

type train3R3UsageProvider struct {
	*wu15ScriptProvider
	usage      providers.UsageInfo
	requestIDs []string
}

func (p *train3R3UsageProvider) Chat(ctx context.Context, msgs []providers.Message, defs []providers.ToolDefinition, model string, opts map[string]any) (*providers.LLMResponse, error) {
	response, err := p.wu15ScriptProvider.Chat(ctx, msgs, defs, model, opts)
	if err != nil {
		return nil, err
	}
	// A plain reply leaves room requests unanswered and legitimately schedules
	// another reminder turn. Answer both through the real tool path instead:
	// one usage-bearing tool-call response, then one zero-usage final response.
	// §6.14 allocates the whole turn, not a scheduler-dependent call count.
	usage := providers.UsageInfo{}
	if len(p.inputs()) == 1 {
		response = &providers.LLMResponse{FinishReason: "tool_calls"}
		for _, requestID := range p.requestIDs {
			response.ToolCalls = append(response.ToolCalls, providers.ToolCall{
				ID: "cost-answer-" + requestID, Type: "function", Name: "answer_request",
				Arguments: map[string]any{"request_id": requestID, "text": "cost boundary complete"},
			})
		}
		usage = p.usage
	}
	response.Usage = &usage
	return response, nil
}

func (p *train3R3UsageProvider) ChatStream(ctx context.Context, msgs []providers.Message, defs []providers.ToolDefinition, model string, opts map[string]any, onChunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	response, err := p.Chat(ctx, msgs, defs, model, opts)
	return wu15StreamResponse(response, err, onChunk)
}

func TestTrain3Fix3_MixedCallRoomCostKeepsRatioDedupAndUnpricedBoundary(t *testing.T) {
	for _, tc := range []struct {
		name           string
		usage          providers.UsageInfo
		turnMicroUSD   int64
		roomMicroUSD   int64
		unpricedTokens int
	}{
		{
			name:  "fully priced control allocates exact thirds",
			usage: providers.UsageInfo{PromptTokens: 3000, TotalTokens: 3000},
			// 3000 input tokens × $3/million = 9000 microdollars;
			// each room supplied one of the three consumed inputs.
			turnMicroUSD: 9000, roomMicroUSD: 3000,
		},
		{
			name: "mixed call adds zero room dollars and marks unpriced",
			usage: providers.UsageInfo{PromptTokens: 100, CompletionTokens: 10, CacheReadTokens: 800,
				CacheWriteTokens: 100, CacheWrite5mTokens: 40, CacheWrite1hTokens: 60, TotalTokens: 1010},
			// Priced portions: 100×3 + 10×15 + 800×.3 + 40×3.75
			// = 840 microdollars. The 60 one-hour tokens are unpriced for
			// cohere, so B1 gives each room ZERO, not 840/3 = 280.
			turnMicroUSD: 840, roomMicroUSD: 0, unpricedTokens: 60,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cat, err := catalog.NewCatalog([]byte(train3R3MixedCatalog))
			require.NoError(t, err)
			previous := providers.ProviderCatalog()
			providers.SetCatalog(cat)
			t.Cleanup(func() { providers.SetCatalog(previous) })
			h := wu15NewHarness(t)
			roomA := h.create("squad", "Cost A", "mia", "jim")
			roomB := h.create("squad", "Cost B", "mia", "jim")
			h.stopMain("mia")
			h.post(roomA.Id, "cost-a", "@Mia COST_ROOM_A")
			h.post(roomB.Id, "cost-b", "@Mia COST_ROOM_B")
			// Join actual admission completion, not a timing delay or a cost
			// value. Both captures must be held before the web revival starts.
			h.api.rooms.Load().svc.WaitIdleForTest()
			a := wu15Delivery(t, wu15FindEntry(t, h.snapshot(roomA.Id), "cost-a"), "mia")
			b := wu15Delivery(t, wu15FindEntry(t, h.snapshot(roomB.Id), "cost-b"), "mia")
			for _, request := range []gen.RoomDeliveryState{a, b} {
				require.Equal(t, "held", request.State, "room admission settled while the main was stopped")
				capture, found, captureErr := h.al.RequestLedger().Get(h.main("mia"), request.RequestId)
				require.NoError(t, captureErr)
				require.True(t, found)
				require.True(t, capture.Held, "both room inputs are durably held before web revival")
				require.False(t, capture.Delivered, "ratio inputs were not consumed by an earlier turn")
			}
			p := &train3R3UsageProvider{
				wu15ScriptProvider: &wu15ScriptProvider{answer: "cost boundary complete"},
				usage:              tc.usage, requestIDs: []string{a.RequestId, b.RequestId},
			}
			inst, ok := h.al.GetRegistry().GetAgent("mia")
			require.True(t, ok)
			inst.Provider = p
			inst.Model = "cache-model"
			inst.Candidates = []providers.FallbackCandidate{{Provider: "cohere", Model: "cache-model"}}
			inst.StoreProviderPool(map[string]providers.LLMProvider{"cohere": p})
			conn := h.connect()
			require.NoError(t, conn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
			wu15ReadFrame(t, conn, "catch_up_complete")
			h.sendMain(conn, "mia", "COST_WEB_C")
			done := wu15ReadTurnDone(t, conn)
			h.waitMainSettled("mia")
			require.NotNil(t, done.TurnId)
			require.NotNil(t, done.Stats)
			require.NotNil(t, done.Stats.Cost)
			require.Equal(t, tc.turnMicroUSD, int64(math.Round(*done.Stats.Cost*1e6)), "turn accounting retains all known portions")
			calls := p.inputs()
			require.Len(t, calls, 2, "one usage-bearing answer call and one zero-usage final call; no unanswered-request reminder")
			for _, text := range []string{"COST_ROOM_A", "COST_ROOM_B", "COST_WEB_C"} {
				seenInFirstCall := false
				for _, message := range calls[0] {
					if message.Role != "system" && strings.Contains(message.Content, text) {
						seenInFirstCall = true
					}
				}
				require.True(t, seenInFirstCall, "all three logical inputs entered the usage-bearing call: missing %s", text)
			}
			entries, err := h.al.GetSessionStore().ReadTranscript(h.main("mia"))
			require.NoError(t, err)
			var callCounts []int
			for _, entry := range entries {
				if entry.TurnID == *done.TurnId && entry.ProviderID == "cohere" && entry.ModelID == "cache-model" {
					callCounts = append(callCounts, entry.UnpricedTokens)
				}
			}
			require.Equal(t, []int{tc.unpricedTokens, 0}, callCounts, "the answer call's exact unpriced count and the zero-usage final call are retained")
			for index, room := range []gen.Room{roomA, roomB} {
				snap := h.snapshot(room.Id)
				delivery := wu15Delivery(t, wu15FindEntry(t, snap, []string{"cost-a", "cost-b"}[index]), "mia")
				require.Equal(t, "answered", delivery.State, "both requests are actually answered, so no reminder turn remains owed")
				require.Equal(t, tc.roomMicroUSD, snap.AllocatedCostMicroUsd, "BDD-17.30: each room's exact one-third allocation, or B1 zero")
				require.Equal(t, tc.unpricedTokens > 0, snap.AllocatedCostUnpriced, "a mixed call must not look fully priced")
			}
			// Replay the producing turn's allocation through the real gateway
			// runtime adapter. This exercises the turn-ID dedup guard itself,
			// not just message admission avoiding another provider call.
			for _, room := range []gen.Room{roomA, roomB} {
				runtime := agentRoomRuntime{svc: h.api.rooms.Load().svc}
				runtime.RoomTurnCost(agent.RoomTurnCost{
					RoomID: room.Id, TurnID: *done.TurnId, Inputs: 3, RoomInputs: 1,
					CostUSD: float64(tc.turnMicroUSD) / 1e6, Priced: tc.unpricedTokens == 0,
				})
				require.Equal(t, tc.roomMicroUSD, h.snapshot(room.Id).AllocatedCostMicroUsd,
					"repeated turn-ID publication never doubles allocation")
			}
			// Retry the SAME logical inputs: no new turn and no new debit.
			h.post(roomA.Id, "cost-a", "@Mia COST_ROOM_A")
			h.post(roomB.Id, "cost-b", "@Mia COST_ROOM_B")
			require.Len(t, p.inputs(), 2, "completed input retry does not call the provider again")
			for _, room := range []gen.Room{roomA, roomB} {
				require.Equal(t, tc.roomMicroUSD, h.snapshot(room.Id).AllocatedCostMicroUsd, "retry never doubles allocation")
			}
			h = h.restart()
			for _, room := range []gen.Room{roomA, roomB} {
				snap := h.snapshot(room.Id)
				require.Equal(t, tc.roomMicroUSD, snap.AllocatedCostMicroUsd, "reload never doubles allocation")
				require.Equal(t, tc.unpricedTokens > 0, snap.AllocatedCostUnpriced, "reload retains the unknown-price warning")
			}
		})
	}
}
