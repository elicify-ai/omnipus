// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// backup_symbols_repo_sweep_1919_test.go — RED test for issue #1919
// (founder decision P-Q3b B, 2026-11-19): Backup and Restore is removed
// from Settings, Data. This is the "no leftover" tripwire: while any of the
// four frontend symbols (createBackup, fetchBackups, restoreBackup,
// BackupEntry) still exists in a git-tracked file outside CHANGELOG and
// docs history, this test fails and names every site.
//
// Scope decisions, stated so CHECK can audit them:
//   - The sweep is git ls-files, not a directory walk. CI checkouts carry
//     untracked build artefacts (run 44525652746: node_modules.tar, too
//     large to read). Those are not part of the repository and must not
//     fail this test. A tracked file that is too large or unreadable still
//     fails: skipping it would let a leftover hide.
//   - If git is missing or git ls-files fails, the test fails. An empty
//     tracked set fails too. Never a silent pass.
//   - Excluded by the brief itself: CHANGELOG.md (history) and docs/ (docs
//     history). Everything else tracked — production code, test code,
//     contracts, generated trees, scripts, .claude — is in scope.
//   - The #1919 RED test family embeds the banned literals BY DESIGN (this
//     file's own patterns, the contract test's banned-schema list, and the
//     frontend test's doc comment). Those three files are the tripwire,
//     not leftovers.
//   - The four literals are case-sensitive on purpose: BackupCreateResponse
//     and RestoreBackupRequest do not contain any of them and are covered by
//     the contract test instead (contracts_backup_removal_1919_test.go).
package gateway

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRepoHasNoBackupRestoreSymbols_1919(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed — cannot locate this test file to resolve the repo root")
	root, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	require.NoError(t, err, "repo root resolution failed")
	selfRel, err := filepath.Rel(root, thisFile)
	require.NoError(t, err, "self-path resolution failed")
	selfSlash := filepath.ToSlash(selfRel)

	banned := []string{"createBackup", "fetchBackups", "restoreBackup", "BackupEntry"}
	// The #1919 RED test family embeds the banned literals BY DESIGN.
	selfFamily := map[string]bool{
		selfSlash: true,
		"pkg/api/generated/contracts_backup_removal_1919_test.go":          true,
		"src/components/settings/DataSection.backup-removal-1919.test.tsx": true,
	}
	const maxReadBytes int64 = 32 << 20 // 32 MB. A tracked file above this cannot be verified, so it fails the sweep.

	tracked := gitTrackedFiles1919(t, root)
	seenSelf := false
	var hits []string
	for _, rel := range tracked {
		rel = filepath.ToSlash(rel)
		if rel == selfSlash {
			seenSelf = true
		}
		if rel == "CHANGELOG.md" || selfFamily[rel] || rel == "docs" || strings.HasPrefix(rel, "docs/") {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Errorf("issue #1919 sweep: cannot stat tracked file %s: %v", rel, statErr)
			continue
		}
		if info.Size() > maxReadBytes {
			t.Errorf("issue #1919 sweep: tracked file %s is %d bytes, over the %d byte sweep limit — a file this large cannot be cleared", rel, info.Size(), maxReadBytes)
			continue
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Errorf("issue #1919 sweep: cannot read tracked file %s: %v", rel, readErr)
			continue
		}
		lines := strings.Split(string(raw), "\n")
		for i, line := range lines {
			for _, sym := range banned {
				if strings.Contains(line, sym) {
					hits = append(hits, rel+":"+itoa_1919(i+1)+" contains "+sym)
				}
			}
		}
	}
	require.Truef(t, seenSelf, "instrument check: git ls-files did not list %s — the sweep is not looking at this repository", selfSlash)
	require.Emptyf(t, hits,
		"issue #1919: Backup and Restore is removed from Settings, Data — these symbols must not exist in tracked files outside CHANGELOG and docs history. Sites found:\n  %s",
		strings.Join(hits, "\n  "))
}

// gitTrackedFiles1919 lists the files that are part of the repository.
// A failure here fails the test: passing on an unknown or empty file set
// would hide a leftover.
func gitTrackedFiles1919(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "ls-files", "-z")
	out, err := cmd.Output()
	if err != nil {
		detail := ""
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			detail = ": " + string(exitErr.Stderr)
		}
		t.Fatalf("issue #1919 sweep: git ls-files failed (refusing to pass on an unknown file set): %v%s", err, detail)
	}
	var files []string
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" {
			continue
		}
		files = append(files, rel)
	}
	if len(files) == 0 {
		t.Fatal("issue #1919 sweep: git ls-files returned no tracked files (refusing to pass on an empty sweep)")
	}
	return files
}

// itoa_1919 is the same tiny strconv-free helper style routes_uniqueness_test.go
// uses for building failure messages.
func itoa_1919(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 8)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}
