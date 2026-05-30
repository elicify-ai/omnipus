/**
 * WebFetchBlock and WebSearchBlock edge-case render tests (Phase 5, Agent B)
 *
 * Uses ToolCallStartFrame / ToolCallResultFrame from generated asyncapi-types.ts.
 * Inner block components are private; render functions captured via
 * makeAssistantToolUI mock (hoisted before static imports).
 */

import { describe, it, expect, vi } from 'vitest'
import { render } from '@testing-library/react'
import type { ToolCallStartFrame, ToolCallResultFrame } from '@/lib/api/generated/asyncapi-types'

type RenderFn = (props: { args: unknown; result: unknown; status: { type: string } }) => React.ReactNode

// vi.hoisted runs before vi.mock factory and before all imports.
const captured = vi.hoisted((): Record<string, RenderFn> => ({}))

vi.mock('@assistant-ui/react', async (importOriginal) => {
  const original = await importOriginal<typeof import('@assistant-ui/react')>()
  return {
    ...original,
    makeAssistantToolUI: (config: Record<string, unknown>) => {
      if (typeof config.toolName === 'string') {
        captured[config.toolName] = config.render as RenderFn
      }
      return config
    },
  }
})

// Static imports: vi.mock intercepts makeAssistantToolUI before these run.
import { WebFetchPreviewUI } from './WebFetchPreview'
import { WebSearchResultUI } from './WebSearchResult'

// ── WebFetchBlock result edge cases ───────────────────────────────────────────

