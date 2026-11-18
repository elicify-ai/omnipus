package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	logger "github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	"github.com/stretchr/testify/require"
)

// #1666 — log-safety follow-up to #1618. F1 extends the #1618 control
// alphabet with the non-ASCII record-boundary characters (U+0085, U+2028,
// U+2029, invalid UTF-8); F2 extends the sanitised-log-site set. The oracle is
// the landed #1618 one: control-free RAW log records, with the safe
// representation form deliberately unspecified. Invalid UTF-8 is checked with
// utf8.Valid because ranging over an invalid string yields U+FFFD, which the
// rune alphabet below must not mistake for clean.
//
// None of these tests may run in parallel: they replace the default slog
// logger and redirect pkg/logger's file sink, exactly like the #1618 suite.

// codeQL1666IsForbiddenRune reports whether r is a character that can forge or
// terminate a log record or act on a terminal: C0, DEL, the C1 range (which
// includes U+0085 NEL), and the Unicode line/paragraph separators.
func codeQL1666IsForbiddenRune(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == 0x2028 || r == 0x2029
}

func codeQL1666AssertStringSafe(t *testing.T, what, value string) {
	t.Helper()
	require.True(t, utf8.ValidString(value), "#1666: %s carries invalid UTF-8: %q", what, value)
	for _, r := range value {
		if codeQL1666IsForbiddenRune(r) {
			t.Errorf("#1666: %s contains raw control U+%04X in %q; want a control-free record", what, r, value)
			return
		}
	}
}

// codeQL1666AssertCaptureControlFree sweeps every record the capture holds —
// messages, attribute keys and attribute values — so sanitising a value into
// one field while copying it raw into another cannot pass.
func codeQL1666AssertCaptureControlFree(t *testing.T, logs *redactionLogCapture) {
	t.Helper()
	for _, record := range logs.snapshot() {
		codeQL1666AssertStringSafe(t, "record message", record.message)
		for key, value := range record.attrs {
			codeQL1666AssertStringSafe(t, "record attribute key", key)
			codeQL1666AssertStringSafe(t, fmt.Sprintf("record attribute %q", key), value)
		}
	}
}

func codeQL1666AssertRecordSafe(t *testing.T, record redactionLogRecord, identifiers ...string) {
	t.Helper()
	for _, key := range identifiers {
		value, present := record.attrs[key]
		require.True(t, present, "#1666: record must retain the identifier field %q", key)
		require.NotEmpty(t, strings.TrimSpace(value), "#1666: identifier field %q must remain non-empty; the safe representation is unspecified", key)
	}
	codeQL1666AssertStringSafe(t, "record message", record.message)
	for key, value := range record.attrs {
		codeQL1666AssertStringSafe(t, "record attribute key", key)
		codeQL1666AssertStringSafe(t, fmt.Sprintf("record attribute %q", key), value)
	}
}

// TestCodeQL1666_SafeLogValueEscapesLineSeparators is the F1 drift guard: %q
// must escape every non-printable rune, not only the ASCII C0 range, so a
// future ASCII-only rewrite of safeLogValue fails here. ORACLE: Go fmt's %q
// contract (non-printable characters are escaped) plus the diagnostic-content
// rule from the #1618 brief — the escaped form is free to choose its spelling,
// but the surrounding text must survive it.
func TestCodeQL1666_SafeLogValueEscapesLineSeparators(t *testing.T) {
	for _, sample := range codeQL1618ControlCases(true) {
		t.Run(sample.name, func(t *testing.T) {
			input := "sess" + sample.value + "1666"
			escaped := safeLogValue(input)
			require.NotEqual(t, input, escaped, "#1666: safeLogValue must quote the value, not pass it through")
			require.Contains(t, escaped, "sess", "#1666: the diagnostic text before the separator must survive")
			require.Contains(t, escaped, "1666", "#1666: the diagnostic text after the separator must survive")
			codeQL1666AssertStringSafe(t, "safeLogValue output", escaped)
		})
	}
}

