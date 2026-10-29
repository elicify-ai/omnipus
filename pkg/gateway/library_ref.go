// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/media"
)

// libraryRefFor is the one function that fills MediaPart.library_ref
// (chat-links FR-021): the Library address of a sent file's local path in the
// chat's own workspace, for workspace and mount files of every media type.
// It returns nil — and the field is omitted — when the workspace is unknown,
// the path resolves outside that workspace's Library (an agent's private
// file, a screenshot in the media store, a symlink out of the work tree), the
// path is the Library root (the server never emits an empty path), or the
// name is one the SPA would refuse to render (the link encoder's rules).
//
// home is the gateway's $OMNIPUS_HOME (WSHandler.home / the REST home path);
// "" falls back to config.OmnipusHomeDir().
func libraryRefFor(home, localPath, workspaceID string) *generated.LibraryRef {
	if localPath == "" || workspaceID == "" {
		return nil
	}
	if home == "" {
		home = config.OmnipusHomeDir()
	}
	ref := library.ResolveHostPath(library.ResolveScope{
		Home:        home,
		WorkspaceID: workspaceID,
	}, localPath)
	if !ref.InLibrary() || ref.WorkspaceID != workspaceID || ref.Rel == "" {
		return nil
	}
	if _, ok := library.EncodeLibraryLink(workspaceID, ref.Rel); !ok {
		return nil
	}
	return &generated.LibraryRef{WorkspaceId: workspaceID, Path: ref.Rel}
}

// libraryRefForMedia resolves a media ref to its local path and maps it with
// libraryRefFor. A store that cannot resolve the ref yields no field.
func libraryRefForMedia(store media.MediaStore, home, ref, workspaceID string) *generated.LibraryRef {
	if store == nil || workspaceID == "" {
		return nil
	}
	path, err := store.ResolveWithOpts(ref, media.ResolveOpts{})
	if err != nil {
		return nil
	}
	return libraryRefFor(home, path, workspaceID)
}
