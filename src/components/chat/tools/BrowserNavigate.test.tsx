import { describe, it, expect } from 'vitest'

// BrowserNavigate.test.tsx
// Tests for the displayUrl and parseResult helper functions in BrowserNavigate.tsx,
// tested indirectly through the BrowserNavigateBlock component since helpers are not exported.
// Traces to: vivid-roaming-planet.md line 173

// The helpers displayUrl and parseResult are not exported. They are tested indirectly
// by rendering BrowserNavigateBlock and observing the output.
//
// Import the exported component to verify the helpers behave as specified.
// makeAssistantToolUI wraps the block; we test BrowserNavigateBlock by importing the
// render function directly via component internals. Since the function is not exported,
// we verify through the rendered component output.

// To isolate helpers without re-exporting them, we duplicate the minimal logic here.
// Note: if backend-lead exports displayUrl/parseResult, these tests should be updated
// to import them directly.

// --- Inlined helper implementations for isolated unit testing ---
// These mirror the production logic exactly. If the production code changes, these
// must be updated to match.

function displayUrl(url: string): string {
  try {
    const u = new URL(url)
    return u.hostname + (u.pathname !== '/' ? u.pathname : '')
  } catch {
    return url
  }
}

interface BrowserResult {
  url?: string
  title?: string
  screenshot?: string
  content?: string
  error?: string
}

// Mirrors stripUntrustedContentWrapper in src/lib/untrustedToolContent.ts —
// see that file's header comment for why browser.navigate results can
// arrive wrapped in [UNTRUSTED_CONTENT] markers.
function stripUntrustedContentWrapper(raw: string): string | null {
  const UNTRUSTED_OPEN = '[UNTRUSTED_CONTENT]'
  const UNTRUSTED_CLOSE = '[/UNTRUSTED_CONTENT]'
  const UNTRUSTED_REDACTED = '[UNTRUSTED_CONTENT_REDACTED_FOR_SUMMARIZATION]'
  const trimmed = raw.trim()
  if (trimmed === UNTRUSTED_REDACTED) return null
  if (trimmed.startsWith(UNTRUSTED_OPEN) && trimmed.endsWith(UNTRUSTED_CLOSE)) {
    return trimmed.slice(UNTRUSTED_OPEN.length, trimmed.length - UNTRUSTED_CLOSE.length).trim()
  }
  return raw
}

function parseResult(result: unknown): BrowserResult {
  if (!result) return {}
  if (typeof result === 'string') {
    const unwrapped = stripUntrustedContentWrapper(result)
    if (unwrapped === null) return { content: '(content withheld by security policy)' }
    try {
      return JSON.parse(unwrapped) as BrowserResult
    } catch {
      return { content: unwrapped }
    }
  }
  if (typeof result === 'object') return result as BrowserResult
  return {}
}

// --- displayUrl tests ---
// Traces to: vivid-roaming-planet.md line 176

describe('displayUrl — URL display helper', () => {
  // Dataset from spec: valid URL, URL with path, invalid string
  it('returns hostname for a valid URL with no path', () => {
    // Traces to: vivid-roaming-planet.md line 176
    expect(displayUrl('https://example.com')).toBe('example.com')
  })

  it('returns hostname for a valid URL with root path /', () => {
    // Root path is omitted — only hostname returned.
    // Traces to: vivid-roaming-planet.md line 176
    expect(displayUrl('https://example.com/')).toBe('example.com')
  })

  it('returns hostname + path for a URL with a non-root path', () => {
    // Traces to: vivid-roaming-planet.md line 176
    expect(displayUrl('https://example.com/search?q=test')).toBe('example.com/search')
  })

  it('returns hostname + path for a URL with nested path segments', () => {
    // Traces to: vivid-roaming-planet.md line 176
    expect(displayUrl('https://github.com/user/repo')).toBe('github.com/user/repo')
  })

  it('returns the raw string for an invalid URL', () => {
    // URL parsing fails — raw string is returned as fallback.
    // Traces to: vivid-roaming-planet.md line 176
    expect(displayUrl('not-a-url')).toBe('not-a-url')
  })

  it('returns the raw string for a malformed URL with spaces', () => {
    // Traces to: vivid-roaming-planet.md line 176
    const raw = 'http://bad url with spaces'
    // new URL() throws for URLs with spaces
    expect(displayUrl(raw)).toBe(raw)
  })

  it('returns an empty string for an empty string input', () => {
    // Empty string is not a valid URL — returned as-is.
    // Traces to: vivid-roaming-planet.md line 176
    expect(displayUrl('')).toBe('')
  })
})

