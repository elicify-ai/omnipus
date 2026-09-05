#!/usr/bin/env node
// Classify each captured chunk by container box structure (init vs media),
// and compute steady-state forwarded bandwidth.
'use strict';
const fs = require('fs'), path = require('path');
const run = process.argv[2] || 'dash';
const dir = path.join(__dirname, 'out', run);
const man = JSON.parse(fs.readFileSync(path.join(dir, 'manifest.json'), 'utf8'));

function boxes(buf) {
  const out = []; let o = 0;
  while (o + 8 <= buf.length && out.length < 12) {
    let size = buf.readUInt32BE(o);
    const type = buf.toString('latin1', o + 4, o + 8);
    if (!/^[\x20-\x7e]{4}$/.test(type)) break;
    if (size === 1) { if (o + 16 > buf.length) break; size = Number(buf.readBigUInt64BE(o + 8)); }
    else if (size === 0) { out.push(type); break; }
    if (size < 8) break;
    out.push(type); o += size;
  }
  return out;
}
// Classify an append: INIT (ftyp/moov, EBML header), BOUNDARY (starts a fresh
// media segment: moof/styp, or a WebM Cluster), or CONT (a continuation slice
// that begins mid-segment -- YouTube splits segments across many appendBuffer
// calls at arbitrary byte offsets, so this is the common case there).
function classify(buf) {
  if (buf.length >= 4) {
    const m = buf.readUInt32BE(0);
    if (m === 0x1A45DFA3) return 'INIT';       // EBML header  -> WebM init
    if (m === 0x1F43B675) return 'BOUNDARY';   // Cluster      -> WebM media segment start
  }
  const bx = boxes(buf);
  if (!bx.length) return 'CONT';
  if (bx.includes('ftyp') || bx.includes('moov')) return 'INIT';
  if (bx[0] === 'moof' || bx[0] === 'styp' || bx[0] === 'sidx' || bx[0] === 'emsg') return 'BOUNDARY';
  return 'CONT';
}

const appends = man.events.filter(e => e.ev === 'append');
console.log(`run=${run}  url=${man.url}`);
console.log(`appends=${appends.length} totalBytes=${man.totalBytes} (${(man.totalBytes/1048576).toFixed(2)} MB) missing=${man.missingChunkData}`);
console.log(`loadavg at record: ${man.loadavg.map(x=>x.toFixed(2)).join(', ')} on ${man.cpus} cpus\n`);

const perSb = new Map();
console.log('idx  sbId  bytes      t(ms)  kind    boxes');
for (const a of appends) {
  let kind = '?', bx = [];
  if (a.file) {
    const buf = fs.readFileSync(path.join(dir, a.file));
    kind = classify(buf);
    bx = boxes(buf);
    if (buf.length >= 4 && (buf.readUInt32BE(0) === 0x1A45DFA3 || buf.readUInt32BE(0) === 0x1F43B675)) bx = ['<webm>'];
  }
  if (!perSb.has(a.sb)) perSb.set(a.sb, []);
  perSb.get(a.sb).push({ ...a, kind });
  console.log(`${String(a.id).padStart(4)}  sb${a.sb}  ${String(a.bytes).padStart(9)}  ${String(a.t).padStart(6)}  ${kind.padEnd(6)}  ${bx.slice(0,5).join(',')}`);
}

console.log('\n--- per source buffer ---');
for (const [sb, list] of perSb) {
  const init = list.filter(x => x.kind === 'INIT');
  const bnd = list.filter(x => x.kind === 'BOUNDARY');
  const cont = list.filter(x => x.kind === 'CONT');
  const mime = (man.events.find(e => e.ev === 'addSourceBuffer' && e.sb === sb) || {}).mime;
  console.log(`sb${sb} ${mime}   (${list.length} appends, ${list.reduce((s,x)=>s+x.bytes,0)} bytes)`);
  console.log(`  INIT     : ${init.length}  bytes=[${init.map(x=>x.bytes).join(', ')}]  at t=[${init.map(x=>x.t).join(', ')}]ms`);
  console.log(`  BOUNDARY : ${bnd.length}  (appends that start a fresh media segment)`);
  console.log(`  CONT     : ${cont.length}  (continuation slices, begin mid-segment)`);
  console.log(`  => segment granularity: ${cont.length === 0 ? 'WHOLE SEGMENTS per append' : 'BYTE-STREAM SLICES (' + (cont.length/list.length*100).toFixed(0) + '% are continuations)'}`);
  console.log(`  ordering: ${list.map(x=>x.kind==='INIT'?'I':x.kind==='BOUNDARY'?'B':'.').join('').slice(0,200)}`);
}

// ---- steady-state bandwidth (exclude first 5s of startup burst) ----
console.log('\n--- forwarded bandwidth ---');
const t0 = 5000, tEnd = Math.max(...appends.map(a => a.t));
const steady = appends.filter(a => a.t >= t0);
const span = (tEnd - t0) / 1000;
const sBytes = steady.reduce((s, a) => s + a.bytes, 0);
console.log(`whole capture : ${(man.totalBytes/1048576).toFixed(2)} MB over ${(tEnd/1000).toFixed(1)}s = ${(man.totalBytes*8/1e6/(tEnd/1000)).toFixed(2)} Mbps`);
if (span > 0) console.log(`steady (t>5s) : ${(sBytes/1048576).toFixed(2)} MB over ${span.toFixed(1)}s = ${(sBytes*8/1e6/span).toFixed(2)} Mbps`);
// media time actually delivered, from the last sample's buffered end
const last = man.samples.filter(s => s.buf && s.buf.length).pop();
if (last) {
  const mediaSec = last.buf[last.buf.length-1][1] - last.buf[0][0];
  console.log(`media seconds buffered: ${mediaSec.toFixed(1)}s  =>  ${(man.totalBytes*8/1e6/mediaSec).toFixed(2)} Mbps per second-of-video`);
  console.log(`  (this is the number to compare against re-encode cost; the capture window`);
  console.log(`   over-reads because the player buffers ahead of the playhead)`);
}
console.log(`\nresolution reached: ${[...new Set(man.samples.filter(s=>s.w).map(s=>s.w+'x'+s.h))].join(' -> ')}`);

// persist the classification so the viewer can do a correct mid-stream join
const kinds = {};
for (const [, list] of perSb) for (const a of list) kinds[a.id] = a.kind;
for (const e of man.events) if (e.ev === 'append' && kinds[e.id]) e.kind = kinds[e.id];
fs.writeFileSync(path.join(dir, 'manifest.json'), JSON.stringify(man, null, 1));
console.log(`\n[analyse] wrote per-append 'kind' back into manifest.json`);
