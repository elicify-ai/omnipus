package common_test

// Spec r10 S-17/S-18/S-19: independently derived exact body oracles. All
// serialization and provider dispatch are real; only the HTTP peer is fake.
// The capture peer deliberately refuses the request after saving its body,
// avoiding response-parser fixtures unrelated to this test's boundary.
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	anthropic "github.com/elicify-ai/omnipus/pkg/providers/anthropic"
	"github.com/elicify-ai/omnipus/pkg/providers/bedrock"
	"github.com/elicify-ai/omnipus/pkg/providers/openai_compat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type effortWireRoute struct {
	name, provider, model, kind string
	high                        map[string]any
}

func effortWireRoutes() []effortWireRoute {
	return []effortWireRoute{
		{"OpenAI chat", "openai", "gpt-6.1-sol", "compat", map[string]any{"reasoning_effort": "high"}},
		{"OpenRouter levels", "openrouter", "deepseek/deepseek-v4.1-flash", "compat", map[string]any{"reasoning": map[string]any{"effort": "high"}}},
		{"DeepSeek native", "deepseek", "deepseek-v4.1-flash", "compat", map[string]any{"reasoning_effort": "high"}},
		{"Z.ai native", "z-ai", "glm-5.2", "compat", map[string]any{"reasoning_effort": "high"}},
		{"other compatible", "example-compat", "generic-model", "compat", map[string]any{"reasoning_effort": "high"}},
		{"native Anthropic", "anthropic", "claude-sonnet-4-6", "anthropic", map[string]any{"thinking": map[string]any{"type": "adaptive"}, "output_config": map[string]any{"effort": "high"}}},
		{"Anthropic-protocol row", "z-ai", "glm-5.2", "anthropic", map[string]any{"thinking": map[string]any{"type": "adaptive"}, "output_config": map[string]any{"effort": "high"}}},
		{"Bedrock Anthropic profile", "bedrock", "us.anthropic.claude-sonnet-4-6-v1", "bedrock", map[string]any{"additionalModelRequestFields": map[string]any{"thinking": map[string]any{"type": "adaptive"}, "output_config": map[string]any{"effort": "high"}}}},
		{"Bedrock Nova profile", "bedrock", "us.amazon.nova-pro-v1:0", "bedrock", map[string]any{"additionalModelRequestFields": map[string]any{"reasoningConfig": map[string]any{"maxReasoningEffort": "high"}}}},
	}
}

