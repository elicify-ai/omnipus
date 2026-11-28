// agent_create_core_red_test.go — RED tests for epic #1094 slice S1a task T-3
// (the shared create function in pkg/gateway/agent_create_core.go) plus the
// rider bug elicify-ai/omnipus#1914 (architect sign-off conflict C-1).
//
// Spec: docs/internal/specs/export-import-s1-spec.md revision 7 (Field
// mapping, FR-034, FR-020, FR-047, FR-048, L17, regression table) and the
// architect's shapes in ARCH-SIGNOFF-T3-T4.md (T-3 block, C-1…C-9). The
// symbols under test are compile-only stubs (stub_for_red.go, "DELETE when
// GREEN lands"): every test here must fail on an assertion or a
// not-implemented error until GREEN lands, never on an undefined symbol.
package gateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snapshotHomeFiles hashes every file under the data folder paths an agent
// create must never touch, plus each file's mtime, so TestBuildAgentCreate_
// HasNoSideEffects and TestCreateAgentWithoutConfigWrite can prove a no-write
// run byte for byte.
// fmtHash hashes file bytes so snapshot comparisons stay terse on failure.
func fmtHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func snapshotHomeFiles(t *testing.T, home string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	roots := []string{
		filepath.Join(home, "entities", "agents"),
		filepath.Join(home, "agents"),
		filepath.Join(home, "config.json"),
	}
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			snap[root] = "absent"
			continue
		}
		if !info.IsDir() {
			data, rerr := os.ReadFile(root)
			require.NoError(t, rerr)
			snap[root] = fmtHash(data) + "|" + info.ModTime().UTC().String()
			continue
		}
		require.NoError(t, filepath.Walk(root, func(p string, fi os.FileInfo, werr error) error {
			if werr != nil {
				return werr
			}
			if fi.IsDir() {
				return nil
			}
			data, rerr := os.ReadFile(p)
			require.NoError(t, rerr)
			snap[p] = fmtHash(data) + "|" + fi.ModTime().UTC().String()
			return nil
		}))
	}
	return snap
}

func requireSnapshotUnchanged(t *testing.T, before, after map[string]string) {
	t.Helper()
	require.Equal(t, before, after, "the data folder files an import create must never touch changed")
}

