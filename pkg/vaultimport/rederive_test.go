// Tests for RederiveBase — the single-`.base` re-derivation entry a raw text
// edit of a base file goes through (D-119's index half; the full spec is the
// "needs another cluster" section of FIX2-REPORT-index-find.md):
//
//   - reuses TranslateBase/SlugRegistry over the CURRENT on-disk schemas;
//   - preserves the existing slugs of views whose `source` is this `.base`;
//   - collision-checks new names against the loaded ViewSet;
//   - writes new/changed view YAMLs into records.ViewsDir;
//   - deletes view files from this source the edited `.base` no longer
//     declares — but never on a refused translation.
//
// The schemas in every fixture are HAND-WRITTEN under
// .omnipus-vault/records/, never inferred: these tests measure the
// re-derivation's contract against decided schemas, exactly the state a
// vault is in when an operator edits a base.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package vaultimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// rederiveWidgetSchema is a decided, on-disk schema: enum status, integer
// priority. A base filtered on both proves the translation ran against the
// CURRENT schema rather than a re-inference of it.
const rederiveWidgetSchema = "schema_version: 1\n" +
	"type: widget\n" +
	"identity:\n" +
	"  prefix: WD\n" +
	"properties:\n" +
	"  name:     { type: text }\n" +
	"  status:   { type: enum, values: [open, closed] }\n" +
	"  priority: { type: integer }\n"

// rederiveBaseWithViews is a `.base` written the way Obsidian writes one,
// declaring the named views filtered on the schema above.
const rederiveBaseWithViews = `
filters:
  and:
    - type == "widget"
views:
  - type: table
    name: Open
    filters:
      and:
        - status == "open"
    order:
      - file.name
      - status
  - type: table
    name: Closed
    filters:
      and:
        - status == "closed"
    order:
      - file.name
`

// buildRederiveVault seeds a throwaway vault: decided schema on disk, the
// given `.base` at the vault-relative path, and whatever view YAMLs a prior
// import is simulated to have written (views maps slug → file body).
func buildRederiveVault(t *testing.T, baseBody string, views map[string]string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(records.SchemaDir(root), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(records.SchemaDir(root), "widget.yaml"),
		[]byte(rederiveWidgetSchema), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Projects.base"), []byte(baseBody), 0o644))
	if len(views) > 0 {
		require.NoError(t, os.MkdirAll(records.ViewsDir(root), 0o700))
	}
	for slug, body := range views {
		require.NoError(t, os.WriteFile(
			filepath.Join(records.ViewsDir(root), slug+".yaml"), []byte(body), 0o600))
	}
	return root
}

// loadRederivedViews loads the vault's views back through the REAL loader,
// with the real schemas — the round trip the product itself performs.
func loadRederivedViews(t *testing.T, root string) (*records.SchemaSet, *records.ViewSet) {
	t.Helper()
	set, _, err := records.LoadSchemas(root)
	require.NoError(t, err)
	vs, report, err := records.LoadViews(root, set)
	require.NoError(t, err)
	require.True(t, report.OK(), "fixture views must load cleanly: %+v", report.Rejections)
	return set, vs
}

// TestRederiveBase_WritesViewsAgainstCurrentOnDiskSchemas — a `.base` whose
// bytes were just saved must produce the view YAMLs an import would have
// written: the files appear under records.ViewsDir, they load through the
// REAL loader against the vault's decided schemas, they carry `source:
// Projects.base`, and their filters translated (an enum literal the schema
// declares is a leaf, not an untranslated loss).
func TestRederiveBase_WritesViewsAgainstCurrentOnDiskSchemas(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)

	res, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err)
	require.NotEqual(t, OutcomeRefused, res.Status, "reason: %s", res.RefusedReason)
	assert.ElementsMatch(t, []string{"projects--open", "projects--closed"}, res.Written)
	assert.Empty(t, res.Unchanged)
	assert.Empty(t, res.Deleted)

	_, vs := loadRederivedViews(t, root)
	open, ok := vs.Get("projects--open")
	require.True(t, ok, "the produced view must load; names: %v", vs.Names())
	require.NotNil(t, open.Def.Source)
	assert.Equal(t, "Projects.base", *open.Def.Source)
	require.NotNil(t, open.Def.Label)
	assert.Equal(t, "Open", *open.Def.Label)

	// The filter TRANSLATED against the decided schema: the enum literal the
	// schema declares survives as a leaf, not as a named loss. A re-derivation
	// running against re-INFERRED (empty) schemas would refuse the view here —
	// no note in this vault carries `type: widget`.
	require.NotNil(t, open.Def.Filter, "the view's filter must survive translation")
	assert.Nil(t, open.Def.Disabled, "a fully translatable view must not be disabled")
	leafs := filterLeaves(*open.Def.Filter)
	found := false
	for _, l := range leafs {
		if l.Property != nil && *l.Property == "status" && l.Value != nil && *l.Value == "open" {
			found = true
		}
	}
	assert.True(t, found, "the status == \"open\" clause must be a real filter leaf: %+v", leafs)
}

