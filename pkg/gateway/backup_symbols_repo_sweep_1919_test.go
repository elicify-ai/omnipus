// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// backup_symbols_repo_sweep_1919_test.go — RED test for issue #1919
// (founder decision P-Q3b B, 2026-11-19): Backup and Restore is removed
// from Settings, Data. This is the "no leftover" tripwire: while any of the
// four frontend symbols (createBackup, fetchBackups, restoreBackup,
// BackupEntry) still exists ANYWHERE in the repository outside CHANGELOG
// and docs history, this test fails and names every site.
//
// Scope decisions, stated so CHECK can audit them:
//   - Excluded: .git, node_modules, dist, pkg/gateway/spa (build outputs,
//     gitignored or vendored build trees — the durable truth is the source
//     that generates them), .gitnexus, coverage.
//   - Excluded by the brief itself: CHANGELOG.md (history) and docs/ (docs
//     history). Everything else — production code, test code, contracts,
//     generated trees, scripts, .claude — is in scope.
//   - The four literals are case-sensitive on purpose: BackupCreateResponse
//     and RestoreBackupRequest do not contain any of them and are covered by
//     the contract test instead (contracts_backup_removal_1919_test.go).
package gateway

import (
	"fmt"
	"io/fs"
	"os"
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

	banned := []string{"createBackup", "fetchBackups", "restoreBackup", "BackupEntry"}
	skipDirNames := map[string]bool{
		".git": true, "node_modules": true, "dist": true,
		"spa": true, ".gitnexus": true, "coverage": true,
	}
	// The #1919 RED test family embeds the banned literals BY DESIGN — the
	// sweep's own patterns, the contract test's banned-schema list, and the
	// frontend test's doc comment naming the APIs it leaves unmocked. These
	// three files ARE the tripwire, not leftovers; excluding them here is
	// what lets every OTHER occurrence stay loud. (Caught by the first RED
	// run: the sweep flagged its own siblings before this exclusion existed.)
	selfFamily := map[string]bool{
		filepath.ToSlash(selfRel):                                          true,
		"pkg/api/generated/contracts_backup_removal_1919_test.go":          true,
		"src/components/settings/DataSection.backup-removal-1919.test.tsx": true,
	}
	const maxReadBytes = 32 << 20 // 32 MB — far above any generated source here; the SPA build tree is excluded by name

	var hits []string
	// A walk or path error must fail the test. Returning the error (never
	// nil) is what keeps that failure loud: WalkDir stops and
	// require.NoError below names the path. Swallowing it and returning
	// nil is what nilerr rejects, and it would also let a later empty
	// hit list look like a clean sweep.
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("issue #1919 sweep: cannot walk %s: %w", path, err)
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return fmt.Errorf("issue #1919 sweep: cannot resolve %s: %w", path, relErr)
		}
		if d.IsDir() {
			if skipDirNames[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == "CHANGELOG.md" || selfFamily[filepath.ToSlash(rel)] || strings.HasPrefix(rel, "docs"+string(filepath.Separator)) {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return fmt.Errorf("issue #1919 sweep: cannot stat %s: %w", rel, statErr)
		}
		if info.Size() > maxReadBytes {
			hits = append(hits, rel+": UNVERIFIABLE (too large) — widen the sweep before trusting a pass")
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("issue #1919 sweep: cannot read %s: %w", rel, readErr)
		}
		lines := strings.Split(string(raw), "\n")
		for i, line := range lines {
			for _, sym := range banned {
				if strings.Contains(line, sym) {
					hits = append(hits, rel+":"+itoa_1919(i+1)+" contains "+sym)
				}
			}
		}
		return nil
	})
	require.NoError(t, walkErr, "repo walk failed")

	require.Emptyf(t, hits,
		"issue #1919: Backup and Restore is removed from Settings, Data — these symbols must not exist outside CHANGELOG and docs history. Sites found:\n  %s",
		strings.Join(hits, "\n  "))
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
