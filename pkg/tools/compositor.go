// Package tools — per-agent tool policy filter (central tool registry redesign).
//
// FilterToolsByPolicy is the primary runtime filter: it resolves effective
// policy (global × agent, deny>ask>allow) before the agent loop assembles the
// LLM tool list.

package tools

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// MCPCaller is the interface used by mcpToolAdapter for execution.
// pkg/mcp.Manager satisfies this interface; a test double may be used in tests.
type MCPCaller interface {
	GetAllTools() map[string][]*mcp.Tool
	CallTool(ctx context.Context, serverName, toolName string, arguments map[string]any) (*mcp.CallToolResult, error)
}

// passesScopeGate reports whether a tool with the given scope is structurally
// accessible to agentType. This is the outer guard — it cannot be bypassed by
// policy configuration.
//
//   - ScopeCore: core agents pass by default; custom agents require an effective
//     policy entry that is not "deny".
//   - ScopeGeneral: any agent type passes.
//   - Unknown/zero-value scopes: denied (fail-closed, per CLAUDE.md hard constraint 6).
func passesScopeGate(scope ToolScope, agentType string) bool {
	switch scope {
	case ScopeCore:
		return agentType == "core"
	case ScopeGeneral:
		return true
	default:
		return false // deny unknown scopes (fail-closed)
	}
}

// wildcardEntry is a parsed wildcard policy key (e.g., "system.*" or "mcp_server_*").
// Supports trailing ".*" (dot-delimited, FR-009) and trailing "_*" (underscore-delimited,
// G10 — enables bulk-deny of a whole MCP server's tools whose names are single-segment
// underscore-delimited identifiers like "mcp_<server>_<tool>").
type wildcardEntry struct {
	prefix    string // the part before ".*" or "_*"
	segments  int    // number of dot-separated segments (primary sort key, FR-071)
	delimiter byte   // '.' for ".*" wildcards, '_' for "_*" wildcards
	policy    config.ToolPolicy
}

// buildWildcardIndex parses a policy map and returns a sorted slice of wildcard
// entries. Sort order: most-specific (most segments) first; tie-break by char
// count descending; final tie-break lexicographic ascending (FR-071).
// Exact-name keys are NOT included here — they are resolved by direct map lookup.
//
// Supported wildcard forms (both may coexist in the same policy map):
//   - Trailing ".*": e.g. "system.*" — matches tools whose name starts with "system."
//     or equals "system". Used for dot-namespaced builtin tools (FR-009).
//   - Trailing "_*": e.g. "mcp_server_*" — matches tools whose name starts with
//     "mcp_server_" or equals "mcp_server". Used for underscore-delimited MCP
//     tool names (G10 — "mcp_<server>_<tool>" format).
//
// Pre-existing "_*" keys were previously no-ops (the ".*" branch did not match them),
// so enabling them is non-regressing for existing configs.
func buildWildcardIndex(policies map[string]config.ToolPolicy) []wildcardEntry {
	var entries []wildcardEntry
	for k, v := range policies {
		switch {
		case strings.HasSuffix(k, ".*"):
			prefix := k[:len(k)-2] // strip trailing ".*"
			if prefix == "" {
				continue // a bare ".*" would match (nearly) everything — ignore it
			}
			entries = append(entries, wildcardEntry{
				prefix:    prefix,
				segments:  strings.Count(prefix, ".") + 1,
				delimiter: '.',
				policy:    v,
			})
		case strings.HasSuffix(k, "_*"):
			prefix := k[:len(k)-2] // strip trailing "_*"
			if prefix == "" {
				continue // a bare "_*" would match every underscore-prefixed tool — ignore it
			}
			// For underscore wildcards, segment count is always 1 (single identifier).
			// Use char count to rank among multiple "_*" entries (longer prefix = more specific).
			entries = append(entries, wildcardEntry{
				prefix:    prefix,
				segments:  1,
				delimiter: '_',
				policy:    v,
			})
		}
	}
	// Primary: segment count descending (most specific first).
	// Secondary: char count descending (longer prefix first).
	// Tertiary: lexicographic ascending.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].segments != entries[j].segments {
			return entries[i].segments > entries[j].segments
		}
		if len(entries[i].prefix) != len(entries[j].prefix) {
			return len(entries[i].prefix) > len(entries[j].prefix)
		}
		return entries[i].prefix < entries[j].prefix
	})
	return entries
}

