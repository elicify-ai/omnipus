package common_test

import (
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
	"testing"
)

// S-34/S-35/S-90: each guard has an enabling positive control. A no-op
// encoder cannot pass simply by omitting everything on the continuation.
func TestT_P5_S34_S35_S90_ContinuationGuardAndExplicitOff(t *testing.T) {
	routes := []effortWireRoute{
		{name: "native", kind: "anthropic", provider: "anthropic", model: "claude-sonnet-4-6", high: map[string]any{"thinking": map[string]any{"type": "adaptive"}, "output_config": map[string]any{"effort": "high"}}},
		{name: "bedrock", kind: "bedrock", provider: "bedrock", model: "us.anthropic.claude-sonnet-4-6-v1", high: map[string]any{"additionalModelRequestFields": map[string]any{"thinking": map[string]any{"type": "adaptive"}, "output_config": map[string]any{"effort": "high"}}}},
	}
	initial := []providers.Message{{Role: "user", Content: "go"}}
	continuation := append(append([]providers.Message{}, initial...), providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "call_1", Function: &providers.FunctionCall{Name: "shell", Arguments: "{}"}}}}, providers.Message{Role: "tool", ToolCallID: "call_1", Content: "done"})
	for _, route := range routes {
		for _, stream := range []bool{false, true} {
			mode := "nonstream"
			if stream {
				mode = "stream-requested"
			}
			t.Run(route.name+"/"+mode+"/high_current_vs_completed_loop", func(t *testing.T) {
				assertEffortWire(t, captureEffortWire(t, route, stream, "high", initial), route.high)
				guarded := captureEffortWire(t, route, stream, "high", continuation)
				assertEffortWire(t, guarded, map[string]any{})
				nextTurn := append(append([]providers.Message{}, continuation...), providers.Message{Role: "user", Content: "new completed-loop boundary"})
				assertEffortWire(t, captureEffortWire(t, route, stream, "high", nextTurn), route.high)
			})
			t.Run(route.name+"/"+mode+"/off_on_both_requests", func(t *testing.T) {
				want := map[string]any{"thinking": map[string]any{"type": "disabled"}}
				if route.kind == "bedrock" {
					want = map[string]any{}
				} // S-90: Bedrock has NO Off encoding; mapper refuses it before the guard.
				for _, history := range [][]providers.Message{initial, continuation} {
					assertEffortWire(t, captureEffortWire(t, route, stream, "off", history), want)
				}
			})
		}
	}
	t.Run("native_switch_on_guard", func(t *testing.T) {
		r := routes[0]
		on := captureEffortWire(t, r, false, "on", initial)
		require.Equal(t, map[string]any{"type": "adaptive"}, on["thinking"])
		assertEffortWire(t, captureEffortWire(t, r, false, "on", continuation), map[string]any{})
	})
}
