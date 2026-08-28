// Omnipus — tests for vault_read (spec §4.1.3, AC-R1..AC-R3, FR-072, FR-072a,
// FR-074, US-9).
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// Fixture — a vault with one record type and a handful of notes
// ---------------------------------------------------------------------------

// readFixtureVault builds a real collection on disk: a schema, a record note
// with one schema-violating property, an ordinary note, a note with headings,
// and a wikilink between two of them (for links/backlinks).
//
// R-F: every record-type, property and value name below is a fixture THIS
// TEST declares. The product ships none of them.
func readFixtureVault(t *testing.T, root string) {
	t.Helper()
	write := func(rel, body string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o600))
	}

	write(".omnipus-vault/records/deal.yaml", `
schema_version: 1
type: deal
properties:
  status: { type: enum, values: [prospect, won, lost] }
  amount: { type: decimal }
  segment: { type: text }
`)

	write("Deals/Acme.md", "---\n"+
		"type: deal\n"+
		"status: prospect\n"+
		"amount: 349.98\n"+
		"segment: [vendor, customer]\n"+ // arity violation: segment is scalar
		"---\n"+
		"## Summary\n"+
		"A short summary mentioning [[Contacts/Jane Smith]].\n"+
		"\n"+
		"## Pricing\n"+
		"Tier one pricing.\n"+
		"\n"+
		"### Tier Detail\n"+
		"Nested under Pricing.\n"+
		"\n"+
		"## Notes\n"+
		"Trailing notes.\n")

	write("Contacts/Jane Smith.md", "---\ntype: person\n---\nSee [[Deals/Acme]] for context.\n")

	write("Notes/Ordinary.md", "No frontmatter here at all, just prose.\n")
}

// readCtxAndDeps mounts root into a fresh workspace and returns a tool
// context plus ToolDeps a ReadTool can Execute against — the full scope-
// resolution path, not a shortcut around it.
func readCtxAndDeps(t *testing.T, root string) (context.Context, ToolDeps) {
	t.Helper()
	home := b5Home(t)
	wsID := b5Workspace(t, home)
	vaultRoot := b5Vault(t, root, "workbench")
	b5Mount(t, home, wsID, "workbench", vaultRoot)
	deps := ToolDeps{Home: home, RateLimiter: NewRetrievalRateLimiter(RetrievalRateLimitConfig{})}
	return b5Ctx("agent-1", wsID), deps
}

// ---------------------------------------------------------------------------
// EXIT PROOF — a real rendered read of a note with properties, a body, links
// and backlinks, and its byte count.
// ---------------------------------------------------------------------------

func TestReadTool_RealRenderedReadWithByteCount(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)

	tool := NewReadTool(deps)
	res := tool.Execute(ctx, map[string]any{"path": "Deals/Acme.md"})
	require.NotNil(t, res)
	require.False(t, res.IsError, "unexpected error: %s", resultText(res))

	out := resultText(res)
	t.Logf("rendered vault_read response, %d bytes:\n%s", len(out), out)

	require.Greater(t, len(out), 0, "a real read must not render to nothing")
	assert.Contains(t, out, "Deals/Acme.md — version v1:", "the version token must be present and use the v1 encoding")
	assert.Contains(t, out, "TYPE: deal")
	assert.Contains(t, out, "status", "declared properties must render")
	assert.Contains(t, out, "prospect")
	assert.Contains(t, out, "amount")
	assert.Contains(t, out, "349.98")
	assert.Contains(t, out, "INVALID:", "the arity violation on segment must be flagged in place")
	assert.Contains(t, out, "BODY (")
	assert.Contains(t, out, "A short summary")
	assert.Contains(t, out, "## Pricing", "body must carry the note's own markdown, unrewritten")
	assert.Contains(t, out, "LINKS (")
	assert.Contains(t, out, "Contacts/Jane Smith.md", "the outbound wikilink must resolve and appear inline")
	assert.Contains(t, out, "BACKLINKS (")
	assert.Contains(t, out, "Contacts/Jane Smith.md", "the inbound wikilink from Jane Smith's note must appear as a backlink")
}

