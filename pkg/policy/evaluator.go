package policy

import (
	"fmt"
	"strings"
)

// Evaluator checks tool invocations against security policies.
// Immutable after construction — safe for concurrent use (SEC-12).
type Evaluator struct {
	defaultPolicy     DefaultPolicy
	agents            map[string]AgentPolicy
	execAllowedBins   []string
	toolPolicies      map[string]ToolPolicy
	defaultToolPolicy ToolPolicy
}

// NewEvaluator creates a policy evaluator from a SecurityConfig.
// A nil config uses deny-by-default with no agent policies.
func NewEvaluator(cfg *SecurityConfig) *Evaluator {
	if cfg == nil {
		return &Evaluator{defaultPolicy: PolicyDeny}
	}
	dp := PolicyDeny
	if cfg.DefaultPolicy != "" {
		dp = cfg.DefaultPolicy
	}
	return &Evaluator{
		defaultPolicy:     dp,
		agents:            cfg.Agents,
		execAllowedBins:   cfg.Policy.Exec.AllowedBinaries,
		toolPolicies:      cfg.ToolPolicies,
		defaultToolPolicy: cfg.DefaultToolPolicy,
	}
}

// EvaluateTool checks whether an agent is permitted to invoke a tool.
//
// SCOPE (#438): this is NOT the live tool-name authority. Live tool-name access is
// resolved by pkg/tools.FilterToolsByPolicy (most-specific-wins). EvaluateTool and
// its tool-name helpers (resolveGlobalToolPolicy, builtinToolPolicies) use
// strictest-wins and have NO live caller — they are reachable only from tests.
// (The live pkg/policy path is Evaluator.EvaluateExec for exec COMMANDS, which uses
// its own execAllowedBins list and does not touch these tool-name helpers.) The two
// tool-name semantics diverge only on a specific-allow carve-out from a broad
// wildcard deny; that divergence is pinned by
// pkg/tools.TestCrossEngine_SpecificAllowCarveOut_EnginesDiverge. Do not reconcile
// without an ADR.
//
// When called with 2 args (agentID, toolName), the agent policy is looked up
// from the config provided at construction.
// When called with 3 args (agentID, toolName, *AgentPolicy), the explicit
// policy is used instead of config lookup.
//
// Evaluation order (SEC-04, SEC-07):
//  1. Check global tool_policies — deny wins outright; ask is held as a floor
//  2. Check agent-level tools.deny — if listed, deny (deny always wins)
//  3. Check agent-level tools.allow — if list exists and tool not in it, deny
//  4. If tools.allow is empty array, deny all (explicit empty = no tools)
//  5. If default_policy is "deny" and no explicit allow, deny
func (e *Evaluator) EvaluateTool(agentID, toolName string, agentPolicy ...*AgentPolicy) Decision {
	// Step 0: Check global tool policy (floor constraint).
	// Global "deny" blocks immediately, regardless of agent policy.
	// Global "ask" is the minimum — agent policy can only raise to "deny",
	// not lower back to "allow" (strictest wins).
	globalPolicy := e.resolveGlobalToolPolicy(toolName)
	if globalPolicy == ToolPolicyDeny {
		return Decision{
			Allowed:    false,
			Policy:     string(ToolPolicyDeny),
			PolicyRule: fmt.Sprintf("tool '%s' denied by global tool policy", toolName),
		}
	}

	// Use explicit policy if provided
	if len(agentPolicy) > 0 && agentPolicy[0] != nil {
		d := e.evaluateWithPolicy(agentID, toolName, agentPolicy[0])
		// Elevate to "ask" if global floor requires it and agent policy said "allow".
		if d.Allowed && d.Policy == "" && globalPolicy == ToolPolicyAsk {
			d.Policy = string(ToolPolicyAsk)
		} else if d.Policy == "" {
			d.Policy = string(ToolPolicyAllow)
		}
		return d
	}

	// Look up from config
	if ap, ok := e.agents[agentID]; ok {
		d := e.evaluateWithPolicy(agentID, toolName, &ap)
		if d.Allowed && d.Policy == "" && globalPolicy == ToolPolicyAsk {
			d.Policy = string(ToolPolicyAsk)
		} else if d.Policy == "" {
			d.Policy = string(ToolPolicyAllow)
		}
		return d
	}

	d := e.evaluateDefault(agentID, toolName)
	if d.Allowed && globalPolicy == ToolPolicyAsk {
		d.Policy = string(ToolPolicyAsk)
	} else if d.Allowed {
		d.Policy = string(ToolPolicyAllow)
	} else {
		d.Policy = string(ToolPolicyDeny)
	}
	return d
}

