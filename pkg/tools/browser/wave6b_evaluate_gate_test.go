package browser

// Tests for BrowserEvaluateEnabled execution gate + ToolPolicies interaction.
//
// Spec: path-sandbox-and-capability-tiers-spec.md /
// BDD test ID: #88 (TestBrowserEvaluate_HardGateAndPolicy)
// Traces to: path-sandbox-and-capability-tiers-spec.md line 848
//
// Contract (post-refactor_tool_enablement):
//
//   browser.evaluate is ALWAYS registered — the LLM sees it in every agent's
//   tool list regardless of the BrowserEvaluateEnabled flag. This lets policy
//   control visibility via the deny-by-default builtin without a second,
//   redundant registration gate (the pattern the refactor eliminated).
//
//   The BrowserEvaluateEnabled flag gates EXECUTION inside EvaluateTool.Execute,
//   not registration. Operators must opt in at two independent levels:
//     (a) Set cfg.Sandbox.BrowserEvaluateEnabled=true (execution gate).
//     (b) Override the policy floor to "ask"/"allow" (policy gate).
//
// BDD Scenario Outline: execution gate + policy precedence
// Given cfg.Sandbox.BrowserEvaluateEnabled = <flag>  (→ evaluateEnabled param)
// And cfg.Tools.ToolPolicies["browser.evaluate"] = <policy>
// When RegisterTools is called
// Then browser.evaluate IS in the registry (always)
// And ResolveToolPolicy("browser.evaluate") returns <expected>

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/policy"
	"github.com/dapicom-ai/omnipus/pkg/security"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// ---------------------------------------------------------------------------
// Sub-scenario (a): flag=false → tool IS registered (hard gate is execution-only)
// Traces to: path-sandbox-and-capability-tiers-spec.md line 954
// ---------------------------------------------------------------------------

// TestBrowserEvaluate_FlagFalse_StillRegistered verifies that even when
// cfg.Sandbox.BrowserEvaluateEnabled=false (the default), EvaluateTool is
// present in the registry. The execution gate is enforced at Execute() time
// inside the tool itself, not at registration time (post-refactor contract).
//
// BDD: Given BrowserEvaluateEnabled=false
// When RegisterTools is called with evaluateEnabled=false
// Then browser.evaluate IS in the tool registry (registration is unconditional)
// And all other browser tools ARE registered
func TestBrowserEvaluate_FlagFalse_StillRegistered(t *testing.T) {
	registry := tools.NewToolRegistry()
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	ssrf := security.NewSSRFChecker(nil)

	// evaluateEnabled=false is forwarded to EvaluateTool.executeEnabled
	// so Execute() can gate at invocation time.
	mgr, err := RegisterTools(registry, cfg, ssrf, false)
	require.NoError(t, err, "RegisterTools must not fail when evaluateEnabled=false")
	require.NotNil(t, mgr)

	// Registration assertion: browser.evaluate IS registered (always).
	_, found := registry.Get("browser.evaluate")
	assert.True(t, found,
		"browser.evaluate must be registered even when evaluateEnabled=false; "+
			"the execution gate is enforced inside Execute(), not at registration")

	// Differentiation: other browser tools must still be registered.
	for _, name := range []string{
		"browser.navigate", "browser.click", "browser.type",
		"browser.screenshot", "browser.get_text", "browser.wait",
	} {
		_, ok := registry.Get(name)
		assert.True(t, ok, "tool %q must be registered", name)
	}
}

// ---------------------------------------------------------------------------
// Sub-scenario (b): flag=true + policy=deny → registered but denied at policy layer
// Traces to: path-sandbox-and-capability-tiers-spec.md line 955
// ---------------------------------------------------------------------------

// TestBrowserEvaluate_FlagTrue_PolicyDeny_RegisteredButDenied verifies that when
// BrowserEvaluateEnabled=true but the builtin (or explicit) policy is "deny",
// the tool IS in the registry (registration gate passed) but the policy engine
// returns a deny decision (runtime gate).
//
// BDD: Given BrowserEvaluateEnabled=true
// And ToolPolicies["browser.evaluate"] = "deny" (the builtin default)
// When RegisterTools is called with evaluateEnabled=true
// Then browser.evaluate IS in the tool registry
// And ResolveToolPolicy("browser.evaluate") returns ToolPolicyDeny
func TestBrowserEvaluate_FlagTrue_PolicyDeny_RegisteredButDenied(t *testing.T) {
	registry := tools.NewToolRegistry()
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	ssrf := security.NewSSRFChecker(nil)

	mgr, err := RegisterTools(registry, cfg, ssrf, true)
	require.NoError(t, err)
	require.NotNil(t, mgr)

	// Registration assertion: tool IS in the registry.
	_, found := registry.Get("browser.evaluate")
	assert.True(t, found,
		"FR-011: browser.evaluate MUST be in the registry when evaluateEnabled=true")

	// Policy assertion: the builtin default for browser.evaluate is "deny".
	sc := &policy.SecurityConfig{}
	resolved := sc.ResolveToolPolicy("browser.evaluate")
	assert.Equal(t, policy.ToolPolicyDeny, resolved,
		"FR-011: browser.evaluate must be denied by the builtin policy when no user override exists")

	// Verify the builtin deny cannot be bypassed by a nil SecurityConfig either.
	var nilSC *policy.SecurityConfig
	resolvedNil := nilSC.ResolveToolPolicy("browser.evaluate")
	assert.Equal(t, policy.ToolPolicyDeny, resolvedNil,
		"FR-011: nil SecurityConfig must also deny browser.evaluate (builtin applies globally)")

	// Differentiation: a tool without a builtin deny policy must resolve to "allow"
	// under an empty SecurityConfig, proving the deny is specific to browser.evaluate.
	navigatePolicy := sc.ResolveToolPolicy("browser.navigate")
	assert.Equal(t, policy.ToolPolicyAllow, navigatePolicy,
		"browser.navigate has no builtin deny — must resolve to allow; proves deny is specific to evaluate")
}

