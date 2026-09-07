package captureext

import "testing"

func TestEncoderResolvesExactCDPTargetInsteadOfForeground(t *testing.T) {
	runEncoderReliabilityJS(t, `
 (async()=>{
  sandbox.chrome.tabs={query:async()=>[{id:99,url:'https://same.test'}]};
  sandbox.chrome.debugger={getTargets:async()=>[
   {id:'other',type:'page',tabId:99,url:'https://same.test'},
   {id:'wanted',type:'page',tabId:7,url:'https://same.test'}]};
  assert.equal(await run("findActiveTargetTab('wanted')"),7,'exact CDP identity wins over duplicate URL and foreground');
 })().catch(e=>{console.error(e);process.exitCode=1});
 `)
}

func TestEncoderRejectsUnprovableCaptureIdentity(t *testing.T) {
	runEncoderReliabilityJS(t, `
 (async()=>{
  sandbox.chrome.tabs={query:async()=>[{id:99,url:'https://same.test'}]};
  for(const candidates of [[],[{id:'wanted',type:'worker',tabId:7}],[{id:'wanted',type:'page'}],[{id:'wanted',type:'page',tabId:-1}],[{id:'wanted',type:'page',tabId:0.5}]]) {
   sandbox.chrome.debugger={getTargets:async()=>candidates};
   await assert.rejects(()=>run("findActiveTargetTab('wanted')"),{name:'Error',message:'requested capture target is unavailable'});
  }
  for(const invalid of ['',null,7,'x'.repeat(129)]) {
   sandbox.invalid=invalid;
   await assert.rejects(()=>run('findActiveTargetTab(invalid)'),{name:'TypeError',message:'capture target_id must be a nonempty string of at most 128 characters'});
  }
 })().catch(e=>{console.error(e);process.exitCode=1});
 `)
}

func TestEncoderCaptureUsesImmutableTargetGeometryCommand(t *testing.T) {
	runEncoderReliabilityJS(t, `
 (async()=>{
  const selections=[],constraints=[];
  let finishLookup, entered;const started=new Promise(r=>entered=r);
  sandbox.chrome.debugger={getTargets:()=>{entered();return new Promise(r=>finishLookup=r)}};
  sandbox.chrome.tabs={get:async()=>({width:1200,height:900}),update:async()=>({})};
  sandbox.chrome.tabCapture={getMediaStreamId:async o=>{selections.push(o.targetTabId);return 'source'}};
  const track={kind:'video',readyState:'live',stop(){},getSettings:()=>({width:624,height:720})};
  sandbox.navigator={mediaDevices:{getUserMedia:async c=>{constraints.push(c);return {getTracks:()=>[track],getVideoTracks:()=>[track],getAudioTracks:()=>[]}}}};
  sandbox.command={target_id:'old-target',capture_generation:1,expected_width:624,expected_height:720,capture_scale:1};
  const pending=run('captureActiveTabStream(command)');await started;
  run("expectedCaptureDims={w:1200,h:900};captureScale=2;");
  finishLookup([{id:'old-target',type:'page',tabId:7},{id:'new-target',type:'page',tabId:99}]);await pending;
  assert.deepEqual(selections,[7],'in-flight target remains old target');
  assert.equal(constraints[0].video.mandatory.maxWidth,624,'old command retains verified width');
  assert.equal(constraints[0].video.mandatory.maxHeight,720,'old command retains verified height and scale');
 })().catch(e=>{console.error(e);process.exitCode=1});
 `)
}

func TestEncoderNewGenerationRequiresFreshPeerAndTaggedOffer(t *testing.T) {
	runEncoderReliabilityJS(t, `
 (async()=>{
  let closed=0,replaced=0;const offers=[];
  const track={kind:'video',readyState:'live',stop(){},getSettings:()=>({width:624,height:720})};
  const stream={getTracks:()=>[track],getVideoTracks:()=>[track],getAudioTracks:()=>[]};
  const sender={track,getParameters:()=>({encodings:[]}),setParameters:async()=>{},replaceTrack:async()=>{replaced++}};
  sandbox.oldPC={connectionState:'connected',getSenders:()=>[sender],close(){closed++}};sandbox.oldStream=stream;
  sandbox.RTCPeerConnection=class {
   constructor(){this.connectionState='new';this.iceGatheringState='complete';this.localDescription={sdp:'v=0\r\n'};}
   addTrack(){}getSenders(){return [sender]}getTransceivers(){return []}
   async createOffer(){return {sdp:'v=0\r\n'}}async setLocalDescription(o){this.localDescription=o}close(){}
  };
  sandbox.chrome.debugger={getTargets:async()=>[{id:'target',type:'page',tabId:7}]};
  sandbox.chrome.tabs={get:async()=>({width:624,height:720}),update:async()=>({})};
  sandbox.chrome.tabCapture={getMediaStreamId:async()=> 'source'};
  sandbox.navigator={mediaDevices:{getUserMedia:async()=>stream}};
  sandbox.socket={readyState:1,send:s=>offers.push(JSON.parse(s))};
  sandbox.command={target_id:'target',capture_generation:2,expected_width:624,expected_height:720,capture_scale:1};
  run("currentPC=oldPC;currentStream=oldStream;currentCaptureCommand={target_id:'target',capture_generation:1};ws=socket;");
  await run('runCaptureAndOfferOnce(command)');run('clearOfferAnswerTimeout()');
  assert.equal(closed,1,'changed generation retires old transport even for same target');
  assert.equal(replaced,0,'a new generation cannot reuse old receiver lineage');
  assert.equal(offers.length,1,'one new offer');
  assert.equal(offers[0].capture_generation,2,'offer carries assigned generation');
  assert.equal(offers[0].target_id,'target','offer carries verified exact target');
 })().catch(e=>{console.error(e);process.exitCode=1});
 `)
}