// resolveFromMap resolves the policy for toolName from a flat policies map
// (supports exact-name, trailing ".*", and trailing "_*" wildcard keys).
// Exact matches win over wildcards; among wildcards longest-prefix wins (FR-009, FR-071).
// Returns "" if no entry matches — callers resolve that to "deny" (fail-closed);
// there is no default-policy fallback (CLAUDE.md hard constraint 6).
func resolveFromMap(
	toolName string,
	policies map[string]config.ToolPolicy,
	wildcards []wildcardEntry,
) config.ToolPolicy {
	// 1. Exact match always wins.
	if p, ok := policies[toolName]; ok {
		return p
	}
	// 2. Most-specific prefix wildcard match (wildcards already sorted by specificity).
	for _, w := range wildcards {
		var matched bool
		switch w.delimiter {
		case '.':
			matched = strings.HasPrefix(toolName, w.prefix+".") || toolName == w.prefix
		case '_':
			matched = strings.HasPrefix(toolName, w.prefix+"_") || toolName == w.prefix
		}
		if matched {
			return w.policy
		}
	}
	return ""
}

// resolveEffectivePolicyWith is the single source of truth for the global×agent
// merge (deny > ask > allow). Both ResolveEffectivePolicy and
// FilterToolsByPolicy resolve through this so the security-critical merge rule
// lives in exactly one place — duplicating it invites silent drift. The
// wildcard indexes are pre-built once per call site and threaded in to avoid
// re-parsing the policy maps for every tool.
//
// There is no default-policy fallback (CLAUDE.md hard constraint 6): a tool
// with no exact-or-wildcard match on EITHER side is a coverage gap that
// config.ValidateToolPolicyCoverage makes structurally impossible at boot and
// at every agent write. Reaching that state here is therefore a bug signal,
// not a normal resolution path — it is logged at Error and resolved to "deny"
// (fail-closed), never "allow". When only one side has a match, that match
// alone determines the verdict — coverage requires an entry on ONE side, not
// both, so a missing side is not treated as an implicit "allow".
func resolveEffectivePolicyWith(
	cfg *ToolPolicyCfg,
	toolName string,
	agentWildcards, globalWildcards []wildcardEntry,
) config.ToolPolicy {
	// God-mode override (O14): the global "bypass-permissions" switch floors
	// EVERY tool's effective policy at "allow" — no permission prompts, no
	// deny. This is the #1 effect of god mode and intentionally short-circuits
	// the normal global×agent merge below. It is the single resolution-time
	// hook for the tool-policy half of god mode, so both ResolveEffectivePolicy
	// and FilterToolsByPolicy honor it identically. The override is
	// non-destructive: cfg.Policies / cfg.GlobalPolicies are NOT mutated, so
	// clearing cfg.GodMode restores the prior decision exactly.
	if cfg.GodMode {
		return config.ToolPolicyAllow
	}
	g := resolveFromMap(toolName, cfg.GlobalPolicies, globalWildcards)
	a := resolveFromMap(toolName, cfg.Policies, agentWildcards)
	switch {
	case g == "" && a == "":
		// Structurally impossible once boot/write-time coverage validation is
		// in place — every static builtin tool must have an explicit global
		// and/or agent policy entry. Fail closed rather than silently
		// defaulting to "allow".
		slog.Error("tools: no policy entry (global or agent) for tool; failing closed to deny",
			"tool", toolName)
		return config.ToolPolicyDeny
	case g == "":
		return a
	case a == "":
		return g
	default:
		if g == config.ToolPolicyDeny || a == config.ToolPolicyDeny {
			return config.ToolPolicyDeny
		}
		if g == config.ToolPolicyAsk || a == config.ToolPolicyAsk {
			return config.ToolPolicyAsk
		}
		return config.ToolPolicyAllow
	}
}

