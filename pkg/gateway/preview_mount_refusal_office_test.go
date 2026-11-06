//go:build darwin || linux

package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func officeMountPost(t *testing.T, api *restAPI, wsID, target string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(gen.WorkspaceMountCreateRequest{Name: "probe", HostPath: target})
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/"+wsID+"/mounts", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.handleWorkspaceMountCreate(w, r, wsID)
	return w
}

// T-86: drive the actual mount REST handler and picker HTTP projection, with
// exact spec texts. The component is outside OMNIPUS_HOME, so the existing
// data-directory refusal cannot make these look green by accident.
func TestOfficePreviewMountRESTAndPickerRefuseProgramFolders(t *testing.T) {
	if !officepreview.AtPackagedExecutable(t) {
		return
	}
	for _, shape := range []string{"root", "inside", "contains", "link", "contains_empty_packaged_slot"} {
		t.Run(shape, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			t.Setenv(config.EnvHome, api.homePath)
			wsID := createTestWorkspace(t, api, "T-86 mount refusal")
			root := filepath.Join(t.TempDir(), "office")
			officepreview.TrustedOffice(t, api.homePath, root)
			root, err := filepath.EvalSymlinks(root)
			require.NoError(t, err)
			target := root
			expected := []string{officepreview.ProgramMountRefusal("LibreOffice", root)}
			switch shape {
			case "inside":
				target = filepath.Join(root, "inside")
				require.NoError(t, os.Mkdir(target, 0700))
			case "contains":
				target = filepath.Dir(root)
			case "link":
				target = filepath.Join(t.TempDir(), "office-alias")
				require.NoError(t, os.Symlink(root, target))
			case "contains_empty_packaged_slot":
				resolver, err := component.NewResolver(api.homePath)
				require.NoError(t, err)
				slot := filepath.Clean(resolver.PackagedCandidates(component.KindOffice)[0])
				require.NoError(t, os.MkdirAll(slot, 0700))
				target = filepath.Dir(slot)
				expected = nil
				// This parent contains BOTH empty component slots. The spec
				// defines no tie-break; either exact eligible refusal is valid.
				for _, kind := range []component.Kind{component.KindOffice, component.KindPDFRenderer} {
					name := "LibreOffice"
					if kind == component.KindPDFRenderer {
						name = "PDF renderer"
					}
					for _, candidate := range resolver.PackagedCandidates(kind) {
						candidate = filepath.Clean(candidate)
						if filepath.Dir(candidate) == target {
							expected = append(expected, officepreview.ProgramMountRefusal(name, candidate))
						}
					}
				}
				require.Len(t, expected, 2, "instrument: the target contains precisely these two protected slots")
			}
			record, err := workspace.MountStorePath(api.homePath, wsID)
			require.NoError(t, err)
			require.NoFileExists(t, record)
			w := officeMountPost(t, api, wsID, target)
			assert.Equal(t, http.StatusForbidden, w.Code, "T-86 refusal: %s", w.Body.String())
			var response gen.ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			assert.Contains(t, expected, response.Error, "must use an exact §3.2 sentence, never the default data-directory reason")
			assert.NoFileExists(t, record, "refused save must create no mount record")
			_, statErr := os.Lstat(filepath.Join(workDir(api, wsID), "probe"))
			assert.ErrorIs(t, statErr, os.ErrNotExist, "refused save must create no work-folder link")
			// Directly exercise the same classification for link rows, which
			// the current directory enumerator otherwise omits entirely.
			row := api.classifyHostFolder(target, filepath.Base(target))
			assert.False(t, row.Mountable)
			require.NotNil(t, row.Reason, "picker must disclose why the folder is disabled")
			assert.Equal(t, "Contains or lies inside a document preview program folder — agents can never write there.", *row.Reason)
			if shape != "link" {
				picker := httptest.NewRecorder()
				api.HandleSystemFolders(picker, httptest.NewRequest(http.MethodGet, "/api/v1/system/folders?path="+url.QueryEscape(filepath.Dir(target)), nil))
				require.Equal(t, http.StatusOK, picker.Code)
				var listing gen.HostFolderListing
				require.NoError(t, json.Unmarshal(picker.Body.Bytes(), &listing))
				found := false
				for _, entry := range listing.Entries {
					if entry.Name == filepath.Base(target) {
						found = true
						assert.False(t, entry.Mountable)
						require.NotNil(t, entry.Reason)
						assert.Equal(t, "Contains or lies inside a document preview program folder — agents can never write there.", *entry.Reason)
					}
				}
				assert.True(t, found, "instrument: actual picker response contains the target row")
			}
		})
	}
	t.Run("unrelated_mount_control", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		t.Setenv(config.EnvHome, api.homePath)
		id := createTestWorkspace(t, api, "T-86 control")
		w := officeMountPost(t, api, id, t.TempDir())
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		mounts, ok := workspace.LoadMounts(api.homePath, id)
		require.True(t, ok)
		require.Len(t, mounts, 1)
	})
	t.Run("existing_refusal_texts", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		id := createTestWorkspace(t, api, "T-86 old refusals")
		for _, tc := range []struct{ path, message string }{
			{api.homePath, "that folder cannot be mounted: it is, contains or lies inside the Omnipus data directory, and mounting it would let an agent change Omnipus's own settings."},
			{"/etc", "that folder is an operating-system directory - no agent workspace lives there, and writing into it changes the whole machine. Mount a specific folder inside it if you really need one."},
		} {
			w := officeMountPost(t, api, id, tc.path)
			require.Equal(t, http.StatusForbidden, w.Code)
			var response gen.ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			require.Equal(t, tc.message, response.Error)
		}
	})
}
