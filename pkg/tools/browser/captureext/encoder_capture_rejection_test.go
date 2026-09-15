package captureext

import (
	"strconv"
	"testing"
)

func TestEncoderRetiredCaptureRejectionKeepsLatestCommand(t *testing.T) {
	for _, stage := range []string{"lookup", "stream-id", "media"} {
		for _, outcome := range []string{"replacement", "shutdown", "current", "replacement-shutdown"} {
			t.Run(stage+"/"+outcome, func(t *testing.T) {
				runEncoderReliabilityJS(t, "const stage="+strconv.Quote(stage)+",outcome="+strconv.Quote(outcome)+";"+encoderCaptureRejectionJS)
			})
		}
	}
}

const encoderCaptureRejectionJS = `
(async()=>{
 let rejectOld, entered;const reached=new Promise(r=>entered=r);
 const delayed=()=>new Promise((_,reject)=>{rejectOld=()=>reject(new Error('original '+stage+' failed'));entered()});
 let lookups=0,ids=0,media=0,closes=0;const offers=[],acquisitions=[],tracks=[];
 const targets=[{id:'fixture',type:'page',tabId:7},{id:'replacement',type:'page',tabId:99}];
 sandbox.chrome.debugger={getTargets:()=>{lookups++;return stage==='lookup'&&lookups===1?delayed():Promise.resolve(targets)}};
 sandbox.chrome.tabs={get:async()=>({width:1200,height:900}),update:async()=>({})};
 sandbox.chrome.tabCapture={getMediaStreamId:async o=>{ids++;if(stage==='stream-id'&&ids===1)return delayed();return 'source-'+o.targetTabId}};
 sandbox.navigator={mediaDevices:{getUserMedia:async c=>{
  media++;if(stage==='media'&&media===1)return delayed();
  acquisitions.push([c.video.mandatory.chromeMediaSourceId,c.video.mandatory.maxWidth,c.video.mandatory.maxHeight]);
  const track={kind:'video',readyState:'live',stopped:false,stop(){this.stopped=true},getSettings:()=>({width:888,height:456})};tracks.push(track);
  return {getTracks:()=>[track],getVideoTracks:()=>[track],getAudioTracks:()=>[]};
 }}};
 sandbox.RTCPeerConnection=class {
  constructor(){this.iceGatheringState='complete';this.senders=[];}
  addTrack(track){this.senders.push({track,getParameters:()=>({encodings:[]}),setParameters:async()=>{}})}getSenders(){return this.senders}getTransceivers(){return []}
  async createOffer(){return {sdp:'v=0\r\n'}}async setLocalDescription(o){this.localDescription=o}close(){}
 };
 sandbox.socket={readyState:1,send:s=>offers.push(JSON.parse(s)),close(){closes++}};run('ws=socket;');
 sandbox.old={action:'recapture',target_id:'fixture',capture_generation:1,expected_width:633,expected_height:741,capture_scale:1};
 sandbox.next={action:'recapture',target_id:'replacement',capture_generation:2,expected_width:888,expected_height:456,capture_scale:1};
 const first=run('handleControlFrame(old)');await reached;
 if(outcome.startsWith('replacement'))await run('handleControlFrame(next)');
 if(outcome.endsWith('shutdown'))await run("handleControlFrame({action:'shutdown'})");
 rejectOld();await first;run('clearOfferAnswerTimeout()');
 assert.equal(run('captureInFlight'),false,'operation must drain');
 assert.equal(run('captureRerunRequested'),false,'no abandoned queued pass');
 if(outcome==='replacement') {
  assert.equal(closes,0,'retired rejection must not close replacement socket');
  assert.equal(lookups,2,'queued replacement must resolve its own target');
  assert.deepEqual(acquisitions,[['source-99',888,456]],'only replacement creates media with its geometry');
  assert.deepEqual(tracks.map(t=>t.stopped),[false],'replacement track remains live');
  assert.deepEqual(offers,[{type:'browser_capture_offer',sdp:'v=0\r\n',capture_generation:2,target_id:'replacement',offer_id:1}],'only exact replacement offer is published');
  assert.equal(run('currentCaptureCommand.target_id'),'replacement');
  assert.equal(run('window.__omnipusState.lastError'),null,'retired failure must not be published');
 } else if(outcome.endsWith('shutdown')) {
  assert.equal(closes,1,'shutdown alone closes the socket');
  assert.equal(lookups,1,'shutdown must not begin a queued replacement');
  assert.deepEqual(offers,[],'shutdown cannot publish an offer');
  assert.equal(run('window.__omnipusState.status'),'shutdown');
  assert.equal(run('window.__omnipusState.lastError'),null,'retired failure must not replace shutdown');
 } else {
  assert.equal(closes,1,'current failure must close its failed socket');
  assert.deepEqual(offers,[],'failed capture must not publish an offer');
  assert.match(run('window.__omnipusState.lastError'),new RegExp('original '+stage+' failed'),'current failure must remain visible');
 }
})().catch(e=>{console.error(e);process.exitCode=1});
`
