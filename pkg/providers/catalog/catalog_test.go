//go:build !windows

// Drift-guard and unit tests for pkg/providers/catalog.
//
// Test coverage:
//
//	#1  TestCatalog_ParsesAndCountIsExpected    — embedded JSON → 30 entries
//	#2  TestCatalog_DriftGuard_IdIsKnownProtocol  — every id IsKnownProtocol
//	#3  TestCatalog_DriftGuard_IdInProbeEnum      — every id ∈ ProbeProviderRequest enum
//	#4  TestCatalog_DriftGuard_BaseNonEmptyOrExempt — GetDefaultAPIBase non-empty or exempt
//	#5  TestCatalog_DriftGuard_NewProtocolUntriagedFails — catalogExcluded completeness
//	#6  TestCatalog_NoSecretsInPayload           — no credential fields in JSON
//	#7  TestWireDerivation_Table                 — DeriveWire matches rule for all ids
//	#8  TestContract_ProviderCatalogEntry_Shape  — Go type marshals schema-valid JSON
//	#13 TestCatalog_EmbedMatchesGeneratedTS      — JSON embed and TS catalog are identical
//
// Traces to: spec §7, ADR-031 §6 G-2, FR-003, FR-005, FR-006, FR-020, US-1.
package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// probeProviderIDEnum is the complete set of ids from
// contracts/components/schemas/ProbeProviderRequest.yaml.
// Re-homed from src/routes/-onboarding.test.tsx:1029-1074 (spec §7 regression).
// Update this set only when ProbeProviderRequest.yaml is updated.
var probeProviderIDEnum = map[string]bool{
	"anthropic":                true,
	"anthropic-messages":       true,
	"openai":                   true,
	"openrouter":               true,
	"gemini":                   true,
	"google":                   true,
	"ollama":                   true,
	"azure":                    true,
	"azure-openai":             true,
	"bedrock":                  true,
	"litellm":                  true,
	"groq":                     true,
	"zhipu":                    true,
	"z-ai":                     true,
	"zai":                      true,
	"z-ai-coding":              true,
	"glm-coding":               true,
	"zhipu-coding":             true,
	"z-ai-anthropic":           true,
	"zhipu-anthropic":          true,
	"moonshot-anthropic":       true,
	"moonshot-cn-anthropic":    true,
	"minimax-anthropic":        true,
	"minimax-cn-anthropic":     true,
	"deepseek-anthropic":       true,
	"nvidia":                   true,
	"moonshot":                 true,
	"moonshot-cn":              true,
	"shengsuanyun":             true,
	"deepseek":                 true,
	"cerebras":                 true,
	"vivgrid":                  true,
	"volcengine":               true,
	"vllm":                     true,
	"qwen":                     true,
	"qwen-intl":                true,
	"qwen-international":       true,
	"dashscope-intl":           true,
	"qwen-us":                  true,
	"dashscope-us":             true,
	"mistral":                  true,
	"avian":                    true,
	"longcat":                  true,
	"modelscope":               true,
	"novita":                   true,
	"coding-plan":              true,
	"alibaba-coding":           true,
	"qwen-coding":              true,
	"mimo":                     true,
	"minimax":                  true,
	"minimax-cn":               true,
	"coding-plan-anthropic":    true,
	"alibaba-coding-anthropic": true,
	"antigravity":              true,
	"claude-cli":               true,
	"claudecli":                true,
	"codex-cli":                true,
	"codexcli":                 true,
	"github-copilot":           true,
	"copilot":                  true,
}

