// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// providerRemovalState snapshots both the live config and every persisted agent
// record, not just the agent model fields. Hashes keep even failure diffs from
// printing credential material. The oracle is the unchanged-state invariant in
// issue #1620 / ADR-068 FR-011, not an expected value derived from the handler.
func providerRemovalState(t *testing.T, api *restAPI) map[string][sha256.Size]byte {
	t.Helper()
	live, err := json.Marshal(api.agentLoop.GetConfig())
	require.NoError(t, err)
	state := map[string][sha256.Size]byte{"live-config": sha256.Sum256(live)}
	paths, err := filepath.Glob(filepath.Join(api.homePath, "entities", "agents", "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, paths, "the unchanged-agent check must observe real entity files")
	paths = append(paths, api.configPath())
	for _, path := range paths {
		raw, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		rel, relErr := filepath.Rel(api.homePath, path)
		require.NoError(t, relErr)
		state[filepath.ToSlash(rel)] = sha256.Sum256(raw)
	}
	return state
}

// TestDeleteProvider_DefaultWithoutReplacementPreservesAllState locks #1620 T1:
// HTTP 409 and the exact error come from ADR-068's Machine-Verifiable
// Constraints; both a two-provider roster and the one-provider boundary must
// refuse before touching any default, provider or agent record.
func TestDeleteProvider_DefaultWithoutReplacementPreservesAllState(t *testing.T) {
	for _, onlyProvider := range []bool{false, true} {
		name := "another_provider_exists"
		if onlyProvider {
			name = "only_provider"
		}
		t.Run(name, func(t *testing.T) {
			rows := []*config.ModelConfig{deleteTestOpenrouterRow()}
			wantIDs := []string{"openrouter"}
			if !onlyProvider {
				rows = append(rows, deleteTestAnthropicRow())
				wantIDs = append(wantIDs, "anthropic")
			}
			pair := config.DefaultModel{Provider: "openrouter", Model: "z-ai/glm-5"}
			cfg := providerDeleteBaseConfig(pair, rows,
				config.AgentConfig{ID: "scout", Name: "Scout",
					Model: &config.AgentModelConfig{Provider: "openrouter", Primary: "z-ai/glm-5"}},
				config.AgentConfig{ID: "writer", Name: "Writer",
					FallbackModels: config.FallbackModelSlice{{Provider: "openrouter", Model: "z-ai/glm-5"}}},
			)
			api, home, auditDir := newProviderDeleteAPI(t, cfg)
			before := providerRemovalState(t, api)
			require.Contains(t, before, "entities/agents/scout.json")
			require.Contains(t, before, "entities/agents/writer.json")
			require.Equal(t, wantIDs, diskProviderIDs(t, home))
			require.Equal(t, pair, api.agentLoop.GetConfig().Agents.Defaults.DefaultModel)

			w := doProviderDelete(t, api, "openrouter", "", nil, true)

			assert.Equal(t, http.StatusConflict, w.Code, "default provider must not be removed without a replacement")
			var failure gen.ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &failure))
			assert.Equal(t, "provider backs the default model; supply new_default", failure.Error)
			assert.Equal(t, wantIDs, diskProviderIDs(t, home), "every provider row must survive the refusal")
			assert.Equal(t, pair, api.agentLoop.GetConfig().Agents.Defaults.DefaultModel, "the entire default pair must survive")
			assert.Equal(t, before, providerRemovalState(t, api), "config and every agent record must remain byte-identical")
			assert.Equal(t, 0, countAuditEvents(t, auditDir, "provider.deleted"), "a refusal must not audit a completed removal")
		})
	}
}

// TestDeleteProvider_InvalidReplacementPreservesAllState is #1620 T1b, the
// chief's 2026-11-05 addendum: a replacement must name a DIFFERENT, EXISTING
// provider WITH a model. Error assertions pin those facts, not incidental prose.
func TestDeleteProvider_InvalidReplacementPreservesAllState(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantError string
	}{
		{"missing_model", `{"new_default":{"provider":"anthropic"}}`, `(?i)model.*required`},
		{"empty_model", `{"new_default":{"provider":"anthropic","model":""}}`, `(?i)model.*required`},
		{"same_provider", `{"new_default":{"provider":"openrouter","model":"z-ai/glm-5"}}`, `(?i)different.*provider`},
		{"unknown_provider", `{"new_default":{"provider":"not-configured","model":"replacement-model"}}`, `(?i)provider.*not configured`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pair := config.DefaultModel{Provider: "openrouter", Model: "z-ai/glm-5"}
			cfg := providerDeleteBaseConfig(pair,
				[]*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()},
				config.AgentConfig{ID: "scout", Name: "Scout",
					Model: &config.AgentModelConfig{Provider: "openrouter", Primary: "z-ai/glm-5"}},
			)
			api, home, _ := newProviderDeleteAPI(t, cfg)
			before := providerRemovalState(t, api)
			require.Contains(t, before, "entities/agents/scout.json")

			w := doProviderDelete(t, api, "openrouter", tc.body, nil, true)

			assert.Equal(t, http.StatusBadRequest, w.Code, "an invalid new_default cannot authorize removal")
			var failure gen.ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &failure))
			assert.Regexp(t, tc.wantError, failure.Error, "the refusal must explain the invalid replacement")
			require.NotNil(t, failure.Field, "replacement validation must identify its field")
			assert.Equal(t, "new_default", *failure.Field)
			assert.Equal(t, []string{"openrouter", "anthropic"}, diskProviderIDs(t, home))
			assert.Equal(t, pair, api.agentLoop.GetConfig().Agents.Defaults.DefaultModel)
			assert.Equal(t, before, providerRemovalState(t, api), "invalid replacement must not touch config or any agent")
		})
	}
}
