package captureext

import (
	"os/exec"
	"strings"
	"testing"
)

// Runs the actual embedded module; only browser globals are replaced.
func runEncoderReliabilityJS(t *testing.T, body string) {
	t.Helper()
	bootstrap := `const vm=require('vm'); const assert=require('node:assert/strict');
 const sandbox={window:{},console:{log(){},warn(){}},setTimeout,clearTimeout,setInterval,clearInterval,WebSocket:{OPEN:1},chrome:{runtime:{getManifest(){return {version:'test'}}}}};
 vm.createContext(sandbox); vm.runInContext(require('node:fs').readFileSync(0,'utf8'),sandbox);
 const run=(code)=>vm.runInContext(code,sandbox);
 sandbox.window.__omnipusCapture={token:'test',ingestUrl:'ws://fixture',target_id:'fixture',capture_generation:1,expected_width:633,expected_height:741,capture_scale:1};
 sandbox.chrome.debugger={getTargets:async()=>[{id:'fixture',type:'page',tabId:7}]};
 run('desiredCaptureCommand=captureCommandFrom(window.__omnipusCapture);currentCaptureCommand=desiredCaptureCommand;');
 `
	cmd := exec.Command("node", "-e", bootstrap+body)
	cmd.Stdin = strings.NewReader(embeddedEncoderJS(t))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("embedded encoder behavior: %v\n%s", err, out)
	}
}

func TestEncoderDimensionsHardwareCompatibleAtEveryScale(t *testing.T) {
	runEncoderReliabilityJS(t, `
 const inputs=[[899,456],[633,741],[632,906],[1280,719],[1280,720],[1280,721],[1,1],[2,3],[11,12],[12,13],[20000,10000],[10000,20000],[1,100000000],[100000000,1]];
 for(const [w,h] of inputs) for(const dpr of [1,1.25,1.5,2,3]) {
  const got=run('budgetedCaptureDims('+w+','+h+','+dpr+')');
  assert.ok(got.w>0&&got.h>0, 'positive dimensions');
  assert.ok(got.w*got.h<=921600,'pixel budget '+JSON.stringify(got));
  if(w*dpr>=12 && h*dpr>=12){
   const reduction=Math.sqrt(Math.max(1,w*h*dpr*dpr/921600));
   assert.ok(Math.abs(got.w-w*dpr/reduction)<12 && Math.abs(got.h-h*dpr/reduction)<12,'uniform scaling preserves aspect within one grid cell '+JSON.stringify(got));
  }
  for(const scale of [1,1.5,2]) {
   assert.equal((got.w/scale)%2,0,'hardware width '+JSON.stringify({w,h,dpr,scale,got}));
   assert.equal((got.h/scale)%2,0,'hardware height '+JSON.stringify({w,h,dpr,scale,got}));
  }
 }
 for(const bad of ['0,720,1','-1,720,1','1280,NaN,1','1280,720,Infinity','1280,720,0','1e308,720,2']) {
  assert.throws(()=>run('budgetedCaptureDims('+bad+')'),{name:'RangeError',message:'capture dimensions and scale must be finite and positive'});
 }
 `)
}

func TestEncoderIdleCPULabelDoesNotReduceQuality(t *testing.T) {
	runEncoderReliabilityJS(t, `
 let st=run('adaptInitialState()');
 for(let i=1;i<=30;i++) {
  sandbox.sample={framesPerSecond:1,qualityLimitationReason:'cpu',sourceFramesPerSecond:1};sandbox.prev=st;
  const result=run('qualityAdaptDecide(sample,prev,'+(i*2000)+')');st=result.state;
  assert.equal(st.index,0,'idle source with CPU label must preserve full text resolution');
 }
 sandbox.sample={framesPerSecond:1,qualityLimitationReason:'cpu'};sandbox.prev=run('adaptInitialState()');
 for(let i=0;i<4;i++) {sandbox.prev=run('qualityAdaptDecide(sample,prev,10000)').state;assert.equal(sandbox.prev.index,0,'missing demand is unknown');}
 `)
}

func TestEncoderRecapturePreservesConnectedPeer(t *testing.T) {
	runEncoderReliabilityJS(t, `
 (async()=>{
  let closed=0, stopped=0;const replaced=[];
  const tracks=['video','audio'].map(kind=>({kind,readyState:'live',stop(){stopped++},getSettings(){return {width:624,height:900}}}));
  const next={getTracks:()=>tracks,getVideoTracks:()=>[tracks[0]],getAudioTracks:()=>[tracks[1]]};
  const senders=tracks.map(track=>({track,replaceTrack:async t=>replaced.push(t.kind),getParameters:()=>({encodings:[]})}));
  sandbox.pc={connectionState:'connected',getSenders:()=>senders,close(){closed++}};
  sandbox.old={getTracks:()=>tracks};sandbox.navigator={mediaDevices:{getUserMedia:async()=>next}};
  sandbox.chrome.tabs={query:async()=>[{id:7,url:'https://fixture.test',width:633,height:741}],get:async()=>({width:633,height:741}),update:async()=>({})};
  sandbox.chrome.tabCapture={getMediaStreamId:async()=> 'stream'};
  run('currentPC=pc;currentStream=old;expectedCaptureDims={w:633,h:741};');
  let failure;try{await run('runCaptureAndOfferOnce()')}catch(e){failure=e;}
  assert.equal(closed,0,'ordinary recapture must retain connected transport');
  assert.equal(failure,undefined,'recapture should complete');
  assert.deepEqual(replaced,['video','audio'],'replace both media tracks');
  assert.equal(stopped,2,'old capture must stop before requesting same tab');
  assert.equal(run('currentPC===pc'),true,'same peer survives');
 })().catch(e=>{console.error(e);process.exitCode=1});
 `)
}

