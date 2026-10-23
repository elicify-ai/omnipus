// Instrument controls only. No feature/B25 acceptance and no CI registration.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { percentile, timingVerdict, combine, allocationReceipt, validateDiagnostic, WORK_FIELDS,
  capVerdict, normalVerdict, missing, prefixWork } from './metrics.mjs';
import { FORBIDDEN, optionalCopy, safeOutput, installReadAudit } from './safety.mjs';
import { options } from './evaluate.mjs';
import { syntheticDays } from './dataset.mjs';
import { isolatedEnvironment, verifyArtifactAttestation } from './runtime.mjs';

const SHA = 'a'.repeat(40);
const receipt = (short, long, sha = SHA) => `HEAD: ${sha}\n${JSON.stringify({ message:'history_page_measurement', case:'short_prefix', sample_count:3, limit:40, min_rows:20, allocated_bytes:short })}\n${JSON.stringify({ message:'history_page_measurement', case:'long_prefix', sample_count:3, limit:40, min_rows:20, allocated_bytes:long })}\n`;
const diagnostic = () => ({ message:'history_page', event:'history_page', carrier:'rest_messages', mode:'latest', outcome:'success', duration_ms:0,
  ...Object.fromEntries(WORK_FIELDS.map(key => [key, 0])), selected_archive_records:40, selected_archive_bytes:400,
  archive_records_read:40, archive_bytes_read:400, limit:40, min_rows:20, source_group_count:40, row_count:40 });