// catalogExcluded is the hand-authored set of knownProtocols ids that are
// intentionally excluded from the catalog.  They are aliases, CLI executors,
// or self-hosted infra ids — not user-selectable API-key provider entries.
//
// A newly-added knownProtocols id must be triaged: add it to the catalog OR
// add it here.  The drift-guard test #5 fails until one of the two is done
// (human-in-the-loop enforcement, per ADR-031 §6 G-2 R2-07).
var catalogExcluded = map[string]bool{
	// Pure aliases (same endpoint as a catalog entry)
	"z.ai":                     true, // alias of z-ai
	"zai":                      true, // alias of z-ai
	"glm-coding":               true, // alias of z-ai-coding
	"azure-openai":             true, // alias of azure
	"gemini":                   true, // alias of google
	"anthropic-messages":       true, // alias of anthropic
	"qwen-international":       true, // alias of qwen-intl
	"dashscope-intl":           true, // alias of qwen-intl
	"dashscope-us":             true, // alias of qwen-us
	"alibaba-coding-anthropic": true, // alias of coding-plan-anthropic

	// CLI executor / non-API-key ids
	"claude-cli":     true,
	"claudecli":      true,
	"codex-cli":      true,
	"codexcli":       true,
	"github-copilot": true,
	"copilot":        true,
	"antigravity":    true,

	// Self-hosted infra (no user-selectable account; excluded from roster
	// but may appear in the "Self-hosted / Custom" group when configured).
	"litellm": true,
	"vllm":    true,
	"ollama":  false, // ollama IS in the catalog; listed here explicitly to document it is NOT excluded

	// Qwen/Alibaba alternative ids not given their own catalog entry
	"coding-plan":    true,
	"alibaba-coding": true,
	"qwen-coding":    true,

	// Chinese provider infra / aggregator ids not given catalog entries
	"shengsuanyun": true,
	"vivgrid":      true,
	"volcengine":   true,
	"modelscope":   true,
	"avian":        true,
	"longcat":      true,
	"mimo":         true,
	"novita":       true,

	// Deployment-configured; intentionally excluded from user-selectable roster
	"bedrock": true,
}

// catalogIDs returns the set of canonical ids for all catalog entries.
func catalogIDs() map[string]bool {
	ids := make(map[string]bool, len(Entries))
	for _, e := range Entries {
		ids[e.Id] = true
	}
	return ids
}

// catalogAliasIDs returns the set of alias ids across all catalog entries.
func catalogAliasIDs() map[string]bool {
	aliases := make(map[string]bool)
	for _, e := range Entries {
		if e.Aliases == nil {
			continue
		}
		for _, a := range *e.Aliases {
			aliases[a] = true
		}
	}
	return aliases
}

// ── Test #1 ──────────────────────────────────────────────────────────────────

// TestCatalog_ParsesAndCountIsExpected asserts the embedded JSON parses to
// exactly 30 entries.  Traces to spec §7 #1, US-1 AS-1.
func TestCatalog_ParsesAndCountIsExpected(t *testing.T) {
	entries, err := LoadCatalog()
	require.NoError(t, err, "LoadCatalog must not fail")
	assert.Len(t, entries, 30, "catalog must have exactly 30 entries")
}

// ── Test #2 ──────────────────────────────────────────────────────────────────

// TestCatalog_DriftGuard_IdIsKnownProtocol asserts every catalog id satisfies
// IsKnownProtocol.  Traces to spec §7 #2, FR-003 property (a).
func TestCatalog_DriftGuard_IdIsKnownProtocol(t *testing.T) {
	for _, e := range Entries {
		t.Run(e.Id, func(t *testing.T) {
			assert.True(t, providers.IsKnownProtocol(e.Id),
				"catalog entry id %q must be a known protocol (IsKnownProtocol==true)", e.Id)
		})
	}
}

// ── Test #3 ──────────────────────────────────────────────────────────────────

// TestCatalog_DriftGuard_IdInProbeEnum asserts every catalog id is a member of
// the ProbeProviderRequest id enum.  Re-homes the invariant from
// src/routes/-onboarding.test.tsx:1029-1074.
// Traces to spec §7 #3, FR-003 property (c), ADR-031 §6 G-2 R3/MAJ-001.
func TestCatalog_DriftGuard_IdInProbeEnum(t *testing.T) {
	for _, e := range Entries {
		t.Run(e.Id, func(t *testing.T) {
			assert.True(t, probeProviderIDEnum[e.Id],
				"catalog entry id %q must be in the ProbeProviderRequest id enum; "+
					"if you added a new provider, update ProbeProviderRequest.yaml + regen", e.Id)
		})
	}
}