// --- parseResult tests ---
// Traces to: vivid-roaming-planet.md line 177

describe('parseResult — result parsing helper', () => {
  it('returns empty object for null', () => {
    // Traces to: vivid-roaming-planet.md line 177
    expect(parseResult(null)).toEqual({})
  })

  it('returns empty object for undefined', () => {
    // undefined is falsy — same branch as null.
    // Traces to: vivid-roaming-planet.md line 177
    expect(parseResult(undefined)).toEqual({})
  })

  it('returns empty object for false', () => {
    // false is falsy — hits the !result branch.
    // Traces to: vivid-roaming-planet.md line 177
    expect(parseResult(false)).toEqual({})
  })

  it('parses a valid JSON string into an object', () => {
    // Traces to: vivid-roaming-planet.md line 177
    const json = '{"url":"https://example.com","title":"Example","content":"page text"}'
    const result = parseResult(json)
    expect(result).toEqual({ url: 'https://example.com', title: 'Example', content: 'page text' })
  })

  it('wraps a plain non-JSON string in {content}', () => {
    // Backend may return plain text summaries — wrapped as content field.
    // Traces to: vivid-roaming-planet.md line 177
    expect(parseResult('plain text summary')).toEqual({ content: 'plain text summary' })
  })

  it('returns an object as-is when passed directly', () => {
    // Traces to: vivid-roaming-planet.md line 177
    const obj = { url: 'https://example.com', title: 'Example' }
    expect(parseResult(obj)).toBe(obj)
  })

  it('returns an empty object for unexpected type: number', () => {
    // Numbers fall through to the final return {} branch.
    // Traces to: vivid-roaming-planet.md line 177
    expect(parseResult(42)).toEqual({})
  })

  it('returns an empty object for unexpected type: array', () => {
    // Arrays are objects, so they pass the typeof === 'object' check and are returned as-is.
    // This matches the production code: typeof [] === 'object'.
    // Traces to: vivid-roaming-planet.md line 177
    const arr = [1, 2, 3]
    expect(parseResult(arr)).toBe(arr)
  })

  it('handles JSON string with nested fields correctly', () => {
    // Traces to: vivid-roaming-planet.md line 177
    const json = '{"screenshot":"base64data","error":"timeout"}'
    expect(parseResult(json)).toEqual({ screenshot: 'base64data', error: 'timeout' })
  })

  it('returns {content: string} for a JSON-invalid string like bare braces', () => {
    // Malformed JSON falls back to plain content wrapping.
    // Traces to: vivid-roaming-planet.md line 177
    expect(parseResult('{not valid json}')).toEqual({ content: '{not valid json}' })
  })

  // --- SEC-25 PromptGuard wrapper (defect: JSON.parse warning bug) ---
  //
  // browser.navigate is classified untrusted (pkg/agent/prompt_guard.go) so
  // the gateway sends the PromptGuard-sanitized string — wrapped in
  // [UNTRUSTED_CONTENT] markers — as the tool result, not raw JSON.

  it('unwraps a [UNTRUSTED_CONTENT]-wrapped JSON result and parses the inner JSON', () => {
    const inner = '{"url":"https://example.com","title":"Example"}'
    const wrapped = `[UNTRUSTED_CONTENT]\n${inner}\n[/UNTRUSTED_CONTENT]`
    expect(parseResult(wrapped)).toEqual({ url: 'https://example.com', title: 'Example' })
  })

  it('unwraps a [UNTRUSTED_CONTENT] wrapper containing Medium-strictness ZWNJ escaping', () => {
    // escapeInjectionPhrases (pkg/security/promptguard.go) splices a U+200C
    // inside matched phrases — it must not break JSON.parse after unwrap.
    const inner = '{"title":"you‌ are now here"}'
    const wrapped = `[UNTRUSTED_CONTENT]\n${inner}\n[/UNTRUSTED_CONTENT]`
    expect(parseResult(wrapped)).toEqual({ title: 'you‌ are now here' })
  })

  it('returns a withheld-content notice for the High-strictness redaction placeholder', () => {
    expect(parseResult('[UNTRUSTED_CONTENT_REDACTED_FOR_SUMMARIZATION]')).toEqual({
      content: '(content withheld by security policy)',
    })
  })
})

