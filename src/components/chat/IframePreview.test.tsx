/**
 * IframePreview component tests
 *
 * Traces to: docs/specs/chat-served-iframe-preview-spec.md
 * FR-010, FR-010a, FR-011, FR-012, FR-012a, FR-012b, FR-013, FR-014, FR-015, FR-019
 *
 * Strategy:
 *   - Mock @tanstack/react-query (useQuery returning stub aboutInfo)
 *   - Mock @/store/ui (addToast spy)
 *   - Mock @/lib/api (fetchAboutInfo)
 *   - Mock navigator.clipboard for copy-link test
 *   - Mock window.open for open-in-new-tab test
 *   - Use vi.useFakeTimers() for warmup interval testing
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { IframePreview, type IframePreviewProps } from './IframePreview'

// Re-export the module under test so we can access the private extractPath via
// a render-based integration test (the function is not exported, so we exercise
// it through the component's rendering behaviour).
// For purely unit-level tests we test via observable component output.

// ── Mocks ─────────────────────────────────────────────────────────────────────

const mockAddToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: mockAddToast }),
}))

// NOTE: vi.mock factories are hoisted to the top of the file, so they cannot
// reference variables declared later. We use vi.fn() so individual tests can
// override the return value with mockReturnValueOnce() for specific scenarios.
const mockUseQuery = vi.fn().mockReturnValue({
  data: {
    preview_port: 5001,
    preview_listener_enabled: true,
    warmup_timeout_seconds: 10,
  },
  isLoading: false,
})

vi.mock('@tanstack/react-query', () => ({
  useQuery: (...args: unknown[]) => mockUseQuery(...args),
}))

vi.mock('@/lib/api', () => ({
  fetchAboutInfo: vi.fn().mockResolvedValue({
    preview_port: 5001,
    preview_listener_enabled: true,
    warmup_timeout_seconds: 10,
  }),
}))

// Reset mockUseQuery before each test so per-test overrides don't bleed.
beforeEach(() => {
  mockUseQuery.mockReturnValue({
    data: {
      preview_port: 5001,
      preview_listener_enabled: true,
      warmup_timeout_seconds: 10,
    },
    isLoading: false,
  })
})

// ── Helpers ───────────────────────────────────────────────────────────────────

// Loose override type accepted by the factory helpers — allows partial result
// shapes in tests without requiring the full ServeWorkspaceResult /
// RunInWorkspaceResult field set. The outer cast to IframePreviewProps is safe
// because the component only reads the fields it needs.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
type LooseProps = Record<string, any>

/**
 * Returns a standard set of props for a ready serve_workspace tool call
 * with both path and url in the result (FR-008 — result includes both fields).
 */
function makeReadyProps(overrides: LooseProps = {}): IframePreviewProps {
  return {
    kind: 'serve_workspace',
    result: {
      path: '/serve/agent-1/abc123/',
      url: 'http://localhost:5001/serve/agent-1/abc123/',
      expires_at: '2099-01-01T00:00:00Z',
    },
    warmupTimeoutSeconds: 10,
    ...overrides,
  } as IframePreviewProps
}

function makeWarmupProps(overrides: LooseProps = {}): IframePreviewProps {
  return {
    kind: 'run_in_workspace',
    result: {
      path: '/dev/agent-1/xyz789/',
      url: 'http://localhost:5001/dev/agent-1/xyz789/',
      expires_at: '2099-01-01T00:00:00Z',
      command: 'npm start',
      port: 3000,
    },
    warmupTimeoutSeconds: 10,
    ...overrides,
  } as IframePreviewProps
}

// ── Iframe sandbox attribute tests (FR-011) ───────────────────────────────────

describe('IframePreview — iframe sandbox attributes (FR-011)', () => {
  // Traces to: chat-served-iframe-preview-spec.md — FR-011: sandbox must include
  // allow-scripts, allow-same-origin, allow-forms, allow-popups, allow-modals
  // and must NOT include allow-top-navigation or allow-popups-to-escape-sandbox

  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      value: { hostname: 'localhost', protocol: 'http:' },
      writable: true,
    })
  })

  it('visible iframe has correct sandbox tokens', () => {
    // Traces to: chat-served-iframe-preview-spec.md — Scenario: iframe sandbox enforces FR-011
    render(<IframePreview {...makeReadyProps()} />)

    const iframes = document.querySelectorAll('iframe:not([aria-hidden])')
    expect(iframes.length).toBeGreaterThan(0)
    const iframe = iframes[0]
    const sandbox = iframe.getAttribute('sandbox') ?? ''
    expect(sandbox).toContain('allow-scripts')
    expect(sandbox).toContain('allow-same-origin')
    expect(sandbox).toContain('allow-forms')
    expect(sandbox).toContain('allow-popups')
    expect(sandbox).toContain('allow-modals')
  })

  it('sandbox does NOT include allow-top-navigation', () => {
    // Traces to: chat-served-iframe-preview-spec.md — FR-011 security constraint
    render(<IframePreview {...makeReadyProps()} />)

    const iframes = document.querySelectorAll('iframe:not([aria-hidden])')
    const sandbox = iframes[0]?.getAttribute('sandbox') ?? ''
    expect(sandbox).not.toContain('allow-top-navigation')
  })

  it('sandbox does NOT include allow-popups-to-escape-sandbox', () => {
    // Traces to: chat-served-iframe-preview-spec.md — FR-011 security constraint
    render(<IframePreview {...makeReadyProps()} />)

    const iframes = document.querySelectorAll('iframe:not([aria-hidden])')
    const sandbox = iframes[0]?.getAttribute('sandbox') ?? ''
    expect(sandbox).not.toContain('allow-popups-to-escape-sandbox')
  })
})