// TestBuildAgentCreate_MapsSharedFieldsPerSpec pins the shared create
// function's field mapping on the happy path (spec "Field mapping", REST
// create column; BDD "Import and the create paths give the same result for
// shared fields"; sign-off T-3 steps 1–5). Import feeds the same
// agentCreateInput built from a package record, so every stored value here is
// the value import must store too.
func TestBuildAgentCreate_MapsSharedFieldsPerSpec(t *testing.T) {
	api := buildExecutorTestAPI(t)
	policies := fullBuiltinPolicyMap("deny") // tools_cfg submits a complete replacement map, not sparse changes

	temp := 0.4
	maxTok := 512
	maxIter := 50
	in := agentCreateInput{
		WireType:    "Main",
		Name:        "  Writer  ",
		Description: strPtr("  Drafts  "),
		Soul:        "\n  Answer in rhyme.\n\n",
		Model:       strPtr("m-1"),
		Provider:    strPtr("  prov-1  "),
		FallbackModels: &[]gen.FallbackModel{
			{Model: "fb-a", Provider: strPtr("prov-a"), ReasoningEffort: strPtr("high")},
			{Model: "fb-b", Provider: strPtr("prov-b")},
		},
		ReasoningEffort:   strPtr("low"),
		ModelParams:       &agentModelParamsInput{Temperature: &temp, MaxTokens: &maxTok},
		ToolsCfg:          &agentCreateToolsCfgInput{BuiltinPolicies: policies},
		MaxToolIterations: &maxIter,
	}

	plan, refusal := api.buildAgentCreate(in)
	require.Nil(t, refusal, "a valid input must build a plan, got refusal %+v", refusal)

	// Name and description are stored trimmed (create rule; Field mapping rows
	// `name`, `description`).
	assert.Equal(t, "Writer", plan.Agent.Name)
	assert.Equal(t, "Drafts", plan.Agent.Description)
	// SOUL.md is stored with leading/trailing whitespace removed; the inner
	// text is never changed (Field mapping row `SOUL.md`).
	assert.Equal(t, "Answer in rhyme.", plan.Soul)
	// New UUID per create (Field mapping row `id`; ADR D5), valid and fresh.
	_, err := uuid.Parse(plan.Agent.ID)
	require.NoError(t, err, "plan ID must be a UUID")
	plan2, refusal2 := api.buildAgentCreate(in)
	require.Nil(t, refusal2)
	assert.NotEqual(t, plan.Agent.ID, plan2.Agent.ID, "every build must mint a fresh ID")
	// Type mapping: Main is stored custom (coreagent.ResolveType; Field
	// mapping row `type`).
	assert.Equal(t, config.AgentTypeCustom, plan.Agent.Type)
	// Model pair: primary kept; provider name kept and trimmed (rows
	// `model.primary`, `model.provider`).
	require.NotNil(t, plan.Agent.Model)
	assert.Equal(t, "m-1", plan.Agent.Model.Primary)
	assert.Equal(t, "prov-1", plan.Agent.Model.Provider)
	// fallback_models stored, paired, with per-entry effort (rows
	// `fallback_models[]`, rider #1440's stored chain).
	require.Len(t, plan.Agent.FallbackModels, 2)
	assert.Equal(t, "fb-a", plan.Agent.FallbackModels[0].Model)
	assert.Equal(t, "prov-a", plan.Agent.FallbackModels[0].Provider)
	assert.Equal(t, "high", plan.Agent.FallbackModels[0].ReasoningEffort)
	assert.Equal(t, "fb-b", plan.Agent.FallbackModels[1].Model)
	// reasoning_effort kept (row `reasoning_effort`).
	assert.Equal(t, "low", plan.Agent.ReasoningEffort)
	// model_params.temperature / .max_tokens merged (rows `model_params.*`).
	require.NotNil(t, plan.Agent.ModelParams)
	require.NotNil(t, plan.Agent.ModelParams.Temperature)
	assert.InDelta(t, 0.4, *plan.Agent.ModelParams.Temperature, 1e-9)
	require.NotNil(t, plan.Agent.ModelParams.MaxTokens)
	assert.Equal(t, 512, *plan.Agent.ModelParams.MaxTokens)
	// max_tool_iterations kept (row `max_tool_iterations`).
	assert.Equal(t, 50, plan.Agent.MaxToolIterations)
	// Canonical visual defaults when omitted (rows `figure`, `role`, `color`).
	assert.Equal(t, coreagent.DefaultFigure, plan.Agent.Figure)
	assert.Equal(t, coreagent.DefaultRole, plan.Agent.Role)
	assert.Equal(t, coreagent.DefaultColor, plan.Agent.Color)
	// The complete caller map passes the submitted-map check and overrides the
	// type's seed. Sparse edits use tool_policy_changes instead (spec alias table;
	// sign-off T-3 step 5). Every explicit deny must survive the build.
	require.NotNil(t, plan.Agent.Tools)
	assert.Equal(t, config.ToolPolicyDeny, plan.Agent.Tools.Builtin.Policies["bash"])
	for name := range buildKnownBuiltinToolNames() {
		_, covered := plan.Agent.Tools.Builtin.Policies[name]
		assert.True(t, covered, "seeded plan must keep an explicit policy for %q", name)
		assert.Equal(t, config.ToolPolicyDeny, plan.Agent.Tools.Builtin.Policies[name], "the caller's deny for %q must override the seed", name)
	}
	// The plan is not persisted by the build step (see
	// TestBuildAgentCreate_HasNoSideEffects).
}

