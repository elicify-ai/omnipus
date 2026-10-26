import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { expect } from '@playwright/test';
import type { Message } from '../../../src/lib/api/generated/openapi-types';
import { Session, SessionDetail } from '../../../src/lib/api/generated/schemas';
import { GatewayProcess } from './gateway-process';
import { requireCanonicalHistoryContext } from './canonical-history-context';

// Private test-driver plans/receipts; never sent across the gateway/SPA boundary.
export type ChatHistoryFixtureDay = { day: string; entries: readonly Message[]; mtime?: string };
export type ChatHistoryFixtureReceipt = {
  schema_version: 1; action: 'seed_days' | 'append_entries' | 'seed_aged' | 'sweep'; session_id?: string;
  written_records: number; rolled_partitions: number; removed_partitions: number;
};
const FORMAT = 'omnipus-chat-history-index/1\n'; // Architect decision F, v1 bytes.

export function sessionHistoryIndexPaths(dataRoot: string, sessionId: string) {
  if (!path.isAbsolute(dataRoot) || !sessionId || sessionId === '.' || sessionId === '..' || /[/\\\0]/.test(sessionId)) {
    throw new Error('sessionHistoryIndexPaths: absolute owned root and one safe session-id component required');
  }
  const directory = path.join(dataRoot, 'sessions', sessionId, '.chat-history');
  return { directory, format: path.join(directory, 'FORMAT'), checkpoint: path.join(directory, 'checkpoint.json') };
}

function ownedSession(gateway: GatewayProcess, sessionId: string) {
  gateway.assertStoppedForFixture();
  const root = fs.realpathSync(gateway.homeDir);
  const session = path.join(root, 'sessions', sessionId);
  sessionHistoryIndexPaths(root, sessionId); // Component validation before any I/O.
  expect(fs.lstatSync(session).isSymbolicLink(), 'fixture must not follow a symlinked session').toBe(false);
  const real = fs.realpathSync(session);
  if (!real.startsWith(root + path.sep) || real !== session) throw new Error('history fixture session escapes its owned home');
  return real;
}

function assertNoSymlinkTree(directory: string) {
  expect(fs.lstatSync(directory).isSymbolicLink(), 'derived subtree must not redirect fixture writes').toBe(false);
  for (const item of fs.readdirSync(directory, { withFileTypes: true })) {
    const child = path.join(directory, item.name);
    if (item.isSymbolicLink()) throw new Error(`Refusing a symlink in derived fixture subtree: ${child}`);
    if (item.isDirectory()) assertNoSymlinkTree(child);
  }
}
function healthyIndex(gateway: GatewayProcess, sessionId: string) {
  const session = ownedSession(gateway, sessionId);
  const paths = sessionHistoryIndexPaths(gateway.homeDir, sessionId);
  expect(fs.existsSync(paths.directory), 'BLOCKED: real .chat-history baseline not implemented — required by D1 and agreed artifact seam F').toBe(true);
  assertNoSymlinkTree(paths.directory);
  if (fs.realpathSync(paths.directory) !== path.join(session, '.chat-history')) throw new Error('history index escapes the addressed session');
  expect(fs.readFileSync(paths.format, 'utf8'), 'positive control: correct v1 index, not an unrelated cache').toBe(FORMAT);
  const checkpoint: unknown = JSON.parse(fs.readFileSync(paths.checkpoint, 'utf8'));
  expect(checkpoint, 'positive control: a real versioned checkpoint exists before mutation').toMatchObject({ format_version: 1 });
  return paths;
}

export async function removeSessionHistoryIndex(gateway: GatewayProcess, sessionId: string): Promise<void> {
  const paths = healthyIndex(gateway, sessionId);
  fs.rmSync(paths.directory, { recursive: true, force: false });
  expect(fs.existsSync(paths.directory), 'the actual entire derived baseline was removed').toBe(false);
}
export async function corruptSessionHistoryIndex(gateway: GatewayProcess, sessionId: string): Promise<void> {
  const paths = healthyIndex(gateway, sessionId);
  const invalid = 'not-json:chat-history-fixture-corruption\n';
  fs.writeFileSync(paths.checkpoint, invalid, { mode: 0o600 });
  expect(fs.readFileSync(paths.checkpoint, 'utf8')).toBe(invalid);
  expect(fs.readFileSync(paths.format, 'utf8')).toBe(FORMAT);
}

