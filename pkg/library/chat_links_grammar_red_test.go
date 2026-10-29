package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Shared with src/lib/chat-links.grammar.test.ts (TDD rows 1–2, SEC-1, SEC-13).
// Expected links are in the fixture, not copied from a live encoder run.
func TestEncodeLibraryLink_SharedGrammarFixture(t *testing.T) {
	var fixture struct {
		RoundTrip []struct {
			Workspace string `json:"workspace"`
			Path      string `json:"path"`
			Link      string `json:"link"`
		} `json:"roundTrip"`
		RejectPath []struct {
			Workspace string `json:"workspace"`
			Path      string `json:"path"`
		} `json:"rejectPath"`
	}
	raw, err := os.ReadFile(chatLinksGrammarFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.RoundTrip) == 0 || len(fixture.RejectPath) == 0 {
		t.Fatal("shared grammar fixture is empty")
	}
	for _, row := range fixture.RoundTrip {
		cleaned, err := CleanRelPath(row.Path)
		if err != nil || cleaned != row.Path {
			t.Fatalf("CleanRelPath(%q) = %q, %v; a browser-accepted path must pass unchanged (SC-001)", row.Path, cleaned, err)
		}
		got, ok := EncodeLibraryLink(row.Workspace, row.Path)
		if !ok || got != row.Link {
			t.Fatalf("Encode(%s, %s) = %q, %v; want %q (BS-1.8)", row.Workspace, row.Path, got, ok, row.Link)
		}
		ws, rel, parsed := ParseLibraryLink(got)
		if !parsed || ws != row.Workspace || rel != row.Path {
			t.Fatalf("Parse(%q) = %s %s %v; want %s %s (BS-1.8)", got, ws, rel, parsed, row.Workspace, row.Path)
		}
	}
	for _, row := range fixture.RejectPath {
		if _, ok := EncodeLibraryLink(row.Workspace, row.Path); ok {
			t.Fatalf("Encode accepted %q; forbidden characters get no link (SEC-13)", row.Path)
		}
	}
	long := strings.Repeat("a", 255)
	if _, ok := EncodeLibraryLink("W", long); !ok {
		t.Fatal("a 255-character segment must be accepted (BS-1.8)")
	}
	if _, ok := EncodeLibraryLink("W", long+"a"); ok {
		t.Fatal("a 256-character segment must be rejected (BS-1.8)")
	}
}

func chatLinksGrammarFixture(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "tests", "fixtures", "chat-links", "grammar.json")
}
