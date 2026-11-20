package gateway

// S1b T-9. Oracles: export-import-s1-spec.md US-1 AS-3; Field mapping;
// BDD external worker without a program / hand-made executor values /
// unsupported runtime fields; FR-010/012/014/020/021/031/033/035/041/043/047.
// All HTTP calls use registered routes, real persistence and the wired roster
// reload. No external CLI process or model call is started.

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackagesS1b_ExternalRoundTripCarriesSecretNamesNotValues(t *testing.T) {
	src := packageAPI(t)
	seedListedAgent(t, src, config.AgentConfig{
		ID: specSourceID, Name: "Coder", Type: config.AgentTypeWorker, Description: "Runs Codex tasks",
		Model:             &config.AgentModelConfig{Primary: "m-1", Provider: "openrouter"},
		MaxToolIterations: 40, Figure: "Robot", Role: "developer", Color: "#6E8BA8",
		Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{
			Kind: config.ExecutorKindExternalCLI, CLI: "codex", CLIPath: "/source-host/private/codex",
			CLIArgs: "--key sk-TEST-123", EnvOverrides: map[string]string{"OPENAI_API_KEY": "sk-TEST-456"},
		}},
	})
	got := callPackageExport(t, src, `{"agent_ids":["`+specSourceID+`"]}`)
	require.Equal(t, http.StatusOK, got.Code, "FR-010: S1b exports external workers; body=%s", got.Body)
	require.Contains(t, got.Hdr.Get("Content-Disposition"), ".omnpkg")
	manifest, files := decodePackageBytes(t, got.Body)
	wantSecrets := []any{map[string]any{"name": "OPENAI_API_KEY", "used_by": specSourceID, "kind": "executor_env"}}
	require.Equal(t, wantSecrets, manifest["needs_secrets"], "FR-014 manifest carries names, not values")
	assert.NotContains(t, files, agentSoulPath(specSourceID), "external workers have no native prompt")
	for _, value := range []string{"sk-TEST-123", "sk-TEST-456", "/source-host/private/codex"} {
		assert.NotContains(t, string(got.Body), value, "compressed package")
		assert.NotContains(t, string(decompressZstd(t, got.Body)), value, "decompressed package")
	}

	dst := packageAPI(t)
	usePackageSeams(t)
	installRosterReload(t, dst, nil)
	ws := createWorkspaceViaAPI(t, dst, "Research", "notes")
	before := snapshotHome(t, dst.homePath)
	prev := stageUpload(t, dst, got.Body)
	requireHomeUnchangedExcept(t, before, snapshotHome(t, dst.homePath))
	require.Len(t, prev.NeedsSecrets, 1, "FR-021 preview carries the manifest's complete needs_secrets")
	assert.Equal(t, "OPENAI_API_KEY", prev.NeedsSecrets[0].Name)
	assert.Equal(t, specSourceID, prev.NeedsSecrets[0].UsedBy)
	assert.Equal(t, "executor_env", prev.NeedsSecrets[0].Kind)
	require.Empty(t, prev.SkippedElements, "external worker is supported, not skipped")
	applied := callPackageApply(t, dst, prev.StagingId, applyBody("new", ws))
	require.Equal(t, http.StatusOK, applied.Code, "FR-041 body=%s", applied.Body)
	res := decodeResult(t, applied.Body)
	require.Len(t, res.Created, 1)
	require.Len(t, res.NeedsSecrets, 1)
	assert.Equal(t, "OPENAI_API_KEY", res.NeedsSecrets[0].Name)
	assert.Equal(t, "executor_env", res.NeedsSecrets[0].Kind)
	// The generated PackageImportResult contract defines used_by as the ID of
	// the created element, unlike the manifest/preview's source ID.
	assert.Equal(t, res.Created[0].NewId, res.NeedsSecrets[0].UsedBy, "result secret must identify the new worker")
	s1bRequireStoredExternal(t, dst, ws, res)
	s1bRequireResultRuntimeWarning(t, res)
}

