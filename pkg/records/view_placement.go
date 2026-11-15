// Omnipus — which folder of a knowledge base a saved view is shown in (#1017).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"path"
	"path/filepath"
	"strings"
)

// ViewPlacementRoot is the placement of a view that belongs to the knowledge
// base as a whole: its root folder.
const ViewPlacementRoot = "."

// ViewPlacementProbe answers the two filesystem questions the placement rule
// asks about a folder of the knowledge base, through the CALLER's own
// confinement (the REST listing's library.Root, list_directory's PathHandle).
// rel is slash-separated and relative to the knowledge base root; it is only
// ever a folder the rule already proved syntactically clean.
type ViewPlacementProbe struct {
	// IsDir reports whether rel exists and is a directory.
	IsDir func(rel string) bool
	// IsKnowledgeBase reports whether rel is itself a (nested) knowledge base.
	IsKnowledgeBase func(rel string) bool
}

// ViewPlacement returns the knowledge-base-relative folder a saved view is
// listed in (#1017, design rules L5/L6): the folder of its declared `source`
// when that is a clean relative path whose first segment is not the
// .omnipus-vault storage folder, the folder exists as a directory, and
// neither it nor any folder between it and the knowledge base root is a
// deeper nested knowledge base. Every other view — authored (no source), or a
// source failing any of those conditions — is placed at ViewPlacementRoot.
//
// The .base file named by source does not have to exist: placement follows
// the folder, so deleting the .base does not move the view.
//
// No path built from source is opened here; the probe is asked only about
// the folder, and only once the text has passed every syntactic check.
func ViewPlacement(source string, probe ViewPlacementProbe) string {
	dir, ok := viewSourceDir(source)
	if !ok || dir == ViewPlacementRoot {
		return ViewPlacementRoot
	}
	if !probe.IsDir(dir) {
		return ViewPlacementRoot
	}
	segments := strings.Split(dir, "/")
	for i := range segments {
		if probe.IsKnowledgeBase(strings.Join(segments[:i+1], "/")) {
			return ViewPlacementRoot
		}
	}
	return dir
}

// viewSourceDir is the syntactic half of ViewPlacement: the folder part of a
// declared source, or ok=false when the source cannot name a folder of the
// knowledge base (empty, absolute, a backslash, not in clean form, a ".."
// segment, or inside the .omnipus-vault storage folder).
func viewSourceDir(source string) (string, bool) {
	s := strings.TrimSpace(source)
	if s == "" || strings.Contains(s, `\`) {
		return "", false
	}
	if strings.HasPrefix(s, "/") || filepath.IsAbs(s) || filepath.VolumeName(s) != "" {
		return "", false
	}
	if path.Clean(s) != s {
		return "", false
	}
	segments := strings.Split(s, "/")
	for _, seg := range segments {
		if seg == ".." {
			return "", false
		}
	}
	if segments[0] == VaultMarkerDirName {
		return "", false
	}
	return path.Dir(s), true
}