function runDriver(gateway: GatewayProcess, plan: Record<string, unknown>): ChatHistoryFixtureReceipt {
  gateway.assertStoppedForFixture();
  const binary = process.env.OMNIPUS_CHAT_HISTORY_FIXTURE_BINARY;
  if (!binary || !path.isAbsolute(binary) || !fs.existsSync(binary)) {
    throw new Error('BLOCKED: prebuilt TestChatHistoryFixtureDriver executable not supplied — required by agreed seam G (OMNIPUS_CHAT_HISTORY_FIXTURE_BINARY); do not compile Go inside a page/assertion');
  }
  const workspace = fs.mkdtempSync(path.join(os.tmpdir(), 'chat-history-fixture-plan-'));
  const file = path.join(workspace, 'plan.json');
  const canonical = fs.realpathSync(gateway.homeDir);
  if (workspace === canonical || workspace.startsWith(canonical + path.sep)) throw new Error('fixture plan must be outside canonical data');
  fs.writeFileSync(file, JSON.stringify({ schema_version: 1, data_root: canonical, ...plan }), { mode: 0o600 });
  const command = ['-test.run', '^TestChatHistoryFixtureDriver$', '-test.v', '-test.count=1'];
  const result = spawnSync(binary, command, { encoding: 'utf8', maxBuffer: 16 * 1024 * 1024,
    env: { ...process.env, OMNIPUS_CHAT_HISTORY_FIXTURE_PLAN: file } });
  const output = result.stdout + result.stderr;
  fs.writeFileSync(path.join(workspace, 'driver.log'), output + `\nexit=${String(result.status)} signal=${String(result.signal)}\n`, { mode: 0o600 });
  if (result.error) throw new Error(`history fixture child failed to start; evidence ${workspace}: ${result.error.message}`);
  expect(result.status, `real fixture driver failed; evidence ${workspace}\n${output}`).toBe(0);
  expect(output, 'named driver must actually PASS, not zero-match/skip or a wrapper0').toMatch(/--- PASS: TestChatHistoryFixtureDriver\b/);
  expect(output).not.toMatch(/--- SKIP: TestChatHistoryFixtureDriver\b/);
  const receipts = output.split('\n').filter((line) => line.startsWith('CHAT_HISTORY_FIXTURE_RESULT='));
  expect(receipts, 'exactly one matching v1 operation receipt').toHaveLength(1);
  const value: unknown = JSON.parse(receipts[0].slice('CHAT_HISTORY_FIXTURE_RESULT='.length));
  expect(value).toMatchObject({ schema_version: 1, action: plan.action });
  if (plan.session_id !== undefined) expect(value).toMatchObject({ session_id: plan.session_id });
  const receipt = value as ChatHistoryFixtureReceipt;
  for (const count of [receipt.written_records, receipt.rolled_partitions, receipt.removed_partitions]) {
    expect(Number.isInteger(count) && count >= 0, 'fixture receipt counters are measured nonnegative integers').toBe(true);
  }
  return receipt;
}

export async function seedChatHistoryDayPartitions(gateway: GatewayProcess, sessionId: string,
  days: readonly ChatHistoryFixtureDay[]): Promise<ChatHistoryFixtureReceipt> {
  const session = ownedSession(gateway, sessionId);
  const receipt = runDriver(gateway, { action: 'seed_days', session_id: sessionId, days });
  expect(receipt.written_records, 'all authored source records went through the actual writer').toBe(days.reduce((n, day) => n + day.entries.length, 0));
  expect(receipt.rolled_partitions).toBe(Math.max(0, days.length - 1));
  for (const day of days.slice(0, -1)) expect(fs.existsSync(path.join(session, `${day.day}.jsonl`)), 'real rolled file, not backdated entry timestamps').toBe(true);
  if (days.length) expect(fs.readFileSync(path.join(session, 'transcript.day'), 'utf8').trim()).toBe(days.at(-1)!.day);
  healthyIndex(gateway, sessionId);
  return receipt;
}
export async function sweepChatHistoryFixture(gateway: GatewayProcess, retentionDays: number): Promise<ChatHistoryFixtureReceipt> {
  gateway.assertStoppedForFixture();
  return runDriver(gateway, { action: 'sweep', retention_days: retentionDays });
}

/** Author existing legacy payloads through the real writer in the actual
 * stopped private gateway. No archive overwrite or automatic index adoption. */
