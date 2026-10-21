import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { createHash } from 'node:crypto';
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

// D-U1 is external acceptance, not something an agent can synthesize. This
// test deliberately remains loud when that independent input is missing.
test('T18 B25:genuine long-session campaign is permissioned, exact-commit and independently validated, not synthetic-only', async ({}, info) => {
  const filename = process.env.OMNIPUS_E2E_GENUINE_HISTORY_EVIDENCE;
  if (!filename) throw new Error('BLOCKED: permissioned anonymized genuine long-session campaign/evidence not supplied — required by B25/D-U1/T18; synthetic prefix fixtures cannot satisfy this acceptance criterion');
  expect(path.isAbsolute(filename), 'evidence location must be explicit').toBe(true);
  const manifest: unknown = JSON.parse(fs.readFileSync(filename, 'utf8'));
  expect(manifest, 'private campaign receipt must describe real independently gathered evidence').toMatchObject({
    dataset_kind: 'genuine-anonymized', permission_receipt: expect.any(String), candidate_commit: expect.any(String),
    tester_account: expect.any(String), validator_account: expect.any(String), independent_validator_receipt: expect.any(String),
    actual_group_count: expect.any(Number), actual_encoded_bytes: expect.any(Number),
    open_samples_ms: expect.any(Array), boundary_drift_px: expect.any(Array), evidence: expect.any(Array),
  });
  // This is a private evidence-manifest type, never a hand-written gateway type.
  const receipt = manifest as { dataset_kind: string; permission_receipt: string; candidate_commit: string;
    tester_account: string; validator_account: string; independent_validator_receipt: string;
    actual_group_count: number; actual_encoded_bytes: number; open_samples_ms: number[]; boundary_drift_px: number[];
    evidence: { path: string; sha256: string }[] };
  const commit = execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
  expect(receipt.candidate_commit, 'a receipt from another candidate is stale, never green').toBe(commit);
  expect(receipt.tester_account).not.toBe(receipt.validator_account);
  expect(receipt.actual_group_count).toBeGreaterThan(500);
  expect(receipt.actual_encoded_bytes).toBeGreaterThan(0);
  expect(receipt.open_samples_ms, 'D-U1 requires30 actual opens').toHaveLength(30);
  expect(receipt.boundary_drift_px, 'D-U1 requires30 normal-use boundaries').toHaveLength(30);
  expect(receipt.open_samples_ms.every((n) => Number.isFinite(n) && n >= 0)).toBe(true);
  expect(receipt.boundary_drift_px.every((n) => Number.isFinite(n) && Math.abs(n) <= 1)).toBe(true);
  const sorted = [...receipt.open_samples_ms].sort((a, b) => a - b);
  expect(sorted[Math.ceil(0.95 * 30) - 1], 'adopted UI p95≤1.5seconds, not a relaxed measured threshold').toBeLessThanOrEqual(1500);
  for (const required of [receipt.permission_receipt, receipt.independent_validator_receipt]) {
    expect(path.isAbsolute(required)).toBe(true);
    expect(fs.statSync(required).size, 'actual independent/permission artifact, not a success boolean').toBeGreaterThan(0);
  }
  expect(receipt.evidence.length, 'must retain actual screenshots/network/geometry/heap/read artifacts').toBeGreaterThanOrEqual(5);
  for (const file of receipt.evidence) {
    expect(path.isAbsolute(file.path)).toBe(true);
    expect(createHash('sha256').update(fs.readFileSync(file.path)).digest('hex'), 'receipt binds the actual evidence bytes').toBe(file.sha256);
  }
  await info.attach('genuine-history-campaign-manifest', { path: filename, contentType: 'application/json' });
});
