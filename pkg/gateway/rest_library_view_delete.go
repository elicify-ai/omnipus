// Omnipus — remove outside-vault view authority before a permanent Library delete.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"errors"
	"os"
	"path"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/library"
)

func (a *restAPI) deleteLibraryWithViewCleanup(root *library.Root, rel string) error {
	// Deleting a mount's own entry is refused by Root.Delete. Do not walk
	// through the mount and revoke records belonging to its real folder first.
	if mount, _, _, ok := root.MountAt(rel); ok && rel == mount {
		return root.Delete(rel)
	}
	collections, err := libraryTransferCollections(root, rel)
	if err != nil {
		return err
	}
	var affected []string
	for _, col := range collections {
		if col.rootMoves {
			affected = append(affected, col.root)
		}
	}
	// Deleting the final marker leaves the folder in place but demotes it
	// from a knowledge base. Its old absolute-root record must not authorize
	// views when a marker is later created at the same path.
	demoted, err := demotedViewMembershipRoot(root, rel)
	if err != nil {
		return err
	}
	if demoted != "" {
		affected = append(affected, demoted)
	}
	return knowledge.DeleteWithViewMembershipCleanup(a.homePath, affected,
		func() error { return root.Delete(rel) })
}

// demotedViewMembershipRoot identifies a deletion of the LAST real marker
// directory. Lstat and SameFile distinguish a differently-cased spelling
// of the same entry on case-insensitive hosts from a separate sibling or
// a symlink to the marker on case-sensitive hosts.
func demotedViewMembershipRoot(root *library.Root, rel string) (string, error) {
	name := path.Base(rel)
	marker, other := "", ""
	switch {
	case strings.EqualFold(name, knowledge.MarkerDirName):
		marker, other = knowledge.MarkerDirName, knowledge.ObsidianMarkerDirName
	case strings.EqualFold(name, knowledge.ObsidianMarkerDirName):
		marker, other = knowledge.ObsidianMarkerDirName, knowledge.MarkerDirName
	default:
		return "", nil
	}
	parent := path.Dir(rel)
	if parent == "." {
		parent = ""
	}
	entry, err := root.Lstat(rel)
	if errors.Is(err, library.ErrNotFound) || errors.Is(err, library.ErrNotDir) {
		return "", nil // Root.Delete will return its usual source error.
	}
	if err != nil {
		return "", err
	}
	if !entry.IsDir() {
		return "", nil
	}
	canonical, err := root.Lstat(path.Join(parent, marker))
	if errors.Is(err, library.ErrNotFound) || errors.Is(err, library.ErrNotDir) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !canonical.IsDir() || !os.SameFile(entry, canonical) {
		return "", nil
	}
	remaining, err := root.Lstat(path.Join(parent, other))
	if err == nil {
		if remaining.IsDir() {
			return "", nil
		}
	} else if !errors.Is(err, library.ErrNotFound) && !errors.Is(err, library.ErrNotDir) {
		return "", err // Cannot prove this is the last marker; refuse deletion.
	}
	return knowledge.ResolveCollectionRoot(root.HostPath(parent))
}
