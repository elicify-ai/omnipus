// Private evaluation artifacts, NOT gateway/SPA wire-format declarations.
// Oracles: chat-history-paging-spec.md::Adopted performance envelope / Section H.
import fs from 'node:fs';

export const ROWS = ['Data', 'Server healthy-index', 'UI ready', 'Normal reading',
  'Prefix independence', 'Row memory/replay', 'Offline utility'];
export const missing = (reason, measurements = {}) => ({ status: 'UNMEASURED', reason, measurements });
export const result = (pass, measurements, reason) => ({ status: pass ? 'PASS' : 'FAIL', reason, measurements });
export function percentile(values, fraction = 0.95) {
  if (!values.length || values.some(v => !Number.isFinite(v) || v < 0)) return null;
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.ceil(fraction * sorted.length) - 1]; // nearest rank; 30 -> sample 29
}
export function timingVerdict(samples, maximum, required = 30) {
  const p95_ms = percentile(samples);
  if (p95_ms === null || samples.length !== required) return missing(
    `need exactly ${required} finite nonnegative samples; received ${samples.length}`, { samples_ms: samples, p95_ms });
  return result(p95_ms <= maximum, { samples_ms: samples, p95_ms, target_ms: maximum }, 'nearest-rank p95');
}
export function combine(parts, measurements = {}) {
  const values = Object.values(parts);
  const status = values.some(v => v.status === 'FAIL') ? 'FAIL'
    : values.some(v => v.status === 'UNMEASURED') || !values.length ? 'UNMEASURED' : 'PASS';
  return { status, measurements, parts };
}

export const WORK_FIELDS = ['selected_archive_records', 'selected_archive_bytes',
  'lookahead_archive_records', 'lookahead_archive_bytes', 'dependency_archive_records',
  'dependency_archive_bytes', 'recovery_archive_records', 'recovery_archive_bytes',
  'discarded_archive_records', 'discarded_archive_bytes', 'archive_records_read', 'archive_bytes_read',
  'disposition_records_read', 'disposition_bytes_read', 'index_bytes_read', 'dependency_count',
  'recovery_items', 'recovery_anchor_records', 'recovery_overflow_records', 'retention_retries'];
