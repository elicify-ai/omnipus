// ChatScreen.outbound-queue.test.tsx — offline send queue (#105).
//
// Closes the last permanently-skipped e2e test (tests/e2e/chat.spec.ts "(f)
// queue-on-disconnect"). Investigation found the store-level queue mechanics
// (useChatStore's outboundQueue/pendingDrainQueue/enqueueOutboundMessage/
// drainOutboundQueue/maybeDrainNext — see src/store/chat.outbound-queue.test.ts
// and src/store/chat.mid-turn-send.test.ts) were already fully implemented
// and thoroughly tested, and the outbound-queue-indicator banner + reconnect
// banner + composerPlaceholder + button styling/aria-labels all ALREADY
// assumed the composer stays usable while reconnecting. But the actual
// `inputEnabled` gate in ChatScreen.tsx required strict `isConnected`,
// which is unconditionally false during the entire 'reconnecting'/'slow'
// phases — so the native `disabled` attribute silently blocked the ONLY
// real-UI path into `enqueueOutboundMessage()`, making the whole queue
// mechanism unreachable by an actual user despite being fully wired
// end-to-end. This file pins the corrected `inputEnabled` formula (mirrors
// composerPlaceholder's pre-existing `canSendOrQueue` argument) and the
// outbound-queue-indicator banner's rendering — the two pieces that make the
// feature real from the user's perspective, not just from the store's.
//
// Mock scaffolding copied from ChatScreen.mid-turn-send.test.tsx's precedent
// (real <form>/<textarea>/<button> DOM so the `disabled` prop is actually
// exercised, not just asserted against a mock's props object).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

import { OmnipusComposer } from './ChatScreen'

