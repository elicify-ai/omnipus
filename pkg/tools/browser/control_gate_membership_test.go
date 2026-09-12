package browser

// control_gate_membership_test.go — FR-040's structural half (S-64, capability
// spec §10 order 24a), AMENDED by ADR-085 R3-a/FR-035/FR-038 into a
// THREE-way partition. The exemption roster below used to be the whole
// complement of the gated set; it is now only ONE of two non-gated-write
// classes — capture (gated, unleased, unaudited) sits between action (fully
// gated) and exempt (this roster, gated by nothing at all).
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
// Since ADR-085 this is the EXEMPT class alone (three tools) — the three
// former "read-only" exemptions (screenshot/get_text/snapshot) moved to the
// CAPTURE class instead, declared separately in declaredCaptureClass below,
// because they DO call controlledResult now (D5) while still never taking
// the write lease or the per-call audit event.
//
// It is written as a literal here rather than derived from anything, because a
// derived exemption list cannot fail: whatever the code does becomes the
// expectation. The value of this map is that changing it is an edit somebody
// has to make and somebody else can read in a diff.
var declaredControlGateExemptions = map[string]string{
	"browser_list_tabs": "read-only: lists the tab set, injects nothing, changes nothing",
	"browser_wait":      "read-only: waits for an element to appear; it does not act on one",
	"browser_handover": "ADR-085 — the HANDOVER verb, exempt for the same shape of reason as " +
		"browser_handle_dialog and for none of the read-only ones. It is the tool that GIVES the " +
		"wheel to a human. Gating it on who currently holds the wheel is circular: the agent could " +
		"not hand over at precisely the moment handing over is what it needs to do, and a tool that " +
		"defers instead of handing over leaves the human waiting for a browser nobody offered them",
	"browser_handle_dialog": "FR-035 — the RECOVERY verb, exempt for a different reason from the " +
		"read-only ones. The click that raised the dialog is still blocked on CDP and still holds " +
		"the lease, and a human staring at a wedged tab has no button either, so gating it behind " +
		"the mechanisms the fault itself disables is a deadlock, not a safety property",
}

// declaredCaptureClass is the ADR-085 D5/FR-035 CAPTURE roster: gated by
// controlledResult (so it CAN defer to a held wheel), but never write-leased
// and never per-call audited via recordBrowserAction — reading pixels/text/
// the accessibility tree is not a page mutation, so it never needs to
// serialise behind another tool's in-flight write, but it CAN describe
// whatever a human is mid-typing if left ungated (the exposure D5 exists to
// close).
var declaredCaptureClass = map[string]string{
	"browser_screenshot": "captures pixels of whatever is on screen — including a credential a " +
		"human is mid-typing if left ungated; deferred while the wheel is held (ADR-085 D5)",
	"browser_get_text": "reads the page an operator may be actively driving; deferred while the " +
		"wheel is held (ADR-085 D5)",
	"browser_snapshot": "FR-038's accessibility-tree read; deferred while the wheel is held " +
		"(ADR-085 D5) but still never leased or per-call audited — capturing is not an action the " +
		"workspace's browser took",
}

// fr040Inclusions is the four ADR-075 D2 action verbs FR-040 is written about.
// Named separately from the set arithmetic below so AC2 fails with the tool's
// own name rather than as a diff between two long lists.
var fr040Inclusions = []string{
	"browser_select_option",
	"browser_press_key",
	"browser_hover",
	"browser_upload_file",
}

