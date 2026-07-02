// Package catalog is the backend-owned single source of truth for the ~30
// user-facing LLM provider variants available in the Omnipus picker.
//
// # Design
//
// Entries is a hand-authored slice of ProviderCatalogEntry values seeded from
// the 30 variants that previously lived in src/routes/onboarding.tsx as
// AVAILABLE_PROVIDERS.  Each entry covers one billable endpoint identified by
// (company × plan × region).
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

// entry constructs a ProviderCatalogEntry using the provided fields.
// w is passed explicitly so the caller documents the expected wire protocol;
// the drift-guard test asserts it matches the derivation rule.
func entry(
	id, company string,
	plan gen.ProviderCatalogEntryPlan,
	w gen.ProviderCatalogEntryWire,
	region *gen.ProviderCatalogEntryRegion,
	endpointHint, logoSlug, label, subtitle string,
	aliases *[]string,
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
	}
}

// standardAPILabel returns a "<Brand> — Standard API [(Region)]" label.
func standardAPILabel(brand string, region *gen.ProviderCatalogEntryRegion) string {
	if region == nil {
		return brand + " — Standard API"
	}
	return brand + " — Standard API (" + regionDisplayName(*region) + ")"
}

// codingPlanLabel returns a "<Brand> — Coding Plan [(Region)]" label.
func codingPlanLabel(brand string, region *gen.ProviderCatalogEntryRegion) string {
	if region == nil {
		return brand + " — Coding Plan"
	}
	return brand + " — Coding Plan (" + regionDisplayName(*region) + ")"
}

