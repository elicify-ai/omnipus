// Omnipus — tests for knowledge_edit (ADR-068 D15.3, spec §4.1.4): byte
// preservation, the version-token compare-and-swap, schema-aware refusals,
// create-from-template, and the cross-tier redirects.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// veTool builds knowledge_edit over a fresh fixture's deps.
func veTool(deps AuthoringDeps) *EditTool { return NewEditTool(deps) }

// veSchema writes a "deal" record schema into root's control-plane
// directory, so schema-bound refusals have something to bind against.
func veSchema(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, records.VaultMarkerDirName, records.RecordsDirName)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	yaml := "schema_version: 1\n" +
		"type: deal\n" +
		"properties:\n" +
		"  status: { type: enum, values: [prospect, won, lost] }\n" +
		"  tags:   { type: text, many: true }\n" +
		"  amount: { type: decimal }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "deal.yaml"), []byte(yaml), 0o600))
}

// ---------------------------------------------------------------------------
// Deliverable 1 — byte-preserving writes
// ---------------------------------------------------------------------------

// TestKnowledgeEdit_ByteIdentical_AwkwardFile is the exact demonstration the
// brief asks for: a deliberately awkward file — a comment, an unusual key
// order, a blank line, one single-quoted and one unquoted value — round-
// tripped through set_property, with every byte OUTSIDE the touched property
// asserted identical, and the change itself isolated to exactly the touched
// value.
func TestKnowledgeEdit_ByteIdentical_AwkwardFile(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	awkward := "---\n" +
		"# a hand-written note — comment, odd order, blank line\n" +
		"tags: [alpha, beta]\n" +
		"\n" +
		"title: 'Quoted Title'\n" +
		"status: draft\n" +
		"---\n" +
		"Some prose the operator wrote, with **markdown** and a [[Link]].\n"
	a4Note(t, root, "Awkward.md", awkward)
	before := awkward

	version := a4Version(t, root, "Awkward.md")
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Awkward.md",
		"property": "status", "value": "active", "expect_version": version,
	})
	require.False(t, res.IsError, "unexpected refusal: %s", res.ForLLM)

	after := a4Read(t, root, "Awkward.md")

	// The change is confined to the "status" line's value: replace it back
	// and the file must be byte-for-byte the ORIGINAL, comment, blank line,
	// key order, single-quoted title and unquoted tags all included.
	roundTripped := strings.Replace(after, "status: active", "status: draft", 1)
	if roundTripped != before {
		t.Fatalf("write touched more than the target property.\nBEFORE:\n%q\nAFTER (with status reverted):\n%q",
			before, roundTripped)
	}
	if !strings.Contains(after, "# a hand-written note") {
		t.Fatalf("comment was lost:\n%s", after)
	}
	if !strings.Contains(after, "tags: [alpha, beta]") {
		t.Fatalf("existing list quoting/style was lost:\n%s", after)
	}
	if !strings.Contains(after, "title: 'Quoted Title'") {
		t.Fatalf("single-quoted value's quoting style was lost:\n%s", after)
	}
	if !strings.Contains(after, "status: active") {
		t.Fatalf("the intended change did not land:\n%s", after)
	}
}

// ---------------------------------------------------------------------------
// Deliverable 2 — list-valued splice, wired through the tool
// ---------------------------------------------------------------------------

func TestKnowledgeEdit_SetProperty_ListOpAddAndRemove(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Note.md", "---\ntags:\n  - alpha\n  - beta\n---\nBody.\n")

	v1 := a4Version(t, root, "Note.md")
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Note.md",
		"property": "tags", "list_op": "add", "value": "gamma", "expect_version": v1,
	})
	require.False(t, res.IsError, "add refused: %s", res.ForLLM)
	if got := a4Read(t, root, "Note.md"); !strings.Contains(got, "  - gamma\n") ||
		!strings.Contains(got, "  - alpha\n") || !strings.Contains(got, "  - beta\n") {
		t.Fatalf("add did not preserve existing items:\n%s", got)
	}

	v2 := a4Version(t, root, "Note.md")
	res2 := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Note.md",
		"property": "tags", "list_op": "remove", "value": "beta", "expect_version": v2,
	})
	require.False(t, res2.IsError, "remove refused: %s", res2.ForLLM)
	got := a4Read(t, root, "Note.md")
	if strings.Contains(got, "beta") {
		t.Fatalf("removed value still present:\n%s", got)
	}
	if !strings.Contains(got, "alpha") || !strings.Contains(got, "gamma") {
		t.Fatalf("unrelated items lost:\n%s", got)
	}

	// Removing an absent value a second time is a defined no-op, not an
	// error, and reports unchanged.
	v3 := a4Version(t, root, "Note.md")
	res3 := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Note.md",
		"property": "tags", "list_op": "remove", "value": "beta", "expect_version": v3,
	})
	require.False(t, res3.IsError, "no-op remove must not be refused: %s", res3.ForLLM)
	if !strings.Contains(res3.ForLLM, "unchanged") {
		t.Fatalf("no-op remove must report unchanged, got: %s", res3.ForLLM)
	}
}

