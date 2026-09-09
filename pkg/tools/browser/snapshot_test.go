// Omnipus — browser_snapshot unit tests (D2 Stream D, §10 orders 22, 23).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// These drive renderSnapshot directly with synthetic accessibility nodes. That
// is deliberate rather than a shortcut: the cap, the ordering and the replacer
// are properties of the RENDER, and driving them through real Chrome would
// make each one depend on whatever a real page's AX tree happened to contain
// — which is how a cap test ends up asserting nothing because no fixture page
// was ever big enough to trip it.

package browser

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/chromedp/cdproto/accessibility"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// axValue builds an accessibility.Value carrying a JSON string, the shape CDP
// actually sends.
func axValue(s string) *accessibility.Value {
	raw, _ := json.Marshal(s)
	return &accessibility.Value{Value: raw}
}

// axNode builds one node. children are wired by the caller.
func axNode(id, role, name, value string, ignored bool, children ...string) *accessibility.Node {
	n := &accessibility.Node{
		NodeID:  accessibility.NodeID(id),
		Ignored: ignored,
		Role:    axValue(role),
		Name:    axValue(name),
	}
	if value != "" {
		n.Value = axValue(value)
	}
	for _, c := range children {
		n.ChildIDs = append(n.ChildIDs, accessibility.NodeID(c))
	}
	return n
}

// TestSnapshot_ReturnsRolesNamesHandles (FR-015) — the shape of one line.
func TestSnapshot_ReturnsRolesNamesHandles(t *testing.T) {
	nodes := []*accessibility.Node{
		axNode("1", "RootWebArea", "Sign in", "", false, "2", "3"),
		axNode("2", "textbox", "Email", "someone@example.com", false),
		axNode("3", "button", "Continue", "", false),
	}
	got := renderSnapshot(nodes)

	if got.NodeCount != 3 {
		t.Fatalf("NodeCount = %d, want 3\n%s", got.NodeCount, got.Text)
	}
	for _, want := range []string{
		`RootWebArea "Sign in" index=0`,
		`textbox "Email" value="someone@example.com" index=0`,
		`button "Continue" index=0`,
	} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("rendered outline is missing %q:\n%s", want, got.Text)
		}
	}
	// Children are indented under their parent, or the outline's nesting
	// describes nothing.
	if !strings.Contains(got.Text, "\n"+snapshotIndent+"textbox") {
		t.Errorf("child nodes are not indented under their parent:\n%s", got.Text)
	}
}

// TestSnapshot_ReturnsFieldValuesByDefault (FR-018) is the operator ruling,
// asserted with a FIXED oracle: a filled password field's value IS present.
//
// A conditional oracle ("present if values are enabled") would pass on a build
// that suppressed everything, which is exactly the outcome the ruling
// rejected. If this test is ever the thing standing between a user and a leak,
// it should be the thing that has to be deliberately deleted.
func TestSnapshot_ReturnsFieldValuesByDefault(t *testing.T) {
	nodes := []*accessibility.Node{
		axNode("1", "RootWebArea", "Login", "", false, "2"),
		axNode("2", "textbox", "Password", "hunter2", false),
	}
	got := renderSnapshot(nodes)
	if !strings.Contains(got.Text, `value="hunter2"`) {
		t.Errorf("a filled field's value is NOT in the snapshot. Values are emitted "+
			"unconditionally by operator ruling (FR-018); if that has been reversed it must be "+
			"reversed in the ruling, not here:\n%s", got.Text)
	}
	if got.ValueNodes != 1 {
		t.Errorf("ValueNodes = %d, want 1 — the audit event's value_nodes_emitted count is what "+
			"tells an operator a capture carried field contents at all", got.ValueNodes)
	}
}

// TestSnapshot_SchemaHasNoIncludeValues pins the ABSENCE of the parameter.
// A parameter that exists but must always be true is worse than none: an
// operator reading the schema would believe there is a control.
func TestSnapshot_SchemaHasNoIncludeValues(t *testing.T) {
	schema := (&SnapshotTool{}).Parameters()
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["include_values"]; ok {
		t.Error("browser_snapshot's schema advertises include_values. FR-018 makes values " +
			"unconditional, so this parameter would be a control that does nothing")
	}
	if len(props) != 0 {
		t.Errorf("browser_snapshot takes no arguments, but its schema declares %v", props)
	}
}

