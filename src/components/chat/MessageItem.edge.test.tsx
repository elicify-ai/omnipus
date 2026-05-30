/**
 * MessageItem — edge-case render tests (Phase 5 agent C)
 *
 * Goal: no degenerate-but-valid payload should crash MessageItem.
 *
 * These tests are ADDITIVE — they cover payloads that were not tested by the
 * existing MessageItem.test.tsx happy-path suite. Every `describe.each` case
 * verifies only that the component renders without throwing, consistent with
 * Phase 5 mission: "no degenerate-but-valid payload should crash any covered
 * component."
 *
 * Fixture types come exclusively from:
 *   - src/store/chat.ts  (ChatMessage, SubagentSpan, SpanStep)
 *   - src/lib/api.ts     (Message, ToolCall)
 * No hand-written wire types are used.
 */

import { describe, it, expect, beforeEach } from 'vitest'
import { render } from '@testing-library/react'
import { act, type ReactElement } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MessageItem } from './MessageItem'
import { useChatStore } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import type { ToolCall } from '@/lib/api'

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeQueryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderWithQuery(ui: ReactElement) {
  return render(
    <QueryClientProvider client={makeQueryClient()}>{ui}</QueryClientProvider>
  )
}

beforeEach(() => {
  act(() => {
    useChatStore.setState({ toolCalls: {}, pendingApprovals: [] })
  })
})

/**
 * validBaseMessage is the minimal fully-valid assistant ChatMessage.
 * All edge-case describe.each cases spread overrides on top of this.
 *
 * Types come from ChatMessage (src/store/chat.ts) which extends Message
 * (src/lib/api.ts). No hand-written wire types.
 */
const validBaseMessage: ChatMessage = {
  id: 'msg_edge_1',
  session_id: 'sess_edge',
  role: 'assistant',
  content: 'Hello',
  timestamp: '2026-03-29T10:00:00Z',
  status: 'done',
}

// ── describe.each: content edge cases ────────────────────────────────────────

describe.each([
  ['empty content', { content: '' }],
  ['very long content (50KB)', { content: 'x'.repeat(50_000) }],
  ['unicode-heavy content', { content: '🚀'.repeat(1000) }],
  [
    'markdown with deeply nested lists',
    { content: '- a\n  - b\n    - c\n      - d\n        - e' },
  ],
  ['code fence with no language', { content: '```\nplain\n```' }],
  ['code fence with unknown language', { content: '```xyzlang\ncode\n```' }],
  // HTML entities in content must be escaped — no script tag should survive
  [
    'HTML in content (must be escaped)',
    { content: "<script>alert('xss')</script>" },
  ],
  ['mixed RTL/LTR', { content: 'english עברית english' }],
])(
  'MessageItem renders assistant message with %s without throwing',
  (_label: string, overrides: Partial<ChatMessage>) => {
    it('renders without throwing', () => {
      const props: ChatMessage = { ...validBaseMessage, ...overrides }
      expect(() => renderWithQuery(<MessageItem message={props} />)).not.toThrow()
    })
  }
)

// ── describe.each: user message content edge cases ───────────────────────────

describe.each([
  ['empty user content', { role: 'user' as const, content: '' }],
  ['very long user content (50KB)', { role: 'user' as const, content: 'y'.repeat(50_000) }],
  [
    'user message with HTML (must be escaped)',
    { role: 'user' as const, content: '<img src=x onerror=alert(1)>' },
  ],
  [
    'user message with newlines',
    { role: 'user' as const, content: 'line1\nline2\nline3' },
  ],
])(
  'MessageItem renders user message with %s without throwing',
  (_label: string, overrides: Partial<ChatMessage>) => {
    it('renders without throwing', () => {
      const props: ChatMessage = { ...validBaseMessage, ...overrides }
      expect(() => renderWithQuery(<MessageItem message={props} />)).not.toThrow()
    })
  }
)

// ── describe.each: timestamp edge cases ──────────────────────────────────────

describe.each([
  ['malformed timestamp', { timestamp: 'invalid-date' }],
  ['future timestamp', { timestamp: new Date(Date.now() + 86_400_000).toISOString() }],
  ['epoch timestamp', { timestamp: '1970-01-01T00:00:00Z' }],
  ['empty timestamp string', { timestamp: '' }],
  // Date.parse returns NaN for the below — must not throw
  ['null-ish timestamp (empty)', { timestamp: '0000-00-00T00:00:00Z' }],
])(
  'MessageItem renders with %s without throwing',
  (_label: string, overrides: Partial<ChatMessage>) => {
    it('renders without throwing', () => {
      const props: ChatMessage = { ...validBaseMessage, ...overrides }
      expect(() => renderWithQuery(<MessageItem message={props} />)).not.toThrow()
    })
  }
)

