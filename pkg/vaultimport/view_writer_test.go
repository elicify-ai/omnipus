// Omnipus — the view WRITER: the guard that every file this importer produces
// loads back through the real loader, plus the grammar constructs it carries
// into those files instead of losing.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// THE TRAP THIS FILE EXISTS TO CLOSE
//
// This package WRITES view files and pkg/records READS them, and the two can
// drift apart in the one direction neither notices on its own: a key the
// writer emits that the reader refuses. records.ParseView decodes with
// DisallowUnknownFields, so a single stray key rejects the whole file — and
// the import reports success anyway, because the rejection only happens the
// next time somebody LOADS the vault. Written successfully, reported as
// converted, dead on arrival.
//
// pkg/records cannot test that half (it must not import the importer), so the
// whole-loop assertion lives here: import a vault, then reload every file
// through records.ParseView and records.ValidateViewAgainstSchemas — the real
// loader, the same one the running product uses.
// ---------------------------------------------------------------------------

// viewWriterVault writes a small vault exercising the awkward corners of the
// grammar — disjunction, multi-clause negation, a folder scope, a directional
// grouping, an untyped view — imports it, and returns the vault root and the
// report.
func viewWriterVault(t *testing.T) (root string, rep *Report) {
	t.Helper()
	root = t.TempDir()

	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("writing %s: %v", p, err)
		}
	}

	write("Live/Open one.md", "---\ntype: task\nstatus: open\npriority: 2\nowner: mia\n---\n\nbody\n")
	write("Live/Blocked one.md", "---\ntype: task\nstatus: blocked\npriority: 5\nowner: jim\n---\n\nbody\n")
	// A `done` task exists so the INFERRED enum declares the value the base's
	// own filter compares against. Without it the clause names a value no note
	// carries, which is a named loss rather than a translation — correctly, but
	// it would make this fixture measure the wrong thing.
	write("Live/Done one.md", "---\ntype: task\nstatus: done\npriority: 3\nowner: mia\n---\n\nbody\n")
	write("99-Temp/Scratch.md", "---\ntype: task\nstatus: open\npriority: 1\nowner: ray\n---\n\nbody\n")
	write("Live/A project.md", "---\ntype: project\nstatus: open\nowner: mia\n---\n\nbody\n")

	write("Tasks.base", `filters:
  and:
    - type == "task"
    - not:
        - file.inFolder("99-Temp")
        - status == "done"
views:
  - type: table
    name: Urgent or blocked
    filters:
      or:
        - priority >= 5
        - status == "blocked"
    groupBy:
      property: status
      direction: ASC
    order:
      - file.name
      - status
`)

	write("Everything.base", `filters:
  and:
    - file.inFolder("Live")
views:
  - type: table
    name: Anything in Live
`)

	var err error
	rep, err = Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	return root, rep
}

// TestWrittenViews_LoadBackThroughTheRealLoader is this file's tripwire.
//
// It fails if the writer ever emits a file the reader refuses, which nothing
// else in either package would catch: the import reports success either way,
// and the rejection only appears the next time somebody loads the vault.
func TestWrittenViews_LoadBackThroughTheRealLoader(t *testing.T) {
	root, rep := viewWriterVault(t)

	if rep.ViewReload == nil {
		t.Fatal("the import did not reload the views it wrote, so this assertion measures nothing")
	}
	for _, rej := range rep.ViewReload.Rejections {
		t.Errorf("the importer wrote a view the real loader REJECTS: %s", rej.String())
	}

	schemas, schemaRep, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("loading schemas: %v", err)
	}
	if !schemaRep.OK() {
		t.Fatalf("the importer wrote schemas the real loader rejects: %v", schemaRep.Rejections)
	}
	views, viewRep, err := records.LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("loading views: %v", err)
	}
	for _, rej := range viewRep.Rejections {
		t.Errorf("re-loading through records.LoadViews rejected a produced view: %s", rej.String())
	}
	if views.Len() == 0 {
		t.Fatal("no views loaded at all — a green result over an empty set")
	}

	t.Logf("%d views written and reloaded through the real loader", views.Len())
}

