// Command _gen-go-fixup replaces inline anonymous struct copies of named types
// that oapi-codegen v2 emits as a side effect of certain spec patterns
// (notably `items: $ref` arrays that also declare `additionalProperties: false`
// on the referenced schema). It enforces Constraint #8 — wire types defined in
// contracts/ are the single source of truth, and consumers must use the named
// types rather than JSON-shape-coincident anonymous copies.
//
// Why this exists: without the fixup, generated Go code carries both a named
// type and an inline anonymous struct for the same schema, and the
// Constraint #8 wire-format lint happily passes because the inline copy has
// two or more `json:` tags. Renaming a field in the named type would NOT
// propagate to the inline copy, silently breaking handlers that depend on the
// Go-side type. This fixup is idempotent: running it twice on a clean tree
// produces no diff.
//
// Known rewrites currently applied:
//   - `fallback_models` on AgentDetail/AgentCreateRequest/AgentUpdateRequest →
//     `*[]FallbackModel`.
//   - `edges` on WorkspaceDelegation/WorkspaceDelegationUpdateRequest →
//     `[]WorkspaceDelegationEdge`.
//   - `heartbeat` (WorkspaceMemberConfig + inlined member_configs values) →
//     `*WorkspaceMemberHeartbeat`, then `member_configs` on Workspace/
//     WorkspaceUpdateRequest → `*map[string]WorkspaceMemberConfig`.
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// fallbackModelsInline matches the inline anonymous struct that oapi-codegen
// emits for `fallback_models: *[]struct{Model string; Provider *string}` and
// captures the field name (FallbackModels) plus the trailing json tag.
//
// Group 1: field name (e.g. "FallbackModels")
// Group 2: json tag with surrounding backticks (e.g. "`json:\"fallback_models,omitempty\"`")
//
// The multiline `(?s)` flag lets `.` match newlines so we capture the entire
// inline struct definition, including its body.
// Matches both the agent `fallback_models` field (FallbackModels) and the memory
// `recap_fallback_models` field (RecapFallbackModels) — the `[A-Za-z]*` prefix on
// the field name plus the `[a-z_]*` prefix on the json tag cover both. The rewrite
// preserves the captured field name ($1) so each field keeps its own name and
// becomes `*[]FallbackModel`.
var fallbackModelsInline = regexp.MustCompile(
	`(?s)([A-Za-z]*FallbackModels)\s+\*\[\]struct\s*\{[^}]*?\}\s*(` + "`json:\"[a-z_]*fallback_models[^\"]*\"`" + `)`,
)

// delegationEdgesInline matches the inline anonymous struct that oapi-codegen
// emits for the `edges: [{ $ref: WorkspaceDelegationEdge }]` arrays on
// WorkspaceDelegation and WorkspaceDelegationUpdateRequest (both schemas declare
// `additionalProperties: false` on the edge item, which triggers the inline copy
// instead of a `[]WorkspaceDelegationEdge` reference). The edges field is
// required (non-pointer slice), so the match is `Edges []struct{...}`. The body
// itself contains a nested enum field with a per-parent name
// (WorkspaceDelegationEdgesModes / WorkspaceDelegationUpdateRequestEdgesModes),
// so the `(?s)...\{...nested...\}` body match must span the inner struct's own
// brace. We anchor on the trailing `json:"edges"` tag to bound the replacement.
//
// Group 1: field name ("Edges")
// Group 2: json tag with backticks ("`json:\"edges\"`")
var delegationEdgesInline = regexp.MustCompile(`(?s)(Edges)\s+\[\]struct\s*\{.*?\}\s*(` + "`json:\"edges\"`" + `)`)

const delegationEdgesRewrite = `Edges []WorkspaceDelegationEdge `

// memberConfigsHeartbeatInline matches the inline anonymous heartbeat struct
// oapi-codegen emits for `heartbeat: $ref WorkspaceMemberHeartbeat`. The
// referenced schema declares `additionalProperties: false`, which triggers the
// inline copy instead of a `*WorkspaceMemberHeartbeat` reference. It appears on
// the named WorkspaceMemberConfig type AND inside the inlined member_configs map
// values on Workspace / WorkspaceUpdateRequest (3 sites). The heartbeat body has
// no nested braces, so `\{.*?\}` bounds it on the first close brace.
//
// Group 1: field name ("Heartbeat"); Group 2: json tag with backticks.
var memberConfigsHeartbeatInline = regexp.MustCompile(
	`(?s)(Heartbeat)\s+\*struct\s*\{.*?\}\s*(` + "`json:\"heartbeat,omitempty\"`" + `)`,
)

