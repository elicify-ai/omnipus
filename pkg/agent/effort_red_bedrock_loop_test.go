package agent

// CHECK-06/07 / S-17/S-19/S-35/S-90: actual factory profile resolution,
// adapter, turn loop, filesystem tool and production answer writer. The opaque
// inference-profile id contains neither family name: prefix guessing must fail.
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func installBedrockEffortCatalog(t *testing.T, base string) {
	t.Helper()
	m := map[string]any{"id": base, "name": base, "context_window": 128000, "max_output_tokens": 8192, "input_modalities": []string{"text"}, "tool_call": true, "status": "active", "reasoning": map[string]any{"levels": []string{"low", "medium", "high"}, "switch": false}}
	p := map[string]any{"id": "amazon-bedrock", "name": "Bedrock", "company": "Amazon", "api": "https://bedrock-runtime.us-east-1.amazonaws.com", "protocol": "bedrock", "region": "us-east-1", "regions": []any{map[string]any{"id": "us-east-1", "group": "us"}}, "tier": "standard", "auth_methods": []string{"api_key"}, "aliases": []string{"bedrock"}, "models": []any{m}}
	raw, err := json.Marshal(map[string]any{"schema_version": "2.0.0", "version": "v2026.10.31", "updated_at": "2026-10-31T00:00:00Z", "source": "test", "default_resize_limits": map[string]any{"long_edge_px": 2048, "max_bytes": 10485760}, "providers": []any{p}})
	require.NoError(t, err)
	cat, err := catalog.NewCatalog(raw)
	require.NoError(t, err)
	old := providers.ProviderCatalog()
	providers.SetCatalog(cat)
	t.Cleanup(func() { providers.SetCatalog(old) })
}