func TestPackagesS1b_ExternalImportStripsHandmadeRuntimeAndWarnsWithoutProgram(t *testing.T) {
	for _, withRuntime := range []bool{false, true} {
		name := "clean external without program"
		if withRuntime {
			name = "hand-made runtime without program"
		}
		t.Run(name, func(t *testing.T) {
			api := packageAPI(t)
			usePackageSeams(t)
			installRosterReload(t, api, nil)
			api.agentLoop.GetConfig().Sandbox.ThirdPartyAgents = nil
			ws := createWorkspaceViaAPI(t, api, "Research", "notes")
			cfgBefore := configBytes(t, api.homePath)
			before := snapshotHome(t, api.homePath)
			// AGENT.md is import-only at create, byte-identical even for an
			// external worker (Field mapping); there is deliberately no SOUL.md.
			const instructions = "  worker instructions\n"
			pkg := buildAgentPackage(t, specSourceID, "Coder", s1bWorkerRecord(withRuntime), nil, []byte(instructions), 0)
			prev := stageUpload(t, api, pkg)
			requireHomeUnchangedExcept(t, before, snapshotHome(t, api.homePath))
			require.Len(t, prev.WillAdd, 1)
			assert.Equal(t, "agent", prev.WillAdd[0].Type)
			assert.Equal(t, specSourceID, prev.WillAdd[0].SourceId)
			assert.Equal(t, "Coder", prev.WillAdd[0].ProposedName)
			assert.Contains(t, previewWarningCodes(prev), s1bRuntimeCode, "FR-033 runtime loss is listed in the preview")
			for _, warning := range prev.Warnings {
				if warning.Code == s1bRuntimeCode {
					s1bRequireRuntimeGuidance(t, warning.Message)
				}
			}
			got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
			require.Equal(t, http.StatusOK, got.Code, "FR-043 missing program is a warning, not a refusal; body=%s", got.Body)
			res := decodeResult(t, got.Body)
			s1bRequireStoredExternal(t, api, ws, res)
			s1bRequireResultRuntimeWarning(t, res)
			messages := make([]string, 0, len(res.Warnings))
			for _, warning := range res.Warnings {
				messages = append(messages, warning.Message)
			}
			assert.Contains(t, messages, s1bNoCodexPath, "FR-043 existing cannot-run text is returned verbatim")
			id := res.Created[0].NewId
			storedInstructions, err := os.ReadFile(filepath.Join(api.homePath, "agents", id, "AGENT.md"))
			require.NoError(t, err)
			assert.Equal(t, instructions, string(storedInstructions), "Field mapping: AGENT.md byte-identical")
			assert.Equal(t, cfgBefore, configBytes(t, api.homePath), "host program configuration is never rewritten")
			for _, value := range []string{"sk-TEST-123", "sk-TEST-456", "/source-host/private/codex"} {
				assert.NotContains(t, string(got.Body), value, "warnings must not echo discarded runtime values")
			}
		})
	}
}

func TestPackagesS1b_ExternalUsesTargetProgramNotPackagedPath(t *testing.T) {
	api := packageAPI(t)
	usePackageSeams(t)
	installRosterReload(t, api, nil)
	// An existing regular executable outside the test home supplies the host
	// setting. It is never executed: this test imports and inspects only.
	program, err := os.Executable()
	require.NoError(t, err)
	api.agentLoop.GetConfig().Sandbox.ThirdPartyAgents = &config.ThirdPartyAgentsConfig{
		CLIs: map[string]config.ThirdPartyCLIConfig{"codex": {Path: program}},
	}
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	pkg := buildAgentPackage(t, specSourceID, "Coder", s1bWorkerRecord(true), nil, nil, 0)
	prev := stageUpload(t, api, pkg)
	got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
	require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
	res := decodeResult(t, got.Body)
	s1bRequireStoredExternal(t, api, ws, res)
	s1bRequireResultRuntimeWarning(t, res)
	for _, warning := range res.Warnings {
		assert.NotContains(t, warning.Message, "no program path is set", "FR-043 cannot report an unset program when the target has one")
	}
	assert.Equal(t, program, api.agentLoop.GetConfig().Sandbox.ThirdPartyCLI("codex").Path, "source path never replaces the target's setting")
}

func s1bRequireResultRuntimeWarning(t *testing.T, res gen.PackageImportResult) {
	t.Helper()
	count := 0
	for _, warning := range res.Warnings {
		if warning.Code != s1bRuntimeCode {
			continue
		}
		count++
		s1bRequireRuntimeGuidance(t, warning.Message)
		require.NotNil(t, warning.ElementSourceId)
		require.Equal(t, specSourceID, *warning.ElementSourceId, "warning identifies the source element")
	}
	require.Equal(t, 1, count, "FR-043 runtime_settings_removed must be listed once")
}