// TestBuildAgentCreate_RefusalsMatchREST pins the create-rule refusals in
// today's order (sign-off T-3 "The order is copied from rest_agents_create.go
// so every refusal happens in the same order"; D1 rows 2, 21, 23, 24, 27–29
// create-rule column: a package record breaking a rule is refused
// `element_not_creatable` with that same rule). Each case asserts the refusal
// the REST handler writes today, so import refusals cannot drift from create.
func TestBuildAgentCreate_RefusalsMatchREST(t *testing.T) {
	// REST deliberately accepts arbitrary ids with an empty installed set.
	// Install a real skill so the unknown-id case reaches referential validation.
	skillsRoot := t.TempDir()
	t.Setenv("OMNIPUS_BUILTIN_SKILLS", skillsRoot)
	skillDir := filepath.Join(skillsRoot, "writing")
	require.NoError(t, os.MkdirAll(skillDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: writing\ndescription: fixture for create-rule validation\n---\n# Writing\n"), 0o600))
	api := buildExecutorTestAPI(t)
	installed := api.installedSkillIDs()
	require.Contains(t, installed, "writing", "setup: the real loader must discover the installed fixture")
	require.NotContains(t, installed, "no-such-skill", "setup: the refusal target must not be installed")
	known, knownRefusal := api.buildAgentCreate(agentCreateInput{WireType: "Main", Name: "Known skill", Soul: "s", Skills: &[]string{"writing"}})
	require.Nil(t, knownRefusal, "instrument: an installed skill is accepted")
	assert.Equal(t, []string{"writing"}, known.Agent.Skills)

	longName := strings.Repeat("A", 101)
	threeFallbacks := []gen.FallbackModel{
		{Model: "a", Provider: strPtr("p")}, {Model: "b", Provider: strPtr("p")}, {Model: "c", Provider: strPtr("p")},
	}
	unpaired := []gen.FallbackModel{{Model: "fb-a"}}

	cases := []struct {
		name    string
		in      agentCreateInput
		status  int
		msgHas  string
		code    string
		field   string
		wantMsg string
	}{
		{
			name:    "blank name is refused 422 (create rule, Field mapping row name)",
			in:      agentCreateInput{WireType: "Main", Name: "   ", Soul: "s"},
			status:  http.StatusUnprocessableEntity,
			wantMsg: "name is required",
		},
		{
			name:   "reserved name is refused 409 with its code (create rule)",
			in:     agentCreateInput{WireType: "Main", Name: "omnipus", Soul: "s"},
			status: http.StatusConflict,
			code:   codeReservedAgentName,
		},
		{
			name:   "name over 100 characters is refused 400 with its code (#1075)",
			in:     agentCreateInput{WireType: "Main", Name: longName, Soul: "s"},
			status: http.StatusBadRequest,
			code:   codeAgentNameTooLong,
		},
		{
			name:    "unknown skill is refused 400 (validateSkillIDs before any build)",
			in:      agentCreateInput{WireType: "Main", Name: "Skilled", Soul: "s", Skills: &[]string{"no-such-skill"}},
			status:  http.StatusBadRequest,
			wantMsg: `unknown skill id: "no-such-skill"`,
		},
		{
			name:    "worker without a description is refused 400 (Field mapping row description)",
			in:      agentCreateInput{WireType: "Subagent", Name: "Worker", Soul: "s"},
			status:  http.StatusBadRequest,
			wantMsg: "description is required for worker agents (Subagent, subagent_3p)",
			field:   "description",
		},
		{
			name:   "three fallback models are refused 400 (create rule: at most 2, paired)",
			in:     agentCreateInput{WireType: "Main", Name: "Fbs", Soul: "s", FallbackModels: &threeFallbacks},
			status: http.StatusBadRequest,
			msgHas: "fallback_models",
			field:  "fallback_models",
		},
		{
			name:   "unpaired fallback model is refused 400 (create rule: paired)",
			in:     agentCreateInput{WireType: "Main", Name: "Fbp", Soul: "s", FallbackModels: &unpaired},
			status: http.StatusBadRequest,
			// REST create writes unpairedFallbackError's exact text today; the
			// shared refusal must stay byte-identical to it.
			wantMsg: unpairedFallbackError(unpaired),
		},
		{
			name:    "whitespace-only soul is refused 400 (Field mapping row SOUL.md)",
			in:      agentCreateInput{WireType: "Main", Name: "Soulless", Soul: "   "},
			status:  http.StatusBadRequest,
			wantMsg: "soul is required (whitespace-only is rejected as minLength violation)",
		},
		{
			name:   "non-canonical color is refused 400 (D3 row 7: no import normalisation)",
			in:     agentCreateInput{WireType: "Main", Name: "Colored", Soul: "s", Color: (*gen.AgentColor)(strPtr("not-a-colour"))},
			status: http.StatusBadRequest,
			msgHas: "color must be one of the ten identity colours",
			field:  "color",
		},
		{
			name:   "non-canonical figure is refused 400 (D1 row 29)",
			in:     agentCreateInput{WireType: "Main", Name: "Figured", Soul: "s", Figure: (*gen.AgentFigure)(strPtr("NotAFigure"))},
			status: http.StatusBadRequest,
			msgHas: "figure must be Robot, Man, Woman, Omnipus, or Monogram",
			field:  "figure",
		},
		{
			name:   "non-canonical role is refused 400 (D1 row 29)",
			in:     agentCreateInput{WireType: "Main", Name: "Roled", Soul: "s", Role: (*gen.AgentRole)(strPtr("not-a-role"))},
			status: http.StatusBadRequest,
			msgHas: "role must be one of the curated role slugs",
			field:  "role",
		},
		{
			name:   "unconfigured MCP server is refused 422 (sign-off step 5 buildToolConfig)",
			in:     agentCreateInput{WireType: "Main", Name: "Ghost", Soul: "s", ToolsCfg: &agentCreateToolsCfgInput{MCPSpecified: true, MCPServers: []agentCreateMCPServerInput{{ID: "ghost"}}}},
			status: http.StatusUnprocessableEntity,
			msgHas: "not configured",
		},
		{
			name:   "name rule wins over the soul rule (sign-off: refusal order is today's)",
			in:     agentCreateInput{WireType: "Main", Name: "omnipus", Soul: "   "},
			status: http.StatusConflict,
			code:   codeReservedAgentName,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, refusal := api.buildAgentCreate(tc.in)
			require.NotNil(t, refusal, "expected a refusal, got a plan")
			assert.Equal(t, tc.status, refusal.Status)
			if tc.wantMsg != "" {
				assert.Equal(t, tc.wantMsg, refusal.Msg)
			}
			if tc.msgHas != "" {
				assert.Contains(t, refusal.Msg, tc.msgHas)
			}
			if tc.code != "" {
				assert.Equal(t, tc.code, refusal.Code)
			}
			if tc.field != "" {
				assert.Equal(t, tc.field, refusal.Field)
			}
			assert.NotEmpty(t, refusal.Msg, "every refusal carries a message")
		})
	}
}

// TestBuildAgentCreate_HasNoSideEffects proves the build step is side-effect
// free (sign-off: "It has no side effects (no disk or config writes), so
// upload's dry run (FR-020) can call it"). The upload dry run's promise —
// nothing on B changes at preview time (FR-023) — rests on exactly this.
func TestBuildAgentCreate_HasNoSideEffects(t *testing.T) {
	api := buildExecutorTestAPI(t)
	before := snapshotHomeFiles(t, api.homePath)

	valid := agentCreateInput{WireType: "Main", Name: "DryRun", Soul: "s"}
	_, refusal := api.buildAgentCreate(valid)
	require.Nil(t, refusal)
	badInput := agentCreateInput{WireType: "Main", Name: "   ", Soul: "s"}
	_, badRefusal := api.buildAgentCreate(badInput)
	require.NotNil(t, badRefusal)

	requireSnapshotUnchanged(t, before, snapshotHomeFiles(t, api.homePath))
}

// TestPersistAgentCreateLocked_D10RecheckAndPersist pins the persist step
// (sign-off persistAgentCreateLocked): it persists record + SOUL.md through
// the agent store and re-checks the #904 D10 limit under configMu (the
// deciding check that closes the window against concurrent global writes).
// The caller holds a.configMu in production; this test calls it directly.
func TestPersistAgentCreateLocked_D10RecheckAndPersist(t *testing.T) {
	t.Run("persists record and soul through the agent store", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		seed := coreagent.NewCustomAgentToolsCfg()
		plan := &agentCreatePlan{
			Agent: config.AgentConfig{ID: "persist01", Name: "Persisted", Type: config.AgentTypeCustom, Tools: seed},
			Soul:  "persisted soul",
		}
		result, err := api.persistAgentCreateLocked(plan)
		require.NoError(t, err)
		assert.Equal(t, agentstore.PersistenceComplete, result.PersistenceStatus)

		state, readErr := agentstore.New(api.homePath).ReadState("persist01")
		require.NoError(t, readErr)
		assert.Equal(t, "Persisted", state.Agent.Name)
		assert.Equal(t, "persisted soul", state.Soul)
	})

	t.Run("D10 re-check refuses an own limit above the global under configMu", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		before, _, listErr := agentstore.New(api.homePath).List()
		require.NoError(t, listErr)
		seed := coreagent.NewCustomAgentToolsCfg()
		plan := &agentCreatePlan{
			Agent: config.AgentConfig{
				ID: "persist02", Name: "Too Many", Type: config.AgentTypeCustom, Tools: seed,
				// The shipped global limit is config.DefaultMaxToolIterations
				// (#904 D10); one above it must be refused here even though the
				// build step's fast check already saw the same global.
				MaxToolIterations: config.DefaultMaxToolIterations + 1,
			},
			Soul: "s",
		}
		result, err := api.persistAgentCreateLocked(plan)
		require.Error(t, err, "an own limit above the global must be refused at persist time")
		var refusal *requestRefusalError
		require.ErrorAs(t, err, &refusal, "the D10 re-check returns today's requestRefusalError")
		assert.Equal(t, maxToolIterationsFieldName, refusal.field)
		assert.Contains(t, err.Error(), "above the global limit")
		assert.Equal(t, agentstore.PersistenceNone, result.PersistenceStatus)

		after, _, listErr := agentstore.New(api.homePath).List()
		require.NoError(t, listErr)
		assert.Len(t, after, len(before), "a refused persist stores nothing")
	})
}