// TestBrowserTools_ControlGateMembershipMatchesExemptions is FR-040 AC2 + AC3,
// AMENDED by ADR-085 FR-035/FR-038 into a three-way assertion over the whole
// catalog: exactly action ∪ capture tools call controlledResult, and exactly
// the (now smaller) exempt roster does not.
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

	// The membership assertion itself, in both directions at once. wantGated
	// is now "everything except the exempt roster" — i.e. action ∪ capture —
	// which is the METADATA-scoped 14 of 18 (11 action + 3 capture; the
	// other four are the exempt roster above, browser_handover included).
	assert.ElementsMatch(t, wantGated, gated,
		"the controlledResult-gated set is not the registered catalog minus the declared "+
			"exemptions (action ∪ capture, ADR-085 FR-035). A name missing from the gated side is a "+
			"tool that acts on, or reads, a page a human may be driving with no deferral. A name "+
			"present on the gated side that is not in the catalog means this parse is reading "+
			"something that is not a registered tool")

	// Every exemption must name a tool that actually exists. Without this, a
	// renamed or deleted tool leaves a stale entry behind that quietly excuses
	// its successor from the gate.
	for name, reason := range declaredControlGateExemptions {
		assert.Contains(t, registered, name,
			"declaredControlGateExemptions excuses %q (%s), which is not a registered browser tool. "+
				"A stale exemption is how a real tool ends up ungated by accident", name, reason)
	}

	// AC2 — the four ADR-075 D2 inclusions, named individually.
	for _, name := range fr040Inclusions {
		assert.Contains(t, gated, name,
			"%s does not call controlledResult. It acts on the page, so it must stand down for a "+
				"human at the wheel — and under D1 §14 rule 3 dropping this call also removes the "+
				"tool from the write lease, which fails in the D1 spec's registry-driven test with "+
				"no local explanation", name)
	}

	// AC3, amended: browser_snapshot is NOW gated (ADR-085 moved it to
	// capture-class) — only browser_handle_dialog remains a genuine
	// exemption from BOTH gates, for the FR-035 recovery-verb reason.
	assert.Contains(t, gated, "browser_snapshot",
		"browser_snapshot must call controlledResult under ADR-085 D5/FR-035 — it moved from exempt "+
			"to capture-class, so it defers to a held wheel exactly like a screenshot does")
	assert.NotContains(t, gated, "browser_handle_dialog",
		"browser_handle_dialog calls controlledResult. %s",
		declaredControlGateExemptions["browser_handle_dialog"])

	// The three CAPTURE-class tools must ALSO be gated (they are part of
	// wantGated above), and must be exactly declaredCaptureClass — asserted
	// separately from the exempt-roster equality below so a capture tool
	// accidentally added to BOTH rosters, or to neither, is caught here
	// rather than only failing the broader membership assertion above with
	// no attribution to which roster is wrong.
	for name, reason := range declaredCaptureClass {
		assert.Contains(t, gated, name, "declaredCaptureClass names %q (%s), which must be "+
			"controlledResult-gated (ADR-085 D5)", name, reason)
		assert.NotContains(t, declaredControlGateExemptions, name,
			"%q is in BOTH declaredCaptureClass and declaredControlGateExemptions — every browser "+
				"tool belongs to exactly one of action/capture/exempt", name)
	}

	// The exempt roster and audit.go's exemptBrowserTools must be the same
	// set. They are two independently hand-maintained lists of the same
	// idea, so tying them together is what stops a new tool being
	// classified in one place and forgotten in the other.
	declaredExempt := make([]string, 0, len(exemptBrowserTools))
	for name := range exemptBrowserTools {
		declaredExempt = append(declaredExempt, name)
	}
	exemptNames := make([]string, 0, len(declaredControlGateExemptions))
	for name := range declaredControlGateExemptions {
		exemptNames = append(exemptNames, name)
	}
	assert.ElementsMatch(t, declaredExempt, exemptNames,
		"audit.go's exemptBrowserTools and this file's control-gate exemption roster disagree. "+
			"They are the same set stated twice — 'not gated' and 'not audited per call, not leased' "+
			"are one classification under the ADR-085 three-way split — so a tool in one and not "+
			"the other has an undecided treatment on whichever side forgot it")

	// The capture roster and audit.go's captureBrowserTools must likewise
	// agree.
	declaredCapture := make([]string, 0, len(captureBrowserTools))
	for name := range captureBrowserTools {
		declaredCapture = append(declaredCapture, name)
	}
	captureNames := make([]string, 0, len(declaredCaptureClass))
	for name := range declaredCaptureClass {
		captureNames = append(captureNames, name)
	}
	assert.ElementsMatch(t, declaredCapture, captureNames,
		"audit.go's captureBrowserTools and this file's declaredCaptureClass roster disagree")
}