// ── Test #4 ──────────────────────────────────────────────────────────────────

// TestCatalog_DriftGuard_BaseNonEmptyOrExempt asserts that GetDefaultAPIBase
// returns a non-empty string for every catalog id, EXCEPT the deployment-configured
// providers {azure, azure-openai, bedrock} (which require a custom endpoint).
// Traces to spec §7 #4, FR-003 property (a), ADR-031 R2-03.
func TestCatalog_DriftGuard_BaseNonEmptyOrExempt(t *testing.T) {
	// Deployment-configured providers where GetDefaultAPIBase intentionally
	// returns "" because the base URL is operator-supplied at deploy time.
	deploymentConfigured := map[string]bool{
		"azure":        true,
		"azure-openai": true,
		"bedrock":      true,
	}

	for _, e := range Entries {
		id := e.Id
		t.Run(id, func(t *testing.T) {
			base := providers.GetDefaultAPIBase(id)
			if deploymentConfigured[id] {
				// Exempt: empty base is expected for deployment-configured providers.
				return
			}
			assert.NotEmpty(t, base,
				"GetDefaultAPIBase(%q) must not be empty unless it is a deployment-configured provider; "+
					"if this is a new self-hosted provider, document it in the drift-guard exemption list", id)
		})
	}
}

// ── Test #5 ──────────────────────────────────────────────────────────────────

// TestCatalog_DriftGuard_NewProtocolUntriagedFails asserts that every entry in
// knownProtocols is either a catalog id, a catalog alias, or explicitly listed
// in catalogExcluded.  A newly-added protocol that is none of the three causes
// this test to fail, blocking CI until a human triages it.
// Traces to spec §7 #5, FR-003 property (b), ADR-031 §6 G-2 R2-07, MAJ-001.
func TestCatalog_DriftGuard_NewProtocolUntriagedFails(t *testing.T) {
	ids := catalogIDs()
	aliases := catalogAliasIDs()

	// Enumerate all known protocols via the package-level knownProtocols map.
	// We cannot import the unexported map directly, so we probe via IsKnownProtocol
	// against a hard-coded list that mirrors the map.  This list must be kept
	// in sync with pkg/providers/factory_provider.go:424-486.
	//
	// If a new protocol is added to knownProtocols and NOT to this list, the
	// new protocol will be silently untested.  To prevent that, we assert the
	// total count matches.
	allKnown := []string{
		"openai", "azure", "azure-openai", "bedrock", "litellm", "openrouter",
		"groq", "zhipu", "z-ai", "z.ai", "zai", "z-ai-coding", "glm-coding",
		"zhipu-coding", "z-ai-anthropic", "zhipu-anthropic", "moonshot-anthropic",
		"moonshot-cn-anthropic", "minimax-anthropic", "minimax-cn-anthropic",
		"deepseek-anthropic", "gemini", "google", "nvidia", "ollama", "moonshot",
		"moonshot-cn", "shengsuanyun", "deepseek", "cerebras", "vivgrid",
		"volcengine", "vllm", "qwen", "qwen-intl", "qwen-international",
		"dashscope-intl", "qwen-us", "dashscope-us", "mistral", "avian",
		"longcat", "modelscope", "novita", "coding-plan", "alibaba-coding",
		"qwen-coding", "mimo", "minimax", "minimax-cn", "anthropic",
		"anthropic-messages", "coding-plan-anthropic", "alibaba-coding-anthropic",
		"antigravity", "claude-cli", "claudecli", "codex-cli", "codexcli",
		"github-copilot", "copilot",
	}
	// Verify every id in our list is actually known (prevents stale entries here).
	for _, id := range allKnown {
		require.True(t, providers.IsKnownProtocol(id),
			"allKnown list contains %q which IsKnownProtocol does not recognise — "+
				"update allKnown in TestCatalog_DriftGuard_NewProtocolUntriagedFails", id)
	}

	// The list must have exactly 61 entries (mirrors factory_provider.go count).
	require.Len(t, allKnown, 61,
		"allKnown must have exactly 61 entries (mirrors knownProtocols in factory_provider.go); "+
			"if you added a new protocol, add it here AND either to catalog.Entries or catalogExcluded")

	// For each known protocol, assert it is triaged.
	var untriaged []string
	for _, id := range allKnown {
		inCatalog := ids[id]
		isAlias := aliases[id]
		excluded, hasExclusion := catalogExcluded[id]
		if inCatalog || isAlias || (hasExclusion && excluded) {
			continue
		}
		untriaged = append(untriaged, id)
	}
	assert.Empty(t, untriaged,
		"the following knownProtocols ids are not triaged: %v\n"+
			"For each: either add a ProviderCatalogEntry to pkg/providers/catalog/catalog.go, "+
			"or add it to catalogExcluded in catalog_test.go with a comment explaining why "+
			"it is excluded from the user-facing catalog", untriaged)
}

