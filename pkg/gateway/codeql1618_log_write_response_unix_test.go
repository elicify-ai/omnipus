//go:build linux || darwin

package gateway

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// #1618 follow-up brings the response-write warning into scope. Only the
// response transport fails; the real request parser, store and handler run.
// Control-bearing stored filenames use the existing Linux/macOS fixture scope.
type codeQL1618WriteFailure struct {
	header    http.Header
	status    int
	attempted []byte
	calls     int
	failure   error
}

func (w *codeQL1618WriteFailure) Header() http.Header { return w.header }

func (w *codeQL1618WriteFailure) WriteHeader(status int) { w.status = status }

func (w *codeQL1618WriteFailure) Write(data []byte) (int, error) {
	w.calls++
	w.attempted = append(w.attempted, data...)
	return 0, w.failure
}

func TestCodeQL1618_ToolResultWriteResponseLogHasSafeIdentifiers(t *testing.T) {
	for _, field := range []string{"session_id", "ref"} {
		for _, sample := range codeQL1618ControlCases(false) {
			t.Run(field+"/"+sample.name, func(t *testing.T) {
				logs := captureRedactionSlog(t)
				api := &restAPI{homePath: t.TempDir()}
				sessionID, ref := "session-write-1618", "result-write-1618"
				var inputID string
				if field == "session_id" {
					sessionID += sample.value + "-item"
					inputID = sessionID
				} else {
					ref += sample.value + "-item"
					inputID = ref
				}
				require.NoError(t, validateEntityID(sessionID), "fixture: the request must reach the response writer, not fail ID validation")
				require.NoError(t, validateEntityID(ref), "fixture: the ref must reach the response writer, not fail validation")
				body := []byte(`{"fixture":"codeql1618 complete response body"}`)
				storedRef, ok := newToolResultStore(api.homePath).saveJSON(sessionID, body)
				require.True(t, ok, "fixture: persist real JSON before requesting it")
				dir := filepath.Join(api.homePath, "tool_results", sessionID)
				require.NoError(t, os.Rename(filepath.Join(dir, storedRef+".json"), filepath.Join(dir, ref+".json")))

				response := &codeQL1618WriteFailure{
					header:  make(http.Header),
					failure: errors.New("response transport failed for " + inputID + ": closed pipe"),
				}
				require.EqualError(t, response.failure, "response transport failed for "+inputID+": closed pipe")
				require.Contains(t, response.failure.Error(), inputID, "fixture: the transport error must really embed the raw request-derived identifier")
				api.HandleToolResults(response, codeQL1618ToolResultRequest(t, sessionID, ref))

				require.Equal(t, http.StatusOK, response.status, "fixture: result retrieval must succeed before the response transport fails")
				require.Equal(t, "application/json", response.header.Get("Content-Type"))
				require.Equal(t, 1, response.calls, "fixture: reach exactly one failing response write")
				require.Equal(t, body, response.attempted, "fixture: the handler must attempt the complete stored JSON, not a validation or storage error body")
				record := codeQL1618SingleRecord(t, logs, "rest: tool-results: write response failed")
				codeQL1618AssertSafeErrorRecord(t, record, "session_id", "ref")
			})
		}
	}
}
