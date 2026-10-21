import { SessionHistoryResponse } from '../../../src/lib/api/generated/schemas';
import type { Message } from '../../../src/lib/api/generated/openapi-types';

// not-wire-format: injected HTTP reader shape for tests. No Playwright runtime
// import/test registration: Vitest imports THIS real helper, not a copy.
export type HistoryHTTPReader = { request: { get(url: string): Promise<{
  ok(): boolean; status(): number; text(): Promise<string>; json(): Promise<unknown>;
}> } };

/** All retained history, explicitly including predecessors of clear dividers.
 * This helper's intent is enumeration, not automatic UI prefetch. It starts at
 * latest, deliberately crosses each clear with exclusive before, and stops
 * only on truthful terminal metadata. It cannot prove absence of FUTURE sends.
 */
export async function sessionMessages(page: HistoryHTTPReader, origin: string, sessionId: string): Promise<Message[]> {
  const messages: Message[] = [];
  const positions = new Set<string>();
  const boundaries = new Set<string>();
  let before: string | undefined;
  for (;;) {
    const query = new URLSearchParams({ limit: '100', min_rows: '20' });
    if (before !== undefined) {
      if (boundaries.has(before)) throw new Error('history enumeration repeated an exclusive boundary');
      boundaries.add(before);
      query.set('before', before);
    }
    const response = await page.request.get(`${origin}/api/v1/sessions/${sessionId}/messages?${query}`);
    if (!response.ok()) throw new Error(`GET session history failed: ${response.status()} ${await response.text()}`);
    const body = SessionHistoryResponse.parse(await response.json());
    if (body.session_id !== sessionId) throw new Error('history enumeration received the wrong session');
    const info = body.page_info;
    if (info.mode !== (before === undefined ? 'latest' : 'older')) throw new Error('history enumeration received the wrong page mode');
    if (info.min_rows > info.limit || info.source_group_count !== body.entries.length) throw new Error('history page has contradictory counts');
    if (body.has_earlier !== (body.next_before !== undefined) || body.has_later !== (body.next_after !== undefined)) throw new Error('history page has contradictory continuation');
    if (info.stop_reason === 'byte_budget' && info.row_count < info.min_rows) throw new Error('history page byte budget cut below the height floor');
    if (info.byte_budget_exceeded !== (info.projected_bytes > info.soft_byte_budget) || info.byte_budget_exceeded !== (info.budget_override !== undefined)) throw new Error('history page has contradictory byte accounting');
    if (!body.entries.length) {
      if (body.has_earlier || body.has_later || info.first_position !== undefined || info.last_position !== undefined) throw new Error('empty history page advertises phantom continuation');
    } else {
      if (info.first_position !== body.entries[0].history_position || info.last_position !== body.entries.at(-1)?.history_position) throw new Error('history page endpoints contradict its source entries');
      if (body.next_before !== undefined && body.next_before !== info.first_position) throw new Error('history before cursor is not the first source position');
      if (body.next_after !== undefined && body.next_after !== info.last_position) throw new Error('history after cursor is not the last source position');
    }
    for (const entry of body.entries) {
      if (entry.history_position === undefined) throw new Error('paged history entry has no occurrence position');
      if (positions.has(entry.history_position)) throw new Error('exclusive history pages overlap an already-read occurrence');
      positions.add(entry.history_position);
    }
    messages.unshift(...body.entries); // Physical pages≤100; source IDs can legitimately repeat.
    if (!body.has_earlier) return messages;
    if (body.next_before === before) throw new Error('history enumeration made no progress');
    before = body.next_before;
  }
}

/** Parent-wake isolation oracle: absence must mean ALL retained pages, including
 * old clear segments, not just the newest suffix or an unvalidated wrapper. */
export async function assertSessionHistoryExcludes(page: HistoryHTTPReader, origin: string, sessionId: string, forbidden: string): Promise<Message[]> {
  const messages = await sessionMessages(page, origin, sessionId);
  if (JSON.stringify(messages).includes(forbidden)) throw new Error('another session retained forbidden parent-wake output');
  return messages;
}
