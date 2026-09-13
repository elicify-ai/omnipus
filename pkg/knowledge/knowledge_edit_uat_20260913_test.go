// Omnipus — regression tests for the UAT 2026-09-13 defects fixed on the
// knowledge_edit / knowledge_read / knowledge_configure agent door (workstream
// W2 of docs/internal/uat/fix-plan-2026-09-14.md). Each test names the
// defect id it pins and reproduces the register's own repro.
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

// uatProjectSchema writes the UAT vault's "project" record type: an enum
// with lower-case members, a decimal, an integer, a checkbox and a many-
// valued text — the property mix D-04, D-49 and D-51 were found against.
func uatProjectSchema(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, records.VaultMarkerDirName, records.RecordsDirName)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	yaml := "schema_version: 1\n" +
		"type: project\n" +
		"identity:\n  prefix: PRJ\n" +
		"properties:\n" +
		"  status: { type: enum, values: [planned, active, done] }\n" +
		"  budget: { type: decimal }\n" +
		"  headcount: { type: integer }\n" +
		"  approved: { type: checkbox }\n" +
		"  tags: { type: text, many: true }\n" +
		"  owner: { type: person }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.yaml"), []byte(yaml), 0o600))
}

// ---------------------------------------------------------------------------
// D-04 — a wrong-case enum member is written in the DECLARED spelling
// ---------------------------------------------------------------------------

func TestUAT_D04_SetProperty_EnumWrittenInDeclaredCase(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	uatProjectSchema(t, root)
	a4Note(t, root, "P.md", "---\ntype: project\nid: PRJ-0001\nstatus: planned\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "P.md",
		"property": "status", "value": "Done", "expect_version": a4Version(t, root, "P.md"),
	})
	require.False(t, res.IsError, "case-insensitive enum match must still be accepted: %s", res.ForLLM)
	got := a4Read(t, root, "P.md")
	require.Contains(t, got, "status: done\n", "the file must carry the declared spelling, not the caller's:\n%s", got)
	require.NotContains(t, got, "Done")
}

func TestUAT_D04_Create_EnumCanonicalisedFromFrontmatterArgAndRawBody(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	uatProjectSchema(t, root)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "A.md",
		"frontmatter": map[string]any{"type": "project", "status": "ACTIVE"},
		"body":        "Hello.\n",
	})
	require.False(t, res.IsError, res.ForLLM)
	require.Contains(t, a4Read(t, root, "A.md"), "status: active\n")

	// Raw body bytes carrying their own frontmatter block go through the
	// assembled-frontmatter check and are canonicalised the same way.
	res2 := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "B.md",
		"body": "---\ntype: project\nstatus: Planned\n---\nHello.\n",
	})
	require.False(t, res2.IsError, res2.ForLLM)
	got := a4Read(t, root, "B.md")
	require.Contains(t, got, "status: planned\n", got)
	require.NotContains(t, got, "Planned")
}

// ---------------------------------------------------------------------------
// D-49 — declared numbers and checkboxes are written bare, not quoted
// ---------------------------------------------------------------------------

func TestUAT_D49_NumbersAndCheckboxesWrittenUnquoted(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	uatProjectSchema(t, root)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "N.md",
		"frontmatter": map[string]any{
			"type": "project", "budget": float64(480000), "headcount": float64(7),
			"approved": true, "status": "active",
		},
		"body": "Body.\n",
	})
	require.False(t, res.IsError, res.ForLLM)
	got := a4Read(t, root, "N.md")
	require.Contains(t, got, "budget: 480000\n", got)
	require.Contains(t, got, "headcount: 7\n", got)
	require.Contains(t, got, "approved: true\n", got)
	require.NotContains(t, got, `"480000"`)
	require.NotContains(t, got, `"true"`)

	// set_property on an existing record takes the same path.
	res2 := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "N.md",
		"property": "budget", "value": "0.5", "expect_version": a4Version(t, root, "N.md"),
	})
	require.False(t, res2.IsError, res2.ForLLM)
	require.Contains(t, a4Read(t, root, "N.md"), "budget: 0.5\n")

	// An UNGOVERNED note keeps SetProperty's defensive quoting: without a
	// schema nothing can say whether "7" is a number or the text "7".
	a4Note(t, root, "Plain.md", "---\nx: 1\n---\nBody.\n")
	res3 := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Plain.md",
		"property": "count", "value": "7", "expect_version": a4Version(t, root, "Plain.md"),
	})
	require.False(t, res3.IsError, res3.ForLLM)
	require.Contains(t, a4Read(t, root, "Plain.md"), `count: "7"`)
}

// ---------------------------------------------------------------------------
// D-51 — identity and virtual/derived writes get their own refusal class
// ---------------------------------------------------------------------------

