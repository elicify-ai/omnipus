//go:build darwin || linux

package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// B10 / FR-049 / §3.2. This is the server creation boundary, complementary
// to the real-turn test in pkg/agent. Test names intentionally stay neutral.
func TestOfficeUAT_B10Server(t *testing.T) {
	for _, row := range []string{"case-1", "case-2"} {
		t.Run(row, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			t.Setenv(config.EnvHome, api.homePath)
			id := createTestWorkspace(t, api, "Office UAT")
			root := officepreview.OwnRoot(t)
			name := "LibreOffice"
			if row == "case-1" {
				officepreview.TrustedOffice(t, api.homePath, root)
			} else {
				name = "PDF renderer"
				officepreview.RegisteredRendererFixture(t, api.homePath, root, true)
			}
			resolved, err := filepath.EvalSymlinks(root)
			require.NoError(t, err)
			record, err := workspace.MountStorePath(api.homePath, id)
			require.NoError(t, err)
			require.NoFileExists(t, record)
			response := officeMountPost(t, api, id, resolved)
			assert.Equal(t, http.StatusForbidden, response.Code)
			var body gen.ErrorResponse
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			assert.Equal(t, officepreview.ProgramMountRefusal(name, resolved), body.Error)
			assert.NoFileExists(t, record)
			_, err = os.Lstat(filepath.Join(workDir(api, id), "probe"))
			assert.ErrorIs(t, err, os.ErrNotExist)

			ordinary, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			control := officeMountPost(t, api, id, ordinary)
			require.Equal(t, http.StatusCreated, control.Code, "instrument: the same server creates an ordinary mount: %s", control.Body.String())
			mounts, readable := workspace.LoadMounts(api.homePath, id)
			require.True(t, readable)
			require.Len(t, mounts, 1)
			assert.Equal(t, ordinary, mounts[0].HostPath)
		})
	}
}