// TestRederiveBase_PreservesItsOwnSlugsAndChecksNewNames — the preservation
// and collision halves together:
//
//   - a view this base already owns (source: Projects.base) KEEPS its slug
//     across the edit, including a slug a prior import suffixed;
//   - a NEW view's deterministic name that another source already holds gets
//     a suffix, and that other source's file is untouched.
//
// The prior state is built by RUNNING the re-derivation itself (so the view
// files on disk are exactly the YAML this pipeline writes, labels and all),
// then simulating the one piece of history a fresh run cannot produce: an
// import-time collision that left a view holding a suffixed slug.
func TestRederiveBase_PreservesItsOwnSlugsAndChecksNewNames(t *testing.T) {
	// A SECOND base with the SAME FILE NAME in a subdirectory — the one shape
	// whose view slugs collide, because a slug is derived from the base's file
	// stem plus the view name ("projects--shipped" from either file).
	const siblingBase = `
filters:
  and:
    - type == "widget"
views:
  - type: table
    name: Shipped
    filters:
      and:
        - status == "open"
`
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "archive"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "archive", "Projects.base"), []byte(siblingBase), 0o644))

	// Prior state: this base owns "projects--open" and "projects--closed";
	// the sibling base owns "projects--shipped" — all with realistic bodies.
	_, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err)
	_, err = RederiveBase(root, "archive/Projects.base")
	require.NoError(t, err)

	// The historical collision: "Closed" came out of an import that had to
	// suffix it, so its FILE and its own `name:` both read projects--closed-2.
	oldPath := filepath.Join(records.ViewsDir(root), "projects--closed.yaml")
	newPath := filepath.Join(records.ViewsDir(root), "projects--closed-2.yaml")
	body, rerr := os.ReadFile(oldPath)
	require.NoError(t, rerr)
	require.NoError(t, os.WriteFile(newPath,
		[]byte(strings.Replace(string(body), "name: projects--closed\n", "name: projects--closed-2\n", 1)), 0o600))
	require.NoError(t, os.Remove(oldPath))

	// The edited base: "Open" kept, "Closed" kept (label preserved → pinned
	// slug preserved), and a NEW "Shipped" view whose deterministic slug
	// collides with the other source's.
	edited := `
filters:
  and:
    - type == "widget"
views:
  - type: table
    name: Open
    filters:
      and:
        - status == "open"
  - type: table
    name: Closed
    filters:
      and:
        - status == "closed"
  - type: table
    name: Shipped
    filters:
      and:
        - status == "open"
        - priority >= 3
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "Projects.base"), []byte(edited), 0o644))

	res, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err)
	require.NotEqual(t, OutcomeRefused, res.Status, "reason: %s", res.RefusedReason)

	assert.Contains(t, res.Written, "projects--closed-2",
		"a view whose source is this base keeps the slug its file already holds")
	assert.NotContains(t, res.Written, "projects--closed",
		"the deterministic slug must not be used when the pinned one differs")
	assert.Contains(t, res.Written, "projects--shipped-2",
		"a NEW view whose deterministic name another source holds must be suffixed, not overwrite it")

	_, vs := loadRederivedViews(t, root)
	for _, name := range []string{"projects--open", "projects--closed-2", "projects--shipped", "projects--shipped-2"} {
		_, ok := vs.Get(name)
		assert.True(t, ok, "view %s must load after the re-derivation", name)
	}
	shipped, ok := vs.Get("projects--shipped")
	require.True(t, ok)
	require.NotNil(t, shipped.Def.Source)
	assert.Equal(t, "archive/Projects.base", *shipped.Def.Source,
		"the other source's view must still be the other source's")

	// No orphan: the unsuffixed projects--closed file was renamed away above,
	// and nothing may write it back at a slug that is only ever a collision
	// artifact.
	_, err = os.Stat(filepath.Join(records.ViewsDir(root), "projects--closed.yaml"))
	assert.True(t, os.IsNotExist(err), "no file may be written at a slug that was only ever a collision artifact")
}

// TestRederiveBase_DeletesViewsTheBaseNoLongerDeclares — an edit that drops
// a view removes its file, so the view index matches the edited base instead
// of accumulating orphans nothing lists.
func TestRederiveBase_DeletesViewsTheBaseNoLongerDeclares(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)

	// Prior state: both views exist, written by this pipeline itself.
	first, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err)
	require.NotEqual(t, OutcomeRefused, first.Status, "reason: %s", first.RefusedReason)

	edited := `
