package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/captureext"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
	"github.com/stretchr/testify/require"
)

// Run alone: OMNIPUS_WEBRTC_REPRO=1 go test ./pkg/tools/browser -run '^TestCaptureInputPressureRealChrome$' -count=1 -timeout=2m.
func TestCaptureInputPressureRealChrome(t *testing.T) {
	if os.Getenv("OMNIPUS_WEBRTC_REPRO") != "1" {
		t.Skip("set OMNIPUS_WEBRTC_REPRO=1 to run real capture pressure verification")
	}
	execPath := requireBrowserOrFail(t)
	extDir, err := captureext.Seed(t.TempDir())
	require.NoError(t, err)
	coord, _ := spikeLaunchChrome(t, "input-pressure", execPath, extDir)
	mgr, err := NewBrowserManager(BrowserConfig{
		Enabled: true, Headless: true, PageTimeout: 30 * time.Second,
		ExecPath: execPath, ExtensionDir: extDir, ExtensionID: captureext.ExtensionID, TrustPathChrome: true,
	}, security.NewSSRFChecker(nil))
	require.NoError(t, err)
	mgr.AttachSharedChrome(coord, browserTestKey("input-pressure"))
	t.Cleanup(mgr.Shutdown)
	ingest := &reproIngestServer{logf: t.Logf}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/browser/capture-ingest", ingest.handle)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cs, err := NewCaptureSession(mgr, "input-pressure", mgr.OperatorSessionID(), webrtc.Config{StunServer: ""}, func(string, []byte) {}, t.Logf)
	require.NoError(t, err)
	ingest.setSession(cs)
	t.Cleanup(cs.Stop)
	_, err = cs.Start(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/v1/browser/capture-ingest")
	require.NoError(t, err)
	require.Eventually(t, func() bool { return cs.Stats().VideoPackets > 0 }, 45*time.Second, 100*time.Millisecond)
	cs.mu.Lock()
	encoder := cs.tabCtx
	cs.mu.Unlock()
	require.NotNil(t, encoder)
	ctx, cancel := context.WithTimeout(encoder, 30*time.Second)
	defer cancel()
	var original struct{ Width, Height int }
	require.NoError(t, chromedp.Run(ctx, chromedp.Evaluate(`(() => {const s=currentPC.getSenders().find(s=>s.track&&s.track.kind==='video').track.getSettings();return {Width:s.width,Height:s.height};})()`, &original)))
	// Timing policy is covered with an injected clock in the Node harness.
	// Here advance only its timestamps to verify actual Chrome settings quickly.
	for _, step := range []struct {
		action         string
		source, sender int
	}{
		{`await handleControlFrame({action:'input_pressure'});`, 20, 20},
		{`inputPressureChangedAt=Date.now()-1000;await handleControlFrame({action:'input_pressure'});`, 15, 15},
		{`inputPressureChangedAt=inputPressureLastAt=Date.now()-10000;await recoverInputPressure(currentPC);`, 20, 20},
		{`inputPressureChangedAt=inputPressureLastAt=Date.now()-10000;await recoverInputPressure(currentPC);`, 30, 0},
	} {
		var actual struct {
			Source        float64
			Sender        float64
			Width, Height int
			Diagnostics   string
		}
		expression := `(async()=>{` + step.action + `const s=currentPC.getSenders().find(s=>s.track&&s.track.kind==='video');const c=s.track.getSettings();return {Source:c.frameRate,Sender:s.getParameters().encodings[0].maxFramerate||0,Width:c.width,Height:c.height,Diagnostics:JSON.stringify({constraints:s.track.getConstraints(),settings:c,parameters:s.getParameters(),index:inputPressureIndex,needsApply:inputPressureNeedsApply,pressure:window.__omnipusState.inputPressure,pressureError:window.__omnipusState.inputPressureError,lastError:window.__omnipusState.lastError,history:window.__omnipusState.history.slice(-8)})};})()`
		require.NoError(t, chromedp.Run(ctx, chromedp.Evaluate(expression, &actual, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) })))
		t.Logf("pressure diagnostics: %s", actual.Diagnostics)
		if actual.Source != float64(step.source) {
			var experiments string
			probe := `(async()=>{const t=currentPC.getSenders().find(s=>s.track&&s.track.kind==='video').track;await new Promise(r=>setTimeout(r,200));const out={after200ms:t.getSettings(),probes:[]};for(const mode of ['standard-only','legacy-updated']){const c=t.clone();const original=c.getConstraints();const constraints=mode==='standard-only'?{frameRate:{max:20}}:{...original,mandatory:{...(original.mandatory||{}),maxFrameRate:20}};try{await c.applyConstraints(constraints);await new Promise(r=>setTimeout(r,200));out.probes.push({mode,constraints:c.getConstraints(),settings:c.getSettings()});}catch(e){out.probes.push({mode,error:String(e),name:e.name,constraint:e.constraint});}finally{c.stop();}}return JSON.stringify(out);})()`
			require.NoError(t, chromedp.Run(ctx, chromedp.Evaluate(probe, &experiments, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) })))
			t.Logf("capture constraint experiments: %s", experiments)
		}
		require.Equal(t, float64(step.source), actual.Source, "actual capture frame-rate setting")
		require.Equal(t, float64(step.sender), actual.Sender, "actual encoder frame-rate setting")
		require.Equal(t, original.Width, actual.Width)
		require.Equal(t, original.Height, actual.Height)
		t.Logf("actual capture=%gfps encoder ceiling=%gfps dimensions=%dx%d", actual.Source, actual.Sender, actual.Width, actual.Height)
	}
}
