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
import { seedCanonicalAgedTranscript } from './chat-history';
import { requireCanonicalHistoryContext } from './canonical-history-context';

// ── Types ───────────────────────────────────────────────────────────────────

export interface AgedTranscriptOptions {
  /**
   * Number of synthetic transcript entries to write.
   * Defaults to 4 (user message + assistant text reply + tool call + tool result).
   * Must be >= 1.
   */
  messageCount?: number;
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

/** Create a real identity, then seed its original bodies and timestamps through
 * the guarded canonical writer on historical append days. Returns the actual
 * opaque session id; the requested label still authors the stable source ids.
 * The owned gateway stays stopped until the caller has checked fixture existence
 * and explicitly calls resumeCanonicalHistory, so startup sweep cannot erase the
 * aged source before that prerequisite is observed. */
export async function agedTranscript(
  omnipusHome: string,
  sessionId: string,
  daysAgo: number,
  opts?: AgedTranscriptOptions,
): Promise<string> {
  const context = requireCanonicalHistoryContext();
  if (context.gateway.homeDir !== omnipusHome) throw new Error('Aged fixture must use its actual owned gateway home');
  const normalizedId = syntheticSessionID(sessionId);
  const agentId = 'mia';
  const messageCount = opts?.messageCount ?? 4;
  const targetDate = new Date(Date.now() - daysAgo * 24 * 60 * 60 * 1000);
  const entries = buildEntries(normalizedId, agentId, targetDate, messageCount);
  // The identity is REST-created; original source IDs/bodies/timestamps stay
  // authored above. Canonical past-day appends and real rollover replace raw
  // meta/transcript/duplicated-partition writes. Return the real opaque id.
  return seedCanonicalAgedTranscript(entries, targetDate, `Aged session (${daysAgo}d ago)`);
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
