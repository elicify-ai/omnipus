/**
 * WebServeUI.serve-web.test.tsx — reachability guard for the backend-canonical
 * `serve_web` tool name (ADR-094 preview isolation, squad fix8; #798 UI
 * reachability gap).
 *
 * The gap (verified by reading, squad lead 2026-09-28): the backend registers
 * the agent-facing tool as `serve_web` (pkg/tools/web_serve.go::
 * ToolNameWebServe), but the SPA only registered the legacy alias `web_serve`
 * — so a real serve_web result fell to the generic badge and never rendered
 * the ADR-094 preview card or its Mode 1 isolated link (US-8 / S-8.x).
 *
 * Two surfaces key tool rendering on the name; this file covers both:
 *   1. LIVE — the makeAssistantToolUI registration in WebServeUI.tsx (what
 *      OmnipusRuntimeProvider mounts). Mirrors WebServeUI.live-wiring.test.tsx:
 *      makeAssistantToolUI is intercepted to capture each registration, and
 *      the serve_web render closure is invoked directly.
 *   2. REPLAY — ChatScreen.tsx's VirtualAssistantMessageRow hardcoded
 *      web_serve/serve_workspace/run_in_workspace branch (a reloaded session
 *      renders outside AssistantUI's registry). Mirrors
 *      ChatScreen.issue-617-replay-outcome.test.tsx (full ChatScreen render,
 *      PlainMessageList fallback) — but IframePreview is left REAL (that file
 *      mocks it to null) because the assertion here IS the Mode 1 link, so
 *      the Chromium engine stub and window.location fixture are copied from
 *      IframePreview.preview-isolation.test.tsx (S-8.1).
 *
 * PINS (must stay green before AND after the fix): the three legacy aliases
 * stay registered, and a replayed web_serve result carrying isolated_url
 * still renders the preview card and the Mode 1 link.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act, screen } from '@testing-library/react'
import * as React from 'react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage, PositionedToolCall } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'

// ── makeAssistantToolUI capture (live registration surface) ─────────────────

type ServeRenderFn = (props: {
  args: unknown
  result: unknown
  status: { type: string; reason?: string }
  isError?: boolean
}) => React.ReactNode

const captured = vi.hoisted((): Record<string, ServeRenderFn> => ({}))

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
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Name: () => null,
      Remove: ({ children, className }: { children: React.ReactNode; className?: string }) =>
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
      subscribe: vi.fn(() => vi.fn()),
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
    // Capture every factory registration (WebServeUI.tsx module scope) so the
    // LIVE surface can be asserted without mounting the provider.
    makeAssistantToolUI: (config: { toolName?: string; render?: unknown }) => {
      if (typeof config.toolName === 'string') {
        captured[config.toolName] = config.render as ServeRenderFn
      }
      return () => null
    },
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

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessionMessages: vi.fn().mockResolvedValue([]),
    fetchAboutInfo: vi.fn().mockResolvedValue({ preview_port: 5001 }),
    createSession: vi.fn(),
    uploadFiles: vi.fn(),
    fetchProviders: vi.fn().mockResolvedValue([]),
    isApiError: vi.fn().mockReturnValue(false),
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
  }
})

vi.mock('../historical-markdown', () => ({
  HistoricalMessageMarkdown: ({ content }: { content: string }) =>
    React.createElement('div', { 'data-testid': 'historical-markdown' }, content),
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('../RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
// IframePreview deliberately LEFT REAL — the Mode 1 link assertion needs it.
// engine stub + location fixture below make its FR-024 selection deterministic.
vi.mock('../markdown-text', () => ({
  MarkdownText: () => React.createElement('div', {}),
}))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
vi.mock('../composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('../composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('../composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('@/lib/memory-observer', () => ({
  startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }),
  addMemoryObserver: () => () => {},
  getCurrentSnapshot: () => ({ usedJSHeapSizeBytes: null, level: 'ok', supported: false }),
}))

// Static imports — vi.mock intercepts before these run. Importing WebServeUI
// executes its module-scope registrations; importing ChatScreen transitively
// imports WebServeBlock from the same module.
import { WebServeUI } from './WebServeUI'
import { ServeWorkspaceUI } from './ServeWorkspaceUI'
import { RunInWorkspaceUI } from './RunInWorkspaceUI'
import { ChatScreen } from '../ChatScreen'

// ── Fixtures (IframePreview.preview-isolation.test.tsx) ──────────────────────

const MODE2_URL = 'http://localhost:5000/preview/mia/tok-abc123/'
const ISOLATED_URL = 'http://myapp.localhost:5000/'
const EXPIRES = () => new Date(Date.now() + 3600_000).toISOString()

const ORIGINAL_UAD = Object.getOwnPropertyDescriptor(
  Object.getPrototypeOf(navigator),
  'userAgentData',
)
const ORIGINAL_UA = navigator.userAgent

beforeEach(() => {
  // FR-024 Mode 1 selection: a Chromium engine (userAgentData) + the SPA's
  // gateway origin, so a static result with isolated_url renders exactly one
  // preview-link whose href is the isolated URL.
  Object.defineProperty(navigator, 'userAgentData', {
    configurable: true,
    value: {
      brands: [
        { brand: 'Not.A/Brand', version: '99' },
        { brand: 'Chromium', version: '130' },
      ],
      mobile: false,
      platform: 'macOS',
    },
  })
  Object.defineProperty(window, 'location', {
    value: { hostname: 'localhost', protocol: 'http:', origin: 'http://localhost:5000', port: '5000' },
    writable: true,
  })
})

afterEach(() => {
  if (ORIGINAL_UAD) {
    Object.defineProperty(Object.getPrototypeOf(navigator), 'userAgentData', ORIGINAL_UAD)
  } else {
    Reflect.deleteProperty(navigator, 'userAgentData')
  }
  Object.defineProperty(navigator, 'userAgent', {
    configurable: true,
    value: ORIGINAL_UA,
  })
})

// ── Replay scaffolding (ChatScreen.issue-617-replay-outcome.test.tsx) ────────

const SID = 'test-session-serve-web-reachability'

function seedBucket(messages: ChatMessage[]): void {
  const bucket = makeBucketMessages(messages)
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
    messages,
    isStreaming: false,
    isReplaying: false,
    replayCompletedForSession: SID,
  }))
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
  vi.unstubAllGlobals()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ;(globalThis as any).ResizeObserver = undefined

  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
  act(() => {
    useChatStore.getState().resetSession()
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

const SERVE_RESULT = {
  kind: 'static' as const,
  url: MODE2_URL,
  path: 'elicify-hello',
  expires_at: EXPIRES(),
  isolated_url: ISOLATED_URL,
}

function seedServeTranscript(tool: string, callId: string): void {
  const assistantMsg: ChatMessage = {
    id: `msg_${callId}`,
    role: 'assistant',
    content: '',
    timestamp: new Date().toISOString(),
    status: 'done',
    tool_calls: [
      {
        id: callId,
        tool,
        params: { path: 'elicify-hello' },
        result: SERVE_RESULT,
        status: 'success',
      } as PositionedToolCall,
    ],
  }
  seedBucket([assistantMsg])
}

async function renderChatScreen(): Promise<void> {
  await act(async () => {
    render(<ChatScreen />)
  })
}

function expectPreviewCardWithMode1Link(label: string): void {
  // The preview card — WebServeBlock's own header, not the generic badge.
  expect(screen.getByTestId('webserve-tool-header')).toBeInTheDocument()
  expect(screen.getByText(label)).toBeInTheDocument()
  // Mode 1 (ADR-094): exactly one link, the isolated *.localhost href.
  const links = screen.getAllByTestId('preview-link')
  expect(links).toHaveLength(1)
  expect(links[0]).toHaveAttribute('href', ISOLATED_URL)
  // The generic raw-JSON badge is NOT the renderer for this call.
  expect(screen.queryByTestId('tool-call-badge')).toBeNull()
}

// ── LIVE surface ─────────────────────────────────────────────────────────────

describe('serve_web live registration (WebServeUI.tsx factory)', () => {
  it('registers a render function for the canonical "serve_web" name', () => {
    expect(captured['serve_web']).toBeDefined()
  })

  it('pins: the legacy aliases stay registered for old transcripts', () => {
    expect(captured['web_serve']).toBeDefined()
    expect(captured['serve_workspace']).toBeDefined()
    expect(captured['run_in_workspace']).toBeDefined()
    expect(WebServeUI).toBeDefined()
    expect(ServeWorkspaceUI).toBeDefined()
    expect(RunInWorkspaceUI).toBeDefined()
  })

  it('the serve_web render closure renders the preview card and the Mode 1 link', () => {
    const renderFn = captured['serve_web']
    expect(renderFn).toBeDefined()
    render(
      renderFn!({
        args: { path: 'elicify-hello' },
        result: SERVE_RESULT,
        status: { type: 'complete' },
        isError: false,
      }) as React.ReactElement,
    )
    expectPreviewCardWithMode1Link('serve_web')
  })
})

// ── REPLAY surface (ChatScreen transcript branch) ────────────────────────────

describe('ChatScreen replay wiring — serve_web branch (WebServeBlock)', () => {
  it('a replayed serve_web result carrying isolated_url renders the preview card and the Mode 1 link, not the generic badge', async () => {
    seedServeTranscript('serve_web', 'tc_serve_web_replay')
    await renderChatScreen()
    expectPreviewCardWithMode1Link('serve_web')
  })

  it('pins: a replayed legacy web_serve result carrying isolated_url still renders the preview card and the Mode 1 link', async () => {
    seedServeTranscript('web_serve', 'tc_web_serve_replay')
    await renderChatScreen()
    expectPreviewCardWithMode1Link('web_serve')
  })
})
