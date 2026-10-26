import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { expect } from '@playwright/test';
import { test, fixtureId } from './fixtures/chat-history-paging/harness';
import { prefixFixtureDays } from './fixtures/chat-history-paging/day-fixtures';

test.use({ storageState: { cookies: [], origins: [] }, viewport: { width: 1280, height: 1000 } });
test.describe.configure({ retries: 0 });
test.setTimeout(600_000); //100,000 real fsynced writes/90 actual partitions: Fly only.

function historyDiagnostics(file: string) {
  const text = fs.readFileSync(file, 'utf8');
  return text.slice(0, text.lastIndexOf('\n') + 1).split('\n').filter(Boolean)
    .map((line) => JSON.parse(line) as Record<string, unknown>)
    .filter((row) => row.message === 'history_page' && row.event === 'history_page' && row.carrier === 'rest_messages' && row.outcome === 'success');
}

test('T18 B21 B22:equal suffix after1000 and100000 prefixes has bounded physical I/O and stable restart positions', async ({ history, page }, info) => {
  const other = history.ids.otherSession.id;
  const setup = [];
  for (const [id, prefix] of [[history.sessionId, 1000], [other, 100000]] as const) {
    setup.push(await history.seedDays(prefixFixtureDays(prefix, history.ids.agent.id), id));
  }
  await history.authenticate();
  const expected40 = Array.from({ length: 40 }, (_, n) => fixtureId('equal-suffix', 160 + n));
  const before = historyDiagnostics(history.logPath).length;
  const small = await history.read({ limit: 40, min_rows: 20 });
  const large = await history.read({ limit: 40, min_rows: 20 }, other);
  expect(small.entries.map((e) => e.id)).toEqual(expected40);
  expect(large.entries.map((e) => e.id)).toEqual(expected40);
  expect(large.entries.map(({ id, content, tool_calls }) => ({ id, content, tool_calls })))
    .toEqual(small.entries.map(({ id, content, tool_calls }) => ({ id, content, tool_calls })));
  expect([small.has_earlier, large.has_earlier]).toEqual([true, true]);
  const diagnostics = historyDiagnostics(history.logPath).slice(before);
  expect(diagnostics, 'exactly one real history_page summary per authorized page attempt').toHaveLength(2);
  for (const row of diagnostics) {
    expect(row).toMatchObject({ mode: 'latest', limit: 40, min_rows: 20, source_group_count: 40,
      selected_archive_records: 40, dependency_archive_records: 0, dependency_archive_bytes: 0,
      discarded_archive_records: 0, discarded_archive_bytes: 0, disposition_records_read: 0 });
    expect(row.lookahead_archive_records, 'at most ONE body lookahead; metadata-only continuation may read0').toBeGreaterThanOrEqual(0);
    expect(row.lookahead_archive_records).toBeLessThanOrEqual(1);
    expect(row.recovery_anchor_records).toBeLessThanOrEqual(1);
    expect(row).not.toHaveProperty('allocated_bytes'); // H: not attributable in a concurrent production event.
    for (const suffix of ['records', 'bytes'] as const) {
      const keys = ['selected', 'lookahead', 'dependency', 'recovery', 'discarded'].map((kind) => `${kind}_archive_${suffix}`);
      for (const key of keys) expect(Number.isInteger(row[key]) && Number(row[key]) >= 0, `real physical I/O counter ${key}`).toBe(true);
      expect(row[suffix === 'records' ? 'archive_records_read' : 'archive_bytes_read'], 'all five disjoint I/O categories remain visible')
        .toBe(keys.reduce((sum, key) => sum + Number(row[key]), 0));
    }
  }
  for (const metric of ['selected_archive_records', 'selected_archive_bytes', 'dependency_archive_records', 'dependency_archive_bytes'] as const) {
    const first = Number(diagnostics[0][metric]);
    const second = Number(diagnostics[1][metric]);
    expect(Math.abs(second - first), `D-A1 selected/dependency ${metric} differs by at most10%`).toBeLessThanOrEqual(first * 0.10);
  }
  expect(diagnostics[1].selected_archive_bytes, 'positive actual-byte instrument; no absent counter as0').toBeGreaterThan(0);
  const positions = large.entries.map((e) => e.history_position);
  await history.gateway.kill9();
  await history.gateway.restart();
  await history.authenticate();
  const restarted = await history.read({ limit: 40, min_rows: 20 }, other);
  expect(restarted.entries.map((e) => e.history_position), 'unchanged final bytes preserve occurrence fingerprints').toEqual(positions);
  await history.open(other);
  await history.record('prefix-independent-restart', { diagnostics, prefixCounts: [1000, 100000], suffixCount: 200, setup });
  await info.attach('reference-host', { contentType: 'application/json', body: JSON.stringify({
    os: os.platform(), architecture: os.arch(), release: os.release(), cpu: os.cpus()[0]?.model,
    commit: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
  }) });
  expect(history.traffic.failures).toEqual([]);
  await expect(page.getByTestId('virtualized-message-list')).toBeVisible();
});

test('T18 B21:isolated complete-page allocation receipts are present, exact-candidate and prefix-independent', async () => {
  const info = test.info();
  // Private CI evidence input, not a product setting. A receipt log starts with
  // HEAD:<full candidate SHA>, followed by the two isolated JSON measurements.
  const file = process.env.OMNIPUS_CHAT_HISTORY_MEASUREMENT_RECEIPT;
  if (!file) throw new Error('UNVERIFIED: isolated history_page_measurement receipt missing — required by spec34612f172/H/T18; never substitute production heap growth or0');
  expect(path.isAbsolute(file)).toBe(true);
  const text = fs.readFileSync(file, 'utf8');
  const commit = execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
  expect(text.split('\n').some((line) => line.trim() === `HEAD: ${commit}`), 'allocation evidence is bound to this exact candidate').toBe(true);
  const samples = text.split('\n').filter((line) => line.startsWith('{')).map((line) => JSON.parse(line) as Record<string, unknown>)
    .filter((row) => row.message === 'history_page_measurement');
  expect(samples.map((r) => r.case).sort()).toEqual(['long_prefix', 'short_prefix']);
  for (const row of samples) {
    expect(row).toMatchObject({ limit: 40, min_rows: 20 });
    expect(Number.isInteger(row.sample_count) && Number(row.sample_count) > 0).toBe(true);
    expect(Number.isInteger(row.allocated_bytes) && Number(row.allocated_bytes) >= 0).toBe(true);
  }
  const short = Number(samples.find((row) => row.case === 'short_prefix')!.allocated_bytes);
  const long = Number(samples.find((row) => row.case === 'long_prefix')!.allocated_bytes);
  expect(Math.abs(long - short), 'isolated real page-operation allocations vary≤10%, construction excluded').toBeLessThanOrEqual(short * 0.10);
  await info.attach('isolated-page-allocation-receipt', { path: file, contentType: 'text/plain' });
});

// B25/D-U1 is team-lead's later permissioned genuine-session hands-on UAT with
// independent validation. Synthetic success above is NOT genuine acceptance.
