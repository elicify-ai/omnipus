// Shared #1537 test harness. Real store, ChatScreen and notice indicators.
// Network/query/router and AssistantUI host are the same edge doubles as the
// existing ChatScreen.provider-waiting-attempt1664 test; no provider reducer mock.
import * as React from 'react'
import { act, render, cleanup } from '@testing-library/react'
import { vi } from 'vitest'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import type { ServerFrame } from '@/lib/ws'
import { inFlightReattachSids, gapReattachRetryAttempts, gapReattachRetryTimers, replayErrorRetryAttempts, replayErrorRetryTimers } from '@/store/chat/runtime-state'
import { emptyHistoryPage, queryDataWithEmptyHistory, type HistoryReadPort } from '../chat-history-paging/legacy-fixtures'

export const roster = { data: [{ id: 'researcher', name: 'Researcher' }, { id: 'writer', name: 'Writer' }] as unknown, error: false }
vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock())
vi.mock('@tanstack/react-query', async (original) => ({
  ...await original<typeof import('@tanstack/react-query')>(),
  useQuery: (opts: { queryKey?: readonly unknown[] }) => ({
    data: opts.queryKey?.[0] === 'agents' ? roster.data : queryDataWithEmptyHistory(opts.queryKey),
    isError: opts.queryKey?.[0] === 'agents' && roster.error, refetch: vi.fn(),
  }),
  useMutation: () => ({ mutate: vi.fn(), isPending: false }),
  useQueryClient: () => ({ invalidateQueries: vi.fn(), removeQueries: vi.fn() }),
}))
vi.mock('@tanstack/react-router', () => ({ useRouter: () => ({ navigate: vi.fn() }), useSearch: () => ({}), Link: ({ children }: { children: React.ReactNode }) => children }))
vi.mock('@/lib/api', async (original) => ({
  ...await original<typeof import('@/lib/api')>(), fetchAgents: vi.fn().mockResolvedValue([]),
  fetchSessionMessages: vi.fn<HistoryReadPort>(async (sid) => emptyHistoryPage(sid)),
  fetchAboutInfo: vi.fn().mockResolvedValue({}), fetchProviders: vi.fn().mockResolvedValue([]),
  fetchCommands: vi.fn().mockResolvedValue([]), fetchSkills: vi.fn().mockResolvedValue([]),
}))
vi.mock('@/components/chat/historical-markdown', () => ({ HistoricalMessageMarkdown: ({ content }: { content: string }) => React.createElement('div', {}, content) }))
vi.mock('@/components/chat/RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('@/components/chat/composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('@/components/chat/composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('@/lib/memory-observer', () => ({ startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }), addMemoryObserver: () => () => {}, getCurrentSnapshot: () => ({ level: 'ok', usedJSHeapSizeBytes: null, supported: false }) }))

import { ChatScreen } from '@/components/chat/ChatScreen'
export const SID = 'hm-main-session'
export const AT = '2026-11-15T12:00:00.000Z'
export function reset() {
  cleanup()
  inFlightReattachSids.clear()
  for (const timers of [gapReattachRetryTimers, replayErrorRetryTimers]) {
    for (const sid of Object.keys(timers)) { clearTimeout(timers[sid]); delete timers[sid] }
  }
  for (const attempts of [gapReattachRetryAttempts, replayErrorRetryAttempts]) {
    for (const sid of Object.keys(attempts)) delete attempts[sid]
  }
  vi.stubGlobal('ResizeObserver', undefined)
  roster.data = [{ id: 'researcher', name: 'Researcher' }, { id: 'writer', name: 'Writer' }]
  roster.error = false
  useConnectionStore.setState({ isConnected: true, liteMode: false, connection: null, reconnectPhase: null, connectionError: null })
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'main' })
  useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {}, messageOrder: [], isStreaming: false } as never)
}
export function feed(...frames: object[]) {
  act(() => { for (const frame of frames) useChatStore.getState().handleFrame(frame as unknown as ServerFrame) })
}
export function retry(child = 'child-a', turn = 'turn-a', extra: Record<string, unknown> = {}) {
  return { type: 'provider_retry', session_id: SID, turn_id: turn, provider: 'openrouter', model: 'model-a',
    sent_at: AT, retry_at: '2026-11-15T12:00:30.000Z', retry_after_seconds: 30, attempt: 2, max_attempts: 6,
    error_code: 'rate_limited', ...(child ? { child_session_id: child, agent_id: 'researcher' } : {}), ...extra }
}
export function status(state: string, child = 'child-a', turn = 'turn-a', extra: Record<string, unknown> = {}) {
  return { type: 'provider_status', session_id: SID, turn_id: turn, state, sent_at: AT,
    ...(child ? { child_session_id: child, agent_id: 'researcher' } : {}),
    ...(state === 'waiting' ? { silent_since: '2026-11-15T11:59:30.000Z' } : {}),
    ...(state === 'recovered' ? { retries: 1 } : {}), ...extra }
}
export function active(turn = 'own-turn', emitted = AT) {
  return { type: 'session_state', session_id: SID, user_id: 'hm-user', pending_approvals: [], emitted_at: emitted,
    active_turn: { turn_id: turn, agent_id: 'main', started_at: AT }, history_state: { status: 'available' } }
}
export function mount() { return render(<ChatScreen />) }
export function bucket() { return useChatStore.getState().sessionsById[SID] }

// Find the smallest visible element containing the specified full sentence.
// Never count a hidden announcement as proof of a visible notice.
export function lines(container: HTMLElement, text: string): HTMLElement[] {
  const nodes = [...container.querySelectorAll<HTMLElement>('*')].filter(el =>
    !el.closest('[aria-live], [role="status"], [aria-hidden="true"], .sr-only') && el.textContent?.includes(text))
  return nodes.filter(el => !nodes.some(other => other !== el && el.contains(other)))
}
export function named(container: HTMLElement, name: string, clause: string): HTMLElement[] {
  const nodes = [...container.querySelectorAll<HTMLElement>('*')].filter(el => {
    const text = el.textContent?.trim() ?? ''
    return !el.closest('[aria-live], [role="status"], [aria-hidden="true"], .sr-only') && text.startsWith(`${name}: `) && text.includes(clause)
  })
  return nodes.filter(el => !nodes.some(other => other !== el && el.contains(other)))
}
