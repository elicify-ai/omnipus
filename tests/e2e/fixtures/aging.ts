/**
 * aging.ts — E2E harness helper for simulating aged session transcripts.
 *
 * ## What it simulates
 *
 * In production, Omnipus stores session transcripts in day-partitioned JSONL
 * files under `$OMNIPUS_HOME/sessions/<sessionId>/`. The retention sweep
 * (`pkg/session/retention_sweep.go`) checks the **mtime** (file modification
 * time) of each `.jsonl` file — not the timestamps embedded in the JSON
 * content — and deletes files whose mtime is older than `retentionDays * 24h`.
 *
 * This helper creates a synthetic session directory and backdates all of its
 * files to a target timestamp using Node's `fs.utimesSync()` (which calls the
 * `utimes(2)` syscall). This makes the Go retention sweep treat the files as if
 * they had last been written N days ago.
 *
 * ## Why `utimes` rather than spoofing the system clock
 *
 * Spoofing the system clock (e.g., via `FAKETIME` or Playwright `clock.install()`)
 * is fragile across Go + Node process boundaries: the gateway process has its
 * own clock, and clock-spoofing at the Playwright layer does not affect the
 * gateway's `time.Now()`. The retention sweep compares Go's `time.Now()` against
 * the file's OS-level mtime, so the correct approach is to set the mtime
 * directly. `utimes` is precise and side-effect-free outside the test fixture.
 *
 * ## File layout assumptions
 *
 * The layout mirrors what `pkg/session/unified.go::NewSession` creates:
 *
 *   $OMNIPUS_HOME/sessions/<sessionId>/
 *     meta.json       — UnifiedMeta (JSON, created by NewSession)
 *     transcript.jsonl — Transcript entries (one JSON object per line)
 *
 * Additionally, the retention sweep targets `.jsonl` files via a filepath.Walk,
 * so we need at least one `.jsonl` file to be backdated for the sweep to find
 * and act on this session.
 *
 * Note: the `.context/` sub-directory at the store level
 * (`$OMNIPUS_HOME/sessions/.context/<sessionId>.jsonl`) is also a target of
 * the sweep. This helper does NOT create context.jsonl entries because the
 * gateway creates those at session-start time; for the backdating purpose,
 * `transcript.jsonl` is sufficient to trigger the sweep.
 */

import * as fs from 'fs';
import * as path from 'path';

// ── Types ───────────────────────────────────────────────────────────────────

export interface AgedTranscriptOptions {
  /**
   * Number of synthetic transcript entries to write.
   * Defaults to 4 (user message + assistant text reply + tool call + tool result).
   * Must be >= 1.
   */
  messageCount?: number;
}

/**
 * A minimal UnifiedMeta shape compatible with what pkg/session writes.
 * Only fields required by the SPA session-list API response are populated;
 * the rest are omitted and the gateway will tolerate their absence.
 */
interface SyntheticMeta {
  id: string;
  agent_id: string;
  agent_ids: string[];
  active_agent_id: string;
  status: 'active' | 'archived' | 'interrupted';
  created_at: string;
  updated_at: string;
  channel: string;
  type: 'chat' | 'task' | 'channel';
  title: string;
  stats: {
    tokens_in: number;
    tokens_out: number;
    tokens_total: number;
    cost: number;
    tool_calls: number;
    message_count: number;
  };
  partitions: string[];
}

interface SyntheticTranscriptEntry {
  id: string;
  type?: string;
  role: string;
  content?: string;
  summary?: string;
  timestamp: string;
  agent_id: string;
  tool_calls?: Array<{
    id: string;
    tool: string;
    status: string;
    duration_ms: number;
    parameters: Record<string, string>;
    result: Record<string, string>;
  }>;
}

// ── Helpers ─────────────────────────────────────────────────────────────────

/** Generate a minimal ULID-shaped session ID (same prefix as Go's NewSessionID). */
function syntheticSessionID(base: string): string {
  // Use the provided base (or a random hex string) to build something that
  // looks plausible to the SPA session list renderer. The Go session store
  // does not validate the ID format when reading fixture directories.
  return base.startsWith('session_') ? base : `session_${base}`;
}

/** Build synthetic transcript entries covering: user msg + assistant reply + tool call + tool result. */
function buildEntries(
  sessionId: string,
  agentId: string,
  timestamp: Date,
  messageCount: number,
): SyntheticTranscriptEntry[] {
  const entries: SyntheticTranscriptEntry[] = [];
  const ts = timestamp.toISOString();

  // Always emit at least the canonical 4-entry sequence.
  // If messageCount > 4, repeat user/assistant pairs.
  const rounds = Math.max(1, Math.ceil((messageCount - 2) / 2));

  // Entry 1: user message
  entries.push({
    id: `${sessionId}-u1`,
    role: 'user',
    content: 'Show me a simple HTML page.',
    timestamp: ts,
    agent_id: agentId,
  });

  // Entry 2: assistant text reply
  entries.push({
    id: `${sessionId}-a1`,
    role: 'assistant',
    content: "I'll create a simple HTML page for you.",
    timestamp: ts,
    agent_id: agentId,
  });

  // Interleave extra user/assistant pairs if messageCount > 4
  for (let i = 0; i < rounds - 1; i++) {
    entries.push({
      id: `${sessionId}-u${i + 2}`,
      role: 'user',
      content: `Follow-up question ${i + 1}.`,
      timestamp: ts,
      agent_id: agentId,
    });
    entries.push({
      id: `${sessionId}-a${i + 2}`,
      role: 'assistant',
      content: `Follow-up answer ${i + 1}.`,
      timestamp: ts,
      agent_id: agentId,
    });
  }

  // Entry N-1: assistant with tool call
  entries.push({
    id: `${sessionId}-atc`,
    role: 'assistant',
    content: 'Creating the file now.',
    timestamp: ts,
    agent_id: agentId,
    tool_calls: [
      {
        id: `${sessionId}-tc1`,
        tool: 'workspace.write_file',
        status: 'success',
        duration_ms: 42,
        parameters: { path: 'index.html', content: '<h1>Hello</h1>' },
        result: { written: 'true' },
      },
    ],
  });

  // Entry N: tool result (system entry)
  entries.push({
    id: `${sessionId}-tr1`,
    type: 'system',
    role: 'system',
    content: 'Tool completed successfully.',
    timestamp: ts,
    agent_id: agentId,
  });

  return entries;
}

