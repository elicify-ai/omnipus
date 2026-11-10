// C2 test setup: an actually loaded, validated empty page, not a success flag.
// Taking the existing store avoids importing a second barrel/registration owner.
import type { StoreApi } from 'zustand'
import { expect, vi } from 'vitest'
import type { ChatStore } from '@/store/chat/types'
import { expectValidPage, page } from './fixtures'

export async function loadFixtureHistory(store: Pick<StoreApi<ChatStore>, 'getState'>, sessionId: string): Promise<void> {
  const history = page([], { sessionId })
  expectValidPage(history, [], 0)
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(input instanceof Request ? input.url : String(input), 'http://localhost')
    if ((init?.method ?? 'GET') !== 'GET' || url.pathname !== `/api/v1/sessions/${sessionId}/messages`) {
      throw new Error(`Unexpected loaded-history fixture request: ${url.pathname}`)
    }
    return new Response(JSON.stringify(history), { status: 200, headers: { 'Content-Type': 'application/json' } })
  }))
  await store.getState().loadLatestHistory(sessionId)
  expect(store.getState().sessionsById[sessionId]?.history.historyLoaded,
    'D7/D7a: the real HTTP validator, adapter and history owner loaded the page before incremental completion').toBe(true)
}
