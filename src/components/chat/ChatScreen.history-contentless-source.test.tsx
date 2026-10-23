// UAT INCIDENT-1 client side: every real agent turn starts with a content-less
// assistant bookkeeping record. The gateway now replays it as one
// origin-stamped, EMPTY replay_message so the snapshot accounts for every
// counted source group. The real app/frame owner/merger must commit that
// snapshot and draw nothing for the record, with Verbose off and on.
import { afterEach, describe, expect, it } from 'vitest'
import { act, screen } from '@testing-library/react'
import { useChatStore } from '@/store/chat'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { entry, page, pos } from '../../../tests/chat-history-paging/fixtures'
import { openHistory, stopHistory } from '../../../tests/chat-history-paging/application'
import { MAIN_ID } from '../../../tests/l1-room-ui/harness'

let app: Awaited<ReturnType<typeof openHistory>> | undefined
afterEach(async () => {
  await stopHistory(app); app = undefined
  act(() => useChatPreferencesStore.setState({ verboseChatEnabled: false }))
})

describe('content-less turn-start source in a WS snapshot', () => {
  for (const verbose of [false, true]) {
    it(`commits the snapshot and draws no bubble for it (Verbose ${verbose ? 'on' : 'off'})`, async () => {
      act(() => useChatPreferencesStore.setState({ verboseChatEnabled: verbose }))
      // Three counted source groups; the middle one is the turn-start record.
      const sources = [
        entry(1, { role: 'user', content: 'CONTENTLESS_QUESTION' }),
        entry(2, { role: 'assistant', content: '' }),
        entry(3, { role: 'assistant', content: 'CONTENTLESS_ANSWER' }),
      ]
      app = await openHistory(page(sources, { sessionId: MAIN_ID }))
      expect(screen.queryByText('Could not load messages.'), 'the snapshot is accepted, not rejected as inconsistent').toBeNull()
      const history = useChatStore.getState().sessionsById[MAIN_ID].history
      expect(history.historyLoaded).toBe(true)
      expect(history.readerPositions, 'all three counted groups are retained in order').toStrictEqual([pos(1), pos(2), pos(3)])
      expect(screen.getByText('CONTENTLESS_QUESTION')).toBeTruthy()
      expect(screen.getByText('CONTENTLESS_ANSWER')).toBeTruthy()
      const region = screen.getByRole('region', { name: 'Conversation history' })
      const turnStart = region.querySelector(`[data-history-position="${pos(2)}"]`)
      expect(turnStart, 'positive instrument: the turn-start group has its own reader row').not.toBeNull()
      expect(turnStart!.textContent?.trim(), 'the turn-start record draws no visible text').toBe('')
      expect(turnStart!.querySelector('button'), 'no Copy or other control for an empty record').toBeNull()
    })
  }
})