// ResolveEffectivePolicy returns the combined global+agent effective policy
// ("allow", "ask", or "deny") for a single tool name. It applies the same
// global×agent resolution order as FilterToolsByPolicy without iterating all
// tools. It deliberately does NOT apply the scope gate — it is the bare
// global×agent merge. Callers that need the FULL per-tool verdict (scope +
// merge) must use EffectiveToolPolicy. Callers that need only the merge
// (e.g. repair.go H3) use this.
//
// A nil cfg has no policy maps to resolve from at all, so it fails closed to
// "deny" (logged at Error) rather than the historical hardcoded "allow" —
// CLAUDE.md hard constraint 6 forbids a language-level allow fallback.
func ResolveEffectivePolicy(cfg *ToolPolicyCfg, toolName string) string {
	if cfg == nil {
		slog.Error("tools: ResolveEffectivePolicy called with nil cfg; failing closed to deny",
			"tool", toolName)
		return string(config.ToolPolicyDeny)
	}
	agentWildcards := buildWildcardIndex(cfg.Policies)
	globalWildcards := buildWildcardIndex(cfg.GlobalPolicies)
	return string(resolveEffectivePolicyWith(cfg, toolName, agentWildcards, globalWildcards))
}

// effectiveToolPolicyWith is the unified single-tool resolver: it produces the
// FULL per-tool verdict ("allow"/"ask"/"deny") for one (tool, scope, agentType)
// in this exact order:
//
//  1. SCOPE GATE (fail-closed): the structural guard that policy cannot bypass.
//     A tool whose scope is neither ScopeCore nor ScopeGeneral (unknown/zero
//     value) is denied outright. ScopeCore on a custom agent and ScopeGeneral on
//     any agent both defer to the global×agent merge below — preserving the
//     EXACT behavior FilterToolsByPolicy had before this primitive existed
//     (where the ScopeCore-on-custom branch dropped the tool iff the merged
//     policy was "deny", i.e. identically to the merge verdict).
//
//  2. GLOBAL×AGENT MERGE (strictest-wins, deny > ask > allow): the standard
//     resolveEffectivePolicyWith path, including god-mode override and wildcard
//     resolution (most-specific-wins among wildcards, exact name beats wildcard).
//
// There used to be a step 0 here: an unconditional "infra force-allow" that
// resolved ToolSearch to "allow" no matter what any seeded policy data said,
// added because no seeded agent named ToolSearch in its own tool-policy
// override map. That was a CLAUDE.md hard-constraint-6 violation (a
// hardcoded allow fallback in the policy-resolution code path, invisible to
// an operator reading their own config) and has been removed. ToolSearch is
// now seeded "allow" as real, explicit, literal data for every agent —
// every core/system agent case in pkg/coreagent/core.go's coreAgentSeed and
// systemAgentSeed, and NewCustomAgentToolsCfg for freshly created agents —
// so it resolves through this exact same merge as every other static
// builtin tool, no special case. ToolManifestTier/ManifestInfra still
// matters for MANIFEST-BUILDING (which tools get a full schema sent every
// turn vs. lazy-loaded via ToolSearch) — that classification is untouched;
// only this policy-resolution shortcut is gone.
//
// This is the SINGLE authority both the agent-loop tool filter
// (FilterToolsByPolicy) and the gateway WS approval hook (resolveApprovalToolPolicy)
// resolve through, so the two can never drift again.
func effectiveToolPolicyWith(
	cfg *ToolPolicyCfg,
	scope ToolScope,
	agentType, toolName string,
	agentWildcards, globalWildcards []wildcardEntry,
) config.ToolPolicy {
	// 1. Scope gate (fail-closed for unknown scopes). The structural guard that
	//    policy cannot bypass. We deny here ONLY for the cases the prior
	//    FilterToolsByPolicy denied independently of the merge — i.e. a scope that
	//    fails the gate AND is not ScopeCore. ScopeCore on a custom agent fails
	//    passesScopeGate but is intentionally NOT denied here: the old code
	//    deferred it to the merge (dropping it iff the merged policy was "deny"),
	//    so we do the same. ScopeGeneral always passes the gate. Unknown/zero
	//    scopes fail the gate, are not ScopeCore, and are denied (fail-closed).
	if !passesScopeGate(scope, agentType) && scope != ScopeCore {
		return config.ToolPolicyDeny
	}

	// 2. Global×agent strictest-wins merge (god-mode + wildcards inside; fails
	//    closed to "deny" if neither side has an entry — see
	//    resolveEffectivePolicyWith's doc comment).
	return resolveEffectivePolicyWith(cfg, toolName, agentWildcards, globalWildcards)
}

