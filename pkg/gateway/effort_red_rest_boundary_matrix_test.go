package gateway

// S-94 / FR-060: every non-onboarding REST writer and both nested fallback
// shapes. Onboarding's two modes/variants are covered by its separate matrix.
// Real handlers and durable files; no callback stands in for persistence.
import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type effortRESTBoundary struct {
	api          *restAPI
	body, member map[string]any
	key          string
	success      int
	send         func(string) *httptest.ResponseRecorder
	read         func(*httptest.ResponseRecorder) any
}

func effortRESTSnapshot(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range []string{filepath.Join(home, "config.json"), filepath.Join(home, "entities", "agents")} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if !d.IsDir() && filepath.Ext(path) == ".json" {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				out[path] = string(data)
			}
			return nil
		})
		require.NoError(t, err)
	}
	require.NotEmpty(t, out, "instrument control: snapshot sees existing durable config/entities")
	return out
}

func requireEffortMemorySchema(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code == http.StatusInternalServerError && strings.Contains(w.Body.String(), "inbound schema unavailable") {
		t.Fatal("BLOCKED: UpdateMemorySettingsJSONRequestBody inbound schema not implemented — required by S-81/S-94 validation-on writers; only MemorySettings.yaml is embedded")
	}
}

func newEffortRESTBoundary(t *testing.T, kind string, validate bool) *effortRESTBoundary {
	t.Helper()
	f := &effortRESTBoundary{key: "reasoning_effort", success: http.StatusOK}
	switch kind {
	case "agent-create":
		f.api = newTestRestAPIWithHome(t)
		f.body = map[string]any{"type": "Main", "name": "Effort REST Boundary", "model": "claude-sonnet-4-6", "soul": "A test agent for the real effort writer boundary."}
		f.success = http.StatusCreated
		f.send = func(body string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			f.api.HandleAgents(w, r)
			return w
		}
		f.read = func(w *httptest.ResponseRecorder) any {
			var payload map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
			id, ok := payload["id"].(string)
			require.True(t, ok)
			return egwReadAgentEntity(t, f.api, id)[f.key]
		}
	case "agent-update", "agent-fallback":
		f.api = buildExecutorTestAPI(t)
		f.body = map[string]any{"description": "valid sibling saves only on success"}
		f.send = func(body string) *httptest.ResponseRecorder { return egwPutAgentField(t, f.api, body) }
		f.read = func(*httptest.ResponseRecorder) any { return egwReadAgentEntity(t, f.api, "test-agent")[f.key] }
		if kind == "agent-fallback" {
			f.member = map[string]any{"provider": "openrouter", "model": "m1"}
			f.body["fallback_models"] = []any{f.member}
			f.read = func(*httptest.ResponseRecorder) any {
				rows := egwReadAgentEntity(t, f.api, "test-agent")["fallback_models"].([]any)
				require.Len(t, rows, 1)
				return rows[0].(map[string]any)["reasoning_effort"]
			}
		}
	case "default-model":
		rows := []*config.ModelConfig{{Provider: "p1", Model: "m1", APIBase: "https://a.example/v1", Protocol: "openai-compatible"}, {Provider: "p2", Model: "m2", APIBase: "https://b.example/v1", Protocol: "openai-compatible"}}
		f.api, _, _ = newDefaultModelAPI(t, config.DefaultModel{Provider: "p1", Model: "m1"}, rows, &recordingLLMProvider{})
		f.body = map[string]any{"provider": "p2", "model": "m2"}
		f.send = func(body string) *httptest.ResponseRecorder { return doDefaultModel(f.api, http.MethodPut, body) }
		f.read = func(*httptest.ResponseRecorder) any { return egwDiskDefaultModel(t, f.api.homePath)[f.key] }
	case "memory-primary", "memory-fallback":
		f.api = egwNewMemoryAPI(t, validate)
		f.body = map[string]any{"recap_model": "deepseek/deepseek-v4.1-flash", "idle_timeout_minutes": 10}
		f.key = "recap_reasoning_effort"
		f.send = func(body string) *httptest.ResponseRecorder { return egwMemoryPut(t, f.api, body) }
		f.read = func(*httptest.ResponseRecorder) any { return egwDefaultsOf(t, diskConfig(t, f.api.homePath))[f.key] }
		if kind == "memory-fallback" {
			f.key = "reasoning_effort"
			f.member = map[string]any{"provider": "openrouter", "model": "m1"}
			f.body["recap_fallback_models"] = []any{f.member}
			f.read = func(*httptest.ResponseRecorder) any {
				rows := egwDefaultsOf(t, diskConfig(t, f.api.homePath))["recap_fallback_models"].([]any)
				require.Len(t, rows, 1)
				return rows[0].(map[string]any)["reasoning_effort"]
			}
		}
	case "provider-delete-new-default":
		cfg := providerDeleteBaseConfig(config.DefaultModel{Provider: "openrouter", Model: "z-ai/glm-5"}, []*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()})
		f.api, _, _ = newProviderDeleteAPI(t, cfg)
		f.member = map[string]any{"provider": "anthropic", "model": "claude-sonnet-4.6"}
		f.body = map[string]any{"new_default": f.member}
		f.send = func(body string) *httptest.ResponseRecorder {
			return doProviderDelete(t, f.api, "openrouter", body, nil, true)
		}
		f.read = func(*httptest.ResponseRecorder) any { return egwDiskDefaultModel(t, f.api.homePath)[f.key] }
	default:
		t.Fatalf("invalid boundary fixture: %s", kind)
	}
	if f.member == nil {
		f.member = f.body
	}
	f.api.agentLoop.GetConfig().Gateway.ValidateInbound = validate
	return f
}