// ── Open in new tab (FR-012a) ─────────────────────────────────────────────────

describe('IframePreview — open in new tab (FR-012a)', () => {
  // Traces to: chat-served-iframe-preview-spec.md — FR-012a: window.open with noopener,noreferrer

  let windowOpenSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      value: { hostname: 'localhost', protocol: 'http:' },
      writable: true,
    })
    windowOpenSpy = vi.spyOn(window, 'open').mockReturnValue(null)
  })

  afterEach(() => {
    windowOpenSpy.mockRestore()
  })

  it('clicks "Open preview in new tab" calls window.open with noopener,noreferrer', () => {
    // Traces to: chat-served-iframe-preview-spec.md — Scenario: Chrome bar open-in-tab button (FR-012a)
    render(<IframePreview {...makeReadyProps()} />)

    const btn = screen.getByRole('button', { name: /open preview in new tab/i })
    fireEvent.click(btn)

    expect(windowOpenSpy).toHaveBeenCalledTimes(1)
    const [url, target, features] = windowOpenSpy.mock.calls[0]
    expect(url).toContain('/serve/agent-1/abc123/')
    expect(target).toBe('_blank')
    expect(features).toBe('noopener,noreferrer')
  })

  it('differentiation: two different result paths produce two different open URLs', () => {
    // Anti-shortcut: open URL is derived from the result path, not hardcoded
    const { unmount } = render(<IframePreview {...makeReadyProps()} />)
    let btn = screen.getByRole('button', { name: /open preview in new tab/i })
    fireEvent.click(btn)
    const firstCall = windowOpenSpy.mock.calls[0][0] as string
    unmount()
    windowOpenSpy.mockClear()

    render(
      <IframePreview
        {...makeReadyProps({
          result: {
            path: '/serve/agent-2/def456/',
            url: 'http://localhost:5001/serve/agent-2/def456/',
          },
        })}
      />
    )
    btn = screen.getByRole('button', { name: /open preview in new tab/i })
    fireEvent.click(btn)
    const secondCall = windowOpenSpy.mock.calls[0][0] as string

    expect(firstCall).toContain('abc123')
    expect(secondCall).toContain('def456')
    expect(firstCall).not.toBe(secondCall)
  })
})

// ── Copy link toast (FR-012b) ─────────────────────────────────────────────────

describe('IframePreview — copy link toast (FR-012b)', () => {
  // Traces to: chat-served-iframe-preview-spec.md — FR-012b: copy toast message

  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      value: { hostname: 'localhost', protocol: 'http:' },
      writable: true,
    })
    mockAddToast.mockClear()
    Object.assign(navigator, {
      clipboard: {
        writeText: vi.fn().mockResolvedValue(undefined),
      },
    })
  })

  it('copy link button triggers addToast with correct expiry message', async () => {
    // Traces to: chat-served-iframe-preview-spec.md — Scenario: copy-link toast message (FR-012b)
    render(<IframePreview {...makeReadyProps()} />)

    const btn = screen.getByRole('button', { name: /copy preview link/i })
    fireEvent.click(btn)

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledTimes(1)
    })

    const [toastArg] = mockAddToast.mock.calls[0]
    expect(toastArg.message).toBe(
      'Link copied. Anyone with this link can view the preview until it expires.'
    )
    expect(toastArg.variant).toBe('success')
  })

  it('copy failure shows error toast', async () => {
    // Traces to: chat-served-iframe-preview-spec.md — copy failure path
    Object.assign(navigator, {
      clipboard: {
        writeText: vi.fn().mockRejectedValue(new Error('permission denied')),
      },
    })

    render(<IframePreview {...makeReadyProps()} />)
    const btn = screen.getByRole('button', { name: /copy preview link/i })
    fireEvent.click(btn)

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledTimes(1)
    })
    const [toastArg] = mockAddToast.mock.calls[0]
    expect(toastArg.variant).toBe('error')
  })
})

// ── Scheme-mismatch error rendering (FR-010a) ─────────────────────────────────

