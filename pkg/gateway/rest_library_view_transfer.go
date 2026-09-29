// Omnipus — refuse Library moves that would strand tracked view membership.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"sort"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/library"
)

type libraryTransferCollection struct {
	root, workspaceRel, sourceRel string
	rootMoves, sourceFolder       bool
}

// libraryTransferCollections includes both the source's enclosing collection
// and collection roots inside a moved folder. A whole collection has an
// absolute-path-keyed outside-vault membership record: moving its root would
// make that record inaccessible even though its relative paths stay the same.
func libraryTransferCollections(root *library.Root, fromRel string) ([]libraryTransferCollection, error) {
	_, dirErr := root.StatDir(fromRel)
	if dirErr != nil && !errors.Is(dirErr, library.ErrNotDir) {
		return nil, dirErr
	}
	collections := make(map[string]libraryTransferCollection)
	add := func(collRel string, moves bool) error {
		abs, err := knowledge.ResolveCollectionRoot(root.HostPath(collRel))
		if err != nil {
			return err
		}
		if existing, found := collections[abs]; found {
			if moves {
				existing.rootMoves = true
				collections[abs] = existing
			}
			return nil
		}
		collections[abs] = libraryTransferCollection{
			root: abs, workspaceRel: collRel, sourceRel: relWithinCollection(collRel, fromRel),
			rootMoves: moves, sourceFolder: dirErr == nil,
		}
		return nil
	}
	// Search the source's ancestors rather than treating a failure to read a
	// marker as "not a collection". A destructive move cannot use the listing
	// UI's deliberately best-effort detection semantics.
	for dir := path.Dir(fromRel); ; dir = path.Dir(dir) {
		if dir == "." {
			dir = ""
		}
		isKB, established := detectKnowledgeBaseInRoot(root, dir)
		if !established {
			return nil, fmt.Errorf("library: cannot establish knowledge-base status of %q", dir)
		}
		if isKB {
			if err := add(dir, false); err != nil {
				return nil, err
			}
			break
		}
		if dir == "" {
			break
		}
	}
	if dirErr == nil {
		if err := root.WalkDirs(fromRel, func(dir string) error {
			isKB, established := detectKnowledgeBaseInRoot(root, dir)
			if !established {
				return fmt.Errorf("library: cannot establish knowledge-base status of %q", dir)
			}
			if isKB {
				return add(dir, true)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	out := make([]libraryTransferCollection, 0, len(collections))
	for _, collection := range collections {
		out = append(out, collection)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].root < out[j].root })
	return out, nil
}

// moveLibraryWithViewGuard holds every affected membership lock from the
// trusted-record check through the actual filesystem operation. A copy never
// calls this function: it leaves the source record and paths unchanged.
func (a *restAPI) moveLibraryWithViewGuard(
	fromRoot *library.Root, fromRel string, move func() (os.FileInfo, error),
) (os.FileInfo, error) {
	collections, err := libraryTransferCollections(fromRoot, fromRel)
	if err != nil {
		return nil, err
	}
	var info os.FileInfo
	var lockedMove func(int) error
	lockedMove = func(i int) error {
		if i == len(collections) {
			var moveErr error
			info, moveErr = move()
			return moveErr
		}
		col := collections[i]
		return knowledge.WithViewMembership(a.homePath, col.root, func(m *knowledge.ViewMembership) error {
			if col.rootMoves {
				if err := m.RefuseTrackedRootMove(); err != nil {
					return prefixTrackedTransferError(err, col.workspaceRel)
				}
			} else if err := m.RefuseTrackedTransfer(col.sourceRel, col.sourceFolder); err != nil {
				return prefixTrackedTransferError(err, col.workspaceRel)
			}
			return lockedMove(i + 1)
		})
	}
	return info, lockedMove(0)
}

func prefixTrackedTransferError(err error, collRel string) error {
	var tracked *knowledge.TrackedViewTransferError
	if !errors.As(err, &tracked) {
		return err
	}
	paths := make([]string, 0, len(tracked.Paths))
	for _, rel := range tracked.Paths {
		paths = append(paths, path.Join(collRel, rel))
	}
	return &knowledge.TrackedViewTransferError{Paths: paths, PendingMoveIDs: tracked.PendingMoveIDs}
}

// mapTrackedLibraryTransferErr uses the existing contracted error response
// until the architect-approved structured refusal schema is regenerated.
func mapTrackedLibraryTransferErr(w http.ResponseWriter, err error) bool {
	var tracked *knowledge.TrackedViewTransferError
	if !errors.As(err, &tracked) {
		return false
	}
	jsonErr(w, http.StatusConflict, tracked.Error())
	return true
}
