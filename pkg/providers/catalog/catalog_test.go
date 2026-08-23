//go:build !windows

// Drift-guard and unit tests for pkg/providers/catalog.
//
// Test coverage:
//
//	#1  TestCatalog_ParsesAndCountIsExpected    — embedded JSON → 23 entries
//	#2  TestCatalog_DriftGuard_IdIsKnownProtocol  — every id IsKnownProtocol
//	#3  TestCatalog_DriftGuard_IdInProbeEnum      — every id AND anthropic_id ∈ ProbeProviderRequest enum
//	#4  TestCatalog_DriftGuard_BaseNonEmptyOrExempt — GetDefaultAPIBase non-empty or exempt
//	#5  TestCatalog_DriftGuard_NewProtocolUntriagedFails — catalogExcluded completeness (anthropic_id counts as covered)
//	#6  TestCatalog_NoSecretsInPayload           — no credential fields in JSON
//	#7  TestWireDerivation_Table                 — DeriveWire matches rule for all ids
//	#8  TestContract_ProviderCatalogEntry_Shape  — Go type marshals schema-valid JSON
//	#9  TestCatalog_AliasEndpointEquality        — alias GetDefaultAPIBase == canonical
//	#10 TestCatalog_LoadCatalogWireConsistency   — LoadCatalog wire-consistency check
//	#11 TestCatalog_LogoSlugCoverage             — every logoSlug has SVG or is intentional lettermark
//	#12 TestCatalog_AnthropicIdEndpointNonEmpty  — GetDefaultAPIBase(anthropic_id) non-empty for every dual-wire entry
//	#13 TestCatalog_EmbedMatchesGeneratedTS      — JSON embed and TS catalog are identical
//
// Traces to: spec §7, ADR-031 §6 G-2, FR-003, FR-005, FR-006, FR-020, US-1,
// docs/internal/specs/provider-ux-fixes-plan.md FIX-4/FIX-5/FIX-6.