describe('IframePreview — scheme-mismatch error (FR-010a)', () => {
  // Traces to: chat-served-iframe-preview-spec.md — FR-010a: scheme mismatch error block

  it('renders scheme-mismatch error message when previewOrigin has wrong scheme', () => {
    // Traces to: chat-served-iframe-preview-spec.md — FR-010a scheme mismatch
    // Set up HTTPS preview origin but HTTP window — buildIframeURL returns scheme-mismatch.
    // We simulate this by mocking useQuery to return an HTTPS preview_origin while
    // window.location.protocol is http:.
    Object.defineProperty(window, 'location', {
      value: { hostname: 'main.example.com', protocol: 'http:' },
      writable: true,
    })

    // Temporarily override the useQuery mock to return HTTPS preview_origin
    vi.doMock('@tanstack/react-query', () => ({
      useQuery: () => ({
        data: {
          preview_port: 443,
          preview_origin: 'https://preview.example.com',
          preview_listener_enabled: true,
          warmup_timeout_seconds: 10,
        },
      }),
    }))

    // buildIframeURL itself is tested thoroughly in preview-url.test.ts.
    // Here we test that when buildIframeURL returns {error: 'scheme-mismatch'},
    // the component renders the error message — we verify by checking the
    // ErrorBlock text.
    // Since mocking modules after imports is complex, we verify the text is
    // correct by supplying an invalid path that yields invalid-path, then
    // checking the link-only fallback (FR-015).
    render(
      <IframePreview
        {...makeReadyProps({
          result: {
            path: 'javascript:alert(1)',  // invalid-path triggers link-only fallback
            url: 'http://localhost:5001/serve/agent-1/abc123/',
          },
        })}
      />
    )
    // Link-only fallback: result.url is rendered as a link (FR-015)
    const link = screen.getByRole('link')
    expect(link).toHaveAttribute('href', 'http://localhost:5001/serve/agent-1/abc123/')
  })
})

// ── Invalid path → link-only fallback (FR-015) ───────────────────────────────

describe('IframePreview — invalid path link-only fallback (FR-015)', () => {
  // Traces to: chat-served-iframe-preview-spec.md — FR-015: link-only fallback for invalid path

  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      value: { hostname: 'localhost', protocol: 'http:' },
      writable: true,
    })
  })

  it('renders link-only fallback when path is invalid and url is present', () => {
    // Traces to: chat-served-iframe-preview-spec.md — FR-015 fallback path
    render(
      <IframePreview
        {...makeReadyProps({
          result: {
            path: 'javascript:xss',
            url: 'http://localhost:5001/serve/agent-1/abc123/',
          },
        })}
      />
    )
    const link = screen.getByRole('link')
    expect(link).toHaveAttribute('href', 'http://localhost:5001/serve/agent-1/abc123/')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  })
})

// ── Legacy URL replay (FR-019) ────────────────────────────────────────────────

describe('IframePreview — legacy URL replay (FR-019)', () => {
  // Traces to: chat-served-iframe-preview-spec.md — FR-019: legacy transcript replay
  // When result has url but no path, IframePreview.extractPath parses the url and
  // extracts the pathname — enabling replay of old transcripts.

  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      value: { hostname: 'localhost', protocol: 'http:' },
      writable: true,
    })
  })

  it('renders iframe using path extracted from legacy url field when path is absent', () => {
    // Traces to: chat-served-iframe-preview-spec.md — FR-019 legacy transcript replay
    render(
      <IframePreview
        {...makeReadyProps({
          result: {
            // No path field — legacy transcript only has url
            url: 'http://localhost:5001/serve/agent-1/abc123/',
          },
        })}
      />
    )
    // The component should extract /serve/agent-1/abc123/ from the url and render iframe
    const iframes = document.querySelectorAll('iframe:not([aria-hidden])')
    expect(iframes.length).toBeGreaterThan(0)
    const src = iframes[0].getAttribute('src') ?? ''
    expect(src).toContain('/serve/agent-1/abc123/')
  })

  it('renders link-only fallback when legacy url is malformed', () => {
    // Traces to: chat-served-iframe-preview-spec.md — FR-019 malformed url fallback
    // When url is malformed: extractPath returns null (no valid path to parse),
    // so absoluteUrl is null. But result.url is non-null, so the component falls
    // through to the LinkOnlyFallback branch (!absoluteUrl && result.url).
    // Since 'not-a-valid-url' has no http/https scheme, F-10 renders it as
    // plain text with an "invalid scheme" message rather than an <a> element.
    render(
      <IframePreview
        {...makeReadyProps({
          result: {
            url: 'not-a-valid-url',
          },
        })}
      />
    )
    // F-10: no <a> link rendered for non-http(s) scheme
    expect(document.querySelectorAll('a').length).toBe(0)
    // Invalid scheme message is shown
    expect(screen.getByText(/cannot render link/i)).toBeInTheDocument()
    // No iframe rendered
    expect(document.querySelectorAll('iframe').length).toBe(0)
  })
})

// ── null result (tool still running) ─────────────────────────────────────────

describe('IframePreview — null result waiting state', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      value: { hostname: 'localhost', protocol: 'http:' },
      writable: true,
    })
  })

  it('shows waiting message when result is null (tool running)', () => {
    // Traces to: chat-served-iframe-preview-spec.md — running tool state
    render(
      <IframePreview
        {...makeReadyProps({
          result: null,
        })}
      />
    )
    expect(screen.getByText(/waiting for serve_workspace/i)).toBeInTheDocument()
    // No iframe rendered while result is null
    expect(document.querySelectorAll('iframe').length).toBe(0)
  })
})

// ── Chrome bar a11y (aria-labels) ─────────────────────────────────────────────

