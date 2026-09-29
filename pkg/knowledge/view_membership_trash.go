// Omnipus — revoke recorded view authority before a Library or agent trash.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"path/filepath"
	"strings"
)

// ViewMembershipTrashIncompleteError means revocation may have reached disk,
// but the trash did not finish. Repeating trash is the recovery action; an
// editable derived_from marker is never used to re-enroll membership.
type ViewMembershipTrashIncompleteError struct {
	Source string
	Paths  []string
	Cause  error
}

func (e *ViewMembershipTrashIncompleteError) Error() string {
	return fmt.Sprintf("knowledge: trash_incomplete for %q; released paths: %s; repeat trash to complete: %v",
		e.Source, strings.Join(e.Paths, ", "), e.Cause)
}

func (e *ViewMembershipTrashIncompleteError) Unwrap() error { return e.Cause }

// TrashWithViewMembership holds the enclosing and nested KB membership locks
// through trash. All affected records are revoked before any file is moved;
// even an interrupted or failed trash cannot leave an old path authorized.
func TrashWithViewMembership(home string, tr *Trasher, req TrashRequest) (result *TrashResult, returnErr error) {
	if tr == nil || !tr.Root.Valid() {
		return nil, fmt.Errorf("knowledge: trash requires a configured collection root")
	}
	from := req.Path
	engineCalled := false
	defer func() {
		if returnErr != nil && !engineCalled {
			outcome := "refused"
			var incomplete *ViewMembershipTrashIncompleteError
			if errors.As(returnErr, &incomplete) {
				outcome = "incomplete"
			}
			tr.emit(trashOpTrash, outcome, []string{from}, returnErr.Error())
		}
	}()
	from, err := cleanNoteArg(req.Path)
	if err != nil {
		return nil, err
	}
	if !req.Folder {
		from = tr.trashSourcePath(tr.fs(), from)
	}
	if err := authorRefuseReserved(from); err != nil {
		return nil, err
	}
	abs, err := tr.Root.ResolveContainedNoSymlink(tr.fs(), from)
	if err != nil {
		return nil, err
	}
	if req.Folder {
		info, statErr := tr.fs().Lstat(abs)
		if errors.Is(statErr, fs.ErrNotExist) || statErr == nil && !info.IsDir() {
			return nil, fmt.Errorf("%w: no folder at %q", ErrTrashSourceMissing, from)
		}
		if statErr != nil {
			return nil, statErr
		}
	}
	if req.Folder || strings.EqualFold(path.Ext(from), ".base") || strings.EqualFold(path.Ext(from), ".view") {
		if err := requireExactSpelling(tr.Root, from); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrTrashSourceMissing, err)
		}
	}
	roots, err := viewRenameCollectionRoots(tr.Root, RenameRequest{From: from, Folder: req.Folder})
	if err != nil {
		return nil, err
	}
	err = withLockedViewMembershipRoots(home, roots, func(members map[string]*ViewMembership) error {
		plans := make(map[string][]viewTrashMember, len(roots))
		for _, root := range roots {
			rel, folder := from, req.Folder
			if root != tr.Root.Path() {
				rel, folder = "", true
			}
			planned, planErr := members[root].planTrashRelease(rel, folder)
			if planErr != nil {
				return prefixNestedTrackedMove(planErr, tr.Root.Path(), root)
			}
			plans[root] = planned
		}
		var released bool
		paths := make(map[string]bool)
		incomplete := func(cause error) error {
			if cause == nil {
				return nil
			}
			if !released {
				return cause
			}
			failure := &ViewMembershipTrashIncompleteError{Source: from, Paths: sortedTrackedPaths(paths), Cause: cause}
			slog.Warn("knowledge: trash incomplete after view membership revocation", "path", from,
				"released_paths", failure.Paths, "error", cause)
			return failure
		}
		for _, root := range roots {
			for _, member := range plans[root] {
				prefix := ""
				if root != tr.Root.Path() {
					rel, relErr := filepath.Rel(tr.Root.Path(), root)
					if relErr != nil {
						return incomplete(relErr)
					}
					prefix = filepath.ToSlash(rel)
				}
				paths[path.Join(prefix, member.rel)] = true
			}
			if len(plans[root]) == 0 {
				continue
			}
			// A save can write the record and then fail its chmod, so even a
			// reported save failure may be post-revocation.
			released = true
			if err := members[root].revokeTrashMembers(plans[root]); err != nil {
				return incomplete(err)
			}
		}
		for _, root := range roots {
			if err := members[root].stripTrashMemberMarkers(plans[root]); err != nil {
				return incomplete(err)
			}
		}
		var trashErr error
		engineCalled = true
		result, trashErr = tr.Trash(req)
		return incomplete(trashErr)
	})
	return result, err
}
