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

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	policy    string
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
func buildWildcardIndex(policies map[string]string) []wildcardEntry {
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
// Returns "" if no entry matches (caller falls back to default policy).
func resolveFromMap(toolName string, policies map[string]string, wildcards []wildcardEntry) string {
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
// merge (deny > ask > allow, with default-fill on each side). Both
// ResolveEffectivePolicy and FilterToolsByPolicy resolve through this so the
// security-critical merge rule lives in exactly one place — duplicating it
// invites silent drift. The wildcard indexes are pre-built once per call site
// and threaded in to avoid re-parsing the policy maps for every tool.
func resolveEffectivePolicyWith(
	cfg *ToolPolicyCfg,
	toolName string,
	agentWildcards, globalWildcards []wildcardEntry,
	defaultAgentPolicy, defaultGlobalPolicy string,
) string {
	// God-mode override (O14): the global "bypass-permissions" switch floors
	// EVERY tool's effective policy at "allow" — no permission prompts, no
	// deny. This is the #1 effect of god mode and intentionally short-circuits
	// the normal global×agent merge below. It is the single resolution-time
	// hook for the tool-policy half of god mode, so both ResolveEffectivePolicy
	// and FilterToolsByPolicy honor it identically. The override is
	// non-destructive: cfg.Policies / cfg.GlobalPolicies are NOT mutated, so
	// clearing cfg.GodMode restores the prior decision exactly.
	if cfg.GodMode {
		return "allow"
	}
	g := resolveFromMap(toolName, cfg.GlobalPolicies, globalWildcards)
	if g == "" {
		g = defaultGlobalPolicy
	}
	a := resolveFromMap(toolName, cfg.Policies, agentWildcards)
	if a == "" {
		a = defaultAgentPolicy
	}
	if g == "deny" || a == "deny" {
		return "deny"
	}
	if g == "ask" || a == "ask" {
		return "ask"
	}
	return "allow"
}

// ResolveEffectivePolicy returns the combined global+agent effective policy
// ("allow", "ask", or "deny") for a single tool name. It applies the same
// resolution order as FilterToolsByPolicy without iterating all tools.
// Callers that need to make one-off policy decisions (e.g. repair.go H3) use
// this rather than spinning up a full filter pass.
func ResolveEffectivePolicy(cfg *ToolPolicyCfg, toolName string) string {
	if cfg == nil {
		return "allow"
	}
	defaultAgentPolicy := cfg.DefaultPolicy
	if defaultAgentPolicy == "" {
		defaultAgentPolicy = "allow"
	}
	defaultGlobalPolicy := cfg.GlobalDefaultPolicy
	if defaultGlobalPolicy == "" {
		defaultGlobalPolicy = "allow"
	}
	agentWildcards := buildWildcardIndex(cfg.Policies)
	globalWildcards := buildWildcardIndex(cfg.GlobalPolicies)
	return resolveEffectivePolicyWith(
		cfg, toolName, agentWildcards, globalWildcards,
		defaultAgentPolicy, defaultGlobalPolicy,
	)
}

// ToolPolicyCfg is the per-agent tool policy configuration.
// Used by FilterToolsByPolicy.
type ToolPolicyCfg struct {
	DefaultPolicy string            // "allow", "ask", or "deny"
	Policies      map[string]string // per-tool overrides (supports trailing ".*" wildcards)

	// GlobalPolicies holds the operator-level global tool policy overrides.
	// Applied before agent-level policies; deny always wins (deny > ask > allow).
	GlobalPolicies      map[string]string // per-tool global overrides (supports wildcards)
	GlobalDefaultPolicy string            // fallback global policy when tool not in GlobalPolicies

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
// Resolution order (strictest wins: deny > ask > allow):
//  1. Global policy (GlobalPolicies / GlobalDefaultPolicy)
//  2. Agent policy (Policies / DefaultPolicy)
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
		cfg = &ToolPolicyCfg{DefaultPolicy: "allow"}
	}
	defaultAgentPolicy := cfg.DefaultPolicy
	if defaultAgentPolicy == "" {
		defaultAgentPolicy = "allow"
	}
	defaultGlobalPolicy := cfg.GlobalDefaultPolicy
	if defaultGlobalPolicy == "" {
		defaultGlobalPolicy = "allow"
	}

	// Pre-build wildcard indexes for O(W) matching per tool rather than O(K).
	agentWildcards := buildWildcardIndex(cfg.Policies)
	globalWildcards := buildWildcardIndex(cfg.GlobalPolicies)

	// Single shared resolver — same merge rule as ResolveEffectivePolicy
	// (deny > ask > allow with default-fill). Defined once here so the
	// security-critical precedence logic is not duplicated.
	resolveEffective := func(toolName string) string {
		return resolveEffectivePolicyWith(
			cfg, toolName, agentWildcards, globalWildcards,
			defaultAgentPolicy, defaultGlobalPolicy,
		)
	}

	out := make([]Tool, 0, len(allTools))
	policyMap := make(map[string]string)

	for _, t := range allTools {
		scope := t.Scope()

		// Layer 1: scope gate (structural constraint — cannot be bypassed by policy).
		// For ScopeCore tools, core agents pass through; custom agents are let through
		// only when their effective policy is not "deny" (the policy layer governs access).
		// ScopeGeneral tools always pass.
		if scope == ScopeCore && !passesScopeGate(scope, agentType) {
			// Custom agent: allowed only if effective policy is not "deny".
			p := resolveEffective(t.Name())
			if p == "deny" {
				activeToolMetricsRecorder.IncFilterTotal(agentType, "deny")
				continue
			}
		} else if !passesScopeGate(scope, agentType) {
			activeToolMetricsRecorder.IncFilterTotal(agentType, "deny")
			continue
		}

		// Layer 2: effective policy gate — deny removes the tool entirely.
		effectivePolicy := resolveEffective(t.Name())
		if effectivePolicy == "deny" {
			activeToolMetricsRecorder.IncFilterTotal(agentType, "deny")
			continue
		}

		activeToolMetricsRecorder.IncFilterTotal(agentType, effectivePolicy)
		out = append(out, t)
		policyMap[t.Name()] = effectivePolicy
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
// packages for FR-039 metrics (IncFilterTotal, IncCollisionTotal).
// pkg/gateway implements this with its toolMetrics type; pkg/tools uses the
// unexported interface so there is no import cycle.
type toolMetricsRecorder interface {
	IncFilterTotal(agentType, effectivePolicy string)
	IncCollisionTotal(conflictWith string)
}

// nopToolMetrics satisfies toolMetricsRecorder when no gateway is wired.
type nopToolMetrics struct{}

func (nopToolMetrics) IncFilterTotal(_, _ string) {}
func (nopToolMetrics) IncCollisionTotal(_ string) {}

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
