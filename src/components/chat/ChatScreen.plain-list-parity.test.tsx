/**
 * ChatScreen.plain-list-parity.test.tsx
 *
 * Covers two fixes in VirtualAssistantMessageRow (the PlainMessageList fallback
 * path — jsdom has no ResizeObserver, so ChatScreen always uses this path in
 * tests; it's also the render path assistant-ui's mocked ThreadPrimitive.Messages
 * forces EVERY test in this repo down for tool-call assertions, matching the
 * "reloaded from history" real-world case where every message is historical):
 *
 * D — the store's optimistic assistant placeholder (isStreaming:true,
 *     content:'') must render a thinking indicator, not an empty bubble with a
 *     Copy button (see ChatScreen.assistant-empty-bubble.test.tsx for the LIVE
 *     AssistantMessage() equivalent — this file covers the same guard in the
 *     fallback row renderer used when ResizeObserver is unavailable).
 *
 * B — a browser.click/browser_click-style tool call must render through the same
 *     BrowserToolBlock UI the live path uses (via BrowserToolReplayBlock), not
 *     GenericToolCall's raw-JSON fallback. Before this fix every browser tool
 *     call fell through to GenericToolCall on replay, which is why a reloaded
 *     browser_screenshot call showed a compact `{"media":[...]}` blob instead
 *     of a parsed result card.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act } from '@testing-library/react'
import * as React from 'react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'

vi.mock('@assistant-ui/react', () => {
  return {
    useThreadViewportStore: () => ({ getState: () => ({ isAtBottom: true }) }),
    ThreadPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Viewport: React.forwardRef(
        (
          { children, className, style, 'data-testid': testId }: {
            children?: React.ReactNode; className?: string; style?: React.CSSProperties; 'data-testid'?: string
          },
          ref: React.Ref<HTMLDivElement>,
        ) => React.createElement('div', { ref, className, style, 'data-testid': testId }, children),
      ),
      Messages: () => null,
    },
    MessagePrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Parts: () => null,
    },
    ComposerPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Input: ({ disabled, placeholder, className, onChange, onKeyDown, onBlur }: {
        disabled?: boolean; placeholder?: string; className?: string;
        onChange?: (e: React.ChangeEvent<HTMLTextAreaElement>) => void;
        onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
        onBlur?: () => void;
      }) =>
        React.createElement('textarea', {
          disabled, placeholder, className, onChange, onKeyDown, onBlur,
          'data-testid': 'composer-input',
        }),
      Send: ({ disabled, children, className, 'data-testid': testId }: {
        disabled?: boolean; children?: React.ReactNode; className?: string; 'data-testid'?: string
      }) =>
        React.createElement('button', { type: 'button', disabled, className, 'data-testid': testId ?? 'chat-send' }, children),
      AddAttachment: ({ disabled, children, className }: { disabled?: boolean; children?: React.ReactNode; className?: string }) =>
        React.createElement('button', { type: 'button', disabled, className, 'data-testid': 'add-attachment' }, children),
      Attachments: () => null,
    },
    AttachmentPrimitive: {
      Root: ({ children, className }: { children?: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Name: () => null,
      Remove: ({ children, className }: { children?: React.ReactNode; className?: string }) =>
        React.createElement('button', { type: 'button', className }, children),
      Thumb: () => null,
    },
    MessagePartPrimitive: { InProgress: () => null },
    ActionBarPrimitive: {
      Root: ({ children }: { children: React.ReactNode }) => React.createElement('div', {}, children),
      Copy: ({ children }: { children: React.ReactNode }) => React.createElement('span', {}, children),
    },
    AuiIf: () => null,
    useComposerRuntime: () => ({
      getState: () => ({ text: '' }),
      setText: vi.fn(),
      addAttachment: vi.fn(),
    }),
    useMessage: () => ({
      id: 'msg_streaming',
      role: 'assistant',
      status: { type: 'running' },
      content: [],
    }),
    useAttachment: vi.fn(() => ({
      id: 'att-default',
      name: 'file.txt',
      contentType: 'text/plain',
      file: undefined,
      status: { type: 'complete' },
      content: [],
    })),
    makeAssistantToolUI: () => () => null,
  }
})

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: () => ({ data: [], isError: false, refetch: vi.fn() }),
    useMutation: () => ({ mutate: vi.fn(), isPending: false }),
    useQueryClient: () => ({ invalidateQueries: vi.fn(), removeQueries: vi.fn() }),
  }
})

vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

vi.mock('@/lib/api', () => ({
  fetchAgents: vi.fn().mockResolvedValue([]),
  fetchSessionMessages: vi.fn().mockResolvedValue([]),
  fetchAboutInfo: vi.fn().mockResolvedValue({ preview_port: 5001 }),
  createSession: vi.fn(),
  uploadFiles: vi.fn(),
  fetchProviders: vi.fn().mockResolvedValue([]),
  isApiError: vi.fn().mockReturnValue(false),
}))

vi.mock('./historical-markdown', () => ({
  HistoricalMessageMarkdown: ({ content }: { content: string }) =>
    React.createElement('div', { 'data-testid': 'historical-markdown' }, content),
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./SessionPanel', () => ({ SessionPanel: () => null }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
// Distinguishable marker so B's assertions can prove GenericToolCall (the raw
// JSON fallback) was NOT used for a browser tool call.
vi.mock('./tools/GenericToolCall', () => ({
  GenericToolCall: ({ toolName }: { toolName: string }) =>
    React.createElement('div', { 'data-testid': 'generic-tool-call-badge' }, toolName),
}))
vi.mock('./markdown-text', () => ({
  MarkdownText: () => React.createElement('div', {}),
}))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
// Composer Redesign (variant A1): this file targets VirtualAssistantMessageRow
// (the PlainMessageList fallback), not the composer's picker/model/token
// sub-components — stub them to null so their workspaces/providers query
// plumbing doesn't need mocking here.
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('@/lib/memory-observer', () => ({
  startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }),
  addMemoryObserver: () => () => {},
  getCurrentSnapshot: () => ({ usedJSHeapSizeBytes: null, level: 'ok', supported: false }),
}))

import { ChatScreen } from './ChatScreen'

const SID = 'test-session-plain-list-parity'

function seedBucket(messages: ChatMessage[], overrides: { isStreaming: boolean }): void {
  const bucket = makeBucketMessages(messages)
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      [SID]: {
        ...((s.sessionsById ?? {})[SID] ?? {}),
        ...bucket,
        isStreaming: overrides.isStreaming,
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
        spanByParentCallId: {},
        trimmedCount: 0,
      },
    },
    messages,
    isStreaming: overrides.isStreaming,
    isReplaying: false,
    replayCompletedForSession: SID,
  }))
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
}

describe('VirtualAssistantMessageRow (PlainMessageList fallback)', () => {
  beforeEach(() => {
    useConnectionStore.setState({
      isConnected: true,
      liteMode: false,
      reconnectPhase: null,
      reconnectAttempt: 0,
      connectionError: null,
      connection: null,
    })
    // Force the PlainMessageList fallback so every message — including a
    // still-streaming last message — renders through VirtualAssistantMessageRow.
    vi.unstubAllGlobals()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ;(globalThis as any).ResizeObserver = undefined
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  describe('D — empty streaming placeholder', () => {
    it('shows the thinking indicator and no Copy button while content is empty', async () => {
      const userMsg: ChatMessage = {
        id: 'msg_user', role: 'user', content: 'hi', timestamp: new Date().toISOString(), status: 'done',
      }
      const streamingMsg: ChatMessage = {
        id: 'msg_assistant', role: 'assistant', content: '',
        timestamp: new Date().toISOString(), status: 'streaming', isStreaming: true,
      }
      seedBucket([userMsg, streamingMsg], { isStreaming: true })

      let container!: HTMLElement
      await act(async () => {
        const result = render(<ChatScreen />)
        container = result.container
      })

      const bubble = container.querySelector('[data-testid="assistant-message"]')
      expect(bubble).toBeTruthy()
      expect(bubble!.textContent).not.toContain('Copy')
      expect(bubble!.textContent).toMatch(
        /Thinking…|Composing response…|Processing your request…|Analyzing…|Generating…/,
      )
    })

    it('shows the streamed text and the Copy button once content has arrived', async () => {
      const userMsg: ChatMessage = {
        id: 'msg_user', role: 'user', content: 'hi', timestamp: new Date().toISOString(), status: 'done',
      }
      const streamingMsg: ChatMessage = {
        id: 'msg_assistant', role: 'assistant', content: 'Hello there',
        timestamp: new Date().toISOString(), status: 'streaming', isStreaming: true,
      }
      seedBucket([userMsg, streamingMsg], { isStreaming: true })

      let container!: HTMLElement
      await act(async () => {
        const result = render(<ChatScreen />)
        container = result.container
      })

      const bubble = container.querySelector('[data-testid="assistant-message"]')
      expect(bubble).toBeTruthy()
      expect(bubble!.querySelector('[data-testid="historical-markdown"]')?.textContent).toBe('Hello there')
      expect(bubble!.textContent).toContain('Copy')
    })
  })

  describe('B — browser tool call replay parity', () => {
    it('renders a replayed browser_screenshot call through BrowserToolBlock, not the raw-JSON GenericToolCall fallback', async () => {
      const assistantMsg: ChatMessage = {
        id: 'msg_assistant',
        role: 'assistant',
        content: 'Here is the screenshot.',
        timestamp: new Date().toISOString(),
        status: 'done',
        tool_calls: [
          {
            id: 'call_1',
            tool: 'browser_screenshot',
            params: {},
            result: { media: [{ ref: 'media://abc', filename: 'shot.jpg', content_type: 'image/jpeg', type: 'image' }] },
            status: 'success',
          },
        ],
      }
      seedBucket([assistantMsg], { isStreaming: false })

      let container!: HTMLElement
      await act(async () => {
        const result = render(<ChatScreen />)
        container = result.container
      })

      // The generic raw-JSON fallback must NOT have been used for this tool.
      expect(container.querySelector('[data-testid="generic-tool-call-badge"]')).toBeNull()
      // BrowserToolBlock's own chrome (tool name label + "Watch live" button) is present.
      expect(container.textContent).toContain('browser.screenshot')
      expect(container.textContent).toContain('Watch live')
    })

    it('still uses GenericToolCall for a non-browser tool (no regression to unrelated tools)', async () => {
      const assistantMsg: ChatMessage = {
        id: 'msg_assistant',
        role: 'assistant',
        content: 'Read the file.',
        timestamp: new Date().toISOString(),
        status: 'done',
        tool_calls: [
          { id: 'call_2', tool: 'read_file', params: { path: '/tmp/x' }, result: { text: 'contents' }, status: 'success' },
        ],
      }
      seedBucket([assistantMsg], { isStreaming: false })

      let container!: HTMLElement
      await act(async () => {
        const result = render(<ChatScreen />)
        container = result.container
      })

      expect(container.querySelector('[data-testid="generic-tool-call-badge"]')?.textContent).toBe('read_file')
    })
  })
})