export async function seedCanonicalLegacyTranscript(sessionId: string, entries: readonly object[], append = false) {
  const context = requireCanonicalHistoryContext();
  if (!context.page) throw new Error('BLOCKED: canonical seed has no owned browser page');
  await context.page.goto('about:blank');
  await context.gateway.kill9();
  context.gateway.assertStoppedForFixture();
  healthyIndex(context.gateway, sessionId);
  const receipt = runDriver(context.gateway, {
    action: 'append_entries', session_id: sessionId, require_empty: !append,
    days: [{ day: new Date().toISOString().slice(0, 10), entries }],
  });
  expect(receipt.written_records, 'every authored legacy record went through the canonical writer').toBe(entries.length);
  healthyIndex(context.gateway, sessionId);
  await resumeCanonicalHistory();
  const result = await context.gateway.apiFetch('GET', `/api/v1/sessions/${sessionId}`);
  expect(result.status, `CANONICAL SEED: first real detail error: ${result.raw}`).toBe(200);
  const detail = SessionDetail.parse(result.body);
  expect(detail.history_state.status, `POSSIBLE PRODUCT DEFECT: canonical seed still has unavailable history: ${result.raw}`).toBe('available');
}

/** Restart only the owned instance and refresh its genuine single-slot cookie. */
export async function resumeCanonicalHistory() {
  const context = requireCanonicalHistoryContext();
  if (!context.page) throw new Error('BLOCKED: canonical restart has no owned browser page');
  await context.gateway.restart();
  const state = await context.gateway.browserStorageState();
  await context.page.context().clearCookies();
  await context.page.context().addCookies(state.cookies);
  fs.writeFileSync(context.authFile, JSON.stringify(state), { mode: 0o600 });
}

/** Seed all original aged entries on real historical append days, then leave
 * the owner stopped so callers can inspect the fixture before startup sweep. */
export async function seedCanonicalAgedTranscript(entries: readonly object[], at: Date, title: string): Promise<string> {
  const context = requireCanonicalHistoryContext();
  if (!context.page) throw new Error('BLOCKED: aged seed has no owned browser page');
  const result = await context.gateway.apiFetch('POST', '/api/v1/sessions', { agent_id: 'mia', type: 'chat' });
  expect(result.status, `canonical aged identity creation failed: ${result.raw}`).toBe(201);
  const session = Session.parse(result.body);
  await context.page.goto('about:blank');
  await context.gateway.kill9();
  context.gateway.assertStoppedForFixture();
  healthyIndex(context.gateway, session.id);
  const prior = new Date(at.getTime());
  prior.setUTCDate(prior.getUTCDate() - 1);
  const split = Math.max(0, entries.length - 1);
  const days = split > 0 ? [
    { day: prior.toISOString().slice(0, 10), entries: entries.slice(0, split), mtime: at.toISOString() },
    { day: at.toISOString().slice(0, 10), entries: entries.slice(split), mtime: at.toISOString() },
  ] : [{ day: at.toISOString().slice(0, 10), entries, mtime: at.toISOString() }];
  const receipt = runDriver(context.gateway, {
    action: 'seed_aged', session_id: session.id, agent_id: 'mia', title, aged_at: at.toISOString(), days,
  });
  expect(receipt.written_records, 'aged fixture keeps the exact original record count').toBe(entries.length);
  expect(receipt.rolled_partitions, 'aged source uses actual historical rollover').toBe(Math.max(0, days.length - 1));
  healthyIndex(context.gateway, session.id);
  return session.id;
}

/** Fixture-only aging AFTER real rollover. It changes mtime, never content or
 * index metadata, then the separate driver calls the ACTUAL retention owner. */
export function ageRolledHistoryPartitions(gateway: GatewayProcess, sessionId: string, days: readonly string[], mtime: Date) {
  const session = ownedSession(gateway, sessionId);
  if (!Number.isFinite(mtime.getTime())) throw new Error('invalid fixture mtime');
  for (const day of days) {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(day)) throw new Error('invalid rolled day key');
    const file = path.join(session, `${day}.jsonl`);
    expect(fs.existsSync(file), 'positive age/sweep control: real rolled partition exists').toBe(true);
    if (fs.lstatSync(file).isSymbolicLink()) throw new Error('rolled fixture file is a symlink');
    fs.utimesSync(file, mtime, mtime);
    expect(fs.statSync(file).mtime.getTime()).toBe(mtime.getTime());
  }
}
