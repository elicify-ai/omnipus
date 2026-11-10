package gateway

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1618 and its test-author brief require control-free raw records, not just
// control-free JSON output. The safe identifier representation is unspecified.
// Reuse preview_path_redact_test.go's raw slog capture and logger restoration;
// none of these tests may run in parallel while the default logger is replaced.

type codeQL1618ControlCase struct {
	name  string
	value string
}

func codeQL1618ControlCases(includeNUL bool) []codeQL1618ControlCase {
	cases := []codeQL1618ControlCase{{name: "ordinary"}}
	first := rune(1) // NUL is not a filename and validateEntityID rejects it.
	if includeNUL {
		first = 0
	}
	// Enumerate the brief's entire finite forbidden alphabet, including its
	// upper C0 boundary and DEL, rather than checking only newline and CR.
	for control := first; control < 0x20; control++ {
		cases = append(cases, codeQL1618ControlCase{
			name: fmt.Sprintf("control_%02x", control), value: string(control),
		})
	}
	cases = append(cases, codeQL1618ControlCase{name: "control_7f", value: "\x7f"})
	// #1666 F1: the non-ASCII record-boundary characters. U+0085 (NEL) is the
	// C1 line-break analogue; U+2028/U+2029 are the Unicode line/paragraph
	// separators, which a request path such as %E2%80%A8 delivers through
	// validateEntityID (it rejects only /, \, .. and NUL). Invalid UTF-8 is
	// the transport-replacement class: every JSON/zerolog sink rewrites it to
	// U+FFFD, so the record-level oracle observes it only where the raw bytes
	// reach the logger. All four are drifted against safeLogValue, not merely
	// enumerated: a future ASCII-only rewrite of the sanitiser must fail these.
	return append(cases,
		codeQL1618ControlCase{name: "control_85", value: "\u0085"},
		codeQL1618ControlCase{name: "line_separator_2028", value: "\u2028"},
		codeQL1618ControlCase{name: "paragraph_separator_2029", value: "\u2029"},
		codeQL1618ControlCase{name: "invalid_utf8", value: "\xff\xfe"},
	)
}

func codeQL1618SingleRecord(t *testing.T, logs *redactionLogCapture, message string) redactionLogRecord {
	t.Helper()
	var matches []redactionLogRecord
	for _, record := range logs.snapshot() {
		if record.message == message {
			matches = append(matches, record)
		}
	}
	require.Len(t, matches, 1, "must reach the named log site exactly once; an empty capture is not evidence of sanitisation: %s", message)
	return matches[0]
}

func codeQL1618AssertControlFree(t *testing.T, field, value string) {
	t.Helper()
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			t.Errorf("#1618: %s contains raw control U+%04X in %q; want a control-free log record", field, char, value)
			return
		}
	}
}

func codeQL1618AssertSafeRecord(t *testing.T, record redactionLogRecord, identifiers ...string) {
	t.Helper()
	for _, key := range identifiers {
		value, present := record.attrs[key]
		assert.True(t, present, "#1618: diagnostic must retain the recognisable identifier field %q", key)
		assert.NotEmpty(t, strings.TrimSpace(value), "#1618: identifier field %q must remain non-empty; no exact escaped/quoted/digest form is prescribed", key)
	}
	codeQL1618AssertControlFree(t, "message", record.message)
	for key, value := range record.attrs {
		codeQL1618AssertControlFree(t, "attribute key", key)
		// Inspect the whole record: sanitising an ID but copying it raw into
		// an error attribute must not satisfy the brief's record-level oracle.
		codeQL1618AssertControlFree(t, fmt.Sprintf("attribute %q", key), value)
	}
}

func codeQL1618ToolResultRequest(t *testing.T, sessionID, ref string) *http.Request {
	t.Helper()
	path := "/api/v1/sessions/" + url.PathEscape(sessionID) + "/tool-results/" + url.PathEscape(ref)
	request := httptest.NewRequest(http.MethodGet, path, nil)
	require.Equal(t, "/api/v1/sessions/"+sessionID+"/tool-results/"+ref, request.URL.Path,
		"fixture: the real request parser must decode the control-bearing path segments")
	return request
}

