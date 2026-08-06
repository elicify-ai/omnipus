// rest_workspace_delete_media_test.go — regression tests for two review
// findings in DELETE /api/v1/workspaces/{id}'s media-library cascade step
// (handleWorkspaceDelete, rest_workspaces.go):
//
//   - FIX-3 (HIGH): WorkspaceDeleteHook was always called with actor="",
//     making every real user's bulk media deletion unattributable in the
//     audit log even though the caller's identity was available on the
//     request.
//   - FIX-4 (HIGH): a failed media cascade was only slog.Warn'd — the
//     handler still fell through to os.RemoveAll and an unconditional 204,
//     AND WorkspaceDeleteHook itself only emitted its media.cascade_delete
//     audit event when len(deleted) > 0, so a cascade that failed before
//     deleting anything (e.g. a corrupt manifest.json) left ZERO audit
//     trail and a caller-visible "success".

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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// writeCorruptManifest plants an unparseable manifest.json at
// workspaces/<id>/media/manifest.json so library.New (called fresh by
// WorkspaceDeleteHook) deterministically fails to load — simulating "library
// exists but failed to load" (corrupt manifest / disk state), the exact
// condition FIX-4 must surface rather than silently swallow.
func writeCorruptManifest(t *testing.T, home, workspaceID string) {
	t.Helper()
	mediaDir := filepath.Join(home, "workspaces", workspaceID, "media")
	require.NoError(t, os.MkdirAll(mediaDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(mediaDir, "manifest.json"), []byte("{not valid json"), 0o600))
}

