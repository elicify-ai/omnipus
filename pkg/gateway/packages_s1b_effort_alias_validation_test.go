package gateway

// #1952 / CHECK P2-1 / RED2: the raw effort rule applies before mapping to
// every case spelling, including an invalid member overwritten by a valid
// alias. The oracle is the dispatch and C-2/S-56/S-94, not the typed result.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type s1bAliasEffortSlot struct {
	name, canonical, alias string
	fallback               bool
}

func s1bAliasEffortSlots() []s1bAliasEffortSlot {
	return []s1bAliasEffortSlot{
		{"primary", "reasoning_effort", "Reasoning_Effort", false},
		{"recap", "recap_reasoning_effort", "Recap_Reasoning_Effort", false},
		{"fallback", "reasoning_effort", "Reasoning_Effort", true},
	}
}

func (slot s1bAliasEffortSlot) wrap(members string) string {
	if slot.fallback {
		return `"fallback_models":[{"model":"m-fb","provider":"openrouter",` + members + `}]`
	}
	return members
}

func TestPackagesS1b_EffortCaseAliasesRefuseOverlongValues(t *testing.T) {
	// 33 = maxLength 32 + 1. A short value must not hide an invalid alias,
	// whichever spelling or incoming order holds the invalid raw member.
	long := strings.Repeat("x", 33)
	for _, runtime := range s1bAliasRuntimes() {
		for _, slot := range s1bAliasEffortSlots() {
			for _, tc := range []struct{ name, members string }{
				{"canonical_short_then_alias_long", fmt.Sprintf(`%q:"high",%q:%q`, slot.canonical, slot.alias, long)},
				{"alias_long_then_canonical_short", fmt.Sprintf(`%q:%q,%q:"high"`, slot.alias, long, slot.canonical)},
				{"alias_short_then_canonical_long", fmt.Sprintf(`%q:"high",%q:%q`, slot.alias, slot.canonical, long)},
				{"canonical_long_then_alias_short", fmt.Sprintf(`%q:%q,%q:"high"`, slot.canonical, long, slot.alias)},
				{"alias_long_only", fmt.Sprintf(`%q:%q`, slot.alias, long)},
				{"canonical_long_refusal_control", fmt.Sprintf(`%q:%q`, slot.canonical, long)},
			} {
				for _, phase := range []string{"upload", "apply"} {
					t.Run(runtime.name+"/"+slot.name+"/"+tc.name+"/"+phase, func(t *testing.T) {
						api := packageAPI(t)
						usePackageSeams(t)
						installRosterReload(t, api, nil)
						ws := createWorkspaceViaAPI(t, api, "Research", "notes")
						native := runtime.kind == config.ExecutorKindNative
						raw := s1bAliasRuntimeRecord(t, runtime, slot.wrap(tc.members))
						pkg := s1bAliasPackage(t, raw, native)
						stagingID := ""
						if phase == "apply" {
							control := s1bAliasPackage(t, s1bAliasRuntimeRecord(t, runtime, ""), native)
							stagingID = stageUpload(t, api, control).StagingId
							replaceStagedPackage(t, api, stagingID, pkg)
						}
						before := snapshotHome(t, api.homePath)
						cfgBefore := append([]byte(nil), configBytes(t, api.homePath)...)
						var got packageHTTP
						if phase == "upload" {
							got = callPackageImport(t, api, pkg)
						} else {
							got = callPackageApply(t, api, stagingID, applyBody("new", ws))
						}
						assert.Equal(t, http.StatusUnprocessableEntity, got.Code, "#1952: every overlong effort spelling must be refused before writes; body=%s", got.Body)
						if got.Code == http.StatusUnprocessableEntity {
							er := decodePackageErr(t, got.Body)
							assert.Equal(t, omnpkg.CodeElementNotCreatable, packageErrCode(er), "effort validation, not archive or runtime refusal")
							assert.Contains(t, er.Error, effortLengthSentence, "#1952: a different refusal must not hide a missed length check; body=%s", got.Body)
						}
						assert.Empty(t, agentEntityNames(t, api.homePath), "a length refusal must leave no stored or pending agent")
						assert.Empty(t, listedAgentIDs(t, api), "a length refusal must not publish an agent")
						assert.Equal(t, cfgBefore, configBytes(t, api.homePath), "a length refusal must leave config bytes unchanged")
						requireHomeUnchangedExcept(t, before, snapshotHome(t, api.homePath))
						if phase == "upload" {
							assert.Empty(t, packageStagingIDs(t, api.homePath), "FR-020: refused upload must release staging")
						} else {
							state, err := api.packageRegistry().State(stagingID)
							require.NoError(t, err)
							assert.Equal(t, omnpkg.StagingReady, state, "pre-write apply refusal preserves ready staging")
						}
					})
				}
			}
		}
	}
}

