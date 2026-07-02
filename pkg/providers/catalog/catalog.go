// Package catalog is the backend-owned single source of truth for the 23
// user-facing LLM provider variants available in the Omnipus picker.
//
// # Design
//
// Entries is a hand-authored slice of ProviderCatalogEntry values. Each entry
// covers one billable endpoint identified by (company × plan × region). Wire
// (OpenAI- vs Anthropic-compatible) is NOT a separate catalog row: per
// docs/internal/specs/provider-ux-fixes-plan.md FIX-5, one account/API key
// commonly exposes both an OpenAI-compatible and an Anthropic-compatible base
// URL, so the Anthropic-compatible sibling id is folded into its primary
// entry's AnthropicId field rather than shipped as its own row. The catalog
// was 30 entries before this merge (7 pure anthropic-wire siblings absorbed:
// z-ai-anthropic, zhipu-anthropic, moonshot-anthropic, moonshot-cn-anthropic,
// minimax-anthropic, minimax-cn-anthropic, deepseek-anthropic; plus the Qwen
// Coding Plan entry renamed from coding-plan-anthropic to coding-plan with
// coding-plan-anthropic demoted to its AnthropicId — see the "coding-plan"
// entry below for the GetDefaultAPIBase / ProbeProviderRequest check that
// justified promoting it to primary).
//
// The slice is serialized to pkg/providers/catalog/data/providers_catalog.json
// and a matching TypeScript constant in src/lib/generated/providerCatalog.ts by
// the generator in pkg/providers/catalog/gen/main.go (invoked via go:generate).
// Both artifacts are committed and consumed at build time — NO live HTTP endpoint
// serves the catalog (ADR-031 FR-016, §6 G-2).
//
// The embedded JSON is exposed through LoadCatalog(), which the drift-guard
// tests and any Go consumer should use rather than reading Entries directly, so
// that the round-trip through JSON is validated (the generator test compares
// both representations).
//
// # Wire derivation rule (FR-005)
//
//	wire = "anthropic" when:
//	  - id matches /-anthropic$/, OR
//	  - id ∈ {anthropic, anthropic-messages, bedrock}
//	otherwise "openai-compatible"
//
// Wire is set explicitly in Entries so the rule is checkable by TestWireDerivation_Table.
// Every primary entry's own Wire is validated against DeriveWire(id); when an
// entry carries an AnthropicId, that sibling id is independently validated to
// derive to "anthropic" and to be a known protocol (see LoadCatalog).
//
// # Alias convention
//
// aliases lists additional protocol ids (from knownProtocols) that resolve to
// the same endpoint as the canonical id.  Derived by inspecting GetDefaultAPIBase:
// ids grouped under the same case share a base URL and are aliases of each other.
// The migration resolver uses aliases to normalise a stored alias id to the
// catalog's canonical id before display.
//
// # go:generate
//
//go:generate go run ./gen/main.go
package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// providersCatalogJSON is the embedded generated JSON artifact.
// It is produced by running go generate ./pkg/providers/catalog/...
// and must be committed alongside any change to Entries.
//
//go:embed data/providers_catalog.json
var providersCatalogJSON []byte

// wire returns the wire protocol for the given provider id per FR-005.
// "anthropic" when id ends with "-anthropic" or id is in the explicit set;
// otherwise "openai-compatible".
func wire(id string) gen.ProviderCatalogEntryWire {
	if strings.HasSuffix(id, "-anthropic") {
		return gen.Anthropic
	}
	switch id {
	case "anthropic", "anthropic-messages", "bedrock":
		return gen.Anthropic
	}
	return gen.OpenaiCompatible
}

// region constructs a *gen.ProviderCatalogEntryRegion from a string, or nil.
func regionPtr(s string) *gen.ProviderCatalogEntryRegion {
	r := gen.ProviderCatalogEntryRegion(s)
	return &r
}