// ---------------------------------------------------------------------------
// FR-072 — never JSON
// ---------------------------------------------------------------------------

func TestReadTool_ResponseIsNeverAJSONDocument(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)

	res := NewReadTool(deps).Execute(ctx, map[string]any{"path": "Deals/Acme.md"})
	require.False(t, res.IsError)
	out := resultText(res)

	var probe any
	require.Error(t, json.Unmarshal([]byte(out), &probe),
		"the envelope must not parse as a whole JSON document (FR-072 D22.1)")
	require.False(t, strings.HasPrefix(strings.TrimSpace(out), "{"),
		"the response must not open like a JSON object")
}

// ---------------------------------------------------------------------------
// FR-074 / AC-R1 / AC-R2 / SC-019 — the version token IS the write token,
// obtained with zero failed writes.
// ---------------------------------------------------------------------------

func TestReadTool_ReadThenEditWithZeroFailedWrites(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)

	res := NewReadTool(deps).Execute(ctx, map[string]any{"path": "Notes/Ordinary.md"})
	require.False(t, res.IsError)
	token := extractVersionToken(t, resultText(res))
	require.NotEmpty(t, token)

	c, err := OpenCollection(root)
	require.NoError(t, err)

	// This is the ENTIRE point: the token vault_read handed back is consumed
	// directly by the real write path, and the write SUCCEEDS on the first
	// try — no prior failing write was needed to obtain it (AC-R2), and this
	// is the only write in the test (SC-019: zero failed writes in between).
	writeRes, werr := EditNote(OSLinkFS(), c, EditNoteRequest{
		RelPath:       "Notes/Ordinary.md",
		Edits:         []NoteEdit{AppendSection("Follow-up", "Added via the token vault_read returned.")},
		ExpectVersion: token,
	})
	require.NoError(t, werr, "the version token vault_read returns must be accepted by vault_edit's write path unchanged (AC-R1)")
	assert.True(t, writeRes.Changed)
}

func TestReadTool_VersionTokenChangesOnEditAndNotOnNoopReread(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)

	tool := NewReadTool(deps)

	res1 := tool.Execute(ctx, map[string]any{"path": "Notes/Ordinary.md"})
	require.False(t, res1.IsError)
	tokenA := extractVersionToken(t, resultText(res1))

	// A no-op re-read: nothing on disk changed, so the token must be
	// byte-identical. This is the negative control — without it, a token
	// that changed on EVERY read (e.g. one seeded from time.Now()) would
	// pass the "changes on edit" half below for the wrong reason.
	res2 := tool.Execute(ctx, map[string]any{"path": "Notes/Ordinary.md"})
	require.False(t, res2.IsError)
	tokenB := extractVersionToken(t, resultText(res2))
	assert.Equal(t, tokenA, tokenB, "re-reading unchanged content must yield the identical token")

	// Now actually change the file, through the real write path, and prove
	// the token DOES move.
	c, err := OpenCollection(root)
	require.NoError(t, err)
	_, werr := EditNote(OSLinkFS(), c, EditNoteRequest{
		RelPath:       "Notes/Ordinary.md",
		Edits:         []NoteEdit{AppendSection("Changed", "Now the bytes differ.")},
		ExpectVersion: tokenB,
	})
	require.NoError(t, werr)

	res3 := tool.Execute(ctx, map[string]any{"path": "Notes/Ordinary.md"})
	require.False(t, res3.IsError)
	tokenC := extractVersionToken(t, resultText(res3))
	assert.NotEqual(t, tokenB, tokenC, "editing the note must change the token an immediately following read returns")
}

