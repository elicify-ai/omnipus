package agent

// CHECK-05/CHECK-15: real command/turn/store state, not another mapper call or
// a registry rebuild around a retained Runtime. Only the provider HTTP edge is
// scripted; the catalog parser, registry, command runtime and stores are real.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func installConversationCatalog(t *testing.T, levels []string, sw bool, reasons bool, ids ...string) {
	t.Helper()
	model := map[string]any{"id": "primary-m", "name": "primary-m", "context_window": 128000, "max_output_tokens": 8192, "input_modalities": []string{"text"}, "tool_call": true, "status": "active"}
	if reasons {
		model["reasoning"] = map[string]any{"levels": levels, "switch": sw}
	}
	models := make([]any, 0, 1+len(ids))
	models = append(models, model)
	for _, id := range ids {
		cloned := map[string]any{}
		for k, v := range model {
			cloned[k] = v
		}
		cloned["id"] = id
		cloned["name"] = id
		models = append(models, cloned)
	}
	doc, err := json.Marshal(map[string]any{"schema_version": "2.0.0", "version": "v2026.10.31", "updated_at": "2026-10-31T00:00:00Z", "source": "test", "default_resize_limits": map[string]any{"long_edge_px": 2048, "max_bytes": 10485760}, "providers": []any{map[string]any{"id": "openrouter", "name": "OpenRouter", "company": "OpenRouter", "api": "https://openrouter.ai/api/v1", "protocol": "openai-compatible", "protocols": []any{map[string]any{"protocol": "openai-compatible", "api": "https://openrouter.ai/api/v1"}}, "tier": "popular", "auth_methods": []string{"api_key"}, "aliases": []string{}, "models": models}}})
	require.NoError(t, err)
	cat, err := catalog.NewCatalog(doc)
	require.NoError(t, err)
	old := providers.ProviderCatalog()
	providers.SetCatalog(cat)
	t.Cleanup(func() { providers.SetCatalog(old) })
}
func conversationValue(t *testing.T, store *session.UnifiedStore, sid string) any {
	t.Helper()
	meta, err := store.GetMeta(sid)
	require.NoError(t, err)
	raw, err := json.Marshal(meta)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m["reasoning_effort"]
}
func runConversationCommand(t *testing.T, al *AgentLoop, sid, text string) string {
	t.Helper()
	m := containmentTurn(text)
	m.Channel = "telegram"
	m.ChatID = sid
	m.SessionID = sid
	m.SessionKey = "agent:" + testDefaultAgentID + ":session:" + sid
	answer, _, err := al.processMessageAsPrimaryForTest(context.Background(), m)
	require.NoError(t, err)
	return answer
}

func TestT_CMD_S36_RealRuntimeCapabilityStatusAndRefusals(t *testing.T) {
	rows := []struct {
		name                     string
		levels                   []string
		sw, reasons              bool
		seed, input, want, after string
	}{
		{"status", []string{"none", "low", "medium", "high"}, false, true, "high", "/effort", "Effort for primary-m: High. Options: Provider default, Off, low, medium, high. /effort reset returns to the agent's setting.", "high"},
		{"unoffered", []string{"low", "medium", "high"}, false, true, "high", "/effort max", "primary-m doesn't offer max. Options: Provider default, low, medium, high. Nothing changed.", "high"},
		{"switch only", []string{}, true, true, "off", "/effort high", "primary-m only switches reasoning on or off. Use /effort on, /effort off or /effort default.", "off"},
		{"muted", []string{}, false, true, "default", "/effort low", "Omnipus can't change effort for primary-m.", "provider_default"},
		{"does not reason", nil, false, false, "default", "/effort low", "primary-m doesn't support reasoning effort.", "provider_default"},
		{"no off", []string{"low", "medium", "high"}, false, true, "high", "/effort off", "primary-m can't turn reasoning off.", "high"},
		{"default", []string{"low", "medium", "high"}, false, true, "high", "/effort DEFAULT", "Effort for primary-m: Provider default (the provider decides).", "provider_default"},
		{"reset", []string{"low", "medium", "high"}, false, true, "high", "/effort reset", "Effort for primary-m: back to the agent's setting (Medium).", ""},
		{"invalid33", []string{"low", "medium", "high"}, false, true, "high", "/effort " + strings.Repeat("x", 33), "That isn't a valid effort level.", "high"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			installConversationCatalog(t, row.levels, row.sw, row.reasons)
			ep, url := newRejectionEndpoint(t, 0, 0)
			al, store := fullEffortRequestLoop(t, url, "medium", nil)
			sid := containmentSessionID
			path := filepath.Join(config.OmnipusHomeDir(), "config.json")
			require.NoError(t, config.SaveConfig(path, al.GetConfig()))
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			seeded := runConversationCommand(t, al, sid, "/effort "+row.seed)
			require.NotEqual(t, "ok", seeded, "the command must not fall through to the model")
			reply := runConversationCommand(t, al, sid, row.input)
			assert.Equal(t, row.want, reply)
			got := conversationValue(t, store, sid)
			if row.after == "" {
				assert.True(t, got == nil || got == "", "reset removes only the conversation override")
			} else {
				assert.Equal(t, row.after, got)
			}
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after, "commands do not write agent-wide config")
			assert.Empty(t, ep.snapshot(), "status and refusal commands never invoke the LLM")
		})
	}
}

