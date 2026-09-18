package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRevisionForInstructions_EmptyAndChange(t *testing.T) {
	empty := RevisionForInstructions("")
	if len(empty) != 64 {
		t.Fatalf("empty revision length=%d want 64", len(empty))
	}
	if empty != EmptyRevision() {
		t.Fatalf("empty instructions revision %q should match EmptyRevision %q", empty, EmptyRevision())
	}
	changed := RevisionForInstructions("a")
	if changed == empty {
		t.Fatal("content change must change revision")
	}
	again := RevisionForInstructions("a")
	if changed != again {
		t.Fatal("same bytes must be stable")
	}
}

func TestReadInstructionsForManagement_RejectsOversizedFileWithoutSyntheticRevision(t *testing.T) {
	home := t.TempDir()
	const id = "ws-oversized-management"
	dir := WorkspaceDir(home, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	original := strings.Repeat("x", maxInstructionsBytes+1)
	path := filepath.Join(dir, instructionsFileName)
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	content, err := ReadInstructionsForManagement(home, id)
	if !errors.Is(err, ErrInstructionsTooLarge) {
		t.Fatalf("error=%v want ErrInstructionsTooLarge", err)
	}
	if content != "" {
		t.Fatalf("content length=%d want 0", len(content))
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != original {
		t.Fatal("strict management read changed the oversized file")
	}
}

func TestReadInstructionsForManagement_ReturnsExactMaximumSizedContent(t *testing.T) {
	home := t.TempDir()
	const id = "ws-max-management"
	dir := WorkspaceDir(home, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	original := strings.Repeat("x", maxInstructionsBytes)
	if err := os.WriteFile(filepath.Join(dir, instructionsFileName), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	content, err := ReadInstructionsForManagement(home, id)
	if err != nil {
		t.Fatal(err)
	}
	if content != original {
		t.Fatalf("content differs at exact %d-byte boundary", maxInstructionsBytes)
	}
}
