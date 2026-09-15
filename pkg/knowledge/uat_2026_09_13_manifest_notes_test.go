// Omnipus — regression coverage for the 2026-09-13 UAT finding D-34: the
// index self-report read "index holds 48 of 30 notes on disk" because the
// manifest count included attachments while the on-disk count did not.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestUAT_D34_ManifestNoteCountExcludesAttachments — the figure describe
// compares with the notes on disk must count notes.
func TestUAT_D34_ManifestNoteCountExcludesAttachments(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "a.md", "# A\n")
	b2WriteFile(t, root, "b.md", "# B\n")
	b2WriteFile(t, root, "img/one.png", "png\n")
	b2WriteFile(t, root, "img/two.pdf", "pdf\n")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	m, err := LoadManifest(filepath.Join(filepath.Dir(ix.blevePath), ManifestFileName), ix.root)
	if err != nil {
		t.Fatal(err)
	}
	if m.Len() != 4 {
		t.Fatalf("the manifest records every file: want 4, got %d", m.Len())
	}
	if m.NoteCount() != 2 {
		t.Fatalf("D-34: NoteCount must exclude the two attachments: want 2, got %d", m.NoteCount())
	}

	// The sentence, with one note added on disk since the sweep: the index
	// holds 2 of 3 notes — never "4 of 3".
	sentence := indexFreshness(DescribeData{
		ManifestKnown: true, ManifestCount: m.NoteCount(),
		NotesCounted: true, NotesOnDisk: 3,
	})
	if !strings.Contains(sentence, "2 of 3 notes on disk") {
		t.Fatalf("D-34: expected 'index holds 2 of 3 notes on disk', got %q", sentence)
	}
}
