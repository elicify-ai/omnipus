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
// but that is a NAMED type, so the body carries no nested braces and `[^{}]*?`
// bounds it exactly. We anchor on the trailing `json:"edges"` tag too.
//
// THE BODY ANCHOR IS LOAD-BEARING, not decoration. This regex used to match on
// the field name and the json tag alone — `Edges []struct{...} json:"edges"` —
// which is not a delegation-specific shape at all. ADR-067's
// KnowledgeGraphResponse also has a required `edges` array of an inline struct,
// it sorts BEFORE WorkspaceDelegation in the generated file, and it was
// therefore rewritten to `[]WorkspaceDelegationEdge`: the knowledge graph's
// links shipped on the wire typed as delegation edges (from_agent/to_agent
// instead of from_path/to_path/resolution). Nothing caught it — the rewrite
// produced compiling Go, and the TypeScript half of the contract was correct,
// so only a handler that actually built the response would have noticed.
// Requiring `json:"from_agent"` INSIDE the body makes the rule say what it
// means: this is the delegation edge, not merely something spelled "edges".
//
// Group 1: field name ("Edges")
// Group 2: json tag with backticks ("`json:\"edges\"`")
var delegationEdgesInline = regexp.MustCompile(
	`(?s)(Edges)\s+\[\]struct\s*\{[^{}]*?json:"from_agent"[^{}]*?\}\s*(` + "`json:\"edges\"`" + `)`,
)

const delegationEdgesRewrite = `Edges []WorkspaceDelegationEdge `

// The three ADR-067 KnowledgeGraphResponse arrays. Each is `items: $ref` onto a
// schema declaring `additionalProperties: false`, which is the same pattern
// that inlines the delegation edges above, so all three arrive as anonymous
// structs that Constraint #8 forbids consumers from using.
//
// Each is anchored on a json tag unique to its own item schema — `from_path`
// for an edge, `exists` for a node, `reason` for a skip — so none of them can
// drift onto a same-named field of some other schema the way the delegation
// rule did.
var (
	knowledgeGraphEdgesInline = regexp.MustCompile(
		`(?s)(Edges)\s+\[\]struct\s*\{[^{}]*?json:"from_path"[^{}]*?\}\s*(` + "`json:\"edges\"`" + `)`,
	)
	knowledgeGraphNodesInline = regexp.MustCompile(
		`(?s)(Nodes)\s+\[\]struct\s*\{[^{}]*?json:"exists"[^{}]*?\}\s*(` + "`json:\"nodes\"`" + `)`,
	)
	knowledgeGraphSkippedInline = regexp.MustCompile(
		`(?s)(Skipped)\s+\[\]struct\s*\{[^{}]*?json:"reason"[^{}]*?\}\s*(` + "`json:\"skipped\"`" + `)`,
	)
)

// The two ADR-067 KnowledgeSearchResponse members, same pattern and same
// reason: `hits` is an array of KnowledgeSearchHit, `incompleteness` is a
// single KnowledgeSearchIncompleteness, and both arrive inlined because their
// schemas declare `additionalProperties: false`.
//
// Without these, a handler building a search response has to spell the whole
// anonymous struct out at every construction site — which is a hand-written
// copy of a contract type in everything but name, and would drift silently the
// day a field is added to the schema.
var (
	knowledgeSearchHitsInline = regexp.MustCompile(
		`(?s)(Hits)\s+\[\]struct\s*\{[^{}]*?json:"excerpt_unavailable,omitempty"[^{}]*?\}\s*(` + "`json:\"hits\"`" + `)`,
	)
	knowledgeSearchIncompletenessInline = regexp.MustCompile(
		`(?s)(Incompleteness)\s+struct\s*\{[^{}]*?json:"total_known"[^{}]*?\}\s*(` + "`json:\"incompleteness\"`" + `)`,
	)
	knowledgeOutlineHeadingsInline = regexp.MustCompile(
		`(?s)(Headings)\s+\[\]struct\s*\{[^{}]*?json:"slug"[^{}]*?\}\s*(` + "`json:\"headings\"`" + `)`,
	)
)

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
	{
		name:    "knowledge_graph_edges",
		inline:  knowledgeGraphEdgesInline,
		rewrite: "$1 []KnowledgeGraphEdge $2",
	},
	{
		name:    "knowledge_graph_nodes",
		inline:  knowledgeGraphNodesInline,
		rewrite: "$1 []KnowledgeGraphNode $2",
	},
	{
		name:    "knowledge_graph_skipped",
		inline:  knowledgeGraphSkippedInline,
		rewrite: "$1 []KnowledgeGraphSkip $2",
	},
	{
		name:    "knowledge_search_hits",
		inline:  knowledgeSearchHitsInline,
		rewrite: "$1 []KnowledgeSearchHit $2",
	},
	{
		name:    "knowledge_search_incompleteness",
		inline:  knowledgeSearchIncompletenessInline,
		rewrite: "$1 KnowledgeSearchIncompleteness $2",
	},
	{
		name:    "knowledge_outline_headings",
		inline:  knowledgeOutlineHeadingsInline,
		rewrite: "$1 []KnowledgeOutlineHeading $2",
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

// run applies every rewrite rule to path.
//
// strict makes "this rule matched nothing" an ERROR rather than a silently
// accepted idempotent state. That distinction is load-bearing: the ONLY
// production invocation (scripts/_gen-go.sh) runs this immediately after
// oapi-codegen, on a file that has just been generated from scratch, so every
// rule's anchor MUST be present. A rule that matches nothing there is a DEAD
// rule — its anchor drifted, or the schema it targets was renamed — and the
// generated file ships with the inline anonymous struct the rule existed to
// remove.
//
// That is not hypothetical. KnowledgeGraphResponse.Edges shipped typed as
// []WorkspaceDelegationEdge because the delegation rule's regex anchored on a
// field name plus a json tag alone, and KnowledgeGraphResponse sorts before
// WorkspaceDelegation in the generated file, so the rule fired on the wrong
// struct. The failure was silent in exactly this way — and it was loud only
// because a Go call site happened to stop compiling. An anchor that stops
// matching produces no such compile error at all: the field simply reverts to
// an anonymous struct, Constraint #8 is violated, and nothing says so.
func run(path string, strict bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	original := string(data)
	current := original
	var hardErrors []string

	for _, rule := range rewriteRules {
		// Count how many inline-struct anchors exist in the current text
		// BEFORE applying the replacement. A non-zero count means we have
		// something to rewrite; 0 means the file was already rewritten or
		// the schema was removed (both are valid idempotent states).
		beforeCount := len(rule.inline.FindAllString(current, -1))
		next := rule.inline.ReplaceAllString(current, rule.rewrite)
		afterCount := len(rule.inline.FindAllString(next, -1))

		if strict && beforeCount == 0 {
			hardErrors = append(hardErrors, fmt.Sprintf(
				"rewrite %q: matched NOTHING in a freshly generated file. Its anchor "+
					"pattern no longer describes what oapi-codegen emits, so the type it "+
					"exists to fix is shipping as an inline anonymous struct (Constraint #8). "+
					"Either update the pattern or delete the rule",
				rule.name))
		}
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

func main() {
	path := flag.String("file", "pkg/api/generated/openapi_types.gen.go", "path to the generated openapi Go file")
	strict := flag.Bool("strict", false,
		"treat a rule that matches nothing as an error; set by scripts/_gen-go.sh, "+
			"which always runs against a freshly generated file")
	flag.Parse()

	if err := run(*path, *strict); err != nil {
		fmt.Fprintf(os.Stderr, "_gen-go-fixup: %v\n", err)
		os.Exit(1)
	}
}
