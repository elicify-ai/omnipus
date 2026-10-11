package gateway

import (
	"os"
	"path/filepath"
)

// leakedPackageDirFiles lists files the test run left under the package directory
// (the working directory of `go test`) in places no test may write: a test that
// builds a store with an empty home resolves its paths against the working
// directory, and drops files such as entities/agents/<id>.lock into the repo.
// TestMain fails the run when this is non-empty. These locations are not
// legitimate runtime paths inside the source tree, so they are not gitignored.
func leakedPackageDirFiles() []string {
	var leaked []string
	for _, rel := range []string{"entities", "sessions", "workspaces", "uploads"} {
		_ = filepath.Walk(rel, func(path string, info os.FileInfo, err error) error {
			//nolint:nilerr // an unreadable entry must not abort the sweep: the
			// assertion is "nothing leaked", and aborting would hide the rest.
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			leaked = append(leaked, path)
			return nil
		})
	}
	return leaked
}

// removeStaleLeakedLocks deletes the empty <root>/**/*.lock files (root is "entities") that an
// EARLIER run left in the package directory, and returns what it removed.
//
// The CI worker keeps one persistent checkout and refreshes it with
// `git checkout -f` + `git reset --hard`, neither of which removes untracked
// files, so lock files dropped by a run of an older revision (before the tests
// were fixed to use their own temp home) stay in the tree for ever and the guard
// then blames the CURRENT run for them: the records-no-sqlite gate failed with
// both of its tests passing. Only zero-byte *.lock files under entities/ are
// removed (the one class that leak produced; no tracked file lives there), so
// anything else found by the guard is still reported as a leak, and a lock file
// written by the run itself is not touched here and still fails the guard.
func removeStaleLeakedLocks(root string) []string {
	var removed []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		//nolint:nilerr // an unreadable entry must not abort the sweep; the guard
		// after the run still reports whatever is left.
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".lock" && info.Size() == 0 && os.Remove(path) == nil {
			removed = append(removed, path)
		}
		return nil
	})
	// Drop the now-empty directories (deepest first); a non-empty one stays.
	for _, dir := range []string{filepath.Join(root, "agents"), root} {
		_ = os.Remove(dir)
	}
	return removed
}
