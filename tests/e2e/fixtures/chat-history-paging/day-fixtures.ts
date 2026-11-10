import type { Message } from '../../../../src/lib/api/generated/openapi-types';
import type { ChatHistoryFixtureDay } from '../chat-history';

export function utcDay(daysAgo: number, reference = new Date()) {
  const date = new Date(reference.getTime());
  date.setUTCDate(date.getUTCDate() - daysAgo);
  return date.toISOString().slice(0, 10);
}
export function fixtureMessage(label: string, ordinal: number, agentId: string, bodyBytes = 96, tool = false): Message {
  const id = `${label}-${String(ordinal).padStart(6, '0')}`;
  const value: Message = { id, type: 'message', role: 'assistant', agent_id: agentId,
    timestamp: '2026-07-01T12:00:00Z', content: id + 'x'.repeat(Math.max(0, bodyBytes - id.length)) };
  if (tool) {
    value.content = '';
    value.tool_calls = [{ id: `call-${id}`, tool: 'read_file', status: 'success', content_state: 'full',
      parameters: { path: `${id}.txt` }, result: { content: id + 'x'.repeat(bodyBytes) } }];
  }
  return value;
}

/** D2 A2.1/A2.7: each result is exactly51,200 JSON bytes (11 syntax
 * bytes +51,189 ASCII bytes), so shortening leaves every contribution whole.
 * Each call counts as one drawn row; nonempty prose adds one more row. */
export const INLINE_RESULT_BYTES = 51_200;
export function inlineToolMessage(label: string, ordinal: number, agentId: string, calls = 5, content = ''): Message {
  const value = fixtureMessage(label, ordinal, agentId);
  value.content = content;
  value.tool_calls = Array.from({ length: calls }, (_, n) => {
    const id = `call-${value.id}-${n}`;
    return { id, tool: 'read_file', status: 'success', content_state: 'full',
      parameters: { path: `${id}.txt` }, result: { text: id + 'x'.repeat(INLINE_RESULT_BYTES - 11 - id.length) } };
  });
  return value;
}

/** D-A1: actual90 append-day partitions, not spoofed entry timestamps. */
export function prefixFixtureDays(prefix: number, agentId: string): ChatHistoryFixtureDay[] {
  const reference = new Date();
  const days = Array.from({ length: 90 }, (_, n) => ({ day: utcDay(89 - n, reference), entries: [] as Message[] }));
  for (let n = 0; n < prefix; n++) days[Math.floor(n * 90 / prefix)].entries.push(fixtureMessage('unrelated-prefix', n, agentId, 1100));
  for (let n = 0; n < 200; n++) days[89].entries.push(fixtureMessage('equal-suffix', n, agentId));
  return days;
}

/** One record per rolled partition for the old1000-group region, current live
 * tail otherwise. Later tests can expire exactly an anchor or its whole held
 * window through real file aging + product sweep, without rewriting bytes. */
export function readerFixtureDays(label: string, agentId: string, largeOldTools = false): ChatHistoryFixtureDay[] {
  const reference = new Date();
  const days: ChatHistoryFixtureDay[] = [];
  for (let n = 0; n < 1000; n++) days.push({ day: utcDay(1000 - n, reference),
    // D-F1/A2.7: two inline50-KiB results/owner, not one shortened200-KiB
    // result.40×102KiB +8KiB envelope <4MiB;41×102,400 >4MiB.
    // Thus500 held owners need13 requests (12×40 +20), not five cap pages.
    entries: [largeOldTools ? inlineToolMessage(label, n, agentId, 2) : fixtureMessage(label, n, agentId)] });
  days.push({ day: utcDay(0, reference), entries: Array.from({ length: 2400 }, (_, n) => fixtureMessage(label, 1000 + n, agentId)) });
  return days;
}
