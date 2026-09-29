//go:build !records_no_sqlite && !mipsle

package knowledgefind

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// loadFindViewFixtures feeds explicitly named on-disk test fixtures to the
// production records parser. Collection discovery is tested in pkg/knowledge;
// these tests exercise the view-to-knowledge_find bridge without an import cycle.
func loadFindViewFixtures(t *testing.T, root string, schemas *records.SchemaSet, names ...string) (*records.ViewSet, *records.ViewLoadReport, error) {
	t.Helper()
	files := make([]records.ViewFileBytes, 0, len(names))
	for _, name := range names {
		path := filepath.Join(records.ViewsDir(root), name)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read view fixture %s: %v", path, err)
		}
		files = append(files, records.ViewFileBytes{Path: path, Bytes: body})
	}
	return records.LoadViewPaths(files, schemas)
}
