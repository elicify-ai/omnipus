// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"os"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// mediaStoreForWorkspace builds a media.MediaStore wired with a
// WorkspaceLibraryProvider that resolves through this restAPI's own
// agentLoop.GetWorkspaceLibrary — mirroring how the production gateway
// wires the agent-loop-wide media store at boot (this lightweight test
// harness, newTestRestAPI, does not do that wiring itself).
func mediaStoreForWorkspace(api *restAPI) media.MediaStore {
	store := media.NewFileMediaStore()
	store.SetWorkspaceLibraryProvider(func(id string) (media.WorkspaceLibraryResolver, error) {
		lib := api.agentLoop.GetWorkspaceLibrary(id)
		if lib == nil {
			return nil, os.ErrNotExist
		}
		return lib, nil
	})
	return store
}

// compile-time check that session.Attachment's field names match what
// buildTranscriptAttachments assumes (documentation-as-test — a rename would
// fail this file to compile, not just a runtime assertion).
var _ = session.Attachment{Type: "", Path: "", Size: 0, MIMEType: ""}
