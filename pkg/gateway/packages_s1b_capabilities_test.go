package gateway

// Native control for architect ruling 4A. Run as a subcase of the existing
// external-runtime test so its real skill fixture and mapping rules are checked
// in the same narrow run as the new external refusals.

import (
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func s1bFix2NativeCapabilities(t *testing.T) {
	t.Helper()
	api := packageAPI(t)
	usePackageSeams(t)
	installRosterReload(t, api, nil)
	installed := api.installedSkillIDs()
	require.Contains(t, installed, "writing", "the native control must really bind an installed skill")
	require.NotContains(t, installed, "missing-on-target")
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	rec := mainRecord(specSourceID, "Writer", "Drafts")
	rec["skills"] = []string{"writing", "missing-on-target"}
	rec["fallback_models"] = []any{map[string]any{"model": "m-fb", "provider": "openrouter"}}
	rec["model_params"] = map[string]any{"temperature": 0.25, "max_tokens": 2048}
	rec["reasoning_effort"], rec["voice"] = "high", "alloy"
	rec["memory_enabled"], rec["context_window_override"] = false, 64000
	rec["tools"] = map[string]any{"mcp": map[string]any{"servers": []any{map[string]any{"id": "github"}}}}
	before := snapshotHome(t, api.homePath)
	prev := stageUpload(t, api, buildAgentPackage(t, specSourceID, "Writer", rec, []byte("Answer in rhyme."), nil, 0))
	requireHomeUnchangedExcept(t, before, snapshotHome(t, api.homePath))
	assert.Contains(t, previewWarningCodes(prev), "skill_bound_local")
	assert.Contains(t, previewWarningCodes(prev), "skill_dropped", "native missing skill still drops normally")
	assert.Contains(t, previewWarningCodes(prev), "mcp_binding_dropped", "native connector mapping remains unchanged")
	got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
	require.Equal(t, http.StatusOK, got.Code, "native capabilities must remain supported; body=%s", got.Body)
	res := decodeResult(t, got.Body)
	require.Len(t, res.Created, 1)
	stored, err := agentstore.New(api.homePath).ReadState(res.Created[0].NewId)
	require.NoError(t, err)
	assert.Equal(t, config.AgentTypeCustom, stored.Agent.Type)
	assert.Equal(t, []string{"writing"}, stored.Agent.Skills)
	assert.Equal(t, config.FallbackModelSlice{{Model: "m-fb", Provider: "openrouter"}}, stored.Agent.FallbackModels)
	assert.Equal(t, "high", stored.Agent.ReasoningEffort)
	require.NotNil(t, stored.Agent.ModelParams)
	require.NotNil(t, stored.Agent.ModelParams.Temperature)
	assert.Equal(t, 0.25, *stored.Agent.ModelParams.Temperature)
	require.NotNil(t, stored.Agent.ModelParams.MaxTokens)
	assert.Equal(t, 2048, *stored.Agent.ModelParams.MaxTokens)
	assert.Equal(t, "alloy", stored.Agent.Voice)
	require.NotNil(t, stored.Agent.MemoryEnabled)
	assert.False(t, *stored.Agent.MemoryEnabled)
	require.NotNil(t, stored.Agent.ContextWindowOverride)
	assert.Equal(t, 64000, *stored.Agent.ContextWindowOverride)
}