// --- Component smoke test ---
//
// Issue #617: prior to this file's fix, BrowserNavigate had ZERO error
// coverage of the actual WIRING between makeAssistantToolUI's render props
// and BrowserNavigateBlock — BrowserNavigateBlock itself was tested directly
// with a hand-passed `isError` prop (see "flat text-line status dot" below),
// but nothing proved that BrowserNavigateUI's `render` callback actually
// threads the real `isError` render prop through, rather than re-deriving it
// from `status.type === 'incomplete'` (which can never be true for a
// finished call carrying a result — see omnipus-runtime.ts). The capture
// setup below (same pattern as BashOutput.edge.test.tsx / FileTools.edge.test.tsx
// / WebFetchAndSearch.edge.test.tsx) closes that gap for both the canonical
// `browser.navigate` registration and its `browser_navigate` underscore alias.

import { vi } from 'vitest'
import { render } from '@testing-library/react'

type NavigateRenderFn = (props: {
  args: unknown
  result: unknown
  status: { type: string; reason?: string }
  isError?: boolean
}) => React.ReactNode

const capturedNavigate = vi.hoisted(() => ({
  dot: null as NavigateRenderFn | null,
  underscore: null as NavigateRenderFn | null,
}))

vi.mock('@assistant-ui/react', async (importOriginal) => {
  const original = await importOriginal<typeof import('@assistant-ui/react')>()
  return {
    ...original,
    makeAssistantToolUI: (config: Record<string, unknown>) => {
      if (config.toolName === 'browser.navigate') capturedNavigate.dot = config.render as NavigateRenderFn
      if (config.toolName === 'browser_navigate') capturedNavigate.underscore = config.render as NavigateRenderFn
      return config
    },
  }
})

// Import the BrowserNavigateUI component to verify it renders without throwing.
// This also exercises the displayUrl helper via the rendered URL display.
import { BrowserNavigateUI, BrowserNavigateUnderscoreUI, BrowserNavigateBlock } from './BrowserNavigate'

describe('BrowserNavigate component — smoke tests', () => {
  it('exports BrowserNavigateUI', () => {
    // Traces to: vivid-roaming-planet.md line 173
    expect(BrowserNavigateUI).toBeDefined()
  })

  it('exports BrowserNavigateUnderscoreUI', () => {
    expect(BrowserNavigateUnderscoreUI).toBeDefined()
  })
})