package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// catalogExcluded is the hand-authored set of knownProtocols ids that are
// intentionally excluded from the catalog.  They are aliases, CLI executors,
// or self-hosted infra ids — not user-selectable API-key provider entries.
//
// A newly-added knownProtocols id must be triaged: add it to the catalog OR
// add it here.  The drift-guard test #5 fails until one of the two is done
// (human-in-the-loop enforcement, per ADR-031 §6 G-2 R2-07).
var catalogExcluded = map[string]bool{
	// Pure aliases (same endpoint as a catalog entry)
	"z.ai":               true, // alias of z-ai
	"zai":                true, // alias of z-ai
	"glm-coding":         true, // alias of z-ai-coding
	"azure-openai":       true, // alias of azure
	"gemini":             true, // alias of google
	"anthropic-messages": true, // alias of anthropic
	"qwen-international": true, // alias of qwen-intl
	"dashscope-intl":     true, // alias of qwen-intl
	"dashscope-us":       true, // alias of qwen-us
	// alias of coding-plan's AnthropicId sibling (coding-plan-anthropic), not
	// of coding-plan's own OpenAI-compatible endpoint — cannot be a catalog
	// Aliases entry (TestCatalog_AliasEndpointEquality would fail) so it stays
	// triaged here (FIX-5, provider-ux-fixes-plan.md).
	"alibaba-coding-anthropic": true,

	// CLI executor / non-API-key ids
	"codex-cli":   true,
	"antigravity": true,

	// Self-hosted infra (no user-selectable account; excluded from roster
	// but may appear in the "Self-hosted / Custom" group when configured).
	"litellm": true,
	"vllm":    true,
	// ollama IS in the catalog; listed here explicitly to document it is NOT excluded
	"ollama": false,

	// NOTE: "coding-plan", "alibaba-coding", "qwen-coding" moved OUT of this
	// exclusion set — "coding-plan" is now the catalog primary id for the
	// Qwen/Alibaba Coding Plan entry and "alibaba-coding"/"qwen-coding" are
	// its real Aliases (FIX-5 Qwen-coding-plan promotion; see catalog.go
	// "coding-plan" entry comment). The 7 FIX-5 anthropic-wire merge siblings
	// (z-ai-anthropic, zhipu-anthropic, moonshot-anthropic,
	// moonshot-cn-anthropic, minimax-anthropic, minimax-cn-anthropic,
	// deepseek-anthropic) plus coding-plan-anthropic are likewise NOT listed
	// here — they are triaged as covered via catalogAnthropicIDs() below,
	// since each is some entry's AnthropicId.

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

// intentionalLettermark is the set of logoSlugs that intentionally have no
// vendored p_<slug>.svg in src/assets/brand-logos/.
// Companies in this set use a generated lettermark in the UI (BrandIcon renders
// the first letter of the company name).
//
// Adding a new catalog entry with a logoSlug that has no SVG and is not in this
// set will cause TestCatalog_LogoSlugCoverage to fail, prompting the developer
// to either vendor the SVG or explicitly document the intent here.
//
// Verified against the actual vendored files under src/assets/brand-logos/:
//   - azure:    no p_azure.svg   → lettermark
//   - cerebras: no p_cerebras.svg → lettermark
//   - nvidia:   no p_nvidia.svg  → lettermark
//   - ollama:   no p_ollama.svg  → lettermark
var intentionalLettermark = map[string]bool{
	"azure":    true,
	"cerebras": true,
	"nvidia":   true,
	"ollama":   true, // no p_ollama.svg; lettermark intentional
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

// catalogAnthropicIDs returns the set of AnthropicId values across all catalog
// entries — the sibling protocol ids exposing each entry's Anthropic-compatible
// endpoint (FIX-5). A knownProtocols id that equals some entry's AnthropicId is
// catalog-covered even though it is not itself an entry id or a plain alias.
func catalogAnthropicIDs() map[string]bool {
	ids := make(map[string]bool)
	for _, e := range Entries {
		if e.AnthropicId == nil {
			continue
		}
		ids[*e.AnthropicId] = true
	}
	return ids
}

// ── Test #1 ──────────────────────────────────────────────────────────────────

// TestCatalog_ParsesAndCountIsExpected asserts the embedded JSON parses to
// exactly 23 entries (30 - 7 FIX-5 anthropic-wire sibling merges; the Qwen
// Coding Plan promotion renames an existing entry and does not change the
// count).  Traces to spec §7 #1, US-1 AS-1, provider-ux-fixes-plan.md FIX-5.
func TestCatalog_ParsesAndCountIsExpected(t *testing.T) {
	entries, err := LoadCatalog()
	require.NoError(t, err, "LoadCatalog must not fail")
	assert.Len(t, entries, 23, "catalog must have exactly 23 entries")
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

// ── Test #3 — RETIRED ─────────────────────────────────────────────────────────
// TestCatalog_DriftGuard_IdInProbeEnum asserted every catalog id was a member of
// the ProbeProviderRequest.id enum. ADR-067 US-10 / FR-023 removed that enum
// (the id is a free string validated at runtime against the served catalog),
// so the property it guarded no longer exists. The whole hand-typed catalog
// and its drift guards are replaced in T067-02.

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
//
// The real knownProtocols map is accessed via providers.AllKnownProtocols() —
// NOT a hand-copied list — so adding a new id to knownProtocols without
// updating this test still causes CI to fail (the guard is non-vacuous).
//
// A knownProtocols id is triaged when it is a catalog id, a catalog Aliases
// entry, some entry's AnthropicId (FIX-5 — the id is real work, just merged
// into its primary entry instead of shipped as its own row), or explicitly
// listed in catalogExcluded.
//
// Traces to spec §7 #5, FR-003 property (b), ADR-031 §6 G-2 R2-07, MAJ-001,
// provider-ux-fixes-plan.md FIX-6.
func TestCatalog_DriftGuard_NewProtocolUntriagedFails(t *testing.T) {
	ids := catalogIDs()
	aliases := catalogAliasIDs()
	anthropicIDs := catalogAnthropicIDs()

	// Enumerate all known protocols via the reflective accessor, which iterates
	// the real knownProtocols map in pkg/providers/factory_provider.go.
	// Adding a new id to knownProtocols without updating catalogExcluded or
	// catalog.Entries will cause the untriaged assertion below to fail.
	allKnown := providers.AllKnownProtocols()

	// For each known protocol, assert it is triaged.
	var untriaged []string
	for _, id := range allKnown {
		inCatalog := ids[id]
		isAlias := aliases[id]
		isAnthropicSibling := anthropicIDs[id]
		excluded, hasExclusion := catalogExcluded[id]
		if inCatalog || isAlias || isAnthropicSibling || (hasExclusion && excluded) {
			continue
		}
		untriaged = append(untriaged, id)
	}
	assert.Empty(t, untriaged,
		"the following knownProtocols ids are not triaged: %v\n"+
			"For each: either add a ProviderCatalogEntry to pkg/providers/catalog/catalog.go, "+
			"set it as an entry's AnthropicId, or add it to catalogExcluded in catalog_test.go "+
			"with a comment explaining why it is excluded from the user-facing catalog", untriaged)
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
		{"openai", gen.ProviderCatalogEntryWireOpenaiCompatible},
		{"z-ai-coding", gen.ProviderCatalogEntryWireOpenaiCompatible},
		{"z-ai-anthropic", gen.ProviderCatalogEntryWireAnthropic},
		{"anthropic", gen.ProviderCatalogEntryWireAnthropic},
		{"anthropic-messages", gen.ProviderCatalogEntryWireAnthropic},
		{"bedrock", gen.ProviderCatalogEntryWireAnthropic},
		{"coding-plan-anthropic", gen.ProviderCatalogEntryWireAnthropic},
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
	// A fully-populated entry (all fields set, including optional region,
	// aliases, and anthropic_id — the FIX-5 dual-wire sibling field).
	region := gen.ProviderCatalogEntryRegion("intl")
	aliases := []string{"z.ai", "zai"}
	anthropicID := "z-ai-anthropic"
	entry := gen.ProviderCatalogEntry{
		Id:           "z-ai",
		Company:      "Zhipu / GLM",
		Plan:         gen.StandardApi,
		Wire:         gen.ProviderCatalogEntryWireOpenaiCompatible,
		Region:       &region,
		EndpointHint: "api.z.ai/api/paas/v4",
		LogoSlug:     "zhipu",
		Label:        "Zhipu / GLM (International)",
		Subtitle:     "Pay-as-you-go, per token · api.z.ai/api/paas/v4",
		Aliases:      &aliases,
		AnthropicId:  &anthropicID,
	}
	raw, err := json.Marshal(entry)
	require.NoError(t, err, "must marshal to JSON")

	// Verify required fields are present in the JSON.
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	for _, required := range []string{"id", "company", "plan", "wire", "endpointHint", "logoSlug", "label", "subtitle"} {
		_, ok := doc[required]
		assert.True(t, ok, "marshaled JSON must contain required field %q", required)
	}

	// Verify plan enum value.
	assert.Equal(t, "standard-api", doc["plan"], "plan field must be 'standard-api'")
	// Verify wire enum value.
	assert.Equal(t, "openai-compatible", doc["wire"], "wire field must be 'openai-compatible'")
	// Verify aliases are present.
	aliasesVal, ok := doc["aliases"].([]any)
	assert.True(t, ok, "aliases field must be an array")
	assert.Len(t, aliasesVal, 2, "aliases must have 2 entries")
	// Verify anthropic_id (FIX-5) round-trips under its snake_case wire name.
	assert.Equal(t, "z-ai-anthropic", doc["anthropic_id"], "anthropic_id field must round-trip")

	// An entry without optional fields must also marshal cleanly.
	minimalEntry := gen.ProviderCatalogEntry{
		Id:           "openai",
		Company:      "OpenAI",
		Plan:         gen.StandardApi,
		Wire:         gen.ProviderCatalogEntryWireOpenaiCompatible,
		EndpointHint: "api.openai.com/v1",
		LogoSlug:     "openai",
		Label:        "OpenAI",
		Subtitle:     "Pay-as-you-go, per token · api.openai.com/v1",
	}
	rawMin, err := json.Marshal(minimalEntry)
	require.NoError(t, err, "minimal entry must marshal to JSON")
	var docMin map[string]any
	require.NoError(t, json.Unmarshal(rawMin, &docMin))
	_, hasRegion := docMin["region"]
	assert.False(t, hasRegion, "region must be omitted when nil (omitempty)")
	_, hasAliases := docMin["aliases"]
	assert.False(t, hasAliases, "aliases must be omitted when nil (omitempty)")
	_, hasAnthropicID := docMin["anthropic_id"]
	assert.False(t, hasAnthropicID, "anthropic_id must be omitted when nil (omitempty)")
}

// ── Test #9 ──────────────────────────────────────────────────────────────────

// TestCatalog_AliasEndpointEquality asserts that for every catalog entry with
// aliases, GetDefaultAPIBase(alias) == GetDefaultAPIBase(entry.Id) for each
// alias.  The migration resolver assumes aliases share the canonical endpoint;
// this test protects that correctness assumption.
//
// Note: aliases need NOT be ProbeProviderRequest enum members (they are
// display-only ids used by the migration resolver, not probe targets).
//
// Traces to ADR-031 §5 migration resolver, FR-003 property (a).
func TestCatalog_AliasEndpointEquality(t *testing.T) {
	for _, e := range Entries {
		if e.Aliases == nil {
			continue
		}
		canonicalBase := providers.GetDefaultAPIBase(e.Id)
		for _, alias := range *e.Aliases {
			t.Run(e.Id+"/"+alias, func(t *testing.T) {
				aliasBase := providers.GetDefaultAPIBase(alias)
				assert.Equal(t, canonicalBase, aliasBase,
					"alias %q of catalog entry %q must return the same GetDefaultAPIBase as the canonical id; "+
						"the migration resolver relies on this for normalisation — fix GetDefaultAPIBase "+
						"in pkg/providers/factory_provider.go", alias, e.Id)
			})
		}
	}
}

// ── Test #10 ─────────────────────────────────────────────────────────────────

// TestCatalog_LoadCatalogWireConsistency asserts that LoadCatalog() returns
// entries whose Wire field matches DeriveWire(id) for every entry.
// LoadCatalog() itself returns an error on wire mismatch (see catalog.go),
// so this test also validates that the embedded JSON was generated from a
// consistent Entries slice.
// Traces to ADR-031 FR-005, spec §7 #7.
func TestCatalog_LoadCatalogWireConsistency(t *testing.T) {
	entries, err := LoadCatalog()
	require.NoError(t, err,
		"LoadCatalog must not return an error — if it does, the embedded "+
			"providers_catalog.json has a wire mismatch; run go generate ./pkg/providers/catalog/... "+
			"to regenerate from the canonical Entries slice")
	for _, e := range entries {
		t.Run(e.Id, func(t *testing.T) {
			expected := DeriveWire(e.Id)
			assert.Equal(t, expected, e.Wire,
				"LoadCatalog entry %q: Wire field %q must match DeriveWire(%q) = %q; "+
					"regenerate providers_catalog.json via go generate", e.Id, e.Wire, e.Id, expected)
		})
	}
}

// ── Test #11 ─────────────────────────────────────────────────────────────────

// TestCatalog_LogoSlugCoverage asserts that every catalog entry's logoSlug
// either has a vendored p_<slug>.svg file in src/assets/brand-logos/ OR is
// listed in intentionalLettermark.  This catches slug typos and missing assets.
//
// Verified intentional lettermarks (no p_<slug>.svg as of 2026-07):
//   - azure:    no p_azure.svg   → lettermark
//   - cerebras: no p_cerebras.svg → lettermark
//   - nvidia:   no p_nvidia.svg  → lettermark
//   - ollama:   no p_ollama.svg  → lettermark (intentional)
//
// Traces to ADR-031 FR-013, spec §7 #11.
func TestCatalog_LogoSlugCoverage(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller must succeed")
	repoRoot := findRepoRoot(t, filepath.Dir(thisFile))
	logoDir := filepath.Join(repoRoot, "src", "assets", "brand-logos")

	// Build the set of slugs that already have vendored SVGs.
	entries, err := os.ReadDir(logoDir)
	if err != nil {
		// If the logo directory doesn't exist (e.g. in a slim checkout), skip
		// rather than hard-fail — the CI runner has a full checkout.
		t.Skipf("brand-logos directory not found at %s: %v", logoDir, err)
	}
	vendoredSlugs := make(map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "p_") && strings.HasSuffix(name, ".svg") {
			slug := strings.TrimSuffix(strings.TrimPrefix(name, "p_"), ".svg")
			vendoredSlugs[slug] = true
		}
	}

	seen := make(map[string]bool)
	for _, e := range Entries {
		slug := e.LogoSlug
		if seen[slug] {
			continue // already checked this slug
		}
		seen[slug] = true
		t.Run(slug, func(t *testing.T) {
			if vendoredSlugs[slug] || intentionalLettermark[slug] {
				return
			}
			t.Errorf(
				"logoSlug %q (used by entry %q) has no vendored p_%s.svg in src/assets/brand-logos/ "+
					"and is not in intentionalLettermark; either add the SVG or add the slug to "+
					"intentionalLettermark in catalog_test.go with a comment explaining the intent",
				slug, e.Id, slug,
			)
		})
	}
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

// ── Test #12 ─────────────────────────────────────────────────────────────────

// TestCatalog_AnthropicIdEndpointNonEmpty asserts that for every catalog entry
// with a non-nil AnthropicId, GetDefaultAPIBase(anthropicId) returns a
// non-empty base URL — i.e. the sibling Anthropic-compatible endpoint that
// FIX-5's "Endpoint format" toggle would configure actually exists and is
// routable by CreateProviderFromConfig.
// Traces to provider-ux-fixes-plan.md FIX-5/FIX-6.
func TestCatalog_AnthropicIdEndpointNonEmpty(t *testing.T) {
	for _, e := range Entries {
		if e.AnthropicId == nil {
			continue
		}
		t.Run(e.Id, func(t *testing.T) {
			base := providers.GetDefaultAPIBase(*e.AnthropicId)
			assert.NotEmpty(t, base,
				"entry %q: GetDefaultAPIBase(AnthropicId=%q) must not be empty — "+
					"the sibling Anthropic-compatible endpoint must exist", e.Id, *e.AnthropicId)
		})
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
		// Region, Aliases, and AnthropicId are all optional but must match.
		assert.Equal(t, embedded.Region, ts.Region, "entry[%d] region mismatch", i)
		assert.Equal(t, embedded.Aliases, ts.Aliases, "entry[%d] aliases mismatch", i)
		assert.Equal(t, embedded.AnthropicId, ts.AnthropicId, "entry[%d] anthropic_id mismatch", i)
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

// TestCatalog_LabelContainsBrand asserts every label starts with the company
// name — "<Brand>[ (Region)]" for pay-as-you-go entries, "<Brand> — Coding
// Plan[ (Region)]" for coding-plan entries (FIX-4).
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
		if e.Wire != gen.ProviderCatalogEntryWireAnthropic {
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

// ── FIX-4 label-terminology invariants ────────────────────────────────────────
// (docs/internal/specs/provider-ux-fixes-plan.md FIX-4/FIX-6)

// TestCatalog_NoLabelContainsRetiredWording asserts no catalog label contains
// the retired "Standard API" or "Anthropic-compatible" wording. FIX-4 deletes
// "Standard API" everywhere (single-plan entries get no plan suffix at all)
// and FIX-5 makes wire a config detail, never label text.
func TestCatalog_NoLabelContainsRetiredWording(t *testing.T) {
	for _, e := range Entries {
		t.Run(e.Id, func(t *testing.T) {
			assert.NotContains(t, e.Label, "Standard API",
				"entry %q: label %q must not contain retired wording 'Standard API' (FIX-4)", e.Id, e.Label)
			assert.NotContains(t, e.Label, "Anthropic-compatible",
				"entry %q: label %q must not contain retired wording 'Anthropic-compatible' (FIX-5 — "+
					"wire is a config detail, never label text)", e.Id, e.Label)
		})
	}
}

// TestCatalog_LabelContainsCodingPlanIffPlanIsCodingPlan asserts the label
// contains "Coding Plan" if and only if plan == coding-plan (FIX-4/FIX-6).
func TestCatalog_LabelContainsCodingPlanIffPlanIsCodingPlan(t *testing.T) {
	for _, e := range Entries {
		t.Run(e.Id, func(t *testing.T) {
			hasCodingPlanText := strings.Contains(e.Label, "Coding Plan")
			isCodingPlan := e.Plan == gen.CodingPlan
			assert.Equal(t, isCodingPlan, hasCodingPlanText,
				"entry %q: label %q contains 'Coding Plan'=%v but plan=%q (isCodingPlan=%v) — "+
					"these must agree", e.Id, e.Label, hasCodingPlanText, e.Plan, isCodingPlan)
		})
	}
}

// TestCatalog_LabelsUniquePlain asserts every catalog label is globally unique
// under plain string equality. Before FIX-5's wire merge, dual-wire siblings
// relied on the wire descriptor ("Anthropic-compatible") to disambiguate an
// otherwise-identical plan+region label; now that those siblings are merged
// into AnthropicId, uniqueness holds naturally from company+plan+region alone
// with no wire-descriptor crutch needed.
func TestCatalog_LabelsUniquePlain(t *testing.T) {
	seen := make(map[string]string, len(Entries)) // label -> first id that used it
	for _, e := range Entries {
		if prior, ok := seen[e.Label]; ok {
			t.Errorf("duplicate label %q shared by entries %q and %q", e.Label, prior, e.Id)
			continue
		}
		seen[e.Label] = e.Id
	}
}

// ── AnthropicId hardening (LoadCatalog invariants) ────────────────────────────

// TestCatalog_AnthropicIdConsistency exercises the three LoadCatalog
// AnthropicId invariants directly against the factored-out validateEntry /
// validateDisjointIDs helpers, so each rule is unit-testable without
// regenerating providers_catalog.json:
//
//	(a) an anthropic-wire primary cannot also declare a sibling AnthropicId
//	(b) AnthropicId must not equal the entry's own id (self-reference)
//	(c) ids, aliases, and anthropic_id values must be mutually disjoint
//	    across the whole catalog
//
// Each negative case is a deliberately-bad entry constructed in the test.
// Traces to the "LoadCatalog additions" fix-wave item (backend hardening).
func TestCatalog_AnthropicIdConsistency(t *testing.T) {
	t.Run("rule_a_anthropic_wire_primary_with_sibling_is_rejected", func(t *testing.T) {
		anthropicID := "some-anthropic"
		bad := gen.ProviderCatalogEntry{
			Id:          "bad-entry",
			Wire:        gen.ProviderCatalogEntryWireAnthropic, // primary itself claims the anthropic wire
			AnthropicId: &anthropicID,
		}
		err := validateEntry(bad)
		require.Error(t, err, "an anthropic-wire primary must not also declare AnthropicId")
		assert.Contains(t, err.Error(), "bad-entry")
	})

	t.Run("rule_b_self_reference_is_rejected", func(t *testing.T) {
		selfID := "self-ref"
		bad := gen.ProviderCatalogEntry{
			Id:          selfID,
			Wire:        gen.ProviderCatalogEntryWireOpenaiCompatible,
			AnthropicId: &selfID,
		}
		err := validateEntry(bad)
		require.Error(t, err, "AnthropicId must not equal the entry's own id")
		assert.Contains(t, err.Error(), selfID)
	})

	t.Run("rule_c_id_vs_alias_collision_is_rejected", func(t *testing.T) {
		aliases := []string{"collide"}
		bad := []gen.ProviderCatalogEntry{
			{Id: "collide", Wire: gen.ProviderCatalogEntryWireOpenaiCompatible},
			{Id: "other", Wire: gen.ProviderCatalogEntryWireOpenaiCompatible, Aliases: &aliases},
		}
		err := validateDisjointIDs(bad)
		require.Error(t, err, "an id used as another entry's alias must be rejected")
		assert.Contains(t, err.Error(), "collide")
	})

	t.Run("rule_c_anthropic_id_vs_id_collision_is_rejected", func(t *testing.T) {
		anthropicID := "sibling"
		bad := []gen.ProviderCatalogEntry{
			{Id: "sibling", Wire: gen.ProviderCatalogEntryWireOpenaiCompatible},
			{Id: "primary", Wire: gen.ProviderCatalogEntryWireOpenaiCompatible, AnthropicId: &anthropicID},
		}
		err := validateDisjointIDs(bad)
		require.Error(t, err, "an id used as another entry's AnthropicId must be rejected")
		assert.Contains(t, err.Error(), "sibling")
	})

	t.Run("rule_c_duplicate_alias_across_entries_is_rejected", func(t *testing.T) {
		aliasesA := []string{"shared-alias"}
		aliasesB := []string{"shared-alias"}
		bad := []gen.ProviderCatalogEntry{
			{Id: "a", Wire: gen.ProviderCatalogEntryWireOpenaiCompatible, Aliases: &aliasesA},
			{Id: "b", Wire: gen.ProviderCatalogEntryWireOpenaiCompatible, Aliases: &aliasesB},
		}
		err := validateDisjointIDs(bad)
		require.Error(t, err, "two entries must not share the same alias")
		assert.Contains(t, err.Error(), "shared-alias")
	})

	t.Run("positive_control_valid_entry_passes_rules_a_and_b", func(t *testing.T) {
		anthropicID := "z-ai-anthropic"
		aliases := []string{"z.ai", "zai"}
		good := gen.ProviderCatalogEntry{
			Id:          "z-ai",
			Wire:        gen.ProviderCatalogEntryWireOpenaiCompatible,
			AnthropicId: &anthropicID,
			Aliases:     &aliases,
		}
		assert.NoError(t, validateEntry(good),
			"a valid openai-wire entry with a distinct AnthropicId must pass rules (a)/(b)")
		assert.NoError(t, validateDisjointIDs([]gen.ProviderCatalogEntry{good}),
			"a single valid entry with distinct id/aliases/anthropic_id must pass the disjointness check")
	})

	t.Run("real_catalog_passes_all_three_rules", func(t *testing.T) {
		for _, e := range Entries {
			assert.NoError(t, validateEntry(e), "entry %q must pass validateEntry", e.Id)
		}
		assert.NoError(t, validateDisjointIDs(Entries), "the real catalog must pass the global disjointness check")
	})
}

// TestValidateCatalog_NegativeCases feeds deliberately-corrupted
// []gen.ProviderCatalogEntry slices through validateCatalog — the exact
// validation path LoadCatalog runs against the unmarshaled embedded JSON —
// and asserts each specific error fires. Using validateCatalog directly
// (rather than corrupting providers_catalog.json on disk) means these tests
// don't need to fake the go:embed FS.
//
// Covers: FR-005 wire mismatch, AnthropicId wire mismatch, AnthropicId not a
// known protocol, rule (a) anthropic-wire owner with a sibling, rule (b)
// self-reference, rule (c) duplicate ids — plus a positive control.
func TestValidateCatalog_NegativeCases(t *testing.T) {
	t.Run("wire_mismatch_fails", func(t *testing.T) {
		// "openai" derives to openai-compatible; claiming Anthropic is a lie.
		bad := []gen.ProviderCatalogEntry{
			{Id: "openai", Wire: gen.ProviderCatalogEntryWireAnthropic},
		}
		err := validateCatalog(bad)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not match DeriveWire")
	})

	t.Run("anthropic_id_wire_mismatch_fails", func(t *testing.T) {
		// AnthropicId "openai" derives to openai-compatible, not anthropic.
		anthropicID := "openai"
		bad := []gen.ProviderCatalogEntry{
			{Id: "z-ai", Wire: gen.ProviderCatalogEntryWireOpenaiCompatible, AnthropicId: &anthropicID},
		}
		err := validateCatalog(bad)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must derive to the anthropic wire")
	})

	t.Run("anthropic_id_unknown_protocol_fails", func(t *testing.T) {
		// Ends in "-anthropic" so DeriveWire says anthropic, but it is not in
		// knownProtocols — an unroutable sibling.
		anthropicID := "totally-fake-anthropic"
		bad := []gen.ProviderCatalogEntry{
			{Id: "z-ai", Wire: gen.ProviderCatalogEntryWireOpenaiCompatible, AnthropicId: &anthropicID},
		}
		err := validateCatalog(bad)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is not a known protocol")
	})

	t.Run("rule_a_anthropic_wire_owner_with_sibling_fails", func(t *testing.T) {
		anthropicID := "z-ai-anthropic"
		bad := []gen.ProviderCatalogEntry{
			{Id: "anthropic", Wire: gen.ProviderCatalogEntryWireAnthropic, AnthropicId: &anthropicID},
		}
		err := validateCatalog(bad)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be")
		assert.Contains(t, err.Error(), "openai-compatible")
	})

	t.Run("rule_b_self_reference_fails", func(t *testing.T) {
		selfID := "z-ai"
		bad := []gen.ProviderCatalogEntry{
			{Id: selfID, Wire: gen.ProviderCatalogEntryWireOpenaiCompatible, AnthropicId: &selfID},
		}
		err := validateCatalog(bad)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "self-reference")
	})

	t.Run("rule_c_duplicate_ids_fail", func(t *testing.T) {
		bad := []gen.ProviderCatalogEntry{
			{Id: "openai", Wire: gen.ProviderCatalogEntryWireOpenaiCompatible},
			{Id: "openai", Wire: gen.ProviderCatalogEntryWireOpenaiCompatible},
		}
		err := validateCatalog(bad)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "claimed twice")
	})

	t.Run("positive_control_valid_slice_passes", func(t *testing.T) {
		anthropicID := "z-ai-anthropic"
		aliases := []string{"z.ai", "zai"}
		good := []gen.ProviderCatalogEntry{
			{Id: "openai", Wire: gen.ProviderCatalogEntryWireOpenaiCompatible},
			{Id: "z-ai", Wire: gen.ProviderCatalogEntryWireOpenaiCompatible, AnthropicId: &anthropicID, Aliases: &aliases},
		}
		assert.NoError(t, validateCatalog(good), "a clean, mutually-disjoint slice must pass validateCatalog")
	})

	t.Run("real_catalog_passes_validateCatalog", func(t *testing.T) {
		assert.NoError(t, validateCatalog(Entries), "the real catalog.Entries must pass validateCatalog")
	})
}
