package gateway

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// leakedPackageDirFiles lists files the test run left under the package directory
// (the working directory of `go test`) in places no test may write: a test that
// builds a store with an empty home resolves its paths against the working
// directory, and drops files such as entities/agents/<id>.lock into the repo.
// tasks/ is the same failure: an empty agents.defaults.workspace makes
// filepath.Dir("") == ".", and the task-store migrations then create
// tasks/.milestones_migrated in the package directory.
// TestMain fails the run when this is non-empty. These locations are not
// legitimate runtime paths inside the source tree, so they are not gitignored.
func leakedPackageDirFiles() []string {
	var leaked []string
	// Older task migrations wrote the completion marker at the home root
	// (.milestones_migrated), and a test whose home was the package directory
	// committed those files. The current marker lives under tasks/; both
	// shapes are leaks here.
	for _, name := range []string{".milestones_migrated", ".planning_status_migrated"} {
		// Lstat sees the directory entry itself. Stat would treat a dangling
		// symlink as absent.
		if _, err := os.Lstat(name); err == nil {
			leaked = append(leaked, name)
		} else if !errors.Is(err, os.ErrNotExist) {
			leaked = append(leaked, fmt.Sprintf("%s (inspection failed: %v)", name, err))
		}
	}
	for _, rel := range []string{"entities", "sessions", "workspaces", "uploads", "tasks"} {
		_ = filepath.Walk(rel, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				// A missing root is not a leak. Any other inspection failure
				// means this sweep cannot certify the tree is clean.
				if path != rel || !errors.Is(err, os.ErrNotExist) {
					leaked = append(leaked, fmt.Sprintf("%s (inspection failed: %v)", path, err))
				}
				return nil
			}
			if info == nil || info.IsDir() {
				return nil
			}
			leaked = append(leaked, path)
			return nil
		})
	}
	return leaked
}
