// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runsDirEntries splits a task's runs directory into its day files
// (YYYY-MM-DD.jsonl) and their sidecar lock files (YYYY-MM-DD.jsonl.lock),
// failing the test on any other entry.
func runsDirEntries(t *testing.T, dir string) (dayFiles, lockFiles []string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		switch name := e.Name(); {
		case strings.HasSuffix(name, ".jsonl"):
			dayFiles = append(dayFiles, name)
		case strings.HasSuffix(name, ".jsonl.lock"):
			lockFiles = append(lockFiles, name)
		default:
			t.Errorf("unexpected entry %q in runs dir %s", name, dir)
		}
	}
	return dayFiles, lockFiles
}

// requireRunsDir asserts that a task's runs directory holds exactly
// wantDayFiles and nothing else but their lock files. Every append takes its
// lock on the day file's sidecar (fileutil.SidecarLockPath), so each day file
// still on disk has one, and PruneRuns removes it with the day file, so a
// pruned day must not leave one behind. WithFlock takes no lock on Windows and
// creates no sidecar there.
func requireRunsDir(t *testing.T, dir string, wantDayFiles []string, msgAndArgs ...any) {
	t.Helper()
	dayFiles, lockFiles := runsDirEntries(t, dir)
	assert.ElementsMatch(t, wantDayFiles, dayFiles, msgAndArgs...)
	var wantLockFiles []string
	if runtime.GOOS != "windows" {
		for _, name := range wantDayFiles {
			wantLockFiles = append(wantLockFiles, name+".lock")
		}
	}
	assert.ElementsMatch(t, wantLockFiles, lockFiles,
		"each day file still on disk keeps its lock file, and a pruned day file must not leave one behind")
}
