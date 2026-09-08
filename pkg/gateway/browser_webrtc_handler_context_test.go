package gateway

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

type handlerViewerHandle struct {
	viewer string
	serial int
}
type handlerContextRelay struct {
	fakeRelay
	requestMu           sync.Mutex
	current             map[string]*handlerViewerHandle
	requests, legacy    int
	parent, negotiation context.Context
	enter               chan struct{}
	release             chan struct{}
}

func (r *handlerContextRelay) HandleViewerOfferHandle(viewer, sdp string) (string, any, error) {
	return r.offer(context.Background(), context.Background(), false, viewer)
}
func (r *handlerContextRelay) HandleViewerOfferHandleRequest(ctx, parent context.Context, _ uint64, viewer, sdp string) (string, any, error) {
	return r.offer(ctx, parent, true, viewer)
}
func (r *handlerContextRelay) offer(ctx, parent context.Context, request bool, viewer string) (string, any, error) {
	r.requestMu.Lock()
	if request {
		r.requests++
		r.parent = parent
		r.negotiation = ctx
	} else {
		r.legacy++
	}
	enter, release := r.enter, r.release
	r.requestMu.Unlock()
	if enter != nil {
		select {
		case enter <- struct{}{}:
		default:
		}
	}
	if release != nil {
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-release:
		}
	}
	r.requestMu.Lock()
	defer r.requestMu.Unlock()
	if r.current == nil {
		r.current = make(map[string]*handlerViewerHandle)
	}
	h := &handlerViewerHandle{viewer: viewer, serial: r.requests + r.legacy}
	r.current[viewer] = h
	return "handler-fixture-answer", h, nil
}
func (r *handlerContextRelay) IsViewerCurrent(handle any) bool {
	h, ok := handle.(*handlerViewerHandle)
	if !ok || h == nil {
		return false
	}
	r.requestMu.Lock()
	defer r.requestMu.Unlock()
	return r.current[h.viewer] == h
}
func (r *handlerContextRelay) CloseViewerIfCurrent(handle any) {
	h, ok := handle.(*handlerViewerHandle)
	if !ok || h == nil {
		return
	}
	r.requestMu.Lock()
	defer r.requestMu.Unlock()
	if r.current[h.viewer] == h {
		delete(r.current, h.viewer)
	}
}

type handlerContextFixture struct {
	handler         *BrowserWSHandler
	cfg             *config.Config
	manager         *browser.BrowserManager
	capture         *browser.CaptureSession
	relay           *handlerContextRelay
	state           *browserConnState
	conn            *browserWSConn
	agentID         string
	original        context.Context
	observeViewport func(int, int, float64)
}