// TestReadTool_VersionTokenIsIndependentOfPropindexSourceHash proves the
// design decision the file header documents: the token vault_read returns is
// pkg/knowledge's own ComputeVersionToken, not propindex's SourceHash — the
// two encodings differ even over identical bytes, so a caller cannot confuse
// one for the other by construction.
func TestReadTool_VersionTokenIsIndependentOfPropindexSourceHash(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)

	res := NewReadTool(deps).Execute(ctx, map[string]any{"path": "Notes/Ordinary.md"})
	require.False(t, res.IsError)
	token := extractVersionToken(t, resultText(res))

	content, rerr := os.ReadFile(filepath.Join(root, "Notes", "Ordinary.md"))
	require.NoError(t, rerr)
	want := string(ComputeVersionToken(content))
	assert.Equal(t, want, token, "vault_read's token must be knowledge.ComputeVersionToken over the exact bytes returned")
	assert.NotEqual(t, want[len("v1:"):], token, "sanity: the token must keep its v1: prefix, not just the digest")
	assert.True(t, strings.HasPrefix(token, "v1:"), "the token must carry the ADR-067 D14 v1 encoding")
}

func extractVersionToken(t *testing.T, rendered string) string {
	t.Helper()
	first, _, _ := strings.Cut(rendered, "\n")
	_, token, found := strings.Cut(first, " — version ")
	require.True(t, found, "header line must carry ' — version <token>': %q", first)
	return strings.TrimSpace(token)
}

// ---------------------------------------------------------------------------
// US-9.2 — a section read still carries the WHOLE-FILE token
// ---------------------------------------------------------------------------

func TestReadTool_SectionReadTokenCoversWholeFile(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)

	tool := NewReadTool(deps)
	full := tool.Execute(ctx, map[string]any{"path": "Deals/Acme.md"})
	require.False(t, full.IsError)
	fullToken := extractVersionToken(t, resultText(full))

	sectioned := tool.Execute(ctx, map[string]any{"path": "Deals/Acme.md", "section": "Pricing"})
	require.False(t, sectioned.IsError)
	sectionToken := extractVersionToken(t, resultText(sectioned))

	assert.Equal(t, fullToken, sectionToken, "a section read must carry the SAME token as reading the whole note")

	out := resultText(sectioned)
	assert.Contains(t, out, `SECTION "Pricing"`)
	assert.Contains(t, out, "Tier one pricing.")
	assert.Contains(t, out, "Nested under Pricing.", "a section includes its own subsections")
	assert.NotContains(t, out, "A short summary", "a section read must not leak sibling sections")
	assert.NotContains(t, out, "Trailing notes.", "a section read must not leak sibling sections")
}

// ---------------------------------------------------------------------------
// §4.1.3 refusal — unknown section names the headings present
// ---------------------------------------------------------------------------

func TestReadTool_UnknownSectionRefusalListsHeadings(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)

	res := NewReadTool(deps).Execute(ctx, map[string]any{"path": "Deals/Acme.md", "section": "Nonexistent"})
	require.NotNil(t, res)
	require.True(t, res.IsError)
	msg := resultText(res)
	assert.Contains(t, msg, "no section 'Nonexistent' in Deals/Acme.md")
	assert.Contains(t, msg, "## Summary")
	assert.Contains(t, msg, "## Pricing")
	assert.Contains(t, msg, "### Tier Detail")
	assert.Contains(t, msg, "## Notes")
}

// TestReadTool_SectionEchoedFromRefusalIsAccepted proves the refusal's own
// wording is itself a valid next call: an agent that copies "## Pricing"
// verbatim out of the refusal must not be refused a second time.
func TestReadTool_SectionEchoedFromRefusalIsAccepted(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)

	tool := NewReadTool(deps)
	refusal := tool.Execute(ctx, map[string]any{"path": "Deals/Acme.md", "section": "nope"})
	require.True(t, refusal.IsError)
	msg := resultText(refusal)
	start := strings.Index(msg, "## Summary")
	require.GreaterOrEqual(t, start, 0)
	echoed := "## Summary"

	res := tool.Execute(ctx, map[string]any{"path": "Deals/Acme.md", "section": echoed})
	require.False(t, res.IsError, "the exact heading text the refusal listed must be accepted: %s", resultText(res))
}