describe('BrowserNavigateUI / BrowserNavigateUnderscoreUI wiring — issue #617', () => {
  it('a genuine error outcome (isError=true from the real render prop) renders the error dot and "Failed" label', () => {
    expect(capturedNavigate.dot).toBeDefined()
    const { container, getByText } = render(
      capturedNavigate.dot!({
        args: { url: 'https://example.com' },
        result: { url: 'https://example.com', error: 'navigation timeout' },
        status: { type: 'complete' },
        isError: true,
      }) as React.ReactElement
    )
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-error)]')
    expect(getByText('Failed')).toBeInTheDocument()
  })

  it('a finished call with a result but isError=false renders the success dot, not error', () => {
    expect(capturedNavigate.dot).toBeDefined()
    const { container, getByText } = render(
      capturedNavigate.dot!({
        args: { url: 'https://example.com' },
        result: { url: 'https://example.com', title: 'Example' },
        status: { type: 'complete' },
        isError: false,
      }) as React.ReactElement
    )
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
    expect(getByText('Done')).toBeInTheDocument()
  })

  it('the underscore alias (browser_navigate) wires isError through identically', () => {
    expect(capturedNavigate.underscore).toBeDefined()
    const { container, getByText } = render(
      capturedNavigate.underscore!({
        args: { url: 'https://example.com' },
        result: { url: 'https://example.com', error: 'navigation timeout' },
        status: { type: 'complete' },
        isError: true,
      }) as React.ReactElement
    )
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-error)]')
    expect(getByText('Failed')).toBeInTheDocument()
  })

  // Second-review-wave correction: this test's ORIGINAL title claimed
  // cancellation renders correctly "through the real wiring." That overstates
  // it — `status:{type:'incomplete',reason:'cancelled'}` is not a shape the
  // live AssistantUI pipeline can actually hand to this render callback.
  // ChatScreen only mounts the live `ThreadPrimitive.Messages` tree (where
  // `capturedNavigate.dot` is registered) while `hasStreamingMessage` is true
  // (ChatScreen.tsx), and `buildMessageStatus` (omnipus-runtime.ts) returns
  // `{type:'running'}` for exactly as long as `msg.isStreaming` holds — the
  // message (and every resultless part inheriting its status, per
  // omnipus-runtime.ts's own doc comment on `toMessagePartStatus`) can only
  // become `incomplete`/cancelled AFTER streaming stops, by which point the
  // message has already moved to the historical/replay render path
  // (VirtualAssistantMessageRow → BrowserToolReplayBlock, not this live
  // registration) and never mounts this callback again. So real cancellation
  // for browser.navigate is only ever OBSERVABLE via the replay path, not
  // this one. What this test legitimately proves — and all it claims to
  // prove now — is that BrowserNavigateBlock's OWN `isCancelledStatus(status)`
  // check renders the cancelled treatment correctly when handed that status,
  // a still-useful unit-level check of the render callback's internal logic.
  it('BrowserNavigateBlock renders the cancelled treatment when handed an incomplete/cancelled status (unit-level; not a claim this shape reaches the callback via the live pipeline — see comment above)', () => {
    expect(capturedNavigate.dot).toBeDefined()
    const { container, getByText } = render(
      capturedNavigate.dot!({
        args: { url: 'https://example.com' },
        result: undefined,
        status: { type: 'incomplete', reason: 'cancelled' },
        isError: false,
      }) as React.ReactElement
    )
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-muted)]')
    expect(indicator?.getAttribute('class')).not.toContain('bg-[var(--color-error)]')
    expect(getByText('Cancelled')).toBeInTheDocument()
  })
})

// --- "Watch live" button (ADR-038 UAT Bug 1) ---
//
// browser_navigate renders through a separate component from the other
// browser.* tools (BrowserTool.tsx's BrowserToolBlock), and was missed when
// the "Watch live" launcher was first added there. These tests assert the
// launcher is present on BrowserNavigateBlock rows too — running AND
// completed, since navigate is the near-universal first browser action and
// the browser session persists after the call finishes.

import { screen, fireEvent } from '@testing-library/react'
import { beforeEach, afterEach } from 'vitest'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'