func captureEffortWire(t *testing.T, route effortWireRoute, streaming bool, effort string, messages []providers.Message) map[string]any {
	t.Helper()
	captured := make(chan map[string]any, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Errorf("decode capture body: %v", err)
		}
		captured <- b
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"wire-capture-7731"}}`))
	}))
	defer srv.Close()
	var p interface {
		Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error)
	}
	var err error
	switch route.kind {
	case "anthropic":
		p = anthropic.NewProviderWithBaseURL("test-key", srv.URL)
	case "bedrock":
		p, err = bedrock.NewProvider("test-key", bedrock.WithBaseEndpoint(srv.URL))
	default:
		p, err = openai_compat.NewProvider("test-key", srv.URL, "", openai_compat.WithProviderID(route.provider))
	}
	require.NoError(t, err)
	opts := map[string]any{"reasoning_effort": effort, "temperature": 0.7, "max_tokens": 4096}
	if messages == nil {
		messages = []providers.Message{{Role: "user", Content: "wire probe"}}
	}
	if sp, ok := p.(providers.StreamingProvider); streaming && ok {
		_, err = sp.ChatStream(context.Background(), messages, nil, route.model, opts, func(string) {}, nil)
	} else {
		// Codex/Bedrock expose only Chat; their one builder is also used when
		// streaming is requested by the loop (there is no second adapter path).
		_, err = p.Chat(context.Background(), messages, nil, route.model, opts)
	}
	require.Error(t, err, "capture peer deliberately refused the serialized request")
	select {
	case b := <-captured:
		return b
	default:
		t.Fatal("adapter did not reach the recording HTTP peer")
		return nil
	}
}

func assertEffortWire(t *testing.T, body, want map[string]any) {
	t.Helper()
	for _, key := range []string{"reasoning_effort", "reasoning", "thinking", "output_config", "additionalModelRequestFields"} {
		if expected, ok := want[key]; ok {
			assert.Equal(t, expected, body[key], "S-17/S-18 exact %s encoding", key)
		} else {
			assert.NotContains(t, body, key, "no extra effort encoding from another route: %s", key)
		}
	}
}

func TestT_P1_S17_S19_SerializedRouteMatrix(t *testing.T) {
	for _, route := range effortWireRoutes() {
		for _, stream := range []bool{false, true} {
			mode := "nonstream"
			if stream {
				mode = "stream-requested"
			}
			t.Run(route.name+"/"+mode+"/S17_high", func(t *testing.T) {
				body := captureEffortWire(t, route, stream, "high", nil)
				assertEffortWire(t, body, route.high)
				if route.kind == "anthropic" {
					assert.NotContains(t, body, "temperature", "S-23: enabling effort clears temperature")
					if thinking, ok := body["thinking"].(map[string]any); ok {
						assert.NotContains(t, thinking, "display")
					}
				}
			})
			t.Run(route.name+"/"+mode+"/S19_provider_default", func(t *testing.T) {
				body := captureEffortWire(t, route, stream, "provider_default", nil)
				assertEffortWire(t, body, map[string]any{})
				if route.kind == "anthropic" {
					assert.Equal(t, 0.7, body["temperature"], "S-23: Provider default restores temperature")
				}
			})
		}
	}
}

func TestT_P1_S18_SerializedSwitchMatrix(t *testing.T) {
	rows := []struct {
		name, kind, provider, model, value string
		want                               map[string]any
	}{
		{"router on", "compat", "openrouter", "minimax/minimax-m3", "on", map[string]any{"reasoning": map[string]any{"enabled": true}}},
		{"router off", "compat", "openrouter", "minimax/minimax-m3", "off", map[string]any{"reasoning": map[string]any{"enabled": false}}},
		{"router none", "compat", "openrouter", "deepseek/deepseek-v4.1-flash", "none", map[string]any{"reasoning": map[string]any{"effort": "none"}}},
		{"flat none", "compat", "openai", "gpt-6.1-sol", "none", map[string]any{"reasoning_effort": "none"}},
		{"anthropic off", "anthropic", "anthropic", "claude-sonnet-4-6", "off", map[string]any{"thinking": map[string]any{"type": "disabled"}}},
		{"anthropic on", "anthropic", "anthropic", "claude-sonnet-4-6", "on", map[string]any{"thinking": map[string]any{"type": "adaptive"}}},
		{"deepseek off", "compat", "deepseek", "deepseek-v4.1-flash", "off", map[string]any{"thinking": map[string]any{"type": "disabled"}}},
		{"deepseek on", "compat", "deepseek", "deepseek-v4.1-flash", "on", map[string]any{"thinking": map[string]any{"type": "enabled"}}},
		{"zai off", "compat", "z-ai", "glm-5.2", "off", map[string]any{"thinking": map[string]any{"type": "disabled"}}},
		{"zai on", "compat", "z-ai", "glm-5.2", "on", map[string]any{"thinking": map[string]any{"type": "enabled"}}},
	}
	for _, row := range rows {
		for _, stream := range []bool{false, true} {
			mode := "nonstream"
			if stream {
				mode = "stream-requested"
			}
			t.Run(row.name+"/"+mode, func(t *testing.T) {
				assertEffortWire(t, captureEffortWire(t, effortWireRoute{provider: row.provider, kind: row.kind, model: row.model}, stream, row.value, nil), row.want)
			})
		}
	}
}
