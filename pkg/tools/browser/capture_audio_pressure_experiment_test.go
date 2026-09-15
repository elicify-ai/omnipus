package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
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

// This is a diagnostic experiment, not input or audio-quality acceptance. Its
// independent contract is a fixed 120ms task every 250ms and three 60s phases
// at 30, 10, 30 FPS, followed by 60s at 30 FPS without the busy task.
// Native timing is measured, never asserted to be realtime. BusyMS measures
// wall time, not CPU consumption: host preemption contributes to this duration.
// Run alone with OMNIPUS_AUDIO_CAPTURE_EXPERIMENT=1 and -timeout=15m.
// OMNIPUS_AUDIO_CAPTURE_WARMUP_SECONDS optionally adds 0..480s of load.
// No remote viewer is attached: this isolates source/capture/encode, not playout.
func TestCaptureAudioPressureExperiment(t *testing.T) {
	if os.Getenv("OMNIPUS_AUDIO_CAPTURE_EXPERIMENT") != "1" {
		t.Skip("set OMNIPUS_AUDIO_CAPTURE_EXPERIMENT=1 to run native audio pressure diagnostic")
	}
	warmup, err := audioPressureWarmup(os.Getenv("OMNIPUS_AUDIO_CAPTURE_WARMUP_SECONDS"))
	require.NoError(t, err)
	execPath := requireBrowserOrFail(t)
	extDir, err := captureext.Seed(t.TempDir())
	require.NoError(t, err)
	coord, _ := spikeLaunchChrome(t, "audio-pressure-experiment", execPath, extDir)
	mgr, err := NewBrowserManager(BrowserConfig{
		Enabled: true, Headless: true, PageTimeout: 30 * time.Second,
		ExecPath: execPath, ExtensionDir: extDir, ExtensionID: captureext.ExtensionID, TrustPathChrome: true,
	}, security.NewSSRFChecker(nil))
	require.NoError(t, err)
	mgr.AttachSharedChrome(coord, browserTestKey("audio-pressure-experiment"))
	t.Cleanup(mgr.Shutdown)
	ingest := &reproIngestServer{logf: t.Logf}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/browser/capture-ingest", ingest.handle)
	mux.HandleFunc("/stimulus", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(audioPressureExperimentPage))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	source, err := mgr.Session(mgr.OperatorSessionID())
	require.NoError(t, err)
	boundedRun := func(ctx context.Context, actions ...chromedp.Action) error {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		return chromedp.Run(ctx, actions...)
	}
	require.NoError(t, boundedRun(source, chromedp.Navigate(srv.URL+"/stimulus")))
	cs, err := NewCaptureSession(mgr, "audio-pressure-experiment", mgr.OperatorSessionID(), webrtc.Config{StunServer: ""}, func(string, []byte) {}, t.Logf)
	require.NoError(t, err)
	ingest.setSession(cs)
	t.Cleanup(cs.Stop)
	startCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	_, err = cs.Start(startCtx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/v1/browser/capture-ingest")
	require.NoError(t, err)
	require.Eventually(t, func() bool { return cs.Stats().VideoPackets > 0 }, 45*time.Second, 100*time.Millisecond)
	cs.mu.Lock()
	encoder := cs.tabCtx
	cs.mu.Unlock()
	require.NotNil(t, encoder)
	eval := func(ctx context.Context, script string, result any) error {
		return boundedRun(ctx, chromedp.Evaluate(script, result, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }))
	}
	// Invalidate an in-flight adaptive tick before draining parameter operations.
	// Keep the original functions/track parameters only inside this encoder page.
	require.NoError(t, eval(encoder, `(async()=>{
const sender=currentPC.getSenders().find(s=>s.track&&s.track.kind==='video');
window.__audioExperiment={pc:currentPC,sender,generation:captureGeneration,
 adaptTick,recoverInputPressure,noteInputPressure,timer:!!adaptTimer,
 constraints:sender.track.getConstraints(),parameters:sender.getParameters()};
adaptEpoch++; if(adaptTimer){clearInterval(adaptTimer);adaptTimer=null;}
adaptTick=async()=>null;recoverInputPressure=async()=>{};noteInputPressure=async()=>{};
if(inputPressureApplying)await inputPressureApplying;await senderParamsChain;
return true;})()`, nil))
	t.Cleanup(func() {
		if err := eval(encoder, `(async()=>{const e=window.__audioExperiment;if(!e)return;
adaptTick=e.adaptTick;recoverInputPressure=e.recoverInputPressure;noteInputPressure=e.noteInputPressure;
if(currentPC===e.pc&&captureGeneration===e.generation){const c={...e.constraints};delete c.deviceId;
await e.sender.track.applyConstraints(c);await queueSenderParams(async()=>{
const p=e.sender.getParameters();p.encodings=e.parameters.encodings;await e.sender.setParameters(p);});
if(e.timer)startQualityAdaptLoop();}delete window.__audioExperiment;})()`, nil); err != nil {
			t.Errorf("restore diagnostic encoder overrides: %v", err)
		}
	})
	setFPS := func(fps int) {
		require.NoError(t, eval(encoder, fmt.Sprintf(`(async()=>{const e=window.__audioExperiment;
if(currentPC!==e.pc||captureGeneration!==e.generation)throw Error('capture replaced during experiment');
const c={...e.constraints};delete c.deviceId;c.frameRate={min:1,max:%d};
await e.sender.track.applyConstraints(c);await queueSenderParams(async()=>{const p=e.sender.getParameters();
if(!encodingsNegotiated(p))throw Error('video encodings not negotiated');
p.encodings[0].maxFramerate=%d;p.encodings[0].scaleResolutionDownBy=1;await e.sender.setParameters(p);});return true;})()`, fps, fps), nil))
	}
	setFPS(30)
	require.NoError(t, boundedRun(source, chromedp.Click("#start", chromedp.ByQuery)))
	t.Cleanup(func() {
		if err := eval(source, `window.__stopAudioExperiment()`, nil); err != nil {
			t.Errorf("stop diagnostic stimulus: %v", err)
		}
	})
	var lastClock float64
	var lastBusy, lastFrames int
	var width, height float64
	lastCounters := make(map[string]float64)
	lastTimestamps := make(map[string]float64)
	statIDs := make(map[string]string)
	sample := func(phase, fps int) {
		var stimulus struct {
			WallMS, AudioSeconds, RMS, BusyMS float64
			BusyCount, Frames                 int
			Running                           bool
		}
		require.NoError(t, eval(source, `window.__sampleAudioExperiment()`, &stimulus))
		require.True(t, stimulus.Running, "native audio context must remain running")
		require.Greater(t, stimulus.RMS, 0.005, "quiet tone must actually reach native analyser")
		require.Less(t, stimulus.RMS, 0.025, "tone must remain at fixed quiet gain")
		require.Greater(t, stimulus.AudioSeconds, lastClock, "native audio clock must advance, at any rate")
		if phase == 3 {
			require.Equal(t, lastBusy, stimulus.BusyCount, "busy task must stay disabled in recovery phase")
		} else {
			require.Greater(t, stimulus.BusyCount, lastBusy, "fixed main-thread load must run")
		}
		require.Greater(t, stimulus.Frames, lastFrames, "full viewport animation must run")
		lastClock, lastBusy, lastFrames = stimulus.AudioSeconds, stimulus.BusyCount, stimulus.Frames
		var capture struct {
			FPS, SenderFPS, Scale, Width, Height float64
			Stats                                []map[string]any
		}
		require.NoError(t, eval(encoder, `(async()=>{const e=window.__audioExperiment;
if(currentPC!==e.pc||captureGeneration!==e.generation)throw Error('capture replaced during experiment');
if(adaptTimer||adaptTickInFlight||inputPressureApplying)throw Error('adaptation not frozen');
const s=e.sender.track.getSettings(),p=e.sender.getParameters().encodings[0],stats=[];
const fields=['timestamp','frames','framesPerSecond','framesEncoded','framesSent','totalEncodeTime',
'totalPacketSendDelay','packetsSent','bytesSent','totalSamplesDuration','totalAudioEnergy','audioLevel',
'frameWidth','frameHeight','width','height','qualityLimitationReason','qualityLimitationDurations'];
(await e.pc.getStats()).forEach(r=>{if(r.type!=='media-source'&&r.type!=='outbound-rtp')return;
const out={id:r.id,type:r.type,kind:r.kind};for(const k of fields)if(r[k]!==undefined)out[k]=r[k];stats.push(out);});
return {FPS:s.frameRate,SenderFPS:p.maxFramerate,Scale:p.scaleResolutionDownBy,Width:s.width,Height:s.height,Stats:stats};})()`, &capture))
		require.Equal(t, float64(fps), capture.FPS, "actual capture FPS setting")
		require.Equal(t, float64(fps), capture.SenderFPS, "actual sender FPS limit")
		require.Equal(t, float64(1), capture.Scale, "resolution must not adapt between phases")
		if width == 0 {
			width, height = capture.Width, capture.Height
			require.Positive(t, width)
			require.Positive(t, height)
		}
		require.Equal(t, width, capture.Width)
		require.Equal(t, height, capture.Height)
		var audioOutbound, videoOutbound bool
		frameRows := make(map[string]int)
		for _, stat := range capture.Stats {
			if stat["type"] == "outbound-rtp" {
				audioOutbound = audioOutbound || stat["kind"] == "audio"
				videoOutbound = videoOutbound || stat["kind"] == "video"
			}
			if stat["kind"] != "video" {
				continue
			}
			kind, _ := stat["type"].(string)
			field := map[string]string{"media-source": "frames", "outbound-rtp": "framesEncoded"}[kind]
			if field == "" {
				continue
			}
			frameRows[kind]++
			counter, present := stat[field].(float64)
			require.True(t, present, "actual %s counter required", field)
			require.True(t, !math.IsNaN(counter) && !math.IsInf(counter, 0), "finite frame counter required")
			timestamp, present := stat["timestamp"].(float64)
			require.True(t, present, "frame counter timestamp required")
			require.True(t, !math.IsNaN(timestamp) && !math.IsInf(timestamp, 0), "finite timestamp required")
			id, _ := stat["id"].(string)
			require.NotEmpty(t, id, "frame source identity required")
			if previous, exists := statIDs[kind]; exists {
				require.Equal(t, previous, id, "frame source must remain unchanged")
			}
			require.GreaterOrEqual(t, counter, lastCounters[kind], "frame counter must not reset")
			require.Greater(t, timestamp, lastTimestamps[kind], "observation timestamp must advance")
			lastCounters[kind], lastTimestamps[kind], statIDs[kind] = counter, timestamp, id
		}
		require.Equal(t, 1, frameRows["media-source"], "one video source counter required")
		require.Equal(t, 1, frameRows["outbound-rtp"], "one video encoding counter required")
		require.True(t, audioOutbound, "native audio sender stats required")
		require.True(t, videoOutbound, "native video sender stats required")
		relay := cs.Stats()
		require.Positive(t, relay.AudioPackets, "actual native audio must reach relay")
		line, err := json.Marshal(map[string]any{"phase": phase, "requestedFPS": fps, "busyEnabled": phase != 3, "hostUnixMS": time.Now().UnixMilli(), "source": stimulus, "capture": capture, "audioPackets": relay.AudioPackets, "videoPackets": relay.VideoPackets})
		require.NoError(t, err)
		t.Logf("AUDIO_CAPTURE_SAMPLE %s", line)
	}
	// Polling is deliberately identical in every phase. Wall deadlines avoid
	// extending workload duration by cumulative CDP sampling delays.
	runPhase := func(phase, fps int, duration time.Duration) {
		started := time.Now()
		deadline := started.Add(duration)
		observations := 0
		for time.Now().Before(deadline) {
			time.Sleep(min(5*time.Second, time.Until(deadline)))
			sample(phase, fps)
			observations++
		}
		label := map[int]string{-1: "warmup", 0: "busy-30-first", 1: "busy-10", 2: "busy-30-return", 3: "no-busy-30"}[phase]
		line, err := json.Marshal(map[string]any{"phase": phase, "label": label, "requestedFPS": fps, "busyEnabled": phase != 3, "observations": observations, "wallMS": time.Since(started).Milliseconds()})
		require.NoError(t, err)
		t.Logf("AUDIO_CAPTURE_PHASE %s", line)
	}
	if warmup > 0 {
		runPhase(-1, 30, warmup)
	}
	for phase, fps := range []int{30, 10, 30} {
		setFPS(fps)
		runPhase(phase, fps, 60*time.Second)
	}
	// Disable only the deliberate busy task; preserve native audio and animation.
	require.NoError(t, eval(source, `(()=>{clearInterval(busyTimer);busyTimer=null;return busyCount;})()`, &lastBusy))
	runPhase(3, 30, 60*time.Second)
}