// TestCreateAgentWithoutConfigWrite pins import's E2+E4 (FR-034: "Apply MUST
// create the agent through the shared create function … with the coverage
// check under the config lock and without writing config.json"; SC-008
// mechanism; sign-off withToolPolicyCoverageCheck / createAgentWithoutConfigWrite).
func TestCreateAgentWithoutConfigWrite(t *testing.T) {
	t.Run("persists without touching config.json", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		cfgPath := filepath.Join(api.homePath, "config.json")
		beforeBytes, err := os.ReadFile(cfgPath)
		require.NoError(t, err)
		beforeMod, err := os.Stat(cfgPath)
		require.NoError(t, err)

		seed := coreagent.NewCustomAgentToolsCfg()
		plan := &agentCreatePlan{
			Agent: config.AgentConfig{ID: "nocfgw1", Name: "No Cfg Write", Type: config.AgentTypeCustom, Tools: seed},
			Soul:  "soul text",
		}
		result, createErr := api.createAgentWithoutConfigWrite(plan)
		require.NoError(t, createErr)
		assert.Equal(t, agentstore.PersistenceComplete, result.PersistenceStatus)

		state, readErr := agentstore.New(api.homePath).ReadState("nocfgw1")
		require.NoError(t, readErr)
		assert.Equal(t, "soul text", state.Soul)

		afterBytes, err := os.ReadFile(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, string(beforeBytes), string(afterBytes), "config.json bytes must be identical (SC-008)")
		afterMod, err := os.Stat(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, beforeMod.ModTime(), afterMod.ModTime(), "config.json must not even be rewritten (mtime unchanged)")
	})

	t.Run("a coverage gap is a typed gap error and persists nothing", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		cfgPath := filepath.Join(api.homePath, "config.json")
		beforeBytes, err := os.ReadFile(cfgPath)
		require.NoError(t, err)

		seed := coreagent.NewCustomAgentToolsCfg()
		delete(seed.Builtin.Policies, "bash") // a caller map that leaves a static builtin uncovered
		plan := &agentCreatePlan{
			Agent: config.AgentConfig{ID: "gapagent1", Name: "Gap", Type: config.AgentTypeCustom, Tools: seed},
			Soul:  "s",
		}
		_, createErr := api.createAgentWithoutConfigWrite(plan)
		require.Error(t, createErr, "an uncovered static builtin must refuse the import create")
		var gapErr *toolPolicyCoverageGapError
		require.ErrorAs(t, createErr, &gapErr, "gaps come back as *toolPolicyCoverageGapError (sign-off)")
		require.NotEmpty(t, gapErr.Gaps)
		found := false
		for _, gap := range gapErr.Gaps {
			if gap.AgentID == "gapagent1" && gap.ToolName == "bash" {
				found = true
			}
		}
		assert.True(t, found, "the gap must name the agent and the uncovered tool, got %+v", gapErr.Gaps)

		_, readErr := agentstore.New(api.homePath).ReadState("gapagent1")
		require.Error(t, readErr, "a refused import create persists nothing")
		afterBytes, err := os.ReadFile(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, string(beforeBytes), string(afterBytes))
	})
}

