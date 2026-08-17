/**
 * WebFetchBlock and WebSearchBlock edge-case render tests (Phase 5, Agent B)
 *
 * Uses ToolCallStartFrame / ToolCallResultFrame from generated asyncapi-types.ts.
 * Inner block components are private; render functions captured via
 * makeAssistantToolUI mock (hoisted before static imports).
 */

import { describe, it, expect, vi } from 'vitest'
import { render, fireEvent } from '@testing-library/react'
import type { ToolCallStartFrame, ToolCallResultFrame } from '@/lib/api/generated/asyncapi-types'

// Issue #617: `isError` is now a real field on the render-prop object (set
// by omnipus-runtime.ts from the store's resolved ToolCall.status) — it is
// NOT derivable from `status.type === 'incomplete'` any more, since that can
// never be true for a finished call carrying a result.
type RenderFn = (props: { args: unknown; result: unknown; status: { type: string; reason?: string }; isError?: boolean }) => React.ReactNode

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

// ── flat text-line status dot (ticket "Tool components in chat", P2) ────────
// The old bordered/backgrounded cards were replaced by a flat text line whose
// only status color comes from an 8px dot (running keeps the spinning icon in
// the same slot) — see GenericToolCall.tsx/toolStatusConfig.tsx for the
// reference language this mirrors. WebFetchBlock/WebSearchBlock have no
// data-testid on their root, so these assertions use `container` queries.

describe('WebFetchBlock — flat text-line status dot', () => {
  function renderFetch(result: unknown, statusType: string, url = 'https://example.com', isError?: boolean) {
    const renderFn = captured['web_fetch']
    return render(renderFn({ args: { url }, result, status: { type: statusType }, isError }) as React.ReactElement)
  }

  it('running: indicator is the spinning icon, not a dot', () => {
    const { container } = renderFetch(null, 'running')
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.tagName.toLowerCase()).toBe('svg')
    expect(indicator?.getAttribute('class')).toContain('animate-spin')
  })

  it('success: indicator is an 8px success-colored dot', () => {
    const { container } = renderFetch('page content', 'complete')
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.tagName.toLowerCase()).toBe('span')
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
    expect(indicator?.getAttribute('class')).toContain('rounded-full')
  })

  it('isError=true: indicator is an 8px error-colored dot with a "Failed" label — not a green dot, not silent', () => {
    // Issue #617: producible pairing — a finished fetch always carries a
    // truthy `result` in production, so `status.type === 'incomplete'`
    // alone (the old shape here) never occurs; `isError` is the real signal.
    const { container, getByText } = renderFetch(null, 'complete', undefined, true)
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-error)]')
    expect(indicator?.getAttribute('class')).not.toContain('bg-[var(--color-success)]')
    expect(getByText('Failed')).toBeInTheDocument()
  })

  it('cancelled: indicator is an 8px muted-colored dot with a "Cancelled" label', () => {
    const renderFn = captured['web_fetch']
    const { container, getByText } = render(
      renderFn({
        args: { url: 'https://example.com' },
        result: null,
        status: { type: 'incomplete', reason: 'cancelled' },
      }) as React.ReactElement
    )
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-muted)]')
    expect(indicator?.getAttribute('class')).not.toContain('bg-[var(--color-error)]')
    expect(getByText('Cancelled')).toBeInTheDocument()
  })

  it('a completed fetch with empty content (no error, no cancellation) shows a success dot + "Done" — no silent terminal row', () => {
    const { container, getByText } = renderFetch('', 'complete')
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
    expect(getByText('Done')).toBeInTheDocument()
  })

  it('button toggle: disabled and aria-expanded omitted while there is nothing to expand (running)', () => {
    const { container } = renderFetch(null, 'running')
    const toggle = container.querySelector('button')!
    expect(toggle).toBeDisabled()
    expect(toggle).not.toHaveAttribute('aria-expanded')
  })

  it('the root has no card-frame classes — flat/transparent on the thread', () => {
    const { container } = renderFetch('page content', 'complete')
    const root = container.firstElementChild as HTMLElement
    expect(root.className).not.toContain('rounded-md')
    expect(root.className).not.toContain('overflow-hidden')
    expect(root.className).not.toMatch(/\bborder\b/)
    expect(root.className).not.toContain('bg-[var(--color-surface-1)]')
  })

  it('no descendant carries a card-frame class (rounded-md/overflow-hidden/bg-surface-1) — border-l-2 accent survives', () => {
    const { container } = renderFetch('page content', 'complete')
    fireEvent.click(container.querySelector('button')!)
    const root = container.firstElementChild as HTMLElement
    expect(
      root.querySelector('[class*="rounded-md"], [class*="overflow-hidden"], [class*="bg-[var(--color-surface-1)]"]')
    ).toBeNull()
  })

  it('expanded content panel uses a left accent line, not a bordered/backgrounded card', () => {
    const { container } = renderFetch('page content', 'complete')
    fireEvent.click(container.querySelector('button')!)
    const root = container.firstElementChild as HTMLElement
    const panel = root.children[1] as HTMLElement
    expect(panel.className).toContain('border-l-2')
    expect(panel.className).not.toMatch(/\bborder-b\b/)
  })

  it('renders an "open in new tab" action link for a safe fetched URL', () => {
    const { container } = renderFetch('page content', 'complete', 'https://example.com/docs')
    const link = container.querySelector('a[href="https://example.com/docs"]')
    expect(link).toBeTruthy()
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  })

  it('omits the action link for an unsafe URL scheme (javascript:)', () => {
    const { container } = renderFetch('page content', 'complete', 'javascript:alert(1)')
    expect(container.querySelector('a')).toBeNull()
  })

  it('omits the action link for a non-parseable URL', () => {
    const { container } = renderFetch('page content', 'complete', 'not-a-url')
    expect(container.querySelector('a')).toBeNull()
  })
})

