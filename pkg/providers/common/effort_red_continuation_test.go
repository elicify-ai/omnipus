package common_test

// S-30..S-33 / CHECK-07: real decoder → Message → private session codec →
// disk close/reopen → real adapter request, two tool rounds. Missing private
// carrier is a loud BLOCKED RED, not an absent-carrier negative test.
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/openai_compat"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setPrivateDetails(t *testing.T, msg *providers.Message, raw []byte) {
	t.Helper()
	v := reflect.ValueOf(msg).Elem()
	field := v.FieldByName("ReasoningDetails")
	if !field.IsValid() {
		t.Fatal("BLOCKED: Message.ReasoningDetails private carrier not implemented — required by S-30/S-31/S-32/S-33 and FR-013")
	}
	sf, _ := v.Type().FieldByName("ReasoningDetails")
	require.Equal(t, "-", sf.Tag.Get("json"), "FR-013 carrier is private, never a default wire field")
	require.NoError(t, json.Unmarshal(raw, field.Addr().Interface()))
}

func carrierAppend(t *testing.T, s *session.UnifiedStore, id string, m providers.Message, issuer *session.ArchiveAddress) session.ModelSlot {
	t.Helper()
	slot, _, err := s.AppendModelMessage(context.Background(), id, session.ModelAppend{Message: m, ViewMembership: session.ViewMembershipModel, Source: session.EntrySource{Kind: "user"}, ToolResultFor: issuer})
	require.NoError(t, err)
	return slot
}

func TestT_C6_S30_S31_S32_TwoToolRoundsCodecRestartAndRoutes(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "sessions")
			store, err := session.NewUnifiedStore(root)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, store.Close()) })
			meta, err := store.NewSession(session.SessionTypeChat, "web", "carrier-agent")
			require.NoError(t, err)
			carrierAppend(t, store, meta.ID, providers.Message{Role: "user", Content: "do two tool rounds"}, nil)
			bodies := make(chan map[string]any, 8)
			round := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				bodies <- body
				round++
				if round > 2 {
					if body["stream"] == true {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"TWO-ROUNDS-COMPLETED-7731\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					} else {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"TWO-ROUNDS-COMPLETED-7731"},"finish_reason":"stop"}]}`)
					}
					return
				}
				id := fmt.Sprintf("call_%d", round)
				if body["stream"] == true {
					w.Header().Set("Content-Type", "text/event-stream")
					// Fragmented text joins by index; opaque/identity fields are unchanged.
					fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_details\":[{\"index\":0,\"type\":\"reasoning.text\",\"text\":\"step \"}]}}]}\n\n")
					fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_details\":[{\"index\":0,\"type\":\"reasoning.text\",\"text\":\"one\"},{\"index\":1,\"type\":\"reasoning.encrypted\",\"format\":\"opaque/v1\",\"unknown_vendor_field\":{\"keep\":[1,2]},\"data\":\"OPAQUE-PAYLOAD-7731\",\"signature\":\"SIG-7731\",\"id\":\"rs_abc123\"}],\"tool_calls\":[{\"index\":0,\"id\":%q,\"type\":\"function\",\"function\":{\"name\":\"shell\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", id)
				} else {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":%q,"type":"function","function":{"name":"shell","arguments":"{}"}}],"reasoning_details":[{"index":0,"type":"reasoning.text","text":"step one"},{"index":1,"type":"reasoning.encrypted","format":"opaque/v1","unknown_vendor_field":{"keep":[1,2]},"data":"OPAQUE-PAYLOAD-7731","signature":"SIG-7731","id":"rs_abc123"}]},"finish_reason":"tool_calls"}]}`, id)
				}
			}))
			defer srv.Close()
			router, err := openai_compat.NewProvider("key", srv.URL, "", openai_compat.WithProviderID("openrouter"))
			require.NoError(t, err)
			for n := 1; n <= 2; n++ {
				history := store.GetHistory(meta.ID)
				require.NotEmpty(t, history)
				var response *providers.LLMResponse
				if stream {
					response, err = router.ChatStream(context.Background(), history, nil, "deepseek/deepseek-v4.1-flash", map[string]any{"reasoning_effort": "high"}, func(string) {}, nil)
				} else {
					response, err = router.Chat(context.Background(), history, nil, "deepseek/deepseek-v4.1-flash", map[string]any{"reasoning_effort": "high"})
				}
				require.NoError(t, err)
				require.Len(t, response.ToolCalls, 1)
				<-bodies
				msg := providers.Message{Role: "assistant", Content: response.Content, ToolCalls: response.ToolCalls}
				sf, exists := reflect.TypeOf(*response).FieldByName("ReasoningDetails")
				require.True(t, exists, "private LLMResponse carrier exists")
				assert.Equal(t, "-", sf.Tag.Get("json"), "response carrier is private too")
				raw, encodeErr := json.Marshal(response.ReasoningDetails)
				require.NoError(t, encodeErr)
				setPrivateDetails(t, &msg, raw)
				slot := carrierAppend(t, store, meta.ID, msg, nil)
				carrierAppend(t, store, meta.ID, providers.Message{Role: "tool", ToolCallID: response.ToolCalls[0].ID, Content: "tool result"}, &slot.Addr)
				// True process-state reset: close the durable store and replace it.
				require.NoError(t, store.Close())
				store, err = session.NewUnifiedStore(root)
				require.NoError(t, err)
			}
			history := store.GetHistory(meta.ID)
			require.Len(t, history, 5, "user + two assistant/tool rounds survive restart")
			other, err := openai_compat.NewProvider("key", srv.URL, "", openai_compat.WithProviderID("openai"))
			require.NoError(t, err)
			crossResponse, err := other.Chat(context.Background(), history, nil, "gpt-6.1-sol", nil)
			require.NoError(t, err)
			require.Equal(t, "TWO-ROUNDS-COMPLETED-7731", crossResponse.Content)
			cross := <-bodies
			crossRaw, err := json.Marshal(cross)
			require.NoError(t, err)
			assert.Contains(t, string(crossRaw), "call_1", "positive control: serializer retained tool calls, not nil history")
			assert.NotContains(t, string(crossRaw), "reasoning_details", "S-32/S-33: populated OpenRouter carrier never sent cross-route")
			finalResponse, err := router.Chat(context.Background(), history, nil, "deepseek/deepseek-v4.1-flash", map[string]any{"reasoning_effort": "high"})
			require.NoError(t, err)
			require.Equal(t, "TWO-ROUNDS-COMPLETED-7731", finalResponse.Content, "two tool rounds followed by a completed final answer")
			back := <-bodies
			messages, ok := back["messages"].([]any)
			require.True(t, ok)
			found := 0
			want := []any{map[string]any{"index": float64(0), "type": "reasoning.text", "text": "step one"}, map[string]any{"index": float64(1), "type": "reasoning.encrypted", "format": "opaque/v1", "unknown_vendor_field": map[string]any{"keep": []any{float64(1), float64(2)}}, "data": "OPAQUE-PAYLOAD-7731", "signature": "SIG-7731", "id": "rs_abc123"}}
			for _, item := range messages {
				m := effortRequireType[map[string]any](t, item)
				if m["role"] == "assistant" {
					found++
					assert.Equal(t, want, m["reasoning_details"], "every decoded opaque item survives codec/restart/route change unchanged")
				}
			}
			assert.Equal(t, 2, found)
		})
	}
}

