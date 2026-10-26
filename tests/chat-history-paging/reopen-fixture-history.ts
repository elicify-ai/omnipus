// Test-only declared HTTP edge for D9 reopen. The supplied generated server
// page is independent fixture data, not a UI clone or proof of backend writes.
import { vi } from 'vitest'
import type { StoreApi } from 'zustand'
import type { ChatStore } from '@/store/chat/types'
import type { SessionHistoryResponse } from '@/lib/api/generated/openapi-types'

export async function reopenFixtureHistory(
  store: Pick<StoreApi<ChatStore>, 'getState'>,
  sessionId: string,
  history: SessionHistoryResponse,
): Promise<string[]> {
  const reads: string[] = []
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(input instanceof Request ? input.url : String(input), 'http://localhost')
    const method = init?.method ?? (input instanceof Request ? input.method : 'GET')
    reads.push(`${method} ${url.pathname}${url.search}`)
    if (method !== 'GET' || url.pathname !== `/api/v1/sessions/${sessionId}/messages`) {
      throw new Error(`Undeclared D9 reopen request: ${method} ${url.pathname}`)
    }
    return new Response(JSON.stringify(history), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }))
  await store.getState().loadLatestHistory(sessionId)
  return reads
}