// TestSnapshot_IgnoredNodesAreDroppedButChildrenSurvive.
//
// Chrome marks wrapper elements ignored constantly. Dropping their SUBTREES
// would silently delete most of a modern page; indenting under them would
// produce nesting that corresponds to nothing visible.
func TestSnapshot_IgnoredNodesAreDroppedButChildrenSurvive(t *testing.T) {
	nodes := []*accessibility.Node{
		axNode("1", "RootWebArea", "Page", "", false, "2"),
		axNode("2", "generic", "wrapper", "", true, "3"),
		axNode("3", "button", "Buy", "", false),
	}
	got := renderSnapshot(nodes)
	if strings.Contains(got.Text, "wrapper") {
		t.Errorf("an Ignored node was rendered:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, `button "Buy"`) {
		t.Errorf("a visible node under an Ignored parent was LOST. Chrome marks wrappers ignored "+
			"constantly; dropping their subtrees deletes most of a real page:\n%s", got.Text)
	}
	// The button sits at the ignored wrapper's own depth, not one deeper.
	if !strings.Contains(got.Text, "\n"+snapshotIndent+`button "Buy"`) {
		t.Errorf("the surviving child is indented under a parent that was not rendered:\n%s", got.Text)
	}
}

// TestSnapshot_HandleIsPerRoleNameOccurrence is FR-016, and it is the
// assertion the whole handle contract rests on.
//
// The action tools' `index` disambiguates among the elements matching a given
// role+name (selectAXCandidate in target.go), NOT among all elements on the
// page. So the snapshot must render the per-(role, name) occurrence. A global
// line number would render a handle that LOOKS usable and resolves to the
// wrong element — the worst available failure, because it succeeds.
func TestSnapshot_HandleIsPerRoleNameOccurrence(t *testing.T) {
	nodes := []*accessibility.Node{
		axNode("1", "RootWebArea", "Cart", "", false, "2", "3", "4"),
		axNode("2", "button", "Remove", "", false),
		axNode("3", "link", "Details", "", false),
		axNode("4", "button", "Remove", "", false),
	}
	got := renderSnapshot(nodes)

	if strings.Count(got.Text, `button "Remove" index=0`) != 1 {
		t.Errorf("the first \"Remove\" button is not index=0:\n%s", got.Text)
	}
	if strings.Count(got.Text, `button "Remove" index=1`) != 1 {
		t.Errorf("the second \"Remove\" button is not index=1. If the handle were a global line "+
			"number it would read index=3 here, and browser_click{role:\"button\", name:\"Remove\", "+
			"index:3} would be out of range — or, on a page with four Remove buttons, would click "+
			"the wrong one:\n%s", got.Text)
	}
	// The unrelated link restarts its own counter — proof the counter is keyed
	// on (role, name) rather than shared across the document.
	if !strings.Contains(got.Text, `link "Details" index=0`) {
		t.Errorf("the index counter is not per (role, name):\n%s", got.Text)
	}
}

// TestChokePoint_PerSurfaceCap_Snapshot (FR-017).
//
// It asserts the CONSTANT is the source, not a literal 64000, and it asserts
// the three properties capGetText's prefix cut cannot give: whole nodes, valid
// UTF-8, and the top of the tree retained.
func TestChokePoint_PerSurfaceCap_Snapshot(t *testing.T) {
	// Multi-byte names on purpose: a byte cut in the middle of one of these
	// produces invalid UTF-8, which is the specific defect capGetText has.
	nodes := []*accessibility.Node{axNode("root", "RootWebArea", "第一", "", false)}
	var childIDs []string
	for i := 0; i < 4000; i++ {
		id := fmt.Sprintf("n%d", i)
		childIDs = append(childIDs, id)
		nodes = append(nodes, axNode(id, "button", fmt.Sprintf("ボタン%d", i), "", false))
	}
	for _, id := range childIDs {
		nodes[0].ChildIDs = append(nodes[0].ChildIDs, accessibility.NodeID(id))
	}

	got := renderSnapshot(nodes)

	if !got.Truncated {
		t.Fatalf("a 4001-node tree did not trip the cap (%d bytes rendered)", got.OutputBytes)
	}
	if got.OutputBytes > snapshotByteCap+200 {
		t.Errorf("output is %d bytes, which exceeds the %d-byte cap by more than the truncation "+
			"marker can account for", got.OutputBytes, snapshotByteCap)
	}
	if snapshotByteCap != config.DefaultBuiltinSuccessCap {
		t.Errorf("snapshotByteCap = %d but config.DefaultBuiltinSuccessCap = %d — the cap must be "+
			"the shared constant, or it goes stale silently the day that constant moves",
			snapshotByteCap, config.DefaultBuiltinSuccessCap)
	}
	if !utf8.ValidString(got.Text) {
		t.Error("truncation produced invalid UTF-8. This is the defect capGetText's prefix cut has " +
			"and the reason this tool does not use it")
	}
	// Whole nodes only: every line before the marker ends with a complete
	// index= handle. A mid-node cut would leave a dangling fragment.
	body := strings.Split(got.Text, "\n[truncated")[0]
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if !strings.Contains(line, "index=") {
			t.Errorf("a rendered line was cut mid-node: %q", line)
			break
		}
	}
	// The TOP of the tree survives — that is what makes the retained portion
	// worth having.
	if !strings.Contains(body, `RootWebArea "第一"`) {
		t.Error("truncation dropped the root. Keeping the tail instead of the head would leave the " +
			"agent with the bottom of a page and no idea what page it is")
	}
	// The marker names both facts.
	marker := fmt.Sprintf("[truncated at %d bytes; %d further nodes omitted]",
		snapshotByteCap, got.OmittedCount)
	if !strings.Contains(got.Text, marker) {
		t.Errorf("the truncation marker does not name the cap and the omitted count.\nwant: %q\ngot tail: %q",
			marker, got.Text[max(0, len(got.Text)-120):])
	}
	if got.OmittedCount <= 0 {
		t.Errorf("OmittedCount = %d on a truncated render", got.OmittedCount)
	}
}