// ── Public API ───────────────────────────────────────────────────────────────

/**
 * agedTranscript — Write a synthetic session directory backdated to `daysAgo` days.
 *
 * The function:
 *   1. Computes target timestamp = now - (daysAgo * 24h).
 *   2. Creates (or overwrites) the session directory under `$omnipusHome/sessions/<sessionId>/`.
 *   3. Writes `meta.json` with the session metadata at the target timestamp.
 *   4. Writes `transcript.jsonl` with synthetic entries (user+assistant+toolcall+toolresult).
 *   5. Back-dates ALL files in the session directory via `fs.utimesSync()`.
 *
 * The caller must ensure the directory `$omnipusHome/sessions/` is writable.
 * This function is synchronous and returns only after fsync (via Node's
 * default synchronous write behavior — JS file APIs flush on close).
 *
 * @param omnipusHome - absolute path to the Omnipus home directory (e.g. `/tmp/omnipus-e2e-test`)
 * @param sessionId   - session ID string. Will be prefixed with `session_` if not already.
 * @param daysAgo     - how many days in the past the transcript should appear to be
 * @param opts        - optional overrides (messageCount)
 */
export function agedTranscript(
  omnipusHome: string,
  sessionId: string,
  daysAgo: number,
  opts?: AgedTranscriptOptions,
): void {
  const normalizedId = syntheticSessionID(sessionId);
  // 'mia' — the seeded default of the 4-base roster. The historical 'main'
  // id no longer resolves to any agent, which flips the session to
  // agent_removed/read-only instead of a normal replay-able session.
  const agentId = 'mia';
  const messageCount = opts?.messageCount ?? 4;

  const targetDate = new Date(Date.now() - daysAgo * 24 * 60 * 60 * 1000);
  const targetISO = targetDate.toISOString();

  const sessionsBase = path.join(omnipusHome, 'sessions');
  const sessionDir = path.join(sessionsBase, normalizedId);

  // Create directories.
  fs.mkdirSync(sessionDir, { recursive: true });

  // Write meta.json
  const meta: SyntheticMeta = {
    id: normalizedId,
    agent_id: agentId,
    agent_ids: [agentId],
    active_agent_id: agentId,
    status: 'active',
    created_at: targetISO,
    updated_at: targetISO,
    channel: 'web',
    type: 'chat',
    title: `Aged session (${daysAgo}d ago)`,
    stats: {
      tokens_in: 50,
      tokens_out: 120,
      tokens_total: 170,
      cost: 0.0001,
      tool_calls: 1,
      message_count: messageCount,
    },
    partitions: [`${targetDate.toISOString().slice(0, 10)}.jsonl`],
  };
  const metaPath = path.join(sessionDir, 'meta.json');
  fs.writeFileSync(metaPath, JSON.stringify(meta, null, 2), { encoding: 'utf-8' });

  // Write transcript.jsonl
  const entries = buildEntries(normalizedId, agentId, targetDate, messageCount);
  const transcriptPath = path.join(sessionDir, 'transcript.jsonl');
  const lines = entries.map((e) => JSON.stringify(e)).join('\n') + '\n';
  fs.writeFileSync(transcriptPath, lines, { encoding: 'utf-8' });

  // Write the day-partition .jsonl file (matches the filename listed in meta.partitions).
  // The retention sweep uses WalkDir and deletes .jsonl files by mtime, so this
  // file must exist and be backdated.
  const partitionName = `${targetDate.toISOString().slice(0, 10)}.jsonl`;
  const partitionPath = path.join(sessionDir, partitionName);
  // Partition contains the same entries as transcript.jsonl for simplicity.
  fs.writeFileSync(partitionPath, lines, { encoding: 'utf-8' });

  // Backdate ALL files in the session directory to the target timestamp.
  // `utimes(2)` sets both atime and mtime. The retention sweep reads mtime.
  const filesInDir = fs.readdirSync(sessionDir);
  for (const fname of filesInDir) {
    const fpath = path.join(sessionDir, fname);
    const stat = fs.statSync(fpath);
    if (!stat.isDirectory()) {
      fs.utimesSync(fpath, targetDate, targetDate);
    }
  }

  // Also backdate the session directory itself (some OS retention tooling
  // checks directory mtime, and it keeps the fixture self-consistent).
  fs.utimesSync(sessionDir, targetDate, targetDate);
}

/**
 * agedSessionExists — Convenience check: does the session directory exist?
 *
 * Returns true if `$omnipusHome/sessions/<sessionId>/meta.json` is present,
 * indicating the session was not swept (deleted) by the retention sweep.
 *
 * @param omnipusHome - absolute path to the Omnipus home directory
 * @param sessionId   - session ID (prefixed with `session_` if needed)
 */
export function agedSessionExists(omnipusHome: string, sessionId: string): boolean {
  const normalizedId = syntheticSessionID(sessionId);
  const metaPath = path.join(omnipusHome, 'sessions', normalizedId, 'meta.json');
  return fs.existsSync(metaPath);
}