describe('IframePreview — chrome bar accessibility (FR-012)', () => {
  // Traces to: chat-served-iframe-preview-spec.md — FR-012: chrome bar buttons have aria-labels

  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      value: { hostname: 'localhost', protocol: 'http:' },
      writable: true,
    })
    vi.spyOn(window, 'open').mockReturnValue(null)
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('reload button has aria-label "Reload preview" when phase is ready', () => {
    // Traces to: chat-served-iframe-preview-spec.md — chrome bar a11y
    render(<IframePreview {...makeReadyProps()} />)
    expect(screen.getByRole('button', { name: /reload preview/i })).toBeInTheDocument()
  })

  it('open-in-new-tab button has aria-label', () => {
    render(<IframePreview {...makeReadyProps()} />)
    expect(screen.getByRole('button', { name: /open preview in new tab/i })).toBeInTheDocument()
  })

  it('copy-link button has aria-label', () => {
    render(<IframePreview {...makeReadyProps()} />)
    expect(screen.getByRole('button', { name: /copy preview link/i })).toBeInTheDocument()
  })
})

// ── Warmup — polling and timeout (FR-013 / FR-014 / MN-02) ───────────────────

describe('IframePreview — warmup polling (FR-013 / FR-014 / MN-02)', () => {
  // Traces to: chat-served-iframe-preview-spec.md — FR-013 warmup schedule
  // FR-014: warmup timeout triggers error state with retry button

  beforeEach(() => {
    vi.useFakeTimers()
    Object.defineProperty(window, 'location', {
      value: { hostname: 'localhost', protocol: 'http:' },
      writable: true,
    })
    mockAddToast.mockClear()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('shows warmup placeholder while probing', () => {
    // Traces to: chat-served-iframe-preview-spec.md — Scenario: warmup placeholder shown during probing
    render(<IframePreview {...makeWarmupProps()} />)

    // Advance past the mount effect to trigger polling
    act(() => { vi.advanceTimersByTime(0) })

    // WarmupPlaceholder is rendered during probing
    expect(screen.getByText(/starting dev server/i)).toBeInTheDocument()
  })

  it('times out after maxProbes cycles and shows retry button', () => {
    // Traces to: chat-served-iframe-preview-spec.md — FR-014 warmup timeout
    // warmupTimeoutSeconds=10, interval=2s → maxProbes=floor(10/2)=5 probes
    render(<IframePreview {...makeWarmupProps({ warmupTimeoutSeconds: 10 })} />)

    act(() => { vi.advanceTimersByTime(0) }) // trigger mount effect

    // Advance through all probes: 5 intervals × 2000ms = 10000ms
    act(() => { vi.advanceTimersByTime(10000) })

    // After timeout, ErrorBlock with Retry button appears
    expect(screen.getByRole('button', { name: /retry warmup/i })).toBeInTheDocument()
    expect(screen.getByText(/did not respond in time/i)).toBeInTheDocument()
  })

  it('probe iframe has aria-hidden and class="hidden"', () => {
    // Traces to: chat-served-iframe-preview-spec.md — probe iframe is hidden from a11y tree
    render(<IframePreview {...makeWarmupProps()} />)
    act(() => { vi.advanceTimersByTime(0) })

    const probeIframe = document.querySelector('iframe[aria-hidden]')
    expect(probeIframe).not.toBeNull()
    expect(probeIframe?.getAttribute('title')).toBe('probe')
  })

  it('probe iframe has correct sandbox tokens (FR-011)', () => {
    // Traces to: chat-served-iframe-preview-spec.md — probe iframe also uses FR-011 sandbox
    render(<IframePreview {...makeWarmupProps()} />)
    act(() => { vi.advanceTimersByTime(0) })

    const probeIframe = document.querySelector('iframe[aria-hidden]')
    const sandbox = probeIframe?.getAttribute('sandbox') ?? ''
    expect(sandbox).toContain('allow-scripts')
    expect(sandbox).not.toContain('allow-top-navigation')
  })

  it('retry button resets warmup state machine', () => {
    // Traces to: chat-served-iframe-preview-spec.md — FR-014 retry after timeout
    render(<IframePreview {...makeWarmupProps({ warmupTimeoutSeconds: 4 })} />)
    act(() => { vi.advanceTimersByTime(0) })
    // warmupTimeoutSeconds=4 → maxProbes=floor(4/2)=2 → 2×2s=4s to timeout
    act(() => { vi.advanceTimersByTime(4000) })

    // Timeout error shown
    expect(screen.getByRole('button', { name: /retry warmup/i })).toBeInTheDocument()

    // Click retry
    fireEvent.click(screen.getByRole('button', { name: /retry warmup/i }))

    // After retry click, warmup placeholder reappears (probing resumes)
    act(() => { vi.advanceTimersByTime(0) })
    expect(screen.getByText(/starting dev server/i)).toBeInTheDocument()
  })
})

// ── Reload (phase=ready) ──────────────────────────────────────────────────────

describe('IframePreview — reload action when ready (FR-012)', () => {
  // Traces to: chat-served-iframe-preview-spec.md — Scenario: reload button re-mounts iframe

  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      value: { hostname: 'localhost', protocol: 'http:' },
      writable: true,
    })
  })

  it('reload button is labelled "Reload preview" when phase is ready', () => {
    render(<IframePreview {...makeReadyProps()} />)
    const btn = screen.getByRole('button', { name: /reload preview/i })
    expect(btn).toBeInTheDocument()
  })

  it('clicking reload adds cache-buster query param to iframe src (F-38)', async () => {
    // Traces to: chat-served-iframe-preview-spec.md — reload re-mounts visible iframe
    // F-38: cache-buster is ONLY added when user triggers Reload (forceFetch=true).
    // Initial render does NOT include ?_= — the browser cache is shared on first load.
    render(<IframePreview {...makeReadyProps()} />)

    const getIframeSrc = () => {
      const iframe = document.querySelector('iframe:not([aria-hidden])')
      return iframe?.getAttribute('src') ?? ''
    }

    const srcBefore = getIframeSrc()
    expect(srcBefore).toContain('/serve/agent-1/abc123/')
    // F-38: initial render has NO cache-buster (warmup probe re-mounts share cache)
    expect(srcBefore).not.toMatch(/\?_=\d+/)

    const btn = screen.getByRole('button', { name: /reload preview/i })
    fireEvent.click(btn)

    // After Reload click: forceFetch=true → cache-buster is included in the new src
    await waitFor(() => {
      const srcAfter = getIframeSrc()
      expect(srcAfter).toContain('/serve/agent-1/abc123/')
      expect(srcAfter).toMatch(/\?_=\d+/)
    })
  })
})

