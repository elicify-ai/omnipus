package gateway

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// Existing handler regression fixtures must dispatch a real request attached
// to the exact panel and use the production offer contract.
func prepareWebRTCHandlerFixture(t *testing.T, h *BrowserWSHandler, al *agent.AgentLoop, state *browserConnState, data []byte) ([]byte, uint64) {
	t.Helper()
	var frame generated.BrowserWebRTCOfferFrame
	parsed := json.Unmarshal(data, &frame) == nil
	agentID := frame.AgentId
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), agentID, h.sessionWorkspaceID(frame.SessionId))
	if outcome != agent.BrowserResolveOK {
		mgr, outcome = al.BrowserManagerForAgent(context.Background(), al.GetRegistry().GetDefaultAgent().ID, "")
	}
	if outcome != agent.BrowserResolveOK {
		t.Fatal("fixture cannot resolve its original browser manager")
	}
	sessionID := frame.SessionId
	if sessionID == "" {
		sessionID = "invalid-offer-fixture"
	}
	panel := "handler-fixture:" + sessionID
	if al.GetConfig().Tools.Browser.CDPURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := mgr.Live().AttachContext(ctx, panel, "handler-fixture-viewer", nil, nil, nil)
		cancel()
		if err != nil {
			t.Fatalf("attach handler CDP fixture: %v", err)
		}
		t.Cleanup(func() { mgr.Live().Detach(panel, "handler-fixture-viewer") })
	}
	if cs := mgr.CaptureSession(); cs != nil {
		if _, err := mgr.EnsureCaptureSessionForPanel(panel, func() (*browser.CaptureSession, error) { return cs, nil }); err != nil {
			t.Fatal(err)
		}
		current := cs.FrameState()
		if current.Width <= 0 || current.Height <= 0 {
			if _, err := cs.BeginFrameTransition("verified-target", 800, 600, 1); err != nil {
				t.Fatal(err)
			}
		}
	}
	attach := state.beginAttach()
	if !state.bindAttachment(attach, mgr, sessionID, panel) {
		t.Fatal("fixture cannot commit original attachment")
	}
	t.Cleanup(func() { state.invalidateWebRTCOffer(); state.clearAttachment() })
	if parsed {
		id := 1
		frame.OfferId = &id
		var err error
		data, err = json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
	}
	return data, state.beginWebRTCOffer()
}

// This adapter controls only the external negotiation boundary. Real relay
// request admission and candidate cleanup are covered by the relay tests.
type requestFixtureRelay struct {
	*fakeRelay
	requestMu sync.Mutex
	handles   map[string]*handlerViewerHandle
}

func newRequestFixtureRelay(base *fakeRelay) *requestFixtureRelay {
	return &requestFixtureRelay{fakeRelay: base, handles: make(map[string]*handlerViewerHandle)}
}
func (r *requestFixtureRelay) HandleViewerOfferHandle(viewer, sdp string) (string, any, error) {
	return r.HandleViewerOfferHandleRequest(context.Background(), context.Background(), 0, viewer, sdp)
}
func (r *requestFixtureRelay) HandleViewerOfferHandleRequest(ctx, parent context.Context, epoch uint64, viewer, sdp string) (string, any, error) {
	handle := &handlerViewerHandle{viewer: viewer, serial: int(epoch)}
	r.requestMu.Lock()
	r.handles[viewer] = handle
	r.requestMu.Unlock()
	r.mu.Lock()
	block, offerErr := r.viewerOfferBlock, r.viewerOfferErr
	r.mu.Unlock()
	if block != nil {
		select {
		case <-ctx.Done():
			return "", handle, ctx.Err()
		case <-parent.Done():
			return "", handle, parent.Err()
		case <-block:
		}
	}
	if ctx.Err() != nil {
		return "", handle, ctx.Err()
	}
	if offerErr != nil {
		return "", handle, offerErr
	}
	return "viewer-answer-" + viewer, handle, nil
}
func (r *requestFixtureRelay) IsViewerCurrent(value any) bool {
	handle, ok := value.(*handlerViewerHandle)
	if !ok || handle == nil {
		return false
	}
	r.requestMu.Lock()
	defer r.requestMu.Unlock()
	return r.handles[handle.viewer] == handle
}
func (r *requestFixtureRelay) CloseViewerIfCurrent(value any) {
	handle, ok := value.(*handlerViewerHandle)
	if !ok || handle == nil {
		return
	}
	r.requestMu.Lock()
	if r.handles[handle.viewer] != handle {
		r.requestMu.Unlock()
		return
	}
	delete(r.handles, handle.viewer)
	r.requestMu.Unlock()
	r.CloseViewer(handle.viewer)
}
func (r *requestFixtureRelay) pendingCount() int {
	r.requestMu.Lock()
	defer r.requestMu.Unlock()
	return len(r.handles)
}

// The external CDP endpoint is the only browser double. Handler attachment,
// manager/live-view state and measured capture refresh remain real.
func newMeasuredBrowserWSTestHandler(t *testing.T, mutate func(*config.Config)) (*BrowserWSHandler, *agent.AgentLoop) {
	t.Helper()
	endpoint, _ := newViewportCDPEndpoint(t, false)
	t.Cleanup(config.SetMemoryProviderForTest(func() (bool, bool) { return false, true }, func() (uint64, bool) { return 8 << 30, true }))
	return newBrowserWSTestHandler(t, func(cfg *config.Config) {
		if mutate != nil {
			mutate(cfg)
		}
		cfg.Tools.Browser.CDPURL = endpoint
		cfg.Tools.Browser.StartPageURL = "about:blank"
	})
}
func newMeasuredFixWaveHandlerWithAudit(t *testing.T, mutate func(*config.Config)) (*BrowserWSHandler, *agent.AgentLoop, string) {
	t.Helper()
	endpoint, _ := newViewportCDPEndpoint(t, false)
	t.Cleanup(config.SetMemoryProviderForTest(func() (bool, bool) { return false, true }, func() (uint64, bool) { return 8 << 30, true }))
	return newFixWaveHandlerWithAudit(t, func(cfg *config.Config) {
		if mutate != nil {
			mutate(cfg)
		}
		cfg.Tools.Browser.CDPURL = endpoint
		cfg.Tools.Browser.StartPageURL = "about:blank"
	})
}