// ---------------------------------------------------------------------------
// Deliverable 3 — create with a template argument
// ---------------------------------------------------------------------------

func TestKnowledgeEdit_Create_FromTemplate(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	tmplDir := TemplatesPath(root, Marker{})
	require.NoError(t, os.MkdirAll(tmplDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(tmplDir, "specimen.md"),
		[]byte("---\ntitle: {{title}}\ncreated: {{date}}\n---\n# {{title}}\n\nUnexpanded: {{nonsense}}\n"), 0o600))

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "Specimens/fern.md",
		"template": "specimen.md", "title": "Fern",
	})
	require.False(t, res.IsError, "create refused: %s", res.ForLLM)
	if !strings.Contains(res.ForLLM, `template "specimen.md"`) {
		t.Fatalf("response must name the template used: %s", res.ForLLM)
	}

	got := a4Read(t, root, "Specimens/fern.md")
	if !strings.Contains(got, "title: Fern") {
		t.Fatalf("{{title}} was not expanded:\n%s", got)
	}
	if !strings.Contains(got, "created: 2026-08-23") {
		t.Fatalf("{{date}} was not expanded to the fixed clock's date:\n%s", got)
	}
	if !strings.Contains(got, "# Fern") {
		t.Fatalf("body's {{title}} was not expanded:\n%s", got)
	}
	if !strings.Contains(got, "Unexpanded: {{nonsense}}") {
		t.Fatalf("an unrecognised token must be left literal, byte for byte:\n%s", got)
	}

	// Creating over the same path a second time never overwrites.
	res2 := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "Specimens/fern.md",
		"template": "specimen.md", "title": "Fern",
	})
	if !res2.IsError {
		t.Fatalf("create must refuse to overwrite an existing note")
	}
}

func TestKnowledgeEdit_Create_WithFrontmatterMapAndSchemaValidation(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "Deals/acme.md",
		"body": "# Acme\n",
		"frontmatter": map[string]any{
			"type":   "deal",
			"status": "prospect",
			"tags":   []any{"west", "enterprise"},
		},
	})
	require.False(t, res.IsError, "create refused: %s", res.ForLLM)
	got := a4Read(t, root, "Deals/acme.md")
	if !strings.Contains(got, "type: deal") || !strings.Contains(got, "status: prospect") {
		t.Fatalf("frontmatter map was not applied:\n%s", got)
	}
	if !strings.Contains(got, "  - west") || !strings.Contains(got, "  - enterprise") {
		t.Fatalf("list-valued frontmatter entry was not applied as a list:\n%s", got)
	}

	// An enum value the schema does not declare is refused, and the file is
	// never created.
	res2 := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "Deals/second.md",
		"frontmatter": map[string]any{"type": "deal", "status": "bogus"},
	})
	if !res2.IsError {
		t.Fatalf("create with an undeclared enum value must be refused")
	}
	if _, err := os.Stat(filepath.Join(root, "Deals", "second.md")); err == nil {
		t.Fatalf("a refused create must not leave a file on disk")
	}
}

// ---------------------------------------------------------------------------
// Deliverable 4 — optimistic concurrency
// ---------------------------------------------------------------------------