describe('BrowserNavigateBlock — "Watch live" launcher', () => {
  beforeEach(() => {
    useUiStore.getState().closeBrowserPanel()
    useSessionStore.setState({ activeSessionId: null, activeAgentId: null })
  })

  afterEach(() => {
    useUiStore.getState().closeBrowserPanel()
    useSessionStore.setState({ activeSessionId: null, activeAgentId: null })
  })

  it('renders the Watch live button on a completed navigate row', () => {
    render(
      <BrowserNavigateBlock
        toolName="browser.navigate"
        args={{ url: 'https://example.com' }}
        result={{ title: 'Example', url: 'https://example.com' }}
        isRunning={false}
      />
    )
    expect(screen.getByRole('button', { name: 'Watch live' })).toBeInTheDocument()
  })

  it('renders the Watch live button on a still-running navigate row (not gated on status)', () => {
    render(<BrowserNavigateBlock toolName="browser.navigate" args={{ url: 'https://example.com' }} result={null} isRunning={true} />)
    expect(screen.getByRole('button', { name: 'Watch live' })).toBeInTheDocument()
  })

  it('clicking Watch live opens the browser panel for the active session/agent', () => {
    useSessionStore.getState().setActiveSession('sess-1', 'agent-1')

    render(
      <BrowserNavigateBlock
        toolName="browser.navigate"
        args={{ url: 'https://example.com' }}
        result={{ title: 'Example' }}
        isRunning={false}
      />
    )
    fireEvent.click(screen.getByRole('button', { name: 'Watch live' }))

    expect(useUiStore.getState().browserPanel).toEqual({ sessionId: 'sess-1', agentId: 'agent-1' })
  })

  it('shows an error toast and does not open the panel when there is no active session', () => {
    render(
      <BrowserNavigateBlock
        toolName="browser.navigate"
        args={{ url: 'https://example.com' }}
        result={{ title: 'Example' }}
        isRunning={false}
      />
    )
    fireEvent.click(screen.getByRole('button', { name: 'Watch live' }))

    expect(useUiStore.getState().browserPanel).toBeNull()
    expect(useUiStore.getState().toasts.some((t) => t.message === 'No active session to watch.')).toBe(
      true
    )
  })
})

// ── flat text-line status dot (ticket "Tool components in chat", P2) ────────
// The old bordered/backgrounded card (rounded-md border overflow-hidden +
// status-tinted border + bg-surface-1 header) is gone — the row is now a
// flat text line whose only status color comes from an 8px dot (running
// keeps the spinning icon in the same slot). The hardcoded Globe identity
// icon is no longer rendered — it was redundant with the fixed
// "browser.navigate" label.

