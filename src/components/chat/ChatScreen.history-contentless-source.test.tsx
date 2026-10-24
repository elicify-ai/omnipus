// UAT INCIDENT-1 client side: every real agent turn starts with a content-less
// assistant bookkeeping record. The gateway replays it as one origin-stamped,
// EMPTY replay_message so the snapshot accounts for every counted source group.
// The real app/frame owner/merger must commit that snapshot and draw nothing for
// the bookkeeping record -- but an empty row that carries an outcome (e.g. a
// saved interrupted reply) must keep its explanation. Both list implementations.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider, ThreadPrimitive } from '@assistant-ui/react'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { queryClient } from '@/lib/queryClient'
import { VirtualizedMessageList } from './ChatMessageList'
import { json } from '../../../tests/l1-room-ui/harness'
import { resetStage1App, Stage1Network, stopStage1App } from '../../../tests/l1-room-ui/stage1-harness'
import type { Message } from '@/lib/api/generated/openapi-types'
import { useChatStore } from '@/store/chat'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { entry, page, pos } from '../../../tests/chat-history-paging/fixtures'
import { openHistory, stopHistory } from '../../../tests/chat-history-paging/application'
import { installHistoryLayout } from '../../../tests/chat-history-paging/measurement-layout'
import { MAIN_ID } from '../../../tests/l1-room-ui/harness'

let app: Awaited<ReturnType<typeof openHistory>> | undefined
let layout: ReturnType<typeof installHistoryLayout> | undefined
afterEach(async () => {
  await stopHistory(app); app = undefined
  layout?.restore(); layout = undefined
  act(() => useChatPreferencesStore.setState({ verboseChatEnabled: false }))
})

async function open(middle: Message, list: 'plain' | 'virtual') {
  if (list === 'virtual') layout = installHistoryLayout({ virtual: true })
  // Three counted source groups; the middle one is the record under test.
  const sources = [entry(1, { role: 'user', content: 'CONTENTLESS_QUESTION' }), middle, entry(3, { role: 'assistant', content: 'CONTENTLESS_ANSWER' })]
  app = await openHistory(page(sources, { sessionId: MAIN_ID }), { plain: list === 'plain' })
  expect(screen.queryByText('Could not load messages.'), 'the snapshot is accepted, not rejected as inconsistent').toBeNull()
  expect(useChatStore.getState().sessionsById[MAIN_ID].history.historyLoaded, 'a successful snapshot is marked loaded').toBe(true)
  expect(useChatStore.getState().sessionsById[MAIN_ID].history.readerPositions, 'all three counted groups are retained in order').toStrictEqual([pos(1), pos(2), pos(3)])
  expect(screen.getByText('CONTENTLESS_QUESTION'), 'hiding bookkeeping must not hide the neighboring user message').toBeTruthy()
  expect(screen.getByText('CONTENTLESS_ANSWER')).toBeTruthy()
  const row = screen.getByRole('region', { name: 'Conversation history' }).querySelector(`[data-history-position="${pos(2)}"]`)
  expect(row, 'positive instrument: the middle group has its own reader row').not.toBeNull()
  return row!
}

describe('content-less sources in a WS snapshot', () => {
  for (const list of ['plain', 'virtual'] as const) {
    for (const verbose of [false, true]) {
      it(`${list} list: a turn-start bookkeeping record draws no bubble (Verbose ${verbose ? 'on' : 'off'})`, async () => {
        act(() => useChatPreferencesStore.setState({ verboseChatEnabled: verbose }))
        const row = await open(entry(2, { role: 'assistant', content: '' }), list)
        expect(row.textContent?.trim(), 'the turn-start record draws no visible text').toBe('')
        expect(row.querySelector('button'), 'no Copy or other control for an empty record').toBeNull()
      })
    }
    it(`${list} list: an empty saved REST reply that was interrupted keeps its "(interrupted)" explanation`, async () => {
      // REST adapter -> renderer: status survives without a truncated flag.
      resetStage1App()
      const network = new Stage1Network()
      if (list === 'virtual') layout = installHistoryLayout({ virtual: true, hidden: true })
      else vi.stubGlobal('ResizeObserver', undefined)
      const sources = [entry(1, { role: 'user', content: 'INTERRUPTED_QUESTION' }), entry(2, { role: 'assistant', content: '', status: 'interrupted' }), entry(3, { role: 'assistant', content: 'LATER_ANSWER' })]
      network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => json(page(sources, { sessionId: MAIN_ID })))
      try {
        await act(async () => { await useChatStore.getState().loadLatestHistory(MAIN_ID, { limit: 40, min_rows: 20 }) })
        function Rows() {
          const runtime = useOmnipusRuntime()
          const messages = useChatStore(state => state.messages)
          return <AssistantRuntimeProvider runtime={runtime}><ThreadPrimitive.Root><VirtualizedMessageList messages={messages} liteMode={false} agentName="Mia" /></ThreadPrimitive.Root></AssistantRuntimeProvider>
        }
        render(<QueryClientProvider client={queryClient}><Rows /></QueryClientProvider>)
        await act(async () => { layout?.flushResize() })
        expect(screen.getByText('LATER_ANSWER'), 'positive instrument: the REST page rendered').toBeTruthy()
        const row = screen.getByRole('region', { name: 'Conversation history' }).querySelector(`[data-history-position="${pos(2)}"]`)
        expect(row, 'positive instrument: the interrupted group has its own reader row').not.toBeNull()
        expect(row!.textContent, 'an outcome-bearing empty row is not silent').toContain('(interrupted)')
      } finally {
        await stopStage1App()
        if (list === 'plain') vi.unstubAllGlobals()
        expect(network.unexpected, 'all process-edge requests are declared').toStrictEqual([])
      }
    })
  }
})
