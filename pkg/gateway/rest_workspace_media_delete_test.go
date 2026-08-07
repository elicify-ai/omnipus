// rest_workspace_media_delete_test.go — re-review FIX 3 / FIX 4 regression
// tests for handleWorkspaceMediaDelete (single-item DELETE
// /api/v1/workspaces/{id}/media/{media_id}, FR-008).
//
// FIX 3 (HIGH): library.Library.Delete's error contract changed (in
// another agent's file, library.go) so a final-unlink-only failure — the
// manifest entry IS committed-removed, only the on-disk quarantine file's
// last unlink step failed — now returns the real, non-zero-value entry
// TOGETHER WITH a non-nil error, instead of silently swallowing it. This
// handler used to treat ANY non-ErrNotFound/ErrInvalidMediaID error as a
// blanket 500, discarding the returned entry — so a single-item delete
// that fully succeeded (the item really is gone from the library) could
// intermittently report 500. classifyMediaDeleteOutcome
// (rest_workspace_media.go) is the fix: entry.Id != nil on the error path
// is the reliable signal (per Delete's own doc comment) that the manifest
// commit went through despite the later error.
//
// FIX 4 (LOW): a failed single-item delete used to produce NO audit trail
// at all (audit.EventMediaDelete was only ever logged on the clean-success
// path) — the same repudiation gap FR-009 closed for the cascade-delete
// case. logMediaDeleteAudit now fires for the hard-failure and straggler
// outcomes too, with claimBytesFreed=false for both (per Delete's doc: an
// audit event must never claim bytes_freed for bytes that are not actually
// free yet).

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

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media/library"
)

// ── Unit coverage: classifyMediaDeleteOutcome (pure function) ──────────────

func TestClassifyMediaDeleteOutcome_Success(t *testing.T) {
	entry := gen.MediaLibraryEntry{Filename: "a.txt"}
	got := classifyMediaDeleteOutcome(entry, nil)
	assert.Equal(t, mediaDeleteOutcomeSuccess, got)
}

func TestClassifyMediaDeleteOutcome_NotFound(t *testing.T) {
	for _, err := range []error{library.ErrNotFound, library.ErrInvalidMediaID} {
		got := classifyMediaDeleteOutcome(gen.MediaLibraryEntry{}, err)
		assert.Equal(t, mediaDeleteOutcomeNotFound, got, "err=%v", err)
	}
}

func TestClassifyMediaDeleteOutcome_Straggler(t *testing.T) {
	// entry.Id set (a real, non-zero-value projection) + a non-nil error
	// not matching ErrNotFound/ErrInvalidMediaID is exactly the shape
	// library.Delete's doc says a final-unlink-only failure returns.
	id := uuid.New()
	entry := gen.MediaLibraryEntry{Id: &id, Filename: "leaked.bin"}
	got := classifyMediaDeleteOutcome(entry, errors.New("final unlink failed"))
	assert.Equal(t, mediaDeleteOutcomeStraggler, got)
}

func TestClassifyMediaDeleteOutcome_HardFailure(t *testing.T) {
	// The zero-value entry (entry.Id == nil) + a non-nil, non-ErrNotFound
	// error is every OTHER Delete failure shape (quarantine-rename hard
	// failure, persist failure).
	got := classifyMediaDeleteOutcome(gen.MediaLibraryEntry{}, errors.New("persist failed"))
	assert.Equal(t, mediaDeleteOutcomeHardFailure, got)
}

// ── Integration: prove library.go's real Delete return shapes match what
//    classifyMediaDeleteOutcome assumes, using library.go's own exported
//    WithFileRemover/WithFileRenamer test seams (the same tool library.go's
//    own doc comment recommends over permission tricks, which don't
//    isolate a single step and don't work at all when tests run as root).

func TestDelete_FinalUnlinkFailure_ReturnsNonZeroEntryWithError(t *testing.T) {
	home := t.TempDir()
	workspaceID := "ws-single-straggler"

	failUnlink := errors.New("final unlink boom")
	lib, err := library.New(home, workspaceID, library.WithFileRemover(func(path string) error {
		if strings.Contains(path, ".delete-") {
			return failUnlink
		}
		return os.Remove(path)
	}))
	require.NoError(t, err)

	_, uploaded, uploadErr := lib.Upload("leaked.bin", gen.UserUpload, strings.NewReader("bytes"))
	require.NoError(t, uploadErr)
	require.NotNil(t, uploaded.Id)

	entry, delErr := lib.Delete(uploaded.Id.String())

	require.Error(t, delErr, "a final-unlink failure must be reported, not swallowed")
	require.NotNil(t, entry.Id, "the manifest commit must have gone through despite the unlink failure")
	assert.Equal(t, "leaked.bin", entry.Filename)

	// Feed the REAL return values through the production classifier.
	assert.Equal(t, mediaDeleteOutcomeStraggler, classifyMediaDeleteOutcome(entry, delErr))

	// The manifest commit is real: the item is gone from a fresh List().
	assert.Empty(t, lib.List())
}

