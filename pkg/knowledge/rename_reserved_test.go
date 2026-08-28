package knowledge

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestRename_RefusesToolStateDirectoriesInBothDirections closes a hard-delete
// path that an operation named "move" should never have had.
//
// .omnipus-vault/ and .obsidian/ are INSIDE the collection root, so
// ResolveContained's FR-043 check holds for them and the move was accepted. The
// destination is in scanSkippedDirNames, which no walker descends, so the note
// vanished from search, backlinks and the orphan check simultaneously — a
// delete with no trash entry, no link accounting and no restore. The reverse
// move was a restore that skipped every check the real restore performs.
//
// CreateNote has always refused these paths (authorRefuseReserved); the move
// path simply never called it.
//
// THE MASK IS WHY THIS NEEDS ITS OWN TEST. Before the trash feature exists the
// move is refused anyway, by an incidental "destination directory does not
// exist" check — so a test written today without MkdirAll passes against the
// UNFIXED code and proves nothing. Trash lives at .omnipus-vault/trash/, so
// implementing it removes the mask. Each case below creates its destination
// directory first, deliberately, to test the guard rather than the accident.
func TestRename_RefusesToolStateDirectoriesInBothDirections(t *testing.T) {
	for _, tc := range []struct {
		name, from, to string
		seed           string // a file to plant before the move, when moving OUT
	}{
		{name: "into .omnipus-vault", from: "Keep.md", to: ".omnipus-vault/trash/Keep.md"},
		{name: "into .obsidian", from: "Keep.md", to: ".obsidian/Keep.md"},
		{name: "into a nested reserved dir", from: "Keep.md", to: "projects/.obsidian/plugins/Keep.md"},
		{name: "out of .omnipus-vault", from: ".omnipus-vault/trash/Buried.md", to: "Restored.md", seed: ".omnipus-vault/trash/Buried.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, root := a2Collection(t, map[string]string{
				"Keep.md":  "# Keep\n\nContent worth not losing.\n",
				"Other.md": "Points at [[Keep]].\n",
			})
			r := a2Renamer(t, root)

			// Remove the incidental mask: make the destination directory real.
			if d := filepath.Dir(tc.to); d != "." {
				if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if tc.seed != "" {
				if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(tc.seed)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, tc.seed), []byte("# Buried\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			_, err := r.Rename(RenameRequest{From: tc.from, To: tc.to})
			if err == nil {
				t.Fatalf("move %q -> %q was ACCEPTED; it crosses a tool-state boundary "+
					"and must be refused", tc.from, tc.to)
			}
			if !errors.Is(err, ErrReservedLocation) {
				t.Errorf("refused with %v, want ErrReservedLocation — a refusal for the "+
					"wrong reason (e.g. a missing directory) disappears as soon as that "+
					"directory exists, which trash makes true", err)
			}
			// The source must be untouched by a refused move.
			if tc.seed == "" {
				if _, serr := os.Stat(filepath.Join(dir, tc.from)); serr != nil {
					t.Errorf("source %q went missing after a REFUSED move: %v", tc.from, serr)
				}
			}
		})
	}
}
