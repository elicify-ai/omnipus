import { readDiagnostics, validateDiagnostic, timingVerdict, combine } from './metrics.mjs';
import { indexPreflight } from './dataset.mjs';
import path from 'node:path';

const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
async function preparedRead(gateway, wire, sid, query, log) {
  const before = readDiagnostics(log).length;
  const params = new URLSearchParams(Object.entries(query).map(([key, value]) => [key, String(value)]));
  const started = performance.now();
  const response = await gateway.apiFetch('GET', `/api/v1/sessions/${encodeURIComponent(sid)}/messages?${params}`);
  const round_trip_ms = performance.now() - started;
  if (response.status !== 200) throw new Error(`history page HTTP ${response.status}; healthy-index path failed`);
  const value = wire.SessionHistoryResponse.parse(response.body);
  let rows = [];
  for (let i = 0; i < 100; i++) {
    rows = readDiagnostics(log).slice(before);
    if (rows.length) break;
    await pause(10);
  }
  if (rows.length !== 1 || rows[0].carrier !== 'rest_messages') throw new Error(`expected exactly one rest_messages history_page diagnostic; received ${rows.length}`);
  const diagnostic = validateDiagnostic(rows[0], { healthy: true });
  if (diagnostic.limit !== query.limit) throw new Error('diagnostic limit does not match actual request');
  return { value, measurement: { diagnostic, round_trip_ms } };
}

export async function serverCampaign(gateway, wire, sid, log, count = 30) {
  const parts = {};
  const samples = {};
  const session = path.join(gateway.homeDir, 'sessions', sid);
  const baseline = indexPreflight(session);
  // Capture distinct opaque boundaries OUTSIDE all measurements. Overlap is
  // intentional: distinct pages, not 30 duplicated requests and not necessarily
  // 3000 disjoint rows (the small rehearsal still measures 30 different cuts).
  const prepared = await preparedRead(gateway, wire, sid, { limit: 100, min_rows: 20 }, log);
  const boundaries = prepared.value.entries.slice(1, count).map(row => row.history_position);
  if (new Set(boundaries).size !== count - 1) throw new Error(`need ${count - 1} distinct retained boundaries for the server campaign`);
  const selectors = [{}, ...boundaries.map(before => ({ before }))];
  for (const limit of [40, 100]) {
    // Warm one real selected page; omit its diagnostic from measured samples.
    await preparedRead(gateway, wire, sid, { limit, min_rows: 20 }, log);
    const warm = [];
    for (let i = 0; i < selectors.length; i++) {
      const read = await preparedRead(gateway, wire, sid, { limit, min_rows: 20, ...selectors[i] }, log);
      warm.push({ ordinal: i + 1, ...read.measurement });
    }
    samples[`warm_${limit}`] = warm;
    parts[`warm_${limit}`] = timingVerdict(warm.map(row => row.diagnostic.duration_ms), 300);
    const cold = [];
    for (let i = 0; i < selectors.length; i++) {
      await gateway.kill9();
      gateway.assertStoppedForFixture();
      await gateway.restart(); // health/login only; first history read follows.
      const read = await preparedRead(gateway, wire, sid, { limit, min_rows: 20, ...selectors[i] }, log);
      cold.push({ ordinal: i + 1, fresh_process_pid: gateway.proc?.pid ?? null, ...read.measurement });
    }
    samples[`cold_${limit}`] = cold;
    parts[`cold_${limit}`] = timingVerdict(cold.map(row => row.diagnostic.duration_ms), 750);
  }
  const after = indexPreflight(session);
  if (baseline.checkpoint_sha256 !== after.checkpoint_sha256) throw new Error('checkpoint changed during healthy-index campaign; cannot exclude hidden reconciliation/construction');
  return combine(parts, { samples, distinct_page_count_per_case: selectors.length, index_before: baseline,
    index_after: after, timing_source: 'real history_page.duration_ms; preparation/validation only',
    cold_definition: 'new gateway process for EACH sample, existing index; not an OS disk-cache eviction', setup_diagnostic: prepared.measurement.diagnostic });
}