func newHandlerContextFixture(t *testing.T, pending bool, metricsHooks ...func(int, int, float64)) handlerContextFixture {
	t.Helper()
	t.Cleanup(config.SetMemoryProviderForTest(func() (bool, bool) { return false, true }, func() (uint64, bool) { return 8 << 30, true }))
	cdpURL, observeViewport, discovered := newViewportCDPEndpoint(t, pending, metricsHooks...)
	dir := t.TempDir()
	h, al := newBrowserWSTestHandler(t, func(c *config.Config) {
		c.Tools.Browser.WebRTCEnabled = true
		c.Tools.Browser.CDPURL = cdpURL
		c.Tools.Browser.StartPageURL = "about:blank"
		c.Tools.Browser.ExecPath = filepath.Join(dir, "no-such-chrome-binary")
		c.Tools.Browser.ProfileDir = filepath.Join(dir, "profile")
	})
	agentID := al.GetRegistry().GetDefaultAgent().ID
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), agentID, "")
	if outcome != agent.BrowserResolveOK || !mgr.CaptureVideoCapability().Capable {
		t.Fatal("fixture browser manager is not capture capable")
	}
	attachContext, cancelAttach := context.WithTimeout(context.Background(), 5*time.Second)
	_, attachErr := mgr.Live().AttachContext(attachContext, "panel", "fixture-viewer", nil, nil, nil)
	cancelAttach()
	if attachErr != nil {
		t.Fatalf("attach measured CDP fixture: %v", attachErr)
	}
	t.Cleanup(func() { mgr.Live().Detach("panel", "fixture-viewer"); mgr.Shutdown() })
	awaitViewportDocumentDiscovery(t, discovered)
	relay := &handlerContextRelay{}
	var starts int32
	cs, err := browser.NewCaptureSessionWithDeps(nil, agentID, relay, fakeEncoderStarter(&starts, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	width, height := 800, 600
	if pending {
		width, height = 0, 0
	}
	if _, err := cs.BeginFrameTransition("verified-target", width, height, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.EnsureCaptureSessionForPanel("panel", func() (*browser.CaptureSession, error) { return cs, nil }); err != nil {
		t.Fatal(err)
	}
	h.captures.set(mgr.BrowsingKey().String(), cs)
	state := &browserConnState{}
	attach := state.beginAttach()
	if !state.bindAttachment(attach, mgr, "chat", "panel") {
		t.Fatal("fixture attachment failed")
	}
	t.Cleanup(func() { state.invalidateWebRTCOffer(); state.clearAttachment(); cs.Stop(); h.Wait() })
	return handlerContextFixture{h, al.GetConfig(), mgr, cs, relay, state, newTestBrowserWSConn(), agentID, state.attachmentRequest().ctx, observeViewport}
}
func (f handlerContextFixture) offer(t *testing.T, claim *browser.CaptureFrameState) []byte {
	t.Helper()
	offerID := 7
	frame := generated.BrowserWebRTCOfferFrame{Type: string(generated.WsFrameTypeBrowserWebrtcOffer), AgentId: f.agentID, SessionId: "chat", Sdp: "v=0\r\n", OfferId: &offerID}
	if claim != nil {
		id, gen := claim.CaptureID, int(claim.Generation)
		frame.CaptureId = &id
		frame.CaptureGeneration = &gen
	}
	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func answerEnvelope(t *testing.T, wc *browserWSConn) browserOutboundFrame {
	t.Helper()
	for {
		select {
		case frame := <-wc.sendCh:
			var header struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(frame.data, &header); err != nil {
				t.Fatal(err)
			}
			if header.Type == string(generated.WsFrameTypeBrowserWebrtcAnswer) {
				return frame
			}
		case <-time.After(time.Second):
			t.Fatal("no viewer answer reached its outbound queue")
			return browserOutboundFrame{}
		}
	}
}
func TestWebRTCHandlerUsesOriginalRouteAndScopedConfirmedAnswer(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	expected := f.capture.FrameState()
	epoch := f.state.beginWebRTCOffer()
	f.handler.handleWebRTCOffer(f.conn, f.state, "viewer", "user", f.offer(t, nil), f.cfg, epoch)
	f.relay.requestMu.Lock()
	requests, legacy, parent, negotiation := f.relay.requests, f.relay.legacy, f.relay.parent, f.relay.negotiation
	f.relay.requestMu.Unlock()
	if requests != 1 || legacy != 0 || parent == nil || negotiation == nil {
		t.Errorf("production offer bypassed context request: requests=%d legacy=%d", requests, legacy)
	} else {
		route, ok := parent.Value(webRTCInputRouteKey{}).(webRTCInputRoute)
		if !ok || route.manager != f.manager || route.panelSessionID != "panel" || route.attachment != f.original || parent.Err() != nil || negotiation.Err() != context.Canceled {
			t.Errorf("production route/lifetimes differ: route=%+v parent=%v negotiation=%v", route, parent.Err(), negotiation.Err())
		}
	}
	queued := answerEnvelope(t, f.conn)
	var answer generated.BrowserWebRTCAnswerFrame
	if err := json.Unmarshal(queued.data, &answer); err != nil {
		t.Fatal(err)
	}
	if answer.CaptureId == nil || *answer.CaptureId != expected.CaptureID || answer.CaptureGeneration == nil || *answer.CaptureGeneration != int(expected.Generation) || answer.OfferId == nil || *answer.OfferId != 7 {
		t.Errorf("answer lost immutable claims: %+v", answer)
	}
	if queued.source != f.original || queued.current == nil || !f.conn.canSendFrame(queued) {
		t.Error("answer has no surviving original-attachment write scope")
	}
	f.state.beginAttach()
	if f.conn.canSendFrame(queued) {
		t.Error("queued old answer remains writable after attachment replacement")
	}
}
func TestWebRTCHandlerRejectsStaleClaimBeforeRelay(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	old := f.capture.FrameState()
	if _, err := f.capture.BeginFrameTransition("next-target", 1024, 768, 1); err != nil {
		t.Fatal(err)
	}
	epoch := f.state.beginWebRTCOffer()
	f.handler.handleWebRTCOffer(f.conn, f.state, "viewer", "user", f.offer(t, &old), f.cfg, epoch)
	f.relay.requestMu.Lock()
	calls := f.relay.requests + f.relay.legacy
	f.relay.requestMu.Unlock()
	if calls != 0 || f.capture.ViewerCount() != 0 {
		t.Errorf("stale frame reached relay: calls=%d viewers=%d", calls, f.capture.ViewerCount())
	}
	for len(f.conn.sendCh) > 0 {
		queued := <-f.conn.sendCh
		var answer generated.BrowserWebRTCAnswerFrame
		_ = json.Unmarshal(queued.data, &answer)
		if answer.Type == string(generated.WsFrameTypeBrowserWebrtcAnswer) {
			t.Error("stale claim produced an answer")
		}
	}
}
func TestWebRTCHandlerCanceledAttachmentStopsOfferAndReplies(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	f.relay.enter = make(chan struct{}, 1)
	f.relay.release = make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(f.relay.release) })
	epoch := f.state.beginWebRTCOffer()
	raw := f.offer(t, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.handler.handleWebRTCOffer(f.conn, f.state, "viewer", "user", raw, f.cfg, epoch)
	}()
	select {
	case <-f.relay.enter:
	case <-time.After(time.Second):
		t.Fatal("offer did not reach relay")
	}
	f.state.clearAttachment()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("canceled original attachment left offer alive")
		release.Do(func() { close(f.relay.release) })
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("released offer did not drain")
		}
	}
	if f.capture.ViewerCount() != 0 {
		t.Error("canceled offer retained active capture registration")
	}
	for len(f.conn.sendCh) > 0 {
		queued := <-f.conn.sendCh
		if f.conn.canSendFrame(queued) {
			t.Errorf("canceled source retained writable reply: %s", queued.data)
		}
	}
}