func TestAudioPressureWarmupBounds(t *testing.T) {
	for _, tc := range []struct {
		input   string
		seconds int
		valid   bool
	}{{"", 0, true}, {"0", 0, true}, {"1", 1, true}, {"479", 479, true}, {"480", 480, true},
		{"-1", 0, false}, {"481", 0, false}, {"1.5", 0, false}, {"abc", 0, false}, {"999999999999999999999", 0, false}} {
		t.Run(tc.input, func(t *testing.T) {
			actual, err := audioPressureWarmup(tc.input)
			if !tc.valid {
				require.EqualError(t, err, "audio pressure warmup must be an integer from 0 through 480 seconds")
				require.Zero(t, actual)
				return
			}
			require.NoError(t, err)
			require.Equal(t, time.Duration(tc.seconds)*time.Second, actual)
		})
	}
}

func audioPressureWarmup(value string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 0 || seconds > 480 {
		return 0, fmt.Errorf("audio pressure warmup must be an integer from 0 through 480 seconds")
	}
	return time.Duration(seconds) * time.Second, nil
}

const audioPressureExperimentPage = `<!doctype html><meta charset="utf-8">
<style>html,body{margin:0;width:100%;height:100%;overflow:hidden}canvas{position:absolute;width:100%;height:100%}button{position:absolute;z-index:2}</style>
<canvas id="picture"></canvas><button id="start">Start diagnostic</button><script>
let audio,oscillator,analyser,busyTimer,animation,started=0,busyCount=0,busyMS=0,frames=0;
const canvas=document.querySelector('canvas'),ctx=canvas.getContext('2d'),samples=new Float32Array(2048);
function draw(){if(canvas.width!==innerWidth)canvas.width=innerWidth;if(canvas.height!==innerHeight)canvas.height=innerHeight;ctx.fillStyle='hsl('+(frames%360)+' 70% 40%)';
ctx.fillRect(0,0,canvas.width,canvas.height);ctx.fillStyle='white';ctx.fillRect((frames*13)%canvas.width,0,60,canvas.height);frames++;animation=requestAnimationFrame(draw);}
document.querySelector('button').onclick=async()=>{if(audio)return;audio=new AudioContext();oscillator=audio.createOscillator();
const gain=audio.createGain();gain.gain.value=.02;oscillator.frequency.value=440;analyser=audio.createAnalyser();analyser.fftSize=2048;
oscillator.connect(gain).connect(analyser).connect(audio.destination);oscillator.start();await audio.resume();started=performance.now();
busyTimer=setInterval(()=>{const begin=performance.now();while(performance.now()-begin<120){}busyMS+=performance.now()-begin;busyCount++;},250);draw();};
window.__sampleAudioExperiment=()=>{analyser.getFloatTimeDomainData(samples);let power=0;for(const v of samples)power+=v*v;
return {WallMS:performance.now()-started,AudioSeconds:audio.currentTime,RMS:Math.sqrt(power/samples.length),
BusyMS:busyMS,BusyCount:busyCount,Frames:frames,Running:audio.state==='running'};};
window.__stopAudioExperiment=async()=>{clearInterval(busyTimer);cancelAnimationFrame(animation);if(oscillator){oscillator.stop();oscillator=null;}if(audio&&audio.state!=='closed')await audio.close();};
window.addEventListener('pagehide',()=>{void window.__stopAudioExperiment();});
</script>`
