/**
 * ChatScreen.tool-ui-live-registration.test.tsx — ctui-gate finding 3a
 * (sev-8): the registration tests captured makeAssistantToolUI calls at
 * import time, but nothing ever MOUNTED OmnipusRuntimeProvider and rendered
 * a live tool part through the real @assistant-ui/react resolution — so a
 * systematically broken registration block would still have passed every
 * test. This file mounts the REAL provider (real useAssistantToolUI, real
 * setToolUI registry, real MessagePrimitive.Parts resolution) over a seeded
 * chat store and asserts a live search_web / list_directory part resolves to
 * its dedicated block — and, as the negative control that the instrument
 * can see the failure, that an unregistered tool falls through to the
 * generic badge (tool-call-badge).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { ThreadPrimitive, MessagePrimitive } from '@assistant-ui/react'
import { render, act, screen } from '@testing-library/react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { GenericToolCall } from './tools/GenericToolCall'
import { OmnipusRuntimeProvider } from './OmnipusRuntimeProvider'

// WsLifecycle constructs a real WsConnection on mount — stub the transport.
vi.mock('@/lib/ws', () => ({
  WsConnection: class {
    connect() {}
    disconnect() {}
    send() {
      return false
    }
  },
}))

vi.mock('@/lib/memory-observer', () => ({
  startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }),
  addMemoryObserver: () => () => {},
  getCurrentSnapshot: () => ({ usedJSHeapSizeBytes: null, level: 'ok', supported: false }),
}))


const SID = 'sess_live_tool_ui'

function seedLiveMessages(tool: string, params: Record<string, unknown>, result: unknown): void {
  const assistantMsg: ChatMessage = {
    id: 'msg_live_tool',
    role: 'assistant',
    content: '',
    timestamp: new Date().toISOString(),
    status: 'done',
    tool_calls: [
      { id: 'tc_live_1', tool, params, status: 'success', result },
    ],
  }
  const bucket = makeBucketMessages([assistantMsg])
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      [SID]: {
        ...((s.sessionsById ?? {})[SID] ?? {}),
        ...bucket,
        isStreaming: false,
        isReplaying: false,
        replayCompletedForSession: SID,
        toolCalls: {},
        toolCallOrder: [],
        textAtToolCallStart: {},
        sessionTokens: 0,
        sessionCost: 0,
        rateLimitEvent: null,
        lastUserMessageAt: null,
        cancelStage: null,
        lastReceivedEventTime: null,
        trimmedCount: 0,
      },
    },
    messages: [assistantMsg],
    isStreaming: false,
    isReplaying: false,
    replayCompletedForSession: SID,
  }))
}

// jsdom has no ResizeObserver; AssistantUI's viewport hook constructs one
// directly (useOnResizeContent) — provide a stub class, NOT undefined (an
// undefined ResizeObserver is what ChatScreen uses to select its
// PlainMessageList fallback, and AssistantUI would throw either way).
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
;(globalThis as unknown as { ResizeObserver?: unknown }).ResizeObserver = ResizeObserverStub

// jsdom has no Element.scrollTo; AssistantUI's viewport auto-scroll calls it
// from a rAF callback that jsdom timers can fire AFTER the test ends, which
// both throws unhandled and nondeterministically poisons the next test.
if (!Element.prototype.scrollTo) {
  Element.prototype.scrollTo = () => {}
  Element.prototype.scrollBy = () => {}
}

beforeEach(() => {
  useConnectionStore.setState({
    isConnected: true,
    liteMode: false,
    reconnectPhase: null,
    reconnectAttempt: 0,
    connectionError: null,
    connection: null,
  })
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
  act(() => {
    useChatStore.getState().resetSession()
  })
})

afterEach(() => {
  vi.restoreAllMocks()
})

// The live bubble mirrors ChatScreen's AssistantMessage parts config: the
// Fallback is GenericToolCall (what FallbackToolUI wraps), so an unregistered
// tool renders the generic badge while a registered one pre-empts it.
async function renderLiveThread() {
  let container!: HTMLElement
  await act(async () => {
    const res = render(
      <OmnipusRuntimeProvider>
        <ThreadPrimitive.Root>
          <ThreadPrimitive.Viewport>
            <ThreadPrimitive.Messages>
              {({ message }: { message: { role: string } }) =>
                message.role === 'assistant' ? (
                  <MessagePrimitive.Root>
                    <MessagePrimitive.Parts
                      components={{
                        tools: {
                          Fallback: GenericToolCall as unknown as import('@assistant-ui/react').ToolCallMessagePartComponent,
                        },
                      }}
                    />
                  </MessagePrimitive.Root>
                ) : null
              }
            </ThreadPrimitive.Messages>
          </ThreadPrimitive.Viewport>
        </ThreadPrimitive.Root>
      </OmnipusRuntimeProvider>,
    )
    container = res.container
  })
  return container
}

describe('Live tool-UI registration — ctui-gate fix 3a (sev-8)', () => {
  it('a live search_web part resolves to WebSearchBlock (web-search-toggle), not the generic badge', async () => {
    seedLiveMessages('search_web', { query: 'go embed spa' }, '1. Result one\n   https://example.com\n   a snippet')
    await renderLiveThread()

    // The setToolUI registry population is asynchronous (each registration's
    // useAssistantToolUI effect lands after mount) — wait for resolution.
    const toggle = await screen.findByTestId('web-search-toggle')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(toggle.textContent).toContain('search_web')
    expect(toggle.textContent).toContain('go embed spa')
    expect(toggle.textContent).toContain('1 results')
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  it('a live list_directory part resolves to FileTreeBlock (file-tree-toggle), not the generic badge', async () => {
    seedLiveMessages('list_directory', { path: '/ws/pkg' }, 'web.go\nfs.go\n')
    await renderLiveThread()

    // Same asynchronous registry wait as the search_web case.
    const toggle = await screen.findByTestId('file-tree-toggle')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(toggle.textContent).toContain('/ws/pkg')
    expect(toggle.textContent).toContain('2 entries')
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  it('negative control: an unregistered tool falls through to the generic badge (the instrument works)', async () => {
    seedLiveMessages('totally_unknown_tool', { x: 1 }, 'plain text result')
    await renderLiveThread()

    const badge = await screen.findByTestId('tool-call-badge', {}, { timeout: 3000 })
    expect(badge).toBeInTheDocument()
    expect(screen.queryByTestId('web-search-toggle')).toBeNull()
    expect(screen.queryByTestId('file-tree-toggle')).toBeNull()
  })
})