// TestApplyImportToolFields pins FR-047: import stores voice,
// memory_enabled and context_window_override with the create_agent tool's
// rules (agentmutation.ValidateFields first, then the shared applier), and —
// adaptation C-9 — never passes model_params and only passes a key the
// package actually carries.
func TestApplyImportToolFields(t *testing.T) {
	t.Run("applies the three tool-rule fields to a Main plan", func(t *testing.T) {
		mem := false
		cw := 64000
		plan := &agentCreatePlan{Agent: config.AgentConfig{ID: "toolf1", Type: config.AgentTypeCustom}}
		err := applyImportToolFields(plan, "alloy", &mem, &cw)
		require.NoError(t, err)
		assert.Equal(t, "alloy", plan.Agent.Voice)
		require.NotNil(t, plan.Agent.MemoryEnabled)
		assert.False(t, *plan.Agent.MemoryEnabled)
		require.NotNil(t, plan.Agent.ContextWindowOverride)
		assert.Equal(t, 64000, *plan.Agent.ContextWindowOverride)
		assert.Nil(t, plan.Agent.ModelParams, "C-9: the applier never passes model_params")
	})

	t.Run("an absent voice is not applied", func(t *testing.T) {
		plan := &agentCreatePlan{Agent: config.AgentConfig{ID: "toolf2", Type: config.AgentTypeCustom}}
		err := applyImportToolFields(plan, "", nil, nil)
		require.NoError(t, err)
		assert.Empty(t, plan.Agent.Voice)
		assert.Nil(t, plan.Agent.MemoryEnabled)
		assert.Nil(t, plan.Agent.ContextWindowOverride)
	})

	t.Run("a worker voice is refused with the create_agent rule", func(t *testing.T) {
		plan := &agentCreatePlan{Agent: config.AgentConfig{ID: "toolf3", Type: config.AgentTypeWorker}}
		err := applyImportToolFields(plan, "alloy", nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "worker cannot have a per-agent voice")
	})
}

// TestCreateExtraction_RESTUnchanged is the CONTROL test of this RED pack: it
// pins REST create's behaviour through the T-3 refactor and is expected to
// PASS on the current code (BDD "REST create unchanged, and #1440 checked";
// regression table; L17). It closes rider #1440 only when it passes: a
// create-then-GET must return both fallback models, and REST create still
// stores an empty voice (Known Limit L17 — accepted, not stored).
func TestCreateExtraction_RESTUnchanged(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"FbKeeper","type":"Main","soul":"soul text","voice":"alloy",` +
		`"fallback_models":[{"model":"fb-a","provider":"prov-a","reasoning_effort":"high"},` +
		`{"model":"fb-b","provider":"prov-b"}]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())

	wGet := httptest.NewRecorder()
	api.getAgent(wGet, created.Id)
	require.Equal(t, http.StatusOK, wGet.Code, "GET body: %s", wGet.Body.String())
	ag := decodeAgentResp(t, wGet.Body.Bytes())
	// Rider #1440: the validated fallback chain is stored AND read back.
	require.NotNil(t, ag.FallbackModels, "GET must return the stored fallback_models (#1440)")
	require.Len(t, *ag.FallbackModels, 2)
	assert.Equal(t, "fb-a", (*ag.FallbackModels)[0].Model)
	require.NotNil(t, (*ag.FallbackModels)[0].Provider)
	assert.Equal(t, "prov-a", *(*ag.FallbackModels)[0].Provider)
	require.NotNil(t, (*ag.FallbackModels)[0].ReasoningEffort)
	assert.Equal(t, "high", *(*ag.FallbackModels)[0].ReasoningEffort)
	assert.Equal(t, "fb-b", (*ag.FallbackModels)[1].Model)
	require.NotNil(t, (*ag.FallbackModels)[1].Provider)
	assert.Equal(t, "prov-b", *(*ag.FallbackModels)[1].Provider)

	// L17: REST create accepts voice but still stores an empty one.
	saved, err := agentstore.New(api.homePath).Get(created.Id)
	require.NoError(t, err)
	assert.Empty(t, saved.Voice, "REST create must not store voice (L17, unchanged behaviour)")
}