// EffectiveToolPolicy is the exported single-tool resolver — the ONE
// authoritative primitive for a single (tool, scope, agentType) verdict
// ("allow"/"ask"/"deny"). Both FilterToolsByPolicy (the agent-loop tool filter)
// and the gateway WS approval hook resolve through this, so the loop's sent-defs
// view and the gateway's exec gate cannot diverge.
//
// Resolution order (see effectiveToolPolicyWith for the full rationale):
//  1. scope gate (unknown scope → "deny"; ScopeCore/ScopeGeneral defer to merge)
//  2. global×agent strictest-wins merge (deny > ask > allow, god-mode, wildcards)
//
// A nil cfg has no policy maps to resolve from at all, so (like
// ResolveEffectivePolicy) it fails closed to "deny" for every tool, ToolSearch
// included — CLAUDE.md hard constraint 6 forbids a language-level allow
// fallback. ToolSearch's real "allow" comes from the seeded policy data every
// agent carries (see effectiveToolPolicyWith's doc comment); a caller with no
// cfg at all has no seeded data to resolve from, same as any other tool.
func EffectiveToolPolicy(cfg *ToolPolicyCfg, scope ToolScope, agentType, toolName string) string {
	if cfg == nil {
		cfg = &ToolPolicyCfg{}
	}
	agentWildcards := buildWildcardIndex(cfg.Policies)
	globalWildcards := buildWildcardIndex(cfg.GlobalPolicies)
	return string(effectiveToolPolicyWith(cfg, scope, agentType, toolName, agentWildcards, globalWildcards))
}