// ---------------------------------------------------------------------------
// Sub-scenario (c): flag=true + policy=allow (explicit opt-in) → invocation permitted
// Traces to: path-sandbox-and-capability-tiers-spec.md line 955
// ---------------------------------------------------------------------------

// TestBrowserEvaluate_FlagTrue_PolicyAllow_Succeeds verifies that when
// BrowserEvaluateEnabled=true AND the operator explicitly sets policy to "allow",
// the tool is registered AND the policy engine returns "allow" — the invocation
// is permitted.
func TestBrowserEvaluate_FlagTrue_PolicyAllow_Succeeds(t *testing.T) {
	registry := tools.NewToolRegistry()
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	ssrf := security.NewSSRFChecker(nil)

	mgr, err := RegisterTools(registry, cfg, ssrf, true)
	require.NoError(t, err)
	require.NotNil(t, mgr)

	// Registration assertion.
	_, found := registry.Get("browser.evaluate")
	require.True(t, found,
		"FR-011: browser.evaluate must be registered when evaluateEnabled=true")

	// Policy assertion: explicit operator opt-in wins over builtin deny.
	sc := &policy.SecurityConfig{
		ToolPolicies: map[string]policy.ToolPolicy{
			"browser.evaluate": policy.ToolPolicyAllow,
		},
	}
	resolved := sc.ResolveToolPolicy("browser.evaluate")
	assert.Equal(t, policy.ToolPolicyAllow, resolved,
		"FR-011: explicit operator allow must override the builtin deny")

	// Verify name + description are non-empty (not a stub).
	evaluateTool, ok := registry.Get("browser.evaluate")
	require.True(t, ok)
	assert.Equal(t, "browser.evaluate", evaluateTool.Name())
	assert.NotEmpty(t, evaluateTool.Description())
}

// ---------------------------------------------------------------------------
// Scenario Outline table-driven: covers the post-refactor Examples
// Traces to: path-sandbox-and-capability-tiers-spec.md line 702–709
// ---------------------------------------------------------------------------

// TestBrowserEvaluate_HardGateAndPolicy_TableDriven implements the BDD
// Scenario Outline. browser.evaluate is always registered; the wantRegistered
// column is always true. The table retains the evaluateEnabled column so the
// execution-gate contract is visible for documentation purposes.
func TestBrowserEvaluate_HardGateAndPolicy_TableDriven(t *testing.T) {
	tests := []struct {
		name               string
		evaluateEnabled    bool
		policyOverride     policy.ToolPolicy
		wantRegistered     bool
		wantPolicyDecision policy.ToolPolicy
	}{
		{
			name:               "flag=false: tool always registered; execution gate inside Execute()",
			evaluateEnabled:    false,
			policyOverride:     "",
			wantRegistered:     true, // always registered (post-refactor)
			wantPolicyDecision: policy.ToolPolicyDeny,
		},
		{
			name:               "flag=true, policy=deny: registered; denied at policy layer",
			evaluateEnabled:    true,
			policyOverride:     "",
			wantRegistered:     true,
			wantPolicyDecision: policy.ToolPolicyDeny,
		},
		{
			name:               "flag=true, policy=ask: registered; awaits decision",
			evaluateEnabled:    true,
			policyOverride:     policy.ToolPolicyAsk,
			wantRegistered:     true,
			wantPolicyDecision: policy.ToolPolicyAsk,
		},
		{
			name:               "flag=true, policy=allow: registered; invocation permitted",
			evaluateEnabled:    true,
			policyOverride:     policy.ToolPolicyAllow,
			wantRegistered:     true,
			wantPolicyDecision: policy.ToolPolicyAllow,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			registry := tools.NewToolRegistry()
			cfg, err := DefaultConfig()
			require.NoError(t, err)
			ssrf := security.NewSSRFChecker(nil)

			_, regErr := RegisterTools(registry, cfg, ssrf, tc.evaluateEnabled)
			require.NoError(t, regErr)

			_, found := registry.Get("browser.evaluate")
			assert.Equal(t, tc.wantRegistered, found,
				"evaluateEnabled=%v: registration must match wantRegistered=%v",
				tc.evaluateEnabled, tc.wantRegistered)

			var sc *policy.SecurityConfig
			if tc.policyOverride != "" {
				sc = &policy.SecurityConfig{
					ToolPolicies: map[string]policy.ToolPolicy{
						"browser.evaluate": tc.policyOverride,
					},
				}
			} else {
				sc = &policy.SecurityConfig{}
			}
			resolved := sc.ResolveToolPolicy("browser.evaluate")
			assert.Equal(t, tc.wantPolicyDecision, resolved,
				"policy=%q: ResolveToolPolicy must return wantPolicyDecision=%q",
				tc.policyOverride, tc.wantPolicyDecision)
		})
	}
}
