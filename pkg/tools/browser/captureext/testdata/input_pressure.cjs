const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
let now = 1000;
let params = { encodings: [{ ssrc: 42 }] };
const trackChanges = [];
const senderChanges = [];
const track = {
  kind: 'video', readyState: 'live',
  getConstraints: () => ({ width: { max: 1280 }, frameRate: { min: 15, max: 30 } }),
  applyConstraints: async value => { trackChanges.push(value); },
};
const sender = {
  track,
  getParameters: () => JSON.parse(JSON.stringify(params)),
  setParameters: async value => { params = value; senderChanges.push(value); },
};
const pc = { getSenders: () => [sender], connectionState: 'connected' };
const box = {
  window: {}, console: { log() {}, warn() {}, error() {} },
  Date: class extends Date { static now() { return now; } },
  setTimeout, clearTimeout, setInterval, clearInterval,
  chrome: { runtime: { getManifest: () => ({ version: 'test' }) } },
  WebSocket: { OPEN: 1 },
};
vm.createContext(box);
vm.runInContext(fs.readFileSync(process.argv[2], 'utf8'), box);
box.testPC = pc;
vm.runInContext('currentPC = testPC;', box);
const run = code => vm.runInContext(code, box);
async function signal() { await run("handleControlFrame({action:'input_pressure'})"); }
(async () => {
  await signal();
  assert.equal(params.encodings[0].maxFramerate, 20, 'first slow input reduces encoder to 20 fps');
  assert.equal(trackChanges.at(-1).frameRate.max, 20, 'capture delivery is reduced too');
  assert.equal(trackChanges.at(-1).width.max, 1280, 'capture geometry stays unchanged');
  now += 999;
  await signal();
  assert.equal(params.encodings[0].maxFramerate, 20, 'pressure burst cannot immediately take second step');
  now += 1;
  await signal();
  assert.equal(params.encodings[0].maxFramerate, 15, 'sustained pressure reduces to floor');
  now += 1000;
  await signal();
  assert.equal(params.encodings[0].maxFramerate, 15, 'never drops below 15 fps');
  now += 9999;
  await run('recoverInputPressure(currentPC)');
  assert.equal(params.encodings[0].maxFramerate, 15, 'recovery waits full quiet period');
  now += 1;
  await run('recoverInputPressure(currentPC)');
  assert.equal(params.encodings[0].maxFramerate, 20, 'recovery restores only one step');
  now += 9999;
  await run('recoverInputPressure(currentPC)');
  assert.equal(params.encodings[0].maxFramerate, 20, 'second recovery also waits quiet period');
  now += 1;
  await run('recoverInputPressure(currentPC)');
  assert.equal(params.encodings[0].maxFramerate, undefined, 'normal encoder ceiling restored');
  assert.equal(trackChanges.at(-1).frameRate.max, 30, 'normal capture ceiling restored');
  await run("handleControlFrame({action:'unknown'})");
  assert.equal(params.encodings[0].maxFramerate, undefined, 'unknown controls cannot reduce quality');
  let release;
  const previousApply = track.applyConstraints;
  track.applyConstraints = () => new Promise(resolve => { release = resolve; });
  const before = senderChanges.length;
  const pending = signal();
  await Promise.resolve();
  const duplicate = signal();
  await Promise.resolve();
  assert.equal(senderChanges.length, before, 'blocked capture update does not queue encoder work');
  run('currentPC = null; captureGeneration += 1;');
  release();
  await Promise.all([pending, duplicate]);
  assert.equal(senderChanges.length, before, 'retired capture cannot apply late encoder constraints');
  track.applyConstraints = previousApply;
  run('currentPC = testPC;');
  await run('recoverInputPressure(currentPC)');
  assert.equal(params.encodings[0].maxFramerate, 20, 'replacement capture can apply pending pressure');
  now += 10000;
  track.applyConstraints = async () => { throw new Error('controlled capture failure'); };
  await run('recoverInputPressure(currentPC)');
  assert.equal(params.encodings[0].maxFramerate, 20, 'failed restore must not claim encoder recovery');
  track.applyConstraints = previousApply;
  await run('recoverInputPressure(currentPC)');
  assert.equal(params.encodings[0].maxFramerate, undefined, 'failed restoration retries even at normal target');
  now += 1000;
  await signal();
  const replacements = [];
  sender.track = { kind: 'video', getConstraints: () => ({ width: { max: 800 }, frameRate: { max: 30 } }), applyConstraints: async value => { replacements.push(value); } };
  run("applyVideoSenderConstraints(currentPC, {context:'recapture'})");
  await run('inputPressureApplying');
  assert.equal(replacements.at(-1).frameRate.max, 20, 'same-PC replacement source inherits active pressure');
  assert.equal(replacements.at(-1).width.max, 800, 'replacement source keeps its own geometry');
  console.log('input pressure behavior passed');
})().catch(e => { console.error(e); process.exitCode = 1; });