func TestEncoderHealthReportsOriginalSampleAndOmitsMissingCounters(t *testing.T) {
	runEncoderReliabilityJS(t, `
 (async()=>{
  const frames=[];
  const report=new Map([['out',{type:'outbound-rtp',kind:'video',mediaSourceId:'src',timestamp:12345,framesEncoded:7,packetsSent:19}],['src',{id:'src',type:'media-source',kind:'video',frames:9}]]);
  sandbox.pc={connectionState:'connected',getSenders:()=>[{track:{kind:'video'},getStats:async()=>report}]};
  sandbox.stream={getVideoTracks:()=>[{readyState:'live',muted:false}]};sandbox.socket={readyState:1,send:s=>frames.push(JSON.parse(s))};
  run('currentPC=pc;currentStream=stream;ws=socket;captureGeneration=4;');
  await run('sendCaptureHealth()');await run('sendCaptureHealth()');
  const expected={type:'browser_capture_control',action:'ping',capture_generation:1,target_id:'fixture',capture_health:{generation:4,track_state:'live',track_muted:false,peer_state:'connected',source_frames:9,encoded_frames:7,packets_sent:19,sample_timestamp_ms:12345}};
  assert.deepEqual(frames,[expected,expected],'same old stats must retain old sample timestamp');
  frames.length=0;report.clear();await run('sendCaptureHealth()');
  assert.deepEqual(frames,[{type:'browser_capture_control',action:'ping',capture_generation:1,target_id:'fixture',capture_health:{generation:4,track_state:'live',track_muted:false,peer_state:'connected'}}],'missing counters must remain absent');
 })().catch(e=>{console.error(e);process.exitCode=1});
 `)
}

func TestEncoderRecaptureCannotResurrectAfterShutdown(t *testing.T) {
	runEncoderReliabilityJS(t, `
 (async()=>{
  let resolveCapture, arrived;const started=new Promise(r=>arrived=r);let stopped=0;
  const replacement={getTracks:()=>[{stop(){stopped++}}],getVideoTracks:()=>[],getAudioTracks:()=>[]};
  sandbox.pc={connectionState:'connected',close(){},getSenders:()=>[]};sandbox.old={getTracks:()=>[]};
  sandbox.navigator={mediaDevices:{getUserMedia:()=>{arrived();return new Promise(r=>resolveCapture=r)}}};
  sandbox.chrome.tabs={query:async()=>[{id:7,url:'https://fixture.test'}],get:async()=>({width:633,height:741}),update:async()=>({})};
  sandbox.chrome.tabCapture={getMediaStreamId:async()=> 'stream'};
  run('currentPC=pc;currentStream=old;expectedCaptureDims={w:633,h:741};');
  const pending=run('runCaptureAndOfferOnce()');await started;run('shuttingDown=true;teardownCapture()');resolveCapture(replacement);await pending;
  assert.equal(run('currentPC'),null,'shutdown peer remains absent');
  assert.equal(run('currentStream'),null,'late capture cannot install stream');
  assert.equal(stopped,1,'late stream releases capture resource');
 })().catch(e=>{console.error(e);process.exitCode=1});
 `)
}

func TestEncoderDemandUsesFreshSourceProgress(t *testing.T) {
	runEncoderReliabilityJS(t, `
 (async()=>{
  const out={type:'outbound-rtp',kind:'video',mediaSourceId:'src',timestamp:1000,framesPerSecond:1,qualityLimitationReason:'cpu'};
  const src={id:'src',type:'media-source',kind:'video',timestamp:1000,frames:30,framesPerSecond:30};
  const sender={track:{kind:'video'},getStats:async()=>new Map([['out',out],['src',src]])};sandbox.pc={getSenders:()=>[sender]};
  assert.equal((await run('readVideoSenderSample(pc)')).sourceFramesPerSecond,30,'first current source report');
  assert.equal((await run('readVideoSenderSample(pc)')).sourceFramesPerSecond,undefined,'repeated timestamp is not fresh demand');
  out.timestamp=src.timestamp=3000;
  assert.equal((await run('readVideoSenderSample(pc)')).sourceFramesPerSecond,0,'static source counter defeats stale30fps label');
  out.timestamp=src.timestamp=5000;src.frames+=60;
  assert.equal((await run('readVideoSenderSample(pc)')).sourceFramesPerSecond,30,'sixty source frames in two seconds');
  out.timestamp=7000;src.frames+=60;
  assert.equal((await run('readVideoSenderSample(pc)')).sourceFramesPerSecond,undefined,'old source report cannot explain fresh output');
 })().catch(e=>{console.error(e);process.exitCode=1});
 `)
}
