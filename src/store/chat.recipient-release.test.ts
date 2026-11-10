// R2-S3-02: an @-message held because the team list is loading or failed must
// be RELEASED by the list loading - no manual drain, no further turn.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useWorkspacesStore } from './workspacesStore'
import { useUiStore } from './ui'
import { queryClient } from '@/lib/queryClient'
import type { WsConnection } from '@/lib/ws'
import type { Agent } from '@/lib/api'
import { makeAgent } from '@/test/factories'

const SID = 'sess_release_test'
const WS = 'ws_release_test'
const agents: Agent[] = [
  makeAgent({ id: 'mia', name: 'Mia', type: 'core', status: 'active' }),
  makeAgent({ id: 'jim', name: 'Jim', type: 'core', status: 'idle' }),
]

beforeEach(() => {
  act(() => {
    useChatStore.getState().clearStreamingState()
    useChatStore.setState({ sessionsById: {}, messages: [], isStreaming: false, outboundQueue: [], pendingDrainQueue: [] } as never)
    useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null } as never)
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'jim', activeAgentType: null })
    useWorkspacesStore.setState({ activeWorkspaceId: WS })
    useUiStore.setState({ toasts: [] })
  })
  queryClient.removeQueries({ queryKey: ['agents'] })
})

function connect() {
  const send = vi.fn().mockReturnValue(true)
  act(() => {
    useConnectionStore.setState({
      connection: { send, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
      isConnected: true,
      connectionError: null,
    } as never)
  })
  return send
}
const frames = (send: ReturnType<typeof vi.fn>) => send.mock.calls.map((c) => c[0] as Record<string, unknown>)

describe('R2-S3-02: the team list loading releases a held @-message', () => {
  it('still-loading list: the held message is sent addressed once the list arrives, with no drain call', () => {
    act(() => { useChatStore.getState().sendMessage('@Mia please review X') }) // offline -> queued
    const send = connect()
    act(() => { useChatStore.getState().drainOutboundQueue() })
    expect(send).not.toHaveBeenCalled()

    act(() => { queryClient.setQueryData(['agents'], agents) })

    expect(frames(send), 'released by the list load').toHaveLength(1)
    expect(frames(send)[0]).toMatchObject({ content: '@Mia please review X', session_id: SID, recipient: { agent_id: 'mia' } })
    expect(useChatStore.getState().pendingDrainQueue).toHaveLength(0)
  })

  it('FAILED list: the hold toast offers Retry, and a later successful load releases the message', async () => {
    await queryClient.fetchQuery({ queryKey: ['agents'], queryFn: () => Promise.reject(new Error('down')), retry: false }).catch(() => {})
    expect(queryClient.getQueryState(['agents'])?.status).toBe('error')
    act(() => { useChatStore.getState().sendMessage('@Mia please review X') })
    const send = connect()
    act(() => { useChatStore.getState().drainOutboundQueue() })
    expect(send).not.toHaveBeenCalled()
    const toast = useUiStore.getState().toasts.find((t) => t.variant === 'warning')
    expect(toast?.action?.label).toBe('Retry')

    act(() => { queryClient.setQueryData(['agents'], agents) })

    expect(frames(send)).toHaveLength(1)
    expect(frames(send)[0]).toMatchObject({ recipient: { agent_id: 'mia' } })
  })

  it('a released message is sent exactly once (a second list update does not resend it)', () => {
    act(() => { useChatStore.getState().sendMessage('@Mia please review X') })
    const send = connect()
    act(() => { useChatStore.getState().drainOutboundQueue() })
    act(() => { queryClient.setQueryData(['agents'], agents) })
    act(() => { queryClient.setQueryData(['agents'], [...agents]) })
    expect(frames(send)).toHaveLength(1)
  })

  it('negative control: with nothing held, a list load sends nothing', () => {
    const send = connect()
    act(() => { queryClient.setQueryData(['agents'], agents) })
    expect(send).not.toHaveBeenCalled()
  })
})
