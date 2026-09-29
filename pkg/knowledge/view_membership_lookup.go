// Omnipus — resolve a private Retry receipt without bounded vault discovery.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// FindViewMoveCollection finds an ID only in live knowledge bases granted to
// workspaceID. A receipt in another workspace looks exactly like a missing
// receipt. Enumeration is over private outside-vault records, NOT the bounded
// workspace collection search, so deeply nested knowledge bases remain usable.
// Unreadable or invalid records are skipped with a server warning, never used
// to authorize a path or to turn another workspace's unknown ID into a 500.
// newPaths are collection-relative paths from the validated pending record;
// completed receipts intentionally contain no paths or write authority.
func FindViewMoveCollection(home, workspaceID, id string) (root string, newPaths []string, err error) {
	if id == "" {
		return "", nil, ErrViewMoveRetryNotFound
	}
	scope := ResolveScope(home, workspaceID)
	indexHome := filepath.Join(home, indexHomeSubdir)
	entries, readErr := os.ReadDir(indexHome)
	if errors.Is(readErr, fs.ErrNotExist) {
		return "", nil, ErrViewMoveRetryNotFound
	}
	if readErr != nil {
		return "", nil, fmt.Errorf("knowledge: list private view receipts: %w", readErr)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidateDir := filepath.Join(indexHome, entry.Name())
		recordPath := filepath.Join(candidateDir, ViewMembershipFileName)
		data, fileErr := os.ReadFile(recordPath)
		if errors.Is(fileErr, fs.ErrNotExist) {
			continue
		}
		if fileErr != nil {
			slog.Warn("knowledge: skip unreadable private view receipt", "record", recordPath, "error", fileErr)
			continue
		}
		var candidate ViewMembership
		if decodeErr := json.Unmarshal(data, &candidate); decodeErr != nil {
			slog.Warn("knowledge: skip invalid private view receipt", "record", recordPath, "reason", "invalid JSON")
			continue
		}
		_, pending := candidate.PendingMoves[id]
		_, completed := candidate.CompletedMoves[id]
		if !pending && !completed {
			continue
		}
		// The private record is a hint for locating a root, not a grant.
		// Check the grant before resolving or loading that root, so another
		// workspace's receipt cannot become an existence oracle.
		if !filepath.IsAbs(candidate.Root) {
			slog.Warn("knowledge: skip invalid private view receipt", "record", recordPath, "reason", "non-absolute root")
			continue
		}
		if !scope.Contains(candidate.Root) {
			continue
		}
		realRoot, resolveErr := ResolveCollectionRoot(candidate.Root)
		if errors.Is(resolveErr, fs.ErrNotExist) {
			continue
		}
		if resolveErr != nil {
			slog.Warn("knowledge: skip unresolvable private view receipt", "record", recordPath, "error", resolveErr)
			continue
		}
		if !scope.Contains(realRoot) {
			continue
		}
		expectedDir, dirErr := IndexDirFor(home, realRoot)
		if dirErr != nil {
			return "", nil, dirErr
		}
		if filepath.Clean(candidateDir) != filepath.Clean(expectedDir) {
			slog.Warn("knowledge: skip invalid private view receipt", "record", recordPath, "reason", "root key mismatch")
			continue
		}
		live, detectErr := IsKnowledgeBase(realRoot)
		if errors.Is(detectErr, fs.ErrNotExist) || detectErr == nil && !live {
			continue
		}
		if detectErr != nil {
			return "", nil, fmt.Errorf("knowledge: inspect private view receipt root: %w", detectErr)
		}
		var paths []string
		var matched bool
		var recordErr error
		lockErr := WithViewMembershipLock(home, realRoot, func() error {
			validated, loadErr := LoadViewMembership(home, realRoot)
			if loadErr != nil {
				recordErr = loadErr
				return nil
			}
			if p, ok := validated.PendingMoves[id]; ok {
				for _, member := range p.Members {
					paths = append(paths, member.NewPath)
				}
				matched = true
			} else if _, ok := validated.CompletedMoves[id]; ok {
				matched = true
			}
			return nil
		})
		if lockErr != nil {
			return "", nil, fmt.Errorf("knowledge: validate private view receipt: %w", lockErr)
		}
		if recordErr != nil {
			slog.Warn("knowledge: skip invalid private view receipt", "record", recordPath, "reason", "failed validation under lock")
			continue
		}
		if !matched {
			continue
		}
		if root != "" {
			return "", nil, fmt.Errorf("knowledge: pending move ID appears in multiple authorized collections")
		}
		root, newPaths = realRoot, sortedUnique(paths...)
	}
	if root == "" {
		return "", nil, ErrViewMoveRetryNotFound
	}
	return root, newPaths, nil
}