const memberConfigsHeartbeatRewrite = `Heartbeat *WorkspaceMemberHeartbeat `

// memberConfigsMapInline matches the inline anonymous map-value struct
// oapi-codegen emits for `member_configs: additionalProperties $ref
// WorkspaceMemberConfig` (again triggered by `additionalProperties: false` on
// the referenced schema). Appears on Workspace and WorkspaceUpdateRequest.
//
// Order note: this regex runs after memberConfigsHeartbeatInline as a clarity
// preference (outer → inner readability), not a correctness requirement.
// The map regex is anchored on the literal `member_configs` json tag, so its
// non-greedy `.*?` body spans the nested heartbeat brace regardless of order.
// If the inner heartbeat struct were NOT already rewritten, the body would span
// an extra close-brace level and still match the outer `member_configs` tag.
// The heartbeat body itself has no nested braces, so both orderings converge.
//
// Group 1: field name ("MemberConfigs"); Group 2: json tag with backticks.
var memberConfigsMapInline = regexp.MustCompile(
	`(?s)(MemberConfigs)\s+\*map\[string\]struct\s*\{.*?\}\s*(` + "`json:\"member_configs,omitempty\"`" + `)`,
)

const memberConfigsMapRewrite = `MemberConfigs *map[string]WorkspaceMemberConfig `

// rewriteRules describes each inline-struct rewrite in application order.
// wantCount is the number of replacements expected in a freshly-generated
// (pre-rewrite) file. When the inline pattern is present in the input and
// produces 0 replacements, the run exits non-zero so a future oapi-codegen
// change that drops the inline structs fails loudly instead of silently
// shipping unfixed types.
// Idempotency: if the inline pattern is absent (already rewritten or the
// schema was removed), 0 replacements are expected and no error is raised.
type rewriteRule struct {
	name    string
	inline  *regexp.Regexp
	rewrite string
}

var rewriteRules = []rewriteRule{
	{
		name:    "fallback_models",
		inline:  fallbackModelsInline,
		rewrite: "$1 *[]FallbackModel $2",
	},
	{
		name:    "delegation_edges",
		inline:  delegationEdgesInline,
		rewrite: delegationEdgesRewrite + "$2",
	},
	// heartbeat before member_configs (style preference; both orderings converge).
	{
		name:    "member_configs_heartbeat",
		inline:  memberConfigsHeartbeatInline,
		rewrite: memberConfigsHeartbeatRewrite + "$2",
	},
	{
		name:    "member_configs_map",
		inline:  memberConfigsMapInline,
		rewrite: memberConfigsMapRewrite + "$2",
	},
}