func TestT_P5_S17_S19_S35_S90_BedrockBaseFamilyAndActualLoop(t *testing.T) {
	rows := []struct {
		base, choice string
		tool         bool
		fields       map[string]any
		reason       string
	}{
		{"anthropic.claude-sonnet-4-6-v1:0", "high", true, map[string]any{"thinking": map[string]any{"type": "adaptive"}, "output_config": map[string]any{"effort": "high"}}, "continuation"},
		{"anthropic.claude-sonnet-4-6-v1:0", "off", true, nil, "not_supported"},
		{"amazon.nova-pro-v1:0", "high", false, map[string]any{"reasoningConfig": map[string]any{"maxReasoningEffort": "high"}}, ""},
		{"amazon.titan-text-v1", "high", false, nil, "not_supported"},
		{"anthropic.claude-sonnet-4-6-v1:0", "provider_default", false, nil, ""},
		{"amazon.nova-pro-v1:0", "provider_default", false, nil, ""},
		{"amazon.titan-text-v1", "provider_default", false, nil, ""},
	}
	for _, row := range rows {
		t.Run(row.base+"/"+row.choice, func(t *testing.T) {
			installBedrockEffortCatalog(t, row.base)
			probe := filepath.Join(t.TempDir(), "bedrock-user-file.txt")
			require.NoError(t, os.WriteFile(probe, []byte("BEDROCK-REAL-TOOL-7731"), 0600))
			var mu sync.Mutex
			bodies := []map[string]any{}
			paths := []string{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("request JSON: %v", err)
					return
				}
				mu.Lock()
				bodies = append(bodies, body)
				paths = append(paths, r.URL.Path)
				n := len(bodies)
				mu.Unlock()
				content := []any{map[string]any{"text": "ok"}}
				stop := "end_turn"
				if row.tool && n == 1 {
					content = []any{map[string]any{"toolUse": map[string]any{"toolUseId": "bedrock-read-7731", "name": "read_file", "input": map[string]any{"path": probe}}}}
					stop = "tool_use"
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{"output": map[string]any{"message": map[string]any{"role": "assistant", "content": content}}, "stopReason": stop, "usage": map[string]any{"inputTokens": 2, "outputTokens": 3, "totalTokens": 5}}); err != nil {
					t.Errorf("response JSON: %v", err)
				}
			}))
			defer peer.Close()
			home := t.TempDir()
			t.Setenv("OMNIPUS_HOME", home)
			profile := "arn:aws:bedrock:us-east-1:123456789012:application-inference-profile/opaque-profile-7731"
			t.Setenv("EFFORT_BEDROCK_TEST_KEY", "test-placeholder")
			raw, err := json.Marshal(map[string]any{"provider": "amazon-bedrock", "model": row.base, "api_base": peer.URL, "api_key_ref": "EFFORT_BEDROCK_TEST_KEY", "region": "us-east-1", "bedrock_inference_profiles": map[string]string{row.base: profile}})
			require.NoError(t, err)
			var mc config.ModelConfig
			require.NoError(t, json.Unmarshal(raw, &mc))
			p, resolved, err := providers.CreateProviderFromConfig(&mc)
			require.NoError(t, err)
			require.Equal(t, profile, resolved, "fixture positive control: actual factory uses opaque profile id")
			raw, err = json.Marshal(map[string]any{"id": testDefaultAgentID, "provider": "amazon-bedrock", "model": row.base, "reasoning_effort": row.choice})
			require.NoError(t, err)
			var entity config.AgentConfig
			require.NoError(t, json.Unmarshal(raw, &entity))
			window := 128000
			entity.ContextWindowOverride = &window
			cfg := &config.Config{}
			cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
			cfg.Agents.List = []config.AgentConfig{entity}
			cfg.Providers = []*config.ModelConfig{&mc}
			cfg.Sandbox.ToolPolicies = config.DefaultToolPolicyCeiling()
			al, store, _ := newContainmentLoop(t, cfg, p)
			ag, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
			require.True(t, ok)
			ag.Tools.RegisterReplacing(tools.NewReadFileTool(filepath.Dir(probe), false, 0))
			answer, _, err := al.processMessageAsPrimaryForTest(context.Background(), containmentTurn("read the actual file then answer"))
			require.NoError(t, err)
			require.Equal(t, "ok", answer)
			mu.Lock()
			first := append([]map[string]any(nil), bodies...)
			urls := append([]string(nil), paths...)
			mu.Unlock()
			count := 1
			if row.tool {
				count = 2
			}
			require.Len(t, first, count)
			for _, path := range urls {
				assert.Equal(t, "/model/"+profile+"/converse", path, "the runtime id is opaque, not a family hint")
			}
			if row.fields == nil {
				assert.NotContains(t, first[0], "additionalModelRequestFields")
			} else {
				assert.Equal(t, row.fields, first[0]["additionalModelRequestFields"], "S-17 uses the catalog BASE family even when runtime id is opaque")
			}
			if row.tool {
				raw, err := json.Marshal(first[1]["messages"])
				require.NoError(t, err)
				require.Contains(t, string(raw), "BEDROCK-REAL-TOOL-7731")
				assert.NotContains(t, first[1], "additionalModelRequestFields", "current-loop guard removes enabling fields; Off was already mapped out")
			}
			saved := fullEffortAnswer(t, store)
			record, ok := saved["effort"].(map[string]any)
			assert.True(t, ok, "loop-generated effort record is missing; keep testing the next real user turn")
			assert.Equal(t, row.choice, record["chosen"])
			if row.reason != "" {
				assert.Equal(t, row.reason, record["reason"])
				assert.NotContains(t, record, "sent")
			} else if row.choice == "provider_default" {
				assert.Equal(t, map[string]any{"chosen": "provider_default"}, record)
			} else {
				assert.Equal(t, "high", record["sent"])
				assert.NotContains(t, record, "reason")
			}
			if row.tool {
				_, _, err := al.processMessageAsPrimaryForTest(context.Background(), containmentTurn("new user turn after completed loop"))
				require.NoError(t, err)
				mu.Lock()
				later := append([]map[string]any(nil), bodies...)
				mu.Unlock()
				require.Len(t, later, 3)
				if row.choice == "high" {
					assert.Equal(t, row.fields, later[2]["additionalModelRequestFields"], fmt.Sprintf("completed loop must not guard new turn on %s", row.base))
				} else {
					assert.NotContains(t, later[2], "additionalModelRequestFields")
				}
			}
		})
	}
}