func TestUAT_D51_ReservedPropertyRefusalsAreClassified(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	uatProjectSchema(t, root)
	a4Note(t, root, "P.md", "---\ntype: project\nid: PRJ-0001\nstatus: planned\n---\nBody.\n")
	a4Note(t, root, "Plain.md", "---\nid: X-1\n---\nBody.\n")

	cases := []struct {
		path, property, want string
	}{
		{"P.md", "id", "identity"},
		{"P.md", "omni_id", "identity"},
		{"P.md", "file.mtime", "virtual"},
		{"P.md", "file.backlinks", "virtual"},
		{"P.md", "formula.days_open", "derived"},
		{"Plain.md", "id", "identity"}, // ungoverned note: still not a caller's to write
	}
	for _, c := range cases {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "set_property", "path": c.path,
			"property": c.property, "value": "x", "expect_version": a4Version(t, root, c.path),
		})
		require.True(t, res.IsError, "%s must be refused", c.property)
		require.Contains(t, res.ForLLM, c.want, "%s: %s", c.property, res.ForLLM)
		require.NotContains(t, res.ForLLM, "declares no property", "%s must not fall through to the generic refusal: %s", c.property, res.ForLLM)
	}
	require.Contains(t, a4Read(t, root, "P.md"), "id: PRJ-0001\n", "the identity must be untouched")
}

// D-05 through the tool: the line_range weld, end to end.
func TestUAT_D05_ReplaceBodyLineRange_ThroughTheTool(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "L.md", "---\nt: 1\n---\nAAA1\nBBB2\nCCC3\nDDD4\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "replace_body", "path": "L.md",
		"line_range": map[string]any{"start": float64(5), "end": float64(6)},
		"body":       "XXX\nYYY", "expect_version": a4Version(t, root, "L.md"),
	})
	require.False(t, res.IsError, res.ForLLM)
	require.Equal(t, "---\nt: 1\n---\nAAA1\nXXX\nYYY\nDDD4\n", a4Read(t, root, "L.md"))
	require.NotContains(t, strings.ToLower(res.ForLLM), "yyyddd4")
}

// ---------------------------------------------------------------------------
// D-12 / D-54 — every embed is its own paragraph; 'section' is optional
// ---------------------------------------------------------------------------

func TestUAT_D12_ConsecutiveEmbedsIntoASectionAreSeparateParagraphs(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	vePicture(t, root, "Assets/a.png")
	vePicture(t, root, "Assets/b.png")
	a4Note(t, root, "Dash.md", "# Dash\n\n## Embeds\n\nIntro line.\n\n## Next\n\nAfter.\n")

	for _, tgt := range []string{"Assets/a.png", "Assets/b.png"} {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "embed", "path": "Dash.md",
			"target": tgt, "section": "Embeds", "expect_version": a4Version(t, root, "Dash.md"),
		})
		require.False(t, res.IsError, res.ForLLM)
	}
	got := a4Read(t, root, "Dash.md")
	require.Equal(t,
		"# Dash\n\n## Embeds\n\nIntro line.\n\n![[Assets/a.png]]\n\n![[Assets/b.png]]\n\n## Next\n\nAfter.\n",
		got)

	// An EMPTY section gets one blank line after the heading, not two.
	a4Note(t, root, "Empty.md", "## Embeds\n")
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Empty.md",
		"target": "Assets/a.png", "section": "Embeds", "expect_version": a4Version(t, root, "Empty.md"),
	})
	require.False(t, res.IsError, res.ForLLM)
	require.Equal(t, "## Embeds\n\n![[Assets/a.png]]\n", a4Read(t, root, "Empty.md"))
}

func TestUAT_D54_EmbedWithoutSectionAppendsAsOwnParagraph(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	vePicture(t, root, "Assets/photo.png")
	a4Note(t, root, "Plain.md", "Just prose.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Plain.md",
		"target": "Assets/photo.png", "expect_version": a4Version(t, root, "Plain.md"),
	})
	require.False(t, res.IsError, "'section' must be optional: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "at the end of the note")
	require.Equal(t, "Just prose.\n\n![[Assets/photo.png]]\n", a4Read(t, root, "Plain.md"))
}

// ---------------------------------------------------------------------------
// D-45 (#697) — PDF page fragment; D-91 — write-time render note
// ---------------------------------------------------------------------------

