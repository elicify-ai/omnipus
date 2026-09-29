// Omnipus — map verified Retry paths into a workspace's Library namespace.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// MapViewRetryLibraryPaths maps the trusted, collection-relative view paths
// to workspace-relative Library paths. It returns no partial list if a grant
// or a path cannot be mapped safely. REST refuses before mutation on an error;
// the agent can still report the outcome and ID without guessing any paths.
func MapViewRetryLibraryPaths(home, workspaceID, collectionRoot string, relPaths []string) ([]string, error) {
	if len(relPaths) == 0 {
		return []string{}, nil
	}
	root, err := library.OpenRoot(home, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("knowledge: open Library root for Retry: %w", err)
	}
	defer root.Close()
	collectionRel, err := retryCollectionLibraryPath(home, workspaceID, collectionRoot, root)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(relPaths))
	for _, rel := range relPaths {
		if !validMembershipPath(rel, ".view") {
			return nil, fmt.Errorf("knowledge: invalid view path in Retry receipt")
		}
		virtual := path.Join(collectionRel, rel)
		clean, cleanErr := library.CleanRelPath(virtual)
		if cleanErr != nil || clean != virtual || virtual == "" {
			return nil, fmt.Errorf("knowledge: Retry view path has no safe Library representation")
		}
		paths = append(paths, virtual)
	}
	sort.Strings(paths)
	return paths, nil
}

// retryCollectionLibraryPath requires the virtual Library directory to resolve
// through a current work-tree or mount grant to the exact real collection.
// Bounded collection discovery cannot locate arbitrarily deep knowledge bases.
func retryCollectionLibraryPath(home, workspaceID, collectionRoot string, root *library.Root) (string, error) {
	workDir, err := workspace.SafeWorkDir(home, workspaceID)
	if err != nil {
		return "", err
	}
	if rel, ok, err := retryCollectionPathUnder(root, workDir, "", collectionRoot); err != nil {
		return "", err
	} else if ok {
		return rel, nil
	}
	mounts, ok := workspace.LoadMounts(home, workspaceID)
	if !ok {
		return "", fmt.Errorf("knowledge: cannot read current workspace mount grants for Retry")
	}
	sort.Slice(mounts, func(i, j int) bool { return mounts[i].Name < mounts[j].Name })
	for _, mount := range mounts {
		name, target, _, mounted := root.MountAt(mount.Name)
		if !mounted || name != mount.Name || target != mount.HostPath {
			return "", fmt.Errorf("knowledge: workspace mount grants changed during Retry")
		}
		if rel, found, err := retryCollectionPathUnder(root, mount.HostPath, mount.Name, collectionRoot); err != nil {
			return "", err
		} else if found {
			return rel, nil
		}
	}
	return "", fmt.Errorf("knowledge: Retry collection cannot be mapped to a granted Library path")
}

func retryCollectionPathUnder(root *library.Root, hostPrefix, virtualPrefix, realRoot string) (string, bool, error) {
	resolvedPrefix, err := ResolveCollectionRoot(hostPrefix)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("knowledge: resolve Retry path prefix: %w", err)
	}
	rel, err := filepath.Rel(resolvedPrefix, realRoot)
	if err != nil {
		return "", false, err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false, nil
	}
	virtual := path.Join(virtualPrefix, filepath.ToSlash(rel))
	virtual, err = library.CleanRelPath(virtual)
	if err != nil {
		return "", false, fmt.Errorf("knowledge: Retry path has no safe Library representation: %w", err)
	}
	resolved, err := ResolveCollectionRoot(root.HostPath(virtual))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("knowledge: resolve Retry Library path: %w", err)
	}
	if resolved != realRoot {
		return "", false, nil
	}
	if _, err := root.StatDir(virtual); err != nil {
		return "", false, fmt.Errorf("knowledge: Retry Library path is not confined: %w", err)
	}
	return virtual, true, nil
}
