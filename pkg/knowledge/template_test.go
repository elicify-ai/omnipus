// Omnipus — ADR-067 stage 3 (US-12): templates a new note starts from.
//
// Every expected value in this file is derived from the SPECIFICATION, not
// from what template.go happens to produce:
//
//   - FR-102 — "MUST substitute only a fixed documented placeholder set and
//     MUST NOT execute template content". The set is asserted as a literal
//     list, so adding a fifth placeholder fails a test rather than passing
//     silently; the "no execution" half is asserted by requiring
//     instruction-shaped input to appear in the output BYTE FOR BYTE.
//   - FR-101 / AC-12.2 — "template surfaces reachable without enabling hidden
//     files". The oracle is a directory listing with dotfiles filtered, which
//     is what "hidden files" means in this product (pkg/library/entries.go
//     filters names beginning with "."): the templates must be invisible to
//     that listing AND still returned by ListTemplates.
//   - US-10 / FR-044 — containment and symlink refusal apply to templates too.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Fixtures (a1* — one prefix per work unit, so two units editing this package
// at once cannot collide on a helper name)
// ---------------------------------------------------------------------------

// a1Real resolves a path through symlinks, which macOS temp directories
// require: /var is a symlink to /private/var, and a containment comparison
// against the unresolved spelling rejects every file in the collection.
func a1Real(t *testing.T, p string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	return resolved
}

// a1Vault creates a knowledge base with an Omnipus marker and returns its real
// root path.
func a1Vault(t *testing.T, displayName string, marker *Marker) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "vault")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, MarkerDirName), 0o700))
	m := Marker{DisplayName: displayName}
	if marker != nil {
		m = *marker
	}
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, MarkerDirName, markerFileName), raw, 0o600))
	return a1Real(t, dir)
}

// a1Collection opens a collection at root.
func a1Collection(t *testing.T, root string) *Collection {
	t.Helper()
	c, err := OpenCollection(root)
	require.NoError(t, err)
	return c
}

// a1WriteTemplate writes a template into the collection's templates directory.
func a1WriteTemplate(t *testing.T, c *Collection, name, body string) {
	t.Helper()
	dir := c.TemplatesDir()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600))
}

// a1Clock is the fixed instant every time-derived expectation in these tests is
// computed from. A test that reads the wall clock cannot state an expected
// value, only re-derive the implementation's own answer.
func a1Clock(t *testing.T) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, "2026-08-23T14:05:09Z")
	require.NoError(t, err)
	return ts
}

// ---------------------------------------------------------------------------
// FR-102 / US-12 AS-4, AS-5 — spec test 42, "instruction-looking content stays
// literal"
// ---------------------------------------------------------------------------

// TestTemplate_NoExecution is spec test 42.
//
// The threat this guards is not hypothetical. Obsidian's Templater plugin uses
// "<% … %>" and executes JavaScript inside it; a shell reads "$(…)" and
// "${…}"; a template is a file an operator (or a sync partner, or a repository
// they cloned) put in the vault. Omnipus's contract is that a template is DATA:
// four literal tokens are substituted and every other byte is copied.
//
// The assertion is byte-level containment of each dangerous string, not
// "produced no error". A silent expansion that emitted an empty string for
// "<% tp.user.run() %>" would satisfy a no-error test perfectly while having
// deleted the operator's line.
func TestTemplate_NoExecution(t *testing.T) {
	now := a1Clock(t)

	// Every one of these is a syntax some OTHER tool executes. None of them
	// is in Omnipus's documented set, so every one must survive verbatim.
	dangerous := []string{
		"<% tp.file.title %>",
		"<%* await tp.user.exfiltrate() %>",
		"$(id)",
		"${HOME}",
		"`rm -rf ~`",
		"{{ date }}",    // spaced — NOT the documented token
		"{{date:YYYY}}", // format argument — deliberately not supported
		"{{unknown_thing}}",
		"{%- if x -%}",
		"#{ruby}",
	}
	body := "---\ntitle: {{title}}\ncreated: {{datetime}}\n---\n\n# {{title}}\n\n" +
		strings.Join(dangerous, "\n") + "\n\nOn {{date}} at {{time}}.\n"

	out := string(ExpandTemplate([]byte(body), TemplateVars{Title: "Weekly review", Now: now}))

	for _, d := range dangerous {
		assert.Contains(t, out, d,
			"instruction-shaped template content must be inserted as plain text and never "+
				"interpreted or dropped (FR-102, US-12 AS-5): %q went missing", d)
	}

	// The four documented tokens, with values derived from the spec's own
	// wording (ISO date, 24-hour time, RFC 3339 instant) and the fixed clock.
	assert.Contains(t, out, "title: Weekly review")
	assert.Contains(t, out, "created: 2026-08-23T14:05:09Z")
	assert.Contains(t, out, "On 2026-08-23 at 14:05.")
	assert.NotContains(t, out, "{{title}}")
	assert.NotContains(t, out, "{{datetime}}")
}

