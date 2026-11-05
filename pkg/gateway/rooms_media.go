// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rooms_media.go: the group chat's files and invitation summary over the
// gateway (#1449). gatewayRoomDeps implements the room service's optional
// rooms.Media, rooms.Publisher and rooms.Summarizer.

package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	"github.com/elicify-ai/omnipus/pkg/rooms"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

var (
	_ rooms.Media      = (*gatewayRoomDeps)(nil)
	_ rooms.Publisher  = (*gatewayRoomDeps)(nil)
	_ rooms.Summarizer = (*gatewayRoomDeps)(nil)
)

// workspaceLibrary is the process's one library instance for a workspace.
func (d *gatewayRoomDeps) workspaceLibrary(workspaceID string) (*library.Library, error) {
	if d.h == nil || d.h.agentLoop == nil {
		return nil, rooms.ErrFilesUnavailable
	}
	if lib := d.h.agentLoop.GetWorkspaceLibrary(workspaceID); lib != nil {
		return lib, nil
	}
	return nil, fmt.Errorf("workspace %s: %w", workspaceID, rooms.ErrFilesUnavailable)
}

// ResolveAttachment resolves a workspace reference with the resolver behind
// the media route, the caller workspace being the room's (ADR C-I1).
func (d *gatewayRoomDeps) ResolveAttachment(workspaceID, ref string) (generated.MediaPart, error) {
	store := d.h.agentLoop.GetMediaStore()
	if store == nil {
		return generated.MediaPart{}, rooms.ErrFilesUnavailable
	}
	_, meta, err := store.ResolveWithMetaOpts(ref, media.WithCallerWorkspace(workspaceID))
	switch {
	case err == nil:
	case errors.Is(err, media.ErrCrossWorkspaceRef), errors.Is(err, library.ErrWorkspaceMismatch):
		return generated.MediaPart{}, rooms.ErrAttachmentOtherWorkspace
	case errors.Is(err, library.ErrEntryStranded):
		return generated.MediaPart{}, rooms.ErrAttachmentStranded
	case errors.Is(err, media.ErrNotFound), errors.Is(err, library.ErrNotFound):
		return generated.MediaPart{}, rooms.ErrAttachmentMissing
	case errors.Is(err, library.ErrInvalidRef), errors.Is(err, library.ErrInvalidMediaID):
		return generated.MediaPart{}, rooms.ErrAttachmentNotWorkspaceFile
	default:
		return generated.MediaPart{}, err
	}
	return mediaPart(ref, meta.Filename, meta.ContentType), nil
}

// mediaPart is the entry shape the SPA renders (MediaPart): the workspace
// media URL, never a path.
func mediaPart(ref, filename, contentType string) generated.MediaPart {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return generated.MediaPart{
		Type:        agent.DetectAttachmentType(contentType, filename),
		Url:         "/api/v1/media/workspace/" + strings.TrimPrefix(ref, media.WorkspaceRefPrefix),
		Filename:    filename,
		ContentType: contentType,
	}
}

// Retain adds a persistent retention owner (FR-076).
func (d *gatewayRoomDeps) Retain(workspaceID, ref, owner string) error {
	lib, id, err := d.refLibrary(workspaceID, ref)
	if err != nil {
		return err
	}
	if err := lib.AddOwner(id, owner); err != nil {
		if errors.Is(err, library.ErrNotFound) {
			return rooms.ErrAttachmentMissing
		}
		return err
	}
	return nil
}

// Release drops a retention owner.
func (d *gatewayRoomDeps) Release(workspaceID, ref, owner string) error {
	lib, id, err := d.refLibrary(workspaceID, ref)
	if err != nil {
		return err
	}
	return lib.RemoveOwner(id, owner)
}

func (d *gatewayRoomDeps) refLibrary(workspaceID, ref string) (*library.Library, string, error) {
	ws, id, ok := media.ParseWorkspaceRef(ref)
	if !ok || ws != workspaceID {
		return nil, "", rooms.ErrAttachmentNotWorkspaceFile
	}
	lib, err := d.workspaceLibrary(workspaceID)
	return lib, id, err
}

