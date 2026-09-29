// Omnipus — keep all recovered folder journals inside their membership locks.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"fmt"
	"os"
	"slices"
	"sort"
)

// withRetryNestedViewMembershipRoots runs the entire retry, including journal
// recovery, while holding the already-acquired enclosing lock and every nested
// root that a pending folder journal could move. A changed scan or a newly
// tracked nested root refuses before recovery can change any filesystem path.
func withRetryNestedViewMembershipRoots(home string, renamer *Renamer, req RenameRequest, id string, replay func() error) error {
	roots, err := retryReplayRoots(renamer, req)
	if err != nil {
		return fmt.Errorf("%w for %s: %w", ErrViewMoveRetryPreflight, id, err)
	}
	mainRoot := renamer.Root.Path()
	if len(roots) == 0 || roots[0] != mainRoot {
		return fmt.Errorf("%w for %s: enclosing membership lock is not first", ErrViewMoveRetryPreflight, id)
	}
	// The caller already holds mainRoot. Acquiring it again would deadlock;
	// each remaining root is a strict descendant and is taken in sorted order.
	return withLockedViewMembershipRoots(home, roots[1:], func(members map[string]*ViewMembership) error {
		current, scanErr := retryReplayRoots(renamer, req)
		if scanErr != nil {
			return fmt.Errorf("%w for %s: %w", ErrViewMoveRetryPreflight, id, scanErr)
		}
		if !slices.Equal(current, roots) {
			return fmt.Errorf("%w for %s: nested knowledge-base roots changed before retry", ErrViewMoveRetryPreflight, id)
		}
		for _, nested := range roots[1:] {
			if err := members[nested].RefuseTrackedRootMove(); err != nil {
				return fmt.Errorf("%w for %s: nested knowledge base cannot move: %w",
					ErrViewMoveRetryPreflight, id, prefixNestedTrackedMove(err, mainRoot, nested))
			}
		}
		return replay()
	})
}

// retryReplayRoots discovers nested roots under the saved pending request AND
// every pending journal RecoverPending would replay. Journals have no Folder
// field, so their current subject's mode determines whether a folder can move.
// Unreadable or indeterminate journals refuse before ANY recovery runs.
func retryReplayRoots(renamer *Renamer, req RenameRequest) ([]string, error) {
	mainRoot := renamer.Root.Path()
	found := map[string]bool{mainRoot: true}
	add := func(roots []string) {
		for _, root := range roots {
			found[root] = true
		}
	}
	journals, err := renamer.PendingJournals()
	if err != nil {
		return nil, fmt.Errorf("knowledge: inspect pending rename journals: %w", err)
	}
	if req.Folder {
		landed, stateErr := viewMoveAlreadyLanded(renamer.Root, req)
		if stateErr != nil {
			return nil, fmt.Errorf("knowledge: locate pending folder %q: %w", req.From, stateErr)
		}
		current, other := req.From, req.To
		if landed {
			current, other = other, current
		}
		roots, scanErr := viewRenameCollectionRoots(renamer.Root,
			RenameRequest{From: current, To: other, Folder: true})
		if scanErr != nil {
			return nil, scanErr
		}
		add(roots)
	}
	for _, journal := range journals {
		if journal.Root != mainRoot {
			return nil, fmt.Errorf("knowledge: journal %s belongs to a different collection", journal.ID)
		}
		state, stateErr := MoveStateOf(renamer.Root, journal)
		if stateErr != nil {
			return nil, fmt.Errorf("knowledge: inspect pending journal %s move state: %w", journal.ID, stateErr)
		}
		if state != MoveDone && state != MoveNotDone {
			return nil, fmt.Errorf("knowledge: pending journal %s move state is indeterminate", journal.ID)
		}
		current, other := journal.From, journal.To
		if state == MoveDone {
			current, other = other, current
		}
		abs, resolveErr := renamer.Root.ResolveContainedNoSymlink(OSLinkFS(), current)
		if resolveErr != nil {
			return nil, fmt.Errorf("knowledge: inspect pending journal %s subject: %w", journal.ID, resolveErr)
		}
		info, statErr := os.Lstat(abs)
		if statErr != nil {
			return nil, fmt.Errorf("knowledge: inspect pending journal %s subject: %w", journal.ID, statErr)
		}
		if info.Mode().IsRegular() {
			continue
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("knowledge: pending journal %s subject is not a regular file or folder", journal.ID)
		}
		roots, scanErr := viewRenameCollectionRoots(renamer.Root,
			RenameRequest{From: current, To: other, Folder: true})
		if scanErr != nil {
			return nil, fmt.Errorf("knowledge: inspect pending journal %s folder: %w", journal.ID, scanErr)
		}
		add(roots)
	}
	roots := make([]string, 0, len(found))
	for root := range found {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	return roots, nil
}
