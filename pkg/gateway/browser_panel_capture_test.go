package gateway

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

func TestBrowserPanelCaptureFactoryUsesResolvedPanel(t *testing.T) {
	h, loop := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCMediaUDPPort = 0
	})
	defaultAgent := loop.GetRegistry().GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("fixture has no default agent")
	}
	agentID := defaultAgent.ID
	mgr, outcome := loop.BrowserManagerForAgent(context.Background(), agentID, "")
	if outcome != agent.BrowserResolveOK {
		t.Fatalf("manager fixture could not resolve: %v", outcome)
	}
	a, err := h.ensureCaptureSession(mgr, agentID, "panel-a", loop.GetConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Stop)
	b, err := h.ensureCaptureSession(mgr, agentID, "panel-b", loop.GetConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Stop)
	if a == b {
		t.Fatal("gateway factory reused another panel's capture")
	}
	if mgr.CaptureSessionForPanel("panel-a") != a || mgr.CaptureSessionForPanel("panel-b") != b {
		t.Fatal("gateway factory did not install captures under resolved panel IDs")
	}
	again, err := h.ensureCaptureSession(mgr, agentID, "panel-a", loop.GetConfig())
	if err != nil || again != a {
		t.Fatalf("same-panel viewer failed to reuse capture: got=%p err=%v", again, err)
	}
	key := mgr.BrowsingKey().String()
	for _, cs := range []*browser.CaptureSession{a, b} {
		if gotKey, got := h.captures.findByToken(cs.TokenHex()); gotKey != key || got != cs {
			t.Fatal("factory-created capture was not registered under its workspace")
		}
	}
	a.Stop()
	if _, got := h.captures.findByToken(a.TokenHex()); got != nil {
		t.Error("factory stop callback retained stopped panel token")
	}
	if gotKey, got := h.captures.findByToken(b.TokenHex()); gotKey != key || got != b {
		t.Error("factory stop callback removed sibling panel token")
	}
}

func TestBrowserPanelCaptureRegistryRetainsIndependentTokens(t *testing.T) {
	r := newCaptureRegistry()
	var captures []*browser.CaptureSession
	for range 2 {
		cs, err := browser.NewCaptureSessionWithDeps(nil, "registry-test", &fakeRelay{}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(cs.Stop)
		captures = append(captures, cs)
		r.set("workspace", cs)
	}
	for i, cs := range captures {
		key, got := r.findByToken(cs.TokenHex())
		if key != "workspace" || got != cs {
			t.Errorf("registry lost panel %d: workspace=%q capture=%p", i, key, got)
		}
	}
	if got := len(r.otherSessions("different-workspace")); got != 2 {
		t.Errorf("registry enumerated %d captures from other workspace, want two", got)
	}
	if got := len(r.otherSessions("workspace")); got != 0 {
		t.Errorf("registry treated %d same-workspace captures as conflicts", got)
	}
	r.removeIfCurrent("workspace", captures[0])
	if _, got := r.findByToken(captures[0].TokenHex()); got != nil {
		t.Error("registry retained removed panel token")
	}
	if key, got := r.findByToken(captures[1].TokenHex()); key != "workspace" || got != captures[1] {
		t.Error("removing one panel removed the other panel token")
	}
}
