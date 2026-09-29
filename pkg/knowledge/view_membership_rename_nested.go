// Omnipus — guard nested knowledge-base roots during folder renames.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// viewRenameCollectionRoots includes the enclosing root and every nested KB
// root whose absolute path changes when this folder moves. Discovery refuses
// unreadable descendants instead of assuming they contain no tracked roots.
func viewRenameCollectionRoots(root CollectionRoot, req RenameRequest) ([]string, error) {
	roots := []string{root.Path()}
	if !req.Folder || normalizeRel(req.From) == normalizeRel(req.To) {
		return roots, nil
	}
	from := normalizeRel(req.From)
	if _, err := root.ResolveContainedNoSymlink(OSLinkFS(), from); err != nil {
		return nil, err
	}
	collection, err := os.OpenRoot(root.Path())
	if err != nil {
		return nil, err
	}
	source, openErr := collection.OpenRoot(filepath.FromSlash(from))
	closeErr := collection.Close()
	if openErr != nil {
		return nil, errors.Join(openErr, closeErr)
	}
	if closeErr != nil {
		return nil, errors.Join(closeErr, source.Close())
	}
	seen := map[string]bool{root.Path(): true}
	err = fs.WalkDir(source.FS(), ".", func(rel string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		for _, marker := range []string{MarkerDirName, ObsidianMarkerDirName} {
			info, statErr := source.Lstat(path.Join(rel, marker))
			if errors.Is(statErr, fs.ErrNotExist) {
				continue
			}
			if statErr != nil {
				return fmt.Errorf("knowledge: check nested knowledge-base marker %q: %w", path.Join(rel, marker), statErr)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("knowledge: cannot establish nested knowledge-base marker %q through a symlink", path.Join(rel, marker))
			}
			if !info.IsDir() {
				continue
			}
			abs, err := ResolveCollectionRoot(filepath.Join(root.Path(), filepath.FromSlash(from), filepath.FromSlash(rel)))
			if err != nil {
				return err
			}
			if !seen[abs] {
				seen[abs] = true
				roots = append(roots, abs)
			}
			break
		}
		return nil
	})
	err = errors.Join(err, source.Close())
	if err != nil {
		return nil, fmt.Errorf("knowledge: inspect folder for nested knowledge bases: %w", err)
	}
	sort.Strings(roots)
	return roots, nil
}

// withRenameMembershipRoots takes all exact-key membership locks in one stable
// order before invoking the renamer, so a concurrent Library transfer cannot
// invert the enclosing/nested lock order.
func withRenameMembershipRoots(home, mainRoot string, roots []string, fn func(*ViewMembership) error) error {
	return withLockedViewMembershipRoots(home, roots, func(members map[string]*ViewMembership) error {
		for _, root := range roots {
			if root != mainRoot {
				if err := members[root].RefuseTrackedRootMove(); err != nil {
					return prefixNestedTrackedMove(err, mainRoot, root)
				}
			}
		}
		return fn(members[mainRoot])
	})
}

// withLockedViewMembershipRoots shares the absolute-root lock order between
// rename and trash, holding every record through the filesystem mutation.
func withLockedViewMembershipRoots(home string, roots []string, fn func(map[string]*ViewMembership) error) error {
	members := make(map[string]*ViewMembership, len(roots))
	var locked func(int) error
	locked = func(i int) error {
		if i == len(roots) {
			return fn(members)
		}
		root := roots[i]
		return WithViewMembership(home, root, func(m *ViewMembership) error {
			members[root] = m
			return locked(i + 1)
		})
	}
	return locked(0)
}

func prefixNestedTrackedMove(err error, mainRoot, nestedRoot string) error {
	var tracked *TrackedViewTransferError
	if !errors.As(err, &tracked) {
		return err
	}
	rel, relErr := filepath.Rel(mainRoot, nestedRoot)
	if relErr != nil {
		return errors.Join(err, fmt.Errorf("knowledge: locate nested collection %q relative to %q: %w", nestedRoot, mainRoot, relErr))
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.Join(err, fmt.Errorf("knowledge: nested collection %q lies outside %q", nestedRoot, mainRoot))
	}
	paths := make([]string, 0, len(tracked.Paths))
	for _, member := range tracked.Paths {
		paths = append(paths, path.Join(filepath.ToSlash(rel), member))
	}
	return &TrackedViewTransferError{Paths: paths, PendingMoveIDs: tracked.PendingMoveIDs}
}
