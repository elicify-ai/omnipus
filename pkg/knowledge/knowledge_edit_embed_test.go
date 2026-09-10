// Omnipus — tests for knowledge_edit's op="embed" (US-11, EMB-095..EMB-102,
// adr-083-embedded-content-spec.md's "Step 1 — resolver, renderers, PDF
// pool, agent authoring"): an agent supplies a path plus NAMED modifiers
// (view, target_heading, target_block, width) and the tool writes the
// embed notation for it, after checking the target exists — and, for a
// fragment modifier, that the named view or heading exists too — rather
// than writing a line a reader would find broken later.
//
// CRITICAL, per this work's own spec review: op="embed" did not exist
// before this change, so EVERY refusal test below is paired with a
// SUCCEEDING op="embed" call in the same test body. Before this change, ANY
// op="embed" call hit EditTool.Execute's default branch (refuseOp,
// "unsupported op %q"), which is indistinguishable from a targeted refusal
// unless the test also proves the op exists by succeeding somewhere in its
// own fixture. Every refusal assertion below therefore checks the MESSAGE,
// never merely res.IsError.
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

// veBaseFile writes a stub .base file at rel. op=embed only checks that it
// EXISTS (EMB-098) — the bytes are never parsed on this write path; a
// data file's saved views are loaded from their own imported YAML under
// records.ViewsDir, never by re-reading the .base itself (import is
// one-shot — see pkg/gateway/rest_knowledge_base_views.go's header on this
// package's sibling read endpoint for the fuller argument).
func veBaseFile(t *testing.T, root, rel string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o700))
	require.NoError(t, os.WriteFile(full, []byte("views: []\n"), 0o600))
}

// vePicture writes a stub image file at rel. op=embed classifies a target
// by extension only (embedPictureExts), matching the reader's own
// Conservative Type Design posture — the bytes are never inspected.
func vePicture(t *testing.T, root, rel string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o700))
	require.NoError(t, os.WriteFile(full, []byte("not a real image; extension-only classification"), 0o600))
}

// veViewFile writes one imported-view YAML under records.ViewsDir, with
// `source` naming the .base file it belongs to — SavedView.Def.Source is
// the ONLY thing that ties a saved view to a data file (view.go's own doc
// comment: "vault-relative path of the file this view was IMPORTED from").
func veViewFile(t *testing.T, root, fileName, name, label, source string) {
	t.Helper()
	dir := records.ViewsDir(root)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	yaml := "name: " + name + "\nlabel: " + label + "\nsource: " + source + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, fileName), []byte(yaml), 0o600))
}

// ---------------------------------------------------------------------------
// Test 32 — the happy path: one file written, the destination section
// created because it did not exist, the embed target left untouched.
// ---------------------------------------------------------------------------

func TestKnowledgeEditEmbedOp_WritesOnlyTheNamedNote(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	veBaseFile(t, root, "Tasks.base")
	veViewFile(t, root, "needs-daniel.yaml", "needs-daniel", "Needs Daniel", "Tasks.base")
	beforeBase, rerr := os.ReadFile(filepath.Join(root, "Tasks.base"))
	require.NoError(t, rerr)

	a4Note(t, root, "Dashboard.md", "---\nstatus: draft\n---\nIntro.\n")
	v := a4Version(t, root, "Dashboard.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Dashboard.md",
		"target": "Tasks.base", "view": "Needs Daniel", "section": "This week",
		"expect_version": v,
	})
	require.False(t, res.IsError, "a valid embed request must not be refused: %s", res.ForLLM)
	if !strings.Contains(res.ForLLM, `EMBED ![[Tasks.base#Needs Daniel]] under "This week"`) {
		t.Fatalf("expected the composed notation in the reply, got: %s", res.ForLLM)
	}

	got := a4Read(t, root, "Dashboard.md")
	if !strings.Contains(got, "## This week") {
		t.Fatalf("the destination section must be created when absent, got: %s", got)
	}
	if !strings.Contains(got, "![[Tasks.base#Needs Daniel]]") {
		t.Fatalf("the exact embed notation must be written, got: %s", got)
	}
	if !strings.Contains(got, "Intro.") {
		t.Fatalf("existing content must survive, got: %s", got)
	}

	afterBase, rerr := os.ReadFile(filepath.Join(root, "Tasks.base"))
	require.NoError(t, rerr)
	if string(beforeBase) != string(afterBase) {
		t.Fatalf("the embed TARGET must never be written to — only the named note")
	}
}