func TestCodeQL1666_MediaLegacyNotFoundLogIsControlFree(t *testing.T) {
	// #1666 F2, serveMedia's 404 branch ("rest: media ref not found") through
	// the real legacy route: a real FileMediaStore, an absent ref, no mocks.
	// The legacy route validates only path separators and "..", so this is
	// also the one fixture where NUL can ride the whole request path.
	for _, sample := range codeQL1618ControlCases(true) {
		t.Run(sample.name, func(t *testing.T) {
			logs := captureRedactionSlog(t)
			api := &restAPI{homePath: t.TempDir(), mediaStore: media.NewFileMediaStore()}
			refID := "media-1666" + sample.value + "-item"

			request := httptest.NewRequest(http.MethodGet,
				"/api/v1/media/"+url.PathEscape(refID), nil)
			require.Equal(t, "/api/v1/media/"+refID, request.URL.Path,
				"fixture: the real request parser must decode the control-bearing ref")
			response := httptest.NewRecorder()
			api.HandleMedia(response, request)

			require.Equal(t, http.StatusNotFound, response.Code)
			require.JSONEq(t, `{"error":"media not found"}`, response.Body.String(),
				"#1666: the response body must stay unchanged; only the log record is in scope")
			record := codeQL1618SingleRecord(t, logs, "rest: media ref not found")
			codeQL1666AssertRecordSafe(t, record, "ref", "error")
			codeQL1666AssertCaptureControlFree(t, logs)
		})
	}
}

func TestCodeQL1666_MediaCrossWorkspaceDenialLogIsControlFree(t *testing.T) {
	// #1666 F2, serveMedia's 403 branch. The two registered routes derive the
	// caller workspace and the ref workspace from the same path segments, so
	// the mismatch branch is unreachable through them; the branch and its raw
	// logRef are still #1666 scope, so the test drives the real serveMedia
	// method with the mismatched opts a caller in another workspace would
	// produce — the same direct-seam precedent the landed #1618 tests set for
	// finishWorkspaceUpload. Real store, real guard, no mocks.
	for _, sample := range codeQL1618ControlCases(false) {
		t.Run(sample.name, func(t *testing.T) {
			logs := captureRedactionSlog(t)
			api := &restAPI{homePath: t.TempDir(), mediaStore: media.NewFileMediaStore()}
			workspaceID := "ws-1666" + sample.value + "-ws"
			require.NoError(t, validateEntityID(workspaceID),
				"fixture: validation must let the workspace id reach the log site")
			ref := media.WorkspaceRefPrefix + workspaceID + "/media-1666-id"

			response := httptest.NewRecorder()
			api.serveMedia(response,
				httptest.NewRequest(http.MethodGet, "/api/v1/media/workspace/", nil),
				ref, media.WithCallerWorkspace("ws-1666-caller-elsewhere"), ref)

			require.Equal(t, http.StatusForbidden, response.Code)
			require.JSONEq(t, `{"error":"media access denied"}`, response.Body.String(),
				"#1666: the response body must stay unchanged; only the log record is in scope")
			record := codeQL1618SingleRecord(t, logs, "rest: media ref not found")
			codeQL1666AssertRecordSafe(t, record, "ref", "error")
			codeQL1666AssertCaptureControlFree(t, logs)
		})
	}
}

// codeQL1666ManifestSafeCases is the part of the #1618 alphabet that can
// physically persist into a media-library manifest key. The loader validates
// every key with uuid.Parse (library.go::loadLocked), whose braced form
// examines only the middle 36 bytes — so a SINGLE-byte ASCII control prefixed
// to a real uuid parses fine (the landed #1618 fixture's premise), while a
// MULTI-byte control (U+0085 = 2 bytes, U+2028/U+2029 = 3 bytes, invalid
// UTF-8) shifts the uuid core and fails the hex parse. The excluded cases are
// not a coverage hole: they are proven through the log sites whose request
// paths never cross a manifest key (the codeql1666 serve/upload fixtures),
// and the loader boundary itself is pinned by
// TestCodeQL1666_ManifestLoaderRejectsMultiByteControlIDs.
func codeQL1666ManifestSafeCases() []codeQL1618ControlCase {
	excluded := map[string]bool{
		"control_85":               true,
		"line_separator_2028":      true,
		"paragraph_separator_2029": true,
		"invalid_utf8":             true,
	}
	var out []codeQL1618ControlCase
	for _, sample := range codeQL1618ControlCases(false) {
		if excluded[sample.name] {
			continue
		}
		out = append(out, sample)
	}
	return out
}

