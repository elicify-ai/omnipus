// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gitevidence

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// ErrNestedRepo is returned by Open when a non-Omnipus git repository is
// found at, or in an ancestor of, the requested repo directory (FR-153's
// "nested-repo detection with a signalled degraded contract" / MIN-6:
// "user repo wins, ours skips, planner/Judge/owner told"). Callers MUST
// treat this as a degraded-but-non-fatal signal, not a fatal error.
var ErrNestedRepo = errors.New("gitevidence: nested user git repository detected")

// markerFileName lives inside .git/ (never in the working tree — it must
// never appear in a boundary commit's write-set) and distinguishes a repo
// this package initialized from a pre-existing user repo that happens to
// occupy the same directory.
const markerFileName = "omnipus-gitevidence.marker"

func markerPath(dir string) string {
	return filepath.Join(dir, ".git", markerFileName)
}

func writeMarker(dir string) error {
	content := fmt.Sprintf("omnipus gitevidence auto-init marker\ninitialized_at=%s\n", time.Now().UTC().Format(time.RFC3339))
	if err := fileutil.WriteFileAtomic(markerPath(dir), []byte(content), 0o600); err != nil {
		return fmt.Errorf("gitevidence: write marker: %w", err)
	}
	return nil
}

func hasMarker(dir string) bool {
	_, err := os.Stat(markerPath(dir))
	return err == nil
}

// hasDotGit reports whether dir has a .git entry (directory OR file — a
// `.git` file, not a directory, is how a git-submodule or a linked
// worktree's pointer is represented).
func hasDotGit(dir string) (bool, error) {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// ancestorHasGit walks strictly upward from dir (starting at its parent,
// never dir itself) looking for a .git entry, stopping at the filesystem
// root. It reports the first ancestor found, if any.
func ancestorHasGit(dir string) (ancestor string, found bool, err error) {
	clean, absErr := filepath.Abs(dir)
	if absErr != nil {
		return "", false, fmt.Errorf("gitevidence: resolve absolute path of %s: %w", dir, absErr)
	}
	parent := filepath.Dir(clean)
	for {
		has, statErr := hasDotGit(parent)
		if statErr != nil {
			return "", false, statErr
		}
		if has {
			return parent, true, nil
		}
		next := filepath.Dir(parent)
		if next == parent {
			// Reached the filesystem root (filepath.Dir is idempotent there).
			return "", false, nil
		}
		parent = next
	}
}
