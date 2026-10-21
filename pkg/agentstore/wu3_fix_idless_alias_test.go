package agentstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/entity"
)

// security-lead W1: a record that stores no id gets the requested id filled
// in by the loader, so on a case-insensitive volume "IDLESS" read
// idless.json and became an agent named by the alias. Get must accept only
// the record's exact file name for an id-less record, like ReadState does.
func TestWU3GetRefusesCaseAliasOfIDlessRecord(t *testing.T) {
	home := t.TempDir()
	s := New(home)
	if err := os.MkdirAll(s.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), "idless.json"), []byte(`{"name":"No stored id"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Control: the exact id reads, and gets its id from the file name.
	got, err := s.Get("idless")
	if err != nil {
		t.Fatalf("exact id must read: %v", err)
	}
	if got.ID != "idless" {
		t.Fatalf("id = %q, want idless", got.ID)
	}
	_, statErr := os.Stat(filepath.Join(s.Dir(), "IDLESS.json"))
	t.Logf("volume aliases letter case: %v", statErr == nil)
	if _, err := s.Get("IDLESS"); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("a case variant of an id-less record must be not found, got %v", err)
	}
}
