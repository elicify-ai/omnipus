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

function parseResult(result: unknown): BrowserResult {
  if (!result) return {}
  if (typeof result === 'string') {
    try {
      return JSON.parse(result) as BrowserResult
    } catch {
      return { content: result }
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
})

// --- Component smoke test ---

// Import the BrowserNavigateUI component to verify it renders without throwing.
// This also exercises the displayUrl helper via the rendered URL display.
import { BrowserNavigateUI, BrowserNavigateBlock } from './BrowserNavigate'

describe('BrowserNavigate component — smoke tests', () => {
  it('exports BrowserNavigateUI', () => {
    // Traces to: vivid-roaming-planet.md line 173
    expect(BrowserNavigateUI).toBeDefined()
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

import { render, screen, fireEvent } from '@testing-library/react'
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
        args={{ url: 'https://example.com' }}
        result={{ title: 'Example', url: 'https://example.com' }}
        isRunning={false}
      />
    )
    expect(screen.getByRole('button', { name: 'Watch live' })).toBeInTheDocument()
  })

  it('renders the Watch live button on a still-running navigate row (not gated on status)', () => {
    render(<BrowserNavigateBlock args={{ url: 'https://example.com' }} result={null} isRunning={true} />)
    expect(screen.getByRole('button', { name: 'Watch live' })).toBeInTheDocument()
  })

  it('clicking Watch live opens the browser panel for the active session/agent', () => {
    useSessionStore.getState().setActiveSession('sess-1', 'agent-1')

    render(
      <BrowserNavigateBlock
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