func TestT_GW_S94_OmissionKeepsAndEmptyClearsStoredOverride(t *testing.T) {
	for _, kind := range []string{"agent-update", "agent-fallback", "default-model", "memory-primary", "memory-fallback"} {
		for _, validate := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/validate=%v", kind, validate), func(t *testing.T) {
				f := newEffortRESTBoundary(t, kind, validate)
				write := func() *httptest.ResponseRecorder {
					raw, err := json.Marshal(f.body)
					require.NoError(t, err)
					w := f.send(string(raw))
					if strings.HasPrefix(kind, "memory-") {
						requireEffortMemorySchema(t, w)
					}
					require.Equal(t, f.success, w.Code, "actual durable seed/change: %s", w.Body.String())
					return w
				}
				f.member[f.key] = "high"
				assert.Equal(t, "high", f.read(write()), "nonempty stored given established through the actual writer")
				delete(f.member, f.key)
				assert.Equal(t, "high", f.read(write()), "omission on the unchanged pair retains its stored override")
				f.member[f.key] = ""
				got := f.read(write())
				assert.True(t, got == nil || got == "", "empty clears the previously stored High, not an already empty slot")
			})
		}
	}
}

func TestT_GW_S94_RESTNullEmptyOmittedAndLengthMatrix(t *testing.T) {
	kinds := []string{"agent-create", "agent-update", "agent-fallback", "default-model", "memory-primary", "memory-fallback", "provider-delete-new-default"}
	inputs := []struct {
		name    string
		present bool
		value   any
		bad     bool
	}{
		{"omitted-control", false, nil, false}, {"clear", true, "", false}, {"high", true, "high", false}, {"max32", true, strings.Repeat("x", 32), false}, {"null", true, nil, true}, {"number", true, 7, true}, {"over32", true, strings.Repeat("x", 33), true},
	}
	for _, kind := range kinds {
		for _, validate := range []bool{false, true} {
			for _, row := range inputs {
				t.Run(fmt.Sprintf("%s/validate=%v/%s", kind, validate, row.name), func(t *testing.T) {
					f := newEffortRESTBoundary(t, kind, validate)
					if row.present {
						f.member[f.key] = row.value
					}
					raw, err := json.Marshal(f.body)
					require.NoError(t, err)
					before := effortRESTSnapshot(t, f.api.homePath)
					w := f.send(string(raw))
					if strings.HasPrefix(kind, "memory-") {
						requireEffortMemorySchema(t, w)
					}
					if row.bad {
						assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
						if !validate && (row.name == "null" || row.name == "over32") {
							var payload map[string]any
							require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
							want := `reasoning_effort must be a string; send "" to clear it`
							if row.name == "over32" {
								want = "reasoning_effort must be at most 32 characters"
							}
							assert.Equal(t, want, payload["error"], "unknown-field refusal is not the new effort validation")
						} else {
							assert.Contains(t, w.Body.String(), f.key)
						}
						assert.Equal(t, before, effortRESTSnapshot(t, f.api.homePath), "refusal commits neither effort nor valid siblings/provider removal")
						return
					}
					require.Equal(t, f.success, w.Code, "valid writer instrument/effort input: %s", w.Body.String())
					value := f.read(w)
					if !row.present || row.value == "" {
						assert.True(t, value == nil || value == "", "Not set remains absent/empty")
					} else {
						assert.Equal(t, row.value, value, "exact durable value; REST does not catalog-validate")
					}
				})
			}
		}
	}
}