func TestWebRTCHandlerInitialOfferWaitsForMeasuredGeometry(t *testing.T) {
	f := newHandlerContextFixture(t, true)
	f.relay.enter = make(chan struct{}, 1)
	epoch := f.state.beginWebRTCOffer()
	raw := f.offer(t, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.handler.handleWebRTCOffer(f.conn, f.state, "viewer", "user", raw, f.cfg, epoch)
	}()
	select {
	case <-f.relay.enter:
		t.Error("unmeasured initial frame reached relay negotiation")
	case <-time.After(50 * time.Millisecond):
	}
	measured, err := f.capture.BeginFrameTransition("verified-target", 641, 479, 1.25)
	if err != nil {
		t.Fatal(err)
	}
	f.observeViewport(641, 479, 1.25)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("measured initial offer did not finish")
	}
	queued := answerEnvelope(t, f.conn)
	var answer generated.BrowserWebRTCAnswerFrame
	if err := json.Unmarshal(queued.data, &answer); err != nil {
		t.Fatal(err)
	}
	if answer.CaptureId == nil || *answer.CaptureId != measured.CaptureID || answer.CaptureGeneration == nil || *answer.CaptureGeneration != int(measured.Generation) {
		t.Errorf("initial answer did not bind first confirmed frame: %+v", answer)
	}
}

func TestWebRTCHandlerCaptureStopCancelsBeforeRelayShutdown(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	f.relay.enter = make(chan struct{}, 1)
	f.relay.release = make(chan struct{})
	controlEntered, controlRelease := make(chan struct{}), make(chan struct{})
	var releaseRelay, releaseControl, enterControl sync.Once
	defer releaseRelay.Do(func() { close(f.relay.release) })
	defer releaseControl.Do(func() { close(controlRelease) })
	f.capture.BindIngest(func(action string, _ *string, _, _, _ int) error {
		if action == "shutdown" {
			enterControl.Do(func() { close(controlEntered) })
			<-controlRelease
		}
		return nil
	}, func() {})
	epoch, raw := f.state.beginWebRTCOffer(), f.offer(t, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.handler.handleWebRTCOffer(f.conn, f.state, "viewer", "user", raw, f.cfg, epoch)
	}()
	select {
	case <-f.relay.enter:
	case <-time.After(time.Second):
		t.Fatal("offer did not reach relay")
	}
	stopped := make(chan struct{})
	go func() { defer close(stopped); f.capture.Stop() }()
	select {
	case <-controlEntered:
	case <-time.After(time.Second):
		t.Fatal("capture stop did not reach blocked shutdown control")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("capture stop left negotiation waiting for relay shutdown")
		releaseRelay.Do(func() { close(f.relay.release) })
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("released negotiation did not drain")
		}
	}
	if f.original.Err() != nil {
		t.Error("capture stop canceled the independent original attachment")
	}
	for len(f.conn.sendCh) > 0 {
		queued := <-f.conn.sendCh
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(queued.data, &header); err != nil {
			t.Fatal(err)
		}
		if header.Type == string(generated.WsFrameTypeBrowserWebrtcAnswer) && f.conn.canSendFrame(queued) {
			t.Error("stopped capture retained a writable answer")
		}
	}
	releaseControl.Do(func() { close(controlRelease) })
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("released capture stop did not drain")
	}
}

