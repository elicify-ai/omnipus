package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/require"
)

// Ruling Part 1 / T-66: person authority is the session Library, including
// real mounts. No agent turn or model is needed to render a Retry.
func TestOfficePreview_RetryLibraryRendersOnlyForPerson(t *testing.T) {
	for _, sourceKind := range []string{"relative_work", "absolute_work", "absolute_mount", "relative_mount"} {
		t.Run(sourceKind, func(t *testing.T) {
			f := newOfficeRetryFixture(t)
			officeRetrySharedRenderer(t)
			data := officepreview.PDF(1)
			rel := "report.pdf"
			source := rel
			path := filepath.Join(workDir(f.api, f.wsID), rel)
			if sourceKind == "absolute_work" {
				source = path
			}
			if sourceKind == "absolute_mount" || sourceKind == "relative_mount" {
				target := t.TempDir()
				mount, _, err := workspace.CreateMount(f.api.homePath, f.wsID, "documents", target)
				require.NoError(t, err)
				path = filepath.Join(mount.HostPath, "report.pdf")
				rel = "documents/report.pdf"
				source = path
				if sourceKind == "relative_mount" {
					source = rel
				}
			}
			require.NoError(t, os.WriteFile(path, data, 0600))
			f.requireLibraryBytes(t, rel, data)
			f.record(t, source, data)
			store := f.api.agentLoop.GetSessionStore()
			modelKey := "agent:mia:session:" + f.sid
			before := store.GetHistory(modelKey)
			other := makeTestConn()
			bindTestConnToSession(f.ws, "other-person-session", "unrelated-session", other)
			response := f.retry(t, f.sid)
			require.Equal(t, http.StatusAccepted, response.Code, "%s", response.Body.String())
			var started generated.PreviewRetryResponse
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &started))
			require.Equal(t, generated.PreviewRetryResponse{Status: "started"}, started)
			f.requireRenderedFrame(t, officeReceiveMedia(t, f.conn))
			require.Empty(t, other.sendCh, "not a broadcast to another session")
			require.Equal(t, int64(0), f.provider.calls.Load(), "Retry must never invoke the model")
			require.Empty(t, f.bus.InboundChan(), "Retry must not queue a model turn")
			require.Equal(t, before, store.GetHistory(modelKey), "Retry pages must not enter model context")
		})
	}
}

func TestOfficePreview_RetryOutsideLibraryIs403WithoutPath(t *testing.T) {
	for _, sourceKind := range []string{"operator_allowance", "agent_home", "other_workspace", "escaping_symlink", "protected_hardlink"} {
		t.Run(sourceKind, func(t *testing.T) {
			f := newOfficeRetryFixture(t)
			data := officepreview.PDF(1)
			base := t.TempDir()
			if sourceKind == "agent_home" {
				base = filepath.Join(f.api.homePath, "agents", "mia")
			}
			if sourceKind == "other_workspace" {
				otherID := seedLibraryWorkspace(t, f.api, "Another Library")
				base = workDir(f.api, otherID)
			}
			require.NoError(t, os.MkdirAll(base, 0700))
			path := filepath.Join(base, "private-source.pdf")
			require.NoError(t, os.WriteFile(path, data, 0600))
			source := path
			if sourceKind == "operator_allowance" {
				// An actual successful agent read does not give the person a
				// file in this session's Library. Canonicalize regex fixture roots.
				physical, err := filepath.EvalSymlinks(path)
				require.NoError(t, err)
				patterns := []*regexp.Regexp{regexp.MustCompile("^" + regexp.QuoteMeta(path) + "$"), regexp.MustCompile("^" + regexp.QuoteMeta(physical) + "$")}
				f.api.agentLoop.GetConfig().Tools.AllowReadPaths = []string{patterns[0].String(), patterns[1].String()}
				result := tools.NewReadFileTool(workDir(f.api, f.wsID), true, 0, patterns).Execute(context.Background(), map[string]any{"path": path})
				require.False(t, result.IsError, "real agent read control: %s", result.ForLLM)
			}
			if sourceKind == "escaping_symlink" {
				source = filepath.Join(workDir(f.api, f.wsID), "escaped-private.pdf")
				require.NoError(t, os.Symlink(path, source))
			}
			if sourceKind == "protected_hardlink" {
				protected := filepath.Join(f.api.homePath, "credentials.json")
				require.NoError(t, os.WriteFile(protected, data, 0600))
				source = filepath.Join(workDir(f.api, f.wsID), "protected-private.pdf")
				require.NoError(t, os.Link(protected, source))
				policy, err := fspolicy.EffectiveFSPolicy(context.Background(), workDir(f.api, f.wsID), "", true, f.api.homePath, "", f.wsID)
				require.NoError(t, err)
				require.True(t, fspolicy.IsCarveOut(source, policy), "fixture aliases a protected file, not an ordinary PDF")
			}
			f.record(t, source, data)
			officeRetryError(t, f.retry(t, f.sid), http.StatusForbidden, "preview_source_not_in_library", source)
			require.Empty(t, f.conn.sendCh)
		})
	}
}

