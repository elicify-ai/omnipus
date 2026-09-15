import { describe, expect, it } from 'vitest';
import { completedDelegation, type DelegationFrame } from './delegation-completion';

// Expectations come from SubagentStartFrame, SubagentEndFrame, DoneFrame and
// the visibility test's requirement for one successfully completed delegation.
const label = 'visibility-test';
const start = { type: 'subagent_start', session_id: 'parent', span_id: 'child-span', parent_call_id: 'call', task_label: label };
const end = { type: 'subagent_end', session_id: 'parent', span_id: 'child-span', status: 'success' };
const done = { type: 'done', session_id: 'parent' };

describe('completedDelegation', () => {
  it('accepts the matching successful child with parent completion and two prose messages', () => {
    // CI's two completed bubbles must not redefine the protocol completion rule.
    const frames: DelegationFrame[] = [start, done, { type: 'message', content: 'Delegated' }, end, { type: 'message', content: '.' }];
    expect(completedDelegation(frames, label)).toBe('child-span');
  });
  it('also accepts awaited child completion before the parent done', () => {
    expect(completedDelegation([start, end, done], label)).toBe('child-span');
  });
  it.each([
    ['no events', []],
    ['missing child end', [start, done]],
    ['missing parent done', [start, end]],
    ['old parent done before this child started', [done, start, end]],
    ['another task label', [{ ...start, task_label: 'unrelated' }, end, done]],
    ['another child span', [start, { ...end, span_id: 'other' }, done]],
    ['another child session', [start, { ...end, session_id: 'other' }, done]],
    ['another parent completion', [start, end, { ...done, session_id: 'other' }]],
  ])('does not complete for %s', (_name, frames) => {
    expect(completedDelegation(frames as DelegationFrame[], label)).toBeNull();
  });
  it.each(['error', 'cancelled', 'interrupted', 'timeout', 'parked'])('rejects terminal child status %s', status => {
    expect(() => completedDelegation([start, { ...end, status }, done], label)).toThrow(`Delegation ended with status ${status}`);
  });
  it('rejects duplicate matching delegation starts', () => {
    expect(() => completedDelegation([start, { ...start, span_id: 'second' }, end, done], label)).toThrow('Expected exactly one matching delegation');
  });
});