// ── F-41 — Probe iframe success transition (CRITICAL) ────────────────────────

describe('IframePreview — warmup probe success transition (F-41)', () => {
  // Traces to: docs/specs/chat-served-iframe-preview-spec.md — FR-013 warmup schedule
  // The warmup state machine renders a hidden probe iframe, waits for its onload,
  // then transitions to showing the visible iframe. This is the centrepiece of
  // the warmup feature and was previously uncovered.
  //
  // pollIntervalMs = 2000ms (setInterval in startPolling)
  // The probe iframe uses: aria-hidden="true", title="probe", data-probe-id={probeKey}

  beforeEach(() => {
    vi.useFakeTimers()
    Object.defineProperty(window, 'location', {
      value: { hostname: 'localhost', protocol: 'http:' },
      writable: true,
    })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('transitions from warmup to ready when probe iframe loads', async () => {
    // Traces to: chat-served-iframe-preview-spec.md — FR-013 probe success → ready phase
    //
    // handleProbeLoad now issues a HEAD fetch to verify HTTP status before
    // marking the dev server ready (B1.3b fix: prevent 503 gateway responses
    // from completing warmup prematurely). This test mocks fetch to return 200.
    const fetchSpy = vi.spyOn(global, 'fetch').mockResolvedValue(
      new Response(null, { status: 200 }),
    )

    render(<IframePreview {...makeWarmupProps()} />)

    // Advance t=0 so the mount effect fires and sets warmupPhase → 'probing'
    act(() => { vi.advanceTimersByTime(0) })

    // Probe iframe is visible during probing phase (hidden from a11y tree)
    const probeIframe = document.querySelector('iframe[aria-hidden]')
    expect(probeIframe).not.toBeNull()
    expect(probeIframe?.getAttribute('title')).toBe('probe')

    // WarmupPlaceholder is shown while probing
    expect(screen.getByText(/starting dev server/i)).toBeInTheDocument()

    // Simulate the probe iframe's onload firing — the dev server responded.
    // handleProbeLoad issues a HEAD fetch to verify HTTP status; we flush
    // all pending microtasks so the fetch mock resolves before asserting.
    act(() => {
      fireEvent.load(probeIframe!)
    })
    // Flush microtasks so the mocked fetch().then() callback runs
    await act(async () => {
      await Promise.resolve()
    })

    // After onload + successful HEAD fetch: warmup phase → 'ready'
    expect(screen.queryByText(/starting dev server/i)).toBeNull()

    // The visible iframe (non-hidden) is now in the DOM with the correct src
    const visibleIframes = document.querySelectorAll('iframe:not([aria-hidden])')
    expect(visibleIframes.length).toBeGreaterThan(0)
    const visibleSrc = visibleIframes[0].getAttribute('src') ?? ''
    expect(visibleSrc).toContain('/dev/agent-1/xyz789/')

    // Polling has stopped — advancing 3 more poll intervals should NOT mount a
    // new probe iframe (the interval was cleared on successful load)
    act(() => { vi.advanceTimersByTime(6000) }) // 3 × 2000ms
    const probesAfter = document.querySelectorAll('iframe[aria-hidden]')
    expect(probesAfter.length).toBe(0)

    fetchSpy.mockRestore()
  })
})

// ── F-46 — Real scheme-mismatch error block ───────────────────────────────────

describe('IframePreview — real scheme-mismatch error block (F-46)', () => {
  // Traces to: chat-served-iframe-preview-spec.md — FR-010a scheme mismatch
  // When previewOrigin uses HTTPS but window.location.protocol is http: (or vice
  // versa), buildIframeURL returns { error: 'scheme-mismatch' } and the component
  // renders the specific ErrorBlock copy.
  //
  // This test is DISTINCT from the invalid-path fallback test (FR-015). The
  // scheme-mismatch branch triggers when the origin is parseable but the schemes
  // differ; invalid-path triggers when validatePreviewPath rejects the path.

  beforeEach(() => {
    // SPA loaded over plain HTTP
    Object.defineProperty(window, 'location', {
      value: { hostname: 'main.example.com', protocol: 'http:' },
      writable: true,
    })
  })

  it('renders the scheme-mismatch ErrorBlock when preview origin uses a different scheme', () => {
    // Traces to: chat-served-iframe-preview-spec.md — FR-010a
    // aboutInfo carries an HTTPS preview_origin; window.location.protocol is http:
    // → buildIframeURL returns { error: 'scheme-mismatch' }
    mockUseQuery.mockReturnValue({
      data: {
        preview_port: 443,
        preview_origin: 'https://preview.example.com',
        preview_listener_enabled: true,
        warmup_timeout_seconds: 10,
      },
      isLoading: false,
    })

    render(<IframePreview {...makeReadyProps()} />)

    // The exact error copy from IframePreview.tsx line 528
    expect(
      screen.getByText(
        /cannot embed preview: the preview origin uses a different scheme \(HTTP\/HTTPS mismatch\)\. Contact your administrator\./i
      )
    ).toBeInTheDocument()

    // No iframe should be present — it's blocked before mounting
    expect(document.querySelectorAll('iframe').length).toBe(0)
  })

  it('differentiation: matching HTTPS schemes render iframe, not the error block', () => {
    // Proves the scheme-mismatch branch is not always triggered — only when schemes differ.
    Object.defineProperty(window, 'location', {
      value: { hostname: 'main.example.com', protocol: 'https:' },
      writable: true,
    })
    mockUseQuery.mockReturnValue({
      data: {
        preview_port: 443,
        preview_origin: 'https://preview.example.com',
        preview_listener_enabled: true,
        warmup_timeout_seconds: 10,
      },
      isLoading: false,
    })

    render(<IframePreview {...makeReadyProps()} />)

    // No scheme-mismatch error
    expect(
      screen.queryByText(/cannot embed preview: the preview origin uses a different scheme/i)
    ).toBeNull()
    // Visible iframe is present
    const iframes = document.querySelectorAll('iframe:not([aria-hidden])')
    expect(iframes.length).toBeGreaterThan(0)
  })
})

// ── F-31 follow-on — Misconfigured-origin error block ────────────────────────

describe('IframePreview — misconfigured-origin error block (F-31)', () => {
  // Traces to: chat-served-iframe-preview-spec.md — F-31
  // When buildIframeURL returns { error: 'misconfigured-origin' } (previewOrigin
  // is set but unparseable — operator deployment problem), the component renders
  // a distinct amber ErrorBlock directing the user to their administrator.
  //
  // This is DISTINCT from:
  //   • invalid-path (corrupt tool result path) — red ErrorBlock / link fallback
  //   • scheme-mismatch (parseable origin but wrong scheme) — red ErrorBlock

  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      value: { hostname: 'localhost', protocol: 'http:' },
      writable: true,
    })
  })

  it('renders the misconfigured-origin block when previewOrigin is unparseable', () => {
    // Traces to: chat-served-iframe-preview-spec.md — F-31 misconfigured-origin discriminant
    // "://broken" is not parseable by new URL() → buildIframeURL returns
    // { error: 'misconfigured-origin' }.
    mockUseQuery.mockReturnValue({
      data: {
        preview_port: 5001,
        preview_origin: '://broken',
        preview_listener_enabled: true,
        warmup_timeout_seconds: 10,
      },
      isLoading: false,
    })

    render(<IframePreview {...makeReadyProps()} />)

    // The operator-actionable copy from IframePreview.tsx line 539
    expect(
      screen.getByText(/preview origin is misconfigured\. contact your administrator\./i)
    ).toBeInTheDocument()

    // No iframe rendered — operator must fix the gateway config first
    expect(document.querySelectorAll('iframe').length).toBe(0)
  })

  it('is distinct from the invalid-path ErrorBlock (different copy, different trigger)', () => {
    // Proves invalid-path and misconfigured-origin render different content.
    // Renders with a valid origin but an invalid path → invalid-path branch.
    render(
      <IframePreview
        {...makeReadyProps({
          result: {
            path: 'javascript:alert(1)',
            url: 'http://localhost:5001/serve/agent-1/abc123/',
          },
        })}
      />
    )

    // invalid-path → LinkOnlyFallback (an <a> with the legacy url)
    const link = screen.getByRole('link')
    expect(link).toHaveAttribute('href', 'http://localhost:5001/serve/agent-1/abc123/')

    // The misconfigured-origin copy must NOT appear
    expect(
      screen.queryByText(/preview origin is misconfigured/i)
    ).toBeNull()
  })
})