// M7 (second review wave): every test above drives `captured['web_fetch']`
// — the LEGACY alias registration (WebFetchLegacyUI, kept only for old
// transcript replay). The CANONICAL registration the backend actually emits
// post-§7-rename, `captured['fetch_url']` (WebFetchPreviewUI), was never
// separately exercised — both are distinct `makeAssistantToolUI({...})` call
// sites in WebFetchPreview.tsx, each with its own `render` closure, so
// proving the alias threads `isError` correctly says nothing about whether
// the canonical one does.
describe('WebFetchPreviewUI — canonical fetch_url registration wiring (issue #617)', () => {
  it('captures a distinct render function under "fetch_url"', () => {
    expect(captured['fetch_url']).toBeDefined()
    expect(captured['fetch_url']).not.toBe(captured['web_fetch'])
  })

  it('a genuine error outcome (isError=true from the real render prop) renders the error dot and "Failed" label', () => {
    const renderFn = captured['fetch_url']!
    const { container, getByText } = render(
      renderFn({
        args: { url: 'https://example.com' },
        result: null,
        status: { type: 'complete' },
        isError: true,
      }) as React.ReactElement
    )
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-error)]')
    expect(getByText('Failed')).toBeInTheDocument()
  })

  it('a finished call with content but isError=false renders the success dot, not error', () => {
    const renderFn = captured['fetch_url']!
    const { container, getByText } = render(
      renderFn({
        args: { url: 'https://example.com' },
        result: 'page content',
        status: { type: 'complete' },
        isError: false,
      }) as React.ReactElement
    )
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
    expect(getByText('Done')).toBeInTheDocument()
  })
})

describe('WebSearchBlock — flat text-line status dot', () => {
  const structuredResult = '1. Example Site\n   https://example.com\n   Site description here.\n'

  function renderSearch(result: unknown, statusType: string, isError?: boolean) {
    const renderFn = captured['web_search']
    return render(renderFn({ args: { query: 'test' }, result, status: { type: statusType }, isError }) as React.ReactElement)
  }

  it('running: indicator is the spinning icon, not a dot', () => {
    const { container } = renderSearch(null, 'running')
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.tagName.toLowerCase()).toBe('svg')
    expect(indicator?.getAttribute('class')).toContain('animate-spin')
  })

  it('with results: indicator is an 8px success-colored dot', () => {
    const { container } = renderSearch(structuredResult, 'complete')
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.tagName.toLowerCase()).toBe('span')
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
    expect(indicator?.getAttribute('class')).toContain('rounded-full')
  })

  it('isError=true: indicator is an 8px error-colored dot with a "Failed" label — not a green dot, not silent', () => {
    // Issue #617: producible pairing — see the equivalent WebFetchBlock test
    // above for the full rationale.
    const { container, getByText } = renderSearch(null, 'complete', true)
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-error)]')
    expect(indicator?.getAttribute('class')).not.toContain('bg-[var(--color-success)]')
    expect(getByText('Failed')).toBeInTheDocument()
  })

  it('cancelled: indicator is an 8px muted-colored dot with a "Cancelled" label', () => {
    const renderFn = captured['web_search']
    const { container, getByText } = render(
      renderFn({
        args: { query: 'test' },
        result: null,
        status: { type: 'incomplete', reason: 'cancelled' },
      }) as React.ReactElement
    )
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-muted)]')
    expect(indicator?.getAttribute('class')).not.toContain('bg-[var(--color-error)]')
    expect(getByText('Cancelled')).toBeInTheDocument()
  })

  it('a completed search that parses no structured results shows a success dot + "0 results" — no silent terminal row', () => {
    const { container, getByText } = renderSearch('unstructured text with no numbered results', 'complete')
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
    expect(getByText('0 results')).toBeInTheDocument()
  })

  it('button toggle: disabled and aria-expanded omitted while there is nothing to expand (running)', () => {
    const { container } = renderSearch(null, 'running')
    const toggle = container.querySelector('button')!
    expect(toggle).toBeDisabled()
    expect(toggle).not.toHaveAttribute('aria-expanded')
  })

  it('the root has no card-frame classes — flat/transparent on the thread', () => {
    const { container } = renderSearch(structuredResult, 'complete')
    const root = container.firstElementChild as HTMLElement
    expect(root.className).not.toContain('rounded-md')
    expect(root.className).not.toContain('overflow-hidden')
    expect(root.className).not.toMatch(/\bborder\b/)
    expect(root.className).not.toContain('bg-[var(--color-surface-1)]')
  })

  it('no descendant carries a card-frame class (rounded-md/overflow-hidden/bg-surface-1) — border-l-2 accent survives', () => {
    const { container } = renderSearch(structuredResult, 'complete')
    fireEvent.click(container.querySelector('button')!)
    const root = container.firstElementChild as HTMLElement
    expect(
      root.querySelector('[class*="rounded-md"], [class*="overflow-hidden"], [class*="bg-[var(--color-surface-1)]"]')
    ).toBeNull()
  })

  it('expanded results panel uses a left accent line, not a bordered/backgrounded card (no divide-y dividers)', () => {
    const { container } = renderSearch(structuredResult, 'complete')
    fireEvent.click(container.querySelector('button')!)
    const root = container.firstElementChild as HTMLElement
    const panel = root.children[1] as HTMLElement
    expect(panel.className).toContain('border-l-2')
    expect(panel.className).not.toContain('divide-y')
  })
})

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
