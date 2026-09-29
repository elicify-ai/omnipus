// Omnipus — retry only a previously verified and atomically revoked move.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
)

// RetryViewMembershipMove completes one named, unexpired move from its trusted
// outside-vault receipt. It never reconstructs the revoked identities from a
// .view file's editable derived_from field. A completed ID is a true no-op.
func RetryViewMembershipMove(home string, renamer *Renamer, id string) (*RenameResult, error) {
	if renamer == nil || !renamer.Root.Valid() {
		return nil, fmt.Errorf("knowledge: retry requires a configured collection root")
	}
	var result *RenameResult
	err := WithViewMembership(home, renamer.Root.Path(), func(m *ViewMembership) error {
		pending, completed, lookupErr := m.pendingMove(id)
		if lookupErr != nil {
			return lookupErr
		}
		if completed {
			result = &RenameResult{From: pending.From, To: pending.To, NoOp: true}
			return nil
		}
		req := RenameRequest{From: pending.From, To: pending.To,
			Folder: pending.Folder, AllowAmbiguity: pending.AllowAmbiguity}
		affectedPaths := viewMemberMovedPaths(pending.affectedMembers())
		return withRetryNestedViewMembershipRoots(home, renamer, req, id, func() error {
			// The renamer journal is forward-only. Recovery can finish a rename
			// that was interrupted before membership enrollment; an incomplete
			// journal is a visible failure and never grants authority.
			if _, recoverErr := renamer.RecoverPending(); recoverErr != nil {
				return &ViewMembershipMoveIncompleteError{
					From: pending.From, To: pending.To, Paths: affectedPaths, RetryID: id, Cause: recoverErr}
			}
			landed, stateErr := viewMoveAlreadyLanded(renamer.Root, req)
			if stateErr != nil {
				return fmt.Errorf("%w for %s: %v", ErrViewMoveRetryPreflight, id, stateErr)
			}
			if !landed {
				for _, member := range pending.affectedMembers() {
					if err := m.withManagedViewLock(member.oldRel, func() error {
						_, _, readErr := m.readManagedView(member.oldBase, member.name, member.oldRel)
						return readErr
					}); err != nil {
						return fmt.Errorf("%w for %s: source view %q changed: %v", ErrViewMoveRetryPreflight, id, member.oldRel, err)
					}
				}
				if _, planErr := renamer.Plan(req); planErr != nil {
					return fmt.Errorf("%w for %s: %v", ErrViewMoveRetryPreflight, id, planErr)
				}
				var renameErr error
				result, renameErr = renamer.Rename(req)
				if renameErr != nil {
					return &ViewMembershipMoveIncompleteError{
						From: pending.From, To: pending.To, Paths: affectedPaths, RetryID: id, Cause: renameErr}
				}
			} else {
				result = &RenameResult{From: pending.From, To: pending.To}
			}
			if enrollErr := m.enrollMovedMembers(pending.affectedMembers(), id, pending.From, pending.To); enrollErr != nil {
				return &ViewMembershipMoveIncompleteError{
					From: pending.From, To: pending.To, Paths: affectedPaths, RetryID: id, Cause: enrollErr}
			}
			return nil
		})
	})
	var incomplete *ViewMembershipMoveIncompleteError
	if errors.As(err, &incomplete) {
		slog.Warn("knowledge: view membership retry incomplete", "id", id,
			"from", incomplete.From, "to", incomplete.To, "error", incomplete.Cause)
	}
	return result, err
}

// viewMoveAlreadyLanded distinguishes an untouched request from a completed
// filesystem rename. An ambiguous or missing pair refuses before markers or
// membership are changed; a file's editable content cannot answer this.
func viewMoveAlreadyLanded(root CollectionRoot, req RenameRequest) (bool, error) {
	from, err := root.ResolveContainedNoSymlink(OSLinkFS(), req.From)
	if err != nil {
		return false, err
	}
	to, err := root.ResolveContainedNoSymlink(OSLinkFS(), req.To)
	if err != nil {
		return false, err
	}
	fromInfo, fromErr := os.Lstat(from)
	toInfo, toErr := os.Lstat(to)
	if fromErr != nil && !errors.Is(fromErr, fs.ErrNotExist) {
		return false, fromErr
	}
	if toErr != nil && !errors.Is(toErr, fs.ErrNotExist) {
		return false, toErr
	}
	if fromErr == nil && toErr == nil {
		return false, fmt.Errorf("both source %q and destination %q exist", req.From, req.To)
	}
	if fromErr != nil && toErr != nil {
		return false, fmt.Errorf("neither source %q nor destination %q exists", req.From, req.To)
	}
	if toErr == nil {
		if toInfo.IsDir() != req.Folder {
			return false, fmt.Errorf("destination %q changed type", req.To)
		}
		return true, nil
	}
	if fromInfo.IsDir() != req.Folder {
		return false, fmt.Errorf("source %q changed type", req.From)
	}
	return false, nil
}