test('nearest-rank p95 of 30 values is the 29th; input order does not change the oracle', () => {
  const values = Array.from({ length:30 }, (_,n)=>n+1);
  assert.equal(percentile(values), 29); assert.equal(percentile(values.reverse()), 29);
});
for (const maximum of [300,750,1500]) test(`timing accepts exact ${maximum}ms and rejects +1ms`, () => {
  assert.equal(timingVerdict(Array(30).fill(maximum), maximum).status, 'PASS');
  assert.equal(timingVerdict(Array(30).fill(maximum+1), maximum).status, 'FAIL');
});
for (const count of [0,29,31]) test(`timing cannot PASS with ${count} samples`, () => {
  assert.equal(timingVerdict(Array(count).fill(1),300).status, 'UNMEASURED');
});
test('unknown/NaN/negative timings are never zero or PASS', () => {
  for (const value of [NaN,Infinity,-1,null,undefined]) assert.equal(timingVerdict(Array(30).fill(value),300).status, 'UNMEASURED');
});
test('aggregate preserves FAIL over unknown and unknown over PASS', () => {
  assert.equal(combine({a:missing('absent'),b:{status:'PASS'}}).status,'UNMEASURED');
  assert.equal(combine({a:missing('absent'),b:{status:'FAIL'}}).status,'FAIL');
  assert.equal(combine({}).status,'UNMEASURED');
});
test('Section H accepts exactly 10 percent and catches actual +1 allocated byte', () => {
  assert.equal(allocationReceipt(receipt(100,110),SHA).status,'PASS');
  assert.equal(allocationReceipt(receipt(100,111),SHA).status,'FAIL');
});
test('Section H rejects wrong SHA and extra receipt record', () => {
  assert.throws(()=>allocationReceipt(receipt(100,100,'b'.repeat(40)),SHA), /exact candidate header/);
  assert.throws(()=>allocationReceipt(receipt(100,100)+'{}\n',SHA), /exactly two records/);
});
test('Section H rejects missing or zero actual allocations', () => {
  assert.throws(()=>allocationReceipt(receipt(0,0),SHA), /actual allocation evidence/);
  assert.throws(()=>allocationReceipt(receipt(100,100).replace('"sample_count":3','"sample_count":0'),SHA), /actual allocation evidence/);
});
test('healthy diagnostic control sees real work and rejects absent actual counters', () => {
  assert.equal(validateDiagnostic(diagnostic(),{healthy:true}).selected_archive_records,40);
  const missing = diagnostic(); delete missing.selected_archive_bytes;
  assert.throws(()=>validateDiagnostic(missing), /missing\/invalid actual-work counter: selected_archive_bytes/);
});
test('healthy diagnostic refuses construction/recovery instead of timing it as fast', () => {
  const row = diagnostic(); row.recovery_items=1;
  assert.throws(()=>validateDiagnostic(row,{healthy:true}), /includes failure\/recovery/);
});
test('T18 permits one separately counted saved-anchor validation, not two recovery reads', () => {
  const row=diagnostic(); Object.assign(row,{recovery_anchor_records:1,recovery_archive_records:1,recovery_archive_bytes:8,archive_records_read:41,archive_bytes_read:408});
  assert.equal(validateDiagnostic(row,{healthy:true}).recovery_anchor_records,1);
  Object.assign(row,{recovery_archive_records:2,archive_records_read:42});
  assert.throws(()=>validateDiagnostic(row,{healthy:true}),/includes failure\/recovery/);
});
test('artifact attestation accepts exact digest pair and rejects other dirty paths or hashes', () => {
  const receipt={candidate:SHA,gateway_sha256:'1',fixture_sha256:'2',attested_by:'dispatcher',allowed_dirty_paths:['docs/internal/design/evidence/design-system-production-provenance.json']};
  assert.equal(verifyArtifactAttestation(receipt,SHA,'1','2'),receipt);
  assert.throws(()=>verifyArtifactAttestation(receipt,SHA,'wrong','2'),/ONLY generated design-system provenance/);
  assert.throws(()=>verifyArtifactAttestation({...receipt,allowed_dirty_paths:['pkg/gateway/handler.go']},SHA,'1','2'),/ONLY generated design-system provenance/);
});
test('output rejects a direct forbidden root before creating any output', () => {
  assert.throws(()=>safeOutput(path.join(FORBIDDEN,'b25-report')),/refuses the founder live-data root/);
});
test('physical counter contradiction cannot manufacture bounded work', () => {
  const row = diagnostic(); row.archive_records_read=0;
  assert.throws(()=>validateDiagnostic(row), /contradictory archive_records_read/);
});
test('prefix comparator requires independently bound zero omitted-prefix decoding', () => {
  const pair={candidate:SHA,prefix_groups:[1000,100000],same_suffix_and_dependencies:true,omitted_prefix_decoded:0,samples:[diagnostic(),diagnostic()]};
  assert.equal(prefixWork(pair,SHA).status,'PASS');
  pair.omitted_prefix_decoded=1;
  assert.throws(()=>prefixWork(pair,SHA), /explicit zero omitted-prefix decoding/);
});
test('actual 500 reader and 100 tail boundaries accept; +1 always fails', () => {
  const samples=Array.from({length:100},()=>({reader:500,tail:100}));
  assert.equal(capVerdict(samples).status,'PASS');
  samples[50]={reader:501,tail:100}; assert.equal(capVerdict(samples).status,'FAIL');
  samples[50]={reader:500,tail:101}; assert.equal(capVerdict(samples).status,'FAIL');
});
test('99 or uninstrumented retained-state samples are UNMEASURED, not DOM-inferred PASS', () => {
  assert.equal(capVerdict(Array(99).fill({reader:1,tail:1})).status,'UNMEASURED');
  assert.equal(capVerdict(Array(100).fill({})).status,'UNMEASURED');
});
test('30 normal boundaries accept exact 1 CSS pixel; visible gap and +epsilon drift fail', () => {
  const samples=Array.from({length:30},()=>({drift_px:1,gap:false,response_ms:500,maximum_viewports_per_second:1}));
  assert.equal(normalVerdict(samples).status,'PASS');
  samples[2].drift_px=1.001; assert.equal(normalVerdict(samples).status,'FAIL');
  samples[2].drift_px=0; samples[2].gap=true; assert.equal(normalVerdict(samples).status,'FAIL');
});
test('missing anchor is UNMEASURED, never zero drift', () => {
  assert.equal(normalVerdict(Array(30).fill({drift_px:null,gap:false,response_ms:1})).status,'UNMEASURED');
});
test('out-of-envelope response is not normal-reading PASS', () => {
  assert.equal(normalVerdict(Array(30).fill({drift_px:0,gap:false,response_ms:501,maximum_viewports_per_second:.75})).status,'UNMEASURED');
});
test('default synthetic options never open or inspect the forbidden live-data root', () => {
  const audit=installReadAudit();
  try {
    let ioCalls=0;
    const poison = new Proxy({}, {get(){ioCalls++;throw new Error('default optional input performed I/O');}});
    assert.equal(optionalCopy(undefined,undefined,'--genuine-copy',poison),null);
    const opts=options(['--candidate',SHA,'--gateway','/isolated/gateway','--fixture-binary','/isolated/session.test',
      '--output','/isolated/report','--groups','2000','--smoke']);
    assert.equal(opts.genuine,null); assert.equal(opts.synthetic,null); assert.equal(ioCalls,0);
    const observed=audit.snapshot();
    assert.equal(observed.forbidden_root_opened,false); assert.equal(observed.blocked_attempts,0);
    assert.equal(observed.read_paths.some(p=>p===FORBIDDEN||p.startsWith(FORBIDDEN+path.sep)),false);
  } finally {audit.restore();}
});
test('explicit live-data directory is refused before any source filesystem call', () => {
  const poison=new Proxy({}, {get(){throw new Error('forbidden source was inspected');}});
  assert.throws(()=>optionalCopy(FORBIDDEN,'/isolated','--genuine-copy',poison), /refuses the founder live-data root/);
  assert.throws(()=>optionalCopy(path.join(FORBIDDEN,'sessions','one'),'/isolated','--genuine-copy',poison), /refuses the founder live-data root/);
});
test('missing copy and a symlink alias to live data are refused; no source read', () => {
  const directory=fs.mkdtempSync(path.join(os.tmpdir(),'b25-input-control-'));
  // Native macOS temp root may itself be symlinked; realpath only this owned root.
  const root=fs.realpathSync(directory);
  try {
    assert.throws(()=>optionalCopy(path.join(root,'missing'),root,'--genuine-copy'), {code:'ENOENT'});
    fs.symlinkSync(FORBIDDEN,path.join(root,'alias'));
    assert.throws(()=>optionalCopy(path.join(root,'alias'),root,'--genuine-copy'), /refuses symlinked input/);
  } finally {fs.rmSync(root,{recursive:true});}
});
test('default child HOME and OMNIPUS_HOME are isolated, never founder-home fallback', () => {
  const env=isolatedEnvironment('/isolated/b25/home');
  assert.equal(env.HOME,'/isolated/b25/home'); assert.equal(env.USERPROFILE,'/isolated/b25/home');
  assert.equal(env.XDG_CONFIG_HOME,'/isolated/b25/home/config');
});
test('synthetic plan independently has 2000 groups on 90 distinct days with all five shapes', () => {
  const plan=syntheticDays(2000,'fixture-agent',new Date('2026-10-23T00:00:00Z'));
  assert.equal(plan.days.length,90); assert.equal(new Set(plan.days.map(d=>d.day)).size,90);
  assert.equal(plan.days[0].day,'2026-07-26'); assert.equal(plan.days.at(-1).day,'2026-10-23');
  assert.equal(plan.days.reduce((n,d)=>n+d.entries.length,0),2000);
  assert.equal(Object.values(plan.counts).reduce((a,b)=>a+b),2000);
  assert.deepEqual(Object.keys(plan.counts),['text','tools','images','clear','system']);
  for(const count of Object.values(plan.counts)) assert.ok(count>0);
});