// ---------------------------------------------------------------------------
// Test 33 — an unknown view is refused, listing the labels that DO exist.
// ---------------------------------------------------------------------------

func TestKnowledgeEditEmbedOp_UnknownViewRefusedListingWhatExists(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	veBaseFile(t, root, "Tasks.base")
	veViewFile(t, root, "open.yaml", "open", "Open", "Tasks.base")
	veViewFile(t, root, "closed.yaml", "closed", "Closed", "Tasks.base")

	orig := "---\nstatus: draft\n---\nIntro.\n"
	a4Note(t, root, "Dashboard.md", orig)
	v := a4Version(t, root, "Dashboard.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Dashboard.md",
		"target": "Tasks.base", "view": "Pending", "section": "This week",
		"expect_version": v,
	})
	if !res.IsError {
		t.Fatalf("a view label that does not exist must be refused, got success: %s", res.ForLLM)
	}
	for _, want := range []string{"Open", "Closed"} {
		if !strings.Contains(res.ForLLM, want) {
			t.Fatalf("the refusal must list the real labels (missing %q), got: %s", want, res.ForLLM)
		}
	}
	if got := a4Read(t, root, "Dashboard.md"); got != orig {
		t.Fatalf("a refused embed must leave the note byte-identical, got: %s", got)
	}
}

// ---------------------------------------------------------------------------
// Test 34 — a target outside the collection is refused BEFORE any write,
// with a message that names containment — and NOT "unsupported op", which
// is what every op="embed" call hit before this change existed. Paired with
// a succeeding embed in the same body (X7 repair).
// ---------------------------------------------------------------------------

func TestKnowledgeEditEmbedOp_TargetOutsideCollectionRefused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	orig := "---\nstatus: draft\n---\nIntro.\n"
	a4Note(t, root, "Dashboard.md", orig)
	v := a4Version(t, root, "Dashboard.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Dashboard.md",
		"target": "../outside/secret.md", "section": "This week",
		"expect_version": v,
	})
	if !res.IsError {
		t.Fatalf("a target escaping the collection must be refused, got success: %s", res.ForLLM)
	}
	if strings.Contains(strings.ToLower(res.ForLLM), "unsupported op") {
		t.Fatalf("op=\"embed\" must be a recognised operation, not fall through to refuseOp's "+
			"'unsupported op' branch: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "not inside this collection") {
		t.Fatalf("the refusal must name CONTAINMENT specifically, got: %s", res.ForLLM)
	}
	if got := a4Read(t, root, "Dashboard.md"); got != orig {
		t.Fatalf("a refused embed must leave the note byte-identical, got: %s", got)
	}

	// The pairing that proves op="embed" genuinely exists: a VALID request
	// against the same note succeeds.
	vePicture(t, root, "photo.png")
	v2 := a4Version(t, root, "Dashboard.md")
	ok := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Dashboard.md",
		"target": "photo.png", "section": "This week", "expect_version": v2,
	})
	require.False(t, ok.IsError, "a valid embed target must succeed: %s", ok.ForLLM)
}

// ---------------------------------------------------------------------------
// Test 35 — a modifier that does not apply to the target's kind is refused,
// naming the mismatch — paired with the SAME modifier accepted on a target
// of the right kind (X7 repair; before this change refuseOp refused both
// requests identically, for the wrong reason).
// ---------------------------------------------------------------------------