// ---------------------------------------------------------------------------
// AC-R3 — a schema violation is flagged, never a refusal
// ---------------------------------------------------------------------------

func TestReadTool_SchemaViolationStillReadsAndIsFlaggedInPlace(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)

	res := NewReadTool(deps).Execute(ctx, map[string]any{"path": "Deals/Acme.md", "include": []any{"frontmatter"}})
	require.False(t, res.IsError, "a schema violation must never block the read")
	out := resultText(res)
	assert.Contains(t, out, "segment")
	assert.Contains(t, out, "INVALID:")
	assert.Contains(t, out, "list of 2", "the specific fault (arity) must be named, not just 'invalid'")
}

// ---------------------------------------------------------------------------
// FR-072a — max_bytes bounds the body only, truncation is always stated
// ---------------------------------------------------------------------------

func TestReadTool_MaxBytesTruncatesBodyAndStatesIt(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)

	res := NewReadTool(deps).Execute(ctx, map[string]any{"path": "Deals/Acme.md", "max_bytes": 10})
	require.False(t, res.IsError)
	out := resultText(res)
	assert.Contains(t, out, "TRUNCATED")
	assert.Contains(t, out, "Deals/Acme.md — version v1:", "the version token is OUTSIDE max_bytes and must still be full-length")
	assert.Contains(t, out, "TYPE: deal", "frontmatter is OUTSIDE max_bytes and must still be complete")
	assert.Contains(t, out, "status", "declared properties must still be fully present under a tiny max_bytes")
}

func TestReadTool_UntruncatedBodyDoesNotClaimTruncation(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)

	res := NewReadTool(deps).Execute(ctx, map[string]any{"path": "Notes/Ordinary.md"})
	require.False(t, res.IsError)
	assert.NotContains(t, resultText(res), "TRUNCATED")
}

// ---------------------------------------------------------------------------
// FR-005 — an ordinary note (no schema, no frontmatter) still reads cleanly
// ---------------------------------------------------------------------------

func TestReadTool_OrdinaryNoteHasNoTypeLineOrFrontmatterBlock(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)

	res := NewReadTool(deps).Execute(ctx, map[string]any{"path": "Notes/Ordinary.md"})
	require.False(t, res.IsError)
	out := resultText(res)
	assert.NotContains(t, out, "TYPE:")
	assert.Contains(t, out, "FRONTMATTER: none")
	assert.Contains(t, out, "No frontmatter here at all")
}

// ---------------------------------------------------------------------------
// `include` trims the response
// ---------------------------------------------------------------------------

func TestReadTool_IncludeTrimsSections(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)

	res := NewReadTool(deps).Execute(ctx, map[string]any{
		"path":    "Deals/Acme.md",
		"include": []any{"body"},
	})
	require.False(t, res.IsError)
	out := resultText(res)
	assert.NotContains(t, out, "FRONTMATTER")
	assert.NotContains(t, out, "LINKS")
	assert.NotContains(t, out, "BACKLINKS")
	assert.Contains(t, out, "BODY (")
}

func TestReadTool_UnknownIncludeMemberIsRefused(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)

	res := NewReadTool(deps).Execute(ctx, map[string]any{
		"path": "Deals/Acme.md", "include": []any{"headings"},
	})
	require.True(t, res.IsError)
	msg := resultText(res)
	for _, want := range append([]string{"headings"}, ReadIncludeOrder...) {
		assert.Contains(t, msg, want)
	}
}

// ---------------------------------------------------------------------------
// Argument / scope refusals
// ---------------------------------------------------------------------------