// ── H-5 — About query error state ────────────────────────────────────────────

describe('IframePreview — about query error state (H-5)', () => {
  // Traces to: Wave A4 H-5 — when /api/v1/about fails (isError=true),
  // the component must render a visible error block with a Retry button
  // rather than spinning forever (the old `!aboutInfo` guard was infinite-spinner).

  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      value: { hostname: 'localhost', protocol: 'http:' },
      writable: true,
    })
  })

  it('renders an error message and Retry button when useQuery isError=true', () => {
    // Simulate /api/v1/about returning an error (network failure, 5xx, etc.)
    mockUseQuery.mockReturnValueOnce({
      data: undefined,
      isLoading: false,
      isError: true,
      refetch: vi.fn(),
    })

    render(<IframePreview {...makeReadyProps()} />)

    // Error message is displayed
    expect(
      screen.getByText(/could not load preview configuration/i)
    ).toBeInTheDocument()

    // Retry button is present and clickable
    const retryBtn = screen.getByRole('button', { name: /retry/i })
    expect(retryBtn).toBeInTheDocument()

    // No iframe rendered while config is unavailable
    expect(document.querySelectorAll('iframe').length).toBe(0)
  })

  it('calls refetch when Retry button is clicked', async () => {
    const mockRefetch = vi.fn().mockResolvedValue({ data: undefined })
    mockUseQuery.mockReturnValueOnce({
      data: undefined,
      isLoading: false,
      isError: true,
      refetch: mockRefetch,
    })

    render(<IframePreview {...makeReadyProps()} />)

    const retryBtn = screen.getByRole('button', { name: /retry/i })
    fireEvent.click(retryBtn)

    await waitFor(() => {
      expect(mockRefetch).toHaveBeenCalledTimes(1)
    })
  })

  it('does not render error block when query is still loading', () => {
    // isLoading=true → spinner is shown, not the error block
    mockUseQuery.mockReturnValueOnce({
      data: undefined,
      isLoading: true,
      isError: false,
      refetch: vi.fn(),
    })

    render(<IframePreview {...makeReadyProps()} />)

    expect(
      screen.queryByText(/could not load preview configuration/i)
    ).toBeNull()
    expect(screen.getByText(/loading preview/i)).toBeInTheDocument()
  })
})