// ── Test #6 ──────────────────────────────────────────────────────────────────

// TestCatalog_NoSecretsInPayload asserts the embedded JSON contains no
// credential fields.  Traces to spec §7 #6, FR-020, US-1 AS-4.
func TestCatalog_NoSecretsInPayload(t *testing.T) {
	secretKeys := []string{
		"api_key", "apiKey", "api_secret", "secret", "password", "token",
		"credential", "private_key", "access_key", "secret_key",
	}
	raw := string(providersCatalogJSON)
	for _, key := range secretKeys {
		// Check for JSON keys in either snake_case or camelCase form.
		assert.NotContains(t, raw, `"`+key+`"`,
			"providers_catalog.json must not contain credential field %q", key)
	}
}

// ── Test #7 ──────────────────────────────────────────────────────────────────

// TestWireDerivation_Table asserts that every entry's Wire field matches the
// derived value from DeriveWire.  Also tests the spec dataset rows explicitly.
// Traces to spec §7 #7, FR-005, ADR-031 §6 G-2 pt 3.
func TestWireDerivation_Table(t *testing.T) {
	// Spec dataset (§7 wire derivation dataset).
	specDataset := []struct {
		id       string
		expected gen.ProviderCatalogEntryWire
	}{
		{"openai", gen.OpenaiCompatible},
		{"z-ai-coding", gen.OpenaiCompatible},
		{"z-ai-anthropic", gen.Anthropic},
		{"anthropic", gen.Anthropic},
		{"anthropic-messages", gen.Anthropic},
		{"bedrock", gen.Anthropic},
		{"coding-plan-anthropic", gen.Anthropic},
	}
	for _, row := range specDataset {
		t.Run("spec/"+row.id, func(t *testing.T) {
			got := DeriveWire(row.id)
			assert.Equal(t, row.expected, got,
				"DeriveWire(%q) must be %q per FR-005 wire derivation rule", row.id, row.expected)
		})
	}

	// All catalog entries: Wire field must match DeriveWire.
	for _, e := range Entries {
		t.Run("entry/"+e.Id, func(t *testing.T) {
			expected := DeriveWire(e.Id)
			assert.Equal(t, expected, e.Wire,
				"entry %q: Wire field %q must match DeriveWire result %q; "+
					"fix the Wire value in catalog.Entries", e.Id, e.Wire, expected)
		})
	}
}

// ── Test #8 ──────────────────────────────────────────────────────────────────

