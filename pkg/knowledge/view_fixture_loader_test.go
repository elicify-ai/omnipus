package knowledge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// loadTestCollectionViews follows the production discovery-and-load path for
// tests that create a real collection on disk. It never supplies hand-selected
// bytes to the parser, so a missing or misplaced .view file remains visible.
func loadTestCollectionViews(t *testing.T, path string, schemas *records.SchemaSet) (*records.ViewSet, *records.ViewLoadReport, error) {
	t.Helper()
	fsys := OSLinkFS()
	root, err := NewCollectionRoot(fsys, path)
	if err != nil {
		t.Fatalf("collection root %s: %v", path, err)
	}
	return LoadViewsForCollection(fsys, root, schemas)
}

// writeCollectionView places a view in the knowledge base's content tree, not
// the retired .omnipus-vault/views directory that discovery intentionally skips.
func writeCollectionView(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create view fixture folder %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write view fixture %s: %v", path, err)
	}
}