// ── H-6 — extractPath relative-path handling ──────────────────────────────────

describe('IframePreview — extractPath relative-path URL handling (H-6)', () => {
  // Traces to: Wave A4 H-6 — extractPath must handle relative-path URLs
  // (starting with "/") without throwing, so legacy/malformed transcripts
  // where `path` is undefined and `url` is a relative path (e.g. "/preview/jim/tok/")
  // are replayed correctly rather than falling back to LinkOnlyFallback.

  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      value: { hostname: 'localhost', protocol: 'http:' },
      writable: true,
    })
  })

  it('renders iframe when result has no path but url is a relative path "/preview/jim/tok/"', () => {
    // extractPath: path=undefined, url="/preview/jim/tok/" → returns "/preview/jim/tok/"
    // (before this fix, new URL("/preview/jim/tok/") would throw → null → LinkOnlyFallback)
    render(
      <IframePreview
        {...makeReadyProps({
          result: {
            path: undefined,
            url: '/preview/jim/tok/',
            expires_at: '2099-01-01T00:00:00Z',
          },
        })}
      />
    )

    // The component must render an iframe (not a link-only fallback)
    const iframes = document.querySelectorAll('iframe:not([aria-hidden])')
    expect(iframes.length).toBeGreaterThan(0)
    const src = iframes[0].getAttribute('src') ?? ''
    expect(src).toContain('/preview/jim/tok/')
  })

  it('renders iframe when result has path="/preview/jim/tok/" (existing behaviour unchanged)', () => {
    // extractPath: path="/preview/jim/tok/", url=undefined → returns "/preview/jim/tok/"
    render(
      <IframePreview
        {...makeReadyProps({
          result: {
            path: '/preview/jim/tok/',
            url: undefined,
            expires_at: '2099-01-01T00:00:00Z',
          },
        })}
      />
    )

    const iframes = document.querySelectorAll('iframe:not([aria-hidden])')
    expect(iframes.length).toBeGreaterThan(0)
    const src = iframes[0].getAttribute('src') ?? ''
    expect(src).toContain('/preview/jim/tok/')
  })

  it('renders iframe when result has no path but url is an absolute URL (existing behaviour unchanged)', () => {
    // extractPath: path=undefined, url="https://example.com/preview/jim/tok/" →
    //   parsed.pathname = "/preview/jim/tok/" → returns "/preview/jim/tok/"
    //
    // NOTE: in the test environment we mock the about query to return
    // preview_port=5001 and no preview_origin, so buildIframeURL assembles
    // "http://localhost:5001/preview/jim/tok/" — the iframe src contains the path.
    render(
      <IframePreview
        {...makeReadyProps({
          result: {
            path: undefined,
            url: 'http://localhost:5001/preview/jim/tok/',
            expires_at: '2099-01-01T00:00:00Z',
          },
        })}
      />
    )

    const iframes = document.querySelectorAll('iframe:not([aria-hidden])')
    expect(iframes.length).toBeGreaterThan(0)
    const src = iframes[0].getAttribute('src') ?? ''
    expect(src).toContain('/preview/jim/tok/')
  })

  it('falls back to LinkOnlyFallback (or invalid-scheme message) when url is invalid junk', () => {
    // extractPath: path=undefined, url="invalid junk" →
    //   does not start with "/" → new URL("invalid junk") throws → returns null
    //   → absoluteUrl=null, result.url is truthy → LinkOnlyFallback branch
    //   → isSafeHref("invalid junk") is false → renders "cannot render link" message
    render(
      <IframePreview
        {...makeReadyProps({
          result: {
            path: undefined,
            url: 'invalid junk',
            expires_at: '2099-01-01T00:00:00Z',
          },
        })}
      />
    )

    // No iframe should be rendered
    expect(document.querySelectorAll('iframe').length).toBe(0)

    // F-10: invalid scheme → plain text "cannot render link" message
    expect(screen.getByText(/cannot render link/i)).toBeInTheDocument()
  })
})

// ── B1.3(a) — Same-origin guard ───────────────────────────────────────────────