func TestKnowledgeEdit_StaleToken_RefusedWithBothTokensNamed(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Deals/Acme.md", "---\nstatus: prospect\n---\nBody.\n")
	staleToken := a4Version(t, root, "Deals/Acme.md")

	// The file changes on disk without going through the tool (Obsidian,
	// git, a sync agent — D14 tier 3's population).
	require.NoError(t, os.WriteFile(filepath.Join(root, "Deals", "Acme.md"),
		[]byte("---\nstatus: won\n---\nBody.\n"), 0o600))
	currentToken := a4Version(t, root, "Deals/Acme.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Deals/Acme.md",
		"property": "status", "value": "lost", "expect_version": staleToken,
	})
	if !res.IsError {
		t.Fatalf("a stale token must be refused, got success: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "{") {
		t.Fatalf("FR-072: the refusal must be compact text, not a JSON document: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Deals/Acme.md") {
		t.Fatalf("refusal must name the path: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, staleToken) {
		t.Fatalf("refusal must name the token the caller sent: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, currentToken) {
		t.Fatalf("refusal must name the file's current token: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "knowledge_read") {
		t.Fatalf("refusal must name the remedy (re-read with knowledge_read): %s", res.ForLLM)
	}

	// The file itself is untouched by the refused write.
	got := a4Read(t, root, "Deals/Acme.md")
	if !strings.Contains(got, "status: won") || strings.Contains(got, "lost") {
		t.Fatalf("a refused write must leave the file exactly as it was: %s", got)
	}

	// Retrying with the token the refusal just handed the caller succeeds —
	// AC-R2's "zero failed writes to obtain a usable token" in miniature.
	res2 := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Deals/Acme.md",
		"property": "status", "value": "lost", "expect_version": currentToken,
	})
	require.False(t, res2.IsError, "retry with the current token must succeed: %s", res2.ForLLM)
}

func TestKnowledgeEdit_MissingExpectVersion_Refused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Note.md", "---\nstatus: draft\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Note.md",
		"property": "status", "value": "active",
	})
	if !res.IsError {
		t.Fatalf("a write with no expect_version must be refused")
	}
	// The refusal is EditNote's own FR-106 compare-and-swap (author.go's
	// checkVersion, "EMPTY IS REFUSED TOO") — there is no separate
	// tool-level check to name here, and asserting the FR-106 wording
	// proves that layer, not just "some error came back", actually fired.
	if !strings.Contains(res.ForLLM, "version token") {
		t.Fatalf("refusal must be FR-106's own (naming the version token), got: %s", res.ForLLM)
	}
	if !strings.Contains(a4Read(t, root, "Note.md"), "status: draft") {
		t.Fatalf("a refused write must not change the file")
	}
}

// ---------------------------------------------------------------------------
// Deliverable 5 — refusals name the valid alternatives
// ---------------------------------------------------------------------------

func TestKnowledgeEdit_UnknownProperty_NamesDeclaredOnes(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Deals/Acme.md", "---\ntype: deal\nstatus: prospect\n---\nBody.\n")
	v := a4Version(t, root, "Deals/Acme.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Deals/Acme.md",
		"property": "priorty", "value": "high", "expect_version": v, // deliberate typo
	})
	if !res.IsError {
		t.Fatalf("an undeclared property must be refused")
	}
	for _, want := range []string{"priorty", "status", "tags", "amount"} {
		if !strings.Contains(res.ForLLM, want) {
			t.Fatalf("refusal must name the offending property and the declared set (missing %q): %s", want, res.ForLLM)
		}
	}
}

func TestKnowledgeEdit_EnumValueNotDeclared_NamesPermittedValues(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Deals/Acme.md", "---\ntype: deal\nstatus: prospect\n---\nBody.\n")
	v := a4Version(t, root, "Deals/Acme.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Deals/Acme.md",
		"property": "status", "value": "closed-won", "expect_version": v,
	})
	if !res.IsError {
		t.Fatalf("an undeclared enum value must be refused")
	}
	for _, want := range []string{"closed-won", "prospect", "won", "lost"} {
		if !strings.Contains(res.ForLLM, want) {
			t.Fatalf("refusal must name the offending value and the permitted set (missing %q): %s", want, res.ForLLM)
		}
	}
	// The file must be untouched by the refused write.
	got := a4Read(t, root, "Deals/Acme.md")
	if !strings.Contains(got, "status: prospect") {
		t.Fatalf("refused write must not change the file: %s", got)
	}
}

func TestKnowledgeEdit_WrongArity_NamesTheFix(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Deals/Acme.md", "---\ntype: deal\nstatus: prospect\n---\nBody.\n")
	v := a4Version(t, root, "Deals/Acme.md")

	// status is declared scalar; sending a list is an arity mismatch.
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Deals/Acme.md",
		"property": "status", "value": []any{"prospect", "won"}, "expect_version": v,
	})
	if !res.IsError {
		t.Fatalf("a scalar property fed a list must be refused")
	}
	if !strings.Contains(res.ForLLM, "many: true") {
		t.Fatalf("refusal must name the fix (declare many: true): %s", res.ForLLM)
	}
}