func TestOfficePreview_RetryMissingIs403AndChangedIs409(t *testing.T) {
	for _, state := range []string{"missing", "changed"} {
		t.Run(state, func(t *testing.T) {
			f := newOfficeRetryFixture(t)
			data := officepreview.PDF(1)
			path := filepath.Join(workDir(f.api, f.wsID), "versioned-private.pdf")
			require.NoError(t, os.WriteFile(path, data, 0600))
			f.requireLibraryBytes(t, "versioned-private.pdf", data)
			f.record(t, path, data)
			status, code := http.StatusForbidden, "preview_source_not_in_library"
			if state == "missing" {
				require.NoError(t, os.Remove(path))
				require.NoFileExists(t, path)
			} else {
				changed := officepreview.PDF(2)
				require.NotEqual(t, officeRetryDigest(data), officeRetryDigest(changed))
				require.NoError(t, os.WriteFile(path, changed, 0600))
				f.requireLibraryBytes(t, "versioned-private.pdf", changed)
				status, code = http.StatusConflict, "preview_source_changed"
			}
			officeRetryError(t, f.retry(t, f.sid), status, code, path)
			require.Empty(t, f.conn.sendCh)
		})
	}
}

func TestOfficePreview_RetryNoSessionAccessIsIndistinguishable404(t *testing.T) {
	f := newOfficeRetryFixture(t)
	mainID, err := f.api.ensureMainSession(f.wsID, "mia", "alice")
	require.NoError(t, err)
	f.sid = mainID
	f.record(t, "report.pdf", officepreview.PDF(1))
	// Revoke the real main-session eligibility gate; owner attribution is NOT
	// access control. The stored main and preview continue to exist.
	ws, err := readWorkspaceFile(f.api.homePath, f.wsID)
	require.NoError(t, err)
	ws.CoreTeam = nil
	require.NoError(t, writeWorkspaceFile(f.api.homePath, ws))
	control := httptest.NewRecorder()
	f.api.HandleSessions(control, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+mainID, nil))
	require.Equal(t, http.StatusNotFound, control.Code, "real session access gate must refuse the fixture")
	_, err = f.api.agentLoop.GetSessionStore().GetMeta(mainID)
	require.NoError(t, err, "refusal is access loss, not a deleted session")
	denied := f.retry(t, mainID)
	require.Equal(t, http.StatusNotFound, denied.Code, "%s", denied.Body.String())
	unknown := f.retry(t, "unknown-office-session")
	require.Equal(t, http.StatusNotFound, unknown.Code, "%s", unknown.Body.String())
	require.JSONEq(t, unknown.Body.String(), denied.Body.String(), "no existence disclosure")
	require.Empty(t, f.conn.sendCh)
}
