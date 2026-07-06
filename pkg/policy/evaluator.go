package policy

import (
	"fmt"
	"strings"
)

// Evaluator checks command invocations against security policies.
// Immutable after construction — safe for concurrent use (SEC-12).
type Evaluator struct {
	defaultPolicy   DefaultPolicy
	execAllowedBins []string
}

// NewEvaluator creates a policy evaluator from a SecurityConfig.
// A nil config uses deny-by-default.
func NewEvaluator(cfg *SecurityConfig) *Evaluator {
	if cfg == nil {
		return &Evaluator{defaultPolicy: PolicyDeny}
	}
	dp := PolicyDeny
	if cfg.DefaultPolicy != "" {
		dp = cfg.DefaultPolicy
	}
	return &Evaluator{
		defaultPolicy:   dp,
		execAllowedBins: cfg.Policy.Exec.AllowedBinaries,
	}
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

// MatchGlob returns true if s matches pattern.
// Wildcards: '*' matches any sequence of characters (including empty);
// '?' matches exactly one character.
// Exported for use by pkg/security exec allowlist matching.
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