// TestRESTCreateDualFormConflictPersistsNothing_Rider1914 is the rider bug
// test for elicify-ai/omnipus#1914 (architect sign-off conflict C-1): today's
// validateAndBuildConfig writes the 400 for "mcp_servers and tools_cfg.mcp
// cannot both be supplied" (and the tool_policy_changes/tools_cfg.builtin
// twin) but returns (false, true), so prepareAgent returns not-stop and
// createAgent CARRIES ON — buildToolConfig runs, the agent is persisted, and a
// second (201) body is written after the 400. REST create must answer ONE
// clean 400 and store nothing. This test must FAIL on the current code for
// exactly that reason; the fix (shared refusal stops the request) makes it
// pass.
func TestRESTCreateDualFormConflictPersistsNothing_Rider1914(t *testing.T) {
	cases := []struct {
		name     string
		mcp      bool
		body     string
		conflict string
	}{
		{
			name:     "mcp_servers and tools_cfg.mcp together",
			mcp:      true,
			body:     `{"name":"ClashMCP","type":"Main","soul":"soul text","mcp_servers":[{"id":"docs"}],"tools_cfg":{"mcp":{"servers":[{"id":"docs"}]}}}`,
			conflict: "mcp_servers and tools_cfg.mcp cannot both be supplied",
		},
		{
			name:     "tool_policy_changes and tools_cfg.builtin together",
			body:     `{"name":"ClashPol","type":"Main","soul":"soul text","tool_policy_changes":{"set":{"bash":"deny"}},"tools_cfg":{"builtin":{"policies":{"bash":"deny"}}}}`,
			conflict: "tool_policy_changes and tools_cfg.builtin cannot both be supplied",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			if tc.mcp {
				// The conflict path only reaches persistence when the server it
				// names IS configured — otherwise buildToolConfig would refuse
				// for a different reason and the test would not prove the bug.
				// The harness leaves the server map nil, so the test makes one.
				cfg := api.agentLoop.GetConfig()
				if cfg.Tools.MCP.Servers == nil {
					cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{}
				}
				cfg.Tools.MCP.Servers["docs"] = config.MCPServerConfig{Enabled: true, Command: "test"}
			}
			before, _, listErr := agentstore.New(api.homePath).List()
			require.NoError(t, listErr)

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			api.HandleAgents(w, r)

			assert.Equal(t, http.StatusBadRequest, w.Code, "the conflict is a 400 (body: %s)", w.Body.String())
			assert.Contains(t, w.Body.String(), tc.conflict)

			// Exactly ONE JSON document is written: no 201 body after the 400.
			dec := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
			var first map[string]any
			require.NoError(t, dec.Decode(&first), "body must be one JSON document: %s", w.Body.String())
			trailing, err := dec.Token()
			assert.True(t, err == io.EOF || trailing == nil,
				"a second response body was written after the 400 (rider #1914): %s", w.Body.String())

			// Nothing was persisted: the store keeps exactly the agents it had.
			after, _, listErr := agentstore.New(api.homePath).List()
			require.NoError(t, listErr)
			assert.Len(t, after, len(before), "REST create must NOT save the agent after the 400 (rider #1914)")
			for _, agent := range after {
				assert.NotContains(t, []string{"ClashMCP", "ClashPol"}, agent.Name,
					"the conflicting create must not be persisted")
			}
		})
	}
}