export function validateDiagnostic(row, { healthy = false } = {}) {
  if (row.message !== 'history_page' || row.event !== 'history_page') throw new Error('missing exact history_page diagnostic');
  if (!Number.isFinite(row.duration_ms) || row.duration_ms < 0) throw new Error('invalid duration_ms');
  for (const field of WORK_FIELDS) if (!Number.isSafeInteger(row[field]) || row[field] < 0) throw new Error(`missing/invalid actual-work counter: ${field}`);
  for (const unit of ['records', 'bytes']) {
    const total = ['selected', 'lookahead', 'dependency', 'recovery', 'discarded'].reduce((n, kind) => n + row[`${kind}_archive_${unit}`], 0);
    if (row[`archive_${unit}_read`] !== total) throw new Error(`contradictory archive_${unit}_read`);
  }
  // Spec T18 permits one saved-anchor validation on an otherwise healthy page;
  // it is separately charged recovery I/O, NOT index construction/suffix replay.
  if (healthy && (row.outcome !== 'success' || row.recovery_items !== 0 || row.recovery_anchor_records > 1 ||
    row.recovery_overflow_records !== 0 || row.recovery_archive_records > row.recovery_anchor_records)) throw new Error('healthy-index sample includes failure/recovery; construction cannot be timed as a healthy page');
  return row;
}
export function readDiagnostics(file) {
  if (!fs.existsSync(file)) return [];
  const text = fs.readFileSync(file, 'utf8');
  return text.slice(0, text.lastIndexOf('\n') + 1).split('\n').filter(Boolean)
    .map(line => JSON.parse(line)).filter(row => row.message === 'history_page' && row.event === 'history_page');
}
export function allocationReceipt(text, candidate) {
  const lines = text.trimEnd().split('\n');
  if (lines.length !== 3 || lines[0] !== `HEAD: ${candidate}`) throw new Error('Section-H receipt must have exact candidate header and exactly two records');
  const records = lines.slice(1).map(line => JSON.parse(line));
  if (records[0].case !== 'short_prefix' || records[1].case !== 'long_prefix') throw new Error('Section-H short/long order missing');
  for (const row of records) {
    if (row.message !== 'history_page_measurement' || row.limit !== 40 || row.min_rows !== 20 ||
      !Number.isSafeInteger(row.sample_count) || row.sample_count < 1 ||
      !Number.isSafeInteger(row.allocated_bytes) || row.allocated_bytes <= 0) throw new Error('invalid/missing Section-H actual allocation evidence');
  }
  const delta_fraction = Math.abs(records[1].allocated_bytes - records[0].allocated_bytes) / records[0].allocated_bytes;
  return result(delta_fraction <= 0.10, { records, delta_fraction, maximum_fraction: 0.10 }, 'existing isolated complete-page Section-H producer; never process heap delta');
}
export function prefixWork(pair, candidate) {
  if (pair.candidate !== candidate || pair.prefix_groups?.join() !== '1000,100000' ||
    pair.same_suffix_and_dependencies !== true || pair.omitted_prefix_decoded !== 0 || pair.samples?.length !== 2) throw new Error('prefix work needs candidate-bound identical-suffix pair and explicit zero omitted-prefix decoding');
  const [short, long] = pair.samples.map(row => validateDiagnostic(row, { healthy: true }));
  for (const row of [short, long]) if (row.limit !== 40 || row.min_rows !== 20 || row.source_group_count !== 40 || row.row_count !== 40 ||
    row.selected_archive_records !== 40 || row.selected_archive_bytes <= 0 || row.lookahead_archive_records > 1 ||
    row.dependency_archive_records !== 0 || row.discarded_archive_records !== 0 || row.disposition_records_read !== 0) throw new Error('prefix pair must satisfy the independent normal T18 40-row physical-work oracle');
  const fields = ['selected_archive_records', 'selected_archive_bytes', 'dependency_archive_records', 'dependency_archive_bytes'];
  const delta_fraction = Object.fromEntries(fields.map(key => [key, short[key] === 0 ? (long[key] === 0 ? 0 : null) : Math.abs(long[key] - short[key]) / short[key]]));
  return result(Object.values(delta_fraction).every(value => value !== null && value <= .10), { pair, delta_fraction }, 'imported real history_page diagnostic pair; no replacement producer');
}
export function capVerdict(samples, required = 100) {
  if (samples.some(row => Number.isInteger(row.reader) && row.reader > 500 || Number.isInteger(row.tail) && row.tail > 100)) return result(false, { samples }, 'actual retained source bounds exceeded');
  if (samples.length !== required || samples.some(row => !Number.isInteger(row.reader) || row.reader < 0 || !Number.isInteger(row.tail) || row.tail < 0)) return missing(`need ${required} actual retained-state samples, not mounted DOM counts`, { samples });
  return result(true, { samples, reader_max: Math.max(...samples.map(row => row.reader)), tail_max: Math.max(...samples.map(row => row.tail)) }, 'actual retained source-group bounds 500 reader / 100 tail');
}
export function normalVerdict(samples) {
  if (samples.some(s => s.gap === true || Number.isFinite(s.drift_px) && s.drift_px > 1)) return result(false, { samples }, 'viewport gap or settled anchor drift >1 CSS pixel');
  if (samples.length !== 30 || samples.some(s => typeof s.gap !== 'boolean' || !Number.isFinite(s.drift_px) || !Number.isFinite(s.response_ms) || !Number.isFinite(s.maximum_viewports_per_second) || s.maximum_viewports_per_second < 0)) return missing('need 30 comparable anchors, delivered response timings and per-frame viewport coverage', { samples });
  if (samples.some(s => s.response_ms > 500 || s.maximum_viewports_per_second > 1.001)) return missing('normal-reading reference conditions not met: response >500 ms or scroll >1 viewport/second', { samples });
  return result(true, { samples }, '30 continuous boundaries, no observed gap, <=1 CSS pixel settled drift');
}
function observation(name, dataset) {
  const row = dataset.rows[name], browser = dataset.browser;
  const number = value => Number.isFinite(value) ? value.toFixed(2) : 'UNMEASURED';
  if (name === 'Data' && dataset.data) return `${dataset.data.groups ?? dataset.data.source_records ?? 'UNMEASURED'} groups/source records; ${number(dataset.data.encoded_archive_bytes / 1048576)} MiB; ${dataset.data.actual_days} days`;
  if (name === 'Server healthy-index') return Object.entries(row.parts ?? {}).map(([key,value]) => `${key}: ${number(value.measurements?.p95_ms)} ms (${value.measurements?.samples_ms?.length ?? 0}/30)`).join('; ');
  if (name === 'UI ready' && browser) return `${browser.opens.length}/30 opens; sample p95 ${number(percentile(browser.opens.map(v=>v.ready_ms)))} ms`;
  if (name === 'Normal reading' && browser) return `${browser.normal.length}/30 boundaries; max observed drift ${number(browser.normal.length ? Math.max(...browser.normal.map(v=>v.drift_px ?? NaN)) : NaN)} px`;
  if (name === 'Row memory/replay' && browser) return `${browser.traversal.length}/100 pages; retained JS heap ${browser.heaps.map(v=>number(v.retained_js_heap_bytes)).join('/')} B; HTTP ${browser.traffic.http_encoded_bytes} B; WS ${browser.traffic.ws_received_bytes} B / ${browser.traffic.ws_received_frames} frames`;
  return row.status === 'UNMEASURED' ? 'UNMEASURED' : 'See JSON measurements';
}
export function markdown(report) {
  const lines = ['# B25 on-demand evaluation', '', `Candidate: \`${report.candidate}\``,
    `Host: ${report.host.hostname} / ${report.host.platform} ${report.host.release} / ${report.host.cpu}`, '',
    '**This report is measurements, not independent B25 acceptance. UNMEASURED is not PASS.**', '',
    '| Data kind | Envelope row | Status | Observed numbers (partial != accepted) | Reason |', '|---|---|---|---|---|'];
  for (const [kind, dataset] of Object.entries(report.datasets)) {
    for (const [row, value] of Object.entries(dataset.rows)) lines.push(`| ${kind} | ${row} | ${value.status} | ${observation(row,dataset)} | ${(value.reason ?? Object.entries(value.parts ?? {}).map(([k,v]) => `${k}: ${v.status}${v.reason ? ` (${v.reason})` : ''}`).join('; ')).replaceAll('|', '/').replaceAll('\n', ' ')} |`);
  }
  lines.push('', `Full numeric samples and provenance: ${report.report_path}`, '',
    'Code correct and tested: only the named evaluator controls / rehearsal receipts establish this; no full feature verdict.',
    'Reachable by a user or agent: this utility is explicitly on demand; live control execution and independent validation remain separate.', '',
    'Screenshots/traces can contain permissioned content. Keep the entire evidence directory private; do not commit it.', '');
  return lines.join('\n');
}
