//go:build linux || darwin

package gateway

import (
	"encoding/json"
	"errors"
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

// These existing-store fixtures require control-bearing filenames, which
// Windows forbids. The raw-capture, stage/cleanup and NUL-validation tests in
// codeql1618_log_sanitise_test.go do not have that filesystem precondition.

func TestCodeQL1618_ToolResultCapLogHasSafeIdentifiers(t *testing.T) {
	codeQL1618ToolResultFailureCases(t, true)
}

func TestCodeQL1618_ToolResultReadFailureLogHasSafeIdentifiers(t *testing.T) {
	codeQL1618ToolResultFailureCases(t, false)
}

func codeQL1618ToolResultFailureCases(t *testing.T, overCap bool) {
	t.Helper()
	for _, field := range []string{"session_id", "ref"} {
		for _, sample := range codeQL1618ControlCases(false) {
			t.Run(field+"/"+sample.name, func(t *testing.T) {
				logs := captureRedactionSlog(t)
				api := &restAPI{homePath: t.TempDir()}
				sessionID, ref := "session-1618", "result-1618"
				if field == "session_id" {
					sessionID += sample.value + "-item"
				} else {
					ref += sample.value + "-item"
				}
				require.NoError(t, validateEntityID(sessionID), "fixture: validation must let the ID reach the named log site")
				require.NoError(t, validateEntityID(ref), "fixture: validation must let the ref reach the named log site")

				body := []byte(`{"fixture":"codeql1618"}`)
				message := "rest: tool-results: read failed"
				if overCap {
					// Valid stored JSON at the documented 1 MiB cap + 1 byte:
					// two quotes plus cap-1 payload bytes. This is setup, not
					// an oracle for the sanitiser's chosen representation.
					body = []byte(`"` + strings.Repeat("x", replayMaxResultBytes-1) + `"`)
					message = "rest: tool-results: stored result exceeds the retrieval cap"
				}
				storedRef, ok := newToolResultStore(api.homePath).saveJSON(sessionID, body)
				require.True(t, ok, "fixture: write a real result using the production store")
				dir := filepath.Join(api.homePath, "tool_results", sessionID)
				target := filepath.Join(dir, ref+".json")
				require.NoError(t, os.Rename(filepath.Join(dir, storedRef+".json"), target))
				if !overCap {
					// Corrupt a real stored pathname into a directory. Reading
					// it fails deterministically even as root; no permission
					// trick or mock substitutes for the real store read.
					require.NoError(t, os.Remove(target))
					require.NoError(t, os.Mkdir(target, 0o700))
				}

				response := httptest.NewRecorder()
				api.HandleToolResults(response, codeQL1618ToolResultRequest(t, sessionID, ref))
				require.Equal(t, http.StatusInternalServerError, response.Code, "fixture must enter the error path rather than be rejected or miss the stored ref")
				require.JSONEq(t, `{"error":"could not read tool result"}`, response.Body.String())
				record := codeQL1618SingleRecord(t, logs, message)
				codeQL1618AssertSafeRecord(t, record, "session_id", "ref")
			})
		}
	}
}

// codeQL1618StoredRefLibrary plants a stored-ref spelling accepted by the real
// loader, not a new production seam. The installed uuid.Parse documents that
// only the middle 36 bytes of a 38-byte input are examined. A control followed
// by a real UUID and '}' therefore survives load and Read under its stored key.
// The generated projection and blob are from a real Upload; only the map key
// and blob basename change. Nothing changes the library's integrity checks.
func codeQL1618StoredRefLibrary(t *testing.T, home, workspaceID, control string) (*library.Library, string, gen.MediaLibraryEntry, *int) {
	t.Helper()
	lib, err := library.New(home, workspaceID)
	require.NoError(t, err)
	const contents = "codeql1618 association fixture bytes"
	ref, projection, err := lib.Upload("item.txt", gen.MediaLibraryEntrySourceUserUpload, strings.NewReader(contents))
	require.NoError(t, err)
	_, canonicalID, ok := media.ParseWorkspaceRef(ref)
	require.True(t, ok)
	mediaID := canonicalID
	if control != "" {
		mediaID = control + canonicalID + "}"
		manifestPath := filepath.Join(lib.Path(), "manifest.json")
		manifestBytes, err := os.ReadFile(manifestPath)
		require.NoError(t, err)
		var envelope map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(manifestBytes, &envelope))
		var entries map[string]gen.MediaLibraryEntry
		require.NoError(t, json.Unmarshal(envelope["entries"], &entries))
		entry, exists := entries[canonicalID]
		require.True(t, exists, "fixture: preserve the real persisted projection")
		delete(entries, canonicalID)
		entries[mediaID] = entry
		envelope["entries"], err = json.Marshal(entries)
		require.NoError(t, err)
		manifestBytes, err = json.Marshal(envelope)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(manifestPath, manifestBytes, 0o600))
		require.NoError(t, os.Rename(filepath.Join(lib.Path(), canonicalID), filepath.Join(lib.Path(), mediaID)))
		ref = "media://workspace/" + workspaceID + "/" + mediaID
	}

	writes := new(int)
	// Already-exported process-edge seam: fail both the association persist
	// and the rollback Delete persist, while keeping real staging/Read intact.
	lib, err = library.New(home, workspaceID, library.WithManifestWriter(func(_ string, _ []byte, _ os.FileMode) error {
		*writes++
		return errors.New("codeql1618: manifest writer unavailable")
	}))
	require.NoError(t, err, "fixture: real loader must accept the stored-ref spelling")
	_, loadedID, ok := media.ParseWorkspaceRef(ref)
	require.True(t, ok)
	data, _, err := lib.Read(loadedID)
	require.NoError(t, err, "fixture: real Read must succeed before claiming association-rollback coverage")
	require.Equal(t, contents, string(data), "fixture: staging reads the real integrity-checked blob")
	require.Zero(t, *writes, "fixture setup must not consume the manifest fault")
	return lib, ref, projection, writes
}

func TestCodeQL1618_UploadAssociationRollbackLogHasSafeMediaID(t *testing.T) {
	for _, sample := range codeQL1618ControlCases(false) {
		t.Run(sample.name, func(t *testing.T) {
			logs := captureRedactionSlog(t)
			api := &restAPI{homePath: t.TempDir()}
			const workspaceID = "ws-codeql1618"
			lib, ref, projection, writes := codeQL1618StoredRefLibrary(t, api.homePath, workspaceID, sample.value)
			response := httptest.NewRecorder()
			upload := &restAPIHandleUpload{a: api, w: response, workspaceID: workspaceID, workspaceLib: lib}

			flow := upload.finishWorkspaceUpload("item.txt", ref, projection, nil)
			require.Equal(t, restAPIHandleUploadReturn, flow)
			require.Equal(t, http.StatusInternalServerError, response.Code)
			require.Equal(t, 2, *writes, "association and rollback must both attempt real persistence after successful staging")
			record := codeQL1618SingleRecord(t, logs, "rest: upload: rollback of media-library entry failed after association failure")
			codeQL1618AssertSafeRecord(t, record, "media_id")
		})
	}
}
