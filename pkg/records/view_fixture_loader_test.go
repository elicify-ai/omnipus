package records

import (
	"os"
	"path/filepath"
	"testing"
)

// loadViewFixtures reads only the fixture files explicitly named by a parser
// test. It does not perform collection discovery: discovery belongs to
// pkg/knowledge, while these tests exercise records.LoadViewPaths.
func loadViewFixtures(t *testing.T, root string, schemas *SchemaSet, names ...string) (*ViewSet, *ViewLoadReport, error) {
	t.Helper()
	files := make([]ViewFileBytes, 0, len(names))
	for _, name := range names {
		path := filepath.Join(ViewsDir(root), name)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read view fixture %s: %v", path, err)
		}
		files = append(files, ViewFileBytes{Path: path, Bytes: body})
	}
	return LoadViewPaths(files, schemas)
}