func TestUAT_D45_EmbedPdfPageFragment(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	vePicture(t, root, "Assets/multi.pdf") // bytes are never inspected; extension-only
	a4Note(t, root, "D.md", "Intro.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "D.md",
		"target": "Assets/multi.pdf", "page": "2", "section": "Docs", "expect_version": a4Version(t, root, "D.md"),
	})
	require.False(t, res.IsError, res.ForLLM)
	require.Contains(t, a4Read(t, root, "D.md"), "![[Assets/multi.pdf#page=2]]")
	require.NotContains(t, res.ForLLM, "RENDERS AS A LINK", "a paged PDF mounts: %s", res.ForLLM)

	// D-91: a whole-document PDF is accepted but the reply says it will be a link.
	res2 := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "D.md",
		"target": "Assets/multi.pdf", "section": "Docs", "expect_version": a4Version(t, root, "D.md"),
	})
	require.False(t, res2.IsError, res2.ForLLM)
	require.Contains(t, res2.ForLLM, "RENDERS AS A LINK: a whole-document PDF")

	// The page fragment may also ride on the target itself (D-19's routing).
	res3 := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "D.md",
		"target": "Assets/multi.pdf#page=3", "section": "Docs", "expect_version": a4Version(t, root, "D.md"),
	})
	require.False(t, res3.IsError, res3.ForLLM)
	require.Contains(t, a4Read(t, root, "D.md"), "![[Assets/multi.pdf#page=3]]")

	bad := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "D.md",
		"target": "Assets/multi.pdf", "page": "two", "section": "Docs", "expect_version": a4Version(t, root, "D.md"),
	})
	require.True(t, bad.IsError)
	require.Contains(t, bad.ForLLM, "page number")
}

// D-20 — a Mermaid file is refused at write time, as ADR-083 §15 says.
func TestUAT_D20_EmbedRefusesMermaidFile(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	vePicture(t, root, "Assets/chart.mmd")
	a4Note(t, root, "D.md", "Intro.\n")
	before := a4Read(t, root, "D.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "D.md",
		"target": "Assets/chart.mmd", "section": "Diagrams", "expect_version": a4Version(t, root, "D.md"),
	})
	require.True(t, res.IsError, "an .mmd embed must be refused: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "Mermaid")
	require.Contains(t, res.ForLLM, "link")
	require.Equal(t, before, a4Read(t, root, "D.md"), "a refusal must leave the note byte-identical")
}

// D-19 — "Note.md#Heading" resolves the heading instead of reporting the note missing.
func TestUAT_D19_EmbedTargetWithHeadingFragment(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Meetings/Review.md", "# Review\n\n## Decisions\n\n- ship it\n")
	a4Note(t, root, "D.md", "Intro.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "D.md",
		"target": "Meetings/Review.md#Decisions", "section": "Embeds", "expect_version": a4Version(t, root, "D.md"),
	})
	require.False(t, res.IsError, res.ForLLM)
	require.Contains(t, a4Read(t, root, "D.md"), "![[Meetings/Review.md#Decisions]]")

	// A heading that really is missing is reported as a missing HEADING,
	// naming the ones that exist — never as a missing note.
	res2 := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "D.md",
		"target": "Meetings/Review.md#Actions", "section": "Embeds", "expect_version": a4Version(t, root, "D.md"),
	})
	require.True(t, res2.IsError)
	require.NotContains(t, res2.ForLLM, "does not exist in this knowledge base")
	require.Contains(t, res2.ForLLM, "## Decisions")
}

// ---------------------------------------------------------------------------
// D-26 template resolution, D-95 trailing newline, D-48 minted id in the reply,
// D-52 no id burned by a refused create
// ---------------------------------------------------------------------------

func TestUAT_D26_TemplateNameResolvesWithoutExtensionAndMissNamesAvailable(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	tmplDir := TemplatesPath(root, Marker{})
	require.NoError(t, os.MkdirAll(tmplDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(tmplDir, "meeting.md"), []byte("# {{title}}\n\nAgenda.\n"), 0o600))

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "M.md", "template": "meeting", "title": "Kickoff",
	})
	require.False(t, res.IsError, "\"meeting\" must resolve to meeting.md: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, `from template "meeting.md"`)
	require.Contains(t, a4Read(t, root, "M.md"), "# Kickoff\n")

	miss := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "N.md", "template": "standup",
	})
	require.True(t, miss.IsError)
	require.Contains(t, miss.ForLLM, "available templates: meeting.md")
}

func TestUAT_D95_D48_CreateEndsWithNewlineAndReportsMintedID(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	uatProjectSchema(t, root)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "P.md",
		"frontmatter": map[string]any{"type": "project", "status": "planned"},
		"body":        "Last line without newline",
	})
	require.False(t, res.IsError, res.ForLLM)
	got := a4Read(t, root, "P.md")
	require.True(t, strings.HasSuffix(got, "Last line without newline\n"), "D-95: %q", got)
	require.Contains(t, got, "id: PRJ-0001\n")
	require.Contains(t, res.ForLLM, "CREATED (")
	require.Contains(t, res.ForLLM, ") id PRJ-0001", "D-48: the minted id must be in the reply: %s", res.ForLLM)
}

