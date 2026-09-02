package browser

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestRegisterTools_RewireMustApplyNewSecurityState pins the behaviour that
// registerSharedTools depends on: RegisterTools re-runs on EVERY hot reload
// (ReloadProviderAndConfig, any Settings save, UpsertAgentFast), and the
// freshly-constructed tools carry the operator's CURRENT security state —
// browser_evaluate's executeEnabled gate, browser_screenshot's workspace
// confinement, and the BrowserManager holding the current SSRFChecker.
//
// If a re-registration is discarded, the registry keeps serving the tools
// built from the PREVIOUS config. That is not cosmetic: turning
// browser_evaluate OFF in Settings would report success and change nothing,
// because EvaluateTool.executeEnabled is (per RegisterTools' own comment)
// "the SOLE live gate" on arbitrary JS execution in the agent's browser.
//
// Regression: #278's collision hardening made ToolRegistry.Register keep the
// incumbent and discard the newcomer. That is correct for an untrusted
// same-name claim, but browser tool re-wiring is a first-party, EXPECTED
// re-registration, so it must use RegisterReplacing.
func TestRegisterTools_RewireMustApplyNewSecurityState(t *testing.T) {
	reg := tools.NewToolRegistry()
	ssrf := security.NewSSRFChecker(nil)
	cfg := BrowserConfig{}

	// First wire: permissive — evaluate ON, workspace confinement OFF.
	if _, err := registerToolsForTest(t, reg, cfg, ssrf, true, "/home/first", false); err != nil {
		t.Fatalf("first RegisterTools: %v", err)
	}

	// Operator tightens the settings and saves; the reload re-wires.
	mgr2, err := registerToolsForTest(t, reg, cfg, ssrf, false, "/home/second", true)
	if err != nil {
		t.Fatalf("second RegisterTools: %v", err)
	}

	got, ok := reg.Get("browser_evaluate")
	if !ok {
		t.Fatal("browser_evaluate missing from registry after re-wire")
	}
	ev, ok := got.(*EvaluateTool)
	if !ok {
		t.Fatalf("browser_evaluate is %T, want *EvaluateTool", got)
	}
	if ev.executeEnabled {
		t.Error("SECURITY: browser_evaluate still has executeEnabled=true after the " +
			"operator disabled it and the config was re-wired — the live gate on " +
			"arbitrary JS execution was silently not applied")
	}

	gotShot, ok := reg.Get("browser_screenshot")
	if !ok {
		t.Fatal("browser_screenshot missing from registry after re-wire")
	}
	shot, ok := gotShot.(*ScreenshotTool)
	if !ok {
		t.Fatalf("browser_screenshot is %T, want *ScreenshotTool", gotShot)
	}
	if !shot.restrict {
		t.Error("SECURITY: browser_screenshot still has restrict=false after the " +
			"operator enabled workspace confinement — screenshot paths still escape " +
			"the agent workspace root")
	}
	if shot.agentHome != "/home/second" {
		t.Errorf("browser_screenshot agentHome = %q, want %q (stale instance retained)",
			shot.agentHome, "/home/second")
	}

	// Every registered tool must resolve through the CURRENT resolver. Under
	// ADR-072 FR-002a a tool holds no manager at all — it asks its resolver on
	// every Execute — so the staleness this guards against moved from "which
	// manager is bolted to the tool" to "which resolver is". The property is
	// the same one: a discarded re-registration leaves the registry serving
	// tools wired to the PREVIOUS config's SSRFChecker and browser config.
	nav, ok := reg.Get("browser_navigate")
	if !ok {
		t.Fatal("browser_navigate missing from registry after re-wire")
	}
	n, ok := nav.(*NavigateTool)
	if !ok {
		t.Fatalf("browser_navigate is %T, want *NavigateTool", nav)
	}
	resolved, _, _, resErr := n.res.ManagerFor(context.Background())
	if resErr != nil {
		t.Fatalf("browser_navigate could not resolve its manager: %v", resErr)
	}
	if resolved != mgr2 {
		t.Error("SECURITY: browser_navigate still resolves the PREVIOUS " +
			"BrowserManager — it keeps the old SSRFChecker and browser config")
	}
}