func TestReadTool_UnknownArgumentIsRefusedWithTheAcceptedOnesListed(t *testing.T) {
	res := NewReadTool(ToolDeps{Home: t.TempDir()}).Execute(context.Background(), map[string]any{
		"path": "x.md", "sectionn": "typo",
	})
	require.True(t, res.IsError)
	msg := resultText(res)
	for _, want := range readArgNames {
		assert.Contains(t, msg, want)
	}
}

func TestReadTool_MissingPathIsRefused(t *testing.T) {
	res := NewReadTool(ToolDeps{Home: t.TempDir()}).Execute(context.Background(), map[string]any{})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "'path' is required")
}

func TestReadTool_UnknownCollectionIsRefusedNamingTheOnesInScope(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)

	res := NewReadTool(deps).Execute(ctx, map[string]any{"path": "Deals/Acme.md", "collection": "nope"})
	require.True(t, res.IsError)
	msg := resultText(res)
	assert.Contains(t, msg, "nope")
	assert.Contains(t, msg, "in scope")
}

func TestReadTool_MissingNoteIsRefused(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)

	res := NewReadTool(deps).Execute(ctx, map[string]any{"path": "Deals/Ghost.md"})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "no note at Deals/Ghost.md")
}

func TestReadTool_PathOutsideCollectionIsRefused(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)

	res := NewReadTool(deps).Execute(ctx, map[string]any{"path": "../../etc/passwd"})
	require.True(t, res.IsError)
}

// TestReadTool_SymlinkedPathIsRefusedNotFollowed is the vault_read half of
// TestSearchTool_SymlinkedHitIsRefusedNotFollowed's guarantee: a path that IS
// itself a symlink, named directly, is refused rather than opened.
func TestReadTool_SymlinkedPathIsRefusedNotFollowed(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip("symlinks are not universally available on the CI Windows image")
	}
	root := t.TempDir()
	readFixtureVault(t, root)

	outside := t.TempDir()
	secretPath := filepath.Join(outside, "secret.md")
	require.NoError(t, os.WriteFile(secretPath, []byte("BEGIN RSA PRIVATE KEY outside\n"), 0o600))
	linkPath := filepath.Join(root, "Deals", "Escape.md")
	if err := os.Symlink(secretPath, linkPath); err != nil {
		t.Skipf("symlinks unavailable on this platform/filesystem: %v", err)
	}

	ctx, deps := readCtxAndDeps(t, root)
	res := NewReadTool(deps).Execute(ctx, map[string]any{"path": "Deals/Escape.md"})
	require.NotNil(t, res)
	require.True(t, res.IsError, "a symlinked path must be refused, never followed")
	msg := resultText(res)
	assert.NotContains(t, msg, "BEGIN RSA PRIVATE KEY", "the target's bytes must never reach the response")
}

func TestReadTool_RateLimited(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)
	deps.RateLimiter = NewRetrievalRateLimiter(RetrievalRateLimitConfig{PerAgentLimit: 1})

	tool := NewReadTool(deps)
	first := tool.Execute(ctx, map[string]any{"path": "Deals/Acme.md"})
	require.False(t, first.IsError)
	second := tool.Execute(ctx, map[string]any{"path": "Deals/Acme.md"})
	require.True(t, second.IsError)
	assert.Contains(t, resultText(second), "rate limited")
}

// ---------------------------------------------------------------------------
// Unit-level: pure rendering and section logic, no filesystem
// ---------------------------------------------------------------------------

func TestFindHeadingSpan_IncludesSubsectionsExcludesSiblings(t *testing.T) {
	content := []byte("prefix\n## A\nbody a\n### A1\nnested\n## B\nbody b\n")
	headings := ExtractHeadings(content)
	require.Len(t, headings, 3)

	start, end, ok := findHeadingSpan(content, headings, "A")
	require.True(t, ok)
	got := string(content[start:end])
	assert.Contains(t, got, "body a")
	assert.Contains(t, got, "nested", "a subsection must be included")
	assert.NotContains(t, got, "body b", "a sibling section must be excluded")
}

