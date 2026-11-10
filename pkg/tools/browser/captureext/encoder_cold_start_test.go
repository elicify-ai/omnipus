package captureext

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// The failure dispatch requires equivalent startup synchronization to retain the
// first acquisition, without disabling later same-generation recovery. Exercise
// the complete script with configuration present before its real entry point.
func TestEncoderColdStartReplayOwnership(t *testing.T) {
	for _, stage := range []string{"lookup", "stream-id", "media", "offer"} {
		for _, mode := range []string{"baseline", "replay", "repeated-replay", "changed-target", "changed-geometry", "changed-generation", "enriched", "stale", "conflicting-target", "conflicting-geometry", "conflicting-scale", "shutdown"} {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				runEncoderColdStartJS(t, stage, mode)
			})
		}
	}
	for _, mode := range []string{"recovery-before-answer", "recovery-connected", "recovery-overlap", "recovery-unanswered-overlap", "media-rejection", "reconnect-resolve", "reconnect-reject"} {
		t.Run(mode, func(t *testing.T) {
			runEncoderColdStartJS(t, "media", mode)
		})
	}
}

// A replacement pass belongs to the socket executing it, not the earlier
// callback still awaiting the shared loop. Derive ownership from the dispatch,
// including controls where the replacement itself has retired before failure.
func TestEncoderReplacementFailureOwnership(t *testing.T) {
	for _, failureStage := range []string{"lookup", "stream-id", "media", "offer"} {
		for _, retiredOutcome := range []string{"resolve", "reject"} {
			for _, owner := range []string{"current", "superseded", "shutdown"} {
				t.Run(failureStage+"/"+retiredOutcome+"/"+owner, func(t *testing.T) {
					runEncoderColdStartJS(t, "media", "owner-failure/"+failureStage+"/"+retiredOutcome+"/"+owner)
				})
			}
		}
	}
}

func runEncoderColdStartJS(t *testing.T, stage, mode string) {
	t.Helper()
	cmd := exec.Command("node", "-e", "const stage="+strconv.Quote(stage)+",mode="+strconv.Quote(mode)+";"+encoderColdStartJS)
	cmd.Stdin = strings.NewReader(embeddedEncoderJS(t))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cold-start encoder behavior: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "COLD_START_CASE_COMPLETE "+stage+"/"+mode) {
		t.Fatalf("cold-start case did not complete: %s", out)
	}
}

