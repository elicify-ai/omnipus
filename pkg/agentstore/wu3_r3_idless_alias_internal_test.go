package agentstore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/entity"
)

// pr-test-analyzer R2-02: on a case-insensitive volume Get("IDLESS") opens
// idless.json, and the directory lists only the real name "idless.json". This
// test reproduces exactly that on any filesystem — IDLESS.json holds the same
// id-less record so the open succeeds, and the listing shows only the real
// name — so it reaches confirmExactAgentID on Linux CI too. Get must refuse.
func TestWU3GetRefusesCaseAliasOnAnyFilesystem(t *testing.T) {
	home := t.TempDir()
	s := New(home)
	record := []byte(`{"name":"No stored id"}`)
	if err := os.WriteFile(filepath.Join(s.Dir(), "idless.json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
	// What a case-insensitive volume does: the variant spelling opens the
	// same record.
	if err := os.WriteFile(filepath.Join(s.Dir(), "IDLESS.json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
	listDir := readStoreDir
	readStoreDir = func(dir string) ([]fs.DirEntry, error) {
		entries, err := listDir(dir)
		out := entries[:0]
		for _, e := range entries {
			if e.Name() != "IDLESS.json" { // the volume lists only the real name
				out = append(out, e)
			}
		}
		return out, err
	}
	t.Cleanup(func() { readStoreDir = listDir })

	if got, err := s.Get("idless"); err != nil || got.ID != "idless" {
		t.Fatalf("control: the exact id reads (got %v, %v)", got, err)
	}
	if _, err := s.Get("IDLESS"); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("a case variant of an id-less record must be not found, got %v", err)
	}
}
