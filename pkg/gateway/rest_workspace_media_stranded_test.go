// rest_workspace_media_stranded_test.go — regression tests for the
// ErrEntryStranded HTTP mapping (library.go's "compound rollback-of-rollback"
// TD, closed alongside this file and pkg/workspace/media_delete.go).
//
// Before this fix, a persist failure whose compensating rename-back ALSO
// failed left the manifest claiming an entry was present at its normal
// on-disk path while the bytes actually sat at an undiscoverable
// ".delete-<id>-<uuid>" quarantine path. Every later Get/Read/Delete got a
// generic library.ErrNotFound, which this handler mapped to the same 404 a
// routine absent id gets — a server-side data-integrity problem silently
// disguised as "not found". library.ErrEntryStranded now distinguishes the
// two; these tests prove the distinction survives all the way through the
// HTTP handlers as a 500 with an attributable message, not a 404.
//
// buildStrandedWorkspaceLibrary provokes the compound failure on a
// throwaway *library.Library (using library.go's own WithFileRenamer /
// WithManifestWriter test seams — see that file's doc on why permission
// tricks can't isolate this), then discards that instance. The corruption
// itself lives on disk (manifest.json still claims the entry present, plus
// a stray quarantine dotfile) under api.homePath, so the handler's own
// unmodified production path — a.agentLoop.GetWorkspaceLibrary, a PLAIN
// library.New(home, workspaceID) with no fault injection at all — discovers
// it naturally via rebuildStrandedLocked on first load. This exercises the
// real end-to-end path, not a mocked one.

package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newStrandedTestAPI is a variant of newTestAPIWithAuditor (channel_instance_
// crud_test.go) / newTestRestAPIWithHome (rest_extra_test.go) with one
// deliberate difference: cfg.Agents.Defaults.Home is set to tmpDir+"/agents"
// (the real production convention — see pkg/agent/loop.go:635's
// filepath.Dir(cfg.AgentHomeBasePath())), not tmpDir itself. That matters
// ONLY for this file's tests: they build a corrupted on-disk workspace
// library OUTSIDE the AgentLoop (via a throwaway library.New call) and then
// need AgentLoop.GetWorkspaceLibrary — which resolves its own home as
// filepath.Dir(cfg.AgentHomeBasePath()) — to land on the SAME directory as
// api.homePath, or the handler would load an unrelated (empty) directory and
// the corruption would never be observed. newTestRestAPIWithHome sets
// Home: tmpDir directly, which is fine for every OTHER test in this package
// (they only ever go through api.agentLoop.GetWorkspaceLibrary, never assume
// it equals api.homePath), but wrong for this file's purpose.
func newStrandedTestAPI(t *testing.T) (api *restAPI, auditDir string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         filepath.Join(tmpDir, "agents"),
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	auditDir = filepath.Join(tmpDir, "system")
	require.NoError(t, os.MkdirAll(auditDir, 0o700))
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           auditDir,
		MaxSizeBytes:  1 << 20,
		RetentionDays: 1,
	})
	require.NoError(t, err, "audit logger must initialize")
	t.Cleanup(func() { _ = auditLogger.Close() })

	api = &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(filepath.Join(tmpDir, "tasks")),
		auditor:       auditLogger,
	}
	return api, auditDir
}

// errInjectedFailure is a fixed sentinel error used only to drive the two
// WithManifestWriter/WithFileRenamer fault-injection branches in
// buildStrandedWorkspaceLibrary below — its identity is never asserted on
// by the tests in this file (they assert on HTTP status/body only), it just
// needs to be a stable non-nil error.
var errInjectedFailure = errors.New("buildStrandedWorkspaceLibrary: injected failure")

// buildStrandedWorkspaceLibrary provokes library.ErrEntryStranded (the
// "compound rollback-of-rollback" state — a persist fails AND the
// compensating rename-back of the already-quarantined file also fails) on a
// throwaway fault-injected library instance rooted at home/workspaceID,
// leaving the corruption on disk, then returns the id of the now-stranded
// entry. The caller's own *restAPI (with homePath == home) discovers it
// through the ordinary, non-fault-injected production load path
// (rebuildStrandedLocked, run by every library.New via load()) — this
// exercises the real end-to-end path, not a mocked one.
func buildStrandedWorkspaceLibrary(t *testing.T, home, workspaceID string) string {
	t.Helper()
	now := time.Now().UTC()

	var failPersist, failRollback bool
	lib, err := library.New(home, workspaceID,
		library.WithClock(func() time.Time { return now }),
		library.WithManifestWriter(func(path string, data []byte, perm os.FileMode) error {
			if failPersist {
				return errInjectedFailure
			}
			return os.WriteFile(path, data, perm)
		}),
		library.WithFileRenamer(func(oldpath, newpath string) error {
			base := oldpath[strings.LastIndexByte(oldpath, '/')+1:]
			if failRollback && strings.HasPrefix(base, ".delete-") {
				return errInjectedFailure
			}
			return os.Rename(oldpath, newpath)
		}),
	)
	require.NoError(t, err)

	_, entry, uploadErr := lib.UploadFixture("stranded.bin", strings.NewReader("bytes"))
	require.NoError(t, uploadErr)
	require.NotNil(t, entry.Id)

	failPersist, failRollback = true, true
	_, delErr := lib.Delete(entry.Id.String())
	require.Error(t, delErr, "the compound failure must be provoked")
	require.True(t, errors.Is(delErr, library.ErrEntryStranded),
		"test setup itself must land the entry in ErrEntryStranded — got %v", delErr)

	return entry.Id.String()
}