func TestKnowledgeEditEmbedOp_ModifierKindMismatchRefused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	vePicture(t, root, "photo.png")
	a4Note(t, root, "song.mp3", "not audio; extension only matters here") // stand-in "sound file"
	a4Note(t, root, "Dashboard.md", "---\nstatus: draft\n---\nIntro.\n")

	v1 := a4Version(t, root, "Dashboard.md")
	refused := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Dashboard.md",
		"target": "song.mp3", "width": "400", "section": "This week",
		"expect_version": v1,
	})
	if !refused.IsError {
		t.Fatalf("'width' on a non-picture must be refused, got success: %s", refused.ForLLM)
	}
	if !strings.Contains(refused.ForLLM, "'width'") || !strings.Contains(refused.ForLLM, "picture") {
		t.Fatalf("the refusal must name the kind mismatch ('width'/picture), got: %s", refused.ForLLM)
	}

	v2 := a4Version(t, root, "Dashboard.md")
	accepted := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Dashboard.md",
		"target": "photo.png", "width": "400", "section": "This week",
		"expect_version": v2,
	})
	require.False(t, accepted.IsError, "'width' on a picture must be accepted: %s", accepted.ForLLM)
	if !strings.Contains(a4Read(t, root, "Dashboard.md"), "![[photo.png|400]]") {
		t.Fatalf("expected the sized picture embed to be written, got: %s", a4Read(t, root, "Dashboard.md"))
	}
}

// ---------------------------------------------------------------------------
// Test 36 — the PER-OPERATION argument set: an argument a DIFFERENT op
// reads is refused for an op that does not read it, naming the operation —
// not merely "some refusal occurred". Sharpened per this work's own X7
// sweep: 'width' is now a legitimate editArgNames member (embed's own
// argument), so the OLD global-only sweep would no longer catch
// op="link"+width by coincidence; only a genuine per-operation set does.
// ---------------------------------------------------------------------------

func TestKnowledgeEdit_PerOpArgumentSetRejectsForeignArgs(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Real.md", "---\nstatus: draft\n---\nBody.\n")
	vePicture(t, root, "photo.png")

	// link does not read 'width' (embed's argument).
	v1 := a4Version(t, root, "Real.md")
	linkPlusWidth := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "link", "path": "Real.md",
		"target": "photo.png", "width": "400", "expect_version": v1,
	})
	if !linkPlusWidth.IsError {
		t.Fatalf("op=link must not read 'width', got success: %s", linkPlusWidth.ForLLM)
	}
	if !strings.Contains(linkPlusWidth.ForLLM, "link") || !strings.Contains(linkPlusWidth.ForLLM, "width") {
		t.Fatalf("the refusal must name the OPERATION ('link') alongside the foreign argument, got: %s",
			linkPlusWidth.ForLLM)
	}

	// embed does not read 'body' (create/append_section/replace_body's
	// argument).
	v2 := a4Version(t, root, "Real.md")
	embedPlusBody := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Real.md",
		"target": "photo.png", "section": "Pics", "body": "stray text",
		"expect_version": v2,
	})
	if !embedPlusBody.IsError {
		t.Fatalf("op=embed must not read 'body', got success: %s", embedPlusBody.ForLLM)
	}
	if !strings.Contains(embedPlusBody.ForLLM, "embed") || !strings.Contains(embedPlusBody.ForLLM, "body") {
		t.Fatalf("the refusal must name the OPERATION ('embed') alongside the foreign argument, got: %s",
			embedPlusBody.ForLLM)
	}

	// Each argument IS still accepted by the operation that actually reads
	// it — the half that cannot pass without a genuine per-op mechanism.
	v3 := a4Version(t, root, "Real.md")
	embedWithWidth := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Real.md",
		"target": "photo.png", "width": "200", "section": "Pics",
		"expect_version": v3,
	})
	require.False(t, embedWithWidth.IsError, "op=embed must accept 'width': %s", embedWithWidth.ForLLM)

	v4 := a4Version(t, root, "Real.md")
	linkWithSection := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "link", "path": "Real.md",
		"target": "photo.png", "section": "See also", "expect_version": v4,
	})
	require.False(t, linkWithSection.IsError, "op=link must accept 'section': %s", linkWithSection.ForLLM)
}