filters:
  and:
    - type == "widget"
views:
  - type: table
    name: Open
    filters:
      and:
        - status == "open"
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "Projects.base"), []byte(edited), 0o644))

	res, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err)
	require.NotEqual(t, OutcomeRefused, res.Status, "reason: %s", res.RefusedReason)
	assert.Equal(t, []string{"projects--closed"}, res.Deleted)

	_, err = os.Stat(filepath.Join(records.ViewsDir(root), "projects--closed.yaml"))
	assert.True(t, os.IsNotExist(err), "the dropped view's file must be removed")

	_, vs := loadRederivedViews(t, root)
	_, ok := vs.Get("projects--open")
	assert.True(t, ok, "the view still declared must survive")
	_, ok = vs.Get("projects--closed")
	assert.False(t, ok, "the dropped view must be gone from the loaded set")
}

// TestRederiveBase_UnchangedViewIsNotRewritten — a re-derivation over bytes
// that translate to exactly what is already on disk leaves those files
// alone (mtime-preserving no-op), which is what makes an idle save cheap and
// makes Written mean "changed", not "touched".
func TestRederiveBase_UnchangedViewIsNotRewritten(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)

	first, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err)
	require.NotEmpty(t, first.Written)

	path := filepath.Join(records.ViewsDir(root), "projects--open.yaml")
	before, err := os.Stat(path)
	require.NoError(t, err)

	second, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err)
	assert.Empty(t, second.Written, "an unchanged translation must rewrite nothing")
	assert.ElementsMatch(t, []string{"projects--open", "projects--closed"}, second.Unchanged)

	after, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, before.ModTime(), after.ModTime(), "an unchanged view file must not be rewritten")
}

// TestRederiveBase_RefusedBaseKeepsExistingViews — a `.base` that no longer
// parses is a refusal about content, and a refusal is NON-DESTRUCTIVE: no
// view is written and no view is deleted, so the operator's last working
// views survive their own broken edit.
func TestRederiveBase_RefusedBaseKeepsExistingViews(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)

	// Prior state: one healthy view from this base, written by this pipeline.
	_, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err)

	// The operator's edit breaks the base file itself.
	require.NoError(t, os.WriteFile(filepath.Join(root, "Projects.base"),
		[]byte("not: a: valid: base: file"), 0o644))

	res, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err, "a content refusal is a verdict, not an infrastructure error")
	assert.Equal(t, OutcomeRefused, res.Status)
	assert.NotEmpty(t, res.RefusedReason)
	assert.Empty(t, res.Written)
	assert.Empty(t, res.Deleted)
	assert.ElementsMatch(t, []string{"projects--open", "projects--closed"}, res.KeptExisting,
		"the refusal must name the views it deliberately left in place")

	_, vs := loadRederivedViews(t, root)
	_, ok := vs.Get("projects--open")
	assert.True(t, ok, "the existing view must survive a refused re-derivation")
}

// TestRederiveBase_MissingBaseFileIsAnError — a path that is not on disk is
// an infrastructure error (the caller logged a save that landed, so the file
// being unreadable means something else is wrong), never a silent no-op.
func TestRederiveBase_MissingBaseFileIsAnError(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)
	_, err := RederiveBase(root, "Gone.base")
	require.Error(t, err)
}

// filterLeaves flattens one filter tree to its leaf nodes, for assertions
// about what actually translated. A leaf is a node carrying an operator —
// VaultFilterNode is flat, with no separate leaf wrapper.
func filterLeaves(n generated.VaultFilterNode) []generated.VaultFilterNode {
	if n.Op != nil {
		return []generated.VaultFilterNode{n}
	}
	out := []generated.VaultFilterNode{}
	if n.All != nil {
		for _, kid := range *n.All {
			out = append(out, filterLeaves(kid)...)
		}
	}
	if n.Any != nil {
		for _, kid := range *n.Any {
			out = append(out, filterLeaves(kid)...)
		}
	}
	if n.Not != nil {
		out = append(out, filterLeaves(*n.Not)...)
	}
	return out
}