func TestWebRTCHandlerInputErrorKeepsInstalledSourceDuringReplacementAttempt(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	epoch := f.state.beginWebRTCOffer()
	f.handler.handleWebRTCOffer(f.conn, f.state, "viewer", "user", f.offer(t, nil), f.cfg, epoch)
	f.relay.requestMu.Lock()
	parent := f.relay.parent
	f.relay.requestMu.Unlock()
	if parent == nil {
		t.Fatal("handler did not establish an original input route")
	}
	route, ok := parent.Value(webRTCInputRouteKey{}).(webRTCInputRoute)
	if !ok || route.report == nil {
		t.Fatal("handler did not retain its original error callback")
	}
	for len(f.conn.sendCh) > 0 {
		<-f.conn.sendCh
	}
	source, cancel := context.WithCancel(parent)
	defer cancel()
	f.state.beginWebRTCOffer()
	route.report(source, "keyDown", context.DeadlineExceeded)
	select {
	case queued := <-f.conn.sendCh:
		var status generated.BrowserStatusFrame
		if err := json.Unmarshal(queued.data, &status); err != nil {
			t.Fatal(err)
		}
		if status.OperationOnly == nil || !*status.OperationOnly || queued.source != source || !f.conn.canSendFrame(queued) {
			t.Fatal("pending replacement hid or mis-scoped an installed peer's input failure")
		}
		cancel()
		if f.conn.canSendFrame(queued) {
			t.Fatal("retired input source retained a writable error")
		}
	case <-time.After(time.Second):
		t.Fatal("installed peer's error disappeared during a later negotiation")
	}
}

func TestWebRTCHandlerPublishesHealthBeforeWaitingForAnswerCapacity(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	expected := f.capture.FrameState()
	for len(f.conn.sendCh) < cap(f.conn.sendCh) {
		f.conn.sendCh <- browserOutboundFrame{data: []byte(`{"type":"fixture"}`)}
	}
	done := make(chan struct{})
	epoch, raw := f.state.beginWebRTCOffer(), f.offer(t, nil)
	go func() {
		defer close(done)
		f.handler.handleWebRTCOffer(f.conn, f.state, "viewer", "user", raw, f.cfg, epoch)
	}()
	defer func() {
		f.state.clearAttachment()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("canceled answer-capacity waiter did not drain")
		}
	}()
	select {
	case <-f.conn.latestWake():
	case <-time.After(time.Second):
		t.Fatal("handler waited for answer capacity before publishing confirmed health")
	}
	queued, ok := f.conn.takeLatestFrame()
	if !ok {
		t.Fatal("handler did not publish a current health snapshot")
	}
	var health generated.BrowserVideoHealthFrame
	if err := json.Unmarshal(queued.data, &health); err != nil {
		t.Fatal(err)
	}
	if health.CaptureId == nil || *health.CaptureId != expected.CaptureID || health.CaptureGeneration == nil || *health.CaptureGeneration != int(expected.Generation) ||
		health.TargetId == nil || *health.TargetId != expected.TargetID || health.CssWidth == nil || *health.CssWidth != 800 || health.CssHeight == nil || *health.CssHeight != 600 ||
		health.RtpTimestamp != nil || queued.source != f.original || !f.conn.canSendFrame(queued) {
		t.Fatalf("handler health lost confirmed original metadata or invented presentation: %+v", health)
	}
	f.state.clearAttachment()
	if f.conn.canSendFrame(queued) {
		t.Fatal("retired attachment retained writable initial health")
	}
}