// TestSnapshot_RoutedThroughSensitiveReplacer (FR-027).
//
// Defence in depth only. The test asserts BOTH halves honestly: a registered
// credential plaintext IS substituted, and an arbitrary form value is NOT —
// because a reader who saw only the first assertion would conclude the tool
// redacts what a page holds, and it does not.
func TestSnapshot_RoutedThroughSensitiveReplacer(t *testing.T) {
	prev := sensitiveReplacer.Load()
	t.Cleanup(func() { sensitiveReplacer.Store(prev) })
	SetSensitiveDataReplacer(strings.NewReplacer("sk-live-abc123", "[FILTERED]"))

	nodes := []*accessibility.Node{
		axNode("1", "RootWebArea", "Settings", "", false, "2", "3"),
		axNode("2", "textbox", "API key", "sk-live-abc123", false),
		axNode("3", "textbox", "Card number", "4111111111111111", false),
	}
	got := renderSnapshot(nodes)

	if strings.Contains(got.Text, "sk-live-abc123") {
		t.Errorf("a REGISTERED credential plaintext survived into the snapshot:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, "[FILTERED]") {
		t.Errorf("the replacer did not run at all:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, "4111111111111111") {
		t.Error("an arbitrary form value was removed. That is NOT what this control does, and a " +
			"test asserting it would misrepresent the tool's disclosure posture: the replacer " +
			"substitutes REGISTERED credential plaintexts and nothing else")
	}

	// OutputBytes is measured after the replacer, because that is the size
	// actually returned. [FILTERED] is a different length from what it
	// replaced, so a pre-replacer figure would describe nothing.
	if got.OutputBytes != len(got.Text) {
		t.Errorf("OutputBytes = %d but len(Text) = %d", got.OutputBytes, len(got.Text))
	}
}

// TestSnapshot_NilReplacerPassesThrough — the replacer is legitimately unset
// (filtering disabled, or a tool constructed outside the agent loop), and that
// must not panic or blank the output.
func TestSnapshot_NilReplacerPassesThrough(t *testing.T) {
	prev := sensitiveReplacer.Load()
	t.Cleanup(func() { sensitiveReplacer.Store(prev) })
	sensitiveReplacer.Store(nil)

	got := renderSnapshot([]*accessibility.Node{axNode("1", "button", "Go", "", false)})
	if !strings.Contains(got.Text, `button "Go"`) {
		t.Errorf("output was lost with no replacer installed: %q", got.Text)
	}
}

// TestSnapshot_AuditEventNameMatchesContractPattern (FR-028, SC-009).
//
// The AuditEntry contract pins the event-name shape. A dotted or
// capitalised name does not merely look untidy — a schema-invalid row blanks
// the operator's WHOLE Audit Log view rather than being skipped, so one bad
// event name costs them every other record too.
func TestSnapshot_AuditEventNameMatchesContractPattern(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-z_]+$`)
	for _, name := range []string{audit.EventBrowserSnapshot, audit.EventBrowserUploadFile} {
		if !pattern.MatchString(name) {
			t.Errorf("audit event %q does not match %s. The AuditEntry contract permits dots as of "+
				"#667, but underscore-only satisfies BOTH the contract and FR-058 and needs no "+
				"adjudication between them", name, pattern)
		}
	}
	if audit.EventBrowserSnapshot != "browser_snapshot" {
		t.Errorf("the snapshot audit event is %q; it must equal the TOOL name so an operator "+
			"filtering the Audit Log by what they saw in the chat thread finds it",
			audit.EventBrowserSnapshot)
	}
	if audit.EventBrowserUploadFile != "browser_upload_file" {
		t.Errorf("the upload audit event is %q, want \"browser_upload_file\"",
			audit.EventBrowserUploadFile)
	}
}

// TestSnapshot_IsNotWriteClass (FR-038) is the structural half of the
// exemption, asserted against the same classification maps the audit
// biconditional test reads — so the snapshot cannot drift into the gated set
// without both tests noticing.
func TestSnapshot_IsNotWriteClass(t *testing.T) {
	if writeClassBrowserTools["browser_snapshot"] {
		t.Error("browser_snapshot is classified write-class. It calls neither controlledResult nor " +
			"the write lease (FR-038): it is read-only and must answer while a human is driving " +
			"the tab and while another tool holds the lease")
	}
	if !readOnlyBrowserTools["browser_snapshot"] {
		t.Error("browser_snapshot is in NEITHER classification set. audit_test.go treats an " +
			"unclassified tool as a defect precisely so a new tool cannot default silently into " +
			"the exempt half")
	}
	for _, name := range []string{
		"browser_select_option", "browser_press_key", "browser_hover", "browser_upload_file",
	} {
		if !writeClassBrowserTools[name] {
			t.Errorf("%s is not write-class. All four call controlledResult, and under D1 §14 "+
				"rule 3's biconditional that IS their write-lease membership", name)
		}
	}
}
