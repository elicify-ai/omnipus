// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Omnipus contributors

import assert from 'node:assert/strict';
import test from 'node:test';

import { Turn } from './uat-knowledge-harness.mjs';

const RUN01_ERROR_MESSAGE =
  'This turn didn’t finish, and we can’t tell why. Retry — if it keeps happening, ' +
  'open Verbose chat for details, or try a different model.';
const RUN01_SESSION_ID = 'session_01M2RPHBSDXAMKG1MYZ3FQKB8X';

// Minimal self-contained fixture from sc003-glm-20260918-01. The original
// evidence lives under git-ignored .local, so the regression test pins only
// the five wire frames needed to reproduce the false-success path.
const RUN01_FRAMES = [
  { type: 'session_state', user_id: 'cli', pending_approvals: [] },
  { type: 'session_started', agent_id: 'jim', session_id: RUN01_SESSION_ID },
  {
    type: 'error',
    session_id: RUN01_SESSION_ID,
    message: RUN01_ERROR_MESSAGE,
    payload: {
      llm_error: {
        code: 'unknown',
        detail: RUN01_ERROR_MESSAGE,
        message: RUN01_ERROR_MESSAGE,
        retryable: false,
      },
    },
  },
  { type: 'token', session_id: RUN01_SESSION_ID, content: RUN01_ERROR_MESSAGE },
  { type: 'done', session_id: RUN01_SESSION_ID },
];

let nextFrames;

class ReplayWebSocket {
  constructor() {
    this.frames = nextFrames;
    queueMicrotask(() => this.onopen?.());
  }

  send(raw) {
    if (JSON.parse(raw).type !== 'message') return;
    queueMicrotask(() => {
      for (const frame of this.frames) {
        this.onmessage?.({ data: JSON.stringify(frame) });
      }
    });
  }

  close() {}
}

async function askWithFrames(frames) {
  nextFrames = structuredClone(frames);
  const turn = new Turn({
    base: 'http://offline.test',
    token: 'test-token',
    agentID: 'jim',
    workspaceID: 'test-workspace',
  });
  return turn.ask('offline regression test', { timeoutMs: 1_000 });
}

test('Turn.ask wires WebSocket failure signals into its result', async (t) => {
  const originalWebSocket = globalThis.WebSocket;
  globalThis.WebSocket = ReplayWebSocket;
  t.after(() => { globalThis.WebSocket = originalWebSocket; });

  await t.test('records the run01 post-auth error and rejects its stats-free done as failed', async () => {
    const result = await askWithFrames(RUN01_FRAMES);

    assert.equal(result.turnFailed, true);
    assert.equal(result.failureReason, `error frame (unknown): ${RUN01_ERROR_MESSAGE}`);
    assert.deepEqual(result.errors, [{
      message: RUN01_ERROR_MESSAGE,
      code: 'unknown',
      retryable: false,
    }]);
    assert.equal(result.stats, null);
  });

  await t.test('accepts a healthy stats-free done frame', async () => {
    const result = await askWithFrames([
      { type: 'session_started', session_id: 'healthy-session' },
      { type: 'token', content: 'healthy response' },
      { type: 'done', session_id: 'healthy-session' },
    ]);

    assert.equal(result.turnFailed, false);
    assert.equal(result.failureReason, null);
    assert.deepEqual(result.errors, []);
    assert.equal(result.stats, null);
  });

  await t.test('preserves done.stats.turn_failed as an independent failure signal', async () => {
    const result = await askWithFrames([
      { type: 'session_started', session_id: 'failed-session' },
      { type: 'done', session_id: 'failed-session', stats: { turn_failed: true } },
    ]);

    assert.equal(result.turnFailed, true);
    assert.equal(result.failureReason, 'done.stats.turn_failed=true');
    assert.deepEqual(result.errors, []);
  });
});