// ---------------------------------------------------------------------------
// Test 37 — a missing (and an empty) version token is refused, naming the
// TOKEN rather than "unsupported op"; the same request with the current
// token succeeds.
// ---------------------------------------------------------------------------

func TestKnowledgeEditEmbedOp_MissingExpectVersionRefused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	vePicture(t, root, "photo.png")
	orig := "---\nstatus: draft\n---\nIntro.\n"
	a4Note(t, root, "Dashboard.md", orig)

	noToken := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Dashboard.md",
		"target": "photo.png", "section": "This week",
	})
	if !noToken.IsError {
		t.Fatalf("an embed with no version token at all must be refused, got success: %s", noToken.ForLLM)
	}
	if !strings.Contains(noToken.ForLLM, "version token") {
		t.Fatalf("the refusal must name the MISSING TOKEN, not an unrecognised op, got: %s", noToken.ForLLM)
	}

	emptyToken := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Dashboard.md",
		"target": "photo.png", "section": "This week", "expect_version": "",
	})
	if !emptyToken.IsError || !strings.Contains(emptyToken.ForLLM, "version token") {
		t.Fatalf("an EMPTY version token must be refused for the same stated reason, got: %s", emptyToken.ForLLM)
	}
	if got := a4Read(t, root, "Dashboard.md"); got != orig {
		t.Fatalf("neither refused call may have written anything, got: %s", got)
	}

	v := a4Version(t, root, "Dashboard.md")
	ok := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Dashboard.md",
		"target": "photo.png", "section": "This week", "expect_version": v,
	})
	require.False(t, ok.IsError, "the identical request WITH the current token must succeed: %s", ok.ForLLM)
	if strings.Count(a4Read(t, root, "Dashboard.md"), "![[photo.png]]") != 1 {
		t.Fatalf("exactly one line must have been written, got: %s", a4Read(t, root, "Dashboard.md"))
	}
}

// ---------------------------------------------------------------------------
// Design mandate beyond the numbered test list: the embed TARGET must
// actually exist — a syntactically valid, IN-COLLECTION path that names no
// real file is a different failure from "outside the collection" (test 34)
// and must be refused with a DIFFERENT, distinguishing message, so a caller
// (or a later maintainer) cannot conflate "escapes the collection" with
// "was never there". Paired with a succeeding call against a target that
// does exist.
// ---------------------------------------------------------------------------

func TestKnowledgeEditEmbedOp_MissingTargetFileRefused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	orig := "---\nstatus: draft\n---\nIntro.\n"
	a4Note(t, root, "Dashboard.md", orig)
	v := a4Version(t, root, "Dashboard.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Dashboard.md",
		"target": "Nonexistent.png", "section": "This week", "expect_version": v,
	})
	if !res.IsError {
		t.Fatalf("a target that does not exist must be refused, got success: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "does not exist") {
		t.Fatalf("the refusal must name EXISTENCE, distinct from containment, got: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "not inside this collection") {
		t.Fatalf("a missing (but in-collection) target must not be reported as a containment escape, got: %s",
			res.ForLLM)
	}
	if got := a4Read(t, root, "Dashboard.md"); got != orig {
		t.Fatalf("a refused embed must leave the note byte-identical, got: %s", got)
	}

	vePicture(t, root, "Real.png")
	v2 := a4Version(t, root, "Dashboard.md")
	ok := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Dashboard.md",
		"target": "Real.png", "section": "This week", "expect_version": v2,
	})
	require.False(t, ok.IsError, "a target that DOES exist must succeed: %s", ok.ForLLM)
}

// ---------------------------------------------------------------------------
// Supplementary coverage: target_heading is validated against the target
// NOTE's real headings, listing what exists when it does not match —
// mirrors test 33's "unknown view" shape for the note/heading case the BDD
// Scenario Outline also names.
// ---------------------------------------------------------------------------