func s1bRequireStoredExternal(t *testing.T, api *restAPI, ws string, res gen.PackageImportResult) {
	t.Helper()
	require.Equal(t, gen.PackageImportResultActivationActive, res.Activation, "wired reload confirms stored worker")
	require.Len(t, res.Created, 1)
	created := res.Created[0]
	require.Equal(t, "agent", created.Type)
	require.Equal(t, specSourceID, created.SourceId)
	require.Equal(t, ws, created.WorkspaceId)
	require.Equal(t, "Coder", created.Name)
	require.NotEqual(t, specSourceID, created.NewId)
	_, err := uuid.Parse(created.NewId)
	require.NoError(t, err, "FR-031 new ID must be a UUID")
	stored, err := agentstore.New(api.homePath).ReadState(created.NewId)
	require.NoError(t, err)
	assert.Equal(t, config.AgentTypeWorker, stored.Agent.Type)
	assert.Equal(t, "Runs Codex tasks", stored.Agent.Description, "create rule trims description")
	assert.Equal(t, &config.AgentModelConfig{Primary: "m-1", Provider: "openrouter"}, stored.Agent.Model, "D3-8 model/provider carried")
	assert.Equal(t, 40, stored.Agent.MaxToolIterations, "D3-8 external worker's own turn cap carried")
	assert.Equal(t, "Robot", stored.Agent.Figure)
	assert.Equal(t, "developer", stored.Agent.Role)
	assert.Equal(t, "#6E8BA8", stored.Agent.Color)
	require.NotNil(t, stored.Agent.Subagents)
	require.NotNil(t, stored.Agent.Subagents.Executor)
	assert.Equal(t, &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "codex"}, stored.Agent.Subagents.Executor,
		"kind+cli survive; program path, env overrides and CLI args never do")
	assert.Contains(t, listedAgentIDs(t, api), created.NewId, "confirmed import is reachable in the live list")
	workspace := readWorkspaceMap(t, api.homePath, ws)
	assert.Equal(t, []any{created.NewId}, workspace["core_team"], "only imported worker joins the empty team")
	edges, err := os.ReadFile(filepath.Join(api.homePath, "entities", "delegation", ws+".json"))
	require.NoError(t, err, "missing delegation file must not hide a missing self-row")
	assert.Equal(t, []delegEdge{{From: created.NewId, To: created.NewId}}, decodeDelegationEdges(t, edges), "no other agent receives a line")
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "a nil session store cannot prove no main chat")
	mainID, err := session.MainSessionID(ws, created.NewId)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(store.BaseDir(), mainID, "meta.json"))
	assert.ErrorIs(t, err, os.ErrNotExist, "external worker is not a chat target")
}