// TestHandleWorkspaceDelete_ActorAttribution is the FIX-3 regression test:
// the media.cascade_delete audit event emitted by WorkspaceDeleteHook must
// carry the authenticated caller's identity (from the request context, the
// same lookup rest_workspace_media.go's handleWorkspaceMediaDelete already
// uses), not a hardcoded empty string.
func TestHandleWorkspaceDelete_ActorAttribution(t *testing.T) {
	api, auditDir := newTestAPIWithAuditor(t)
	id := createWorkspaceViaAPI(t, api, "ActorAttribution", "")

	// Upload one file so the cascade has something to delete — isolates this
	// test's actor assertion from the FIX-4 "always audit, even empty"
	// behavior covered separately below.
	lib := api.agentLoop.GetWorkspaceLibrary(id)
	require.NotNil(t, lib, "workspace library must be resolvable")
	_, _, uploadErr := lib.Upload("note.txt", gen.UserUpload, strings.NewReader("bytes"))
	require.NoError(t, uploadErr)

	r := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/"+id, nil)
	r = r.WithContext(contextWithUser(r.Context(), "alice"))
	w := httptest.NewRecorder()
	api.handleWorkspaceDelete(w, r, id)
	require.Equal(t, http.StatusNoContent, w.Code, "body: %s", w.Body.String())

	events := readAuditEventsForTest(t, auditDir)
	var cascadeEvent map[string]any
	for _, e := range events {
		if e["event"] == "media.cascade_delete" {
			cascadeEvent = e
			break
		}
	}
	require.NotNil(t, cascadeEvent, "media.cascade_delete audit event must be emitted")
	details, ok := cascadeEvent["details"].(map[string]any)
	require.True(t, ok, "audit event must carry a details object")
	assert.Equal(t, "alice", details["actor"],
		"media.cascade_delete audit event must carry the authenticated caller, not a hardcoded empty string")

	// The generic workspace.delete event must also attribute the actor.
	var deleteEvent map[string]any
	for _, e := range events {
		if e["event"] == "workspace.delete" {
			deleteEvent = e
			break
		}
	}
	require.NotNil(t, deleteEvent, "workspace.delete audit event must be emitted")
	deleteDetails, ok := deleteEvent["details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "alice", deleteDetails["actor"])
	assert.Equal(t, false, deleteDetails["media_cascade_failed"])
}

// TestHandleWorkspaceDelete_MediaCascadeFailure_Returns500AndAudits is the
// FIX-4 regression test: a media-library cascade that fails (here, because
// the manifest is corrupt) must be visible to the caller as a non-2xx
// response — not a blank 204 — and must leave an audit trail even though
// zero entries were actually deleted.
func TestHandleWorkspaceDelete_MediaCascadeFailure_Returns500AndAudits(t *testing.T) {
	api, auditDir := newTestAPIWithAuditor(t)
	id := createWorkspaceViaAPI(t, api, "CascadeFailure", "")
	writeCorruptManifest(t, api.homePath, id)

	// Re-review FIX 1: the "media cascade-delete" failure log used to be a
	// bare slog.Error, invisible on a real (backgrounded) gateway. Capture
	// via pkg/logger's file sink (the one gateway.go's own
	// logger.EnableFileLogging call wires to gateway.log) to prove it is now
	// discoverable there. This is also a FIX 2 guard: a genuine hard cascade
	// failure (library.New itself fails to open a corrupt manifest, as here
	// — zero entries ever committed) must still be logged at Error, not
	// downgraded to the Warn level FIX 2 introduced for the benign
	// straggler case.
	logFile := filepath.Join(t.TempDir(), "workspace-delete-cascade.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	require.NoError(t, logger.EnableFileLogging(logFile))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	r := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/"+id, nil)
	r = r.WithContext(contextWithUser(r.Context(), "bob"))
	w := httptest.NewRecorder()
	api.handleWorkspaceDelete(w, r, id)

	// Before the fix this was an unconditional 204 with no way for the
	// caller to know the media cascade never ran.
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	var errResp gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.NotEmpty(t, errResp.Error)

	// The authoritative delete (the workspace JSON file) already happened
	// before the best-effort media cascade ran — the workspace itself is
	// genuinely gone even though this request reports 500 for the failed
	// cleanup step. A follow-up GET must 404, not 200.
	wsPath := filepath.Join(api.homePath, "workspaces", id+".json")
	_, statErr := os.Stat(wsPath)
	assert.True(
		t,
		os.IsNotExist(statErr),
		"workspace file must be gone — the authoritative delete is unaffected by the cascade failure",
	)

	getW := httptest.NewRecorder()
	getR := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+id, nil)
	api.handleWorkspaceGet(getW, getR, id)
	assert.Equal(
		t,
		http.StatusNotFound,
		getW.Code,
		"workspace must be confirmed gone via GET after the cascade-failure 500",
	)

	// Before the fix, WorkspaceDeleteHook only audited when len(deleted) > 0
	// — a cascade that failed before deleting anything left ZERO audit
	// trail. Assert the event now exists, with Decision=error.
	events := readAuditEventsForTest(t, auditDir)
	var cascadeEvent map[string]any
	for _, e := range events {
		if e["event"] == "media.cascade_delete" {
			cascadeEvent = e
			break
		}
	}
	require.NotNil(t, cascadeEvent, "a failed cascade must still emit a media.cascade_delete audit event (FIX-4)")
	assert.Equal(t, "error", cascadeEvent["decision"], "a failed cascade must be recorded as Decision=error, not allow")
	details, ok := cascadeEvent["details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "bob", details["actor"])
	assert.NotEmpty(t, details["error"], "the failure reason must be recorded in the audit details")

	// The workspace.delete event must flag the cascade failure so an
	// operator scanning the audit log for this event alone (without
	// cross-referencing media.cascade_delete) still sees it.
	var deleteEvent map[string]any
	for _, e := range events {
		if e["event"] == "workspace.delete" {
			deleteEvent = e
			break
		}
	}
	require.NotNil(t, deleteEvent, "workspace.delete audit event must still be emitted despite the cascade failure")
	deleteDetails, ok := deleteEvent["details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, deleteDetails["media_cascade_failed"])

	// FIX 1: the failure must be discoverable in the gateway.log-equivalent
	// file sink, not just returned to the caller / written to the audit log.
	logged, readErr := os.ReadFile(logFile)
	require.NoError(t, readErr)
	assert.Contains(t, string(logged), "delete workspace: media cascade-delete")
	assert.Contains(t, string(logged), id)
}

// TestHandleWorkspaceDelete_MediaCascadeSuccess_StillReturns204 is a
// happy-path guard: a workspace with no media library at all (never
// uploaded to) must still delete cleanly with 204, proving FIX-4 did not
// turn the common case into a false failure.
func TestHandleWorkspaceDelete_MediaCascadeSuccess_StillReturns204(t *testing.T) {
	api, auditDir := newTestAPIWithAuditor(t)
	id := createWorkspaceViaAPI(t, api, "NoMediaAtAll", "")

	r := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/"+id, nil)
	w := httptest.NewRecorder()
	api.handleWorkspaceDelete(w, r, id)

	require.Equal(t, http.StatusNoContent, w.Code, "body: %s", w.Body.String())
	assert.Empty(t, w.Body.String())

	events := readAuditEventsForTest(t, auditDir)
	var deleteEvent map[string]any
	for _, e := range events {
		if e["event"] == "workspace.delete" {
			deleteEvent = e
			break
		}
	}
	require.NotNil(t, deleteEvent)
	deleteDetails, ok := deleteEvent["details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, deleteDetails["media_cascade_failed"])
}

// TestHandleWorkspaceDelete_DirRemoveFailure_Returns500 is the FIX 3
// regression test: os.RemoveAll(wsDir) failing (e.g. EBUSY/permission on
// workspaces/<id>/work/, which a live agent turn may still be writing to)
// used to only reach a slog.Warn — the response gate checked solely
// mediaCascadeFailed, so a caller got a 204 "fully deleted" even though
// wsDir was still (partially) on disk. Forces a genuine RemoveAll failure
// via a read-only subdirectory (no write permission means the file inside
// it cannot be unlinked) rather than mocking os.RemoveAll, so this proves
// the real call site's behavior against a genuine kernel-level failure.
//
// Skipped when running as root: root bypasses DAC permission checks, so the
// chmod produces no error at all, RemoveAll succeeds, and the handler
// correctly returns 204 — the test would fail for a reason unrelated to the
// behavior it guards. That is exactly what happened in CI, which runs as root,
// while it passed locally as uid 1000. The deterministic, uid-independent
// coverage lives in TestHandleWorkspaceDelete_DirRemoveFailure_Injected below;
// this variant is retained because it is the only one that proves the real
// os.RemoveAll call site fails the way production would.
func TestHandleWorkspaceDelete_DirRemoveFailure_Returns500(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip(
			"root bypasses directory permissions; chmod cannot force a RemoveAll failure — see the _Injected variant",
		)
	}
	api, auditDir := newTestAPIWithAuditor(t)
	id := createWorkspaceViaAPI(t, api, "DirRemoveFailure", "")

	wsDir := filepath.Join(api.homePath, "workspaces", id)
	lockedDir := filepath.Join(wsDir, "work", "locked")
	require.NoError(t, os.MkdirAll(lockedDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(lockedDir, "busy.txt"), []byte("x"), 0o600))
	require.NoError(t, os.Chmod(lockedDir, 0o500)) // r-x: contents cannot be unlinked
	t.Cleanup(func() {
		_ = os.Chmod(lockedDir, 0o700)
		_ = os.RemoveAll(wsDir)
	})

	r := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/"+id, nil)
	r = r.WithContext(contextWithUser(r.Context(), "carol"))
	w := httptest.NewRecorder()
	api.handleWorkspaceDelete(w, r, id)

	// Before the fix this was an unconditional 204 even though wsDir was
	// still (partially) on disk.
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	var errResp gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.NotEmpty(t, errResp.Error)

	// The leftover file must genuinely still be on disk — proving the 500
	// reflects reality, not a spurious failure.
	_, statErr := os.Stat(filepath.Join(lockedDir, "busy.txt"))
	assert.NoError(t, statErr, "the locked file must still exist — RemoveAll only partially succeeded")

	// The authoritative workspace record delete is unaffected by the
	// directory-wipe failure — a follow-up GET must still 404.
	getW := httptest.NewRecorder()
	getR := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+id, nil)
	api.handleWorkspaceGet(getW, getR, id)
	assert.Equal(t, http.StatusNotFound, getW.Code,
		"workspace record must be confirmed gone via GET despite the directory-removal 500")

	events := readAuditEventsForTest(t, auditDir)
	var deleteEvent map[string]any
	for _, e := range events {
		if e["event"] == "workspace.delete" {
			deleteEvent = e
			break
		}
	}
	require.NotNil(t, deleteEvent)
	deleteDetails, ok := deleteEvent["details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, deleteDetails["media_cascade_failed"],
		"this failure is directory-removal, not a media cascade failure")
	assert.Equal(t, true, deleteDetails["dir_remove_failed"],
		"audit details must flag the directory-removal failure")
}

// TestHandleWorkspaceDelete_DirRemoveFailure_Injected is the uid-independent
// half of the FIX 3 coverage. The chmod-based test above cannot run as root
// (root ignores directory permissions), which left CI — running as root — with
// no coverage of this path at all. Injecting the failure through the
// removeAllFn seam reproduces the same handler branch deterministically for any
// uid, so the 500-not-204 guarantee is actually enforced by CI.
func TestHandleWorkspaceDelete_DirRemoveFailure_Injected(t *testing.T) {
	api, auditDir := newTestAPIWithAuditor(t)
	id := createWorkspaceViaAPI(t, api, "DirRemoveInjected", "")

	sentinel := errors.New("injected RemoveAll failure")
	orig := removeAllFn
	removeAllFn = func(string) error { return sentinel }
	t.Cleanup(func() { removeAllFn = orig })

	r := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/"+id, nil)
	r = r.WithContext(contextWithUser(r.Context(), "carol"))
	w := httptest.NewRecorder()
	api.handleWorkspaceDelete(w, r, id)

	require.Equal(t, http.StatusInternalServerError, w.Code,
		"a RemoveAll failure must surface as 500, not a silent 204; body: %s", w.Body.String())
	var errResp gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.NotEmpty(t, errResp.Error)

	// The workspace record delete is independent of the directory wipe.
	getW := httptest.NewRecorder()
	getR := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+id, nil)
	api.handleWorkspaceGet(getW, getR, id)
	assert.Equal(t, http.StatusNotFound, getW.Code,
		"workspace record must still be gone despite the directory-removal 500")

	events := readAuditEventsForTest(t, auditDir)
	var deleteEvent map[string]any
	for _, e := range events {
		if e["event"] == "workspace.delete" {
			deleteEvent = e
			break
		}
	}
	require.NotNil(t, deleteEvent)
	deleteDetails, ok := deleteEvent["details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, deleteDetails["media_cascade_failed"],
		"this failure is directory-removal, not a media cascade failure")
	assert.Equal(t, true, deleteDetails["dir_remove_failed"],
		"audit details must flag the directory-removal failure")
}