// TestWrittenViews_CarryNoRetiredKeys reads the same rule off the BYTES rather
// than through the loader.
//
// Three keys used to exist and no longer do: `schema_version`, and the flat
// AND-only format's `filters` and `group_by`. The loader refuses all three as
// unknown keys, so a writer that emitted one would be caught by the test above
// — but only where a fixture happens to produce that key. This asserts it over
// every produced file directly, so the coverage does not depend on the fixture
// having the right shape.
//
// It is worth having because a retired key comes back the easy way: a merge of
// any branch cut before the removal re-adds the emitting line as an ordinary,
// conflict-free addition.
func TestWrittenViews_CarryNoRetiredKeys(t *testing.T) {
	root, _ := viewWriterVault(t)

	dir := records.ViewsDir(root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	if len(entries) == 0 {
		t.Fatal("the import wrote no view files")
	}
	for _, e := range entries {
		data, readErr := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // path from this test's own temp dir
		if readErr != nil {
			t.Fatalf("reading %s: %v", e.Name(), readErr)
		}
		var top map[string]any
		if err := yaml.Unmarshal(data, &top); err != nil {
			t.Fatalf("%s is not valid YAML: %v", e.Name(), err)
		}
		for _, retired := range []string{"schema_version", "filters", "group_by"} {
			if _, present := top[retired]; present {
				t.Errorf("%s carries the retired key %q; the view format has no such field, so records.LoadViews refuses the whole file as an unknown key:\n%s", e.Name(), retired, data)
			}
		}
	}
}

// TestWrittenViews_CarryTheFullGrammar is the capability half: the four
// constructs that are the whole reason the format looks the way it does, each
// written into a real file.
//
// Without it, the two tests above would pass with a writer that produced
// well-formed files carrying nothing — a valid file format over the same
// losses, which is the shape of a green number that means nothing.
func TestWrittenViews_CarryTheFullGrammar(t *testing.T) {
	root, rep := viewWriterVault(t)

	byName := map[string]ViewOutcome{}
	for _, b := range rep.Bases {
		for _, v := range b.Views {
			byName[v.DisplayName] = v
		}
	}

	read := func(slug string) map[string]any {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(records.ViewsDir(root), slug+".yaml")) //nolint:gosec // path from this test's own temp dir
		if err != nil {
			t.Fatalf("reading %s: %v", slug, err)
		}
		var top map[string]any
		if err := yaml.Unmarshal(data, &top); err != nil {
			t.Fatalf("%s is not valid YAML: %v", slug, err)
		}
		return top
	}

	t.Run("disjunction becomes any, and multi-clause negation becomes not(all)", func(t *testing.T) {
		vo := byName["Urgent or blocked"]
		if vo.Disabled {
			t.Fatalf("the view is disabled; losses: %v", vo.DisablingLosses)
		}
		if vo.Status != OutcomeConverted {
			t.Errorf("status = %s, want CONVERTED; losses: %v", vo.Status, vo.Losses)
		}
		rendered := renderVerbatim(read("tasks--urgent-or-blocked")["filter"])
		for _, want := range []string{"any:", "not:", "all:", "file.folder", "99-Temp", "priority", "status"} {
			if !strings.Contains(rendered, want) {
				t.Errorf("the written filter does not contain %q — a filter that loses this construct silently returns a different row set:\n%s", want, rendered)
			}
		}
	})

	t.Run("groupBy carries its direction", func(t *testing.T) {
		top := read("tasks--urgent-or-blocked")
		grouping, _ := top["grouping"].([]any)
		if len(grouping) != 1 {
			t.Fatalf("grouping = %v, want one key", top["grouping"])
		}
		g, _ := grouping[0].(map[string]any)
		if g["property"] != "status" || g["direction"] != "asc" {
			t.Errorf("grouping key = %v, want status/asc — a grouping key that loses its direction is silently re-sorted, which is the failure ViewGroupBy exists to end", g)
		}
	})

	t.Run("file.name survives as a display column", func(t *testing.T) {
		props, _ := read("tasks--urgent-or-blocked")["properties"].([]any)
		var found bool
		for _, p := range props {
			if p == "file.name" {
				found = true
			}
		}
		if !found {
			t.Errorf("properties = %v, want file.name among them", props)
		}
	})

	t.Run("a folder-scoped view is written UNTYPED", func(t *testing.T) {
		vo := byName["Anything in Live"]
		if vo.Status == OutcomeRefused {
			t.Fatalf("the folder-scoped view was refused: %s", vo.RefusedReason)
		}
		if vo.ResolvedType != "" {
			t.Errorf("ResolvedType = %q, want empty", vo.ResolvedType)
		}
		top := read("everything--anything-in-live")
		if _, present := top["type"]; present {
			t.Errorf("an untyped view was written with a `type` key: %v", top["type"])
		}
		if rendered := renderVerbatim(top["filter"]); !strings.Contains(rendered, "Live") {
			t.Errorf("the folder scope did not reach the written filter:\n%s", rendered)
		}
	})
}

// TestWrittenViews_UntypedViewIsServableEndToEnd is the reachability check.
//
// A view that loads is not a view anybody can use. The product applies a saved
// view through records.NewViewFindLoader, so the untyped, folder-scoped view
// this importer writes has to come out of that bridge as a real request —
// otherwise the twenty folder-scoped views in the founder's vault are twenty
// files nothing can run.
func TestWrittenViews_UntypedViewIsServableEndToEnd(t *testing.T) {
	root, _ := viewWriterVault(t)

	schemas, _, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("loading schemas: %v", err)
	}
	views, _, err := records.LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("loading views: %v", err)
	}
	loader := records.NewViewFindLoader(views)

	for _, name := range []string{"everything--anything-in-live", "tasks--urgent-or-blocked"} {
		t.Run(name, func(t *testing.T) {
			req, servable := loader.View(name)
			if !servable {
				refusal, _ := loader.ServeRefusal(name)
				t.Fatalf("the view->find bridge will not serve the view this importer wrote: %s", refusal.String())
			}
			if req.Filter == nil {
				t.Error("the served request carries no filter, so the view's own scope was lost between the file and the request")
			}
		})
	}
}