// TestTemplate_PlaceholderSetIsExactlyTheDocumentedFour guards FR-102's word
// "fixed". A placeholder set that grows by accident is how a template gains
// access to something it was never meant to interpolate.
func TestTemplate_PlaceholderSetIsExactlyTheDocumentedFour(t *testing.T) {
	assert.Equal(t,
		[]string{"{{title}}", "{{date}}", "{{time}}", "{{datetime}}"},
		TemplatePlaceholders(),
		"the placeholder set is fixed and documented (FR-102); changing it is a product "+
			"decision that must update the documentation and this assertion together")
}

// TestTemplate_SubstitutionIsNotRecursive proves the single-pass property.
//
// A note whose TITLE is the text "{{date}}" must produce that text, not the
// date. Recursive expansion is how a substituted value becomes an instruction:
// it is the difference between inserting operator data and evaluating it.
func TestTemplate_SubstitutionIsNotRecursive(t *testing.T) {
	out := string(ExpandTemplate([]byte("title: {{title}}\n"), TemplateVars{
		Title: "{{date}}",
		Now:   a1Clock(t),
	}))
	assert.Equal(t, "title: {{date}}\n", out,
		"expansion must be a single left-to-right pass: a substituted VALUE is never re-scanned")
}

// TestTemplate_NoClockLeavesTimeTokensLiteral asserts the zero-clock rule.
//
// The alternative — rendering time.Time's zero value — writes "0001-01-01"
// into the operator's note: a wrong answer that looks like a right one, and
// which no later reader can tell from a real date.
func TestTemplate_NoClockLeavesTimeTokensLiteral(t *testing.T) {
	out := string(ExpandTemplate([]byte("{{date}} {{time}} {{datetime}} {{title}}"), TemplateVars{Title: "T"}))
	assert.Equal(t, "{{date}} {{time}} {{datetime}} T", out)
	assert.NotContains(t, out, "0001-01-01",
		"a zero clock must leave the token visible, never render the zero time")
}

// ---------------------------------------------------------------------------
// FR-101 / AC-12.2 — spec test 41, templates reachable without hidden files
// ---------------------------------------------------------------------------

// TestTemplates_ReachableWithoutHiddenFiles is spec test 41.
//
// The oracle is deliberately the OPPOSITE of the API under test: a plain
// directory listing of the collection root with dot-prefixed names filtered —
// exactly what pkg/library/entries.go does when includeHidden is false. The
// templates must be invisible to that listing (proving they really are hidden
// files, so the requirement is not vacuous) and simultaneously returned by
// ListTemplates (proving the requirement is met).
//
// Without the first half this test passes on a collection whose templates sit
// in plain sight, which would prove nothing at all.
func TestTemplates_ReachableWithoutHiddenFiles(t *testing.T) {
	root := a1Vault(t, "Research", nil)
	c := a1Collection(t, root)
	a1WriteTemplate(t, c, "Meeting.md", "---\ntype: meeting\n---\n")
	a1WriteTemplate(t, c, "Daily.md", "---\ntype: daily\n---\n")

	// --- the templates are genuinely hidden from an ordinary listing ---
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	visible := make([]string, 0, len(entries))
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			visible = append(visible, e.Name())
		}
	}
	require.Empty(t, visible,
		"the fixture must keep templates inside the dot-prefixed marker directory, "+
			"otherwise this test proves nothing about hidden files")

	// --- and still reachable ---
	got, err := ListTemplates(OSLinkFS(), c)
	require.NoError(t, err)
	names := make([]string, 0, len(got))
	for _, ti := range got {
		names = append(names, ti.Name)
	}
	assert.Equal(t, []string{"Daily.md", "Meeting.md"}, names,
		"ListTemplates must reach templates inside the hidden marker directory, sorted (FR-101)")

	for _, ti := range got {
		require.FileExists(t, ti.AbsPath,
			"AbsPath is what a settings surface opens for editing; it must be a real file")
	}

	raw, err := ReadTemplate(OSLinkFS(), c, "Meeting.md")
	require.NoError(t, err)
	assert.Equal(t, "---\ntype: meeting\n---\n", string(raw))
}