func TestKnowledgeEditEmbedOp_TargetHeadingMustExist(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Plan.md", "# Plan\n\n## Q3\n\nBody.\n\n## Q4\n\nMore.\n")
	orig := "---\nstatus: draft\n---\nIntro.\n"
	a4Note(t, root, "Dashboard.md", orig)

	v1 := a4Version(t, root, "Dashboard.md")
	missing := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Dashboard.md",
		"target": "Plan.md", "target_heading": "Q1", "section": "This week",
		"expect_version": v1,
	})
	if !missing.IsError {
		t.Fatalf("a target_heading that does not exist must be refused, got success: %s", missing.ForLLM)
	}
	for _, want := range []string{"Q3", "Q4"} {
		if !strings.Contains(missing.ForLLM, want) {
			t.Fatalf("the refusal must list the real headings (missing %q), got: %s", want, missing.ForLLM)
		}
	}
	if got := a4Read(t, root, "Dashboard.md"); got != orig {
		t.Fatalf("a refused embed must leave the note byte-identical, got: %s", got)
	}

	v2 := a4Version(t, root, "Dashboard.md")
	ok := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Dashboard.md",
		"target": "Plan.md", "target_heading": "Q3", "section": "This week",
		"expect_version": v2,
	})
	require.False(t, ok.IsError, "an existing target_heading must be accepted: %s", ok.ForLLM)
	if !strings.Contains(a4Read(t, root, "Dashboard.md"), "![[Plan.md#Q3]]") {
		t.Fatalf("expected the heading-fragmented embed to be written, got: %s", a4Read(t, root, "Dashboard.md"))
	}
}

// ---------------------------------------------------------------------------
// Supplementary coverage: target_block is written as a "^" fragment once it
// passes embedBlockAnchorPattern (there is still no EXISTENCE check against
// the target note's real blocks — the spec states none, unlike view/
// heading — but the STRING is now constrained to the reader's own
// block-anchor grammar; see the malformed-target_block test below for the
// refusal side of that). A redundant leading "^" from the caller is not
// doubled.
// ---------------------------------------------------------------------------

func TestKnowledgeEditEmbedOp_TargetBlockWritesCaretFragment(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Plan.md", "# Plan\n\nA paragraph. ^abc123\n")
	a4Note(t, root, "Dashboard.md", "---\nstatus: draft\n---\nIntro.\n")
	v := a4Version(t, root, "Dashboard.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Dashboard.md",
		"target": "Plan.md", "target_block": "^abc123", "section": "This week",
		"expect_version": v,
	})
	require.False(t, res.IsError, "a target_block modifier must be accepted: %s", res.ForLLM)
	got := a4Read(t, root, "Dashboard.md")
	if !strings.Contains(got, "![[Plan.md#^abc123]]") {
		t.Fatalf("expected a single '^' before the block id (no doubling), got: %s", got)
	}
	if strings.Contains(got, "^^abc123") {
		t.Fatalf("a caller-supplied leading '^' must not be doubled, got: %s", got)
	}
}

// ---------------------------------------------------------------------------
// Security-review regression: target_block used to be written VERBATIM into
// "^" + value with no validation, so a value containing "]]" plus a
// newline could break out of the single fragment position inside the
// composed "![[target#^value]]" notation and land arbitrary markdown lines
// in the note under the guise of one embed notation. target_block must now
// be checked against the reader's own block-anchor grammar
// (src/components/library/preview/noteTransclusion.ts's BLOCK_ANCHOR_RE:
// `[A-Za-z0-9-]+`), exactly as width/view/target_heading are already
// checked against theirs.
// ---------------------------------------------------------------------------

