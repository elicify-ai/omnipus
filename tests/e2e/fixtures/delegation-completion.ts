export type DelegationFrame = Record<string, unknown>;

/** Match the requested live child and its parent turn, independently of prose bubbles. */
export function completedDelegation(frames: DelegationFrame[], label: string): string | null {
  const starts = frames.filter(frame => frame.type === 'subagent_start' && frame.task_label === label);
  if (starts.length > 1) throw new Error('Expected exactly one matching delegation');
  const start = starts[0];
  if (!start || typeof start.span_id !== 'string' || typeof start.session_id !== 'string') return null;
  const subsequent = frames.slice(frames.indexOf(start) + 1);
  const end = subsequent.find(frame => frame.type === 'subagent_end' && frame.span_id === start.span_id && frame.session_id === start.session_id);
  if (!end) return null;
  if (end.status !== 'success') throw new Error(`Delegation ended with status ${String(end.status)}`);
  // Background children may finish after their parent; awaited children finish
  // before it. A replay/previous-turn done preceding this start is insufficient.
  const parentDone = subsequent.some(frame => frame.type === 'done' && frame.session_id === start.session_id);
  return parentDone ? start.span_id : null;
}
