package agent

// CHECK-13 / S-80: load the setting, run the real recap option builder, then
// delegate unchanged options to the actual adapter. Only the HTTP peer is fake.
// Field names and 4096 come from S-80's output-cap adapter table, not the code.
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	anthropic "github.com/elicify-ai/omnipus/pkg/providers/anthropic"
	"github.com/elicify-ai/omnipus/pkg/providers/azure"
	"github.com/elicify-ai/omnipus/pkg/providers/bedrock"
	"github.com/elicify-ai/omnipus/pkg/providers/openai_compat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recapAdapterChat interface {
	Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error)
}

// A leaf adapter does not necessarily expose GetDefaultModel. This wrapper is
// plumbing only: it changes neither the request options nor the paid result.
type recapAdapterBridge struct {
	recapAdapterChat
	model string
}

func (b *recapAdapterBridge) GetDefaultModel() string { return b.model }

func TestT_L4_S80_RecapCapReachesEverySerializedAdapterField(t *testing.T) {
	rows := []struct {
		name, kind, model, configuredField, wantField string
	}{
		{"configured compatible field", "compat", "recap-model", "max_output_tokens", "max_output_tokens"},
		{"compatible glm heuristic", "compat", "glm-test", "", "max_completion_tokens"},
		{"compatible o1 heuristic", "compat", "o1-test", "", "max_completion_tokens"},
		{"compatible gpt5 heuristic", "compat", "gpt-5-test", "", "max_completion_tokens"},
		{"compatible ordinary model", "compat", "deepseek/deepseek-v4.1-flash", "", "max_tokens"},
		{"native Anthropic", "anthropic", "claude-sonnet-4-6", "", "max_tokens"},
		{"Azure Responses", "azure", "recap-deployment", "", "max_output_tokens"},
		{"Bedrock Converse", "bedrock", "amazon.nova-pro-v1:0", "", "inferenceConfig.maxTokens"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			captured := make(chan map[string]any, 4)
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("adapter request JSON: %v", err)
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				captured <- body
				// A normal non-transient refusal avoids response-format assumptions
				// and never substitutes for an assertion about serialization.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"recap-cap-capture-7731","type":"invalid_request_error"}}`))
			}))
			defer peer.Close()
			var adapter recapAdapterChat
			var err error
			switch row.kind {
			case "anthropic":
				adapter = anthropic.NewProviderWithBaseURL("test-key", peer.URL)
			case "azure":
				adapter, err = azure.NewProvider("test-key", peer.URL, "")
			case "bedrock":
				adapter, err = bedrock.NewProvider("test-key", bedrock.WithBaseEndpoint(peer.URL))
			default:
				adapter, err = openai_compat.NewProvider("test-key", peer.URL, "", openai_compat.WithMaxTokensField(row.configuredField))
			}
			require.NoError(t, err)
			cfg, _ := recapRuntimeConfig(t, "settings", map[string]any{"recap_max_output_tokens": 4096})
			cfg.Agents.Defaults.RecapModel = row.model
			al, sid, ag, _ := buildEffortRecapLoop(t, cfg, &recapAdapterBridge{adapter, row.model})
			rr := &agentLoopRunRecap{al: al, sessionID: sid, trigger: "test", agentInst: ag, userTurns: []string{"recap-cap-input-7731"}}
			require.True(t, rr.callCandidates(), "recording peer refused, so the real heuristic path handles the failure")
			var body map[string]any
			select {
			case body = <-captured:
			default:
				t.Fatal("real recap did not reach the actual adapter HTTP peer")
			}
			got, present := bodyFind(body, row.wantField)
			require.True(t, present, "S-80 serialized cap field %s", row.wantField)
			assert.Equal(t, float64(4096), got, "configured recap limit must reach serialized %s", row.wantField)
			for _, other := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens", "inferenceConfig.maxTokens"} {
				if other != row.wantField {
					_, exists := bodyFind(body, other)
					assert.False(t, exists, "no foreign output-cap field %s", other)
				}
			}
		})
	}
}