func TestKnowledgeEditEmbedOp_MalformedTargetBlockRefused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Plan.md", "# Plan\n\nA paragraph. ^abc123\n")
	orig := "---\nstatus: draft\n---\nIntro.\n"
	a4Note(t, root, "Dashboard.md", orig)

	// The reviewer's exact payload: a caret id that closes the wikilink
	// early ("x]]"), then a blank line and a second embed notation of the
	// attacker's choosing ("## Injected" / "![[Other").
	const injectionPayload = "x]]\n\n## Injected\n\n![[Other"

	v1 := a4Version(t, root, "Dashboard.md")
	injected := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Dashboard.md",
		"target": "Plan.md", "target_block": injectionPayload, "section": "This week",
		"expect_version": v1,
	})
	if !injected.IsError {
		t.Fatalf("a target_block that is not a valid block anchor must be refused, got success: %s", injected.ForLLM)
	}
	for _, want := range []string{"embed", "target_block"} {
		if !strings.Contains(injected.ForLLM, want) {
			t.Fatalf("the refusal must name the operation and the argument (missing %q), got: %s", want, injected.ForLLM)
		}
	}
	if !strings.Contains(injected.ForLLM, "^abc123") {
		t.Fatalf("the refusal must show what a valid block anchor looks like, got: %s", injected.ForLLM)
	}
	if got := a4Read(t, root, "Dashboard.md"); got != orig {
		t.Fatalf("a refused embed must leave the note byte-identical — no injected markdown may reach the file, got: %s", got)
	}

	// A plain space, an empty-after-caret value, and a value carrying '#'
	// are all rejected the same way — none of them can ever match a real
	// Obsidian block anchor either.
	for _, bad := range []string{"has space", "^", "a#b", "a|b"} {
		v := a4Version(t, root, "Dashboard.md")
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "embed", "path": "Dashboard.md",
			"target": "Plan.md", "target_block": bad, "section": "This week",
			"expect_version": v,
		})
		if !res.IsError {
			t.Fatalf("malformed target_block %q must be refused, got success: %s", bad, res.ForLLM)
		}
	}

	// The valid form still works, proving the fix is a grammar check, not
	// a blanket ban.
	v2 := a4Version(t, root, "Dashboard.md")
	ok := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Dashboard.md",
		"target": "Plan.md", "target_block": "abc123", "section": "This week",
		"expect_version": v2,
	})
	require.False(t, ok.IsError, "a valid target_block must still be accepted: %s", ok.ForLLM)
	if !strings.Contains(a4Read(t, root, "Dashboard.md"), "![[Plan.md#^abc123]]") {
		t.Fatalf("expected the valid block-fragmented embed to be written, got: %s", a4Read(t, root, "Dashboard.md"))
	}
}

// ---------------------------------------------------------------------------
// Supplementary coverage: at most one fragment modifier, and 'page' is
// accepted by the argument envelope but always refused today (US-12 is
// deferred) — paired with a plain, unfragmented embed succeeding.
// ---------------------------------------------------------------------------

func TestKnowledgeEditEmbedOp_FragmentModifiersMutuallyExclusiveAndPageDeferred(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Plan.md", "# Plan\n\n## Q3\n\nBody.\n")
	a4Note(t, root, "Dashboard.md", "---\nstatus: draft\n---\nIntro.\n")

	v1 := a4Version(t, root, "Dashboard.md")
	both := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Dashboard.md",
		"target": "Plan.md", "target_heading": "Q3", "target_block": "abc123",
		"section": "This week", "expect_version": v1,
	})
	if !both.IsError {
		t.Fatalf("giving both target_heading and target_block must be refused, got success: %s", both.ForLLM)
	}

	v2 := a4Version(t, root, "Dashboard.md")
	withPage := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Dashboard.md",
		"target": "Plan.md", "page": "3", "section": "This week", "expect_version": v2,
	})
	if !withPage.IsError {
		t.Fatalf("'page' must be refused (US-12 is deferred), got success: %s", withPage.ForLLM)
	}
	if !strings.Contains(withPage.ForLLM, "page") {
		t.Fatalf("the refusal must name 'page', got: %s", withPage.ForLLM)
	}

	v3 := a4Version(t, root, "Dashboard.md")
	plain := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "embed", "path": "Dashboard.md",
		"target": "Plan.md", "section": "This week", "expect_version": v3,
	})
	require.False(t, plain.IsError, "a plain, unfragmented embed must still succeed: %s", plain.ForLLM)
	if !strings.Contains(a4Read(t, root, "Dashboard.md"), "![[Plan.md]]") {
		t.Fatalf("expected the plain embed to be written, got: %s", a4Read(t, root, "Dashboard.md"))
	}
}
