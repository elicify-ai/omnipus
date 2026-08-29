package knowledge

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestAuthorRefuseReserved_CoversEveryWalkerSkippedName derives its cases from
// scanSkippedDirNames rather than listing names, so the guard and the walker
// cannot drift apart.
//
// They HAD drifted. The guard tested MarkerDirName and ObsidianMarkerDirName —
// two of the set's four — so .git/ and .trash/ were accepted write
// destinations. Every walker skips them, so a note written there vanishes from
// search, backlinks and the orphan check simultaneously: an untracked hard
// delete reachable through an operation named "move".
//
// A table listing ".obsidian" and ".omnipus-vault" would have passed against
// the broken guard, which is exactly what the previous tests did. Enumerating
// the authority instead means a name added to scanSkippedDirNames fails here
// until the guard handles it.
func TestAuthorRefuseReserved_CoversEveryWalkerSkippedName(t *testing.T) {
	names := make([]string, 0, len(scanSkippedDirNames))
	for n := range scanSkippedDirNames {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) < 2 {
		t.Fatalf("precondition: scanSkippedDirNames has %d entries; this test is pointless", len(names))
	}

	for _, n := range names {
		for _, rel := range []string{n + "/Note.md", "projects/" + n + "/deep/Note.md"} {
			if err := authorRefuseReserved(rel); !errors.Is(err, ErrReservedLocation) {
				t.Errorf("authorRefuseReserved(%q) = %v, want ErrReservedLocation — "+
					"%q is skipped by every walker, so a note written there is invisible "+
					"to search, backlinks and the orphan check at once", rel, err, n)
			}
		}
	}

	// The guard must not over-reach: an ordinary dotfile is NOT reserved.
	// DS-3 row 6 requires .hidden.md to be indexed.
	for _, ok := range []string{"notes/.hidden.md", ".hidden.md", "gitignore/Note.md"} {
		if err := authorRefuseReserved(ok); err != nil {
			t.Errorf("authorRefuseReserved(%q) = %v, want nil — only the walker's "+
				"skipped DIRECTORY names are reserved", ok, err)
		}
	}
}

// TestRename_RefusesEveryWalkerSkippedDestination is the behavioural half: the
// same coverage proven through Rename, with the destination directory created
// first so the incidental "destination does not exist" refusal cannot stand in
// for the guard.
func TestRename_RefusesEveryWalkerSkippedDestination(t *testing.T) {
	names := make([]string, 0, len(scanSkippedDirNames))
	for n := range scanSkippedDirNames {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, n := range names {
		t.Run(n, func(t *testing.T) {
			dir, root := a2Collection(t, map[string]string{
				"Keep.md": "# Keep\n\nContent worth not losing.\n",
				"Refs.md": "see [[Keep]]\n",
			})
			r := a2Renamer(t, root)
			if err := os.MkdirAll(filepath.Join(dir, n), 0o755); err != nil {
				t.Fatal(err)
			}

			_, err := r.Rename(RenameRequest{From: "Keep.md", To: n + "/Keep.md"})
			if !errors.Is(err, ErrReservedLocation) {
				t.Fatalf("move into %s/ returned %v, want ErrReservedLocation", n, err)
			}
			if _, serr := os.Stat(filepath.Join(dir, "Keep.md")); serr != nil {
				t.Errorf("source vanished after a REFUSED move into %s/: %v", n, serr)
			}
		})
	}
}
