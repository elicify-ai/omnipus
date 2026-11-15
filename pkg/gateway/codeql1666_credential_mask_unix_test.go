//go:build linux || darwin

package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/stretchr/testify/require"
)

// #1666 CHECK finding 1, the pre-existing #1618 site. HandleToolResults logs
// safeLogValue(session_id) and safeLogValue(err.Error()) when the real store
// read fails. The session id IS the synthetic credential, so both fields carry
// it, and the OS error names the path. A backslash credential is not in this
// list: validateEntityID rejects '\', so that shape cannot reach this site.
// It is covered at serverFailureText and in the scrubber table.

func codeQL1666ToolResultSessionShapes() []codeQL1666CredentialShape {
	var out []codeQL1666CredentialShape
	for _, shape := range codeQL1666CredentialShapes() {
		if shape.name == "backslash" {
			continue
		}
		out = append(out, shape)
	}
	return out
}

func codeQL1666DriveToolResultReadFailure(t *testing.T, cred string) {
	t.Helper()
	require.NoError(t, validateEntityID(cred),
		"fixture: this shape must be a legal session id so it reaches the log site")
	api := &restAPI{homePath: t.TempDir()}
	const ref = "result-1666"
	target := filepath.Join(api.homePath, "tool_results", cred, ref+".json")
	require.NoError(t, os.MkdirAll(target, 0o700),
		"fixture: a directory where the store expects a file, so the read fails with the path in the error")
	response := httptest.NewRecorder()
	api.HandleToolResults(response, codeQL1618ToolResultRequest(t, cred, ref))
	require.Equal(t, http.StatusInternalServerError, response.Code,
		"fixture: the read must fail closed and reach the read-failed log")
	require.NotContains(t, response.Body.String(), cred,
		"fixture: the client body must stay free of the session id")
}

func TestCodeQL1666_ToolResultQuotedCredentialNeverReachesFileSink(t *testing.T) {
	for _, shape := range codeQL1666ToolResultSessionShapes() {
		t.Run(shape.name, func(t *testing.T) {
			logPath := codeQL1666WithMaskedFileSink(t, shape.value)
			codeQL1666DriveToolResultReadFailure(t, shape.value)
			codeQL1666AssertCredentialMasked(t, logPath, shape.name, shape.value, "rest: tool-results: read failed")
		})
	}
}

func TestCodeQL1666_ToolResultPlainCredentialIsMaskedInFileSink(t *testing.T) {
	logPath := codeQL1666WithMaskedFileSink(t, codeQL1666PlainCredential)
	codeQL1666DriveToolResultReadFailure(t, codeQL1666PlainCredential)
	codeQL1666AssertCredentialMasked(t, logPath, "plain", codeQL1666PlainCredential, "rest: tool-results: read failed")
}

// TestCodeQL1666_StageMediaIDEveryFileRecordIsControlFree is CHECK finding 3.
// The stage-media-ID fixture corrupts a real blob, so Library.Read emits its
// native "sha256 verification failed" warning with the raw media id before
// the upload ErrorCF. The existing test selects one named record. This one
// asserts every record the file reader returns. ORACLE: the same control-free
// record rule as the #1666 file-sink tests (no C0, DEL, C1, U+2028, U+2029,
// and valid UTF-8), applied to the whole sink, not to one message.
func TestCodeQL1666_StageMediaIDEveryFileRecordIsControlFree(t *testing.T) {
	for _, sample := range codeQL1666ManifestSafeCases() {
		t.Run(sample.name, func(t *testing.T) {
			readFile := codeQL1666CaptureFileLog(t)
			api := &restAPI{homePath: t.TempDir()}
			const workspaceID = "ws-codeql1666"
			lib, ref, projection, _ := codeQL1618StoredRefLibrary(t, api.homePath, workspaceID, sample.value)
			_, loadedID, ok := media.ParseWorkspaceRef(ref)
			require.True(t, ok)
			require.NoError(t, os.WriteFile(filepath.Join(lib.Path(), loadedID), []byte("corrupted fixture bytes"), 0o600),
				"fixture: corrupt the real blob so staging fails at the integrity check")
			response := httptest.NewRecorder()
			upload := &restAPIHandleUpload{a: api, w: response, workspaceID: workspaceID, workspaceLib: lib}
			flow := upload.finishWorkspaceUpload("item.txt", ref, projection, nil)
			require.Equal(t, restAPIHandleUploadReturn, flow, "fixture: staging must fail")
			records := readFile()
			require.NotEmpty(t, records, "fixture: the file sink must have received the call's records")
			sawIntegrity := false
			for _, record := range records {
				if record.message == "sha256 verification failed" {
					sawIntegrity = true
				}
				codeQL1666AssertStringSafe(t, "file record message "+record.message, record.message)
				for key, value := range record.fields {
					codeQL1666AssertStringSafe(t, "file field key", key)
					codeQL1666AssertStringSafe(t, "file field "+key+" of "+record.message, value)
				}
				codeQL1666AssertStringSafe(t, "raw file line of "+record.message, string(record.raw))
			}
			require.True(t, sawIntegrity,
				"fixture: Library.Read's integrity warning must be in the sink, or the sweep is not looking at it")
		})
	}
}
