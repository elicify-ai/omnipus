import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { walkFiles } from './safety.mjs';

export function digest(file) {
  const hash = crypto.createHash('sha256');
  const fd = fs.openSync(file, 'r');
  const buffer = Buffer.alloc(1024 * 1024);
  try { for (;;) { const n = fs.readSync(fd, buffer, 0, buffer.length, null); if (!n) break; hash.update(buffer.subarray(0, n)); } }
  finally { fs.closeSync(fd); }
  return hash.digest('hex');
}
export function inventory(directory) {
  return walkFiles(directory).map(file => ({ relative_path: path.relative(directory, file), bytes: fs.statSync(file).size, sha256: digest(file) }));
}
export function indexPreflight(directory) {
  const format = fs.readFileSync(path.join(directory, '.chat-history', 'FORMAT'), 'utf8');
  const checkpoint = JSON.parse(fs.readFileSync(path.join(directory, '.chat-history', 'checkpoint.json'), 'utf8'));
  if (format !== 'omnipus-chat-history-index/1\n' || checkpoint.format_version !== 1 || !checkpoint.generation) throw new Error('existing versioned index required; evaluator never repairs or constructs a missing index on open');
  return { format: format.trim(), checkpoint_sha256: digest(path.join(directory, '.chat-history', 'checkpoint.json')), index_generation: checkpoint.generation };
}
export function archiveShape(directory) {
  const files = walkFiles(directory).filter(f => /(?:^|\/)(?:transcript|\d{4}-\d{2}-\d{2})\.jsonl$/.test(f));
  return { encoded_archive_bytes: files.reduce((sum, f) => sum + fs.statSync(f).size, 0), archive_files: files.length,
    actual_days: files.filter(f => /\d{4}-\d{2}-\d{2}\.jsonl$/.test(f)).length + (fs.existsSync(path.join(directory, 'transcript.day')) ? 1 : 0) };
}
export function syntheticDays(count, agent, reference = new Date()) {
  const days = Array.from({ length: 90 }, (_, day) => {
    const at = new Date(Date.UTC(reference.getUTCFullYear(), reference.getUTCMonth(), reference.getUTCDate()));
    at.setUTCDate(at.getUTCDate() - 89 + day);
    return { day: at.toISOString().slice(0, 10), entries: [] };
  });
  const counts = { text: 0, tools: 0, images: 0, clear: 0, system: 0 };
  for (let i = 0; i < count; i++) {
    const key = `b25-${String(i).padStart(6, '0')}`;
    const day = days[Math.floor(i * 90 / count)];
    const entry = { id: key, type: 'message', role: 'assistant', agent_id: agent,
      view_membership: 'chat', timestamp: `${day.day}T12:00:00Z`, content: `${key} ${'Deterministic B25 synthetic content. '.repeat(34)}` };
    let kind = 'text';
    if (i === Math.floor(count / 10)) {
      entry.type = 'system'; entry.role = 'system'; entry.content = 'Conversation context cleared'; kind = 'clear';
    } else if (i % 17 === 0) {
      entry.type = 'system'; entry.role = 'system'; kind = 'system';
    } else if (i % 11 === 0) {
      entry.content += '\n\n![Synthetic one pixel](data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jX1sAAAAASUVORK5CYII=)'; kind = 'images';
    } else if (i % 7 === 0) {
      entry.content = '';
      entry.tool_calls = [{ id: `call-${key}`, tool: 'read_file', status: 'success',
        parameters: { path: `${key}.txt` }, result: { content: key + 'x'.repeat(1200) } }]; kind = 'tools';
    }
    counts[kind]++;
    day.entries.push(entry);
  }
  return { days, counts, groups: count };
}
export function seedSynthetic({ fixtureBinary, home, sid, agent, groups, output, environment }) {
  const planData = syntheticDays(groups, agent);
  const plan = path.join(output, 'synthetic-plan.json'); // Outside canonical source.
  fs.writeFileSync(plan, JSON.stringify({ schema_version: 1, data_root: fs.realpathSync(home),
    action: 'seed_days', session_id: sid, days: planData.days }), { mode: 0o600 });
  const started = performance.now();
  const run = spawnSync(fixtureBinary, ['-test.run', '^TestChatHistoryFixtureDriver$', '-test.v', '-test.count=1'],
    { encoding: 'utf8', maxBuffer: 16 * 1024 * 1024, env: { ...environment, OMNIPUS_CHAT_HISTORY_FIXTURE_PLAN: plan } });
  fs.writeFileSync(path.join(output, 'canonical-writer.log'), `${run.stdout ?? ''}${run.stderr ?? ''}\nexit=${run.status} signal=${run.signal}\n`, { mode: 0o600 });
  if (run.error) throw run.error;
  if (run.status !== 0 || !/--- PASS: TestChatHistoryFixtureDriver\b/.test(run.stdout) || /--- SKIP: TestChatHistoryFixtureDriver\b/.test(run.stdout)) throw new Error(`canonical fixture driver did not PASS; exit=${run.status}; see canonical-writer.log`);
  const lines = run.stdout.split('\n').filter(line => line.startsWith('CHAT_HISTORY_FIXTURE_RESULT='));
  if (lines.length !== 1) throw new Error('canonical fixture receipt missing/duplicated');
  const receipt = JSON.parse(lines[0].slice('CHAT_HISTORY_FIXTURE_RESULT='.length));
  if (receipt.written_records !== groups || receipt.rolled_partitions !== 89) throw new Error('canonical fixture did not write all groups across 90 actual append days');
  const directory = path.join(home, 'sessions', sid);
  return { kind: 'synthetic', groups, mixed_counts: planData.counts, canonical_writer: 'TestChatHistoryFixtureDriver -> buildChatHistoryDayFixture -> AppendTranscriptAddressed',
    generation_ms: performance.now() - started, receipt, ...archiveShape(directory), index: indexPreflight(directory), session_directory: directory };
}

export function installCopy(source, home) {
  // Only an EXPLICIT, already-permissioned isolated copy reaches this function.
  // Preserve original identity/content; do not synthesize an index or rewrite meta.
  const before = inventory(source);
  const meta = JSON.parse(fs.readFileSync(path.join(source, 'meta.json'), 'utf8'));
  const sid = meta.id;
  if (!sid || sid === '.' || sid === '..' || /[/\\\x00]/.test(sid)) throw new Error('copy meta.json needs a safe original session id');
  const target = path.join(home, 'sessions', sid);
  if (fs.existsSync(target)) throw new Error('copied session identity collides with isolated session');
  indexPreflight(source);
  fs.cpSync(source, target, { recursive: true, errorOnExist: true, force: false, dereference: false });
  if (JSON.stringify(before) !== JSON.stringify(inventory(target))) throw new Error('copied session differs from supplied read-only input');
  return { sid, meta, source, before, directory: target, ...archiveShape(target), index: indexPreflight(target) };
}
export function verifyCopyUnchanged(copy) {
  if (JSON.stringify(copy.before) !== JSON.stringify(inventory(copy.source))) throw new Error('permissioned input copy changed during evaluation');
  return { unchanged: true, file_count: copy.before.length, manifest: copy.before };
}