func TestT_C6_S31_ThinkingBlocksSurviveDurableCodecRestart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	s, err := session.NewUnifiedStore(root)
	require.NoError(t, err)
	meta, err := s.NewSession(session.SessionTypeChat, "web", "carrier-agent")
	require.NoError(t, err)
	want := []string{`{"type":"thinking","thinking":"private","signature":"NATIVE-SIGNED-7731"}`}
	carrierAppend(t, s, meta.ID, providers.Message{Role: "assistant", Content: "visible", ThinkingBlocks: want}, nil)
	require.NoError(t, s.Close())
	s, err = session.NewUnifiedStore(root)
	require.NoError(t, err)
	defer s.Close()
	history := s.GetHistory(meta.ID)
	require.Len(t, history, 1)
	assert.Equal(t, want, history[0].ThinkingBlocks)
	assert.Equal(t, "visible", history[0].Content)
}

func TestT_C6_S33_PopulatedPrivateCarrierCrossRoute(t *testing.T) {
	msg := providers.Message{Role: "assistant", Content: "VISIBLE-CONTROL-7731", ThinkingBlocks: []string{`{"type":"thinking","thinking":"native-private","signature":"NATIVE-SIGNED-7731"}`}}
	setPrivateDetails(t, &msg, []byte(`[{"type":"reasoning.encrypted","format":"opaque/v1","unknown_vendor_field":{"keep":[1,2]},"data":"OPAQUE-PAYLOAD-7731","signature":"SIG-7731"}]`))
	for _, route := range []effortWireRoute{{provider: "openai", kind: "compat", model: "gpt-6.1-sol"}, {provider: "anthropic", kind: "anthropic", model: "claude-sonnet-4-6"}, {provider: "openrouter", kind: "compat", model: "deepseek/deepseek-v4.1-flash"}} {
		body := captureEffortWire(t, route, false, "provider_default", []providers.Message{{Role: "user", Content: "hi"}, msg, {Role: "user", Content: "continue"}})
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		require.Contains(t, string(raw), "VISIBLE-CONTROL-7731", "empty/nil serializer must fail")
		if route.provider == "openrouter" {
			assert.Contains(t, string(raw), "reasoning_details")
			assert.Contains(t, string(raw), "OPAQUE-PAYLOAD-7731")
		} else {
			assert.NotContains(t, string(raw), "reasoning_details")
			assert.NotContains(t, string(raw), "OPAQUE-PAYLOAD-7731")
		}
		if route.kind == "anthropic" {
			assert.Contains(t, string(raw), "NATIVE-SIGNED-7731", "own native carrier must still replay")
		}
		if route.kind == "compat" {
			assert.NotContains(t, string(raw), "NATIVE-SIGNED-7731")
		}
	}
	raw, _ := json.Marshal(msg)
	assert.False(t, strings.Contains(string(raw), "OPAQUE-PAYLOAD-7731"), "private carrier does not default-marshal")
}