func TestFindHeadingSpan_LastSectionRunsToEOF(t *testing.T) {
	content := []byte("## Only\nlast section body\n")
	headings := ExtractHeadings(content)
	start, end, ok := findHeadingSpan(content, headings, "Only")
	require.True(t, ok)
	assert.Equal(t, len(content), end)
	assert.Contains(t, string(content[start:end]), "last section body")
}

func TestFindHeadingSpan_UnknownReturnsNotOK(t *testing.T) {
	content := []byte("## A\nx\n")
	headings := ExtractHeadings(content)
	_, _, ok := findHeadingSpan(content, headings, "Z")
	assert.False(t, ok)
}

func TestMatchSectionQuery_StripsMarkersAndWhitespace(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Pricing", "Pricing"},
		{"## Pricing", "Pricing"},
		{"  ## Pricing  ", "Pricing"},
		{"#Pricing", "Pricing"},
	} {
		assert.Equal(t, tc.want, matchSectionQuery(tc.in), "input %q", tc.in)
	}
}

func TestReadSectionRefusalText_MatchesSpecWording(t *testing.T) {
	headings := []Heading{{Level: 2, Text: "Summary"}, {Level: 2, Text: "Terms"}, {Level: 2, Text: "Notes"}}
	got := readSectionRefusalText("Deals/Acme.md", "## Pricing", headings)
	assert.Equal(t, "no section '## Pricing' in Deals/Acme.md; headings: ## Summary, ## Terms, ## Notes", got)
}

func TestReadSectionRefusalText_NoHeadingsAtAll(t *testing.T) {
	got := readSectionRefusalText("Notes/Plain.md", "Pricing", nil)
	assert.Equal(t, "no section 'Pricing' in Notes/Plain.md; this note has no headings", got)
}

func TestTruncateUTF8_NeverSplitsARune(t *testing.T) {
	s := "hello 世界" // "世" and "界" are 3 bytes each in UTF-8
	for n := 0; n <= len(s)+2; n++ {
		got := truncateUTF8(s, n)
		assert.True(t, isValidUTF8(got), "n=%d produced invalid UTF-8: %q", n, got)
		assert.LessOrEqual(t, len(got), n)
	}
	assert.Equal(t, s, truncateUTF8(s, len(s)))
	assert.Equal(t, s, truncateUTF8(s, len(s)+50))
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			// Only acceptable if the source string itself contained the
			// replacement character, which none of this test's fixtures do.
			return false
		}
	}
	return true
}

func TestRenderRead_UnresolvedLinkNamesTheReason(t *testing.T) {
	d := ReadData{
		Path:     "A.md",
		Version:  "v1:deadbeef",
		Included: map[string]bool{ReadIncludeLinks: true},
		Links: []ReadLink{
			{Form: "[[Nowhere]]", Resolved: false, Reason: string(ReasonNoMatch), Line: 5},
		},
	}
	out := RenderRead(d)
	assert.Contains(t, out, "(unresolved) [[Nowhere]]")
	assert.Contains(t, out, string(ReasonNoMatch))
	assert.Contains(t, out, "(line 5)")
}

func TestRenderRead_ZeroLinksSaysNoneRatherThanNothing(t *testing.T) {
	d := ReadData{
		Path:     "A.md",
		Version:  "v1:deadbeef",
		Included: map[string]bool{ReadIncludeLinks: true, ReadIncludeBacklinks: true},
	}
	out := RenderRead(d)
	assert.Contains(t, out, "LINKS (0) — none")
	assert.Contains(t, out, "BACKLINKS (0) — none")
}

func TestRenderRead_UnrecognisedDeclaredTypeStillLabelled(t *testing.T) {
	d := ReadData{
		Path:     "A.md",
		Version:  "v1:deadbeef",
		TypeName: "ghost_type",
		Included: map[string]bool{},
	}
	out := RenderRead(d)
	assert.Contains(t, out, "TYPE: ghost_type (not a declared record type")
}

