import fs from 'node:fs'
import { randomUUID } from 'node:crypto'
import { expect, type Page } from '@playwright/test'
import { DoneFrame } from '../../../src/lib/api/generated/ws-schemas'
import { assistantMessages } from './selectors'

/** First-principles oracle: concatenate two input strings; never copy an observed model output. */
export function assistantReplyChallenge(prefix: string) {
  const left = `${prefix}-${randomUUID()}-`
  const right = randomUUID()
  return {
    nonce: left + right,
    instruction: `Output the concatenation of these two strings with no separator and no quotes: ` +
      `first string "${left}"; second string "${right}".`,
  }
}

/** Completion-selection gate shared by the browser checker and its focused unit tests. */
export function matchingHansReplyCompletions(
  receivedFrames: readonly string[],
  sessionId: string,
  row: { messageId: string | null; turnId: string | null; mergedMessageIds: string | null },
) {
  const messageIds = new Set([row.messageId, ...(row.mergedMessageIds?.split(' ').filter(Boolean) ?? [])])
  return receivedFrames.flatMap((raw) => {
    const frame = DoneFrame.safeParse(JSON.parse(raw))
    return frame.success && frame.data.session_id === sessionId && frame.data.turn_id === row.turnId &&
      frame.data.message_id !== undefined && messageIds.has(frame.data.message_id)
      ? [frame.data] : []
  })
}

/** Hans's "follow-up succeeds" requires an actual completed answer saved in the same conversation. */
export async function assertHansReplyPersisted(
  page: Page,
  receivedFrames: readonly string[],
  sessionId: string,
  agentId: string,
  nonce: string,
  transcriptPath: string,
) {
  const reply = assistantMessages(page).filter({ hasText: nonce })
  await expect(reply, 'the follow-up receives exactly one completed assistant reply').toHaveCount(1, { timeout: 60_000 })
  await expect(reply).toBeVisible()
  const messageId = await reply.getAttribute('data-message-id')
  expect(messageId, 'the completed assistant row has a durable message identity').toEqual(expect.any(String))
  expect(messageId).not.toBe('')

  const row = {
    messageId,
    turnId: await reply.getAttribute('data-turn-id'),
    mergedMessageIds: await reply.getAttribute('data-merged-message-ids'),
  }
  const completed = () => matchingHansReplyCompletions(receivedFrames, sessionId, row)
  await expect.poll(() => completed().length, {
    timeout: 60_000, message: 'one actual turn completion must identify the rendered assistant reply',
  }).toBe(1)
  const done = completed()[0]
  expect(done.turn_id, 'completion identifies the answer turn').toEqual(expect.any(String))
  expect(done.turn_id).not.toBe('')
  expect(done.stats?.turn_failed, 'a failed fallback is not a successful follow-up answer').not.toBe(true)
  expect(done.stats?.truncated, 'a stopped or truncated answer is not completion').not.toBe(true)

  await expect.poll(() => {
    const raw = fs.readFileSync(transcriptPath, 'utf8')
    // Ignore only an unfinished final JSONL row; malformed complete rows remain loud failures.
    return raw.slice(0, raw.lastIndexOf('\n') + 1).split('\n').filter(Boolean)
      .map((line) => JSON.parse(line))
      .filter((entry) => entry.role === 'assistant' && entry.id === messageId &&
        entry.agent_id === agentId && entry.turn_id === done.turn_id && entry.view_membership !== 'model' &&
        entry.truncated !== true && typeof entry.content === 'string' && entry.content.includes(nonce))
  }, {
    timeout: 60_000, message: 'the completed follow-up assistant reply must be persisted with the same ID, agent and turn',
  }).toHaveLength(1)
}