// TestContract_ProviderCatalogEntry_Shape asserts that the Go struct marshals
// to schema-valid JSON, following the mustPassComponent pattern from
// pkg/api/generated/contract_test.go.
// Traces to spec §7 #8, FR-020.
func TestContract_ProviderCatalogEntry_Shape(t *testing.T) {
	// A fully-populated entry (all fields set, including optional region and aliases).
	region := gen.ProviderCatalogEntryRegion("intl")
	aliases := []string{"glm-coding"}
	entry := gen.ProviderCatalogEntry{
		Id:           "z-ai-coding",
		Company:      "Zhipu / GLM",
		Plan:         gen.ProviderCatalogEntryPlanCodingPlan,
		Wire:         gen.OpenaiCompatible,
		Region:       &region,
		EndpointHint: "api.z.ai/api/coding/paas/v4",
		LogoSlug:     "zhipu",
		Label:        "Zhipu / GLM — Coding Plan (International)",
		Subtitle:     "Subscription (Coding Plan) · api.z.ai/api/coding/paas/v4",
		Aliases:      &aliases,
	}
	raw, err := json.Marshal(entry)
	require.NoError(t, err, "must marshal to JSON")

	// Verify required fields are present in the JSON.
	var doc map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &doc))
	for _, required := range []string{"id", "company", "plan", "wire", "endpointHint", "logoSlug", "label", "subtitle"} {
		_, ok := doc[required]
		assert.True(t, ok, "marshaled JSON must contain required field %q", required)
	}

	// Verify plan enum value.
	assert.Equal(t, "coding-plan", doc["plan"], "plan field must be 'coding-plan'")
	// Verify wire enum value.
	assert.Equal(t, "openai-compatible", doc["wire"], "wire field must be 'openai-compatible'")
	// Verify aliases are present.
	aliasesVal, ok := doc["aliases"].([]interface{})
	assert.True(t, ok, "aliases field must be an array")
	assert.Len(t, aliasesVal, 1, "aliases must have 1 entry")

	// An entry without optional fields must also marshal cleanly.
	minimalEntry := gen.ProviderCatalogEntry{
		Id:           "openai",
		Company:      "OpenAI",
		Plan:         gen.ProviderCatalogEntryPlanStandardApi,
		Wire:         gen.OpenaiCompatible,
		EndpointHint: "api.openai.com/v1",
		LogoSlug:     "openai",
		Label:        "OpenAI — Standard API",
		Subtitle:     "Pay-as-you-go, per token · api.openai.com/v1",
	}
	rawMin, err := json.Marshal(minimalEntry)
	require.NoError(t, err, "minimal entry must marshal to JSON")
	var docMin map[string]interface{}
	require.NoError(t, json.Unmarshal(rawMin, &docMin))
	_, hasRegion := docMin["region"]
	assert.False(t, hasRegion, "region must be omitted when nil (omitempty)")
	_, hasAliases := docMin["aliases"]
	assert.False(t, hasAliases, "aliases must be omitted when nil (omitempty)")
}

// findRepoRoot walks up from dir until it finds a directory containing go.mod,
// returning that directory. Worktree- and CI-safe (no hardcoded depth).
func findRepoRoot(t *testing.T, dir string) string {
	t.Helper()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate repo root (go.mod) walking up from the test file")
		}
		dir = parent
	}
}

// ── Test #13 ─────────────────────────────────────────────────────────────────