// BuildFallbackPolicyCfg builds a *ToolPolicyCfg from the global config alone
// (no live agent registry access needed) and resolves the agent's type from
// the same config. It walks cfg.Sandbox.ToolPolicies for the global floor and
// cfg.Agents.List for the matching agent's builtin policy overrides.
//
// Shared by two call sites that both need a policy verdict without a live
// registry: AgentLoop.ResolveApprovalToolPolicy's not-yet-registered fallback
// (pkg/agent/loop.go) and the gateway WS approval hook's config-only path
// (pkg/gateway/websocket.go). Both are intentionally NOT god-mode-aware (the
// returned cfg never sets GodMode) and resolve every tool as ScopeGeneral
// (config alone cannot know a tool's real scope) — see each call site's own
// doc comment for why that is safe (fail-closed, never more permissive than
// the live registry path). Extracted after both call sites independently
// received the same DefaultPolicy-removal fix (CLAUDE.md hard constraint 6)
// without either noticing they were byte-for-byte duplicates of each other.
//
// Returns a non-nil polCfg even when cfg has no matching agent or policy
// entries (an empty *ToolPolicyCfg); agentType defaults to "custom" when the
// agent is not found in cfg.Agents.List or has no explicit Type set.
func BuildFallbackPolicyCfg(cfg *config.Config, agentID string) (polCfg *ToolPolicyCfg, agentType string) {
	polCfg = &ToolPolicyCfg{}
	if len(cfg.Sandbox.ToolPolicies) > 0 {
		// cfg.Sandbox.ToolPolicies is raw config data (map[string]string) — its
		// values are validated at the write boundary (rest_tool_policies.go's
		// putToolPolicies rejects anything outside allow/ask/deny before
		// persisting), so this is a trusting conversion, matching this
		// codebase's existing pattern of trusting validated config rather than
		// re-validating everywhere. NOTE: boot-time load of a hand-edited
		// config.json does NOT re-validate these specific values (only
		// per-agent tools.builtin.policies entries are checked by
		// config.validatePolicyValues) — see this function's caller-facing
		// doc comment for the flagged gap.
		gp := make(map[string]config.ToolPolicy, len(cfg.Sandbox.ToolPolicies))
		for k, v := range cfg.Sandbox.ToolPolicies {
			gp[k] = config.ToolPolicy(v)
		}
		polCfg.GlobalPolicies = gp
	}
	agentType = "custom"
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		if ac.ID != agentID {
			continue
		}
		if ac.Type != "" {
			agentType = string(ac.Type)
		}
		if ac.Tools != nil && len(ac.Tools.Builtin.Policies) > 0 {
			// ac.Tools.Builtin.Policies is already map[string]config.ToolPolicy
			// (typed at the config layer) — a direct copy, no string round-trip.
			ap := make(map[string]config.ToolPolicy, len(ac.Tools.Builtin.Policies))
			for k, v := range ac.Tools.Builtin.Policies {
				ap[k] = v
			}
			polCfg.Policies = ap
		}
		break
	}
	return polCfg, agentType
}

// ToolPolicyCfg is the per-agent tool policy configuration.
// Used by FilterToolsByPolicy.
//
// There is no default-policy fallback (CLAUDE.md hard constraint 6): a tool
// with no exact-or-wildcard match in EITHER Policies or GlobalPolicies fails
// closed to "deny" (see resolveEffectivePolicyWith). Coverage — an explicit
// entry on at least one side for every static builtin tool, for every agent —
// is enforced structurally by config.ValidateToolPolicyCoverage at boot and at
// every agent create/update/tools-write.
type ToolPolicyCfg struct {
	Policies map[string]config.ToolPolicy // per-tool overrides (supports trailing ".*" wildcards)

	// GlobalPolicies holds the operator-level global tool policy overrides.
	// Applied before agent-level policies; deny always wins (deny > ask > allow).
	GlobalPolicies map[string]config.ToolPolicy // per-tool global overrides (supports wildcards)

	// GodMode, when true, activates the O14 global "bypass-permissions"
	// override: every tool's effective policy is floored at "allow" — no tool
	// can remain "ask" or "deny". Set by agentToolsCfgToPolicy when
	// sandbox.god_mode is on AND god mode is available (build supports it +
	// --allow-god-mode passed). Non-destructive: the underlying policy maps are
	// untouched, so clearing this restores the prior decisions exactly.
	GodMode bool
}

