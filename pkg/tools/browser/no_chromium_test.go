package browser

// no_chromium_test.go — FR-033 / S-55 (capability spec §10 order 25): on a host
// where no Chromium can be found, every one of ADR-075 D2's six new tools has
// to fail in a way that sends the operator to the right place.
//
// This is the linux/arm64 shipping state (#665) and it is not exotic: it is
// also every air-gapped host, every container built without a browser, and
// every developer machine before the first managed download completes. What
// makes it worth a test rather than a holdout is the failure mode it guards
// against — an error that reads as a BUG IN THE TOOL. An agent that is told
// "browser_hover failed" retries, reasons about its selector, and burns a turn;
// an agent told the browser is missing stops and says so.
//
// The stub is a resolver that can find nothing, built from four independent
// sources at once so the verdict does not depend on the developer's machine:
// an empty $PATH, a package-Chrome root with nothing in it, a managed install
// root under t.TempDir() that has never been written to, and a
// chrome-for-testing manifest server that refuses. Every resolution step
// therefore fails for a reason the test created.
//
// ── WHAT THIS TEST DOES NOT ASSERT, AND WHY ────────────────────────────────
//
// S-55's Then-clause is "every error names the missing browser AND THE INSTALL
// PATH". The traceability matrix's FR-033 row is the shorter "no-Chromium
// error names the missing browser". This file asserts the FR row. The install
// path is NOT in the message on this path and this test does not pretend
// otherwise: the resolver's install root appears only in the CORRUPT-managed-
// install error ("remove <installRoot> and retry", exec_resolver.go), never in
// the no-manifest / offline / nothing-installed error, which is the actual
// no-Chromium shape. Recorded as a finding rather than asserted, because
// asserting it would be red and asserting its absence would freeze it.
//
// The second deviation is browser_handle_dialog — see the table's own comment.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// newNoChromiumRegistry builds a manager whose Chromium resolution cannot
// succeed, and registers the tools against it.
//
// It never touches the network and never spawns a process: the manifest server
// is an httptest server that refuses, so the download step fails locally.
func newNoChromiumRegistry(t *testing.T) (*tools.ToolRegistry, *BrowserManager, string) {
	t.Helper()

	// Source 1 — $PATH has no candidate at all.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")

	// Source 2 — the package-Chrome root exists but holds no browser. Pinned
	// through the test hook rather than left to the real candidate list, so a
	// packaged Omnipus on the developer's own machine cannot resolve here and
	// turn this test green for the wrong reason.
	prevPkgRoot := packageChromeRootForTest
	packageChromeRootForTest = t.TempDir()
	t.Cleanup(func() { packageChromeRootForTest = prevPkgRoot })

	// Source 3 — the chrome-for-testing manifest refuses, so the managed
	// download cannot run. Local; no egress.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no manifest on this host", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	prevManifest := globalManifestURLForTesting
	globalManifestURLForTesting = srv.URL + "/manifest"
	t.Cleanup(func() { globalManifestURLForTesting = prevManifest })

	// Source 4 — the managed install root is a fresh temp dir with nothing in
	// it. (InstallRootForProfileDir derives it from ProfileDir, hence the
	// production-shaped <root>/browser/profiles/default layout.)
	cfg := BrowserConfig{
		Enabled:     true,
		Headless:    true,
		PageTimeout: 3 * time.Second,
		ProfileDir:  filepath.Join(t.TempDir(), "browser", "profiles", "default"),
	}

	registry := tools.NewToolRegistry()
	agentHome := t.TempDir()
	mgr, err := registerToolsForTest(
		t, registry, cfg, security.NewSSRFChecker([]string{"127.0.0.1"}),
		true /* evaluateEnabled */, agentHome, true /* restrict */)
	require.NoError(t, err)
	t.Cleanup(mgr.Shutdown)

	// The FR-060 memory gate is pinned OPEN. On a machine under real memory
	// pressure it would refuse the tab before resolution was ever attempted,
	// and every row below would pass its "this is not a tool bug" assertion
	// while proving nothing about the missing browser.
	mgr.memoryPressureFn = func(int) (bool, bool) { return false, true }

	return registry, mgr, agentHome
}

// toolDefectWords are phrasings that would make an agent treat a missing
// browser as its own mistake and retry. None may appear in any row's message.
var toolDefectWords = []string{
	"invalid argument",
	"unknown tool",
	"not implemented",
	"unsupported",
	"panic",
	"internal error",
}

