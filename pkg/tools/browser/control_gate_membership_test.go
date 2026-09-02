package browser

// control_gate_membership_test.go — FR-040's structural half (S-64, capability
// spec §10 order 24a).
//
// WHAT THIS ADDS THAT THE TWO NEIGHBOURING TESTS DO NOT.
//
// The behaviour is already covered from two sides. interact_test.go's
// TestActionTools_DeferWhenHumanControls_Table drives all four new action tools
// against a controlled manager and watches each one stand down; audit_test.go's
// TestAudit_WriteClassSetIsTheControlledResultSet asserts, per Execute body,
// that a tool calls controlledResult if and only if it calls
// recordBrowserAction. Between them they pin the INCLUSIONS.
//
// Neither of them pins the EXEMPTIONS against the registry. Both compare the
// gated set to another set that is itself derived from the same wiring: the
// audit test's oracle is the audit classification, so a new tool added to both
// maps at once is consistent and invisible there. This test's oracle is
// different on purpose — it is the REGISTERED CATALOG minus a literal,
// reasoned exemption roster written out below. A new browser tool that arrives
// ungated therefore has to be added to that roster, with a stated reason,
// before this file goes green again.
//
// Under D1 §14's biconditional (a tool is write-leased iff it is
// controlledResult-gated) this membership IS lease membership, which is why
// D1's own TestWriteLease_EveryActionToolIsLeased fails — in the other
// document, for a reason invisible from there — if a controlledResult call is
// ever dropped from one of the four.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// declaredControlGateExemptions is the roster of registered browser tools that
// deliberately do NOT call controlledResult, each with the reason it is out.
//
// It is written as a literal here rather than derived from anything, because a
// derived exemption list cannot fail: whatever the code does becomes the
// expectation. The value of this map is that changing it is an edit somebody
// has to make and somebody else can read in a diff.
var declaredControlGateExemptions = map[string]string{
	"browser_list_tabs": "read-only: lists the tab set, injects nothing, changes nothing",
	"browser_screenshot": "read-only: captures pixels of whatever is already on screen, including " +
		"while a human drives",
	"browser_get_text": "read-only: reads the page an operator is already looking at",
	"browser_wait":     "read-only: waits for an element to appear; it does not act on one",
	"browser_snapshot": "FR-038 — read-only, and exempt so it can still answer 'what is on the " +
		"page' while a human holds the live view and while another tool holds the write lease",
	"browser_handle_dialog": "FR-035 — the RECOVERY verb, exempt for a different reason from the " +
		"read-only ones. The click that raised the dialog is still blocked on CDP and still holds " +
		"the lease, and a human staring at a wedged tab has no button either, so gating it behind " +
		"the mechanisms the fault itself disables is a deadlock, not a safety property",
}

// fr040Inclusions is the four ADR-072 D2 action verbs FR-040 is written about.
// Named separately from the set arithmetic below so AC2 fails with the tool's
// own name rather than as a diff between two long lists.
var fr040Inclusions = []string{
	"browser_select_option",
	"browser_press_key",
	"browser_hover",
	"browser_upload_file",
}

// TestBrowserTools_ControlGateMembershipMatchesExemptions is FR-040 AC2 + AC3
// as one assertion over the whole catalog: exactly the non-exempt tools call
// controlledResult, and exactly the exempt ones do not.
func TestBrowserTools_ControlGateMembershipMatchesExemptions(t *testing.T) {
	gatedReceivers, _ := executeBodyCallSites(t)
	require.NotEmpty(t, gatedReceivers,
		"the source parse found no controlledResult call sites at all. That is a broken parse, not "+
			"an ungated build — every assertion below would pass vacuously on an empty set")
	gated := toolNamesFor(t, gatedReceivers)

	registered := make([]string, 0, len(BrowserBuiltinMetadata()))
	wantGated := make([]string, 0, len(BrowserBuiltinMetadata()))
	for _, tool := range BrowserBuiltinMetadata() {
		name := tool.Name()
		registered = append(registered, name)
		if _, exempt := declaredControlGateExemptions[name]; !exempt {
			wantGated = append(wantGated, name)
		}
	}

	// The membership assertion itself, in both directions at once.
	assert.ElementsMatch(t, wantGated, gated,
		"the controlledResult-gated set is not the registered catalog minus the declared "+
			"exemptions. A name missing from the gated side is a tool that acts on a page a human "+
			"may be driving — and, under D1 §14's biconditional, one that has silently left the "+
			"write lease. A name present on the gated side that is not in the catalog means this "+
			"parse is reading something that is not a registered tool")

	// Every exemption must name a tool that actually exists. Without this, a
	// renamed or deleted tool leaves a stale entry behind that quietly excuses
	// its successor from the gate.
	for name, reason := range declaredControlGateExemptions {
		assert.Contains(t, registered, name,
			"declaredControlGateExemptions excuses %q (%s), which is not a registered browser tool. "+
				"A stale exemption is how a real tool ends up ungated by accident", name, reason)
	}

	// AC2 — the four inclusions, named individually.
	for _, name := range fr040Inclusions {
		assert.Contains(t, gated, name,
			"%s does not call controlledResult. It acts on the page, so it must stand down for a "+
				"human at the wheel — and under D1 §14 rule 3 dropping this call also removes the "+
				"tool from the write lease, which fails in the D1 spec's registry-driven test with "+
				"no local explanation", name)
	}

	// AC3 — the two ADR-072 D2 exemptions, named individually. The inclusion
	// and the exemption are one assertion over six names precisely so neither
	// can drift without the other failing.
	for _, name := range []string{"browser_snapshot", "browser_handle_dialog"} {
		assert.NotContains(t, gated, name,
			"%s calls controlledResult. %s", name, declaredControlGateExemptions[name])
	}

	// The roster and audit.go's read-only classification must be the same set.
	// They are two independently hand-maintained lists of the same idea, so
	// tying them together is what stops a new tool being classified in one
	// place and forgotten in the other.
	declaredReadOnly := make([]string, 0, len(readOnlyBrowserTools))
	for name := range readOnlyBrowserTools {
		declaredReadOnly = append(declaredReadOnly, name)
	}
	exemptNames := make([]string, 0, len(declaredControlGateExemptions))
	for name := range declaredControlGateExemptions {
		exemptNames = append(exemptNames, name)
	}
	assert.ElementsMatch(t, declaredReadOnly, exemptNames,
		"audit.go's readOnlyBrowserTools and this file's control-gate exemption roster disagree. "+
			"They are the same set stated twice — 'not gated' and 'not audited per call' are one "+
			"classification under §14 rule 3 — so a tool in one and not the other has an "+
			"undecided treatment on whichever side forgot it")
}