vi.mock('@assistant-ui/react', () => {
  return {
    useThreadViewportStore: () => ({ getState: () => ({ isAtBottom: true }) }),
    ThreadPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Viewport: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Messages: () => null,
    },
    MessagePrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Parts: () => null,
    },
    ComposerPrimitive: {
      Root: ({ children, className, onSubmit }: { children: React.ReactNode; className?: string; onSubmit?: (e: React.FormEvent) => void }) =>
        React.createElement('form', { className, onSubmit, 'data-testid': 'composer-form' }, children),
      Input: ({ disabled, placeholder, className, onChange, onKeyDown, onBlur }: {
        disabled?: boolean; placeholder?: string; className?: string;
        onChange?: (e: React.ChangeEvent<HTMLTextAreaElement>) => void;
        onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
        onBlur?: () => void;
      }) =>
        React.createElement('textarea', {
          disabled, placeholder, className,
          onChange, onKeyDown, onBlur,
          'data-testid': 'composer-input',
        }),
      Send: ({ disabled, children, className, 'data-testid': testId, 'aria-label': ariaLabel }: {
        disabled?: boolean; children?: React.ReactNode; className?: string;
        'data-testid'?: string; 'aria-label'?: string
      }) =>
        React.createElement('button', {
          type: 'button', disabled, className,
          'data-testid': testId ?? 'chat-send',
          'aria-label': ariaLabel,
        }, children),
      AddAttachment: ({ disabled, children, className, 'aria-label': ariaLabel }: {
        disabled?: boolean; children?: React.ReactNode; className?: string; 'aria-label'?: string
      }) =>
        React.createElement('button', { type: 'button', disabled, className, 'aria-label': ariaLabel, 'data-testid': 'add-attachment' }, children),
      Attachments: () => null,
    },
    AttachmentPrimitive: {
      Root: ({ children, className }: { children?: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Name: () => null,
      Remove: ({ children, className, 'aria-label': ariaLabel }: { children?: React.ReactNode; className?: string; 'aria-label'?: string }) =>
        React.createElement('button', { type: 'button', className, 'aria-label': ariaLabel }, children),
      Thumb: () => null,
    },
    MessagePartPrimitive: { InProgress: () => null },
    ActionBarPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Copy: ({ children }: { children: React.ReactNode }) => React.createElement('span', {}, children),
    },
    AuiIf: () => null,
    useComposerRuntime: vi.fn(() => ({
      getState: () => ({ text: '' }),
      setText: vi.fn(),
      addAttachment: vi.fn(),
      subscribe: vi.fn(() => vi.fn()),
      send: vi.fn(),
    })),
    useMessage: () => ({
      id: 'msg_1',
      role: 'assistant',
      status: { type: 'complete' },
      content: [],
    }),
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

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessionMessages: vi.fn().mockResolvedValue([]),
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
    fetchProviders: vi.fn().mockResolvedValue([]),
    uploadFiles: vi.fn(),
  }
})

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
vi.mock('./markdown-text', () => ({ MarkdownText: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))

function resetStores() {
  act(() => {
    useChatStore.setState({
      messages: [],
      isStreaming: false,
      isReplaying: false,
      toolCalls: {},
      sessionTokens: 0,
      sessionCost: 0,
      outboundQueue: [],
      pendingDrainQueue: [],
    })
    useConnectionStore.setState({
      connection: { send: vi.fn().mockReturnValue(true) } as any,
      isConnected: true,
      connectionError: null,
      reconnectPhase: null,
      reconnectAttempt: 0,
    })
    useSessionStore.setState({
      activeSessionId: 'sess_outbound_queue_ui_test',
      activeAgentId: 'general-assistant',
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)
// The reconnect-banner debounce (useSettledFlag) needs fake timers in the two
// tests that assert it; make sure they never leak into the rest of the file.
afterEach(() => {
  vi.useRealTimers()
})

describe('OmnipusComposer — composer usability while reconnecting (#105 offline send queue)', () => {
  it('reconnectPhase "reconnecting" (isConnected:false): textarea and Send stay ENABLED so a message can be queued', () => {
    vi.useFakeTimers()
    act(() => {
      useConnectionStore.setState({ isConnected: false, reconnectPhase: 'reconnecting', reconnectAttempt: 1 })
    })
    render(<OmnipusComposer />)

    expect(screen.getByTestId('composer-input')).not.toBeDisabled()
    expect(screen.getByTestId('chat-send')).not.toBeDisabled()
    // The banner is DEBOUNCED (useSettledFlag, 2s) as of 2026-07-31 so a
    // sub-second blip never paints a scary banner — it must therefore be
    // absent immediately and present only once the outage has persisted.
    // The composer staying usable is independent of that and is immediate.
    expect(screen.queryByTestId('reconnect-banner')).toBeNull()
    act(() => {
      vi.advanceTimersByTime(2100)
    })
    expect(screen.getByTestId('reconnect-banner')).toHaveTextContent(/Reconnecting…\s*\(attempt 1\)/)
  })

  it('reconnectPhase "slow" (isConnected:false): textarea and Send stay ENABLED so a message can be queued', () => {
    vi.useFakeTimers()
    act(() => {
      useConnectionStore.setState({ isConnected: false, reconnectPhase: 'slow', reconnectAttempt: 3 })
    })
    render(<OmnipusComposer />)

    expect(screen.getByTestId('composer-input')).not.toBeDisabled()
    expect(screen.getByTestId('chat-send')).not.toBeDisabled()
    expect(screen.queryByTestId('reconnect-banner')).toBeNull() // debounced — see above
    act(() => {
      vi.advanceTimersByTime(2100)
    })
    expect(screen.getByTestId('reconnect-banner')).toHaveTextContent(/slow retry/)
  })

  it('isConnected:false with reconnectPhase:null (just dropped / never yet connected): textarea and Send are DISABLED', () => {
    // This is the brief "Connecting to gateway..." state — before a retry
    // has even been scheduled. Unlike 'reconnecting'/'slow', there is no
    // active retry loop backing a promise that the message will ever be
    // dispatched, so this state is intentionally NOT treated as queueable.
    act(() => {
      useConnectionStore.setState({ isConnected: false, reconnectPhase: null })
    })
    render(<OmnipusComposer />)

    expect(screen.getByTestId('composer-input')).toBeDisabled()
    expect(screen.getByTestId('chat-send')).toBeDisabled()
  })

  it('reconnectPhase "gave_up": textarea and Send are DISABLED (no more automatic retries to queue against)', () => {
    act(() => {
      useConnectionStore.setState({ isConnected: false, reconnectPhase: 'gave_up', reconnectAttempt: 30 })
    })
    render(<OmnipusComposer />)

    expect(screen.getByTestId('composer-input')).toBeDisabled()
    expect(screen.getByTestId('chat-send')).toBeDisabled()
    expect(screen.getByTestId('reconnect-banner')).toHaveTextContent(/Connection lost after all retry attempts/)
  })

  it('isConnected:true, reconnectPhase:null (the normal case): textarea and Send are ENABLED and no reconnect banner renders', () => {
    render(<OmnipusComposer />)

    expect(screen.getByTestId('composer-input')).not.toBeDisabled()
    expect(screen.getByTestId('chat-send')).not.toBeDisabled()
    expect(screen.queryByTestId('reconnect-banner')).not.toBeInTheDocument()
  })
})

describe('OmnipusComposer — outbound-queue-indicator banner (#105 offline send queue)', () => {
  it('renders nothing when both queues are empty', () => {
    render(<OmnipusComposer />)
    expect(screen.queryByTestId('outbound-queue-indicator')).not.toBeInTheDocument()
  })

  it('shows the singular count when exactly one message is buffered in outboundQueue', () => {
    act(() => {
      useChatStore.setState({ outboundQueue: ['hello while offline'] })
      useConnectionStore.setState({ isConnected: false, reconnectPhase: 'reconnecting', reconnectAttempt: 1 })
    })
    render(<OmnipusComposer />)

    expect(screen.getByTestId('outbound-queue-indicator')).toHaveTextContent(
      '1 message queued — will send on reconnect',
    )
  })

  it('shows the plural, summed count when messages are split across outboundQueue and pendingDrainQueue', () => {
    act(() => {
      useChatStore.setState({ outboundQueue: ['b'], pendingDrainQueue: ['a'] })
      useConnectionStore.setState({ isConnected: true, reconnectPhase: null })
    })
    render(<OmnipusComposer />)

    expect(screen.getByTestId('outbound-queue-indicator')).toHaveTextContent(
      '2 messages queued — will send on reconnect',
    )
  })

  it('shows the "sending…" wording once the queue has fully moved to pendingDrainQueue (mid-drain)', () => {
    act(() => {
      useChatStore.setState({ outboundQueue: [], pendingDrainQueue: ['queued message'] })
      useConnectionStore.setState({ isConnected: true, reconnectPhase: null })
    })
    render(<OmnipusComposer />)

    expect(screen.getByTestId('outbound-queue-indicator')).toHaveTextContent(
      '1 queued message sending…',
    )
  })
})