// ---------------------------------------------------------------------------
// Issue 6 / F3 — a comma-joined scalar auto-splits into a list for a
// many-valued property, matching the corpus's own on-disk convention
// (`tags: a, b`) so an agent that reads a note and writes its tags back
// unmodified no longer fails every time.
// ---------------------------------------------------------------------------

func TestKnowledgeEdit_SetProperty_ManyValuedCommaString_AutoSplits(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Deals/Acme.md", "---\ntype: deal\nstatus: prospect\n---\nBody.\n")
	v := a4Version(t, root, "Deals/Acme.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Deals/Acme.md",
		"property": "tags", "value": "a, b", "expect_version": v,
	})
	require.False(t, res.IsError, "a comma-joined scalar for a many-valued property must auto-split: %s", res.ForLLM)

	got := a4Read(t, root, "Deals/Acme.md")
	if !strings.Contains(got, "- a\n") || !strings.Contains(got, "- b\n") {
		t.Fatalf("tags must be stored as a two-element list [a, b]:\n%s", got)
	}
}

// TestKnowledgeEdit_Create_ManyValuedCommaString_AutoSplits is the same fix,
// exercised through create's frontmatter argument rather than
// set_property's value argument — the other call site sharing
// decodeValueArg/knowledgeEditAutoSplitCommaList.
func TestKnowledgeEdit_Create_ManyValuedCommaString_AutoSplits(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "Deals/NewCo.md",
		"frontmatter": map[string]any{"type": "deal", "tags": "a, b"},
	})
	require.False(t, res.IsError, "create's frontmatter.tags comma-string must auto-split: %s", res.ForLLM)

	got := a4Read(t, root, "Deals/NewCo.md")
	if !strings.Contains(got, "- a\n") || !strings.Contains(got, "- b\n") {
		t.Fatalf("tags must be stored as a two-element list [a, b]:\n%s", got)
	}
}

// TestKnowledgeEdit_SetProperty_ManyValuedSingleScalar_AutoSplitsToOneElement
// covers the N=1 boundary: a scalar with NO comma is still the same
// comma-joined shape (a one-element list), so it must split too, not just
// the two-or-more-element case.
func TestKnowledgeEdit_SetProperty_ManyValuedSingleScalar_AutoSplitsToOneElement(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Deals/Acme.md", "---\ntype: deal\nstatus: prospect\n---\nBody.\n")
	v := a4Version(t, root, "Deals/Acme.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Deals/Acme.md",
		"property": "tags", "value": "solo", "expect_version": v,
	})
	require.False(t, res.IsError, "a bare scalar for a many-valued property must auto-split to a one-element list: %s", res.ForLLM)

	got := a4Read(t, root, "Deals/Acme.md")
	if !strings.Contains(got, "- solo\n") {
		t.Fatalf("tags must be stored as a one-element list [solo]:\n%s", got)
	}
}

// TestKnowledgeEdit_SetProperty_ManyValuedExplicitList_NotReSplit documents
// the escape hatch: a caller who sends an EXPLICIT JSON list is never
// auto-split, so an element containing a literal comma survives exactly as
// sent — the ambiguity auto-split cannot resolve on its own is resolved by
// the caller choosing the unambiguous shape.
func TestKnowledgeEdit_SetProperty_ManyValuedExplicitList_NotReSplit(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Deals/Acme.md", "---\ntype: deal\nstatus: prospect\n---\nBody.\n")
	v := a4Version(t, root, "Deals/Acme.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Deals/Acme.md",
		"property": "tags", "value": []any{"New York, NY"}, "expect_version": v,
	})
	require.False(t, res.IsError, "an explicit list must not be refused: %s", res.ForLLM)

	got := a4Read(t, root, "Deals/Acme.md")
	if !strings.Contains(got, "New York, NY") {
		t.Fatalf("an explicit list's element must survive its literal comma unsplit:\n%s", got)
	}
}