func TestPackagesS1b_ExternalFieldsRejectedBeforeSharedApplier(t *testing.T) {
	// Architect ruling sections 1/4A: inspect the raw record, before skills or
	// MCP bindings can disappear during mapping. Installed/missing is not a
	// runtime-policy distinction. The native controls below test the fixture.
	skillsRoot := t.TempDir()
	t.Setenv(config.EnvBuiltinSkills, skillsRoot)
	writeFixtureSkill(t, skillsRoot, "writing")
	for _, tc := range []struct {
		name, field string
		value       any
	}{
		{"D1-33 explicit false", "memory_enabled", false},
		{"explicit true", "memory_enabled", true},
		{"D1-34 positive integer", "context_window_override", 64000},
		// 0 and 1 straddle the native positive-integer rule. An external
		// runtime must refuse BOTH before that value rule (FR-047).
		{"zero cannot reach native applier", "context_window_override", 0},
		{"one still unsupported", "context_window_override", 1},
		{"D1-35 installed skill still unsupported", "skills", []string{"writing"}},
		{"D1-35 missing skill refused before it can be dropped", "skills", []string{"missing-on-target"}},
		{"D1-36 valid fallback still unsupported", "fallback_models", []any{map[string]any{"model": "m-fb", "provider": "openrouter"}}},
		{"D1-37 populated model params", "model_params", map[string]any{"temperature": 0.25, "max_tokens": 2048}},
		{"D1-37 empty model params is still supplied", "model_params", map[string]any{}},
		{"D1-38 effort uses external CLI refusal", "reasoning_effort", "high"},
		{"MCP binding refused before it can be dropped", "mcp_servers", []any{map[string]any{"id": "github"}}},
		{"voice still unsupported", "voice", "alloy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := packageAPI(t)
			installed := api.installedSkillIDs()
			require.Contains(t, installed, "writing", "positive control: real skill registry sees the fixture")
			require.NotContains(t, installed, "missing-on-target", "missing-skill case must exercise pre-mapping validation")
			rec := s1bWorkerRecord(false)
			if tc.field == "mcp_servers" {
				rec["tools"] = map[string]any{"mcp": map[string]any{"servers": tc.value}}
			} else {
				rec[tc.field] = tc.value
			}
			before := snapshotHome(t, api.homePath)
			slots := packageStagingIDs(t, api.homePath)
			agents := agentEntityNames(t, api.homePath)
			live := listedAgentIDs(t, api)
			audits := len(auditJSONLLines(t, api.homePath))
			pkg := buildAgentPackage(t, specSourceID, "Coder", rec, nil, nil, 0)
			got := callPackageImport(t, api, pkg)
			er := requirePackageStatus(t, got, http.StatusUnprocessableEntity, "element_not_creatable")
			assert.Equal(t, tc.field, packageErrField(er), "FR-047: the unsupported field, not a generic worker refusal")
			// The spec requires the runtime refusal create_agent gets from
			// agentmutation.ValidateFields, not the native integer error.
			if tc.field == "reasoning_effort" {
				assert.Contains(t, er.Error, agentmutation.ExternalCLIEffortRefusal, "same pinned reasoning-effort rule as create_agent")
			} else {
				assert.Contains(t, er.Error, "unsupported by this runtime")
			}
			assert.NotContains(t, er.Error, "must be >= 1", "runtime validation must win over the shared value applier")
			assert.Equal(t, slots, packageStagingIDs(t, api.homePath), "refused upload releases its slot")
			assert.Equal(t, agents, agentEntityNames(t, api.homePath), "no published or pending agent record")
			assert.Equal(t, live, listedAgentIDs(t, api), "no agent reaches the live roster")
			requireHomeUnchangedExcept(t, before, snapshotHome(t, api.homePath))
			requireUploadRefusalAudit(t, api.homePath, audits)
		})
	}

	t.Run("native control keeps false memory and positive context", func(t *testing.T) {
		api := packageAPI(t)
		usePackageSeams(t)
		installRosterReload(t, api, nil)
		ws := createWorkspaceViaAPI(t, api, "Research", "notes")
		rec := mainRecord(specSourceID, "Writer", "Drafts")
		rec["memory_enabled"], rec["context_window_override"] = false, 64000
		prev := stageUpload(t, api, buildAgentPackage(t, specSourceID, "Writer", rec, []byte("Answer in rhyme."), nil, 0))
		got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
		require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body)
		res := decodeResult(t, got.Body)
		require.Len(t, res.Created, 1)
		stored, err := agentstore.New(api.homePath).ReadState(res.Created[0].NewId)
		require.NoError(t, err)
		require.NotNil(t, stored.Agent.MemoryEnabled)
		assert.False(t, *stored.Agent.MemoryEnabled)
		require.NotNil(t, stored.Agent.ContextWindowOverride)
		assert.Equal(t, 64000, *stored.Agent.ContextWindowOverride)
	})

	t.Run("native capabilities still import with their own mapping rules", s1bFix2NativeCapabilities)
}

func TestPackagesS1b_ExternalSoulRejectedAsInvalidElement(t *testing.T) {
	api := packageAPI(t)
	before := snapshotHome(t, api.homePath)
	pkg := buildAgentPackage(t, specSourceID, "Coder", s1bWorkerRecord(false), []byte("not an external prompt"), nil, 0)
	got := callPackageImport(t, api, pkg)
	requirePackageStatus(t, got, http.StatusUnprocessableEntity, "invalid_element")
	require.Empty(t, packageStagingIDs(t, api.homePath), "invalid external layout cannot keep a staging")
	requireHomeUnchangedExcept(t, before, snapshotHome(t, api.homePath))
}