func TestEncoderIgnoresAnswerForRetiredOfferWithoutClearingTimeout(t *testing.T) {
	runEncoderReliabilityJS(t, `
 (async()=>{
  const applied=[];
  sandbox.pc={signalingState:'have-local-offer',setRemoteDescription:async d=>applied.push(d.sdp),getSenders:()=>[]};
  run("currentPC=pc;currentOfferID=2;currentCaptureCommand={capture_generation:7,target_id:'target'};offerAnswerTimer=123;selfHealBudget=0;");
  const base={type:'browser_capture_answer',sdp:'answer',offer_id:2,capture_generation:7,target_id:'target'};
  for(const stale of [{...base,offer_id:1},{...base,capture_generation:6},{...base,target_id:'other'},{type:'browser_capture_answer',sdp:'untagged'}]) {
   sandbox.message=JSON.stringify(stale);await run('handleWsMessage(message)');
   assert.deepEqual(applied,[],'stale answer must not reach replacement peer');
   assert.equal(run('offerAnswerTimer'),123,'stale answer must not cancel current deadline');
  }
  sandbox.message=JSON.stringify(base);await run('handleWsMessage(message)');
  assert.deepEqual(applied,['answer'],'matching answer applies once');
  assert.equal(run('offerAnswerTimer'),null,'matching answer cancels deadline');
 })().catch(e=>{console.error(e);process.exitCode=1});
 `)
}

func TestEncoderCoalescedCaptureCannotOfferOldGenerationOrMixGeometry(t *testing.T) {
	runEncoderReliabilityJS(t, `
 (async()=>{
  const selections=[],dimensions=[],offers=[],tracks=[];let release,entered;
  const started=new Promise(r=>entered=r);let reads=0;
  const targets=[{id:'old',type:'page',tabId:7},{id:'new',type:'page',tabId:99}];
  sandbox.chrome.debugger={getTargets:()=>++reads===1?(entered(),new Promise(r=>release=r)):Promise.resolve(targets)};
  sandbox.chrome.tabs={get:async()=>({width:1200,height:900}),update:async()=>({})};
  sandbox.chrome.tabCapture={getMediaStreamId:async o=>{selections.push(o.targetTabId);return 'source'}};
  sandbox.navigator={mediaDevices:{getUserMedia:async c=>{
   dimensions.push([c.video.mandatory.maxWidth,c.video.mandatory.maxHeight]);
   const track={kind:'video',readyState:'live',stopped:false,stop(){this.stopped=true},getSettings:()=>({width:888,height:456})};tracks.push(track);
   return {getTracks:()=>[track],getVideoTracks:()=>[track],getAudioTracks:()=>[]};
  }}};
  sandbox.RTCPeerConnection=class {
   constructor(){this.iceGatheringState='complete';this.senders=[];}
   addTrack(track){this.senders.push({track,getParameters:()=>({encodings:[]}),setParameters:async()=>{}})}getSenders(){return this.senders}getTransceivers(){return []}
   async createOffer(){return {sdp:'v=0\r\n'}}async setLocalDescription(o){this.localDescription=o}close(){}
  };
  sandbox.socket={readyState:1,send:s=>offers.push(JSON.parse(s))};run('ws=socket;desiredCaptureCommand=null;currentCaptureCommand=null;');
  sandbox.oldCommand={target_id:'old',capture_generation:1,expected_width:624,expected_height:720,capture_scale:1};
  sandbox.newCommand={target_id:'new',capture_generation:2,expected_width:888,expected_height:456,capture_scale:1};
  const first=run('runCaptureAndOffer(oldCommand)');await started;
  await run('runCaptureAndOffer(newCommand)');release(targets);await first;run('clearOfferAnswerTimeout()');
  assert.deepEqual(selections,[7,99],'each attempt keeps its own requested target');
  assert.deepEqual(dimensions,[[624,720],[888,456]],'old target cannot acquire new target geometry');
  assert.deepEqual(tracks.map(t=>t.stopped),[true,false],'retired late capture stops before replacement remains live');
  assert.equal(offers.length,1,'canceled generation cannot publish an offer');
  assert.equal(offers[0].capture_generation,2,'only latest generation is offered');
  assert.equal(offers[0].target_id,'new','only actual replacement target is offered');
 })().catch(e=>{console.error(e);process.exitCode=1});
 `)
}

