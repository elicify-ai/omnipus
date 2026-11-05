// @vitest-environment node
import { EventEmitter } from 'node:events';
import type { Page, WebSocket } from '@playwright/test';
import { describe, expect, it } from 'vitest';
import type { WsFrame } from '../../../src/lib/api/generated/ws-schemas';
import { observeReplayAttach } from './replay-attach-observer';

// F2: controlled native-event inputs test the observer instrument, not the product.
const SESSION = 'observer-target';
const TURN = 'observer-turn';
const attach: WsFrame = { type: 'attach_session', session_id: SESSION };
const token = (content: string): WsFrame => ({ type: 'token', session_id: SESSION, turn_id: TURN, content });
function deliver(socket: EventEmitter, frame: WsFrame, event = 'framereceived') {
  socket.emit(event, { payload: JSON.stringify(frame) });
}
function harness() {
  const page = new EventEmitter();
  const observer = observeReplayAttach(page as unknown as Page, SESSION);
  return {
    observer,
    open() {
      const socket = Object.assign(new EventEmitter(), { url: () => 'ws://127.0.0.1/api/v1/chat/ws' });
      page.emit('websocket', socket as unknown as WebSocket);
      return socket;
    },
  };
}

describe('replay attach observer connection integrity', () => {
  it('retains a duplicated chunk on one socket instead of deduplicating the evidence', () => {
    const { observer, open } = harness();
    try {
      const socket = open();
      observer.startExperiment();
      for (const text of ['prefix ', 'chunk ', 'chunk ']) deliver(socket, token(text));
      expect(observer.liveTokens().map(frame => frame.content)).toEqual(['prefix ', 'chunk ', 'chunk ']);
      expect(observer.liveTokens().map(frame => frame.content).join('')).not.toBe('prefix chunk ');
      expect(observer.failures).toEqual([]);
    } finally { observer.dispose(); }
  });

  it('leaves a missing chunk missing on one socket instead of repairing the evidence', () => {
    const { observer, open } = harness();
    try {
      const socket = open();
      observer.startExperiment();
      for (const text of ['prefix ', 'last']) deliver(socket, token(text));
      expect(observer.liveTokens().map(frame => frame.content)).toEqual(['prefix ', 'last']);
      expect(observer.liveTokens().map(frame => frame.content).join('')).not.toBe('prefix missing last');
      expect(observer.failures).toEqual([]);
    } finally { observer.dispose(); }
  });

  it('rejects a reconnect explicitly and preserves the original evidence without merging generations', () => {
    const { observer, open } = harness();
    try {
      const first = open();
      observer.startExperiment();
      deliver(first, attach, 'framesent');
      deliver(first, token('prefix '));
      const second = open();
      deliver(second, attach, 'framesent');
      deliver(second, token('replacement must not repair or duplicate the experiment'));
      deliver(first, token('late superseded data'));
      const diagnostic = 'reconnect during experiment: session observer-target, socket generation 1 -> 2';
      expect(() => observer.assertNoReconnect()).toThrowError(new Error(diagnostic));
      expect(() => observer.liveTokens()).toThrowError(new Error(diagnostic));
      expect(() => observer.liveDone(TURN)).toThrowError(new Error(diagnostic));
      expect(observer.snapshot()).toEqual({
        tokens: [token('prefix ')], attaches: [{ frame: attach, receivedBefore: 0 }],
        failures: [diagnostic], socketGeneration: 2, experimentGeneration: 1,
      });
    } finally { observer.dispose(); }
  });

  it('uses only the current setup socket and ignores stale frames and attaches before the experiment', () => {
    const { observer, open } = harness();
    try {
      const first = open();
      deliver(first, attach, 'framesent');
      deliver(first, token('old setup'));
      const second = open();
      deliver(first, attach, 'framesent');
      deliver(first, token('stale setup'));
      deliver(second, attach, 'framesent');
      observer.startExperiment();
      deliver(second, token('current'));
      expect(observer.snapshot()).toEqual({
        tokens: [token('current')], attaches: [{ frame: attach, receivedBefore: 0 }],
        failures: [], socketGeneration: 2, experimentGeneration: 2,
      });
      expect(observer.liveTokens().map(frame => frame.content)).toEqual(['current']);
    } finally { observer.dispose(); }
  });
});