func TestDelete_QuarantineRenameHardFailure_ReturnsZeroEntryWithError(t *testing.T) {
	home := t.TempDir()
	workspaceID := "ws-single-hardfail"

	failRename := errors.New("rename boom")
	lib, err := library.New(home, workspaceID, library.WithFileRenamer(func(oldpath, newpath string) error {
		return failRename
	}))
	require.NoError(t, err)

	_, uploaded, uploadErr := lib.Upload("still-there.bin", gen.UserUpload, strings.NewReader("bytes"))
	require.NoError(t, uploadErr)
	require.NotNil(t, uploaded.Id)

	entry, delErr := lib.Delete(uploaded.Id.String())

	require.Error(t, delErr)
	assert.Nil(t, entry.Id, "a hard failure (nothing committed) must return the zero-value entry")

	assert.Equal(t, mediaDeleteOutcomeHardFailure, classifyMediaDeleteOutcome(entry, delErr))
}

// ── logMediaDeleteAudit: FIX 4 audit-shape unit coverage ───────────────────

func TestLogMediaDeleteAudit_StragglerDoesNotClaimBytesFreed(t *testing.T) {
	api, auditDir := newTestAPIWithAuditor(t)
	size := int64(999)
	entry := gen.MediaLibraryEntry{Filename: "leaked.bin", Size: &size}

	api.logMediaDeleteAudit("ws-1", "media-1", "alice", entry, "error", false, errors.New("final unlink boom"))

	events := readAuditEventsForTest(t, auditDir)
	require.Len(t, events, 1)
	assert.Equal(t, "media.delete", events[0]["event"])
	assert.Equal(t, "error", events[0]["decision"])
	details, ok := events[0]["details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "alice", details["actor"])
	assert.Equal(t, "leaked.bin", details["filename"])
	// The whole point of FIX 4 / claimBytesFreed=false: must NOT claim the
	// 999-byte entry.Size as freed when the disk cleanup did not actually
	// complete.
	assert.Equal(t, float64(0), details["bytes_freed"])
	assert.NotEmpty(t, details["error"])
}

func TestLogMediaDeleteAudit_SuccessClaimsBytesFreed(t *testing.T) {
	api, auditDir := newTestAPIWithAuditor(t)
	size := int64(42)
	entry := gen.MediaLibraryEntry{Filename: "ok.bin", Size: &size}

	api.logMediaDeleteAudit("ws-1", "media-1", "alice", entry, "allow", true, nil)

	events := readAuditEventsForTest(t, auditDir)
	require.Len(t, events, 1)
	assert.Equal(t, "allow", events[0]["decision"])
	details, ok := events[0]["details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(42), details["bytes_freed"])
}

// ── Full HTTP round-trip coverage ───────────────────────────────────────────

func TestHandleWorkspaceMediaDelete_Success_AuditsLogsAndReturns200(t *testing.T) {
	api, auditDir := newTestAPIWithAuditor(t)
	workspaceID := "ws-media-delete-success"

	logFile := filepath.Join(t.TempDir(), "media-delete-success.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.INFO)
	require.NoError(t, logger.EnableFileLogging(logFile))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	lib := api.agentLoop.GetWorkspaceLibrary(workspaceID)
	require.NotNil(t, lib)
	_, uploaded, uploadErr := lib.Upload("note.txt", gen.UserUpload, strings.NewReader("bytes"))
	require.NoError(t, uploadErr)
	mediaID := uploaded.Id.String()

	r := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/"+workspaceID+"/media/"+mediaID, nil)
	r = r.WithContext(contextWithUser(r.Context(), "alice"))
	w := httptest.NewRecorder()
	api.handleWorkspaceMediaDelete(w, r, workspaceID, mediaID)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var got gen.MediaLibraryEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "note.txt", got.Filename)

	events := readAuditEventsForTest(t, auditDir)
	var deleteEvent map[string]any
	for _, e := range events {
		if e["event"] == "media.delete" {
			deleteEvent = e
		}
	}
	require.NotNil(t, deleteEvent, "media.delete audit event must be emitted on success")
	assert.Equal(t, "allow", deleteEvent["decision"])
	details, ok := deleteEvent["details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "alice", details["actor"])

	logged, readErr := os.ReadFile(logFile)
	require.NoError(t, readErr)
	assert.Contains(t, string(logged), "workspace media: deleted")
}

func TestHandleWorkspaceMediaDelete_NotFound_404NoAudit(t *testing.T) {
	api, auditDir := newTestAPIWithAuditor(t)
	workspaceID := "ws-media-delete-404"
	// Force the library to exist (so we exercise Delete's own ErrNotFound,
	// not openLibraryForWorkspace's separate error path).
	require.NotNil(t, api.agentLoop.GetWorkspaceLibrary(workspaceID))

	missingID := "22222222-2222-2222-2222-222222222222"
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/"+workspaceID+"/media/"+missingID, nil)
	w := httptest.NewRecorder()
	api.handleWorkspaceMediaDelete(w, r, workspaceID, missingID)

	require.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())

	events := readAuditEventsForTest(t, auditDir)
	for _, e := range events {
		assert.NotEqual(t, "media.delete", e["event"],
			"a not-found delete (no real media entity involved) must not emit a media.delete audit event")
	}
}
