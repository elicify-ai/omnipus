import fs from 'node:fs';
import os from 'node:os';
import { execFileSync } from 'node:child_process';
import { expect } from '@playwright/test';
import { test, fixtureId } from './fixtures/chat-history-paging/harness';

test.setTimeout(600_000); // Seeding100,000 real fsynced writes belongs on Fly only.

function historyDiagnostics(file: string) {
  const text = fs.readFileSync(file, 'utf8');
  return text.slice(0, text.lastIndexOf('\n') + 1).split('\n').filter(Boolean)
    .map((line) => JSON.parse(line) as Record<string, unknown>)
    .filter((row) => row.message === 'history_page' && row.outcome === 'success');
}

test('T18 B21 B22:equal suffix after1000 and100000 prefixes has the same bounded read work and stable restart positions', async ({ history, page }, info) => {
  const other = history.ids.otherSession.id;
  for (const [id, prefix] of [[history.sessionId, 1000], [other, 100000]] as const) {
    await history.seed({ label: 'unrelated-prefix', count: prefix, bodyBytes: 1100 }, id);
    await history.seed({ label: 'equal-suffix', count: 200, bodyBytes: 96 }, id);
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
  expect(diagnostics, 'positive instrument: both real healthy-index reads emit history_page counts').toHaveLength(2);
  // D1/observability counts are an implementation handoff seam, not stopwatch
  // proxies. The keys here are explicit assumptions for GREEN to match/review.
  for (const metric of ['selected_archive_records', 'selected_archive_bytes', 'allocated_bytes'] as const) {
    const first = diagnostics[0][metric];
    const second = diagnostics[1][metric];
    expect(typeof first, `required content-free ${metric} diagnostic`).toBe('number');
    expect(typeof second, `required content-free ${metric} diagnostic`).toBe('number');
    expect(first, 'positive sample; absent counters must not make prefix independence vacuous').toBeGreaterThan(0);
    expect(Math.abs(Number(second) - Number(first)) / Number(first), `D-A1 selected ${metric} differs by at most10%`).toBeLessThanOrEqual(0.10);
  }
  expect(Number(diagnostics[1].selected_archive_records), 'selected groups+lookahead only; no100000-prefix decode').toBeLessThanOrEqual(41);
  const positions = large.entries.map((e) => e.history_position);
  await history.gateway.kill9();
  await history.gateway.restart();
  await history.authenticate();
  const restarted = await history.read({ limit: 40, min_rows: 20 }, other);
  expect(restarted.entries.map((e) => e.history_position), 'unchanged final canonical bytes preserve47-character occurrence tokens across restart').toEqual(positions);
  await history.open(other);
  await history.record('prefix-independent-restart', { diagnostics, prefixCounts: [1000, 100000], suffixCount: 200 });
  await info.attach('reference-host', { contentType: 'application/json', body: JSON.stringify({
    os: os.platform(), architecture: os.arch(), release: os.release(), cpu: os.cpus()[0]?.model,
    commit: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
  }) });
  expect(history.traffic.failures).toEqual([]);
  await expect(page.getByTestId('virtualized-message-list')).toBeVisible();
});

// B25/D-U1: squad-lead/founder clarification2026-10-21 assigns genuine
// permissioned long-session acceptance to team-lead's later hands-on UAT and
// independent validator. It is NOT an automated fixture gate and synthetic
// success above must never be reported as genuine-session acceptance.
// Required handoff: exact candidate, actual dataset shape,30 opens/30 normal
// boundaries, network/read/heap/geometry/focus artifacts and separate accounts.