// PublishAnswerFile publishes a staged answer file into both representations
// under its preallocated identities, create-or-adopt (FR-077, FR-078 (2)).
func (d *gatewayRoomDeps) PublishAnswerFile(workspaceID string, f rooms.PublishFile) (generated.MediaPart, string, error) {
	lib, err := d.workspaceLibrary(workspaceID)
	if err != nil {
		return generated.MediaPart{}, "", err
	}
	staged, err := os.Open(f.StagedPath)
	if err != nil {
		return generated.MediaPart{}, "", fmt.Errorf("open staged answer file: %w", err)
	}
	ref, projection, err := lib.PublishWithID(f.MediaID, f.Filename, f.SHA256, staged)
	closeErr := staged.Close()
	if errors.Is(err, library.ErrDeletedByUser) {
		return generated.MediaPart{}, f.LibraryName, rooms.ErrPublishDeleted
	}
	if err != nil {
		return generated.MediaPart{}, "", err
	}
	if closeErr != nil {
		return generated.MediaPart{}, "", closeErr
	}
	data, _, err := lib.Read(f.MediaID)
	if err != nil {
		return generated.MediaPart{}, "", fmt.Errorf("read back the published answer file: %w", err)
	}
	rel, err := writeLibraryFile(d.home, workspaceID, f.LibraryName, data, false)
	if errors.Is(err, errLibraryNameTaken) {
		return generated.MediaPart{}, "", rooms.ErrLibraryNameTaken
	}
	if err != nil {
		return generated.MediaPart{}, "", err
	}
	if err := associateLibraryFile(lib, ref, f.MediaID, rel); err != nil {
		return generated.MediaPart{}, "", err
	}
	mime := ""
	if projection.Mime != nil {
		mime = *projection.Mime
	}
	return mediaPart(ref, f.Filename, mime), f.LibraryName, nil
}

// UnpublishAnswerFile removes a rejected answer's published copies when
// nothing else keeps them (FR-078 (4)).
func (d *gatewayRoomDeps) UnpublishAnswerFile(workspaceID string, f rooms.PublishFile) error {
	lib, err := d.workspaceLibrary(workspaceID)
	if err != nil {
		return err
	}
	entry, err := lib.Get(f.MediaID)
	if errors.Is(err, library.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if lib.OwnerCount(f.MediaID) > 0 || (entry.Refcount != nil && *entry.Refcount > 0) {
		return nil // referenced elsewhere: kept
	}
	rel := ""
	if entry.LibraryPath != nil {
		rel = *entry.LibraryPath
	}
	if _, err := lib.Delete(f.MediaID); err != nil {
		return err
	}
	removeLibraryFile(d.home, workspaceID, rel)
	return nil
}

// SummarizeInvitation writes the invitation summary with the starting
// agent's model (FR-039, FR-040).
func (d *gatewayRoomDeps) SummarizeInvitation(ctx context.Context, req rooms.InvitationRequest) (rooms.InvitationSummary, error) {
	res, err := d.h.agentLoop.SummarizeRoomInvitation(ctx, agent.RoomInvitationRequest{
		WorkspaceID: req.WorkspaceID, OriginSessionID: req.OriginSessionID, AgentID: req.AgentID,
		MaxEntries: req.MaxEntries, MaxBytes: req.MaxBytes,
	})
	return rooms.InvitationSummary{Text: res.Text, Called: res.Called, CostUSD: res.CostUSD, Priced: res.Priced}, err
}

// answerFiles maps the tool's resolved files onto the room's answer files.
func answerFiles(in []tools.ReplyMedia) []rooms.AnswerFile {
	if len(in) == 0 {
		return nil
	}
	out := make([]rooms.AnswerFile, 0, len(in))
	for _, m := range in {
		f := rooms.AnswerFile{Ref: m.Ref, Identity: m.Identity, Filename: m.Filename, Size: m.Size}
		if m.Open != nil {
			open := m.Open
			f.Open = func() (io.ReadCloser, error) { return open() }
		}
		out = append(out, f)
	}
	return out
}
