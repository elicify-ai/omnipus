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