// FilterToolsByPolicy returns the subset of tools that pass the scope gate
// and are not denied by policy. Also returns a map of tool name → resolved
// effective policy ("allow" or "ask") for tools that passed the filter.
// Tools with effective policy "deny" are removed from the result.
//
// Resolution order (strictest wins: deny > ask > allow): Global policy
// (GlobalPolicies) merges with Agent policy (Policies); a tool covered by only
// one side resolves to that side's value (see resolveEffectivePolicyWith).
//
// Wildcard support (FR-009, G10): policy map keys ending in ".*" are treated as
// dot-segment prefix wildcards (e.g., "system.*" matches any tool whose name starts
// with "system."). Keys ending in "_*" are treated as underscore prefix wildcards
// (e.g., "mcp_server_*" matches all tools from that MCP server). Exact-name matches
// always win over wildcards; among wildcards, the most-specific match wins (most
// dot-separated segments first); ties broken by char count then lexicographically (FR-071).
//
// Metrics (FR-039): emits IncFilterTotal once per tool decision.
func FilterToolsByPolicy(allTools []Tool, agentType string, cfg *ToolPolicyCfg) ([]Tool, map[string]string) {
	if cfg == nil {
		cfg = &ToolPolicyCfg{}
	}

	// Pre-build wildcard indexes for O(W) matching per tool rather than O(K).
	agentWildcards := buildWildcardIndex(cfg.Policies)
	globalWildcards := buildWildcardIndex(cfg.GlobalPolicies)

	out := make([]Tool, 0, len(allTools))
	policyMap := make(map[string]string)

	for _, t := range allTools {
		// Resolve the FULL per-tool verdict through the single shared primitive
		// (scope gate → global×agent strictest-wins). Routing every per-tool
		// decision through effectiveToolPolicyWith is what makes the loop's
		// sent-defs view and the gateway's exec gate provably identical — they
		// cannot diverge because they run the SAME function.
		verdict := effectiveToolPolicyWith(cfg, t.Scope(), agentType, t.Name(), agentWildcards, globalWildcards)
		if verdict == config.ToolPolicyDeny {
			activeToolMetricsRecorder.IncFilterTotal(agentType, "deny")
			continue
		}

		activeToolMetricsRecorder.IncFilterTotal(agentType, string(verdict))
		out = append(out, t)
		policyMap[t.Name()] = string(verdict)
	}
	return out, policyMap
}

// mcpToolAdapter wraps a single MCP tool as a Tool, forwarding Execute calls
// through the MCPCaller.
type mcpToolAdapter struct {
	serverName string
	toolDef    *mcp.Tool
	caller     MCPCaller
	params     map[string]any
}

func newMCPToolAdapter(serverName string, toolDef *mcp.Tool, caller MCPCaller) *mcpToolAdapter {
	params := make(map[string]any)
	if toolDef.InputSchema != nil {
		if m, ok := toolDef.InputSchema.(map[string]any); ok {
			params = m
		}
	}
	return &mcpToolAdapter{
		serverName: serverName,
		toolDef:    toolDef,
		caller:     caller,
		params:     params,
	}
}

func (a *mcpToolAdapter) Name() string               { return a.toolDef.Name }
func (a *mcpToolAdapter) Description() string        { return a.toolDef.Description }
func (a *mcpToolAdapter) Parameters() map[string]any { return a.params }
func (a *mcpToolAdapter) Scope() ToolScope           { return ScopeGeneral }
func (a *mcpToolAdapter) Category() ToolCategory     { return CategoryMCP }

func (a *mcpToolAdapter) Execute(ctx context.Context, args map[string]any) *ToolResult {
	result, err := a.caller.CallTool(ctx, a.serverName, a.toolDef.Name, args)
	if err != nil {
		return ErrorResult(fmt.Sprintf("MCP tool %q failed: %v", a.toolDef.Name, err))
	}
	text := mcpContentText(result.Content)
	if result.IsError {
		return ErrorResult(fmt.Sprintf("MCP tool %q error: %s", a.toolDef.Name, text))
	}
	return SilentResult(text)
}

