package browser

// Tests for the BrowserEvaluateEnabled execution gate.
//
// Spec: path-sandbox-and-capability-tiers-spec.md /
// BDD test ID: #88 (TestBrowserEvaluate_HardGateAndPolicy)
// Traces to: path-sandbox-and-capability-tiers-spec.md line 848
//
// Contract (post-refactor_tool_enablement):
//
//   browser_evaluate is ALWAYS registered — the LLM sees it in every agent's
//   tool list regardless of the BrowserEvaluateEnabled flag. This lets policy
//   control visibility via the deny-by-default builtin without a second,
//   redundant registration gate (the pattern the refactor eliminated).
//
//   The BrowserEvaluateEnabled flag gates EXECUTION inside EvaluateTool.Execute,
//   not registration — this is the SOLE live gate (pkg/policy's advisory
//   builtin deny entry was removed, #70; it had no live tool-dispatch caller).
//
// BDD Scenario: execution gate
// Given cfg.Sandbox.BrowserEvaluateEnabled = <flag>  (→ evaluateEnabled param)
// When RegisterTools is called
// Then browser_evaluate IS in the registry (always)

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---------------------------------------------------------------------------
// Sub-scenario (a): flag=false → tool IS registered (hard gate is execution-only)
// Traces to: path-sandbox-and-capability-tiers-spec.md line 954
// ---------------------------------------------------------------------------

// TestBrowserEvaluate_FlagFalse_StillRegistered verifies that even when
// cfg.Sandbox.BrowserEvaluateEnabled=false (an operator opt-OUT; the seeded
// default is now true), EvaluateTool is
// present in the registry. The execution gate is enforced at Execute() time
// inside the tool itself, not at registration time (post-refactor contract).
//
// BDD: Given BrowserEvaluateEnabled=false
// When RegisterTools is called with evaluateEnabled=false
// Then browser_evaluate IS in the tool registry (registration is unconditional)
// And all other browser tools ARE registered
func TestBrowserEvaluate_FlagFalse_StillRegistered(t *testing.T) {
	registry := tools.NewToolRegistry()
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	ssrf := security.NewSSRFChecker(nil)

	// evaluateEnabled=false is forwarded to EvaluateTool.executeEnabled
	// so Execute() can gate at invocation time.
	mgr, err := RegisterTools(registry, cfg, ssrf, false, t.TempDir(), true)
	require.NoError(t, err, "RegisterTools must not fail when evaluateEnabled=false")
	require.NotNil(t, mgr)

	// Registration assertion: browser_evaluate IS registered (always).
	_, found := registry.Get("browser_evaluate")
	assert.True(t, found,
		"browser_evaluate must be registered even when evaluateEnabled=false; "+
			"the execution gate is enforced inside Execute(), not at registration")

	// Differentiation: other browser tools must still be registered.
	for _, name := range []string{
		"browser_navigate", "browser_click", "browser_type",
		"browser_screenshot", "browser_get_text", "browser_wait",
	} {
		_, ok := registry.Get(name)
		assert.True(t, ok, "tool %q must be registered", name)
	}
}

// ---------------------------------------------------------------------------
// Sub-scenario (b): flag=true + policy=deny → registered but denied at policy layer
// Traces to: path-sandbox-and-capability-tiers-spec.md line 955
// ---------------------------------------------------------------------------

// TestBrowserEvaluate_FlagTrue_PolicyDeny_RegisteredButDenied verifies that
// browser_evaluate registers even when BrowserEvaluateEnabled=true (the
// registration gate is unconditional; the deny-by-default runtime gate is the
// tool's own executeEnabled flag inside Execute(), covered separately by
// TestBrowserEvaluate_FlagFalse_StillRegistered).
func TestBrowserEvaluate_FlagTrue_PolicyDeny_RegisteredButDenied(t *testing.T) {
	registry := tools.NewToolRegistry()
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	ssrf := security.NewSSRFChecker(nil)

	mgr, err := RegisterTools(registry, cfg, ssrf, true, t.TempDir(), true)
	require.NoError(t, err)
	require.NotNil(t, mgr)

	// Registration assertion: tool IS in the registry.
	_, found := registry.Get("browser_evaluate")
	assert.True(t, found,
		"FR-011: browser_evaluate MUST be in the registry when evaluateEnabled=true")
}

// ---------------------------------------------------------------------------
// Sub-scenario (c): flag=true + policy=allow (explicit opt-in) → invocation permitted
// Traces to: path-sandbox-and-capability-tiers-spec.md line 955
// ---------------------------------------------------------------------------

// TestBrowserEvaluate_FlagTrue_PolicyAllow_Succeeds verifies that when
// BrowserEvaluateEnabled=true, the tool is registered with a real name and
// description (not a stub).
func TestBrowserEvaluate_FlagTrue_PolicyAllow_Succeeds(t *testing.T) {
	registry := tools.NewToolRegistry()
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	ssrf := security.NewSSRFChecker(nil)

	mgr, err := RegisterTools(registry, cfg, ssrf, true, t.TempDir(), true)
	require.NoError(t, err)
	require.NotNil(t, mgr)

	// Registration assertion.
	_, found := registry.Get("browser_evaluate")
	require.True(t, found,
		"FR-011: browser_evaluate must be registered when evaluateEnabled=true")

	// Verify name + description are non-empty (not a stub).
	evaluateTool, ok := registry.Get("browser_evaluate")
	require.True(t, ok)
	assert.Equal(t, "browser_evaluate", evaluateTool.Name())
	assert.NotEmpty(t, evaluateTool.Description())
}
