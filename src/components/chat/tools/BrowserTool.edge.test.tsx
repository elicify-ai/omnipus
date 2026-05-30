/**
 * BrowserToolBlock and BrowserNavigateBlock edge-case render tests (Phase 5, Agent B)
 *
 * Covers degenerate-but-valid inputs to browser tool components. Uses
 * ToolCallStartFrame / ToolCallResultFrame from generated asyncapi-types.ts
 * to build base props where applicable.
 */

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { BrowserToolBlock } from './BrowserTool'
import { Globe } from '@phosphor-icons/react'
import type { ToolCallStartFrame, ToolCallResultFrame } from '@/lib/api/generated/asyncapi-types'

// ── result edge cases for BrowserToolBlock ────────────────────────────────────

describe.each([
  ['null result', null],
  ['undefined result', undefined],
  ['empty string result', ''],
  ['plain string result', 'page loaded'],
  ['JSON string with screenshot', JSON.stringify({ screenshot: 'base64data', text: 'hello' })],
  ['JSON string with error', JSON.stringify({ error: 'element not found' })],
  ['JSON string with evaluate result', JSON.stringify({ result: 42 })],
  ['malformed JSON string', '{not valid json}'],
  ['very long string result', 'x'.repeat(50_000)],
  ['unicode string result', '\u{1F680}\u{1F480}⚡'],
  ['object result with screenshot', { screenshot: 'base64data', text: 'page text' }],
  ['object result with error', { error: 'timeout waiting for element' }],
  ['object result — empty', {}],
  ['object result — null screenshot', { screenshot: null }],
  ['object result — deeply nested', { a: { b: { c: { d: 'deep' } } } }],
  ['number result', 42],
  ['boolean result', true],
  ['array result', [1, 2, 3]],
] as Array<[string, unknown]>)(
  'BrowserToolBlock renders result "%s" without throwing',
  (_label, result) => {
    it('renders', () => {
      expect(() =>
        render(
          <BrowserToolBlock
            toolName="browser.click"
            icon={Globe}
            args={{}}
            result={result}
            status={{ type: 'complete' }}
            summary="(no selector)"
          />
        )
      ).not.toThrow()
    })
  }
)

// ── args edge cases ───────────────────────────────────────────────────────────

describe.each([
  ['empty args', {}],
  ['args with selector', { selector: '#btn' }],
  ['args with very long selector', { selector: '#' + 'a'.repeat(10_000) }],
  ['args with null selector', { selector: null }],
  ['args with numeric selector', { selector: 123 }],
  ['args with XSS selector', { selector: '<script>alert(1)</script>' }],
  ['args with unicode selector', { selector: '\u{1F680}.button' }],
  ['args with nested object', { a: { b: { c: 'deep' } } }],
  ['args with array value', { list: [1, 2, 3] }],
] as Array<[string, Record<string, unknown>]>)(
  'BrowserToolBlock renders args "%s" without throwing',
  (_label, args) => {
    it('renders', () => {
      expect(() =>
        render(
          <BrowserToolBlock
            toolName="browser.click"
            icon={Globe}
            args={args}
            result={null}
            status={{ type: 'running' }}
            summary="test"
          />
        )
      ).not.toThrow()
    })
  }
)

// ── status variants ───────────────────────────────────────────────────────────

describe.each([
  ['running', { type: 'running' } as { type: string }, null],
  ['complete', { type: 'complete' } as { type: string }, { text: 'result' }],
  ['incomplete/error', { type: 'incomplete' } as { type: string }, null],
] as Array<[string, { type: string }, unknown]>)(
  'BrowserToolBlock renders status "%s" without throwing',
  (_label, status, result) => {
    it('renders', () => {
      expect(() =>
        render(
          <BrowserToolBlock
            toolName="browser.screenshot"
            icon={Globe}
            args={{ selector: '#main' }}
            result={result}
            status={status}
            summary="#main"
          />
        )
      ).not.toThrow()
    })
  }
)

// ── summary edge cases ────────────────────────────────────────────────────────

