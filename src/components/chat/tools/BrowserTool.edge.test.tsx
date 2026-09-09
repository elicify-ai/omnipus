/**
 * BrowserToolBlock and BrowserNavigateBlock edge-case render tests (Phase 5, Agent B)
 *
 * Covers degenerate-but-valid inputs to browser tool components. Uses
 * ToolCallStartFrame / ToolCallResultFrame from generated asyncapi-types.ts
 * to build base props where applicable.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { BrowserToolBlock } from './BrowserTool'
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
            args={{}}
            result={result}
            status={{ type: 'complete' }}
            isError={undefined}
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
            args={args}
            result={null}
            status={{ type: 'running' }}
            isError={undefined}
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
            args={{ selector: '#main' }}
            result={result}
            status={status}
            isError={undefined}
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
            args={{}}
            result={null}
            status={{ type: 'running' }}
            isError={undefined}
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
            args={frame.params as Record<string, unknown>}
            result={result}
            status={{ type: statusType }}
            isError={undefined}
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
            args={{}}
            result={frame.result}
            status={{ type: statusType }}
            isError={undefined}
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
      args={{ url: 'https://example.com' }}
      result={null}
      status={{ type: 'running' }}
      isError={undefined}
      summary="https://example.com"
    />
  )
  expect(screen.getByText('browser.navigate')).toBeInTheDocument()
  expect(screen.getByText('https://example.com')).toBeInTheDocument()
})

// ── SEC-25 PromptGuard [UNTRUSTED_CONTENT] wrapper (defect: JSON.parse warning spam) ──
//
// browser.click/browser.type/browser.wait/browser.evaluate results are sent
// to the SPA already wrapped by the backend's untrusted-content guard (see
// src/lib/untrustedToolContent.ts). Before the fix, parseResult tried
// JSON.parse on the wrapped string directly, always failed, and logged
// `console.warn('[BrowserTool] ... returned non-JSON string result', ...)`
// on every single call to these 4 tools — this is the reproduction for
// that exact regression.

describe('BrowserToolBlock — SEC-25 [UNTRUSTED_CONTENT] wrapper handling', () => {
  let warnSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
  })

  afterEach(() => {
    warnSpy.mockRestore()
  })

  it.each(['browser.click', 'browser.type', 'browser.wait', 'browser.evaluate'])(
    '%s: does not console.warn and renders the unwrapped JSON result',
    (toolName) => {
      const inner = JSON.stringify({ result: 'ok', selector: '#submit' })
      const wrapped = `[UNTRUSTED_CONTENT]\n${inner}\n[/UNTRUSTED_CONTENT]`

      render(
        <BrowserToolBlock
          toolName={toolName}
          args={{}}
          result={wrapped}
          status={{ type: 'complete' }}
          isError={undefined}
          summary="test"
        />
      )

      expect(warnSpy).not.toHaveBeenCalled()
    }
  )

  it('browser.evaluate: unwraps and shows the actual JS evaluate result, not an error', () => {
    const inner = JSON.stringify({ result: 42 })
    const wrapped = `[UNTRUSTED_CONTENT]\n${inner}\n[/UNTRUSTED_CONTENT]`

    render(
      <BrowserToolBlock
        toolName="browser.evaluate"
        args={{}}
        result={wrapped}
        status={{ type: 'complete' }}
        isError={undefined}
        summary="document.title"
      />
    )

    // Expand the panel — the "Result" section title only appears once expanded.
    // Two buttons render per row (toggle + "Watch live"); target the toggle by name.
    fireEvent.click(screen.getByRole('button', { name: /browser\.evaluate/ }))
    expect(screen.getByText('42')).toBeInTheDocument()
    expect(screen.queryByText(/Malformed result/)).not.toBeInTheDocument()
  })

  it('browser.get_text: unwraps and shows the real text field instead of the raw wrapper markers', () => {
    const inner = JSON.stringify({ text: 'Welcome to the page' })
    const wrapped = `[UNTRUSTED_CONTENT]\n${inner}\n[/UNTRUSTED_CONTENT]`

    render(
      <BrowserToolBlock
        toolName="browser.get_text"
        args={{}}
        result={wrapped}
        status={{ type: 'complete' }}
        isError={undefined}
        summary="#main"
      />
    )

    fireEvent.click(screen.getByRole('button', { name: /browser\.get_text/ }))
    expect(screen.getByText('Welcome to the page')).toBeInTheDocument()
    expect(screen.queryByText(/UNTRUSTED_CONTENT/)).not.toBeInTheDocument()
  })

  it('shows a withheld-content notice (not a parse error) for the High-strictness redaction placeholder', () => {
    render(
      <BrowserToolBlock
        toolName="browser.click"
        args={{}}
        result="[UNTRUSTED_CONTENT_REDACTED_FOR_SUMMARIZATION]"
        status={{ type: 'complete' }}
        isError={undefined}
        summary="test"
      />
    )

    expect(warnSpy).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: /browser\.click/ }))
    expect(screen.getByText('(content withheld by security policy)')).toBeInTheDocument()
  })

  it('still warns and surfaces an error for genuinely malformed, non-wrapped JSON (no false negatives)', () => {
    render(
      <BrowserToolBlock
        toolName="browser.click"
        args={{}}
        result="{not valid json at all}"
        status={{ type: 'complete' }}
        isError={undefined}
        summary="test"
      />
    )

    expect(warnSpy).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button', { name: /browser\.click/ }))
    expect(screen.getByText(/Malformed result/)).toBeInTheDocument()
  })
})

it('null result with complete status renders without showing a <script> tag (XSS regression)', () => {
  render(
    <BrowserToolBlock
      toolName="browser.evaluate"
      args={{ script: '<script>alert(1)</script>' }}
      result={'<script>window.__xss=true</script>'}
      status={{ type: 'complete' }}
      isError={undefined}
      summary="evaluate"
    />
  )
  // No injected <script> elements in the DOM
  expect(document.querySelectorAll('script')).toHaveLength(0)
})

// ── flat text-line status dot (ticket "Tool components in chat", P2) ────────
// The old bordered/backgrounded card (rounded-md border overflow-hidden +
// status-tinted border + bg-surface-1 header) is gone — the row is now a
// flat text line whose only status color comes from an 8px dot (running
// keeps the spinning icon in the same slot). The per-tool identity icon
// (CursorClick/Keyboard/…) passed via the `icon` prop is no longer rendered
// — it was redundant with `toolName`, which already disambiguates uniquely.

describe('BrowserToolBlock — flat text-line status dot', () => {
  /** The toggle button is always the first <button> in the row (Watch Live
   * is a separate sibling button) — its first child is the status indicator
   * when one is rendered. */
  function getIndicatorEl(container: HTMLElement) {
    return container.querySelector('button')?.children[0] as HTMLElement | undefined
  }

  it('running: indicator is the spinning icon, not a dot', () => {
    const { container } = render(
      <BrowserToolBlock
        toolName="browser.click"
        args={{}}
        result={null}
        status={{ type: 'running' }}
        isError={undefined}
        summary="#btn"
      />
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.tagName.toLowerCase()).toBe('svg')
    expect(indicator?.getAttribute('class')).toContain('animate-spin')
  })

  it('complete with a result: indicator is an 8px success-colored dot', () => {
    const { container } = render(
      <BrowserToolBlock
        toolName="browser.click"
        args={{}}
        result={{ ok: true }}
        status={{ type: 'complete' }}
        isError={undefined}
        summary="#btn"
      />
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.tagName.toLowerCase()).toBe('span')
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
    expect(indicator?.getAttribute('class')).toContain('rounded-full')
  })

  it('isError=true: indicator is an 8px error-colored dot', () => {
    // Issue #617: BrowserToolBlock no longer derives isError from
    // `status.type === 'incomplete'` internally — that status can never be
    // true for a finished call carrying a result. Callers (the live
    // makeAssistantToolUI render prop, or the replay path via
    // BrowserToolReplayBlock) now pass the real outcome as an explicit
    // `isError` prop.
    const { container } = render(
      <BrowserToolBlock
        toolName="browser.click"
        args={{}}
        result={{ error: 'element not found' }}
        status={{ type: 'complete' }}
        isError
        summary="#btn"
      />
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-error)]')
  })

  it('a finished call with a result but isError=false renders the success dot, not error (producible pairing)', () => {
    const { container } = render(
      <BrowserToolBlock
        toolName="browser.click"
        args={{}}
        result={{ ok: true }}
        status={{ type: 'complete' }}
        isError={false}
        summary="#btn"
      />
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
  })

  it('there is no per-tool identity icon prop at all — the `icon` field was fully removed (dead plumbing)', () => {
    // The CursorClick/Keyboard/etc. glyph previously threaded through as an
    // `icon` prop was redundant with the toolName text and has been deleted
    // entirely from BrowserToolBlockProps/BrowserToolSpec/BROWSER_TOOL_SPECS
    // — only the status dot/spinner (a <span> or <svg> from
    // getToolBadgeStatusConfig) leads the row now.
    render(
      <BrowserToolBlock
        toolName="browser.click"
        args={{}}
        result={{ ok: true }}
        status={{ type: 'complete' }}
        isError={undefined}
        summary="#btn"
      />
    )
    // toolName text is still present — the row identifies the tool via text, not icon.
    expect(screen.getByText('browser.click')).toBeInTheDocument()
  })

  it('cancelled: indicator is an 8px muted-colored dot with a "Cancelled" label, not the red error dot', () => {
    render(
      <BrowserToolBlock
        toolName="browser.click"
        args={{}}
        result={null}
        status={{ type: 'incomplete', reason: 'cancelled' }}
        isError={undefined}
        summary="#btn"
      />
    )
    const indicator = screen.getByText('browser.click').parentElement?.children[0] as HTMLElement
    expect(indicator.getAttribute('class')).toContain('bg-[var(--color-muted)]')
    expect(indicator.getAttribute('class')).not.toContain('bg-[var(--color-error)]')
    expect(screen.getByText('Cancelled')).toBeInTheDocument()
  })

  it('a completed call with no result (no error, no cancellation) still shows a status dot + "Done" label — no silent terminal row', () => {
    render(
      <BrowserToolBlock
        toolName="browser.click"
        args={{}}
        result={null}
        status={{ type: 'complete' }}
        isError={undefined}
        summary="#btn"
      />
    )
    expect(screen.getByText('Done')).toBeInTheDocument()
  })

  it('button toggle: disabled and aria-expanded omitted while there is nothing to expand (complete, no result)', () => {
    render(
      <BrowserToolBlock
        toolName="browser.click"
        args={{}}
        result={null}
        status={{ type: 'complete' }}
        isError={undefined}
        summary="#btn"
      />
    )
    const toggle = screen.getByRole('button', { name: /browser\.click/ })
    expect(toggle).toBeDisabled()
    expect(toggle).not.toHaveAttribute('aria-expanded')
  })

  it('no descendant carries a card-frame class (rounded-md/overflow-hidden/bg-surface-1) — border-l-2 accent survives', () => {
    render(
      <BrowserToolBlock
        toolName="browser.evaluate"
        args={{ expression: 'document.title' }}
        result={{ result: 'My Page' }}
        status={{ type: 'complete' }}
        isError={undefined}
        summary="document.title"
      />
    )
    fireEvent.click(screen.getByRole('button', { name: /browser\.evaluate/ }))
    const root = screen.getByText('browser.evaluate').closest('div.mt-2') as HTMLElement
    expect(
      root.querySelector('[class*="rounded-md"], [class*="overflow-hidden"], [class*="bg-[var(--color-surface-1)]"]')
    ).toBeNull()
  })

  it('the outer container has no card-frame classes — flat/transparent on the thread', () => {
    const { container } = render(
      <BrowserToolBlock
        toolName="browser.click"
        args={{}}
        result={{ ok: true }}
        status={{ type: 'complete' }}
        isError={undefined}
        summary="#btn"
      />
    )
    const root = container.firstElementChild as HTMLElement
    expect(root.className).not.toContain('rounded-md')
    expect(root.className).not.toContain('border')
    expect(root.className).not.toContain('overflow-hidden')
    expect(root.className).not.toContain('bg-[var(--color-surface-1)]')
  })

  it('expanded detail uses a left accent line, not a full bordered panel', () => {
    render(
      <BrowserToolBlock
        toolName="browser.evaluate"
        args={{ expression: 'document.title' }}
        result={{ result: 'My Page' }}
        status={{ type: 'complete' }}
        isError={undefined}
        summary="document.title"
      />
    )
    fireEvent.click(screen.getByRole('button', { name: /browser\.evaluate/ }))
    const detail = screen.getByText('Result').parentElement?.parentElement
    expect(detail?.className).toContain('border-l-2')
    expect(detail?.className).not.toContain('border-t')
  })
})
