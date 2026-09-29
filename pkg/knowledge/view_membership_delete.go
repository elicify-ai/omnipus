// Omnipus — remove stale authority when a whole knowledge base is deleted.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ViewMembershipRecordDeleteError makes a failed cleanup (or a deletion that
// failed after cleanup) visible. Retrying the delete is safe; a recreated KB
// must never inherit a previous occupant's record or pending move.
type ViewMembershipRecordDeleteError struct {
	Roots []string
	Cause error
}

func (e *ViewMembershipRecordDeleteError) Error() string {
	return fmt.Sprintf("knowledge: knowledge-base deletion incomplete; membership cleanup affected %s: %v; repeat the delete if the folder remains",
		strings.Join(e.Roots, ", "), e.Cause)
}

func (e *ViewMembershipRecordDeleteError) Unwrap() error { return e.Cause }

// DeleteWithViewMembershipCleanup removes each affected collection's outside-
// vault membership record, including pending plans and completed receipts,
// before a permanent Library delete. It holds the exact-key locks in sorted
// absolute-root order until the delete finishes.
func DeleteWithViewMembershipCleanup(home string, roots []string, deleteFolder func() error) error {
	if len(roots) == 0 {
		return deleteFolder()
	}
	ordered := append([]string(nil), roots...)
	sort.Strings(ordered)
	var removed []string
	var locked func(int) error
	locked = func(i int) error {
		if i < len(ordered) {
			root := ordered[i]
			if i > 0 && root == ordered[i-1] {
				return locked(i + 1)
			}
			return WithViewMembershipLock(home, root, func() error { return locked(i + 1) })
		}
		for _, root := range ordered {
			dir, err := IndexDirFor(home, root)
			if err != nil {
				return &ViewMembershipRecordDeleteError{Roots: ordered, Cause: err}
			}
			file := filepath.Join(dir, ViewMembershipFileName)
			if err := os.Remove(file); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return &ViewMembershipRecordDeleteError{Roots: ordered,
					Cause: fmt.Errorf("remove membership record %q: %w", file, err)}
			}
			removed = append(removed, root)
		}
		if err := deleteFolder(); err != nil && len(removed) > 0 {
			return &ViewMembershipRecordDeleteError{Roots: removed, Cause: err}
		} else {
			return err
		}
	}
	return locked(0)
}