// aliasesPtr wraps a variadic string list as *[]string, or nil when empty.
func aliasesPtr(aa ...string) *[]string {
	if len(aa) == 0 {
		return nil
	}
	return &aa
}

// anthropicIDPtr wraps a sibling Anthropic-wire protocol id as *string, or
// nil when the entry has no Anthropic-compatible sibling endpoint (FIX-5).
func anthropicIDPtr(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

// entry constructs a ProviderCatalogEntry using the provided fields.
// w is passed explicitly so the caller documents the expected wire protocol;
// the drift-guard test asserts it matches the derivation rule. anthropicID,
// when non-empty, is the sibling protocol id exposing the Anthropic-compatible
// endpoint for the same account/API key (FIX-5) — set AnthropicId, never a
// separate catalog row.
func entry(
	id, company string,
	plan gen.ProviderCatalogEntryPlan,
	w gen.ProviderCatalogEntryWire,
	region *gen.ProviderCatalogEntryRegion,
	endpointHint, logoSlug, label, subtitle string,
	aliases *[]string,
	anthropicID string,
) gen.ProviderCatalogEntry {
	return gen.ProviderCatalogEntry{
		Id:           id,
		Company:      company,
		Plan:         plan,
		Wire:         w,
		Region:       region,
		EndpointHint: endpointHint,
		LogoSlug:     logoSlug,
		Label:        label,
		Subtitle:     subtitle,
		Aliases:      aliases,
		AnthropicId:  anthropicIDPtr(anthropicID),
	}
}

// companyLabel returns a plain "<Brand> [(Region)]" label for a pay-as-you-go
// (standard-api) entry — NO plan-name suffix. Wire (OpenAI- vs
// Anthropic-compatible) is a config detail, never label text (FIX-4/FIX-5,
// docs/internal/specs/provider-ux-fixes-plan.md): "Standard API" and
// "Anthropic-compatible" must never appear in a catalog label.
func companyLabel(brand string, region *gen.ProviderCatalogEntryRegion) string {
	if region == nil {
		return brand
	}
	return brand + " (" + regionDisplayName(*region) + ")"
}

// codingPlanLabel returns a "<Brand> — Coding Plan [(Region)]" label.
func codingPlanLabel(brand string, region *gen.ProviderCatalogEntryRegion) string {
	if region == nil {
		return brand + " — Coding Plan"
	}
	return brand + " — Coding Plan (" + regionDisplayName(*region) + ")"
}

func regionDisplayName(r gen.ProviderCatalogEntryRegion) string {
	switch r {
	case gen.Intl:
		return "International"
	case gen.China:
		return "China"
	case gen.Us:
		return "US"
	default:
		return string(r)
	}
}

// subtitleAPI returns the subtitle for a pay-as-you-go (standard-api) entry.
func subtitleAPI(hint string) string {
	return "Pay-as-you-go, per token · " + hint
}

// subtitleCoding returns the subtitle for a Coding Plan entry.
func subtitleCoding(hint string) string {
	return "Subscription (Coding Plan) · " + hint
}

// Entries is the authoritative Go slice of user-facing provider variants.
// Companies with no vendored SVG use a short lettermark-fallback logoSlug
// (azure, cerebras, nvidia, ollama — BrandIcon will render a lettermark for
// those; ollama has no p_ollama.svg so it lettermarks intentionally).
//
// Ordering: single-option companies first, then multi-variant families in the
// same order (Zhipu, Moonshot, MiniMax, DeepSeek, Qwen/Alibaba). Dual-wire
// families carry their Anthropic-compatible sibling id in AnthropicId rather
// than as a separate row (FIX-5).
var Entries = []gen.ProviderCatalogEntry{
	// ── Single-option companies ───────────────────────────────────────────────

	entry(
		"openai", "OpenAI",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("openai"),
		nil,
		"api.openai.com/v1",
		"openai",
		companyLabel("OpenAI", nil),
		subtitleAPI("api.openai.com/v1"),
		nil,
		"",
	),
	entry(
		"anthropic", "Anthropic",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("anthropic"),
		nil,
		"api.anthropic.com/v1",
		"anthropic",
		companyLabel("Anthropic", nil),
		subtitleAPI("api.anthropic.com/v1"),
		aliasesPtr("anthropic-messages"),
		"",
	),
	entry(
		"google", "Google Gemini",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("google"),
		nil,
		"generativelanguage.googleapis.com",
		"gemini",
		companyLabel("Google Gemini", nil),
		subtitleAPI("generativelanguage.googleapis.com"),
		aliasesPtr("gemini"),
		"",
	),
	entry(
		"openrouter", "OpenRouter",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("openrouter"),
		nil,
		"openrouter.ai/api/v1",
		"openrouter",
		companyLabel("OpenRouter", nil),
		subtitleAPI("openrouter.ai/api/v1"),
		nil,
		"",
	),
	entry(
		"groq", "Groq",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("groq"),
		nil,
		"api.groq.com/openai/v1",
		"groq",
		companyLabel("Groq", nil),
		subtitleAPI("api.groq.com/openai/v1"),
		nil,
		"",
	),
	entry(
		"mistral", "Mistral",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("mistral"),
		nil,
		"api.mistral.ai/v1",
		"mistral",
		companyLabel("Mistral", nil),
		subtitleAPI("api.mistral.ai/v1"),
		nil,
		"",
	),
	entry(
		"nvidia", "NVIDIA",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("nvidia"),
		nil,
		"integrate.api.nvidia.com/v1",
		"nvidia",
		companyLabel("NVIDIA", nil),
		subtitleAPI("integrate.api.nvidia.com/v1"),
		nil,
		"",
	),
	entry(
		"cerebras", "Cerebras",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("cerebras"),
		nil,
		"api.cerebras.ai/v1",
		"cerebras",
		companyLabel("Cerebras", nil),
		subtitleAPI("api.cerebras.ai/v1"),
		nil,
		"",
	),
	entry(
		"ollama", "Ollama (local)",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("ollama"),
		nil,
		"localhost:11434",
		"ollama",
		companyLabel("Ollama (local)", nil),
		subtitleAPI("localhost:11434"),
		nil,
		"",
	),
	entry(
		"azure", "Azure OpenAI",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("azure"),
		nil,
		"<resource>.openai.azure.com",
		"azure",
		companyLabel("Azure OpenAI", nil),
		subtitleAPI("<resource>.openai.azure.com"),
		aliasesPtr("azure-openai"),
		"",
	),

	// ── Zhipu / GLM (plan × region; anthropic-wire siblings merged, FIX-5) ────

	entry(
		"z-ai", "Zhipu / GLM",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("z-ai"),
		regionPtr("intl"),
		"api.z.ai/api/paas/v4",
		"zhipu",
		companyLabel("Zhipu / GLM", regionPtr("intl")),
		subtitleAPI("api.z.ai/api/paas/v4"),
		aliasesPtr("z.ai", "zai"),
		"z-ai-anthropic",
	),
	entry(
		"zhipu", "Zhipu / GLM",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("zhipu"),
		regionPtr("china"),
		"open.bigmodel.cn/api/paas/v4",
		"zhipu",
		companyLabel("Zhipu / GLM", regionPtr("china")),
		subtitleAPI("open.bigmodel.cn/api/paas/v4"),
		nil,
		"zhipu-anthropic",
	),
	entry(
		"z-ai-coding", "Zhipu / GLM",
		gen.ProviderCatalogEntryPlanCodingPlan,
		wire("z-ai-coding"),
		regionPtr("intl"),
		"api.z.ai/api/coding/paas/v4",
		"zhipu",
		codingPlanLabel("Zhipu / GLM", regionPtr("intl")),
		subtitleCoding("api.z.ai/api/coding/paas/v4"),
		aliasesPtr("glm-coding"),
		"",
	),
	entry(
		"zhipu-coding", "Zhipu / GLM",
		gen.ProviderCatalogEntryPlanCodingPlan,
		wire("zhipu-coding"),
		regionPtr("china"),
		"open.bigmodel.cn/api/coding/paas/v4",
		"zhipu",
		codingPlanLabel("Zhipu / GLM", regionPtr("china")),
		subtitleCoding("open.bigmodel.cn/api/coding/paas/v4"),
		nil,
		"",
	),

	// ── Moonshot / Kimi (plan × region; anthropic-wire siblings merged) ──────

	entry(
		"moonshot", "Moonshot / Kimi",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("moonshot"),
		regionPtr("intl"),
		"api.moonshot.ai/v1",
		"kimi",
		companyLabel("Moonshot / Kimi", regionPtr("intl")),
		subtitleAPI("api.moonshot.ai/v1"),
		nil,
		"moonshot-anthropic",
	),
	entry(
		"moonshot-cn", "Moonshot / Kimi",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("moonshot-cn"),
		regionPtr("china"),
		"api.moonshot.cn/v1",
		"kimi",
		companyLabel("Moonshot / Kimi", regionPtr("china")),
		subtitleAPI("api.moonshot.cn/v1"),
		nil,
		"moonshot-cn-anthropic",
	),

	// ── MiniMax (plan × region; anthropic-wire siblings merged) ──────────────

	entry(
		"minimax", "MiniMax",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("minimax"),
		regionPtr("intl"),
		"api.minimax.io/v1",
		"minimax",
		companyLabel("MiniMax", regionPtr("intl")),
		subtitleAPI("api.minimax.io/v1"),
		nil,
		"minimax-anthropic",
	),
	entry(
		"minimax-cn", "MiniMax",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("minimax-cn"),
		regionPtr("china"),
		"api.minimaxi.com/v1",
		"minimax",
		companyLabel("MiniMax", regionPtr("china")),
		subtitleAPI("api.minimaxi.com/v1"),
		nil,
		"minimax-cn-anthropic",
	),

	// ── DeepSeek (plan only, no region; anthropic-wire sibling merged) ───────

	entry(
		"deepseek", "DeepSeek",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("deepseek"),
		nil,
		"api.deepseek.com/v1",
		"deepseek",
		companyLabel("DeepSeek", nil),
		subtitleAPI("api.deepseek.com/v1"),
		nil,
		"deepseek-anthropic",
	),

	// ── Qwen / Alibaba (plan × region) ────────────────────────────────────────

	entry(
		"qwen", "Qwen / Alibaba",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("qwen"),
		regionPtr("china"),
		"dashscope.aliyuncs.com/compatible-mode/v1",
		"qwen",
		companyLabel("Qwen / Alibaba", regionPtr("china")),
		subtitleAPI("dashscope.aliyuncs.com/compatible-mode/v1"),
		nil,
		"",
	),
	entry(
		"qwen-intl", "Qwen / Alibaba",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("qwen-intl"),
		regionPtr("intl"),
		"dashscope-intl.aliyuncs.com/compatible-mode/v1",
		"qwen",
		companyLabel("Qwen / Alibaba", regionPtr("intl")),
		subtitleAPI("dashscope-intl.aliyuncs.com/compatible-mode/v1"),
		aliasesPtr("qwen-international", "dashscope-intl"),
		"",
	),
	entry(
		"qwen-us", "Qwen / Alibaba",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("qwen-us"),
		regionPtr("us"),
		"dashscope-us.aliyuncs.com/compatible-mode/v1",
		"qwen",
		companyLabel("Qwen / Alibaba", regionPtr("us")),
		subtitleAPI("dashscope-us.aliyuncs.com/compatible-mode/v1"),
		aliasesPtr("dashscope-us"),
		"",
	),
	// "coding-plan" is the promoted primary for the Qwen/Alibaba Coding Plan
	// (formerly a standalone "coding-plan-anthropic" entry). Both
	// GetDefaultAPIBase("coding-plan") (non-empty, pkg/providers/factory_provider.go)
	// and ProbeProviderRequest's id enum (contracts/components/schemas/ProbeProviderRequest.yaml)
	// carry "coding-plan" as an OpenAI-compatible endpoint, so it satisfies the
	// same primary-entry contract as every other id in this catalog; the
	// Anthropic-compatible endpoint (coding-intl.dashscope.aliyuncs.com/apps/anthropic)
	// is demoted to AnthropicId. "alibaba-coding" and "qwen-coding" share
	// coding-plan's GetDefaultAPIBase case and are promoted from
	// catalogExcluded to real aliases; "alibaba-coding-anthropic" (the former
	// alias of coding-plan-anthropic) stays excluded — see catalog_test.go.
	entry(
		"coding-plan", "Qwen / Alibaba",
		gen.ProviderCatalogEntryPlanCodingPlan,
		wire("coding-plan"),
		nil,
		"coding-intl.dashscope.aliyuncs.com/v1",
		"qwen",
		codingPlanLabel("Qwen / Alibaba", nil),
		subtitleCoding("coding-intl.dashscope.aliyuncs.com/v1"),
		aliasesPtr("alibaba-coding", "qwen-coding"),
		"coding-plan-anthropic",
	),
}

// LoadCatalog unmarshals the embedded providers_catalog.json into a slice of
// ProviderCatalogEntry values.  Call this instead of reading Entries directly
// when you need the round-trip guarantee (generator test compares both).
//
// Wire consistency is validated at load time (FR-005): if any entry's Wire
// field does not match DeriveWire(entry.Id), an error is returned.  This
// ensures ANY consumer of the embedded JSON — not just in-repo Entries — gets
// the wire-consistency guarantee at runtime rather than only at CI time.
//
// AnthropicId consistency is also validated (FIX-5): when an entry carries a
// non-nil AnthropicId, that sibling id must itself derive to the "anthropic"
// wire (it is the Anthropic-compatible endpoint for the same account/key) and
// must be a known protocol — otherwise the config/probe path would accept a
// choice that CreateProviderFromConfig cannot route.
func LoadCatalog() ([]gen.ProviderCatalogEntry, error) {
	var out []gen.ProviderCatalogEntry
	if err := json.Unmarshal(providersCatalogJSON, &out); err != nil {
		return nil, fmt.Errorf("catalog: unmarshal providers_catalog.json: %w", err)
	}
	for i, e := range out {
		expected := DeriveWire(e.Id)
		if e.Wire != expected {
			return nil, fmt.Errorf(
				"catalog: entry[%d] id=%q: Wire=%q does not match DeriveWire=%q (FR-005); "+
					"run go generate ./pkg/providers/catalog/... to regenerate providers_catalog.json",
				i, e.Id, e.Wire, expected,
			)
		}
		if e.AnthropicId == nil {
			continue
		}
		anthropicID := *e.AnthropicId
		if got := DeriveWire(anthropicID); got != gen.Anthropic {
			return nil, fmt.Errorf(
				"catalog: entry[%d] id=%q: AnthropicId=%q must derive to the anthropic wire, got %q (FIX-5); "+
					"run go generate ./pkg/providers/catalog/... to regenerate providers_catalog.json",
				i, e.Id, anthropicID, got,
			)
		}
		if !providers.IsKnownProtocol(anthropicID) {
			return nil, fmt.Errorf(
				"catalog: entry[%d] id=%q: AnthropicId=%q is not a known protocol (FIX-5); "+
					"add it to knownProtocols in pkg/providers/factory_provider.go",
				i, e.Id, anthropicID,
			)
		}
	}
	return out, nil
}

// DeriveWire returns the wire protocol for a given protocol id using FR-005.
// Exported so the drift-guard test can validate every Entries row against the rule.
func DeriveWire(id string) gen.ProviderCatalogEntryWire {
	return wire(id)
}
