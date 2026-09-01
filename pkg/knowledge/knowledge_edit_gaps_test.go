// Omnipus — tests for the three write-path type-safety gaps closed against
// docs/internal/design/knowledge-write-path-type-safety.md:
//
//	G1  op:create validates the WHOLE assembled note's frontmatter (raw body
//	    and expanded template bytes included), not only the `frontmatter`
//	    argument map.
//	G3  an ungoverned write (unparsable frontmatter, no declared type, an
//	    undeclared type) now says so explicitly in the tool result, instead
//	    of being indistinguishable from a write that was checked and passed.
//	G4  a record type whose OWN schema file was rejected at load time is
//	    reported by name, distinctly from a type nobody ever declared.
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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// veBrokenDealSchema writes a "deal" record schema that ParseSchema MUST
// reject (schema_version is omitted — FR-002/RejectMissingVersion) but which
// still declares `type: deal`, so records.SchemaLoadReport.Rejections carries
// an entry naming that type (G4's distinguishing case). Returns the schema
// file's own bytes so a test can assert on the exact rejection text it
// expects to see echoed back.
func veBrokenDealSchema(t *testing.T, root string) []byte {
	t.Helper()
	dir := filepath.Join(root, records.VaultMarkerDirName, records.RecordsDirName)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	yaml := "type: deal\n" +
		"properties:\n" +
		"  status: { type: enum, values: [prospect, won, lost] }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "deal.yaml"), []byte(yaml), 0o600))
	return []byte(yaml)
}

// ---------------------------------------------------------------------------
// G1 — op:create validates the WHOLE assembled frontmatter, not only the
// `frontmatter` argument map.
// ---------------------------------------------------------------------------

// TestKnowledgeEdit_Create_RawBodyFrontmatterIsValidated is the exact
// scenario the design doc's G1 entry names: a `body` argument that carries
// its own `---` frontmatter block with a bad value for a declared property
// (amount: decimal on the "deal" schema). Before this fix, `body` bytes went
// straight to CreateNote unchecked and this write landed on disk verbatim.
func TestKnowledgeEdit_Create_RawBodyFrontmatterIsValidated(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veSchema(t, root) // "deal": status enum, tags list, amount decimal
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	// Precondition: the "deal" schema really did load, and really does
	// declare "amount" as decimal — otherwise this test would pass for the
	// wrong reason (nothing governing the write at all, per G3, rather than
	// the value genuinely failing decimal parsing).
	set, report, err := records.LoadSchemas(root)
	require.NoError(t, err)
	require.True(t, report.OK(), "fixture schema must load cleanly: %v", report.Rejections)
	dealSchema, ok := set.Get("deal")
	require.True(t, ok, "fixture must declare a \"deal\" schema")
	amountProp, ok := dealSchema.Property("amount")
	require.True(t, ok, "fixture \"deal\" schema must declare \"amount\"")
	require.Equal(t, records.TypeDecimal, amountProp.Type,
		"test proves nothing unless \"amount\" is declared decimal")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "Deals/raw-body.md",
		"body": "---\ntype: deal\nstatus: prospect\namount: not-a-number\n---\n# Acme\n",
	})
	if !res.IsError {
		t.Fatalf("a raw body's own bad frontmatter value must be refused, got success: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "amount") {
		t.Fatalf("refusal must name the offending property: %s", res.ForLLM)
	}
	if _, statErr := os.Stat(filepath.Join(root, "Deals", "raw-body.md")); statErr == nil {
		t.Fatalf("a refused create must not leave a file on disk")
	}
}

// TestKnowledgeEdit_Create_RawBodyFrontmatterUnknownPropertyIsValidated
// covers the OTHER sentinel this layer already had for the argument-map
// path (ErrUnknownProperty) — proving G1 reuses the exact same vocabulary
// rather than a value-only special case.
func TestKnowledgeEdit_Create_RawBodyFrontmatterUnknownPropertyIsValidated(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "Deals/typo.md",
		"body": "---\ntype: deal\nstatuz: prospect\n---\nBody.\n",
	})
	if !res.IsError {
		t.Fatalf("an undeclared property written via raw body must be refused, got success: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "statuz") {
		t.Fatalf("refusal must name the offending property: %s", res.ForLLM)
	}
}

