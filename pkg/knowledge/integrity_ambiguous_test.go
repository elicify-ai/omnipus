// Omnipus — tests for the ambiguous-note-name integrity category (Issue 10 /
// V1). Owned solely by the E1-lint work so it shares no file with the D3/D4
// describe changes.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// TestIntegrity_AmbiguousNoteNamesAreReported — Issue 10 / V1 (E1-lint).
//
// Two notes that share a basename in different folders make a bare wikilink
// like [[Composio]] ambiguous: NoteIndex resolves it to exactly one of them by
// a tie-break rule, and the other is silently unreachable by that name.
// check_integrity used to say nothing about this. The `ambiguous name` category
// now names the collision and every note that carries the name, so an operator
// can rename or path-qualify.
func TestIntegrity_AmbiguousNoteNamesAreReported(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	// Two notes, same basename, different folders → [[Composio]] is ambiguous.
	write("Vendors/Composio.md", "one\n")
	write("Research/Composio.md", "two\n")
	// A uniquely-named note must NOT be reported.
	write("Vendors/Unique.md", "solo\n")

	report, err := CheckIntegrity(context.Background(), IntegrityOptions{
		FS:             OSLinkFS(),
		Root:           mustCollectionRoot(t, root),
		CollectionName: "workbench",
		Schemas:        records.NewSchemaSet(),
		Store:          &fakePropertyIndex{},
	})
	if err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}

	c := report.Category(CategoryAmbiguousName)
	if c == nil {
		t.Fatalf("no %q category in the report; the closed set never grew", CategoryAmbiguousName)
	}
	// Exactly one colliding name exists (Composio); Unique does not collide.
	if c.Total != 1 || len(c.Findings) != 1 {
		t.Fatalf("expected exactly one ambiguous-name finding; got Total=%d, findings=%d (%v)",
			c.Total, len(c.Findings), c.Findings)
	}
	detail := c.Findings[0].Detail
	// The finding names the ambiguous name and EVERY note that carries it, in a
	// stable order, because the operator must decide which one keeps the name.
	for _, want := range []string{"Composio", "Research/Composio.md", "Vendors/Composio.md"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the ambiguous-name finding must name %q; got: %q", want, detail)
		}
	}
	if strings.Contains(detail, "Unique") {
		t.Errorf("a uniquely-named note must not be reported as ambiguous: %q", detail)
	}
	// The paths are listed in sorted order so the report is deterministic.
	if i, j := strings.Index(detail, "Research/Composio.md"), strings.Index(detail, "Vendors/Composio.md"); i < 0 || j < 0 || i > j {
		t.Errorf("colliding paths must be listed in sorted order; got: %q", detail)
	}

	// And it must reach the compact text a model actually reads.
	text := RenderDescribe(DescribeData{
		Collection: "workbench",
		Schemas:    records.NewSchemaSet(),
		Views:      records.NewViewSet(),
		Integrity:  report,
		Sections:   map[string]bool{},
	})
	if !strings.Contains(text, string(CategoryAmbiguousName)) {
		t.Errorf("the rendered report must carry the %q category:\n%s", CategoryAmbiguousName, text)
	}
	if !strings.Contains(text, "Research/Composio.md") {
		t.Errorf("the rendered report must name the colliding notes:\n%s", text)
	}
}

// TestIntegrity_AmbiguousNameCategoryIsInTheClosedSet asserts the new category
// joined IntegrityCategories (the render-order set) and is NOT a typed category
// — it is a name-uniqueness check over the walk and must run on a build with no
// properties index, unlike duplicate-id / relation / orphan-row.
func TestIntegrity_AmbiguousNameCategoryIsInTheClosedSet(t *testing.T) {
	found := false
	for _, c := range IntegrityCategories {
		if c == CategoryAmbiguousName {
			found = true
		}
	}
	if !found {
		t.Fatalf("CategoryAmbiguousName is not in IntegrityCategories; renderIntegrity's width and the sink will not account for it")
	}
	if typedCategories[CategoryAmbiguousName] {
		t.Fatalf("ambiguous-name must NOT be a typed category: it needs no properties index and must run on every build")
	}
}
