package captureext

import "testing"

func TestEncoderHealthIdentityNamesSampledPeerNotDesiredCapture(t *testing.T) {
	runEncoderReliabilityJS(t, `
 (async()=>{
  const frames=[];let release,entered;const started=new Promise(r=>entered=r);
  const report=new Map([['out',{type:'outbound-rtp',kind:'video',timestamp:123,framesEncoded:7,packetsSent:19}]]);
  sandbox.pc={connectionState:'connected',getSenders:()=>[{track:{kind:'video'},getStats:()=>{entered();return new Promise(r=>release=r)}}]};
  sandbox.stream={getVideoTracks:()=>[{readyState:'live',muted:false}]};sandbox.socket={readyState:1,send:s=>frames.push(JSON.parse(s))};
  run('currentPC=pc;currentStream=stream;ws=socket;captureGeneration=47;');
  const pending=run('sendCaptureHealth()');await started;
  run("desiredCaptureCommand=Object.freeze({capture_generation:9,target_id:'future-target'});");
  release(report);await pending;
  assert.deepEqual(frames,[{type:'browser_capture_control',action:'ping',capture_generation:1,target_id:'fixture',capture_health:{generation:47,track_state:'live',track_muted:false,peer_state:'connected',encoded_frames:7,packets_sent:19,sample_timestamp_ms:123}}],'server generation comes from sampled peer command, local attempt stays47');
 })().catch(e=>{console.error(e);process.exitCode=1});
 `)
}

func TestEncoderHealthIdentityDiscardsReplacedAsyncSample(t *testing.T) {
	runEncoderReliabilityJS(t, `
 (async()=>{
  for(const changed of ['peer','stream','command','socket','local attempt']) {
   const frames=[];let release,entered;const started=new Promise(r=>entered=r);
   const report=new Map([['out',{type:'outbound-rtp',kind:'video',timestamp:123,framesEncoded:7,packetsSent:19}]]);
   sandbox.pc={connectionState:'connected',getSenders:()=>[{track:{kind:'video'},getStats:()=>{entered();return new Promise(r=>release=r)}}]};
   sandbox.stream={getVideoTracks:()=>[{readyState:'live',muted:false}]};sandbox.socket={readyState:1,send:s=>frames.push(JSON.parse(s))};
   sandbox.nextSocket={readyState:1,send:s=>frames.push(JSON.parse(s))};sandbox.nextStream={getVideoTracks:()=>[]};
   run("currentPC=pc;currentStream=stream;ws=socket;captureGeneration=47;currentCaptureCommand=Object.freeze({capture_generation:1,target_id:'fixture'});");
   const pending=run('sendCaptureHealth()');await started;
   if(changed==='peer')run('currentPC={}');
   if(changed==='stream')run('currentStream=nextStream');
   if(changed==='command')run("currentCaptureCommand=Object.freeze({capture_generation:2,target_id:'replacement'});");
   if(changed==='socket')run('ws=nextSocket');
   if(changed==='local attempt')run('captureGeneration=48');
   release(report);await pending;
   const expected=changed==='socket'?[]:[{type:'browser_capture_control',action:'ping'}];
   assert.deepEqual(frames,expected,'discard evidence after '+changed+' changes while preserving original socket liveness');
  }
 })().catch(e=>{console.error(e);process.exitCode=1});
 `)
}

func TestEncoderHealthIdentityDoesNotBorrowDesiredCommandWithoutPeer(t *testing.T) {
	runEncoderReliabilityJS(t, `
 (async()=>{
  const frames=[];sandbox.socket={readyState:1,send:s=>frames.push(JSON.parse(s))};
  run("ws=socket;currentPC=null;currentStream=null;currentCaptureCommand=null;desiredCaptureCommand=Object.freeze({capture_generation:9,target_id:'future-target'});");
  await run('sendCaptureHealth()');
  assert.equal(frames.length,1,'socket heartbeat survives an in-flight capture');
  assert.equal(frames[0].capture_generation,undefined,'no sampled peer means no server frame identity');
  assert.equal(frames[0].target_id,undefined,'desired target is not captured evidence');
 })().catch(e=>{console.error(e);process.exitCode=1});
 `)
}