func TestT_CMD_S37_S38_S39_RealConversationScopeServingModelAndRestart(t *testing.T) {
	installConversationCatalog(t, []string{"none", "low", "medium", "high"}, false, true)
	ep, url := newRejectionEndpoint(t, 0, 0)
	al, store := fullEffortRequestLoop(t, url, "medium", nil)
	sid := containmentSessionID
	other, err := store.NewSession(session.SessionTypeChat, "telegram", testDefaultAgentID)
	require.NoError(t, err)
	path := filepath.Join(config.OmnipusHomeDir(), "config.json")
	require.NoError(t, config.SaveConfig(path, al.GetConfig()))
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	reply := runConversationCommand(t, al, sid, "/effort high")
	require.NotEqual(t, "ok", reply, "registered command must handle before provider invocation")
	assert.Equal(t, "high", conversationValue(t, store, sid))
	assert.Nil(t, conversationValue(t, store, other.ID))
	require.Equal(t, "ok", runConversationCommand(t, al, sid, "first answer"))
	first := ep.snapshot()
	require.Len(t, first, 1)
	assertOpenRouterLevelBody(t, first[0], "high")
	require.Equal(t, "ok", runConversationCommand(t, al, other.ID, "other answer"))
	both := ep.snapshot()
	require.Len(t, both, 2)
	assertOpenRouterLevelBody(t, both[1], "medium") // conversation B keeps its inherited value
	// Real runtime and store replacement before the next serving request.
	fresh, freshStore := restartContainmentLoop(t, al, &effortScriptedProvider{content: "UNEXPECTED-FALLBACK", finish: "stop"})
	require.Equal(t, "ok", runConversationCommand(t, fresh, sid, "after restart"))
	bodies := ep.snapshot()
	require.Len(t, bodies, 3)
	assertOpenRouterLevelBody(t, bodies[2], "high")
	assert.Equal(t, "high", conversationValue(t, freshStore, sid))
	// The serving model's capabilities change; the stored conversation High is
	// intentionally NOT rewritten. The next real request maps it to Medium.
	installConversationCatalog(t, []string{"low", "medium"}, false, true, "changed-m")
	changedCfg := fresh.GetConfig()
	changedCfg.Providers = append(changedCfg.Providers, &config.ModelConfig{Provider: "openrouter", Model: "changed-m", APIBase: url})
	changedCfg.Agents.List[0].Model = &config.AgentModelConfig{Primary: "changed-m", Provider: "openrouter"}
	require.NoError(t, fresh.ReloadProviderAndConfig(context.Background(), &effortScriptedProvider{content: "UNEXPECTED-FALLBACK", finish: "stop"}, changedCfg))
	require.Equal(t, "ok", runConversationCommand(t, fresh, sid, "model now offers low medium"))
	bodies = ep.snapshot()
	require.Len(t, bodies, 4)
	assertOpenRouterLevelBody(t, bodies[3], "medium")
	assert.Equal(t, "high", conversationValue(t, freshStore, sid))
	entries, err := freshStore.ReadTranscript(sid)
	require.NoError(t, err)
	var answer map[string]any
	for _, entry := range entries {
		if entry.Content == "ok" {
			answer = entryJSON(t, entry)
		}
	}
	require.NotNil(t, answer)
	assert.Equal(t, map[string]any{"chosen": "high", "sent": "medium", "reason": "mapped", "provider": "openrouter", "model": "changed-m"}, answer["effort"])
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after, "neither command nor request mapping writes agent config")
}

func TestT_S2_S87_InheritedHighChangedToOffAtRuntime(t *testing.T) {
	installConversationCatalog(t, []string{"low", "medium", "high"}, false, true)
	ep, url := newRejectionEndpoint(t, 0, 0)
	al, store := fullEffortRequestLoop(t, url, "high", nil)
	sid := containmentSessionID
	require.Equal(t, "ok", runConversationCommand(t, al, sid, "inherited high"))
	assert.Nil(t, conversationValue(t, store, sid), "Not set really is unset")
	bodies := ep.snapshot()
	require.Len(t, bodies, 1)
	assertOpenRouterLevelBody(t, bodies[0], "high")
	cfg := al.GetConfig()
	var replacement config.AgentConfig
	raw, err := json.Marshal(cfg.Agents.List[0])
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(raw, &fields))
	fields["reasoning_effort"] = "off"
	raw, err = json.Marshal(fields)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &replacement))
	cfg.Agents.List[0] = replacement
	require.NoError(t, al.ReloadProviderAndConfig(context.Background(), &effortScriptedProvider{content: "UNEXPECTED-FALLBACK", finish: "stop"}, cfg))
	require.Equal(t, "ok", runConversationCommand(t, al, sid, "agent changed elsewhere"))
	bodies = ep.snapshot()
	require.Len(t, bodies, 2)
	assert.False(t, bodyHasEffort(bodies[1]))
	assert.Nil(t, conversationValue(t, store, sid))
	entries, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	var answer map[string]any
	for _, entry := range entries {
		if entry.Content == "ok" {
			answer = entryJSON(t, entry)
		}
	}
	require.NotNil(t, answer)
	assert.Equal(t, map[string]any{"chosen": "off", "reason": "not_supported", "provider": "openrouter", "model": "primary-m"}, answer["effort"])
}