// TestBrowserTools_NoChromium_ErrorNamesMissingBrowser is FR-033 / S-55.
func TestBrowserTools_NoChromium_ErrorNamesMissingBrowser(t *testing.T) {
	registry, mgr, agentHome := newNoChromiumRegistry(t)

	// A real file in the work dir, so browser_upload_file's path chokepoint
	// resolves and the call reaches the browser. Without it the row would fail
	// on the path instead, and would assert nothing about Chromium.
	require.NoError(t, os.WriteFile(filepath.Join(agentHome, "report.txt"), []byte("x"), 0o600))

	cases := []struct {
		name string
		args map[string]any
		// tool is nil for the registered five; browser_upload_file is
		// deliberately absent from the registry (FR-029) and is constructed.
		tool tools.Tool
		// namesMissingBrowser is false for the one row that never reaches
		// Chromium resolution — see the comment on that row.
		namesMissingBrowser bool
		// wantContains is what the row's error must say instead.
		wantContains string
	}{
		{
			name: "browser_select_option", args: map[string]any{"selector": "#s", "value": "a"},
			namesMissingBrowser: true,
		},
		{
			name: "browser_press_key", args: map[string]any{"key": "Enter"},
			namesMissingBrowser: true,
		},
		{
			name: "browser_hover", args: map[string]any{"selector": "#x"},
			namesMissingBrowser: true,
		},
		{
			name: "browser_snapshot", args: map[string]any{},
			namesMissingBrowser: true,
		},
		{
			// browser_upload_file is not registered (FR-029 holds it until
			// #659 closes), so it is constructed directly — exactly as
			// interact_e2e_test.go's newUploadTool does. It is in this table
			// because "held back" is a registration decision, not a reason to
			// leave its error path unmeasured.
			name: "browser_upload_file", args: map[string]any{"selector": "#f", "path": "report.txt"},
			namesMissingBrowser: true,
		},
		{
			// ── THE ONE ROW THAT DOES NOT NAME THE MISSING BROWSER ────────
			//
			// browser_handle_dialog never consults the exec-path resolver.
			// Its Execute goes straight to TakePendingDialog, which reads the
			// sessions map and returns ErrNoBrowsingContext when the browsing
			// context does not exist — and on a no-Chromium host it never
			// does, because nothing could ever start one.
			//
			// So its message explains the absence with D1.11's named failure
			// ("this turn is not rooted in a workspace"), which is the WRONG
			// CAUSE here: the turn is perfectly well rooted, and the operator
			// it sends to the workspace Team tab will find nothing wrong
			// there. Reported, not fixed in this unit: ErrNoBrowsingContext's
			// text is a behavioural contract (FR-008, key.go), so re-pointing
			// this path at a different error is a decision, not a test fix.
			//
			// The row is asserted at its ACTUAL text on purpose. It is not
			// silently skipped — a hole nobody can see is how this stayed
			// unmeasured in the first place — and if the misattribution is
			// ever corrected, this test fails and whoever corrects it flips
			// namesMissingBrowser to true in the same change.
			name: "browser_handle_dialog", args: map[string]any{"accept": false},
			namesMissingBrowser: false,
			wantContains:        "has no browser of its own",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool := tc.tool
			if tool == nil {
				if tc.name == "browser_upload_file" {
					tool = &UploadFileTool{res: newFixedResolver(mgr), agentHome: agentHome, restrict: true}
				} else {
					tool = mustGetTool(t, registry, tc.name)
				}
			}

			res := tool.Execute(context.Background(), tc.args)
			require.NotNil(t, res, "%s returned no result at all", tc.name)
			require.True(t, res.IsError,
				"%s reported SUCCESS on a host with no browser. A tool that cannot have done "+
					"anything must not say it did; got: %s", tc.name, res.ForLLM)

			assert.Contains(t, res.ForLLM, tc.name,
				"%s's error does not name the tool it came from, so an agent reading a turn with "+
					"several browser calls in it cannot tell which one failed; got: %s",
				tc.name, res.ForLLM)

			if tc.namesMissingBrowser {
				assert.Contains(t, res.ForLLM, "cannot locate chromium",
					"%s does not name the missing browser. This is the linux/arm64 shipping state "+
						"(#665) and every air-gapped host: without those words the agent has no way "+
						"to tell 'this machine has no browser' from 'my selector was wrong', and it "+
						"retries instead of reporting; got: %s", tc.name, res.ForLLM)
			} else {
				assert.Contains(t, res.ForLLM, tc.wantContains,
					"%s's no-browser error changed. See this row's comment: it is the one tool that "+
						"never reaches Chromium resolution, and its message is pinned so the "+
						"deviation stays visible; got: %s", tc.name, res.ForLLM)
			}

			for _, word := range toolDefectWords {
				assert.NotContains(t, res.ForLLM, word,
					"%s's missing-browser error reads as a defect in the tool (%q). S-55's whole "+
						"point is that this failure is a HOST fact, and an agent that reads it as a "+
						"tool bug retries a call that can never work; got: %s",
					tc.name, word, res.ForLLM)
			}
		})
	}
}

// TestBrowserTools_NoChromium_TableCoversEveryNewTool stops the table above
// from quietly shrinking. FR-033's coverage claim is "all six", and a table
// that lost a row would still be green.
func TestBrowserTools_NoChromium_TableCoversEveryNewTool(t *testing.T) {
	// The six ADR-075 D2 additions, listed here independently of the table so
	// the two have to agree.
	want := []string{
		"browser_select_option",
		"browser_press_key",
		"browser_hover",
		"browser_upload_file",
		"browser_snapshot",
		"browser_handle_dialog",
	}
	registered := make(map[string]bool, len(BrowserBuiltinMetadata()))
	for _, tool := range BrowserBuiltinMetadata() {
		registered[tool.Name()] = true
	}
	for _, name := range want {
		assert.True(t, registered[name],
			"%s is not in BrowserBuiltinMetadata. Either the tool was renamed and FR-033's table "+
				"is now testing a name nothing answers to, or it was removed and this list is "+
				"stale", name)
	}
}
