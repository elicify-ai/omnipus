package gateway

// Architect ruling sections 4A/4B: genuine external workers carry the normal
// worker policy seed, so that field cannot be refused. Package overrides are
// ignored and the TARGET's standard worker seed is used, without policy-drop
// or policy-tightening warnings. The independent oracle is normal REST create,
// not import's own stored output; no CLI process or model call is started.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackagesS1b_ExternalRoundTripUsesTargetWorkerPolicySeed(t *testing.T) {
	for _, changed := range []bool{false, true} {
		name := "genuine REST-created worker retains target seed"
		if changed {
			name = "edited source policies cannot replace target seed"
		}
		t.Run(name, func(t *testing.T) {
			src := packageAPI(t)
			sourceID := s1bCreateExternalByREST(t, src, "Coder")
			source, err := agentstore.New(src.homePath).ReadState(sourceID)
			require.NoError(t, err)
			require.NotNil(t, source.Agent.Tools)
			require.NotEmpty(t, source.Agent.Tools.Builtin.Policies, "normal create must really store the worker seed")
			out := callPackageExport(t, src, `{"agent_ids":["`+sourceID+`"]}`)
			require.Equal(t, http.StatusOK, out.Code, "genuine external export; body=%s", out.Body)
			_, files := decodePackageBytes(t, out.Body)
			var exported config.AgentConfig
			require.NoError(t, json.Unmarshal(files[agentRecordPath(sourceID)], &exported))
			require.NotNil(t, exported.Tools)
			require.Equal(t, source.Agent.Tools.Builtin.Policies, exported.Tools.Builtin.Policies, "positive control: export carries the real source worker seed")

			dst := packageAPI(t)
			usePackageSeams(t)
			installRosterReload(t, dst, nil)
			targetID := s1bCreateExternalByREST(t, dst, "Target Seed")
			target, err := agentstore.New(dst.homePath).ReadState(targetID)
			require.NoError(t, err)
			require.NotNil(t, target.Agent.Tools)
			want := target.Agent.Tools.Builtin.Policies
			require.NotEmpty(t, want)
			require.Equal(t, config.ToolPolicyAllow, want["search_web"], "standard worker seed permits search_web; package's opposite must not win")
			pkg := out.Body
			if changed {
				pkg = s1bFix2DifferentPackagePolicies(t, pkg, sourceID)
			}
			ws := createWorkspaceViaAPI(t, dst, "Research", "notes")
			cfgBefore := configBytes(t, dst.homePath)
			prev := stageUpload(t, dst, pkg)
			for _, code := range []string{"tool_policy_dropped", "tool_policy_tightened"} {
				assert.NotContains(t, previewWarningCodes(prev), code, "ruling 4B: external policies select the target seed silently")
			}
			got := callPackageApply(t, dst, prev.StagingId, applyBody("new", ws))
			require.Equal(t, http.StatusOK, got.Code, "worker seed must not make a genuine external package non-creatable; body=%s", got.Body)
			res := decodeResult(t, got.Body)
			require.Len(t, res.Created, 1)
			assert.Equal(t, sourceID, res.Created[0].SourceId)
			assert.NotEqual(t, sourceID, res.Created[0].NewId)
			for _, code := range []string{"tool_policy_dropped", "tool_policy_tightened"} {
				assert.NotContains(t, resultWarningCodes(res), code)
			}
			stored, err := agentstore.New(dst.homePath).ReadState(res.Created[0].NewId)
			require.NoError(t, err)
			require.True(t, stored.Agent.IsExternalCLIWorker())
			require.NotNil(t, stored.Agent.Tools)
			assert.Equal(t, want, stored.Agent.Tools.Builtin.Policies, "imported external worker must use the complete target worker seed, not source policy overrides")
			assert.Contains(t, listedAgentIDs(t, dst), res.Created[0].NewId, "real roster reload publishes the imported worker")
			assert.Equal(t, cfgBefore, configBytes(t, dst.homePath))
		})
	}
}

func s1bCreateExternalByREST(t *testing.T, api *restAPI, name string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"name": name, "type": "subagent_3p", "description": "Runs Codex tasks",
		"model": "m-1", "provider": "openrouter", "executor": map[string]any{"cli": "codex"},
	})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "normal external create supplies the genuine worker seed; body=%s", w.Body)
	created := decodeAgentResp(t, w.Body.Bytes())
	require.NotEmpty(t, created.Id)
	return created.Id
}

func s1bFix2DifferentPackagePolicies(t *testing.T, pkg []byte, sourceID string) []byte {
	t.Helper()
	doc, files := decodePackageBytes(t, pkg)
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	var manifest omnpkg.Manifest
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.Len(t, manifest.Elements, 1)
	path := agentRecordPath(sourceID)
	var record map[string]any
	require.NoError(t, json.Unmarshal(files[path], &record))
	tools, ok := record["tools"].(map[string]any)
	require.True(t, ok)
	builtin, ok := tools["builtin"].(map[string]any)
	require.True(t, ok)
	policies, ok := builtin["policies"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "allow", policies["search_web"], "fixture must begin with the real worker seed")
	policies["search_web"] = "deny"
	policies["retired_s1b_tool"] = "allow"
	raw, err = json.Marshal(record)
	require.NoError(t, err)
	files[path] = raw
	manifest.Files[path] = omnpkg.FileInfo{SHA256: sha256hex(raw), Size: int64(len(raw))}
	parts := make([]tarPart, 0, len(manifest.Elements[0].Files))
	for _, path := range manifest.Elements[0].Files {
		parts = append(parts, tarPart{name: path, body: files[path]})
	}
	return s1bArchive(t, &manifest, parts)
}