describe.each([
  ['empty summary', ''],
  ['very long summary', 'a'.repeat(10_000)],
  ['unicode summary', '\u{1F680} element'],
  ['XSS summary', '<script>alert(1)</script>'],
  ['multiline summary (display-only)', 'line1\nline2'],
] as Array<[string, string]>)(
  'BrowserToolBlock renders summary "%s" without throwing',
  (_label, summary) => {
    it('renders', () => {
      expect(() =>
        render(
          <BrowserToolBlock
            toolName="browser.evaluate"
            icon={Globe}
            args={{}}
            result={null}
            status={{ type: 'running' }}
            summary={summary}
          />
        )
      ).not.toThrow()
    })
  }
)

// ── ToolCallStartFrame params as args (using generated type) ──────────────────

describe.each([
  [
    'browser.click frame params',
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'browser.click',
      call_id: 'call-1',
      params: { selector: '#submit-btn' },
    } satisfies ToolCallStartFrame,
    'complete' as const,
    { text: 'clicked' } as unknown,
  ],
  [
    'browser.screenshot frame params',
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'browser.screenshot',
      call_id: 'call-2',
      params: { selector: undefined },
    } satisfies ToolCallStartFrame,
    'running' as const,
    null as unknown,
  ],
  [
    'browser.evaluate frame params',
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'browser.evaluate',
      call_id: 'call-3',
      params: { expression: 'document.title' },
    } satisfies ToolCallStartFrame,
    'complete' as const,
    { result: 'My Page' } as unknown,
  ],
] as Array<[string, ToolCallStartFrame, 'complete' | 'running' | 'incomplete', unknown]>)(
  'BrowserToolBlock renders ToolCallStartFrame params "%s" without throwing',
  (_label, frame, statusType, result) => {
    it('renders', () => {
      expect(() =>
        render(
          <BrowserToolBlock
            toolName={frame.tool}
            icon={Globe}
            args={frame.params as Record<string, unknown>}
            result={result}
            status={{ type: statusType }}
            summary={String((frame.params as Record<string, unknown>).selector ?? frame.tool)}
          />
        )
      ).not.toThrow()
    })
  }
)

// ── ToolCallResultFrame as result source (using generated type) ───────────────

describe.each([
  [
    'success result frame',
    {
      type: 'tool_call_result' as const,
      session_id: 'sess-1',
      tool: 'browser.click',
      call_id: 'call-1',
      result: { text: 'ok' },
      status: 'success' as const,
    } satisfies ToolCallResultFrame,
  ],
  [
    'error result frame',
    {
      type: 'tool_call_result' as const,
      session_id: 'sess-1',
      tool: 'browser.navigate',
      call_id: 'call-2',
      result: null,
      status: 'error' as const,
      error: 'navigation timeout',
    } satisfies ToolCallResultFrame,
  ],
  [
    'success result frame with null result',
    {
      type: 'tool_call_result' as const,
      session_id: 'sess-1',
      tool: 'browser.type',
      call_id: 'call-3',
      result: null,
      status: 'success' as const,
    } satisfies ToolCallResultFrame,
  ],
] as Array<[string, ToolCallResultFrame]>)(
  'BrowserToolBlock renders ToolCallResultFrame "%s" without throwing',
  (_label, frame) => {
    it('renders', () => {
      const statusType = frame.status === 'error' ? 'incomplete' : 'complete'
      expect(() =>
        render(
          <BrowserToolBlock
            toolName={frame.tool}
            icon={Globe}
            args={{}}
            result={frame.result}
            status={{ type: statusType }}
            summary={frame.tool}
          />
        )
      ).not.toThrow()
    })
  }
)

// ── Positive invariants ───────────────────────────────────────────────────────
//
// Verify that the tool name and summary are always visible in the rendered DOM.

it('renders toolName as visible text', () => {
  render(
    <BrowserToolBlock
      toolName="browser.navigate"
      icon={Globe}
      args={{ url: 'https://example.com' }}
      result={null}
      status={{ type: 'running' }}
      summary="https://example.com"
    />
  )
  expect(screen.getByText('browser.navigate')).toBeInTheDocument()
  expect(screen.getByText('https://example.com')).toBeInTheDocument()
})

it('null result with complete status renders without showing a <script> tag (XSS regression)', () => {
  render(
    <BrowserToolBlock
      toolName="browser.evaluate"
      icon={Globe}
      args={{ script: '<script>alert(1)</script>' }}
      result={'<script>window.__xss=true</script>'}
      status={{ type: 'complete' }}
      summary="evaluate"
    />
  )
  // No injected <script> elements in the DOM
  expect(document.querySelectorAll('script')).toHaveLength(0)
})