// mcpContentText concatenates all TextContent entries from an MCP Content slice.
func mcpContentText(content []mcp.Content) string {
	var sb strings.Builder
	for _, c := range content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// toolMetricsRecorder is the minimal interface needed by the tool registry
// packages for FR-039 metrics (IncFilterTotal, IncCollisionTotal) and, as of
// ADR-071 §4.3.1(a), the two ToolSearch detection counters
// (IncToolSearchZeroResult, IncToolSearchNoFollowUp). pkg/gateway implements
// this with its toolMetrics type; pkg/tools uses the unexported interface so
// there is no import cycle.
type toolMetricsRecorder interface {
	IncFilterTotal(agentType, effectivePolicy string)
	IncCollisionTotal(conflictWith string)
	IncToolSearchZeroResult()
	IncToolSearchNoFollowUp()
}

// nopToolMetrics satisfies toolMetricsRecorder when no gateway is wired.
type nopToolMetrics struct{}

func (nopToolMetrics) IncFilterTotal(_, _ string) {}
func (nopToolMetrics) IncCollisionTotal(_ string) {}
func (nopToolMetrics) IncToolSearchZeroResult()   {}
func (nopToolMetrics) IncToolSearchNoFollowUp()   {}

// activeToolMetricsRecorder is swapped at gateway boot.
var activeToolMetricsRecorder toolMetricsRecorder = nopToolMetrics{}

// SetToolMetricsRecorder registers the gateway-level metrics recorder.
// Called once at gateway boot before any agent turn runs.
func SetToolMetricsRecorder(m toolMetricsRecorder) {
	if m != nil {
		activeToolMetricsRecorder = m
		slog.Debug("tools: filter metrics recorder wired")
	}
}

// ── ADR-071 §4.3.1(a): ToolSearch detection counters ────────────────────────
//
// Two operator-visible signals that make the D3 revisit trigger falsifiable:
// omnipus_toolsearch_zero_result_total (a query returned no policy-loadable
// hit) and omnipus_toolsearch_no_followup_total (a query-path promotion sat
// unused for more than searchPromotionHorizonTurns turns). Both route through
// the SAME toolMetricsRecorder indirection FR-039 established above — no
// second, weaker mechanism — AND are mirrored into package-level
// atomic.Uint64s here so unit tests can assert on the counters without
// standing up a gateway (the one part of the earlier
// handoffTranscriptWriteFailures framing that survives, as a test affordance
// rather than the operator surface).

// toolSearchZeroResultQueries and toolSearchNoFollowUpCalls back
// ToolSearchZeroResultQueries() and ToolSearchNoFollowUpCalls().
var (
	toolSearchZeroResultQueries atomic.Uint64
	toolSearchNoFollowUpCalls   atomic.Uint64
)

// recordToolSearchZeroResult increments both the local test-affordance
// counter and the gateway-reachable recorder. Called from tools_tool.go's
// query (by-description) path when it returns no policy-loadable hit.
func recordToolSearchZeroResult() {
	toolSearchZeroResultQueries.Add(1)
	activeToolMetricsRecorder.IncToolSearchZeroResult()
}

// ToolSearchZeroResultQueries returns the current value of the
// omnipus_toolsearch_zero_result_total counter. Exported for tests.
func ToolSearchZeroResultQueries() uint64 {
	return toolSearchZeroResultQueries.Load()
}

// RecordToolSearchNoFollowUp increments both the local test-affordance
// counter and the gateway-reachable recorder. Exported because the
// no-followup increment originates in pkg/agent (the promotion-age horizon
// lives on AgentLoop, which owns the per-session state this counter has to
// observe — pkg/tools has no visibility into it), and
// activeToolMetricsRecorder above is unexported. pkg/agent already imports
// pkg/tools, so this adds no new dependency and no import cycle.
func RecordToolSearchNoFollowUp() {
	toolSearchNoFollowUpCalls.Add(1)
	activeToolMetricsRecorder.IncToolSearchNoFollowUp()
}

// ToolSearchNoFollowUpCalls returns the current value of the
// omnipus_toolsearch_no_followup_total counter. Exported for tests.
func ToolSearchNoFollowUpCalls() uint64 {
	return toolSearchNoFollowUpCalls.Load()
}
