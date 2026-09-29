package vaultimport

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// loadImportedViews uses the same collection discovery and parse path as the
// application. It intentionally does not copy legacy .yaml output into the
// collection: an importer or re-derivation writer still using the retired
// control-plane directory must fail its behavioral test.
func loadImportedViews(t *testing.T, path string, schemas *records.SchemaSet) (*records.ViewSet, *records.ViewLoadReport, error) {
	t.Helper()
	fsys := knowledge.OSLinkFS()
	root, err := knowledge.NewCollectionRoot(fsys, path)
	if err != nil {
		t.Fatalf("collection root %s: %v", path, err)
	}
	return knowledge.LoadViewsForCollection(fsys, root, schemas)
}