func TestCodeQL1618_LogCaptureRetainsRawControls(t *testing.T) {
	logs := captureRedactionSlog(t)
	var input strings.Builder
	for _, sample := range codeQL1618ControlCases(true) {
		input.WriteString(sample.value)
	}
	const message = "codeql1618: raw capture instrument control"
	slog.Warn(message, "id", input.String())
	record := codeQL1618SingleRecord(t, logs, message)
	require.Equal(t, map[string]string{"id": input.String()}, record.attrs,
		"the instrument must retain every raw control; formatted JSON would conceal this defect")
}

func TestCodeQL1618_ToolResultNULRejectedBeforeLogging(t *testing.T) {
	for _, field := range []string{"session_id", "ref"} {
		t.Run(field, func(t *testing.T) {
			logs := captureRedactionSlog(t)
			api := &restAPI{homePath: t.TempDir()}
			sessionID, ref := "session-1618", "result-1618"
			if field == "session_id" {
				sessionID += "\x00"
			} else {
				ref += "\x00"
			}
			response := httptest.NewRecorder()
			api.HandleToolResults(response, codeQL1618ToolResultRequest(t, sessionID, ref))
			require.Equal(t, http.StatusBadRequest, response.Code)
			require.JSONEq(t, fmt.Sprintf(`{"error":"invalid %s"}`, field), response.Body.String())
			require.Empty(t, logs.snapshot(), "NUL must be refused by validation, not reach a result-read log")
		})
	}
}

func TestCodeQL1618_UploadStageRollbackLogHasSafeMediaID(t *testing.T) {
	for _, sample := range codeQL1618ControlCases(true) {
		t.Run(sample.name, func(t *testing.T) {
			logs := captureRedactionSlog(t)
			api := &restAPI{homePath: t.TempDir()}
			const workspaceID = "ws-codeql1618"
			lib, err := library.New(api.homePath, workspaceID)
			require.NoError(t, err)
			mediaID := "media-" + sample.value + "-1618"
			ref := "media://workspace/" + workspaceID + "/" + mediaID
			response := httptest.NewRecorder()
			upload := &restAPIHandleUpload{a: api, w: response, workspaceID: workspaceID, workspaceLib: lib}

			// Existing finish seam: real Read rejects the invalid stored-ref ID;
			// real Delete also fails, reaching the stage-rollback warning.
			flow := upload.finishWorkspaceUpload("item.txt", ref, gen.MediaLibraryEntry{Filename: "item.txt"}, nil)
			require.Equal(t, restAPIHandleUploadReturn, flow)
			require.Equal(t, http.StatusInternalServerError, response.Code)
			record := codeQL1618SingleRecord(t, logs, "rest: upload: rollback of media-library entry failed after stage failure")
			codeQL1618AssertSafeRecord(t, record, "media_id")
		})
	}
}

func TestCodeQL1618_UploadCleanupLogHasSafeMediaID(t *testing.T) {
	for _, sample := range codeQL1618ControlCases(true) {
		t.Run(sample.name, func(t *testing.T) {
			logs := captureRedactionSlog(t)
			api := &restAPI{homePath: t.TempDir()}
			const workspaceID = "ws-codeql1618"
			lib, err := library.New(api.homePath, workspaceID)
			require.NoError(t, err)
			mediaID := "media-" + sample.value + "-1618"
			ref := "media://workspace/" + workspaceID + "/" + mediaID
			response := gen.UploadFilesResponse{Files: []gen.UploadedFile{{Ref: &ref}}}

			// Existing stored-ref cleanup seam; the library and Delete remain
			// real. In particular, NUL need not become an on-disk filename.
			api.cleanupWorkspaceUploads(&response, lib)
			record := codeQL1618SingleRecord(t, logs, "rest: upload: cleanup workspace file failed")
			codeQL1618AssertSafeRecord(t, record, "media_id")
		})
	}
}