// TestTemplates_MarkerDecidesTheLocation asserts that the marker's own
// templates_dir is honoured (ADR-067 D2 — "the marker holds a name and a
// templates/ directory"), rather than a hardcoded path.
func TestTemplates_MarkerDecidesTheLocation(t *testing.T) {
	root := a1Vault(t, "Research", &Marker{DisplayName: "Research", TemplatesDir: "note-forms"})
	c := a1Collection(t, root)
	require.Equal(t, filepath.Join(root, MarkerDirName, "note-forms"), c.TemplatesDir())

	a1WriteTemplate(t, c, "Idea.md", "# {{title}}\n")
	got, err := ListTemplates(OSLinkFS(), c)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Idea.md", got[0].Name)
}

// TestTemplates_AbsentDirectoryIsNotAnError: a brand-new knowledge base, and
// every Obsidian vault Omnipus has never written to, has no templates
// directory. That is the ordinary state, not a failure.
func TestTemplates_AbsentDirectoryIsNotAnError(t *testing.T) {
	c := a1Collection(t, a1Vault(t, "Fresh", nil))
	got, err := ListTemplates(OSLinkFS(), c)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestReadTemplate_RefusesEscapeAndSymlink covers the two ways a template read
// becomes a read of something else entirely.
func TestReadTemplate_RefusesEscapeAndSymlink(t *testing.T) {
	root := a1Vault(t, "Research", nil)
	c := a1Collection(t, root)
	a1WriteTemplate(t, c, "Meeting.md", "ok\n")

	outsideDir := a1Real(t, t.TempDir())
	secret := filepath.Join(outsideDir, "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600))

	t.Run("traversal", func(t *testing.T) {
		_, err := ReadTemplate(OSLinkFS(), c, "../../../secret.txt")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrTemplateNameRefused)
	})

	t.Run("absolute", func(t *testing.T) {
		_, err := ReadTemplate(OSLinkFS(), c, secret)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrTemplateNameRefused)
	})

	t.Run("symlink out of the collection", func(t *testing.T) {
		link := filepath.Join(c.TemplatesDir(), "Sneaky.md")
		require.NoError(t, os.Symlink(secret, link))

		_, err := ReadTemplate(OSLinkFS(), c, "Sneaky.md")
		require.Error(t, err, "a symlinked template must never be read through")
		assert.NotContains(t, err.Error(), "PRIVATE KEY")

		// And it is not offered in the listing either — a symlink is skipped,
		// not followed (FR-044).
		got, listErr := ListTemplates(OSLinkFS(), c)
		require.NoError(t, listErr)
		for _, ti := range got {
			assert.NotEqual(t, "Sneaky.md", ti.Name)
		}
	})

	t.Run("symlink INSIDE the collection is still not followed", func(t *testing.T) {
		// This case is the one that isolates FR-044 from containment: the
		// link's target is a perfectly ordinary note inside the collection,
		// so the containment gate has no objection. Only the "a symlink is
		// not a regular file" rule refuses it — which is the rule this
		// package applies to symlinks everywhere else.
		inside := filepath.Join(c.Root(), "ordinary.md")
		require.NoError(t, os.WriteFile(inside, []byte("ordinary note\n"), 0o600))
		require.NoError(t, os.Symlink(inside, filepath.Join(c.TemplatesDir(), "Linked.md")))

		_, err := ReadTemplate(OSLinkFS(), c, "Linked.md")
		require.Error(t, err, "a symlink is skipped, never followed (FR-044)")
		assert.ErrorIs(t, err, ErrTemplateNameRefused)

		got, listErr := ListTemplates(OSLinkFS(), c)
		require.NoError(t, listErr)
		for _, ti := range got {
			assert.NotEqual(t, "Linked.md", ti.Name)
		}
	})

	t.Run("missing", func(t *testing.T) {
		_, err := ReadTemplate(OSLinkFS(), c, "NoSuch.md")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrTemplateNotFound)
	})
}