// resolveGlobalToolPolicy returns the effective global policy for a tool name.
// Resolution order:
//  1. User-configured tool_policies — exact match first, then glob scan (deny beats allow across patterns)
//  2. Builtin safety defaults — exact then glob scan
//  3. DefaultToolPolicy
//  4. ToolPolicyAllow
//
// When multiple glob patterns in tool_policies match the same tool name, "deny"
// takes precedence over "ask", and "ask" takes precedence over "allow" — the
// most restrictive matching pattern wins.
func (e *Evaluator) resolveGlobalToolPolicy(toolName string) ToolPolicy {
	// Scan user-configured tool_policies using glob matching.
	// Strictest matching policy wins (deny > ask > allow).
	if len(e.toolPolicies) > 0 {
		resolved := resolveStrictestPolicy(e.toolPolicies, toolName)
		if resolved != "" {
			return resolved
		}
	}

	// Builtin safety defaults (exact then glob).
	if resolved := resolveStrictestPolicy(builtinToolPolicies, toolName); resolved != "" {
		return resolved
	}

	if e.defaultToolPolicy != "" {
		return e.defaultToolPolicy
	}
	return ToolPolicyAllow
}

// resolveStrictestPolicy scans a pattern→policy map and returns the strictest
// policy matched by toolName (deny > ask > allow). Returns "" if no pattern matches.
func resolveStrictestPolicy(policies map[string]ToolPolicy, toolName string) ToolPolicy {
	best := ToolPolicy("")
	for pattern, pol := range policies {
		if !MatchGlob(pattern, toolName) {
			continue
		}
		// First match or stricter than current best.
		if best == "" || isStricter(pol, best) {
			best = pol
		}
	}
	return best
}

// isStricter returns true when candidate is more restrictive than current.
// Order: deny > ask > allow.
func isStricter(candidate, current ToolPolicy) bool {
	rank := map[ToolPolicy]int{
		ToolPolicyAllow: 0,
		ToolPolicyAsk:   1,
		ToolPolicyDeny:  2,
	}
	return rank[candidate] > rank[current]
}

func (e *Evaluator) evaluateWithPolicy(agentID, toolName string, ap *AgentPolicy) Decision {
	deny := ap.effectiveDeny()
	allow := ap.effectiveAllow()

	// Step 1: Check deny list — glob patterns supported; deny always wins.
	for _, denied := range deny {
		if MatchGlob(denied, toolName) {
			for _, allowed := range allow {
				if MatchGlob(allowed, toolName) {
					return Decision{
						Allowed: false,
						Policy:  string(ToolPolicyDeny),
						PolicyRule: fmt.Sprintf(
							"tool '%s' in tools.deny for agent '%s' (deny takes precedence over allow)",
							toolName,
							agentID,
						),
					}
				}
			}
			return Decision{
				Allowed:    false,
				Policy:     string(ToolPolicyDeny),
				PolicyRule: fmt.Sprintf("tool '%s' in tools.deny for agent '%s'", toolName, agentID),
			}
		}
	}

	// Step 2: Check allow list — glob patterns supported.
	if ap.hasAllowList() {
		if len(allow) == 0 {
			return Decision{
				Allowed:    false,
				Policy:     string(ToolPolicyDeny),
				PolicyRule: fmt.Sprintf("tools.allow is empty for agent '%s' (no tools permitted)", agentID),
			}
		}
		for _, allowed := range allow {
			if MatchGlob(allowed, toolName) {
				return Decision{
					Allowed:    true,
					Policy:     string(ToolPolicyAllow),
					PolicyRule: fmt.Sprintf("tools.allow matched '%s' for agent '%s'", toolName, agentID),
				}
			}
		}
		return Decision{
			Allowed:    false,
			Policy:     string(ToolPolicyDeny),
			PolicyRule: fmt.Sprintf("tool '%s' not in tools.allow for agent '%s'", toolName, agentID),
		}
	}

	return e.evaluateDefault(agentID, toolName)
}