// TestKnowledgeEdit_SetProperty_ManyValuedEnum_InvalidElementAfterSplit_StillRefused
// proves enum validation stays intact across the split (the brief's
// explicit requirement): a comma string auto-splits into elements, and one
// of those elements failing the property's OWN enum constraint must still
// be refused, exactly as an explicit list with the same invalid element
// would be.
func TestKnowledgeEdit_SetProperty_ManyValuedEnum_InvalidElementAfterSplit_StillRefused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	dir := filepath.Join(root, records.VaultMarkerDirName, records.RecordsDirName)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	yaml := "schema_version: 1\n" +
		"type: contact\n" +
		"properties:\n" +
		"  labels: { type: enum, many: true, values: [alpha, beta] }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "contact.yaml"), []byte(yaml), 0o600))
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "People/Jo.md", "---\ntype: contact\n---\nBody.\n")
	v := a4Version(t, root, "People/Jo.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "People/Jo.md",
		"property": "labels", "value": "alpha, gamma", "expect_version": v,
	})
	if !res.IsError {
		t.Fatalf("an invalid enum element surviving the split must still be refused: %s", res.ForLLM)
	}
	for _, want := range []string{"gamma", "alpha", "beta"} {
		if !strings.Contains(res.ForLLM, want) {
			t.Fatalf("refusal must name the offending value and the permitted set (missing %q): %s", want, res.ForLLM)
		}
	}
	got := a4Read(t, root, "People/Jo.md")
	if strings.Contains(got, "gamma") {
		t.Fatalf("refused write must not change the file: %s", got)
	}
}

func TestKnowledgeEdit_WrongType_NamesExpectedShape(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Deals/Acme.md", "---\ntype: deal\namount: 100\n---\nBody.\n")
	v := a4Version(t, root, "Deals/Acme.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Deals/Acme.md",
		"property": "amount", "value": "a lot", "expect_version": v,
	})
	if !res.IsError {
		t.Fatalf("a non-numeric value for a decimal property must be refused")
	}
	if !strings.Contains(res.ForLLM, "decimal") {
		t.Fatalf("refusal must name the expected shape: %s", res.ForLLM)
	}
}

// TestKnowledgeEdit_OrdinaryNote_NoSchemaBound proves FR-005: a note with no
// declared type (or none the vault's schemas recognise) is unconstrained —
// any property name and value is accepted.
func TestKnowledgeEdit_OrdinaryNote_NoSchemaBound(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veSchema(t, root) // schemas exist in the vault; this note just doesn't use one
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Note.md", "---\ntitle: Plain\n---\nBody.\n")
	v := a4Version(t, root, "Note.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Note.md",
		"property": "whatever-i-want", "value": "anything", "expect_version": v,
	})
	require.False(t, res.IsError, "an ordinary note's properties must be unconstrained: %s", res.ForLLM)
}

// ---------------------------------------------------------------------------
// The blast-radius rule (FR-070b) and cross-tier redirects
// ---------------------------------------------------------------------------

func TestKnowledgeEdit_CrossTierOps_RedirectByName(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Note.md", "---\nstatus: draft\n---\nBody.\n")

	cases := []struct{ op, want string }{
		{"rename", "knowledge_restructure"},
		{"move", "knowledge_restructure"},
		{"trash", "knowledge_restructure"},
		{"create_record_type", "knowledge_configure"},
		{"write_view", "knowledge_configure"},
	}
	for _, c := range cases {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": c.op, "path": "Note.md",
		})
		if !res.IsError {
			t.Fatalf("op %q must be refused by knowledge_edit", c.op)
		}
		if !strings.Contains(res.ForLLM, c.want) {
			t.Fatalf("op %q must name %s, got: %s", c.op, c.want, res.ForLLM)
		}
	}
}