func run(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	original := string(data)
	current := original
	var hardErrors []string

	// Brace-aware rewrites first (ADR-066/067/068 contract commit): these
	// inline bodies nest other inline structs (CatalogProvider → models[] →
	// CatalogModel), which a flat `[^}]*?` regex cannot span. Inner-most
	// shapes are listed first so every outer body is flat by the time it is
	// matched.
	for _, rule := range namedTypeRules {
		next, err := rewriteInlineStruct(current, rule)
		if err != nil {
			hardErrors = append(hardErrors, fmt.Sprintf("rewrite %q: %v", rule.name, err))
			continue
		}
		current = next
	}

	for _, rule := range rewriteRules {
		// Count how many inline-struct anchors exist in the current text
		// BEFORE applying the replacement. A non-zero count means we have
		// something to rewrite; 0 means the file was already rewritten or
		// the schema was removed (both are valid idempotent states).
		beforeCount := len(rule.inline.FindAllString(current, -1))
		next := rule.inline.ReplaceAllString(current, rule.rewrite)
		afterCount := len(rule.inline.FindAllString(next, -1))

		if beforeCount > 0 && afterCount > 0 {
			// The pattern matched but the replacement left inline structs behind.
			// This is a real failure: the regex matched but did not rewrite correctly.
			hardErrors = append(hardErrors, fmt.Sprintf(
				"rewrite %q: %d inline anchor(s) still present after replacement (regex matched but did not rewrite correctly)",
				rule.name,
				afterCount,
			))
		}
		current = next
	}

	if len(hardErrors) > 0 {
		for _, e := range hardErrors {
			fmt.Fprintf(os.Stderr, "_gen-go-fixup: %s\n", e)
		}
		return fmt.Errorf("one or more rewrites failed to eliminate their inline anchor patterns")
	}

	if current == original {
		return nil
	}

	if err := os.WriteFile(path, []byte(current), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}

// namedTypeRule describes one inline `<Field> <prefix>struct{...} `json:"<tag>..."`
// occurrence to replace with `<Field> <prefix><Named>`. mustContain, when
// non-empty, must appear inside the struct body — it disambiguates fields
// that share a name and json tag but reference different schemas (both
// EntitlementResponse.models and CatalogProvider.models are `Models []struct`).
type namedTypeRule struct {
	name        string
	field       string // Go field name, e.g. "Dependents"
	jsonTag     string // json tag name, e.g. "dependents"
	named       string // replacement named type, e.g. "ProviderDependent"
	mustContain string
}

// namedTypeRules — inner-most shapes first (see run()).
var namedTypeRules = []namedTypeRule{
	{name: "catalog_model", field: "Models", jsonTag: "models", named: "CatalogModel", mustContain: "ContextWindow int"},
	{name: "catalog_protocol", field: "Protocols", jsonTag: "protocols", named: "CatalogProtocol"},
	{name: "catalog_resize_limits", field: "ResizeLimits", jsonTag: "resize_limits", named: "CatalogResizeLimits"},
	{name: "catalog_default_resize_limits", field: "DefaultResizeLimits", jsonTag: "default_resize_limits", named: "CatalogResizeLimits"},
	{name: "catalog_provider", field: "Providers", jsonTag: "providers", named: "CatalogProvider", mustContain: "AuthMethods"},
	{name: "entitlement_model", field: "Models", jsonTag: "models", named: "EntitlementModel", mustContain: "Entitled bool"},
	{name: "provider_dependent", field: "Dependents", jsonTag: "dependents", named: "ProviderDependent"},
	{name: "context_model_override", field: "ModelOverrides", jsonTag: "model_overrides", named: "ContextModelOverride"},
	{name: "new_default", field: "NewDefault", jsonTag: "new_default", named: "DefaultModelUpdateRequest"},
}

// rewriteInlineStruct replaces every `<field> <prefix>struct {…} `json:"<tag>…"“
// whose body satisfies rule.mustContain with `<field> <prefix><named>`,
// scanning braces so nested inline structs are spanned correctly.
func rewriteInlineStruct(src string, rule namedTypeRule) (string, error) {
	head := regexp.MustCompile(`(?m)^(\s*)` + rule.field + `(\s+)(\*\[\]|\[\]|\*|)struct \{`)
	tag := regexp.MustCompile(`^\s*` + "`json:\"" + rule.jsonTag + `[^"]*"` + "`")
	var out []byte
	rest := src
	for {
		loc := head.FindStringSubmatchIndex(rest)
		if loc == nil {
			out = append(out, rest...)
			break
		}
		open := loc[1] - 1 // index of '{'
		depth := 0
		closeIdx := -1
		for i := open; i < len(rest); i++ {
			switch rest[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					closeIdx = i
				}
			}
			if closeIdx >= 0 {
				break
			}
		}
		if closeIdx < 0 {
			return "", fmt.Errorf("unbalanced braces after %q", rule.field)
		}
		body := rest[open : closeIdx+1]
		after := rest[closeIdx+1:]
		tagLoc := tag.FindStringIndex(after)
		if tagLoc == nil || (rule.mustContain != "" && !strings.Contains(body, rule.mustContain)) {
			// Not this rule's shape — keep the text through the '{' and continue after it.
			out = append(out, rest[:open+1]...)
			rest = rest[open+1:]
			continue
		}
		indent := rest[loc[2]:loc[3]]
		gap := rest[loc[4]:loc[5]]
		prefix := rest[loc[6]:loc[7]]
		out = append(out, rest[:loc[0]]...)
		out = append(out, indent+rule.field+gap+prefix+rule.named...)
		rest = after
	}
	return string(out), nil
}

func main() {
	path := flag.String("file", "pkg/api/generated/openapi_types.gen.go", "path to the generated openapi Go file")
	flag.Parse()

	if err := run(*path); err != nil {
		fmt.Fprintf(os.Stderr, "_gen-go-fixup: %v\n", err)
		os.Exit(1)
	}
}