// TestImportFieldCoverage_ResolvesEveryEditableName is spec test row 21a
// (FR-048): every name in the agent tools' create and update lists, the three
// REST create schemas and AgentUpdateRequest resolves — through the alias
// table — to Field-mapping rows (nested leaves included), or is a listed
// control name with its written decision. There is no unlisted ignore set, so
// a new user-editable field cannot be added without an import decision. The
// accessor names come from systools.AgentCreateArgNames / AgentUpdateArgNames
// (sign-off C-8); the REST names come from reflection over the generated
// schemas' JSON tags.
func TestImportFieldCoverage_ResolvesEveryEditableName(t *testing.T) {
	createNames := systools.AgentCreateArgNames()
	updateNames := systools.AgentUpdateArgNames()
	require.NotEmpty(t, createNames, "AgentCreateArgNames must be implemented (C-8) — RED stub returns nil")
	require.NotEmpty(t, updateNames, "AgentUpdateArgNames must be implemented (C-8) — RED stub returns nil")

	// stored-field rows: the "Field (stored JSON name)" column of the spec's
	// Field-mapping table (revision 7), every row incl. the [S1b] executor rows.
	rows := map[string]bool{
		"id": true, "name": true, "description": true, "type": true, "SOUL.md": true, "AGENT.md": true,
		"model.primary": true, "model.provider": true, "model.fallbacks": true,
		"fallback_models[].model": true, "fallback_models[].provider": true, "fallback_models[].reasoning_effort": true,
		"reasoning_effort": true, "max_tool_iterations": true,
		"model_params.temperature": true, "model_params.max_tokens": true,
		"skills": true, "skill_grants": true,
		"tools.builtin.policies": true, "tools.mcp.servers": true,
		"figure": true, "role": true, "color": true,
		"voice": true, "memory_enabled": true, "context_window_override": true,
		"subagents.allow_agents": true, "subagents.model": true,
		"subagents.executor.kind": true, "subagents.executor.cli": true,
		"subagents.executor.env_overrides": true, "subagents.executor.cli_args": true, "subagents.executor.cli_path": true,
		"workspace": true, "created_at": true, "updated_at": true, "locked": true,
	}

	// Control and alias names (spec "Control and alias names (not agent
	// properties; never stored from the package)"). A control carries its
	// written decision; an alias resolves to stored-field rows.
	controls := map[string]string{
		"revision":             "not transported: the new agent gets its own revision from the store",
		"workspace_id":         "not taken from the package: the destination comes from the apply request or the default workspace",
		"env_overrides_remove": "not transported: there is nothing to remove on a new agent",
		"id":                   "selector only: the new agent gets a new UUID (row id, ADR D5)",
		"programs":             "not transported: export lists switched-on programs under needs_secrets (kind program); an imported agent's programs start empty (connected programs FR-022, S-49)",
	}
	alias := map[string][]string{
		"soul":                {"SOUL.md"},
		"agent_md":            {"AGENT.md"},
		"agent_type":          {"type"},
		"type":                {"type"},
		"tools_cfg":           {"tools.builtin.policies", "tools.mcp.servers"},
		"tool_policy_changes": {"tools.builtin.policies"},
		"mcp_servers":         {"tools.mcp.servers"},
		"executor": {"subagents.executor.kind", "subagents.executor.cli",
			"subagents.executor.env_overrides", "subagents.executor.cli_args", "subagents.executor.cli_path"},
		"cli": {"subagents.executor.kind", "subagents.executor.cli",
			"subagents.executor.env_overrides", "subagents.executor.cli_args", "subagents.executor.cli_path"},
		"model":                   {"model.primary"},
		"provider":                {"model.provider"},
		"fallback_models":         {"fallback_models[].model", "fallback_models[].provider", "fallback_models[].reasoning_effort"},
		"model_params":            {"model_params.temperature", "model_params.max_tokens"},
		"name":                    {"name"},
		"description":             {"description"},
		"color":                   {"color"},
		"figure":                  {"figure"},
		"role":                    {"role"},
		"skills":                  {"skills"},
		"voice":                   {"voice"},
		"memory_enabled":          {"memory_enabled"},
		"context_window_override": {"context_window_override"},
		"reasoning_effort":        {"reasoning_effort"},
		"max_tool_iterations":     {"max_tool_iterations"},
		"env_overrides":           {"subagents.executor.env_overrides"},
		"cli_args":                {"subagents.executor.cli_args"},
	}

	names := map[string]bool{}
	for _, n := range createNames {
		names[n] = true
	}
	for _, n := range updateNames {
		names[n] = true
	}
	for _, variant := range []any{
		gen.AgentCreateRequestMain{}, gen.AgentCreateRequestSubagent{}, gen.AgentCreateRequestSubagent3p{},
		gen.AgentUpdateRequest{},
	} {
		for n := range schemaTopLevelNames(t, variant) {
			names[n] = true
		}
	}

	var unresolved, rowless []string
	for name := range names {
		if _, isControl := controls[name]; isControl {
			continue
		}
		leaves, ok := alias[name]
		if !ok {
			unresolved = append(unresolved, name)
			continue
		}
		for _, leaf := range leaves {
			if !rows[leaf] {
				rowless = append(rowless, name+" -> "+leaf)
			}
		}
	}
	require.Empty(t, unresolved, "field names with no import decision (FR-048): %v", unresolved)
	require.Empty(t, rowless, "alias resolutions pointing at rows that do not exist: %v", rowless)

	// Nested leaves come from the generated schemas, not from a fixed list.
	// A new field inside fallback_models, model_params, executor or tools_cfg
	// fails until it has a row, an alias leaf or a control decision (FR-048).
	decided := map[string]bool{}
	for leaf := range rows {
		decided[leaf] = true
	}
	for name := range controls {
		decided[name] = true
	}
	for name, leaves := range alias {
		decided[name] = true
		for _, leaf := range leaves {
			decided[leaf] = true
		}
	}
	var unseen []string
	for _, variant := range []any{
		gen.AgentCreateRequestMain{}, gen.AgentCreateRequestSubagent{}, gen.AgentCreateRequestSubagent3p{},
		gen.AgentUpdateRequest{},
	} {
		for _, leaf := range schemaNestedLeaves(reflect.TypeOf(variant)) {
			if nestedLeafDecided(leaf, decided, controls) {
				continue
			}
			unseen = append(unseen, leaf)
		}
	}
	sort.Strings(unseen)
	require.Empty(t, unseen, "FR-048: nested editable leaves with no import decision: %v", unseen)

	// Instrument checks (skill: test the instrument). The nested leaves the
	// spec names must reuse the existing agentmutation vocabulary rather than
	// a second spelling (FR-048), and the control names must stay listed as
	// deliberate exclusions there too.
	mapping := agentmutation.MappingRows()
	mappingSet := map[string]bool{}
	for _, row := range mapping {
		mappingSet[row.RESTPath] = true
	}
	for _, leaf := range []string{
		"fallback_models[].model", "fallback_models[].provider", "fallback_models[].reasoning_effort",
		"model_params.temperature", "model_params.max_tokens",
	} {
		assert.True(t, mappingSet[leaf], "row spelling %q must reuse the agentmutation MappingRows vocabulary", leaf)
	}
	excluded := agentmutation.ExcludedRows()
	excludedSet := map[string]bool{}
	for _, row := range excluded {
		excludedSet[row.Path] = true
	}
	for _, control := range []string{"id", "workspace_id", "agent_md"} {
		assert.True(t, excludedSet[control],
			"control/exclusion %q must reuse the agentmutation ExcludedRows vocabulary", control)
	}
}

