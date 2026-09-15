package captureext

import "testing"

func TestEncoderShutdownOwnsLateCaptureSideEffects(t *testing.T) {
	runEncoderReliabilityJS(t, `
 (async()=>{
  for(const stage of ['lookup','unmute']) {
   let release, entered;const reached=new Promise(r=>entered=r);let muted=true,mediaCalls=0;
   const writes=[];
   sandbox.chrome.debugger={getTargets:()=>stage==='lookup'?new Promise(r=>{release=()=>r([{id:'fixture',type:'page',tabId:7}]);entered()}):Promise.resolve([{id:'fixture',type:'page',tabId:7}])};
   sandbox.chrome.tabs={update:async(id,o)=>{writes.push([id,o.muted]);if(stage==='unmute'&&!o.muted){await new Promise(r=>{release=r;entered()})}muted=o.muted},get:async()=>({width:633,height:741})};
   sandbox.chrome.tabCapture={getMediaStreamId:async()=> 'stream'};
   const track={kind:'video',stop(){},getSettings:()=>({width:624,height:732})};
   sandbox.navigator={mediaDevices:{getUserMedia:async()=>{mediaCalls++;return {getTracks:()=>[track],getVideoTracks:()=>[track],getAudioTracks:()=>[]}}}};
   sandbox.socket={close(){}};
   run('shuttingDown=false;capturedTabId=7;ws=socket;');
   const pending=run('runCaptureAndOfferOnce(desiredCaptureCommand)');await reached;
   await run("handleControlFrame({action:'shutdown'})");release();await pending;
   assert.equal(mediaCalls,0,stage+': retired capture must not acquire media');
   assert.equal(muted,true,stage+': shutdown must remain locally muted');
   assert.equal(run('window.__omnipusState.status'),'shutdown');
   if(stage==='lookup')assert.deepEqual(writes,[[7,true]],'lookup retirement must not unmute');
   else assert.deepEqual(writes,[[7,false],[7,true],[7,true]],'late unmute must be compensated on exact tab');
  }
 })().catch(e=>{console.error(e);process.exitCode=1});
 `)
}

func TestEncoderAdaptationRejectsRetiredAsyncWork(t *testing.T) {
	runEncoderReliabilityJS(t, `
 (async()=>{
  const report=()=>new Map([['source',{id:'source',type:'media-source',kind:'video',timestamp:2000,framesPerSecond:30,frames:60}],['out',{type:'outbound-rtp',kind:'video',mediaSourceId:'source',timestamp:2000,framesPerSecond:1,qualityLimitationReason:'cpu'}]]);
  const initial={index:0,badStreak:0,goodStreak:0,cooldown:0,lastPressureAt:0};
  for(const stage of ['stats-peer','stats-attempt','stats-reset','params-reject','params-success','params-queued']) {
   let release, entered;const reached=new Promise(r=>entered=r);const writes=[];
   const sender={track:{kind:'video'},getParameters:()=>({encodings:[{active:true}]}),getStats:()=>stage.startsWith('stats')?new Promise(r=>{release=()=>r(report());entered()}):Promise.resolve(report()),setParameters:async p=>{writes.push(p.encodings[0].scaleResolutionDownBy);if(stage==='params-reject'||stage==='params-success')await new Promise((resolve,reject)=>{release=()=>stage==='params-reject'?reject(new Error('retired sender')):resolve();entered()})}};
   sandbox.peer={connectionState:'connected',getSenders:()=>[sender]};sandbox.nextPeer={connectionState:'connected',getSenders:()=>[]};
   run('shuttingDown=false;currentPC=peer;adaptState={index:1,badStreak:1,goodStreak:0,cooldown:0,lastPressureAt:0};window.__omnipusState.qualityAdapt=null;window.__omnipusState.lastError=null;');
   if(stage==='params-queued')run('senderParamsChain=new Promise(resolve=>{window.releaseParams=resolve})');
   const pending=run('adaptTick(undefined,2000)');
   if(stage==='params-queued') {await new Promise(r=>setImmediate(r));release=()=>run('window.releaseParams()');}
   else await reached;
   if(stage==='stats-reset') {await run("handleControlFrame({action:'adapt_reset'})");await run('senderParamsChain');}
   else {
    if(stage==='stats-peer'||stage.startsWith('params'))run('currentPC=nextPeer');
    if(stage==='stats-attempt')run('captureGeneration++');
    run('adaptState=adaptInitialState()');
   }
   release();await pending;await run('senderParamsChain').catch(e=>{if(stage!=='params-reject')throw e});
   assert.deepEqual(JSON.parse(run('JSON.stringify(adaptState)')),initial,stage+': stale work changed replacement state');
   assert.equal(run('window.__omnipusState.qualityAdapt'),null,stage+': stale diagnostics published');
   assert.equal(run('window.__omnipusState.lastError'),null,stage+': stale failure reported');
   if(stage.startsWith('stats')||stage==='params-queued')assert.deepEqual(writes,stage==='stats-reset'?[1]:[],stage+': stale sender write');
  }
 })().catch(e=>{console.error(e);process.exitCode=1});
 `)
}

func TestEncoderAdaptationDropsOverlappingTicks(t *testing.T) {
	runEncoderReliabilityJS(t, `
 (async()=>{
  let release,reads=0;const writes=[];
  const report=new Map([['source',{id:'source',type:'media-source',kind:'video',timestamp:2000,framesPerSecond:30,frames:60}],['out',{type:'outbound-rtp',kind:'video',mediaSourceId:'source',timestamp:2000,framesPerSecond:1,qualityLimitationReason:'cpu'}]]);
  const sender={track:{kind:'video'},getParameters:()=>({encodings:[{active:true}]}),setParameters:async p=>writes.push(p.encodings[0].scaleResolutionDownBy),getStats:()=>{reads++;return reads===1?new Promise(r=>release=()=>r(report)):Promise.resolve(report)}};
  sandbox.peer={connectionState:'connected',getSenders:()=>[sender]};
  run('currentPC=peer;adaptState={index:0,badStreak:1,goodStreak:0,cooldown:0,lastPressureAt:0};');
  const first=run('adaptTick(undefined,2000)');const second=run('adaptTick(undefined,2001)');
  assert.equal(await second,null,'overlapping tick is dropped');assert.equal(reads,1,'one outstanding stats read');
  release();await first;
  assert.deepEqual(writes,[1.5],'one pressure decision applies once');
 })().catch(e=>{console.error(e);process.exitCode=1});
 `)
}
