package agentstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/entity"
)

// Chief ruling 2026-10-14: agent ids match exactly, whatever the filesystem.
// A record whose stored id differs from the id asked for is not that agent —
// the same refusal a case-insensitive filesystem (macOS) needs for "OMNIPUS"
// resolving to omnipus.json.
func TestWU3ExactAgentID_StoredIDMustMatch(t *testing.T) {
	home := t.TempDir()
	s := New(home)
	if _, err := s.CreateState("omnipus", &config.AgentConfig{ID: "omnipus", Name: "Omnipus"}, "soul"); err != nil {
		t.Fatal(err)
	}
	// Control: the exact id reads.
	if _, err := s.ReadState("omnipus"); err != nil {
		t.Fatalf("exact id must read: %v", err)
	}
	// A record file that claims another id (what a case alias looks like to
	// the store on a case-insensitive filesystem).
	raw, err := os.ReadFile(filepath.Join(s.Dir(), "omnipus.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), "alias.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, read := range map[string]func() error{
		"ReadState":    func() error { _, e := s.ReadState("alias"); return e },
		"PeekRevision": func() error { _, e := s.PeekRevision("alias"); return e },
		"Get":          func() error { _, e := s.Get("alias"); return e },
		"MutateState": func() error {
			_, e := s.MutateState("alias", "0000000000000000000000000000000000000000000000000000000000000000", nil, nil)
			return e
		},
		"case variant": func() error { _, e := s.ReadState("OMNIPUS"); return e },
	} {
		if err := read(); !errors.Is(err, entity.ErrNotFound) {
			t.Errorf("%s: want entity.ErrNotFound, got %v", name, err)
		}
	}
}
