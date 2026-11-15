import { createElement, type PropsWithChildren } from 'react'
import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ComposerRuntime } from '@assistant-ui/react'
import type { CancelFrame, ServerFrame } from '@/lib/api/generated/asyncapi-types'
import type { SlashCommand } from '@/lib/api/generated/openapi-types'
import type { WsConnection } from '@/lib/ws'
import { useCancelState } from '@/hooks/useCancelState'
import { useSlashMenu } from '@/hooks/useSlashMenu'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useUiStore } from './ui'
import { pendingCancelAckSids } from './chat/runtime-state'
import { emptySessionState } from './chat/session'

// Oracle: arch-decision.md section 1 + CancelFrame.yaml::shown + Round 3d:
// a FIRST Stop freezing an answer carries shown, including scope tree.
// Only a genuinely confirming SECOND press omits another checkpoint.
// REAL: both hooks, React Query/API parsing, reducers and cancelStream.
// EDGES: HTTP, recording socket, editor text/submission, confirmation clock.
const SID = 'first-tree-stop-1534'
const MID = 'first-tree-message-1534'
const TURN = 'first-tree-turn-1534'
const TEXT = 'A😀é'
const SHOWN: NonNullable<CancelFrame['shown']> = {
  message_id: MID, turn_id: TURN,
  code_points: 4, // A + emoji + e + combining mark: 4 code points, not 5 UTF-16 units.
}
const TREE_FRAME: CancelFrame = {
  type: 'cancel', session_id: SID, scope: 'tree', shown: SHOWN,
}
const CANCEL: SlashCommand = {
  name: 'cancel', label: '/cancel', description: 'Stop this session and its helpers',
  delivery: 'client', available_while_streaming: true,
}
const sender = { send: vi.fn<WsConnection['send']>() }
let client: QueryClient | undefined

function resetStores() {
  pendingCancelAckSids.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
  useUiStore.setState(useUiStore.getInitialState(), true)
}

function applyText() {
  const token: ServerFrame = {
    type: 'token', session_id: SID, message_id: MID, turn_id: TURN, content: TEXT,
  }
  act(() => { useChatStore.getState().handleFrame(token) })
  expect(useChatStore.getState().isStreaming).toBe(true)
  expectReply('streaming')
  expect(sender.send.mock.calls).toEqual([])
}

function expectReply(status: 'streaming' | 'interrupted') {
  const bucket = useChatStore.getState().sessionsById[SID]
  expect(bucket.messageOrder.map(id => {
    const row = bucket.messagesById[id]
    return [row.id, row.turnId, row.content, row.status]
  })).toEqual([[MID, TURN, TEXT, status]])
}

function expectFrozen() {
  expect(useChatStore.getState().isStreaming).toBe(false)
  expectReply('interrupted')
  expect(pendingCancelAckSids.has(SID)).toBe(true)
  expect(useUiStore.getState().toasts).toEqual([])
}

function useRealCancel() {
  const streaming = useChatStore(state => state.isStreaming)
  const cancel = useChatStore(state => state.cancelStream)
  return useCancelState(streaming, cancel)
}

beforeEach(() => {
  resetStores()
  sender.send.mockReset().mockReturnValue(true)
  act(() => {
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'mia' })
    useChatStore.setState({ sessionsById: { [SID]: emptySessionState() } })
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
  })
})