// EvaluateExec checks whether an agent is permitted to execute a command
// against the exec allowlist (SEC-05).
func (e *Evaluator) EvaluateExec(agentID, command string) Decision {
	if len(e.execAllowedBins) == 0 {
		if e.defaultPolicy == PolicyDeny {
			binary := FirstToken(command)
			return Decision{
				Allowed:    false,
				PolicyRule: fmt.Sprintf("binary %q not in exec allowlist (empty allowlist)", binary),
			}
		}
		return Decision{
			Allowed:    true,
			PolicyRule: "exec allowed: no exec allowlist configured, default_policy is 'allow'",
		}
	}

	for _, pat := range e.execAllowedBins {
		if MatchGlob(pat, command) {
			return Decision{
				Allowed:    true,
				PolicyRule: fmt.Sprintf("exec allowed: command matched pattern %q", pat),
			}
		}
	}

	binary := FirstToken(command)
	return Decision{
		Allowed:    false,
		PolicyRule: fmt.Sprintf("binary %q not in exec allowlist", binary),
	}
}

func (e *Evaluator) evaluateDefault(agentID, toolName string) Decision {
	if e.defaultPolicy == PolicyDeny {
		return Decision{
			Allowed: false,
			Policy:  string(ToolPolicyDeny),
			PolicyRule: fmt.Sprintf(
				"default_policy is 'deny', no allow rule for tool '%s' (agent '%s')",
				toolName,
				agentID,
			),
		}
	}

	return Decision{
		Allowed:    true,
		Policy:     string(ToolPolicyAllow),
		PolicyRule: fmt.Sprintf("security.default_policy is 'allow', no deny rule matched for tool '%s'", toolName),
	}
}

// MatchGlob returns true if s matches pattern.
// Wildcards: '*' matches any sequence of characters (including empty);
// '?' matches exactly one character.
// Exported for use by pkg/security exec allowlist matching and tool policy evaluation.
func MatchGlob(pattern, s string) bool {
	// Fast path: no wildcards — exact match only.
	if !strings.ContainsAny(pattern, "*?") {
		return pattern == s
	}

	// Use iterative DP matching to handle both * and ?.
	// pi = index into pattern, si = index into s.
	// starPI and starSI track the last '*' position for backtracking.
	pi, si := 0, 0
	starPI, starSI := -1, -1

	for si < len(s) {
		if pi < len(pattern) && pattern[pi] == '*' {
			// Record position of star; advance pattern only.
			starPI = pi
			starSI = si
			pi++
		} else if pi < len(pattern) && (pattern[pi] == '?' || pattern[pi] == s[si]) {
			// '?' matches any single char; literal char matches itself.
			pi++
			si++
		} else if starPI >= 0 {
			// Mismatch but we have a prior '*': backtrack.
			// The '*' consumes one more character of s.
			starSI++
			si = starSI
			pi = starPI + 1
		} else {
			return false
		}
	}

	// Consume any trailing '*' patterns.
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}

	return pi == len(pattern)
}

// FirstToken returns the first space-separated token of s.
// Exported for use by pkg/security exec allowlist matching.
func FirstToken(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i]
	}
	return s
}

// EngineConfig configures the policy engine.
type EngineConfig struct {
	DefaultPolicy string
}

// Engine is a higher-level wrapper around the Evaluator for integration use.
type Engine struct {
	eval *Evaluator
}

// NewEngine creates a policy engine with the given config.
func NewEngine(cfg EngineConfig) *Engine {
	sc := &SecurityConfig{DefaultPolicy: DefaultPolicy(cfg.DefaultPolicy)}
	return &Engine{eval: NewEvaluator(sc)}
}

// Evaluate checks a tool invocation against an explicit agent policy.
func (eng *Engine) Evaluate(agentID, toolName string, agentPolicy *AgentPolicy) Decision {
	return eng.eval.EvaluateTool(agentID, toolName, agentPolicy)
}