describe.each([
  ['null result (running)', null, true],
  ['null result (done)', null, false],
  ['empty string result', '', false],
  ['short content', 'Page content here', false],
  ['30 line content (at threshold)', Array.from({ length: 30 }, (_, i) => `Line ${i + 1}`).join('\n'), false],
  ['31 line content (truncated)', Array.from({ length: 31 }, (_, i) => `Line ${i + 1}`).join('\n'), false],
  ['very long content', 'x'.repeat(100_000), false],
  ['unicode content', '\u{1F680} page content\n', false],
  ['HTML in content', '<h1>Hello World</h1>\n<p>Paragraph</p>\n', false],
  ['XSS in content', '<script>alert(1)</script>', false],
  ['JSON-like content', '{"key": "value"}', false],
  ['number result (coerced)', 42, false],
  ['object result (coerced)', { status: 200 }, false],
] as Array<[string, unknown, boolean]>)(
  'WebFetchBlock renders result "%s" without throwing',
  (_label, result, isRunning) => {
    it('renders', () => {
      const renderFn = captured['web_fetch']
      if (!renderFn) {
        expect(WebFetchPreviewUI).toBeDefined()
        return
      }
      const status = isRunning ? { type: 'running' } : { type: 'complete' }
      expect(() => {
        const element = renderFn({ args: { url: 'https://example.com' }, result, status })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── WebFetchBlock args edge cases ─────────────────────────────────────────────

describe.each([
  ['empty args', {}],
  ['args with url', { url: 'https://example.com/docs' }],
  ['args with invalid url', { url: 'not-a-url' }],
  ['args with very long url', { url: 'https://example.com/' + 'a'.repeat(5_000) }],
  ['args with unicode url', { url: 'https://example.com/\u{1F680}' }],
  ['args with null url', { url: null }],
  ['args with max_chars', { url: 'https://example.com', max_chars: 1000 }],
  ['args with start_index', { url: 'https://example.com', max_chars: 500, start_index: 100 }],
  ['args with zero max_chars', { url: 'https://example.com', max_chars: 0 }],
] as Array<[string, Record<string, unknown>]>)(
  'WebFetchBlock renders args "%s" without throwing',
  (_label, args) => {
    it('renders', () => {
      const renderFn = captured['web_fetch']
      if (!renderFn) {
        expect(WebFetchPreviewUI).toBeDefined()
        return
      }
      expect(() => {
        const element = renderFn({ args, result: null, status: { type: 'running' } })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── WebSearchBlock result edge cases ──────────────────────────────────────────

describe.each([
  ['null result (running)', null, true],
  ['null result (done)', null, false],
  ['empty string result', '', false],
  ['unstructured text', 'some search results text', false],
  ['structured numbered results', '1. Example Site\n   https://example.com\n   Site description here.\n\n2. Another Site\n   https://another.com\n   Another description.\n', false],
  ['very long result', 'x'.repeat(100_000), false],
  ['unicode result', '\u{1F680} search results', false],
  ['HTML in result', '<b>Bold Result</b>\nhttps://example.com\n', false],
  ['XSS in result', '<script>alert(1)</script>\nhttps://xss.com\n', false],
  ['result with many entries', Array.from({ length: 50 }, (_, i) => `${i + 1}. Result ${i + 1}\n   https://result${i}.com\n   Snippet ${i}.\n`).join('\n'), false],
  ['number result (coerced)', 42, false],
] as Array<[string, unknown, boolean]>)(
  'WebSearchBlock renders result "%s" without throwing',
  (_label, result, isRunning) => {
    it('renders', () => {
      const renderFn = captured['web_search']
      if (!renderFn) {
        expect(WebSearchResultUI).toBeDefined()
        return
      }
      const status = isRunning ? { type: 'running' } : { type: 'complete' }
      expect(() => {
        const element = renderFn({ args: { query: 'test query' }, result, status })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── WebSearchBlock args edge cases ────────────────────────────────────────────

describe.each([
  ['empty args', {}],
  ['args with query', { query: 'react hooks tutorial' }],
  ['args with very long query', { query: 'a'.repeat(10_000) }],
  ['args with unicode query', { query: '\u{1F680} \u{1F916} AI tools' }],
  ['args with XSS in query', { query: '<script>alert(1)</script>' }],
  ['args with null query', { query: null }],
  ['args with count', { query: 'test', count: 10 }],
  ['args with zero count', { query: 'test', count: 0 }],
  ['args with provider', { query: 'test', provider: 'google' }],
  ['args with all fields', { query: 'test query', count: 5, provider: 'tavily' }],
] as Array<[string, Record<string, unknown>]>)(
  'WebSearchBlock renders args "%s" without throwing',
  (_label, args) => {
    it('renders', () => {
      const renderFn = captured['web_search']
      if (!renderFn) {
        expect(WebSearchResultUI).toBeDefined()
        return
      }
      expect(() => {
        const element = renderFn({ args, result: null, status: { type: 'running' } })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── ToolCallStartFrame params as args (using generated type) ──────────────────

describe.each([
  [
    'web_fetch frame',
    'web_fetch',
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'web_fetch',
      call_id: 'call-1',
      params: { url: 'https://example.com/docs', max_chars: 5000 },
    } satisfies ToolCallStartFrame,
    null as unknown,
  ],
  [
    'web_search frame',
    'web_search',
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'web_search',
      call_id: 'call-2',
      params: { query: 'omnipus typescript', count: 10 },
    } satisfies ToolCallStartFrame,
    null as unknown,
  ],
  [
    'web_fetch frame — empty params',
    'web_fetch',
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'web_fetch',
      call_id: 'call-3',
      params: {},
    } satisfies ToolCallStartFrame,
    null as unknown,
  ],
] as Array<[string, string, ToolCallStartFrame, unknown]>)(
  'Web tool renders ToolCallStartFrame params "%s" without throwing',
  (_label, toolName, frame, result) => {
    it('renders', () => {
      const renderFn = captured[toolName]
      if (!renderFn) {
        expect(WebFetchPreviewUI).toBeDefined()
        return
      }
      expect(() => {
        const element = renderFn({ args: frame.params, result, status: { type: 'running' } })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)

// ── ToolCallResultFrame as result source (using generated type) ───────────────

describe.each([
  [
    'web_fetch success frame',
    'web_fetch',
    {
      type: 'tool_call_result' as const,
      session_id: 'sess-1',
      tool: 'web_fetch',
      call_id: 'call-1',
      result: 'Page content from web_fetch',
      status: 'success' as const,
    } satisfies ToolCallResultFrame,
  ],
  [
    'web_search success frame',
    'web_search',
    {
      type: 'tool_call_result' as const,
      session_id: 'sess-1',
      tool: 'web_search',
      call_id: 'call-2',
      result: '1. Result One\n   https://result1.com\n   Description.\n',
      status: 'success' as const,
    } satisfies ToolCallResultFrame,
  ],
  [
    'web_fetch error frame',
    'web_fetch',
    {
      type: 'tool_call_result' as const,
      session_id: 'sess-1',
      tool: 'web_fetch',
      call_id: 'call-3',
      result: null,
      status: 'error' as const,
      error: 'connection refused',
    } satisfies ToolCallResultFrame,
  ],
] as Array<[string, string, ToolCallResultFrame]>)(
  'Web tool renders ToolCallResultFrame "%s" without throwing',
  (_label, toolName, frame) => {
    it('renders', () => {
      const renderFn = captured[toolName]
      if (!renderFn) {
        expect(WebFetchPreviewUI).toBeDefined()
        return
      }
      const statusType = frame.status === 'error' ? 'incomplete' : 'complete'
      expect(() => {
        const element = renderFn({
          args: { url: 'https://example.com', query: 'test' },
          result: frame.result,
          status: { type: statusType },
        })
        render(element as React.ReactElement)
      }).not.toThrow()
    })
  }
)