// TestKnowledgeEdit_Create_RawBodyFrontmatterValidValuePasses is the positive
// control for the two refusal tests above: a body carrying its own,
// perfectly conforming frontmatter block must still be allowed straight
// through, so the new check is a genuine gate and not a create-time refusal
// of every body with a frontmatter block.
func TestKnowledgeEdit_Create_RawBodyFrontmatterValidValuePasses(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "Deals/valid-body.md",
		"body": "---\ntype: deal\nstatus: prospect\namount: 1250.50\n---\n# Acme\n",
	})
	require.False(t, res.IsError, "a conforming raw-body frontmatter block must not be refused: %s", res.ForLLM)
	got := a4Read(t, root, "Deals/valid-body.md")
	if !strings.Contains(got, "amount: 1250.50") {
		t.Fatalf("valid body content must be written unchanged:\n%s", got)
	}
	// A conforming write is governed and has nothing further to report.
	if strings.Contains(res.ForLLM, "NOTE:") {
		t.Fatalf("a fully governed, conforming write must carry no governance NOTE: %s", res.ForLLM)
	}
}

// TestKnowledgeEdit_Create_TemplateFrontmatterIsValidated covers the template
// half of G1: a template's OWN frontmatter defaults, expanded before this
// tool ever sees a `frontmatter` argument, must be checked too.
func TestKnowledgeEdit_Create_TemplateFrontmatterIsValidated(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	tmplDir := TemplatesPath(root, Marker{})
	require.NoError(t, os.MkdirAll(tmplDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(tmplDir, "deal.md"),
		[]byte("---\ntype: deal\nstatus: bogus-status\n---\n# {{title}}\n"), 0o600))

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "Deals/from-template.md",
		"template": "deal.md", "title": "Acme",
	})
	if !res.IsError {
		t.Fatalf("a template's own bad frontmatter default must be refused, got success: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "status") {
		t.Fatalf("refusal must name the offending property: %s", res.ForLLM)
	}
	if _, statErr := os.Stat(filepath.Join(root, "Deals", "from-template.md")); statErr == nil {
		t.Fatalf("a refused create must not leave a file on disk")
	}
}

// ---------------------------------------------------------------------------
// G3 — an ungoverned write says so, and distinguishes WHY.
// ---------------------------------------------------------------------------

// TestKnowledgeEdit_SetProperty_NoTypeDeclared_ReportsGovernanceNote covers
// the "absent type" case: the write is ALLOWED (FR-005/class 5's ruling),
// but the result must say plainly that nothing was checked.
func TestKnowledgeEdit_SetProperty_NoTypeDeclared_ReportsGovernanceNote(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Note.md", "---\ntitle: Untyped\n---\nBody.\n")
	v := a4Version(t, root, "Note.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Note.md",
		"property": "anything", "value": "goes", "expect_version": v,
	})
	require.False(t, res.IsError, "an untyped note's write must still be ALLOWED: %s", res.ForLLM)
	if !strings.Contains(res.ForLLM, "NOTE:") || !strings.Contains(res.ForLLM, "no record type") {
		t.Fatalf("result must say plainly that no schema governed this write: %s", res.ForLLM)
	}
}

// TestKnowledgeEdit_SetProperty_UnknownType_ReportsGovernanceNote covers the
// "declared type nobody defined a schema for" case, and — critically for
// G3's "three distinct misses" — the wording must differ from the
// no-type-declared case above so the two are not indistinguishable.
func TestKnowledgeEdit_SetProperty_UnknownType_ReportsGovernanceNote(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veSchema(t, root) // declares "deal" only
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Note.md", "---\ntype: alien\n---\nBody.\n")
	v := a4Version(t, root, "Note.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Note.md",
		"property": "anything", "value": "goes", "expect_version": v,
	})
	require.False(t, res.IsError, "an undeclared-type note's write must still be ALLOWED: %s", res.ForLLM)
	if !strings.Contains(res.ForLLM, "NOTE:") || !strings.Contains(res.ForLLM, `"alien"`) ||
		!strings.Contains(res.ForLLM, "no schema in this vault") {
		t.Fatalf("result must name the undeclared type and say no schema governed this write: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "no record type") {
		t.Fatalf("the undeclared-type case must not read identically to the no-type-declared case: %s", res.ForLLM)
	}
}

// TestKnowledgeEditResolveSchema_UnparsableFrontmatter_ReasonIsDistinct is a
// white-box check on the third G3 case (unparsable frontmatter). It is not
// exercised end-to-end through set_property because a note whose frontmatter
// cannot be parsed is refused earlier by the splice layer itself
// (ErrFrontmatterUnterminated — see knowledge_edit_schema_test.go), so there
// is no successful write for a governance NOTE to attach to; the reason code
// itself, and its distinct message, are what this test pins.
func TestKnowledgeEditResolveSchema_UnparsableFrontmatter_ReasonIsDistinct(t *testing.T) {
	set := records.NewSchemaSet()
	unparsable := []byte("---\ntitle: Old\n\nbody with no closing fence\n")

	schema, typeName, reason, detail := knowledgeEditResolveSchema(set, nil, unparsable)
	require.Equal(t, knowledgeEditUnparsable, reason)
	require.Nil(t, schema)
	require.Equal(t, "", typeName)
	require.Equal(t, "", detail)

	gov := knowledgeEditGovernance{Reason: reason}
	note := gov.Note()
	if !strings.Contains(note, "could not be parsed") {
		t.Fatalf("unparsable-frontmatter note must name the reason distinctly: %q", note)
	}
	if strings.Contains(note, "no record type") || strings.Contains(note, "no schema in this vault") {
		t.Fatalf("unparsable-frontmatter note must not read like either of the other two cases: %q", note)
	}
}