// ── describe.each: tool_calls field edge cases ────────────────────────────────
//
// MessageItem uses: message.tool_calls?.map((tc) => toolCalls[tc.id]).filter(Boolean) ?? []
// Valid values for tool_calls: undefined, empty array, array with items.
// null is NOT in the ChatMessage interface (tool_calls?: ToolCall[]) but
// runtime payloads from the wire could carry null — we test the guard.

describe.each([
  ['empty tool_calls array', { tool_calls: [] as ToolCall[] }],
  ['one tool call with empty args', {
    tool_calls: [
      { id: 'tc_1', tool: 'x', params: {}, status: 'success' as const },
    ] as ToolCall[],
  }],
  ['tool calls with undefined id in store (lookup miss)', {
    // store has no matching tool call for id 'orphan' — filter(Boolean) guards this
    tool_calls: [
      { id: 'orphan', tool: 'fs.list', params: {}, status: 'running' as const },
    ] as ToolCall[],
  }],
])(
  'MessageItem renders with %s without throwing',
  (_label: string, overrides: Partial<ChatMessage>) => {
    it('renders without throwing', () => {
      const props: ChatMessage = { ...validBaseMessage, ...overrides }
      expect(() => renderWithQuery(<MessageItem message={props} />)).not.toThrow()
    })
  }
)

// ── describe.each: status edge cases ─────────────────────────────────────────

describe.each([
  ['status=streaming with content', { status: 'streaming' as const, isStreaming: true, content: 'partial text' }],
  ['status=error', { status: 'error' as const }],
  ['status=interrupted', { status: 'interrupted' as const }],
  ['status=done', { status: 'done' as const }],
  ['status=undefined', { status: undefined }],
])(
  'MessageItem renders with %s without throwing',
  (_label: string, overrides: Partial<ChatMessage>) => {
    it('renders without throwing', () => {
      const props: ChatMessage = { ...validBaseMessage, ...overrides }
      expect(() => renderWithQuery(<MessageItem message={props} />)).not.toThrow()
    })
  }
)

// ── XSS regression: no <script> tag in DOM ───────────────────────────────────

it('HTML script tag in assistant content does not produce a <script> element', () => {
  const props: ChatMessage = {
    ...validBaseMessage,
    role: 'assistant',
    content: "<script>window.__xss = true</script>",
  }
  renderWithQuery(<MessageItem message={props} />)
  expect(document.querySelectorAll('script')).toHaveLength(0)
})

it('HTML script tag in user content does not produce a <script> element', () => {
  const props: ChatMessage = {
    ...validBaseMessage,
    role: 'user',
    content: "<script>window.__xss = true</script>",
  }
  renderWithQuery(<MessageItem message={props} />)
  expect(document.querySelectorAll('script')).toHaveLength(0)
})

// ── javascript: URL in markdown link is blocked ───────────────────────────────

it('javascript: URL in assistant markdown link is not rendered as an <a> href', () => {
  const props: ChatMessage = {
    ...validBaseMessage,
    role: 'assistant',
    content: "[click me](javascript:alert('xss'))",
  }
  renderWithQuery(<MessageItem message={props} />)
  // The custom `a` renderer blocks javascript: URLs — the link must have no href
  // or the href is stripped. Check no <a> with javascript: scheme is present.
  const links = document.querySelectorAll('a[href^="javascript:"]')
  expect(links).toHaveLength(0)
})

// ── agentId with no matching agent (lookup miss) ──────────────────────────────

it('renders without throwing when agentId does not match any agent', () => {
  const props: ChatMessage = {
    ...validBaseMessage,
    role: 'assistant',
    agentId: 'nonexistent-agent-id',
  }
  // The agents query returns [] (no matching agent). Falls back to agentId as label.
  expect(() => renderWithQuery(<MessageItem message={props} />)).not.toThrow()
})

// ── system message edge cases ─────────────────────────────────────────────────

describe.each([
  ['empty system content', { role: 'system' as const, content: '' }],
  ['very long system content', { role: 'system' as const, content: 'z'.repeat(10_000) }],
  ['system content with HTML', { role: 'system' as const, content: '<b>bold</b>' }],
])(
  'MessageItem renders system message with %s without throwing',
  (_label: string, overrides: Partial<ChatMessage>) => {
    it('renders without throwing', () => {
      const props: ChatMessage = { ...validBaseMessage, ...overrides }
      expect(() => renderWithQuery(<MessageItem message={props} />)).not.toThrow()
    })
  }
)