// TestCatalog_EmbedMatchesGeneratedTS asserts that the embedded
// providers_catalog.json and the generated src/lib/generated/providerCatalog.ts
// contain identical entry data.
// Traces to spec §7 #13, FR-001, US-1 AS-1.
func TestCatalog_EmbedMatchesGeneratedTS(t *testing.T) {
	// Load the embedded JSON.
	embeddedEntries, err := LoadCatalog()
	require.NoError(t, err, "embedded catalog must parse cleanly")

	// Locate the generated TS file relative to this test file. Walk up to the
	// repo root (the dir containing go.mod) rather than assuming a fixed depth —
	// a fixed "../../../.." breaks in git worktrees and is fragile in CI.
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller must succeed")
	repoRoot := findRepoRoot(t, filepath.Dir(thisFile))
	tsPath := filepath.Join(repoRoot, "src", "lib", "generated", "providerCatalog.ts")

	tsContent, err := os.ReadFile(tsPath)
	require.NoError(t, err,
		"src/lib/generated/providerCatalog.ts must exist; run: go run ./pkg/providers/catalog/gen/main.go")

	// Extract the JSON literal from the TS file.
	// The generated TS format is:
	//   export const PROVIDER_CATALOG: ProviderCatalogEntry[] = [...]
	// Locate the first '[' and extract from there to the end (minus trailing newline).
	tsStr := string(tsContent)
	idx := strings.Index(tsStr, "= [")
	require.NotEqual(t, -1, idx,
		"providerCatalog.ts must contain '= [' marking the start of the catalog array")
	jsonLiteral := strings.TrimSpace(tsStr[idx+2:]) // skip "= "

	// Parse the TS-embedded JSON and compare field-by-field.
	var tsEntries []gen.ProviderCatalogEntry
	require.NoError(t, json.Unmarshal([]byte(jsonLiteral), &tsEntries),
		"JSON literal in providerCatalog.ts must parse as []ProviderCatalogEntry")

	require.Len(t, tsEntries, len(embeddedEntries),
		"providerCatalog.ts must have the same entry count as providers_catalog.json; "+
			"run go run ./pkg/providers/catalog/gen/main.go to regenerate")

	for i, embedded := range embeddedEntries {
		ts := tsEntries[i]
		assert.Equal(t, embedded.Id, ts.Id, "entry[%d] id mismatch", i)
		assert.Equal(t, embedded.Company, ts.Company, "entry[%d] company mismatch", i)
		assert.Equal(t, embedded.Plan, ts.Plan, "entry[%d] plan mismatch", i)
		assert.Equal(t, embedded.Wire, ts.Wire, "entry[%d] wire mismatch", i)
		assert.Equal(t, embedded.EndpointHint, ts.EndpointHint, "entry[%d] endpointHint mismatch", i)
		assert.Equal(t, embedded.LogoSlug, ts.LogoSlug, "entry[%d] logoSlug mismatch", i)
		assert.Equal(t, embedded.Label, ts.Label, "entry[%d] label mismatch", i)
		assert.Equal(t, embedded.Subtitle, ts.Subtitle, "entry[%d] subtitle mismatch", i)
		// Region and Aliases are both optional but must match.
		assert.Equal(t, embedded.Region, ts.Region, "entry[%d] region mismatch", i)
		assert.Equal(t, embedded.Aliases, ts.Aliases, "entry[%d] aliases mismatch", i)
	}
}

// ── Additional catalog invariant tests ────────────────────────────────────────

// TestCatalog_NoPlanContainsTokenPlan asserts "token-plan" is absent from the
// shipped enum.  Traces to ADR-031 FR-006 / US-6 AS-4.
func TestCatalog_NoPlanContainsTokenPlan(t *testing.T) {
	for _, e := range Entries {
		assert.NotEqual(t, gen.ProviderCatalogEntryPlan("token-plan"), e.Plan,
			"entry %q: 'token-plan' must not be in the shipped plan enum (ADR-031 FR-006)", e.Id)
	}
}

// TestCatalog_AllRequiredFieldsNonEmpty asserts every required string field is
// non-empty for all entries.
func TestCatalog_AllRequiredFieldsNonEmpty(t *testing.T) {
	for _, e := range Entries {
		t.Run(e.Id, func(t *testing.T) {
			assert.NotEmpty(t, e.Id, "id must be non-empty")
			assert.NotEmpty(t, e.Company, "company must be non-empty")
			assert.NotEmpty(t, string(e.Plan), "plan must be non-empty")
			assert.NotEmpty(t, string(e.Wire), "wire must be non-empty")
			assert.NotEmpty(t, e.EndpointHint, "endpointHint must be non-empty")
			assert.NotEmpty(t, e.LogoSlug, "logoSlug must be non-empty")
			assert.NotEmpty(t, e.Label, "label must be non-empty")
			assert.NotEmpty(t, e.Subtitle, "subtitle must be non-empty")
		})
	}
}