// schemaTopLevelNames reflects the generated wire struct's top-level JSON
// property names (sign-off C-8: "REST schema names come from reflection on
// the generated variant structs' JSON tags").
func schemaTopLevelNames(t *testing.T, v any) map[string]bool {
	t.Helper()
	typ := reflect.TypeOf(v)
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	require.Equal(t, reflect.Struct, typ.Kind(), "%T must be a struct", v)
	names := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name != "" {
			names[name] = true
		}
	}
	return names
}

// schemaNestedLeaves reflects nested JSON leaves under the field groups whose
// import decision is per leaf. Maps and slices of scalars stay one leaf. The
// names are the generated schema's, so a new nested property cannot hide
// behind the group's top-level name (FR-048).
func schemaNestedLeaves(typ reflect.Type) []string {
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := jsonName(field)
		if name == "" || !nestedGroup(name) {
			continue
		}
		collectNested(field.Type, name, &out)
	}
	return out
}

func nestedGroup(name string) bool {
	switch name {
	case "fallback_models", "model_params", "executor", "tools_cfg":
		return true
	default:
		return false
	}
}

func collectNested(typ reflect.Type, path string, out *[]string) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name := jsonName(field)
			if name == "" {
				continue
			}
			collectNested(field.Type, path+"."+name, out)
		}
	case reflect.Slice, reflect.Array:
		elem := typ.Elem()
		for elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		}
		if elem.Kind() == reflect.Struct && (path == "fallback_models" || strings.HasPrefix(path, "fallback_models[]")) {
			collectNested(elem, path+"[]", out)
			return
		}
		*out = append(*out, path)
	default:
		*out = append(*out, path)
	}
}

func jsonName(field reflect.StructField) string {
	if field.PkgPath != "" {
		return ""
	}
	tag := field.Tag.Get("json")
	if tag == "" || tag == "-" {
		return ""
	}
	name := strings.Split(tag, ",")[0]
	if name == "-" {
		return ""
	}
	return name
}

// nestedLeafDecided reports whether path is a stored row, an alias leaf,
// the same dotted suffix as one of those (executor.cli matches
// subagents.executor.cli; tools_cfg.builtin.policies matches
// tools.builtin.policies), or a control named by its last segment
// (executor.env_overrides_remove is the control env_overrides_remove).
// A short alias such as cli does not decide every field whose tail is cli.
func nestedLeafDecided(path string, decided map[string]bool, controls map[string]string) bool {
	if decided[path] {
		return true
	}
	last := path
	if i := strings.LastIndex(path, "."); i >= 0 {
		last = path[i+1:]
	}
	if _, ok := controls[last]; ok {
		return true
	}
	for leaf := range decided {
		if leaf == path || boundedSuffix(leaf, path) {
			return true
		}
		if strings.Contains(path, ".") && strings.Contains(leaf, ".") && nestedTail(leaf) == nestedTail(path) {
			return true
		}
	}
	return false
}

func boundedSuffix(leaf, path string) bool {
	if !strings.HasSuffix(leaf, path) || len(leaf) == len(path) {
		return false
	}
	return leaf[len(leaf)-len(path)-1] == '.'
}

func nestedTail(path string) string {
	if i := strings.Index(path, "."); i >= 0 {
		return path[i+1:]
	}
	return path
}