func TestEncoderRejectsReuseOfGenerationForDifferentGeometry(t *testing.T) {
	runEncoderReliabilityJS(t, `
 (async()=>{
  sandbox.pc={connectionState:'connected'};
  sandbox.stream={getVideoTracks:()=>[{readyState:'live',getSettings:()=>({width:624,height:720})}]};
  run("currentPC=pc;currentStream=stream;capturedTabId=7;lastPinnedCapDims={w:624,h:720,scale:1};currentCaptureCommand=captureCommandFrom({target_id:'fixture',capture_generation:1,expected_width:624,expected_height:720,capture_scale:1});desiredCaptureCommand=currentCaptureCommand;");
  sandbox.message={action:'recapture',target_id:'fixture',capture_generation:1,expected_width:625,expected_height:720,capture_scale:1};
  await assert.rejects(()=>run('handleControlFrame(message)'),{name:'Error',message:'capture generation reused for a different source or geometry'});
  run('desiredCaptureCommand={...currentCaptureCommand,capture_generation:2};');sandbox.message.expected_width=624;
  await assert.rejects(()=>run('handleControlFrame(message)'),{name:'Error',message:'stale capture generation'});
 })().catch(e=>{console.error(e);process.exitCode=1});
 `)
}

func TestEncoderRequestedRecoveryIsNotSkippedForHealthyLookingGeometry(t *testing.T) {
	runEncoderReliabilityJS(t, `
 (async()=>{
  let closed=0;const replacements=[];
  const oldTracks=['video','audio'].map(kind=>({kind,readyState:'live',stopped:false,stop(){this.stopped=true},getSettings:()=>({width:624,height:732})}));
  const newTracks=['video','audio'].map(kind=>({kind,readyState:'live',stop(){},getSettings:()=>({width:624,height:732})}));
  sandbox.oldStream={getTracks:()=>oldTracks,getVideoTracks:()=>[oldTracks[0]],getAudioTracks:()=>[oldTracks[1]]};
  const next={getTracks:()=>newTracks,getVideoTracks:()=>[newTracks[0]],getAudioTracks:()=>[newTracks[1]]};
  sandbox.pc={connectionState:'connected',getSenders:()=>oldTracks.map(track=>({track,getParameters:()=>({encodings:[]}),replaceTrack:async t=>replacements.push(t.kind)})),close(){closed++}};
  sandbox.chrome.tabs={get:async()=>({width:633,height:741}),update:async()=>({})};
  sandbox.chrome.tabCapture={getMediaStreamId:async()=> 'source'};
  sandbox.navigator={mediaDevices:{getUserMedia:async()=>next}};
  run('currentPC=pc;currentStream=oldStream;capturedTabId=7;lastPinnedCapDims={w:633,h:741,scale:1};');
  sandbox.message={action:'recapture',capture_generation:1,target_id:'fixture',expected_width:633,expected_height:741,capture_scale:1};
  await run('handleControlFrame(message)');
  assert.deepEqual(oldTracks.map(t=>t.stopped),[true,true],'explicit recovery stops even apparently live tracks');
  assert.deepEqual(replacements,['video','audio'],'both requested capture tracks replaced');
  assert.equal(closed,0,'same generation recovery retains negotiated peer');
 })().catch(e=>{console.error(e);process.exitCode=1});
 `)
}

func TestEncoderTargetLookupTimeoutCleansUpDeadline(t *testing.T) {
	runEncoderReliabilityJS(t, `
 (async()=>{
  const cleared=[];let finish;
  sandbox.chrome.debugger={getTargets:()=>new Promise(r=>finish=r)};
  sandbox.setTimeout=(fn,ms)=>{assert.equal(ms,1500,'identity lookup budget');queueMicrotask(fn);return 77};
  sandbox.clearTimeout=id=>cleared.push(id);
  await assert.rejects(()=>run("findActiveTargetTab('fixture')"),{name:'Error',message:'capture target lookup timed out'});
  assert.deepEqual(cleared,[77],'lookup deadline is released after timeout');
  finish([{id:'fixture',type:'page',tabId:7}]);await Promise.resolve();
  assert.deepEqual(cleared,[77],'late lookup completion cannot rearm a timed-out operation');
 })().catch(e=>{console.error(e);process.exitCode=1});
 `)
}

func TestEncoderCaptureScaleDefaultsPerCommand(t *testing.T) {
	runEncoderReliabilityJS(t, `
 run('captureScale=2;');
 for(const [value,want] of [[undefined,1],[null,1],['2',1],[NaN,1],[Infinity,1],[-1,1],[0,1],[0.5,1],[1,1],[1.5,1.5],[4,4],[4.1,4]]) {
  sandbox.frame={capture_generation:2,target_id:'fixture',capture_scale:value};
  assert.equal(run('captureCommandFrom(frame).capture_scale'),want,'each command normalizes scale independently of previous2x capture');
 }
 `)
}
