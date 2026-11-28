package main

// Spec #1968 (read-file native PDF) — T28's regenerated-reference GREEN gate
// (F14). FR-9 requires the committed generated reference
// (docs/reference/built-in-tools.md) to stay in sync with the live tool
// descriptions: the moment a tool description changes without re-running
// cmd/docsref, this gate fails and names the first differing line. An
// explicit GREEN gate replaces the "deferred, not covered" state the check
// flagged — the comparison is now executable work, not a manual step.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

func TestCommittedToolReferenceMatchesRegeneration(t *testing.T) {
	doc, err := catalog.ParseDocument(catalog.EmbeddedSnapshot)
	if err != nil {
		t.Fatalf("parse embedded provider catalog: %v", err)
	}
	out := t.TempDir()
	if err = writeReferences(out, doc, staticToolGroups()); err != nil {
		t.Fatalf("regenerate references: %v", err)
	}
	committed, err := os.ReadFile(filepath.Join("..", "..", "docs", "reference", "built-in-tools.md"))
	if err != nil {
		t.Fatalf("read committed docs/reference/built-in-tools.md (run `go run ./cmd/docsref` to regenerate it): %v", err)
	}
	generated, err := os.ReadFile(filepath.Join(out, "built-in-tools.md"))
	if err != nil {
		t.Fatalf("read regenerated reference: %v", err)
	}
	if string(committed) != string(generated) {
		i := 0
		for i < len(committed) && i < len(generated) && committed[i] == generated[i] {
			i++
		}
		line := 1 + strings.Count(string(committed)[:i], "\n")
		t.Fatalf("docs/reference/built-in-tools.md is stale against the live tool descriptions "+
			"(first difference at committed line %d); re-run `go run ./cmd/docsref` and commit the regenerated file.\n"+
			"--- committed around the difference ---\n%s\n--- regenerated around the difference ---\n%s",
			line, referenceExcerpt(committed, i), referenceExcerpt(generated, i))
	}
}

// referenceExcerpt bounds the failure output: a small window around the
// first differing byte, expanded to line boundaries.
func referenceExcerpt(data []byte, at int) string {
	if at > len(data) {
		at = len(data)
	}
	start := at
	for start > 0 && data[start-1] != '\n' && at-start < 200 {
		start--
	}
	end := at
	for end < len(data) && data[end] != '\n' && end-at < 200 {
		end++
	}
	if end < len(data) {
		end++
	}
	if end > len(data) {
		end = len(data)
	}
	return fmt.Sprintf("…%s…", data[start:end])
}