// TestCodeQL1666_ManifestLoaderRejectsMultiByteControlIDs pins the boundary
// that scopes the manifest-persisting fixtures: a manifest key carrying a
// multi-byte control is rejected by the real loader, so a media_id that
// reaches an upload rollback log site can never carry one. ORACLE: the
// loader's own ErrInvalidManifest contract (library.go::loadLocked), not this
// test's convenience.
func TestCodeQL1666_ManifestLoaderRejectsMultiByteControlIDs(t *testing.T) {
	for _, sample := range []codeQL1618ControlCase{
		{name: "control_85", value: "\u0085"},
		{name: "line_separator_2028", value: "\u2028"},
		{name: "paragraph_separator_2029", value: "\u2029"},
		{name: "invalid_utf8", value: "\xff\xfe"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			home := t.TempDir()
			lib, err := library.New(home, "ws-codeql1666")
			require.NoError(t, err)
			ref, _, err := lib.Upload("item.txt", gen.MediaLibraryEntrySourceUserUpload, strings.NewReader("codeql1666 boundary fixture bytes"))
			require.NoError(t, err)
			_, canonicalID, ok := media.ParseWorkspaceRef(ref)
			require.True(t, ok)

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
			entries[sample.value+canonicalID+"}"] = entry
			envelope["entries"], err = json.Marshal(entries)
			require.NoError(t, err)
			manifestBytes, err = json.Marshal(envelope)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(manifestPath, manifestBytes, 0o600))

			_, err = library.New(home, "ws-codeql1666")
			require.Error(t, err, "#1666: the loader must reject a manifest key carrying %s", sample.name)
			require.ErrorIs(t, err, library.ErrInvalidManifest,
				"#1666: the rejection must be the loader's own manifest-invalid contract")
		})
	}
}

// codeQL1666FileRecord is one parsed gateway.log line plus its raw bytes.
type codeQL1666FileRecord struct {
	message string
	fields  map[string]string
	raw     []byte
}

// codeQL1666CaptureFileLog redirects pkg/logger's real file sink into a
// private file for the test's duration and returns a reader for the parsed
// records. This is the exported seam (logger.EnableFileLogging) — the same
// sink production writes; the console writer is pinned by pkg/logger's own
// tests (console_control_bytes_test.go) and is not redirectable from here.
func codeQL1666CaptureFileLog(t *testing.T) func() []codeQL1666FileRecord {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.log")
	require.NoError(t, logger.EnableFileLogging(path), "fixture: enable the real file sink")
	t.Cleanup(logger.DisableFileLogging)
	return func() []codeQL1666FileRecord {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		var out []codeQL1666FileRecord
		for _, line := range bytes.Split(raw, []byte("\n")) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var event map[string]any
			require.NoError(t, json.Unmarshal(line, &event),
				"fixture: the file sink must emit one JSON object per line")
			record := codeQL1666FileRecord{raw: line, fields: map[string]string{}}
			if message, ok := event["message"].(string); ok {
				record.message = message
			}
			for key, value := range event {
				if key == "message" {
					continue
				}
				record.fields[key] = fmt.Sprintf("%v", value)
			}
			out = append(out, record)
		}
		return out
	}
}

func codeQL1666SingleFileRecord(t *testing.T, records []codeQL1666FileRecord, message string) codeQL1666FileRecord {
	t.Helper()
	var matches []codeQL1666FileRecord
	for _, record := range records {
		if record.message == message {
			matches = append(matches, record)
		}
	}
	require.Len(t, matches, 1,
		"must reach the named log site exactly once; an empty capture is not evidence of sanitisation: %s", message)
	return matches[0]
}

