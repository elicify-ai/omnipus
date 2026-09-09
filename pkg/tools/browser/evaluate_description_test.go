// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package browser

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// EvaluateTool.Description() is a STRING THE MODEL READS ON EVERY REQUEST, which
// makes it assertable in a way most prose is not. It told the model the tool was
// "off by default at RUNTIME regardless of your tool policy" — false once
// sandbox.browser_evaluate_enabled is seeded true, and a model that believes it
// will either not attempt the call or will apologise for a failure that did not
// happen.

// TestEvaluateTool_DescriptionDoesNotClaimOffByDefault is the negative half.
func TestEvaluateTool_DescriptionDoesNotClaimOffByDefault(t *testing.T) {
	desc := (&EvaluateTool{}).Description()
	lower := strings.ToLower(desc)

	for _, claim := range []string{
		"off by default",
		"disabled by default",
		"must set sandbox.browser_evaluate_enabled=true",
		"a policy of allow does not mean this tool is usable",
	} {
		if strings.Contains(lower, strings.ToLower(claim)) {
			t.Errorf("EvaluateTool.Description() still tells the model %q. sandbox.browser_evaluate_enabled is seeded true, so this is now false — and it is read on every request.", claim)
		}
	}
}

// TestEvaluateTool_DescriptionStillDescribesTheRuntimeDisable is the positive
// half, and without it the test above is satisfied by deleting the paragraph
// entirely — which would be a different bug.
//
// The runtime kill switch still exists. A model that hits it needs to be able to
// tell an installation-wide disable (a tool result whose error names the
// setting) from its own policy denying it (the tool is absent from its list
// altogether), because those send it to two completely different conclusions
// about what to tell the user.
func TestEvaluateTool_DescriptionStillDescribesTheRuntimeDisable(t *testing.T) {
	desc := (&EvaluateTool{}).Description()

	if !strings.Contains(desc, "sandbox.browser_evaluate_enabled") {
		t.Error("EvaluateTool.Description() no longer names sandbox.browser_evaluate_enabled at all. The runtime kill switch still exists; a model that hits it with no idea what it is cannot tell the user anything useful.")
	}
	if !strings.Contains(strings.ToLower(desc), "enabled by default") {
		t.Error("EvaluateTool.Description() does not say the tool is enabled by default, so the model has no reason to believe an attempt will succeed")
	}
	if !strings.Contains(strings.ToLower(desc), "policy") {
		t.Error("EvaluateTool.Description() no longer distinguishes an installation-wide disable from a tool-policy denial. They produce completely different observations — an error result versus the tool not being in the list — and conflating them makes a model report the wrong cause.")
	}
}

// TestEvaluateTool_EnabledDoesNotReturnDisabledString: with the gate on, the
// tool must not short-circuit on the disabled message.
//
// This is the behavioural companion to the description tests. A build where the
// description was corrected but the gate still refused would leave the model
// confidently attempting a call that always fails — strictly worse than the
// honest "off by default" it replaced.
func TestEvaluateTool_EnabledDoesNotReturnDisabledString(t *testing.T) {
	enabled := &EvaluateTool{executeEnabled: true}

	// No js argument: the tool must get PAST the enablement gate and fail on the
	// missing parameter instead. That is the observable difference between "the
	// gate let me through" and "the gate stopped me", without needing a real
	// browser.
	res := enabled.Execute(t.Context(), map[string]any{})
	if res == nil {
		t.Fatal("Execute returned nil")
	}
	body := resultText(res)
	if strings.Contains(body, "disabled") {
		t.Fatalf("EvaluateTool with executeEnabled=true returned the runtime-disabled message: %q", body)
	}
	if !strings.Contains(body, "'js' parameter is required") {
		t.Fatalf("EvaluateTool with executeEnabled=true did not reach its argument validation; got %q", body)
	}

	// And the gate still works when an operator has opted out — otherwise this
	// test passes against a build with no gate at all.
	disabled := &EvaluateTool{executeEnabled: false}
	res = disabled.Execute(t.Context(), map[string]any{"js": "1+1"})
	if res == nil {
		t.Fatal("Execute returned nil")
	}
	body = resultText(res)
	if !strings.Contains(body, "sandbox.browser_evaluate_enabled") {
		t.Fatalf("EvaluateTool with executeEnabled=false did not refuse naming the setting; got %q", body)
	}
}

// resultText flattens a ToolResult into something searchable, without depending
// on which field the message landed in.
func resultText(res *tools.ToolResult) string {
	return res.ForLLM + " " + res.ForUser
}