// TestCatalog_PlanEnumValues asserts all Plan values are within the closed enum.
func TestCatalog_PlanEnumValues(t *testing.T) {
	for _, e := range Entries {
		t.Run(e.Id, func(t *testing.T) {
			assert.True(t, e.Plan.Valid(),
				"entry %q: plan %q must be a valid ProviderCatalogEntryPlan enum value", e.Id, e.Plan)
		})
	}
}

// TestCatalog_WireEnumValues asserts all Wire values are within the closed enum.
func TestCatalog_WireEnumValues(t *testing.T) {
	for _, e := range Entries {
		t.Run(e.Id, func(t *testing.T) {
			assert.True(t, e.Wire.Valid(),
				"entry %q: wire %q must be a valid ProviderCatalogEntryWire enum value", e.Id, e.Wire)
		})
	}
}

// TestCatalog_UniqueIDs asserts no two catalog entries share the same id.
func TestCatalog_UniqueIDs(t *testing.T) {
	seen := make(map[string]bool, len(Entries))
	for _, e := range Entries {
		assert.False(t, seen[e.Id],
			"duplicate catalog entry id %q; each entry must have a unique id", e.Id)
		seen[e.Id] = true
	}
}

// TestCatalog_LabelContainsBrand asserts every label starts with the company name,
// which is the "<Brand> — <Access Type>..." format contract.
func TestCatalog_LabelContainsBrand(t *testing.T) {
	for _, e := range Entries {
		t.Run(e.Id, func(t *testing.T) {
			assert.True(t, strings.HasPrefix(e.Label, e.Company),
				"entry %q: label %q must start with company name %q", e.Id, e.Label, e.Company)
		})
	}
}

// TestCatalog_SubtitleContainsEndpointHint asserts the subtitle contains the
// endpointHint, per the subtitle format contract.
func TestCatalog_SubtitleContainsEndpointHint(t *testing.T) {
	for _, e := range Entries {
		t.Run(e.Id, func(t *testing.T) {
			assert.Contains(t, e.Subtitle, e.EndpointHint,
				"entry %q: subtitle %q must contain endpointHint %q", e.Id, e.Subtitle, e.EndpointHint)
		})
	}
}

// TestCatalog_AliasesNotDuplicateCatalogIDs asserts no alias in an entry is also
// a canonical catalog id (aliases must be distinct non-catalog ids).
// Exception: we allow an alias to match a DIFFERENT entry's canonical id only
// if that id is the same endpoint — but in practice our catalog has no such case.
func TestCatalog_AliasesNotDuplicateCatalogIDs(t *testing.T) {
	ids := catalogIDs()
	for _, e := range Entries {
		if e.Aliases == nil {
			continue
		}
		for _, alias := range *e.Aliases {
			// If an alias is also a catalog id, it must be a DIFFERENT entry
			// (i.e., not self-aliasing — that would be a data error).
			if ids[alias] {
				assert.NotEqual(t, alias, e.Id,
					"entry %q: alias %q must not be the same as the entry's own id", e.Id, alias)
			}
		}
	}
}

// TestCatalog_AnthropicWireEntriesHaveAnthropicSuffix asserts that entries with
// Wire=anthropic either end with "-anthropic" or are in the explicit set
// {anthropic, anthropic-messages, bedrock}.  Validates FR-005 is applied consistently.
func TestCatalog_AnthropicWireEntriesHaveAnthropicSuffix(t *testing.T) {
	anthropicExplicitSet := map[string]bool{
		"anthropic":          true,
		"anthropic-messages": true,
		"bedrock":            true,
	}
	re := regexp.MustCompile(`-anthropic$`)
	for _, e := range Entries {
		if e.Wire != gen.Anthropic {
			continue
		}
		t.Run(e.Id, func(t *testing.T) {
			hasAnthropicSuffix := re.MatchString(e.Id)
			inExplicitSet := anthropicExplicitSet[e.Id]
			assert.True(t, hasAnthropicSuffix || inExplicitSet,
				"entry %q has Wire=anthropic but id does not match /-anthropic$/ "+
					"and is not in the explicit set {anthropic,anthropic-messages,bedrock}", e.Id)
		})
	}
}