const encoderColdStartJS = `
'use strict';
const assert = require('node:assert/strict');
const vm = require('node:vm');
const source = require('node:fs').readFileSync(0, 'utf8');
// Unfinished async work must not inherit Node's default successful exit.
process.exitCode = 1;
const first = {capture_generation:1,target_id:'fixture',expected_width:633,expected_height:741,capture_scale:1};
const replacement = {capture_generation:2,target_id:'replacement',expected_width:888,expected_height:456,capture_scale:1};
const deferred = () => {
  let resolve, reject;
  const promise = new Promise((yes, no) => {resolve=yes;reject=no});
  return {promise,resolve,reject};
};
const entered = deferred(), release = deferred();
const replacementEntered = deferred(), replacementResult = deferred();
const counts = {}, frames = [], streams = [], peers = [], selections = [], constraints = [], replaced = [], warnings = [], sockets = [], offeredOn = [];
const timers = new Map();
let socket, sandbox, run, opening, recoveryBarrier;
async function barrier(name) {
  counts[name] = (counts[name] || 0) + 1;
  if (name === 'media' && recoveryBarrier) {
    const held=recoveryBarrier;recoveryBarrier=null;
    held.entered.resolve();await held.release.promise;
  }
  if (name === stage && counts[name] === 1) {
    entered.resolve();
    await release.promise;
  }
  if (mode.startsWith('owner-failure/') && name === mode.split('/')[1] && counts[name] === (name === 'offer' ? 1 : 2)) {
    replacementEntered.resolve();
    await replacementResult.promise;
  }
}
function later(fn, delay, ...args) {
  const timer = setTimeout(() => {timers.delete(timer);fn(...args)}, delay);
  timers.set(timer, {kind:'timeout',fn,delay,args});
  return timer;
}
function every(fn, delay, ...args) {
  const timer = setInterval(fn, delay, ...args);
  timers.set(timer, 'interval');
  return timer;
}
function cancel(timer) {
  timers.delete(timer);
  clearTimeout(timer);
  clearInterval(timer);
}
class Socket {
  static OPEN = 1;
  constructor() {this.readyState=0;this.listeners={};this.closes=0;this.failureNotices=[];socket=this;sockets.push(this)}
  addEventListener(name, fn) {(this.listeners[name] ||= []).push(fn)}
  open() {this.readyState=1;return Promise.all((this.listeners.open || []).map(fn=>fn({})))}
  send(raw) {
    assert.equal(this.readyState, 1, 'only an open socket can send');
    const frame=JSON.parse(raw);
    delete frame.token;
    frames.push(frame);
    if (frame.type==='browser_capture_offer') offeredOn.push(this);
  }
  close() {
    this.closes++;
    this.failureNotices.push({error:sandbox.window.__omnipusState.lastError,status:sandbox.window.__omnipusState.status});
    this.readyState=3;
    for (const fn of this.listeners.close || []) fn({});
  }
}
class Peer {
  constructor() {
    this.connectionState='new';this.iceConnectionState='new';this.signalingState='stable';
    this.iceGatheringState='complete';this.senders=[];this.closes=0;peers.push(this);
  }
  addTrack(track) {
    const sender={track,getParameters:()=>({encodings:[{}]}),setParameters:async()=>{},getStats:async()=>new Map(),
      replaceTrack:async next=>{replaced.push(next.kind);sender.track=next}};
    this.senders.push(sender);
  }
  getSenders() {return this.senders}
  getTransceivers() {return []}
  async createOffer() {
    await barrier('offer');
    return {type:'offer',sdp:'v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\na=rtpmap:96 VP8/90000\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=rtpmap:111 opus/48000/2\r\n'};
  }
  async setLocalDescription(offer) {this.localDescription=offer;this.signalingState='have-local-offer'}
  async setRemoteDescription(answer) {
    this.remoteDescription=answer;this.signalingState='stable';
    this.connectionState='connected';this.iceConnectionState='connected';
    this.oniceconnectionstatechange();this.onconnectionstatechange();
  }
  close() {this.closes++;this.connectionState='closed';this.iceConnectionState='closed'}
}
function streamFor(request) {
  const tracks=['video','audio'].map(kind=>({kind,readyState:'live',muted:false,stops:0,label:'synthetic-'+kind,
    stop() {this.stops++;this.readyState='ended'},
    getSettings:()=>kind==='video'?{width:request.video.mandatory.maxWidth,height:request.video.mandatory.maxHeight}:{}}));
  const stream={tracks,getTracks:()=>tracks,getVideoTracks:()=>[tracks[0]],getAudioTracks:()=>[tracks[1]]};
  streams.push(stream);
  return stream;
}
const offers = () => frames.filter(frame=>frame.type==='browser_capture_offer');
const stopped = () => streams.flatMap(stream=>stream.tracks).reduce((n,track)=>n+track.stops,0);
async function control(frame) {
  sandbox.incoming=JSON.stringify({type:'browser_capture_control',...frame});
  await run('handleWsMessage(incoming)');
}
async function replay(frame=first) {await control({action:'recapture',...frame})}
async function reconnectWhileHeld() {
  socket.close();
  const timer=run('reconnectTimer'), scheduled=timers.get(timer);
  assert.equal(typeof scheduled.fn,'function','socket close schedules the real reconnect callback');
  cancel(timer);scheduled.fn(...scheduled.args);
  assert.equal(sockets.length,2,'reconnect creates one replacement socket');
  await socket.open();await replay();
  if (mode==='reconnect-reject') release.reject(new Error('retired cold acquisition failed'));
  else release.resolve();
  await opening;await run('senderParamsChain');
  assert.equal(constraints.length,2,'replacement socket must acquire fresh media after old work retires');
  assert.equal(offers().length,1,'only the replacement startup publishes an offer');
  assert.equal(offeredOn[0],sockets[1],'replacement offer is sent on the current socket');
  assert.deepEqual([offers()[0].capture_generation,offers()[0].target_id],[1,'fixture'],'replacement keeps the authorized source');
  assert.deepEqual(sockets.map(item=>item.closes),[1,0],'retired completion cannot close replacement signaling');
  assert.equal(stopped(),mode==='reconnect-reject'?0:2,'late resolved old media is released');
  assert.deepEqual(streams.at(-1).tracks.map(track=>track.readyState),['live','live'],'replacement audio/video stay live');
  assert.equal(run('window.__omnipusState.lastError'),null,'retired rejection cannot become a current failure');
  assert.equal(run('captureInFlight'),false,'replacement work drains');
  assert.equal(run('captureRerunRequested'),false,'replacement leaves no abandoned command');
  assert.deepEqual(warnings,[],'reconnect path has no hidden browser-edge failure');
  console.log('COLD_START_CASE_COMPLETE '+stage+'/'+mode);
}
function reconnectFromClose() {
  socket.close();
  const timer=run('reconnectTimer'), scheduled=timers.get(timer);
  assert.equal(typeof scheduled.fn,'function','disconnect schedules the real reconnect callback');
  cancel(timer);scheduled.fn(...scheduled.args);
}
async function replacementFailureWhileHeld() {
  const [,failureStage,retiredOutcome,owner]=mode.split('/');
  reconnectFromClose();
  assert.equal(sockets.length,2,'B replaces the disconnected A socket');
  await socket.open();await replay();
  if (retiredOutcome==='reject') release.reject(new Error('retired A acquisition failed'));
  else release.resolve();
  assert.equal(await Promise.race([replacementEntered.promise.then(()=>true),opening.then(()=>false)]),true,'B must reach its own replacement acquisition');
  if (owner==='superseded') {
    reconnectFromClose();
    assert.equal(sockets.length,3,'C replaces B before B fails');
    await socket.open();await replay();
  } else if (owner==='shutdown') await control({action:'shutdown'});
  const message='replacement '+failureStage+' denied';
  replacementResult.reject(new Error(message));
  await opening;await run('senderParamsChain');
  const state=sandbox.window.__omnipusState;
  assert.equal(sockets[0].closes,1,'B failure must never be attributed to or re-close A');
  assert.equal(sockets[0].failureNotices[0].error,null,'A disconnected before any current failure');
  if (owner==='current') {
    assert.equal(sockets[1].closes,1,'B current failure must close B to signal visible recovery');
    assert.deepEqual(sockets[1].failureNotices,[{error:'Error: '+message,status:'error'}],'B owns the exact visible acquisition error');
    assert.equal(state.lastError,'Error: '+message,'current failure is retained for diagnostics');
    assert.equal(state.wsState,'closed','failed current signaling is not left open');
    assert.equal(state.status,'reconnecting','B failure enters the existing visible recovery path');
    assert.equal(run('pingBeaconTimer'),null,'failed B cannot keep a healthy-looking heartbeat');
    assert.notEqual(run('reconnectTimer'),null,'B failure schedules its own restart');
    assert.deepEqual(offers(),[],'failed replacement cannot publish an offer');
    assert.equal(state.history.filter(item=>item.evt.includes(message)).length,1,'B failure must be published once, not again through A');
  } else {
    assert.equal(state.lastError,null,'retired B failure cannot poison shutdown or C');
    assert.equal(state.history.filter(item=>item.evt.includes(message)).length,0,'retired B failure cannot be published against another owner');
    if (owner==='superseded') {
      assert.deepEqual(sockets.map(item=>item.closes),[1,1,0],'late B failure must not close healthy C');
      assert.equal(offers().length,1,'C publishes the only current offer');
      assert.equal(offeredOn[0],sockets[2],'the offer belongs to C, not A or B');
      assert.deepEqual(streams.at(-1).tracks.map(track=>track.readyState),['live','live'],'C keeps its current audio/video');
    } else {
      assert.equal(state.status,'shutdown','late B failure cannot replace shutdown');
      assert.deepEqual(offers(),[],'shutdown cannot publish replacement work');
      assert.equal(run('reconnectTimer'),null,'shutdown cannot schedule another restart');
    }
  }
  assert.equal(run('captureInFlight'),false,'shared loop drains after the owning pass settles');
  assert.equal(run('captureRerunRequested'),false,'no abandoned queued work');
  assert.deepEqual(warnings,[],'browser fixture must support the exercised path');
  console.log('COLD_START_CASE_COMPLETE '+stage+'/'+mode);
}
async function main() {
  const initial={...first};
  if (mode==='enriched') {delete initial.expected_width;delete initial.expected_height}
  sandbox={
    window:{__omnipusCapture:{token:'synthetic-not-recorded',ingestUrl:'ws://fixture',stunServer:'',...initial}},
    console:{log(){},warn(...args){warnings.push(args.map(String).join(' '))}},
    setTimeout:later,clearTimeout:cancel,setInterval:every,clearInterval:cancel,
    WebSocket:Socket,RTCPeerConnection:Peer,RTCRtpSender:{getCapabilities:()=>({codecs:[]})},
    chrome:{
      runtime:{getManifest:()=>({version:'test'})},
      debugger:{getTargets:async()=>{await barrier('lookup');return [
        {id:'fixture',type:'page',tabId:7},{id:'replacement',type:'page',tabId:99}]}},
      tabs:{get:async()=>({width:633,height:741}),update:async()=>({})},
      tabCapture:{getMediaStreamId:async selection=>{
        selections.push(selection.targetTabId);await barrier('stream-id');return 'source-'+selection.targetTabId}},
    },
    navigator:{mediaDevices:{getUserMedia:async request=>{
      constraints.push(request);const stream=streamFor(request);await barrier('media');return stream}}},
  };
  vm.createContext(sandbox);
  vm.runInContext(source,sandbox);
  run=code=>vm.runInContext(code,sandbox);
  assert.ok(socket instanceof Socket, 'real entry point creates the socket');
  opening=socket.open();
  assert.equal(await Promise.race([entered.promise.then(()=>true),opening.then(()=>false)]),true,'startup must reach the controlled barrier');
  assert.equal(frames.filter(frame=>frame.type==='browser_capture_hello').length,1,'one hello precedes capture');
  assert.equal(offers().length,0,'no offer may escape the held stage');
  if (mode.startsWith('owner-failure/')) {await replacementFailureWhileHeld();return}
  if (mode.startsWith('reconnect-')) {await reconnectWhileHeld();return}
  let expected=first;
  if (mode==='changed-target') {expected=replacement;await replay(expected)}
  else if (mode==='changed-geometry') {expected={...replacement,target_id:'fixture'};await replay(expected)}
  else if (mode==='changed-generation') {expected={...first,capture_generation:2};await replay(expected)}
  else if (mode==='stale') {
    // Advance from generation one to two, then reject a delayed generation one.
    expected=replacement;await replay(expected);
    await assert.rejects(()=>replay(first),{name:'Error',message:'stale capture generation'});
  } else if (mode.startsWith('conflicting-')) {
    const invalid={...first};
    if (mode==='conflicting-target') invalid.target_id='replacement';
    if (mode==='conflicting-geometry') invalid.expected_width=888;
    if (mode==='conflicting-scale') invalid.capture_scale=2;
    await assert.rejects(()=>replay(invalid),{name:'Error',message:'capture generation reused for a different source or geometry'});
  } else if (mode!=='baseline') {
    await replay();
    if (mode==='repeated-replay') await replay();
  }
  if (mode==='shutdown') await control({action:'shutdown'});
  if (mode==='media-rejection') release.reject(new Error('cold media denied'));
  else release.resolve();
  await opening;
  await run('senderParamsChain');
  assert.equal(run('captureInFlight'),false,'startup operation drains');
  assert.equal(run('captureRerunRequested'),false,'no abandoned queued command');
  if (mode==='shutdown') {
    assert.deepEqual(offers(),[],'shutdown cannot publish an offer');
    assert.equal(constraints.length,stage==='lookup'||stage==='stream-id'?0:1,'shutdown never starts a queued capture');
    assert.equal(stopped(),streams.length*2,'shutdown releases every acquired audio/video track');
    assert.equal(run('window.__omnipusState.status'),'shutdown','late completion cannot resurrect startup');
  } else if (mode==='media-rejection') {
    assert.equal(run('window.__omnipusState.lastError'),'Error: cold media denied','a matching replay cannot hide a current acquisition failure');
    assert.equal(socket.closes,1,'current acquisition failure closes the failed socket');
    assert.deepEqual(offers(),[],'failed acquisition cannot publish an offer');
    assert.equal(constraints.length,1,'failure must not be silently retried as a retired attempt');
  } else {
    assert.equal(offers().length,1,'only one current initial offer');
    const offer=offers()[0];
    assert.deepEqual([offer.capture_generation,offer.target_id],[expected.capture_generation,expected.target_id],'offer identifies the requested source');
    const changed=['changed-target','changed-geometry','changed-generation','stale','enriched'].includes(mode);
    const expectedAcquisitions=changed&&(stage==='media'||stage==='offer')?2:1;
    assert.equal(counts.lookup,changed?2:1,'equivalent initial replay must not restart target lookup');
    assert.equal(constraints.length,expectedAcquisitions,'equivalent initial replay must retain the first media acquisition');
    assert.equal(stopped(),(expectedAcquisitions-1)*2,'only superseded media may be stopped');
    assert.deepEqual(streams.at(-1).tracks.map(track=>track.readyState),['live','live'],'current audio/video remain live');
    if (mode==='changed-target'||mode==='stale') {
      assert.equal(selections.at(-1),99,'replacement resolves the exact new tab');
    } else assert.equal(selections.at(-1),7,'unchanged source stays on the assigned tab');
    // Grid dimensions are rounded down to multiples of twelve by the capture
    // contract; these fixture sizes remain below the physical pixel budget.
    const dims=constraints.at(-1).video.mandatory;
    assert.deepEqual([dims.maxWidth,dims.maxHeight],[Math.floor(expected.expected_width/12)*12,Math.floor(expected.expected_height/12)*12],'capture uses the requested immutable geometry');
    if (mode.startsWith('recovery-')) {
      const connected=mode!=='recovery-before-answer'&&mode!=='recovery-unanswered-overlap';
      if (connected) {
        sandbox.incoming=JSON.stringify({type:'browser_capture_answer',sdp:'v=0\r\n',capture_generation:offer.capture_generation,target_id:offer.target_id,offer_id:offer.offer_id});
        await run('handleWsMessage(incoming)');await run('senderParamsChain');
        assert.equal(peers[0].connectionState,'connected','recovery begins on a connected peer');
      }
      if (mode.endsWith('overlap')) {
        const held={entered:deferred(),release:deferred()};recoveryBarrier=held;
        const recovery=replay();
        try {
          assert.equal(await Promise.race([held.entered.promise.then(()=>true),recovery.then(()=>false)]),true,'later recovery must reach media acquisition');
          await replay();
        } finally {held.release.resolve()}
        await recovery;await run('senderParamsChain');
        assert.equal(constraints.length,3,'a later recovery command must still supersede overlapping recovery work');
        assert.equal(stopped(),4,'superseded recovery releases the old and retired replacement audio/video');
        assert.deepEqual(streams.at(-1).tracks.map(track=>track.readyState),['live','live'],'latest recovery stream survives');
        const active=run('currentPC');
        assert.equal(active,peers.at(-1),'latest recovery owns the current peer');
        assert.deepEqual(active.getSenders().map(sender=>sender.track),streams.at(-1).tracks,'current peer sends the newest audio and video tracks');
        if (active.signalingState==='have-local-offer') {
          const latest=offers().at(-1);
          assert.deepEqual([latest.capture_generation,latest.target_id],[1,'fixture'],'recovery offer retains the authorized identity');
          sandbox.incoming=JSON.stringify({type:'browser_capture_answer',sdp:'v=0\r\n',capture_generation:latest.capture_generation,target_id:latest.target_id,offer_id:latest.offer_id});
          await run('handleWsMessage(incoming)');await run('senderParamsChain');
        }
        assert.equal(active.connectionState,'connected','latest recovery media has a usable negotiated peer');
      } else {
        await replay();await run('senderParamsChain');
        assert.equal(constraints.length,2,'later same-generation recovery must acquire new media');
        assert.equal(stopped(),2,'later recovery releases old audio and video');
        assert.equal(offers().length,connected?1:2,'connected recovery retains negotiation; unanswered recovery re-offers');
        assert.equal(peers.length,connected?1:2,'only connected recovery retains its peer');
        assert.equal(peers[0].closes,connected?0:1,'connected recovery cannot close its peer');
        assert.deepEqual(replaced,connected?['video','audio']:[],'connected recovery replaces both sender tracks');
      }
    }
    assert.equal(socket.closes,0,'successful capture does not reconnect');
    assert.equal(run('window.__omnipusState.lastError'),null,'success has no hidden capture failure');
  }
  assert.deepEqual(warnings,[],'browser edge must support the exercised encoder path');
  console.log('COLD_START_CASE_COMPLETE '+stage+'/'+mode);
}
main().then(()=>{process.exitCode=0}).catch(error=>{console.error(error);process.exitCode=1}).finally(async()=>{
  release.resolve();replacementResult.resolve();
  try {
    if (run) await control({action:'shutdown'});
    if (opening) await opening;
  } finally {
    for (const timer of timers.keys()) cancel(timer);
  }
});
`