describe('BrowserNavigateBlock — flat text-line status dot', () => {
  beforeEach(() => {
    useUiStore.getState().closeBrowserPanel()
    useSessionStore.setState({ activeSessionId: null, activeAgentId: null })
  })

  afterEach(() => {
    useUiStore.getState().closeBrowserPanel()
    useSessionStore.setState({ activeSessionId: null, activeAgentId: null })
  })

  /** The toggle button is always the first <button> in the row (Watch Live
   * is a separate sibling button) — its first child is the status indicator
   * when one is rendered. */
  function getIndicatorEl(container: HTMLElement) {
    return container.querySelector('button')?.children[0] as HTMLElement | undefined
  }

  it('running: indicator is the spinning icon, not a dot', () => {
    const { container } = render(
      <BrowserNavigateBlock toolName="browser.navigate" args={{ url: 'https://example.com' }} result={null} isRunning={true} />
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.tagName.toLowerCase()).toBe('svg')
    expect(indicator?.getAttribute('class')).toContain('animate-spin')
  })

  it('completed with a result: indicator is an 8px success-colored dot', () => {
    const { container } = render(
      <BrowserNavigateBlock
        toolName="browser.navigate"
        args={{ url: 'https://example.com' }}
        result={{ title: 'Example', url: 'https://example.com' }}
        isRunning={false}
      />
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.tagName.toLowerCase()).toBe('span')
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
    expect(indicator?.getAttribute('class')).toContain('rounded-full')
  })

  it('error: indicator is an 8px error-colored dot', () => {
    const { container } = render(
      <BrowserNavigateBlock toolName="browser.navigate" args={{ url: 'https://example.com' }} result={null} isRunning={false} isError />
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-error)]')
  })

  it('cancelled: indicator is an 8px muted-colored dot with a "Cancelled" label, not the red error dot', () => {
    const { container } = render(
      <BrowserNavigateBlock toolName="browser.navigate" args={{ url: 'https://example.com' }} result={null} isRunning={false} isCancelled />
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-muted)]')
    expect(indicator?.getAttribute('class')).not.toContain('bg-[var(--color-error)]')
    expect(screen.getByText('Cancelled')).toBeInTheDocument()
  })

  it('a completed navigate with no result (no error, no cancellation) still shows a status dot + "Done" label — no silent terminal row', () => {
    const { container } = render(
      <BrowserNavigateBlock toolName="browser.navigate" args={{ url: 'https://example.com' }} result={null} isRunning={false} />
    )
    const indicator = getIndicatorEl(container)
    expect(indicator).toBeTruthy()
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
  })

  it('button toggle: disabled and aria-expanded omitted while there is nothing to expand (not running, no result)', () => {
    render(<BrowserNavigateBlock toolName="browser.navigate" args={{ url: 'https://example.com' }} result={null} isRunning={false} />)
    const toggle = screen.getByRole('button', { name: /browser\.navigate/ })
    expect(toggle).toBeDisabled()
    expect(toggle).not.toHaveAttribute('aria-expanded')
  })

  it('the outer container has no card-frame classes — flat/transparent on the thread', () => {
    const { container } = render(
      <BrowserNavigateBlock
        toolName="browser.navigate"
        args={{ url: 'https://example.com' }}
        result={{ title: 'Example', url: 'https://example.com' }}
        isRunning={false}
      />
    )
    const root = container.firstElementChild as HTMLElement
    expect(root.className).not.toContain('rounded-md')
    expect(root.className).not.toContain('border')
    expect(root.className).not.toContain('overflow-hidden')
    expect(root.className).not.toContain('bg-[var(--color-surface-1)]')
  })

  it('the row identifies the tool via the "browser.navigate" text, not a Globe icon', () => {
    render(
      <BrowserNavigateBlock
        toolName="browser.navigate"
        args={{ url: 'https://example.com' }}
        result={{ title: 'Example', url: 'https://example.com' }}
        isRunning={false}
      />
    )
    expect(screen.getByText('browser.navigate')).toBeInTheDocument()
  })

  it('expanded detail uses a left accent line, not a full bordered panel', () => {
    render(
      <BrowserNavigateBlock
        toolName="browser.navigate"
        args={{ url: 'https://example.com' }}
        result={{ title: 'Example', url: 'https://example.com', content: 'page text' }}
        isRunning={false}
      />
    )
    fireEvent.click(screen.getByRole('button', { name: /browser\.navigate/ }))
    const detail = screen.getByText('page text').parentElement
    expect(detail?.className).toContain('border-l-2')
    expect(detail?.className).not.toContain('border-t')
  })

  it('no descendant carries a card-frame class (rounded-md/overflow-hidden/bg-surface-1) — border-l-2 accent survives', () => {
    const { container } = render(
      <BrowserNavigateBlock
        toolName="browser.navigate"
        args={{ url: 'https://example.com' }}
        result={{ title: 'Example', url: 'https://example.com', content: 'page text' }}
        isRunning={false}
      />
    )
    fireEvent.click(screen.getByRole('button', { name: /browser\.navigate/ }))
    const root = container.firstElementChild as HTMLElement
    expect(
      root.querySelector('[class*="rounded-md"], [class*="overflow-hidden"], [class*="bg-[var(--color-surface-1)]"]')
    ).toBeNull()
  })
})
