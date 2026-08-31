// Omnipus — the version-2 WRITER: the guard that the version stamped on a
// produced view file and the KEYS in it are the same version, plus the
// version-2 constructs the importer now carries instead of losing.
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
// records.SupportedViewVersion says which schema_version a writer STAMPS on a
// file. The version partition in pkg/records/view.go then refuses any file
// whose KEYS belong to the other version. So the constant and this package's
// writer are one decision with two halves, and moving either half alone
// produces view files that are written successfully, reported as converted,
// and REJECTED on the very next load — silently, until somebody re-runs an
// import.
//
// pkg/records cannot test the other half (it must not import the importer), so
// the whole-loop assertion lives here: import a vault, then reload every file
// through records.ParseView and records.ValidateViewAgainstSchemas — the real
// loader, the same one the running product uses.
// ---------------------------------------------------------------------------

// v2WriterVault writes a small vault exercising the constructs version 2 added
// — disjunction, multi-clause negation, a folder scope, a directional
// grouping, an untyped view — imports it, and returns the vault root and the
// report.
func v2WriterVault(t *testing.T) (root string, rep *Report) {
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

// TestWrittenViews_LoadBackThroughTheRealLoader is the tripwire named in
// records.SupportedViewVersion's own comment.
//
// It fails if the writer's keys and the stamped version ever disagree — which
// is exactly what bumping the constant without changing the writer (or the
// reverse) produces, and which nothing else in either package would catch: the
// import reports success either way, and the rejection only appears the next
// time somebody loads the vault.
func TestWrittenViews_LoadBackThroughTheRealLoader(t *testing.T) {
	root, rep := v2WriterVault(t)

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

	// Every loaded view must declare the version the writer stamps, or the
	// two halves have drifted apart in the one direction the loader cannot
	// see (a file that happens to parse under both).
	for _, v := range views.Views() {
		if v.Def.SchemaVersion != records.SupportedViewVersion {
			t.Errorf("view %q declares schema_version %d; the writer's constant is %d", v.Name(), v.Def.SchemaVersion, records.SupportedViewVersion)
		}
	}
	t.Logf("%d views written and reloaded at schema_version %d", views.Len(), records.SupportedViewVersion)
}

// TestWrittenViews_UseVersion2KeysOnly is the other side of the partition, read
// off the bytes rather than through the loader.
//
// The loader refuses a v2 file carrying `filters:` or `group_by:`, so a writer
// that emitted one would be caught above — but only where a fixture happens to
// produce that key. This asserts it over every produced file directly, so the
// coverage does not depend on the fixture having the right shape.
func TestWrittenViews_UseVersion2KeysOnly(t *testing.T) {
	root, _ := v2WriterVault(t)

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
		if top["schema_version"] != records.ViewVersion2 {
			t.Errorf("%s stamps schema_version %v, want %d", e.Name(), top["schema_version"], records.ViewVersion2)
		}
		for _, v1Key := range []string{"filters", "group_by"} {
			if _, present := top[v1Key]; present {
				t.Errorf("%s carries the VERSION-1 key %q on a version-2 file; records.LoadViews refuses that mixture outright:\n%s", e.Name(), v1Key, data)
			}
		}
	}
}

// TestWrittenViews_CarryTheV2Constructs is the capability half: the four
// things version 1 could not express, each written into a real file.
//
// Without it, the two tests above would pass with a writer that emitted
// version 2 and carried nothing new — a correct file format over the same
// losses, which is the shape of a green number that means nothing.
func TestWrittenViews_CarryTheV2Constructs(t *testing.T) {
	root, rep := v2WriterVault(t)

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
				t.Errorf("the written filter does not contain %q — version 1 lost this construct whole:\n%s", want, rendered)
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
			t.Errorf("grouping key = %v, want status/asc — version 1's group_by had no direction field at all", g)
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
// this importer now writes has to come out of that bridge as a real request —
// otherwise the twenty views the version bump "unlocked" are twenty files
// nothing can run.
func TestWrittenViews_UntypedViewIsServableEndToEnd(t *testing.T) {
	root, _ := v2WriterVault(t)

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
