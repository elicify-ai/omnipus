// Omnipus — resolve a private Retry receipt without bounded vault discovery.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// FindViewMoveCollection finds an ID only in live knowledge bases granted to
// workspaceID. A receipt in another workspace looks exactly like a missing
// receipt. Enumeration is over private outside-vault records, NOT the bounded
// workspace collection search, so deeply nested knowledge bases remain usable.
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
		data, fileErr := os.ReadFile(filepath.Join(candidateDir, ViewMembershipFileName))
		if errors.Is(fileErr, fs.ErrNotExist) {
			continue
		}
		if fileErr != nil {
			return "", nil, fmt.Errorf("knowledge: read private view receipt: %w", fileErr)
		}
		var candidate ViewMembership
		if decodeErr := json.Unmarshal(data, &candidate); decodeErr != nil {
			return "", nil, fmt.Errorf("knowledge: parse private view receipt: %w", decodeErr)
		}
		_, pending := candidate.PendingMoves[id]
		_, completed := candidate.CompletedMoves[id]
		if !pending && !completed {
			continue
		}
		// The private record is a hint for locating a root, not a grant.
		// Check the grant before resolving or loading that root, so another
		// workspace's receipt cannot become an existence oracle.
		if !filepath.IsAbs(candidate.Root) || !scope.Contains(candidate.Root) {
			continue
		}
		realRoot, resolveErr := ResolveCollectionRoot(candidate.Root)
		if errors.Is(resolveErr, fs.ErrNotExist) {
			continue
		}
		if resolveErr != nil {
			return "", nil, fmt.Errorf("knowledge: resolve private view receipt root: %w", resolveErr)
		}
		if !scope.Contains(realRoot) {
			continue
		}
		expectedDir, dirErr := IndexDirFor(home, realRoot)
		if dirErr != nil {
			return "", nil, dirErr
		}
		if filepath.Clean(candidateDir) != filepath.Clean(expectedDir) {
			return "", nil, fmt.Errorf("knowledge: private view receipt root key mismatch")
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
		loadErr := WithViewMembership(home, realRoot, func(validated *ViewMembership) error {
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
		if loadErr != nil {
			return "", nil, fmt.Errorf("knowledge: validate private view receipt: %w", loadErr)
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