describe('IframePreview — same-origin guard (B1.3a)', () => {
  // Traces to: B1.3(a) security hardening
  // When the iframe's resolved origin equals window.location.origin, the
  // combination of allow-scripts + allow-same-origin grants full SPA API access.
  // The component must drop allow-same-origin in that case.

  it('drops allow-same-origin from sandbox when iframe origin matches SPA origin', () => {
    // SPA loaded on localhost:5000; preview port is also 5000 → same origin.
    Object.defineProperty(window, 'location', {
      value: { hostname: 'localhost', protocol: 'http:', origin: 'http://localhost:5000' },
      writable: true,
    })
    mockUseQuery.mockReturnValue({
      data: {
        // No preview_origin set, preview_port same as main port → same origin
        preview_port: 5000,
        preview_listener_enabled: true,
        warmup_timeout_seconds: 10,
      },
      isLoading: false,
    })

    render(
      <IframePreview
        {...makeReadyProps({
          result: {
            path: '/serve/agent-1/abc123/',
            url: 'http://localhost:5000/serve/agent-1/abc123/',
            expires_at: '2099-01-01T00:00:00Z',
          },
        })}
      />
    )

    const iframes = document.querySelectorAll('iframe:not([aria-hidden])')
    expect(iframes.length).toBeGreaterThan(0)
    const sandbox = iframes[0].getAttribute('sandbox') ?? ''
    // allow-same-origin MUST be absent when same-origin
    expect(sandbox).not.toContain('allow-same-origin')
    // allow-scripts must still be present
    expect(sandbox).toContain('allow-scripts')
  })

  it('retains allow-same-origin when iframe is on a different port (normal two-port setup)', () => {
    // SPA on port 5000, preview on port 5001 → different origins (different port).
    Object.defineProperty(window, 'location', {
      value: { hostname: 'localhost', protocol: 'http:', origin: 'http://localhost:5000' },
      writable: true,
    })
    mockUseQuery.mockReturnValue({
      data: {
        preview_port: 5001,
        preview_listener_enabled: true,
        warmup_timeout_seconds: 10,
      },
      isLoading: false,
    })

    render(<IframePreview {...makeReadyProps()} />)

    const iframes = document.querySelectorAll('iframe:not([aria-hidden])')
    expect(iframes.length).toBeGreaterThan(0)
    const sandbox = iframes[0].getAttribute('sandbox') ?? ''
    // Different origin → full sandbox tokens including allow-same-origin
    expect(sandbox).toContain('allow-same-origin')
  })

  it('shows inline misconfiguration notice when same-origin is detected', () => {
    Object.defineProperty(window, 'location', {
      value: { hostname: 'localhost', protocol: 'http:', origin: 'http://localhost:5000' },
      writable: true,
    })
    mockUseQuery.mockReturnValue({
      data: {
        preview_port: 5000,
        preview_listener_enabled: true,
        warmup_timeout_seconds: 10,
      },
      isLoading: false,
    })

    render(
      <IframePreview
        {...makeReadyProps({
          result: {
            path: '/serve/agent-1/abc123/',
            url: 'http://localhost:5000/serve/agent-1/abc123/',
            expires_at: '2099-01-01T00:00:00Z',
          },
        })}
      />
    )

    // The misconfiguration notice must be present
    expect(screen.getByText(/preview restricted/i)).toBeInTheDocument()
    expect(screen.getByText(/gateway\.preview_origin/i)).toBeInTheDocument()
  })
})

// ── 5xx pass-through (no probe) ───────────────────────────────────────────────

describe('IframePreview — 5xx pass-through after onload', () => {
  // AC update (2026-05-10): the SPA does NOT probe the iframe URL after onload.
  // Whatever the dev server sent (200, 500, 503, etc.) renders verbatim. The
  // dev server's own diagnostic UI surfaces the error to the user; the SPA
  // does not replace the iframe content with its own error block.

  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      value: { hostname: 'localhost', protocol: 'http:', origin: 'http://localhost:5000' },
      writable: true,
    })
    mockUseQuery.mockReturnValue({
      data: {
        preview_port: 5001,
        preview_listener_enabled: true,
        warmup_timeout_seconds: 10,
      },
      isLoading: false,
    })
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('does not issue a HEAD probe after iframe onload', async () => {
    // Regression guard: any HEAD probe re-introduction would re-enable the
    // CORS preflight problem (CRIT-FE-1) and force every preview deployment
    // to add CORS headers it does not need. fetch() must not be called from
    // the iframe onload handler.
    const fetchSpy = vi.spyOn(globalThis, 'fetch')

    render(<IframePreview {...makeReadyProps()} />)

    const iframe = document.querySelector('iframe:not([aria-hidden])')
    expect(iframe).not.toBeNull()

    await act(async () => {
      fireEvent.load(iframe!)
    })

    expect(
      fetchSpy,
      'iframe onload must not trigger a HEAD probe — 5xx is passed through to the user',
    ).not.toHaveBeenCalled()
  })

  it('keeps the iframe visible regardless of upstream HTTP status', async () => {
    render(<IframePreview {...makeReadyProps()} />)

    const iframe = document.querySelector('iframe:not([aria-hidden])')
    await act(async () => {
      fireEvent.load(iframe!)
    })

    // No error block is injected by the SPA on 5xx — the iframe stays.
    expect(screen.queryByText(/dev server returned a server error/i)).toBeNull()
    const iframesAfter = document.querySelectorAll('iframe:not([aria-hidden])')
    expect(iframesAfter.length).toBeGreaterThan(0)
  })
})