// anthropicLabel returns a "<Brand> — Anthropic-compatible [(Region)]" label
// for entries that use the Anthropic wire protocol.
func anthropicLabel(brand string, region *gen.ProviderCatalogEntryRegion) string {
	if region == nil {
		return brand + " — Anthropic-compatible"
	}
	return brand + " — Anthropic-compatible (" + regionDisplayName(*region) + ")"
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

// subtitleAPI returns the subtitle for a Standard API entry.
func subtitleAPI(hint string) string {
	return "Pay-as-you-go, per token · " + hint
}

// subtitleCoding returns the subtitle for a Coding Plan entry.
func subtitleCoding(hint string) string {
	return "Subscription (Coding Plan) · " + hint
}

// subtitleAnthropic returns the subtitle for an Anthropic-compatible entry.
func subtitleAnthropic(hint string) string {
	return "Pay-as-you-go, per token (Anthropic-compatible) · " + hint
}

// Entries is the authoritative Go slice of user-facing provider variants.
// Seeded from the 30 entries previously in onboarding.tsx AVAILABLE_PROVIDERS.
// Companies with no vendored SVG use a short lettermark-fallback logoSlug
// (azure, cerebras, nvidia, ollama — BrandIcon will render a lettermark for
// those; ollama has no p_ollama.svg so it lettermarks intentionally).
//
// Ordering mirrors AVAILABLE_PROVIDERS: single-option companies first, then
// multi-variant families in the same order (Zhipu, Moonshot, MiniMax, DeepSeek,
// Qwen/Alibaba).
var Entries = []gen.ProviderCatalogEntry{
	// ── Single-option companies ───────────────────────────────────────────────

	entry(
		"openai", "OpenAI",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("openai"),
		nil,
		"api.openai.com/v1",
		"openai",
		standardAPILabel("OpenAI", nil),
		subtitleAPI("api.openai.com/v1"),
		nil,
	),
	entry(
		"anthropic", "Anthropic",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("anthropic"),
		nil,
		"api.anthropic.com/v1",
		"anthropic",
		anthropicLabel("Anthropic", nil),
		subtitleAnthropic("api.anthropic.com/v1"),
		aliasesPtr("anthropic-messages"),
	),
	entry(
		"google", "Google Gemini",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("google"),
		nil,
		"generativelanguage.googleapis.com",
		"gemini",
		standardAPILabel("Google Gemini", nil),
		subtitleAPI("generativelanguage.googleapis.com"),
		aliasesPtr("gemini"),
	),
	entry(
		"openrouter", "OpenRouter",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("openrouter"),
		nil,
		"openrouter.ai/api/v1",
		"openrouter",
		standardAPILabel("OpenRouter", nil),
		subtitleAPI("openrouter.ai/api/v1"),
		nil,
	),
	entry(
		"groq", "Groq",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("groq"),
		nil,
		"api.groq.com/openai/v1",
		"groq",
		standardAPILabel("Groq", nil),
		subtitleAPI("api.groq.com/openai/v1"),
		nil,
	),
	entry(
		"mistral", "Mistral",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("mistral"),
		nil,
		"api.mistral.ai/v1",
		"mistral",
		standardAPILabel("Mistral", nil),
		subtitleAPI("api.mistral.ai/v1"),
		nil,
	),
	entry(
		"nvidia", "NVIDIA",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("nvidia"),
		nil,
		"integrate.api.nvidia.com/v1",
		"nvidia",
		standardAPILabel("NVIDIA", nil),
		subtitleAPI("integrate.api.nvidia.com/v1"),
		nil,
	),
	entry(
		"cerebras", "Cerebras",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("cerebras"),
		nil,
		"api.cerebras.ai/v1",
		"cerebras",
		standardAPILabel("Cerebras", nil),
		subtitleAPI("api.cerebras.ai/v1"),
		nil,
	),
	entry(
		"ollama", "Ollama (local)",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("ollama"),
		nil,
		"localhost:11434",
		"ollama",
		standardAPILabel("Ollama (local)", nil),
		subtitleAPI("localhost:11434"),
		nil,
	),
	entry(
		"azure", "Azure OpenAI",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("azure"),
		nil,
		"<resource>.openai.azure.com",
		"azure",
		standardAPILabel("Azure OpenAI", nil),
		subtitleAPI("<resource>.openai.azure.com"),
		aliasesPtr("azure-openai"),
	),

	// ── Zhipu / GLM (plan × region) ──────────────────────────────────────────

	entry(
		"z-ai", "Zhipu / GLM",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("z-ai"),
		regionPtr("intl"),
		"api.z.ai/api/paas/v4",
		"zhipu",
		standardAPILabel("Zhipu / GLM", regionPtr("intl")),
		subtitleAPI("api.z.ai/api/paas/v4"),
		aliasesPtr("z.ai", "zai"),
	),
	entry(
		"zhipu", "Zhipu / GLM",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("zhipu"),
		regionPtr("china"),
		"open.bigmodel.cn/api/paas/v4",
		"zhipu",
		standardAPILabel("Zhipu / GLM", regionPtr("china")),
		subtitleAPI("open.bigmodel.cn/api/paas/v4"),
		nil,
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
	),
	entry(
		"z-ai-anthropic", "Zhipu / GLM",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("z-ai-anthropic"),
		regionPtr("intl"),
		"api.z.ai/api/anthropic/v1",
		"zhipu",
		anthropicLabel("Zhipu / GLM", regionPtr("intl")),
		subtitleAnthropic("api.z.ai/api/anthropic/v1"),
		nil,
	),
	entry(
		"zhipu-anthropic", "Zhipu / GLM",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("zhipu-anthropic"),
		regionPtr("china"),
		"open.bigmodel.cn/api/anthropic/v1",
		"zhipu",
		anthropicLabel("Zhipu / GLM", regionPtr("china")),
		subtitleAnthropic("open.bigmodel.cn/api/anthropic/v1"),
		nil,
	),

	// ── Moonshot / Kimi (plan × region) ──────────────────────────────────────

	entry(
		"moonshot", "Moonshot / Kimi",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("moonshot"),
		regionPtr("intl"),
		"api.moonshot.ai/v1",
		"kimi",
		standardAPILabel("Moonshot / Kimi", regionPtr("intl")),
		subtitleAPI("api.moonshot.ai/v1"),
		nil,
	),
	entry(
		"moonshot-cn", "Moonshot / Kimi",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("moonshot-cn"),
		regionPtr("china"),
		"api.moonshot.cn/v1",
		"kimi",
		standardAPILabel("Moonshot / Kimi", regionPtr("china")),
		subtitleAPI("api.moonshot.cn/v1"),
		nil,
	),
	entry(
		"moonshot-anthropic", "Moonshot / Kimi",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("moonshot-anthropic"),
		regionPtr("intl"),
		"api.moonshot.ai/anthropic/v1",
		"kimi",
		anthropicLabel("Moonshot / Kimi", regionPtr("intl")),
		subtitleAnthropic("api.moonshot.ai/anthropic/v1"),
		nil,
	),
	entry(
		"moonshot-cn-anthropic", "Moonshot / Kimi",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("moonshot-cn-anthropic"),
		regionPtr("china"),
		"api.moonshot.cn/anthropic/v1",
		"kimi",
		anthropicLabel("Moonshot / Kimi", regionPtr("china")),
		subtitleAnthropic("api.moonshot.cn/anthropic/v1"),
		nil,
	),

	// ── MiniMax (plan × region) ───────────────────────────────────────────────

	entry(
		"minimax", "MiniMax",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("minimax"),
		regionPtr("intl"),
		"api.minimax.io/v1",
		"minimax",
		standardAPILabel("MiniMax", regionPtr("intl")),
		subtitleAPI("api.minimax.io/v1"),
		nil,
	),
	entry(
		"minimax-cn", "MiniMax",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("minimax-cn"),
		regionPtr("china"),
		"api.minimaxi.com/v1",
		"minimax",
		standardAPILabel("MiniMax", regionPtr("china")),
		subtitleAPI("api.minimaxi.com/v1"),
		nil,
	),
	entry(
		"minimax-anthropic", "MiniMax",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("minimax-anthropic"),
		regionPtr("intl"),
		"api.minimax.io/anthropic/v1",
		"minimax",
		anthropicLabel("MiniMax", regionPtr("intl")),
		subtitleAnthropic("api.minimax.io/anthropic/v1"),
		nil,
	),
	entry(
		"minimax-cn-anthropic", "MiniMax",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("minimax-cn-anthropic"),
		regionPtr("china"),
		"api.minimaxi.com/anthropic/v1",
		"minimax",
		anthropicLabel("MiniMax", regionPtr("china")),
		subtitleAnthropic("api.minimaxi.com/anthropic/v1"),
		nil,
	),

	// ── DeepSeek (plan only, no region) ──────────────────────────────────────

	entry(
		"deepseek", "DeepSeek",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("deepseek"),
		nil,
		"api.deepseek.com/v1",
		"deepseek",
		standardAPILabel("DeepSeek", nil),
		subtitleAPI("api.deepseek.com/v1"),
		nil,
	),
	entry(
		"deepseek-anthropic", "DeepSeek",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("deepseek-anthropic"),
		nil,
		"api.deepseek.com/anthropic/v1",
		"deepseek",
		anthropicLabel("DeepSeek", nil),
		subtitleAnthropic("api.deepseek.com/anthropic/v1"),
		nil,
	),

	// ── Qwen / Alibaba (plan × region; anthropic has no region) ──────────────

	entry(
		"qwen", "Qwen / Alibaba",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("qwen"),
		regionPtr("china"),
		"dashscope.aliyuncs.com/compatible-mode/v1",
		"qwen",
		standardAPILabel("Qwen / Alibaba", regionPtr("china")),
		subtitleAPI("dashscope.aliyuncs.com/compatible-mode/v1"),
		nil,
	),
	entry(
		"qwen-intl", "Qwen / Alibaba",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("qwen-intl"),
		regionPtr("intl"),
		"dashscope-intl.aliyuncs.com/compatible-mode/v1",
		"qwen",
		standardAPILabel("Qwen / Alibaba", regionPtr("intl")),
		subtitleAPI("dashscope-intl.aliyuncs.com/compatible-mode/v1"),
		aliasesPtr("qwen-international", "dashscope-intl"),
	),
	entry(
		"qwen-us", "Qwen / Alibaba",
		gen.ProviderCatalogEntryPlanStandardApi,
		wire("qwen-us"),
		regionPtr("us"),
		"dashscope-us.aliyuncs.com/compatible-mode/v1",
		"qwen",
		standardAPILabel("Qwen / Alibaba", regionPtr("us")),
		subtitleAPI("dashscope-us.aliyuncs.com/compatible-mode/v1"),
		aliasesPtr("dashscope-us"),
	),
	entry(
		"coding-plan-anthropic", "Qwen / Alibaba",
		gen.ProviderCatalogEntryPlanCodingPlan,
		wire("coding-plan-anthropic"),
		nil,
		"coding-intl.dashscope.aliyuncs.com/apps/anthropic",
		"qwen",
		anthropicLabel("Qwen / Alibaba", nil)+" (Coding Plan)",
		subtitleCoding("coding-intl.dashscope.aliyuncs.com/apps/anthropic"),
		aliasesPtr("alibaba-coding-anthropic"),
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
	}
	return out, nil
}

// DeriveWire returns the wire protocol for a given protocol id using FR-005.
// Exported so the drift-guard test can validate every Entries row against the rule.
func DeriveWire(id string) gen.ProviderCatalogEntryWire {
	return wire(id)
}