// TestKnowledgeEdit_CrossTierRedirect_FiresRegardlessOfArgumentShape is the
// guarantee the file header states in words ("refused, never attempted
// under a different argument shape") turned into an assertion. Dispatch is
// entirely by the `op` STRING (Execute's switch), before any other argument
// is inspected — so a cascading op name is refused even when it arrives
// dressed as a different op's arguments, and a permitted op is not derailed
// into cascading behaviour by rename-shaped arguments riding along with it.
func TestKnowledgeEdit_CrossTierRedirect_FiresRegardlessOfArgumentShape(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Note.md", "---\nstatus: draft\n---\nBody.\n")
	v := a4Version(t, root, "Note.md")

	// op: "rename" carrying set_property's own argument shape (property,
	// value, expect_version) must still be redirected by name — the
	// redirect map is consulted before any of those fields is read.
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "rename", "path": "Note.md",
		"property": "status", "value": "active", "expect_version": v,
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "knowledge_restructure") {
		t.Fatalf("op %q with set_property-shaped arguments must still redirect to knowledge_restructure, got: %s (IsError=%v)",
			"rename", res.ForLLM, res.IsError)
	}
	if got := a4Read(t, root, "Note.md"); got != "---\nstatus: draft\n---\nBody.\n" {
		t.Fatalf("a redirected op must never write, got: %s", got)
	}

	// The converse: op: "set_property" carrying rename/move's own argument
	// names (new_name, dest) must NOT be derailed into a cascade — no
	// Renamed.md, no elsewhere/Note.md, ever. Code review B finding 7
	// changed WHAT proves that: this file used to let "new_name"/"dest"
	// through as "simply ignored", which is a silently-narrowed argument in
	// exactly the shape tools.go's own unknownArgs principle warns about —
	// a caller sending "new_name" reasonably believes it renamed something.
	// knowledge_edit now refuses any argument outside editArgNames before the
	// op switch runs at all, so the proof that set_property is not derailed
	// is stronger than before: the whole call is refused, INCLUDING the
	// property write, rather than silently applying the write while mutely
	// dropping the rename/move-shaped fields riding along with it.
	res2 := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Note.md",
		"property": "status", "value": "active", "expect_version": v,
		"new_name": "Renamed.md", "dest": "elsewhere/Note.md",
	})
	if !res2.IsError {
		t.Fatalf("set_property carrying unknown rename/move-shaped fields must be refused, got success: %s", res2.ForLLM)
	}
	for _, want := range []string{"new_name", "dest"} {
		if !strings.Contains(res2.ForLLM, want) {
			t.Fatalf("the refusal must name the unknown argument %q, got: %s", want, res2.ForLLM)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "Renamed.md")); err == nil {
		t.Fatalf("a 'new_name' argument on set_property must never cause a rename")
	}
	if _, err := os.Stat(filepath.Join(root, "elsewhere", "Note.md")); err == nil {
		t.Fatalf("a 'dest' argument on set_property must never cause a move")
	}
	if got := a4Read(t, root, "Note.md"); got != "---\nstatus: draft\n---\nBody.\n" {
		t.Fatalf("a refused call must not have applied the property write either, got: %s", got)
	}

	// The legitimate case still works unchanged: set_property with ONLY its
	// own accepted arguments applies normally.
	res3 := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Note.md",
		"property": "status", "value": "active", "expect_version": v,
	})
	require.False(t, res3.IsError, "set_property with only its own accepted arguments must succeed: %s", res3.ForLLM)
	if !strings.Contains(a4Read(t, root, "Note.md"), "status: active") {
		t.Fatalf("the ordinary property write must have applied to Note.md")
	}
}

func TestKnowledgeEdit_UnknownOp_NamesSupportedSet(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Note.md", "---\nstatus: draft\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "delete_everything", "path": "Note.md",
	})
	if !res.IsError {
		t.Fatalf("an unrecognised op must be refused")
	}
	for _, want := range knowledgeEditOps {
		if !strings.Contains(res.ForLLM, want) {
			t.Fatalf("refusal must list the supported ops (missing %q): %s", want, res.ForLLM)
		}
	}
}

// TestKnowledgeEdit_NeverTouchesAFileTheAgentDidNotName is the blast-radius
// criterion from the coordinator's directive, made concrete: every op
// writes exactly the named path (plus nothing) and never reaches into
// .omnipus-vault/ or any sibling file.
func TestKnowledgeEdit_NeverTouchesAFileTheAgentDidNotName(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Untouched.md", "---\nstatus: draft\n---\nSibling note.\n")
	a4Note(t, root, "Target.md", "---\nstatus: draft\n---\nBody.\n")
	sibling := a4Read(t, root, "Untouched.md")

	v := a4Version(t, root, "Target.md")
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Target.md",
		"property": "status", "value": "active", "expect_version": v,
	})
	require.False(t, res.IsError, "unexpected refusal: %s", res.ForLLM)

	if got := a4Read(t, root, "Untouched.md"); got != sibling {
		t.Fatalf("a set_property on Target.md must not touch Untouched.md:\nbefore: %q\nafter:  %q", sibling, got)
	}

	// A path that resolves inside the tool-state directory is refused
	// outright, never written.
	res2 := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": ".omnipus-vault/records/sneaky.yaml",
		"body": "type: sneaky\n",
	})
	if !res2.IsError {
		t.Fatalf("create must refuse a destination inside %s/", MarkerDirName)
	}
}