afterEach(() => {
  cleanup()
  client?.clear()
  client = undefined
  resetStores()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

describe('#1534 first tree Stop versus confirming second Stop', () => {
  it('a first cancelStream(undefined, tree) freezes the answer and sends its exact shown checkpoint', () => {
    applyText()
    let delivered: boolean | undefined
    act(() => { delivered = useChatStore.getState().cancelStream(undefined, 'tree') })
    expect(delivered).toBe(true)
    expectFrozen()
    expect(sender.send.mock.calls, '#1534 first tree Stop must carry the frozen four-code-point checkpoint')
      .toEqual([[TREE_FRAME]])
  })

  it('typed /cancel alone traverses the real slash and cancel hooks and sends the same first-Stop checkpoint', async () => {
    applyText()
    const fetched: string[] = []
    vi.stubGlobal('fetch', vi.fn<typeof fetch>(async (input) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, 'http://localhost')
      const route = url.pathname + url.search
      fetched.push(route)
      if (route === '/api/v1/commands?surface=web') {
        return new Response(JSON.stringify([CANCEL]), { status: 200 })
      }
      if (route === '/api/v1/skills' || route === '/api/v1/agents') {
        return new Response('[]', { status: 200 })
      }
      throw new Error(`Unexpected HTTP request in first-/cancel test: ${route}`)
    }))
    client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const queryClient = client
    const wrapper = ({ children }: PropsWithChildren) => createElement(QueryClientProvider, { client: queryClient }, children)
    let text = '/cancel'
    const send = vi.fn()
    const composer = {
      getState: () => ({ text }),
      setText: vi.fn((value: string) => { text = value }),
      addAttachment: vi.fn(), subscribe: vi.fn(() => vi.fn()), send,
    } as unknown as ComposerRuntime
    const { result } = renderHook(() => {
      const cancelState = useRealCancel()
      const slash = useSlashMenu({
        isStreaming: useChatStore(state => state.isStreaming),
        isReplaying: false, inputEnabled: true, composerRuntime: composer,
        appendMessage: useChatStore.getState().appendMessage,
        activateStop: cancelState.cancelUnconditional,
        // Same real binding as OmnipusComposer; no cancel callback mock.
        cancelIfStreaming: cancelState.cancelAllTreeScoped,
        sendRedirectFrame: () => { throw new Error('/cancel must not redirect') },
      })
      return { slash, cancelState }
    }, { wrapper })
    act(() => { result.current.slash.onInputChange('/cancel') })
    await waitFor(() => {
      expect(result.current.slash.slashItems.filter(item => item.label === '/cancel').map(item => item.section))
        .toEqual(['commands'])
    })
    expect(result.current.slash.commandsError).toBe(false)
    expect(fetched.filter(route => route === '/api/v1/commands?surface=web')).toEqual(['/api/v1/commands?surface=web'])
    expect(sender.send.mock.calls).toEqual([])
    expect(result.current.cancelState.stopAllArmed).toBe(false)
    let intercepted: boolean | undefined
    act(() => { intercepted = result.current.slash.interceptClientCommand() })
    expect(intercepted).toBe(true)
    expect(send).not.toHaveBeenCalled()
    expect(text).toBe('')
    expect(result.current.cancelState.stopAllArmed).toBe(false)
    expectFrozen()
    expect(sender.send.mock.calls, '#1534 typed /cancel is itself the first Stop; tree scope cannot drop shown')
      .toEqual([[TREE_FRAME]])
  })

  it('control: a genuine second Stop inside the armed window sends a tree frame without a second shown', () => {
    // Freeze the clock, not the unit: both presses happen inside the real window.
    vi.useFakeTimers()
    applyText()
    const { result } = renderHook(useRealCancel)
    expect(result.current.stopAllArmed).toBe(false)
    act(() => { result.current.cancelUnconditional() })
    expect(result.current.stopAllArmed).toBe(true)
    expectFrozen()
    expect(sender.send.mock.calls).toEqual([[{
      type: 'cancel', session_id: SID, shown: SHOWN,
    }]])
    act(() => { result.current.cancelUnconditional() })
    expect(result.current.stopAllArmed).toBe(false)
    expectFrozen()
    expect(sender.send.mock.calls, 'only the first Stop carries shown; the confirming tree frame adds no checkpoint')
      .toEqual([
        [{ type: 'cancel', session_id: SID, shown: SHOWN }],
        [{ type: 'cancel', session_id: SID, scope: 'tree' }],
      ])
  })
})
