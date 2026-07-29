//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
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

// TestBuildTranscriptAttachments_WorkspaceRef is the D2 (library-spec,
// 2026-07-29 UAT) regression test: before the fix, websocket.go built a
// session.TranscriptEntry for every user message but NEVER set Attachments,
// even though the field has existed on TranscriptEntry all along — so a
// later turn (or a DIFFERENT AGENT after a handoff, exactly what happened in
// the UAT: Mia -> Ray) had no durable record of what was uploaded.
//
// This drives a REAL upload through HandleUpload (reusing the D-1 dual-write
// test harness) and then calls buildTranscriptAttachments with the resulting
// ref, exactly the way handleChatMessage does when it persists the user's
// TranscriptEntry — proving the persisted Attachments would carry the
// correct Type/Path/Size/MIMEType for that real, on-disk file.
func TestBuildTranscriptAttachments_WorkspaceRef(t *testing.T) {
	api := newUploadTestAPI(t)
	workspaceID := "ws-attachments"
	fileContent := "hello attachment"
	fileName := "notes.txt"

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	require.NoError(t, w.WriteField("workspace_id", workspaceID))
	fw, err := w.CreateFormFile("file", fileName)
	require.NoError(t, err)
	_, _ = io.WriteString(fw, fileContent)
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rr := httptest.NewRecorder()
	api.HandleUpload(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code, "body: %s", rr.Body.String())

	var resp gen.UploadFilesResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Len(t, resp.Files, 1)
	require.NotNil(t, resp.Files[0].Ref)
	ref := *resp.Files[0].Ref

	store := mediaStoreForWorkspace(api)

	attachments := buildTranscriptAttachments(store, []string{ref}, workspaceID)

	require.Len(t, attachments, 1, "the accepted ref must produce exactly one persisted attachment")
	got := attachments[0]
	assert.Equal(t, ".library/"+fileName, got.Path,
		"the persisted attachment must carry the EXACT work-relative path the D-1 dual-write staged — "+
			"a later turn, or a different agent after a handoff, must be able to act on this path directly")
	assert.Equal(t, int64(len(fileContent)), got.Size)
	assert.NotEmpty(t, got.MIMEType)
	// The persisted Type MUST be a member of the Attachment contract enum
	// (image|audio|video|file) — this value crosses the wire and the SPA
	// validates it with a strict Zod schema. An earlier revision asserted
	// "document" here, matching an implementation that used DetectFileClass
	// (the presentation-noun taxonomy used to phrase LLM guidance). That
	// combination shipped a BLOCKER found in live UAT on 2026-07-29: the SPA
	// rejected the entire messages payload and chat history refused to load
	// with "Backend response failed validation" after any document upload.
	// The test encoded the bug, so it could not catch it.
	assert.Equal(t, "file", got.Type,
		"a .txt must persist as the contract's media-category 'file', never the presentation-noun 'document'")
	assert.Contains(t, []string{"image", "audio", "video", "file"}, got.Type,
		"persisted attachment Type must be a member of the Attachment contract enum — anything else "+
			"fails the SPA's strict schema validation and breaks history loading entirely")
}

// TestBuildTranscriptAttachments_UnresolvableRefSkippedNotFatal verifies a
// ref that fails to resolve (deleted file, malformed ref) is skipped rather
// than aborting the whole message — an attachment record is best-effort
// metadata, not load-bearing for the turn itself.
func TestBuildTranscriptAttachments_UnresolvableRefSkippedNotFatal(t *testing.T) {
	api := newUploadTestAPI(t)
	store := mediaStoreForWorkspace(api)

	unresolvableRef := "media://workspace/ws-does-not-exist/00000000-0000-0000-0000-000000000000"
	attachments := buildTranscriptAttachments(store, []string{unresolvableRef}, "ws-does-not-exist")
	assert.Empty(t, attachments, "an unresolvable ref must be skipped, not panic or error out the caller")
}

// TestBuildTranscriptAttachments_NoStoreOrRefs verifies the nil/empty guards.
func TestBuildTranscriptAttachments_NoStoreOrRefs(t *testing.T) {
	assert.Nil(t, buildTranscriptAttachments(nil, []string{"media://workspace/ws1/id1"}, "ws1"))
	api := newUploadTestAPI(t)
	assert.Nil(t, buildTranscriptAttachments(mediaStoreForWorkspace(api), nil, "ws1"))
}

// compile-time check that session.Attachment's field names match what
// buildTranscriptAttachments assumes (documentation-as-test — a rename would
// fail this file to compile, not just a runtime assertion).
var _ = session.Attachment{Type: "", Path: "", Size: 0, MIMEType: ""}