// codeQL1666AssertFileRecordSafe asserts the record-level oracle on the JSON
// file sink: named fields present and non-empty, every field value, the
// message and the raw line bytes control-free. The raw-byte sweep is what
// catches a value zerolog passes through unescaped (U+2028/U+2029 are valid
// JSON and need no escaping); the parsed-value sweep is what survives any
// transport escaping. Invalid UTF-8 is not observable at this sink even
// today — zerolog replaces invalid bytes with U+FFFD — so it is asserted at
// the raw-record sites instead (stated gap, not a silent omission).
func codeQL1666AssertFileRecordSafe(t *testing.T, record codeQL1666FileRecord, fields ...string) {
	t.Helper()
	for _, key := range fields {
		value, present := record.fields[key]
		require.True(t, present, "#1666: file record must retain the field %q", key)
		require.NotEmpty(t, strings.TrimSpace(value), "#1666: field %q must remain non-empty; the safe representation is unspecified", key)
		codeQL1666AssertStringSafe(t, fmt.Sprintf("file field %q", key), value)
	}
	codeQL1666AssertStringSafe(t, "file record message", record.message)
	for key, value := range record.fields {
		codeQL1666AssertStringSafe(t, "file field key", key)
		codeQL1666AssertStringSafe(t, fmt.Sprintf("file field %q", key), value)
	}
	codeQL1666AssertStringSafe(t, "raw file line", string(record.raw))
}

// TestCodeQL1666_UploadStageErrorCFLogHasSafeFilename covers the filename
// field of finishWorkspaceUpload's staging ErrorCF line (#1666 F2) with the
// control alphabet the real request path can actually deliver there.
// library normalizeFilename and agent.SanitizeUploadFilename both reject
// unicode.IsControl characters upstream, so a C0/C1-bearing multipart
// filename provably never reaches this site; U+2028/U+2029 pass both gates
// (they are Zl/Zp, not controls) and reach the logger raw. The staging fault
// is a real corrupted blob — the integrity check fails before the filename
// is ever sanitised, so the raw multipart name is what the site logs.
func TestCodeQL1666_UploadStageErrorCFLogHasSafeFilename(t *testing.T) {
	for _, sample := range []codeQL1618ControlCase{
		{name: "ordinary"},
		{name: "line_separator_2028", value: "\u2028"},
		{name: "paragraph_separator_2029", value: "\u2029"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			logs := captureRedactionSlog(t)
			readFile := codeQL1666CaptureFileLog(t)
			api := &restAPI{homePath: t.TempDir()}
			const workspaceID = "ws-codeql1666"
			lib, err := library.New(api.homePath, workspaceID)
			require.NoError(t, err)
			fileName := "item" + sample.value + ".txt"
			ref, projection, err := lib.Upload(fileName, gen.MediaLibraryEntrySourceUserUpload, strings.NewReader("codeql1666 filename fixture bytes"))
			require.NoError(t, err, "fixture: the library must accept the separator-bearing name (Zl/Zp are not controls)")
			_, mediaID, ok := media.ParseWorkspaceRef(ref)
			require.True(t, ok, "fixture: the uploaded ref must parse to its id")
			require.NoError(t, os.WriteFile(filepath.Join(lib.Path(), mediaID), []byte("corrupted fixture bytes"), 0o600),
				"fixture: corrupt the real blob so staging fails at the integrity check")

			response := httptest.NewRecorder()
			upload := &restAPIHandleUpload{a: api, w: response, workspaceID: workspaceID, workspaceLib: lib}
			flow := upload.finishWorkspaceUpload(fileName, ref, projection, nil)
			require.Equal(t, restAPIHandleUploadReturn, flow)
			require.Equal(t, http.StatusInternalServerError, response.Code)
			// characterization test: the issue pins this response as unchanged;
			// the oracle for the fix is the log record below.
			require.JSONEq(t, `{"error":"could not stage uploaded file for agent access: server storage or configuration failed. Check disk space and permissions, then retry. Details are in the server log."}`,
				response.Body.String())

			record := codeQL1666SingleFileRecord(t, readFile(),
				"upload: could not stage workspace library copy for agent access")
			codeQL1666AssertFileRecordSafe(t, record, "media_id", "filename", "error", "workspace_id")
			codeQL1666AssertCaptureControlFree(t, logs)
		})
	}
}
