package browser

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
	fixture "github.com/elicify-ai/omnipus/tests/l1_red/protectedfixture"
)

// D6/T-FT. UploadFileTool is catalogued but held out of live registration;
// construction here exercises its existing implementation without re-enabling it.
func TestWU23_BrowserUploadProtectsInternalFiles(t *testing.T) {
	f := fixture.New(t)
	cfg := testBrowserCfg(t)
	cfg.ExecPath = requireBrowserOrFail(t) // never use a skipping fixture.
	registry, mgr := newPermissiveRegistry(t, cfg)
	srv := interactFixtureServer(t)
	ctx := tools.WithTurnWorkspaceDir(tools.WithWorkspaceID(tools.WithAgentID(t.Context(), "self"), "alpha"), f.Work)
	res := mustGetTool(t, registry, "browser_navigate").Execute(ctx, map[string]any{"url": srv.URL})
	if res.IsError {
		t.Fatalf("local upload fixture navigation failed: %s", res.ForLLM)
	}
	tool := &UploadFileTool{res: newFixedResolver(mgr), agentHome: f.AgentHome, restrict: false}
	t.Run("workspace_positive", func(t *testing.T) {
		res := tool.Execute(ctx, map[string]any{"selector": "#file-input", "path": filepath.Join(f.Work, "allowed.txt")})
		if res.IsError {
			t.Fatalf("A1 upload refused: %s", res.ForLLM)
		}
		if text := readFixtureText(t, registry, "#file-log"); text != "allowed.txt" {
			t.Fatalf("page received %q, want exact filename allowed.txt", text)
		}
	})
	for _, rel := range fixture.ProtectedRelative() {
		t.Run(rel, func(t *testing.T) {
			res := tool.Execute(ctx, map[string]any{"selector": "#file-input", "path": filepath.Join(f.Home, filepath.FromSlash(rel))})
			if !res.IsError {
				t.Errorf("D6: browser attached internal %s", rel)
			}
			if text := readFixtureText(t, registry, "#file-log"); text != "allowed.txt" {
				t.Errorf("D6: refused internal file reached page (filename=%q)", text)
			}
			if strings.Contains(res.ForLLM, "WU23_PROTECTED_SENTINEL") {
				t.Error("protected contents disclosed in upload result")
			}
		})
	}
}