func TestUAT_D52_RefusedDuplicateCreateBurnsNoID(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	uatProjectSchema(t, root)
	create := func(path string) *string {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "create", "path": path,
			"frontmatter": map[string]any{"type": "project"}, "body": "x\n",
		})
		if res.IsError {
			return nil
		}
		out := res.ForLLM
		return &out
	}
	require.NotNil(t, create("A.md"))
	require.Nil(t, create("A.md"), "the duplicate must be refused")
	second := create("B.md")
	require.NotNil(t, second)
	require.Contains(t, *second, "id PRJ-0002", "the refused create must not have consumed PRJ-0002: %s", *second)
}

// ---------------------------------------------------------------------------
// D-28 promote mints an id; D-93 stale keys are named; value:null removes
// ---------------------------------------------------------------------------

func TestUAT_D28_SettingTypeOnAPlainNoteMintsAnID(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	uatProjectSchema(t, root)
	a4Note(t, root, "Plain.md", "---\ntitle: Something\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Plain.md",
		"property": "type", "value": "project", "expect_version": a4Version(t, root, "Plain.md"),
	})
	require.False(t, res.IsError, res.ForLLM)
	got := a4Read(t, root, "Plain.md")
	require.Contains(t, got, "type: project\n")
	require.Contains(t, got, "id: PRJ-0001\n", "a promoted note must get an id: %s", got)
	require.Contains(t, res.ForLLM, "PROMOTED to a record: id PRJ-0001 minted")

	// Setting the type again on a note that already has an id mints nothing.
	res2 := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Plain.md",
		"property": "type", "value": "project", "expect_version": a4Version(t, root, "Plain.md"),
	})
	require.False(t, res2.IsError, res2.ForLLM)
	require.NotContains(t, res2.ForLLM, "PROMOTED")
	require.Equal(t, 1, strings.Count(a4Read(t, root, "Plain.md"), "id: "))
}

func TestUAT_D93_StaleUndeclaredKeyIsNamedAndRemovable(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	uatProjectSchema(t, root)
	// `priority` is the pre-rename key the type no longer declares.
	a4Note(t, root, "P.md", "---\ntype: project\nid: PRJ-0001\npriority: low\nstatus: planned\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "P.md",
		"property": "status", "value": "active", "expect_version": a4Version(t, root, "P.md"),
	})
	require.False(t, res.IsError, res.ForLLM)
	require.Contains(t, res.ForLLM, "NOTE: this note also carries 1 key(s) its record type does not declare — priority —", res.ForLLM)

	rm := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "P.md",
		"property": "priority", "value": nil, "expect_version": a4Version(t, root, "P.md"),
	})
	require.False(t, rm.IsError, rm.ForLLM)
	require.Contains(t, rm.ForLLM, "REMOVED priority (changed)")
	got := a4Read(t, root, "P.md")
	require.NotContains(t, got, "priority")
	require.Contains(t, got, "status: active\n")

	// The identity is never removable this way (D-51's classification).
	rmID := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "P.md",
		"property": "id", "value": nil, "expect_version": a4Version(t, root, "P.md"),
	})
	require.True(t, rmID.IsError)
	require.Contains(t, rmID.ForLLM, "identity")
}

// D-50 — a value the note already holds is reported unchanged, token untouched.
func TestUAT_D50_SameValueWriteIsUnchanged(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	uatProjectSchema(t, root)
	a4Note(t, root, "P.md", "---\ntype: project\nid: PRJ-0001\nstatus: done\n---\nBody.\n")
	before := a4Version(t, root, "P.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "P.md",
		"property": "status", "value": "Done", "expect_version": before,
	})
	require.False(t, res.IsError, res.ForLLM)
	require.Contains(t, res.ForLLM, "SET status (unchanged — already so)", res.ForLLM)
	require.Equal(t, before, a4Version(t, root, "P.md"))
}

// D-18 — an anchor replaces only itself, and the reply says so.
func TestUAT_D18_AnchorReplaceReportsWhatWasKept(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "B.md", "# Plan\n\nBudget: 480000 approved for FY26. Spend is tracked.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "replace_body", "path": "B.md",
		"anchor": "Budget:", "body": "Budget: 500000", "expect_version": a4Version(t, root, "B.md"),
	})
	require.False(t, res.IsError, res.ForLLM)
	require.Contains(t, res.ForLLM, "replaced only the 7-byte anchor on line 3")
	require.Contains(t, res.ForLLM, `kept on that line after it: " 480000 approved for FY26. Spend is tracked."`)
	require.Contains(t, res.ForLLM, "make the anchor the whole line or use line_range")
}
