//go:build linux || darwin

package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	"github.com/stretchr/testify/require"
)

// #1618 follow-up: CHECK's m12/m13/m14 survived because the old fixtures'
// Delete errors either quoted the ID themselves or omitted it. Keep those
// tests intact and add a real Delete error variant whose %s embeds the ID raw.
// These stored-blob fixtures share the existing Linux/macOS filename boundary.
func codeQL1618RawDeleteErrorLibrary(t *testing.T, home, workspaceID, control string) (*library.Library, string, gen.MediaLibraryEntry, *int) {
	t.Helper()
	lib, ref, projection, writes := codeQL1618StoredRefLibrary(t, home, workspaceID, control)
	_, mediaID, ok := media.ParseWorkspaceRef(ref)
	require.True(t, ok)

	// This existing process-edge option changes only the filesystem rename,
	// not Delete. The library itself constructs the raw-ID quarantine error.
	library.WithFileRenamer(func(oldPath, newPath string) error {
		require.Equal(t, filepath.Join(lib.Path(), mediaID), oldPath, "fixture: Delete must reach the blob's real quarantine rename")
		require.Equal(t, lib.Path(), filepath.Dir(newPath))
		require.True(t, strings.HasPrefix(filepath.Base(newPath), ".delete-"+mediaID+"-"), "fixture: rename must target this item's quarantine path")
		return os.ErrPermission
	})(lib)

	// Probe the real error before invoking a log site. Without this check an
	// already-escaped or ID-free error would recreate the original false green.
	_, deleteErr := lib.Delete(mediaID)
	require.ErrorIs(t, deleteErr, os.ErrPermission)
	require.Contains(t, deleteErr.Error(), "quarantine delete "+mediaID+":", "fixture: the real library must embed the identifier without quoting it")
	if control != "" {
		require.Contains(t, deleteErr.Error(), control, "fixture: the error that reaches the logger must actually contain the raw input control")
	}
	require.Zero(t, *writes, "fixture: quarantine failure must precede any manifest persistence")
	t.Logf("fixture: real Delete error before log sanitisation = %q", deleteErr.Error())
	return lib, ref, projection, writes
}

func codeQL1618AssertSafeErrorRecord(t *testing.T, record redactionLogRecord, identifiers ...string) {
	t.Helper()
	errorText, present := record.attrs["error"]
	require.True(t, present, "#1618: the error attribute under test must remain present")
	require.NotEmpty(t, strings.TrimSpace(errorText), "#1618: the diagnostic error must not be dropped to obtain a control-free record")
	codeQL1618AssertSafeRecord(t, record, identifiers...)
}

func TestCodeQL1618_UploadStageRollbackErrorAttributeIsSafe(t *testing.T) {
	for _, sample := range codeQL1618ControlCases(false) {
		t.Run(sample.name, func(t *testing.T) {
			logs := captureRedactionSlog(t)
			api := &restAPI{homePath: t.TempDir()}
			const workspaceID = "ws-codeql1618-raw-error"
			lib, ref, projection, writes := codeQL1618RawDeleteErrorLibrary(t, api.homePath, workspaceID, sample.value)
			_, mediaID, ok := media.ParseWorkspaceRef(ref)
			require.True(t, ok)

			// A real integrity failure prevents staging. Delete still finds the
			// manifest entry and reaches the quarantine-rename error above.
			require.NoError(t, os.WriteFile(filepath.Join(lib.Path(), mediaID), []byte("corrupted fixture bytes"), 0o600))
			_, _, readErr := lib.Read(mediaID)
			require.ErrorIs(t, readErr, library.ErrIntegrityCheckFailed)
			response := httptest.NewRecorder()
			upload := &restAPIHandleUpload{a: api, w: response, workspaceID: workspaceID, workspaceLib: lib}

			flow := upload.finishWorkspaceUpload("item.txt", ref, projection, nil)
			require.Equal(t, restAPIHandleUploadReturn, flow)
			require.Equal(t, http.StatusInternalServerError, response.Code)
			require.Zero(t, *writes, "staging and quarantine failures must not reach manifest persistence")
			record := codeQL1618SingleRecord(t, logs, "rest: upload: rollback of media-library entry failed after stage failure")
			codeQL1618AssertSafeErrorRecord(t, record, "media_id")
		})
	}
}

func TestCodeQL1618_UploadAssociationRollbackErrorAttributeIsSafe(t *testing.T) {
	for _, sample := range codeQL1618ControlCases(false) {
		t.Run(sample.name, func(t *testing.T) {
			logs := captureRedactionSlog(t)
			api := &restAPI{homePath: t.TempDir()}
			const workspaceID = "ws-codeql1618-raw-error"
			lib, ref, projection, writes := codeQL1618RawDeleteErrorLibrary(t, api.homePath, workspaceID, sample.value)
			response := httptest.NewRecorder()
			upload := &restAPIHandleUpload{a: api, w: response, workspaceID: workspaceID, workspaceLib: lib}

			flow := upload.finishWorkspaceUpload("item.txt", ref, projection, nil)
			require.Equal(t, restAPIHandleUploadReturn, flow)
			require.Equal(t, http.StatusInternalServerError, response.Code)
			require.Equal(t, 1, *writes, "staging must succeed and association must attempt persistence; rollback must fail earlier at quarantine")
			record := codeQL1618SingleRecord(t, logs, "rest: upload: rollback of media-library entry failed after association failure")
			codeQL1618AssertSafeErrorRecord(t, record, "media_id")
		})
	}
}

func TestCodeQL1618_UploadCleanupErrorAttributeIsSafe(t *testing.T) {
	for _, sample := range codeQL1618ControlCases(false) {
		t.Run(sample.name, func(t *testing.T) {
			logs := captureRedactionSlog(t)
			api := &restAPI{homePath: t.TempDir()}
			const workspaceID = "ws-codeql1618-raw-error"
			lib, ref, _, writes := codeQL1618RawDeleteErrorLibrary(t, api.homePath, workspaceID, sample.value)
			response := gen.UploadFilesResponse{Files: []gen.UploadedFile{{Ref: &ref}}}

			api.cleanupWorkspaceUploads(&response, lib)
			require.Zero(t, *writes, "quarantine failure must precede cleanup persistence")
			record := codeQL1618SingleRecord(t, logs, "rest: upload: cleanup workspace file failed")
			codeQL1618AssertSafeErrorRecord(t, record, "media_id")
		})
	}
}