func TestHandleWorkspaceMediaGet_Stranded_Returns500WithDistinctMessage(t *testing.T) {
	api, _ := newStrandedTestAPI(t)
	workspaceID := "ws-media-get-stranded"
	mediaID := buildStrandedWorkspaceLibrary(t, api.homePath, workspaceID)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/media/"+mediaID, nil)
	w := httptest.NewRecorder()
	api.handleWorkspaceMediaGet(w, r, workspaceID, mediaID)

	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	assert.NotEqual(t, http.StatusNotFound, w.Code,
		"a stranded entry must NOT collapse into the same 404 a routine absent id gets")

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	msg, _ := body["error"].(string)
	assert.Contains(t, msg, "inconsistent state", "error body must attribute the failure distinctly, not a generic 500")
}

func TestHandleWorkspaceMediaDelete_Stranded_Returns500WithAudit(t *testing.T) {
	api, auditDir := newStrandedTestAPI(t)
	workspaceID := "ws-media-delete-stranded"
	mediaID := buildStrandedWorkspaceLibrary(t, api.homePath, workspaceID)

	r := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/"+workspaceID+"/media/"+mediaID, nil)
	r = r.WithContext(contextWithUser(r.Context(), "alice"))
	w := httptest.NewRecorder()
	api.handleWorkspaceMediaDelete(w, r, workspaceID, mediaID)

	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	assert.NotEqual(t, http.StatusNotFound, w.Code,
		"a stranded entry must NOT be reported as a routine not-found delete")
	assert.NotEqual(t, http.StatusOK, w.Code,
		"a stranded entry must NOT be reported as a successful delete (nothing was actually removed)")

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	msg, _ := body["error"].(string)
	assert.Contains(t, msg, "inconsistent state")

	// FIX 4 precedent (this same file's other delete tests): even a failed
	// delete must leave an audit trail, decision=error, no bytes_freed claim.
	events := readAuditEventsForTest(t, auditDir)
	var deleteEvent map[string]any
	for _, e := range events {
		if e["event"] == "media.delete" {
			deleteEvent = e
		}
	}
	require.NotNil(t, deleteEvent, "media.delete audit event must be emitted even for a stranded-entry delete attempt")
	assert.Equal(t, "error", deleteEvent["decision"])
	details, ok := deleteEvent["details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(0), details["bytes_freed"], "must not claim bytes freed — nothing was actually deleted")
}

func TestHandleWorkspaceMediaAttach_Stranded_Returns500(t *testing.T) {
	api, _ := newStrandedTestAPI(t)
	workspaceID := "ws-media-attach-stranded"
	mediaID := buildStrandedWorkspaceLibrary(t, api.homePath, workspaceID)

	parsedID := uuid.MustParse(mediaID)
	reqBody, err := json.Marshal(gen.MediaAttachmentRequest{MediaId: parsedID})
	require.NoError(t, err)

	r := httptest.NewRequest(http.MethodPost,
		"/api/v1/workspaces/"+workspaceID+"/media/attachments", strings.NewReader(string(reqBody)))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.handleWorkspaceMediaAttach(w, r, workspaceID)

	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	assert.NotEqual(t, http.StatusNotFound, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	msg, _ := body["error"].(string)
	assert.Contains(t, msg, "inconsistent state")
}

// TestHandleWorkspaceMediaList_Stranded_AnnotatesStatusInResponse is the
// review FIX 2 end-to-end regression test: GET .../media (list) must still
// include a stranded entry (it genuinely is still in the manifest — dropping
// it would be its own kind of lie) but annotate it with status="stranded" in
// the JSON response, rather than presenting it identically to a healthy
// entry the way it did before this fix (the single-entry Get/Attach/Delete
// handlers above already refuse a stranded id outright with a 500; List has
// no single id to refuse, so it must annotate instead).
func TestHandleWorkspaceMediaList_Stranded_AnnotatesStatusInResponse(t *testing.T) {
	api, _ := newStrandedTestAPI(t)
	workspaceID := "ws-media-list-stranded"
	mediaID := buildStrandedWorkspaceLibrary(t, api.homePath, workspaceID)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/media", nil)
	w := httptest.NewRecorder()
	api.handleWorkspaceMediaList(w, r, workspaceID)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var entries []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))
	require.Len(t, entries, 1, "the stranded entry must still be cataloged in the list")

	assert.Equal(t, mediaID, entries[0]["id"])
	assert.Equal(t, "stranded", entries[0]["status"],
		"a stranded entry in the list must be annotated, not presented as a healthy entry")
}

// ── classifyMediaDeleteOutcome unit coverage for the new outcome ───────────

func TestClassifyMediaDeleteOutcome_Stranded(t *testing.T) {
	id := uuid.New()
	// Delete's own contract (library.go): a stranded outcome returns the
	// ZERO-value entry (unlike the straggler shape) together with an error
	// wrapping library.ErrEntryStranded.
	err := fmt.Errorf("%w: %s", library.ErrEntryStranded, id)
	got := classifyMediaDeleteOutcome(gen.MediaLibraryEntry{}, err)
	assert.Equal(t, mediaDeleteOutcomeStranded, got)
}
