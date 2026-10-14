package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestL1WU1ToolListings_CentralCatalogueOr503NeverAgentProxy(t *testing.T) {
	// FR-125 / §6.4: use a distinctive real builtin subset as the central
	// input. The expected output is those input names, not observed responses.
	for _, wired := range []bool{false, true} {
		t.Run(map[bool]string{false: "not_wired", true: "central_wired"}[wired], func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			wantNames := []string{"AskUserQuestion", "read_file"}
			if wired {
				api.builtinRegistry = tools.NewBuiltinRegistry()
				for _, tool := range tools.GeneralBuiltinMetadata() {
					if tool.Name() == "read_file" || tool.Name() == "AskUserQuestion" {
						require.NoError(t, api.builtinRegistry.RegisterBuiltin(tool))
					}
				}
				require.Equal(t, len(wantNames), api.builtinRegistry.Count(), "central input instrument control")
			}
			for _, endpoint := range []string{"tools", "registry"} {
				t.Run(endpoint, func(t *testing.T) {
					w := httptest.NewRecorder()
					r := httptest.NewRequest(http.MethodGet, "/api/v1/tools", nil)
					if endpoint == "tools" {
						api.HandleTools(w, r)
					} else {
						api.HandleToolsRegistry(w, r)
					}
					if !wired {
						assert.Equal(t, http.StatusServiceUnavailable, w.Code, "FR-125: unwired cannot borrow a default agent: %s", w.Body.String())
						assert.Contains(t, w.Body.String(), "tool registry not ready")
						return
					}
					require.Equal(t, http.StatusOK, w.Code)
					var rows []map[string]any
					require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
					got := []string{}
					for _, row := range rows {
						name, ok := row["name"].(string)
						require.True(t, ok)
						got = append(got, name)
					}
					sort.Strings(got)
					assert.Equal(t, wantNames, got, "central catalogue only; a live agent has a larger unrelated tool set")
				})
			}
		})
	}
}

func TestL1WU1WarmBrowser_DefaultWorkspaceWithoutAnyDefaultAgent(t *testing.T) {
	// FR-126 / BDD-13.11: a browser belongs to a workspace, never to whichever
	// agent happens to be selected by the old default resolution ladder.
	home := warmTestHome(t)
	t.Setenv("OMNIPUS_HOME", home)
	coord := newWarmTestCoordinator(t)
	defaultKey := warmTestWorkspace(t, home, "zzzdefault", "mia")
	salesKey := warmTestWorkspace(t, home, "aaasales", "jim")
	path := filepath.Join(home, "workspaces", "zzzdefault.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var ws map[string]any
	require.NoError(t, json.Unmarshal(raw, &ws))
	ws["is_default"] = true
	raw, err = json.Marshal(ws)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	def := newWarmTestManagerForKey(t, coord, defaultKey)
	sales := newWarmTestManagerForKey(t, coord, salesKey)
	cfg := config.DefaultConfig()
	t.Run("default_workspace_selected", func(t *testing.T) {
		chosen, reason := pickWarmBrowserManager(cfg, home, []*browser.BrowserManager{sales, def})
		assert.Empty(t, reason, "FR-126: existing default workspace is warmable without a default-agent setting")
		assert.Same(t, def, chosen, "default workspace wins even when Sales sorts first")
	})
	t.Run("default_manager_missing_never_warms_sales", func(t *testing.T) {
		chosen, reason := pickWarmBrowserManager(cfg, home, []*browser.BrowserManager{sales})
		assert.Nil(t, chosen, "no browser may be silently substituted")
		assert.Contains(t, reason, "default workspace", "visible skip reason must describe the actual missing manager")
	})
}
