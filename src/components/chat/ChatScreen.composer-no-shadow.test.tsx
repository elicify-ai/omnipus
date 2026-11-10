// ChatScreen composer no-shadow lock (founder UI feedback 2026-10-11,
// point 5 — triaged with the chief: the composer container's visible top
// edge came from a shadow; REMOVE THE SHADOW ENTIRELY, keep the full-width
// container, keep borders and the focus-within accent cue).
//
// This file pins the resulting contract: the composer card AND its wrapper
// chain (composer root → centered max-width wrapper → full-width wrapper)
// carry NO shadow utility and NO elevation token, while the card keeps its
// border and the focus-within accent border (design-system rule 7's
// sanctioned replacement cue). If a shadow ever reappears on the container
// or any wrapper, this file goes red.
//
// Scaffolding (mocks for @assistant-ui/react, @tanstack/react-query,
// @tanstack/react-router, @/lib/api, the SVG asset, and the deep child
// components) is copied verbatim from ChatScreen.composer-card.test.tsx's
// OmnipusComposer mount setup rather than invented fresh, per the sibling
// file's precedent.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'

// Static import: vi.mock() calls are hoisted before this import, so all mocks
// are in place when the module resolves (see ChatScreen.composer-card.test.tsx
// for the rationale).
import { OmnipusComposer } from './ChatScreen'

// ── Mocks (verbatim from ChatScreen.composer-card.test.tsx) ──────────────────

import { MockButton, MockTextarea } from '@/test/assistantUiMock'
vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock({
  ThreadPrimitive: {
    Viewport: ({ children, className }: { children: React.ReactNode; className?: string }) =>
              React.createElement('div', { className }, children),
  },
  ComposerPrimitive: {
    Input: ({ disabled, placeholder, className }: { disabled?: boolean; placeholder?: string; className?: string }) =>
              React.createElement(MockTextarea, { disabled, placeholder, className, 'data-testid': 'composer-input'}),
    Send: ({ disabled, children, className, 'data-testid': testId, 'aria-label': ariaLabel, 'aria-disabled': ariaDisabled }: {
              disabled?: boolean; children?: React.ReactNode; className?: string;
              'data-testid'?: string; 'aria-label'?: string; 'aria-disabled'?: boolean | string
            }) =>
              React.createElement(MockButton, {
                type: 'button',
                disabled,
                className,
                'data-testid': testId ?? 'chat-send',
                'aria-label': ariaLabel,
                'aria-disabled': ariaDisabled, children: children}),
  },
  ActionBarPrimitive: {
    Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
              React.createElement('div', { className }, children),
  },
  useMessage: () => ({
          id: 'msg_1',
          role: 'assistant',
          status: { type: 'complete' },
          content: [],
        }),
}))

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: (opts: { queryKey?: readonly unknown[] }) => ({ data: queryDataWithEmptyHistory(opts?.queryKey), isError: false, refetch: vi.fn() }),
    useMutation: () => ({ mutate: vi.fn(), isPending: false }),
    useQueryClient: () => ({ invalidateQueries: vi.fn(), removeQueries: vi.fn() }),
  }
})

vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

import { emptyHistoryPage, queryDataWithEmptyHistory, type HistoryReadPort } from '../../../tests/chat-history-paging/legacy-fixtures'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessionMessages: vi.fn<HistoryReadPort>(async (sessionId) => emptyHistoryPage(sessionId)),
    createSession: vi.fn(),
    uploadFiles: vi.fn(),
    fetchProviders: vi.fn().mockResolvedValue([]),
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
  }
})

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))

vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./markdown-text', () => ({ MarkdownText: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))

vi.mock('./composer/ModelPicker', () => ({
  ModelPicker: ({ disabled }: { disabled?: boolean }) => (
    <div data-testid="model-picker-stub" data-disabled={disabled ? 'true' : 'false'} />
  ),
}))
vi.mock('./composer/TokenCounter', () => ({
  TokenCounter: ({ className }: { className?: string }) => (
    <div data-testid="token-counter-stub" data-cls={className} />
  ),
}))
vi.mock('./ActivityBar', () => ({ ActivityBar: () => <div data-testid="activity-bar-stub" /> }))

// ── Store reset (verbatim from ChatScreen.composer-card.test.tsx) ─────────────

function resetStores() {
  act(() => {
    useChatStore.setState({
      messages: [],
      isStreaming: false,
      isReplaying: false,
      toolCalls: {},
      sessionTokens: 0,
      sessionCost: 0,
    })
    useConnectionStore.setState({
      connection: null,
      isConnected: true,
      connectionError: null,
    })
    useSessionStore.setState({
      activeSessionId: 'sess_composer_no_shadow_test',
      activeAgentId: 'general-assistant',
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)

// ── No-shadow contract ────────────────────────────────────────────────────────

describe('OmnipusComposer — composer container carries no shadow (founder feedback 2026-10-11 point 5)', () => {
  it('renders the card and its wrapper chain with no shadow utility or elevation token, borders and focus cue kept', () => {
    render(<OmnipusComposer />)

    const card = screen.getByTestId('composer-card')

    // The card itself: no shadow utility (shadow-*, drop-shadow-*) and no
    // elevation token anywhere in its className.
    expect(card.className).not.toMatch(/shadow/i)
    expect(card.className).not.toMatch(/elevation/i)

    // ...and the same on every wrapper above it, up to and including the
    // composer root (the full-width container the feedback point is about):
    // composer root (@container) → centered max-width wrapper → full-width
    // wrapper. Walk three ancestors explicitly so a shadow reintroduced on
    // ANY surface in the chain is caught, not only on the card.
    const surfaces: HTMLElement[] = [card]
    let cursor: HTMLElement | null = card
    for (let i = 0; i < 3 && cursor?.parentElement; i++) {
      cursor = cursor.parentElement as HTMLElement
      surfaces.push(cursor)
    }
    for (const surface of surfaces) {
      expect(surface.className).not.toMatch(/shadow/i)
      expect(surface.className).not.toMatch(/elevation/i)
    }

    // The borders stay: the card's own border AND the focus-within accent
    // swap (design-system rule 7 — the textarea's sanctioned replacement
    // focus cue lives on this card; removing the shadow must not take the
    // cue with it).
    expect(card.className).toContain('border')
    expect(card.className).toContain('focus-within:border-[var(--color-accent)]')
  })
})