func TestPackagesS1b_EffortCaseAliasControlsRemainImportable(t *testing.T) {
	for _, runtime := range s1bAliasRuntimes() {
		t.Run(runtime.name+"/omitted_control", func(t *testing.T) {
			s1bRequireAliasEffortImported(t, runtime, "", "", nil)
		})
		// Nonempty external primary efforts and any nonempty fallback list
		// are unsupported (FR-047), not successful length-boundary controls.
		slots := s1bAliasEffortSlots()
		if runtime.kind == config.ExecutorKindExternalCLI {
			slots = slots[:2]
		}
		for _, slot := range slots {
			values := []struct{ name, value string }{
				{"empty_min", ""}, {"one", "x"}, {"below_max31", strings.Repeat("x", 31)},
				// 32 Unicode characters, 64 bytes: do not accept a byte-count check.
				{"unicode_max32", strings.Repeat("é", 32)},
			}
			if runtime.kind == config.ExecutorKindExternalCLI && slot.name == "primary" {
				values = values[:1]
			}
			for _, key := range []string{slot.canonical, slot.alias} {
				for _, value := range values {
					t.Run(runtime.name+"/"+slot.name+"/"+key+"/"+value.name, func(t *testing.T) {
						members := slot.wrap(fmt.Sprintf(`%q:%q`, key, value.value))
						wantPrimary := ""
						var wantFallbacks config.FallbackModelSlice
						if slot.name == "primary" {
							wantPrimary = value.value
						}
						if slot.fallback {
							wantFallbacks = config.FallbackModelSlice{{Model: "m-fb", Provider: "openrouter", ReasoningEffort: value.value}}
						}
						// Recap is a raw-rule control, not a claim that agents
						// have a stored recap setting (no such field is promised).
						s1bRequireAliasEffortImported(t, runtime, members, wantPrimary, wantFallbacks)
					})
				}
			}
		}
	}
}

func s1bRequireAliasEffortImported(t *testing.T, runtime s1bAliasRuntime, members, wantPrimary string, wantFallbacks config.FallbackModelSlice) {
	t.Helper()
	api := packageAPI(t)
	usePackageSeams(t)
	installRosterReload(t, api, nil)
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	native := runtime.kind == config.ExecutorKindNative
	pkg := s1bAliasPackage(t, s1bAliasRuntimeRecord(t, runtime, members), native)
	before := snapshotHome(t, api.homePath)
	prev := stageUpload(t, api, pkg)
	require.Len(t, prev.WillAdd, 1)
	require.Empty(t, agentEntityNames(t, api.homePath), "a valid preview still must not store an agent")
	requireHomeUnchangedExcept(t, before, snapshotHome(t, api.homePath))
	got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
	require.Equal(t, http.StatusOK, got.Code, "valid-length control must import; body=%s", got.Body)
	result := decodeResult(t, got.Body)
	require.Len(t, result.Created, 1)
	stored, err := agentstore.New(api.homePath).ReadState(result.Created[0].NewId)
	require.NoError(t, err)
	assert.Equal(t, runtime.agentType, stored.Agent.Type)
	assert.Equal(t, !native, stored.Agent.IsExternalCLIWorker())
	assert.Equal(t, wantPrimary, stored.Agent.ReasoningEffort, "valid native effort must be kept exactly, not catalog-normalized")
	assert.Equal(t, wantFallbacks, stored.Agent.FallbackModels, "valid native fallback effort and model/provider pair must be kept exactly")
	if native {
		assert.Equal(t, s1bAliasSoul, stored.Soul)
	} else {
		assert.Empty(t, stored.Soul, "external success control stays promptless")
	}
	assert.Contains(t, listedAgentIDs(t, api), result.Created[0].NewId, "accepted control must publish the stored agent")
}