// ---------------------------------------------------------------------------
// Unit-level: property projection against a hand-built schema, no filesystem
// ---------------------------------------------------------------------------

func TestProjectReadProperties_DeclaredValueRendersTyped(t *testing.T) {
	sc, rej := records.ParseSchema("deal.yaml", []byte(`
schema_version: 1
type: deal
properties:
  status: { type: enum, values: [prospect, won] }
`))
	require.Nil(t, rej)

	fm, ferr := records.ParseFrontmatter([]byte("---\ntype: deal\nstatus: prospect\n---\nbody\n"))
	require.NoError(t, ferr)
	rec := records.Record{Path: "x.md", Frontmatter: fm}

	props := projectReadProperties(rec, fm, sc)
	require.Len(t, props, 2) // type, status
	var status ReadProperty
	for _, p := range props {
		if p.Key == "status" {
			status = p
		}
	}
	assert.True(t, status.Declared)
	assert.Equal(t, "prospect", status.Value)
	assert.Empty(t, status.Findings)
}

func TestProjectReadProperties_UndeclaredKeyRendersRawUntyped(t *testing.T) {
	sc, rej := records.ParseSchema("deal.yaml", []byte(`
schema_version: 1
type: deal
properties:
  status: { type: enum, values: [prospect, won] }
`))
	require.Nil(t, rej)

	fm, ferr := records.ParseFrontmatter([]byte("---\ntype: deal\nstatus: prospect\ntags: [alpha, beta]\n---\nbody\n"))
	require.NoError(t, ferr)
	rec := records.Record{Path: "x.md", Frontmatter: fm}

	props := projectReadProperties(rec, fm, sc)
	var tags ReadProperty
	for _, p := range props {
		if p.Key == "tags" {
			tags = p
		}
	}
	assert.False(t, tags.Declared, "tags is not in the schema and must render raw")
	assert.Equal(t, "alpha, beta", tags.Value)
}

func TestProjectReadProperties_NoSchemaEveryKeyIsRaw(t *testing.T) {
	fm, ferr := records.ParseFrontmatter([]byte("---\nfoo: bar\nnums: [1, 2]\n---\nbody\n"))
	require.NoError(t, ferr)
	rec := records.Record{Path: "x.md", Frontmatter: fm}

	props := projectReadProperties(rec, fm, nil)
	require.Len(t, props, 2)
	for _, p := range props {
		assert.False(t, p.Declared)
	}
}

func TestRenderRawNode_MappingNamesItsKeysRatherThanVanishing(t *testing.T) {
	n := records.Node{Kind: records.KindMapping, Keys: []string{"a", "b"}}
	got := renderRawNode(n)
	assert.Contains(t, got, "a")
	assert.Contains(t, got, "b")
}

// ---------------------------------------------------------------------------
// Unterminated frontmatter fence — the one place fmParse and
// records.ParseFrontmatter provably disagree; vault_read must follow the
// index's own reading (fields.go), not records.ParseFrontmatter's.
// ---------------------------------------------------------------------------

func TestReadTool_UnterminatedFrontmatterFenceStillReadsAsBody(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	write := func(rel, body string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o600))
	}
	// Opens with "---" and never closes it — DS-3's "frontmatter is the
	// entire file" case, except here it is prose, not YAML.
	write("Notes/Unterminated.md", "---\nThis never closes with another fence.\nMore prose.\n")

	ctx, deps := readCtxAndDeps(t, root)
	res := NewReadTool(deps).Execute(ctx, map[string]any{"path": "Notes/Unterminated.md"})
	require.False(t, res.IsError, "an unparseable frontmatter fence must never block the read (FR-005 posture)")
	out := resultText(res)
	assert.Contains(t, out, "could not be parsed")
	assert.Contains(t, out, "This never closes with another fence.", "the whole note must still be shown as body")
}
