package browser

import (
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
	if _, err := RegisterTools(reg, cfg, ssrf, true, "/home/first", false); err != nil {
		t.Fatalf("first RegisterTools: %v", err)
	}

	// Operator tightens the settings and saves; the reload re-wires.
	mgr2, err := RegisterTools(reg, cfg, ssrf, false, "/home/second", true)
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

	// Every registered tool must belong to the CURRENT manager. A stale tool
	// keeps a manager built with the previous SSRFChecker and browser config.
	if nav, ok := reg.Get("browser_navigate"); ok {
		if n, ok := nav.(*NavigateTool); ok && n.mgr != mgr2 {
			t.Error("SECURITY: browser_navigate still points at the PREVIOUS " +
				"BrowserManager — it keeps the old SSRFChecker and browser config")
		}
	}
}
