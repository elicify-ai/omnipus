// Test-only final-gate harness. Real history/span/token owners and Activity.
import { createElement, type ReactNode } from 'react'
import { renderHook } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { vi } from 'vitest'
import type { Message } from '@/lib/api/generated/openapi-types'
import type { ServerFrame, SessionStateFrame, SubagentStartFrame, SubagentStateFrame, SubagentEndFrame } from '@/lib/api/generated/asyncapi-types'
import { useRunningActivity } from '@/hooks/useRunningActivity'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { resetRealStores } from '../l1-room-ui/harness'
import { SID, STAMP, entry, expectValidPage, page, pos, snapshot } from './fixtures'
import { historyTransport } from './transport'

export { SID, STAMP, entry, expectValidPage, page, pos }
export const SPAN = 'fg-helper-span'
export const START: SubagentStartFrame = { type: 'subagent_start', session_id: SID, span_id: SPAN, parent_call_id: 'fg-parent-call', task_label: 'Read the retained pages', agent_id: 'jim', child_session_id: 'fg-child' }
export const RUNNING: SubagentStateFrame = { type: 'subagent_state', session_id: SID, span_id: SPAN, child_session_id: 'fg-child', state: 'running', created_at: STAMP }
export const AVAILABLE: SessionStateFrame = { type: 'session_state', session_id: SID, user_id: 'fg-reader', pending_approvals: [], emitted_at: STAMP, history_state: { status: 'available' } }
export const BOOT = 'fg-same-boot'
let transport: ReturnType<typeof historyTransport> | undefined
export function reset() {
  vi.useFakeTimers({ toFake: ['Date'] }); vi.setSystemTime(STAMP)
  resetRealStores(); useSessionStore.getState().setActiveSession(SID, 'mia')
  useChatStore.getState().releaseSessionHistory(SID)
}
export function cleanup() {
  transport?.connection.disconnect(); transport = undefined
  useChatStore.getState().releaseSessionHistory(SID)
  queryClient.clear(); useChatStore.setState(useChatStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  vi.unstubAllGlobals(); vi.useRealTimers()
}
export function bucket() {
  const b = useChatStore.getState().sessionsById[SID]
  if (!b) throw new Error('Final-gate instrument: no addressed session bucket')
  return b
}
export function connect() { transport?.connection.disconnect(); transport = historyTransport(); return transport }
export function socket() { if (!transport) throw new Error('Final-gate instrument: no socket'); return transport }
export function open(sources: Message[], frames: ServerFrame[], options: Parameters<typeof page>[1] = {}) {
  const selected = page(sources, { ...options, info: { limit: 20, min_rows: Math.min(20, sources.length || 1), ...options.info } })
  expectValidPage(selected, sources.map(source => source.history_position!), options.rows ?? sources.length)
  const wire = connect(); wire.attach()
  wire.deliver(snapshotAt(selected), AVAILABLE, ...frames, { type: 'done', session_id: SID }, complete())
  return selected
}
export function snapshotAt(selected: ReturnType<typeof page>) { return { ...snapshot(selected), seq: 100, boot_id: BOOT } }
export function complete() { return { type: 'catch_up_complete', session_id: SID, seq: 100, boot_id: BOOT, mode: 'snapshot' } as const }
export function origin(rank: number, id = `fg-source-${rank}`) { return { history_origin: { position: pos(rank), entry_id: id } } }
export function source(rank: number, frame: SubagentStartFrame | SubagentStateFrame | SubagentEndFrame): Message {
  const base = { id: `fg-source-${rank}`, role: 'system' as const, type: 'system' as const, content: '', system_subtype: frame.type }
  switch (frame.type) {
    case 'subagent_start': return entry(rank, { ...base, subagent_start: frame })
    case 'subagent_state': return entry(rank, { ...base, subagent_state: frame })
    case 'subagent_end': return entry(rank, { ...base, subagent_end: frame })
  }
}
export function response(value: unknown) { return new Response(JSON.stringify(value), { status: 200, headers: { 'Content-Type': 'application/json' } }) }
export function mockPage(selected: ReturnType<typeof page>) { vi.stubGlobal('fetch', async () => response(selected)) }
export function activity() {
  useSessionStore.setState({ activeAgentId: null })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  client.setQueryData(['agents'], [])
  const wrapper = ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children)
  const { result, unmount } = renderHook(() => useRunningActivity(), { wrapper })
  const view = result.current; unmount(); client.clear(); return view
}
export function runningPage() {
  const start = source(2, START), state = source(3, RUNNING)
  const later = Array.from({ length: 18 }, (_, i) => entry(4 + i, { id: `fg-later-${i}`, role: 'assistant', content: `Independent saved row ${i}` }))
  return { sources: [start, state, ...later], start, state, later }
}