// ---------------------------------------------------------------------------
// G4 — a REJECTED schema file is reported by name, distinctly from a type
// nobody ever declared.
// ---------------------------------------------------------------------------

// TestKnowledgeEdit_SetProperty_RejectedSchema_WarnsButAllowsWrite is the
// core G4 scenario: the "deal" schema file exists but fails to load
// (schema_version omitted). The write must still be ALLOWED — refusing it
// would block a note whose only fault is a vault-level schema bug the
// caller cannot fix through knowledge_edit — but the result must say so
// loudly and by name, distinctly from "no schema in this vault" (G3's
// unknown-type case), naming the actual load failure.
func TestKnowledgeEdit_SetProperty_RejectedSchema_WarnsButAllowsWrite(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veBrokenDealSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	// Precondition: the fixture schema file really is rejected, and the
	// rejection really does name "deal" — otherwise a passing test would
	// prove nothing about G4 specifically.
	_, report, err := records.LoadSchemas(root)
	require.NoError(t, err)
	require.False(t, report.OK(), "fixture schema must be rejected for this test to mean anything")
	require.Contains(t, report.RejectedTypes(), "deal")

	a4Note(t, root, "Deals/broken.md", "---\ntype: deal\n---\nBody.\n")
	v := a4Version(t, root, "Deals/broken.md")

	// A property no valid "deal" schema would ever declare — proving
	// nothing constrained this write, since the schema that WOULD have
	// governed it never loaded.
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Deals/broken.md",
		"property": "totally_unconstrained_field", "value": "anything at all", "expect_version": v,
	})
	require.False(t, res.IsError, "a write to a note whose schema failed to load must still be ALLOWED: %s", res.ForLLM)
	if !strings.Contains(res.ForLLM, "NOTE:") || !strings.Contains(res.ForLLM, "deal") ||
		!strings.Contains(res.ForLLM, "schema_version") {
		t.Fatalf("result must name the type and echo the real load failure reason: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "no schema in this vault") {
		t.Fatalf("a REJECTED schema must read distinctly from an UNKNOWN type: %s", res.ForLLM)
	}
	got := a4Read(t, root, "Deals/broken.md")
	if !strings.Contains(got, "totally_unconstrained_field: anything at all") {
		t.Fatalf("the write must actually have gone through:\n%s", got)
	}
}

// TestKnowledgeEdit_Create_RejectedSchemaDistinctFromUnknownType is the create
// path's version of the same distinction, run against a genuinely unknown
// type in the SAME vault as a rejected one, to prove the two coexist and are
// told apart rather than one hiding the other.
func TestKnowledgeEdit_Create_RejectedSchemaDistinctFromUnknownType(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veBrokenDealSchema(t, root) // "deal" is a candidate, but rejected
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	resRejected := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "Deals/rejected.md",
		"body": "---\ntype: deal\nnote: unconstrained\n---\nBody.\n",
	})
	require.False(t, resRejected.IsError, "create must not be refused for a rejected-schema type: %s", resRejected.ForLLM)
	if !strings.Contains(resRejected.ForLLM, "schema_version") {
		t.Fatalf("create's governance note must echo the rejection reason: %s", resRejected.ForLLM)
	}

	resUnknown := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "Other/unknown.md",
		"body": "---\ntype: nevermind\nnote: unconstrained\n---\nBody.\n",
	})
	require.False(t, resUnknown.IsError, "create must not be refused for a genuinely unknown type: %s", resUnknown.ForLLM)
	if strings.Contains(resUnknown.ForLLM, "schema_version") {
		t.Fatalf("a genuinely unknown type must not be reported as a load failure: %s", resUnknown.ForLLM)
	}
	if !strings.Contains(resUnknown.ForLLM, "no schema in this vault") {
		t.Fatalf("a genuinely unknown type's note must say so: %s", resUnknown.ForLLM)
	}

	assert.NotEqual(t, resRejected.ForLLM, resUnknown.ForLLM,
		"a rejected schema and an unknown type must never render identically")
}
