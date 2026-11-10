package sandbox

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Binding amendment §2 D2/A5/A6: a synthetic runtime branch is not an
// operator grant. Neither an ancestor override nor a traversal node can
// reopen an inventory subtree. The same expansion serves real Landlock.
func TestExpandRulesExcluding_DeniedSubtreeWinsOverBranchAndTraversal(t *testing.T) {
	for _, branch := range []string{"ancestor", "under_denied_subtree"} {
		t.Run(branch, func(t *testing.T) {
			home := t.TempDir()
			protected := filepath.Join(home, "agents", "self", ".omnipus", "private.txt")
			if err := os.MkdirAll(filepath.Dir(protected), 0o700); err != nil {
				t.Fatal(err)
			}
			control := filepath.Join(home, "allowed.txt")
			for _, path := range []string{control, protected} {
				if err := os.WriteFile(path, []byte("sentinel"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			root := home
			if branch == "under_denied_subtree" {
				root = filepath.Join(home, "agents", "self")
			}
			access := AccessRead | AccessWrite | AccessExecute
			got, err := ExpandRulesExcluding([]PathRule{{Path: home, Access: access}},
				[]string{filepath.Join(home, "agents")},
				[]string{home, filepath.Join(home, "agents")}, nil,
				[]PathRule{{Path: root, Access: access}})
			if err != nil {
				t.Fatal(err)
			}
			want := []PathRule{{Path: control, Access: access}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("inventory must win without losing the ordinary control: got=%+v want=%+v", got, want)
			}
		})
	}
}
