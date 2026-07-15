/**
 * IframePreview component tests (ADR-044 — preview-on-main-listener)
 *
 * Traces to: docs/internal/specs/preview-on-main-listener-spec.md
 * FR-016, FR-017, US-9 — dev/serve_web previews render as a clickable LINK;
 * they are NEVER embedded as a same-origin iframe.
 *
 * IframePreview no longer reads /api/v1/about (no separate preview
 * origin/port to resolve — preview shares the SPA's own origin), so there is
 * no useQuery/fetchAboutInfo mock required here.
 *
 * Strategy:
 *   - Mock @/store/ui (addToast spy)
 *   - Mock navigator.clipboard for copy-link test
 *   - Mock window.open for open-in-new-tab test
 *   - Mock global.fetch for dev-mode warmup polling (same-origin HEAD probe)
 *   - Use vi.useFakeTimers() for warmup interval testing
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { IframePreview, type IframePreviewProps } from './IframePreview'

// ── Mocks ─────────────────────────────────────────────────────────────────────

const mockAddToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: mockAddToast }),
}))

// ── Helpers ───────────────────────────────────────────────────────────────────

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type LooseProps = Record<string, any>

function makeReadyProps(overrides: LooseProps = {}): IframePreviewProps {
  return {
    kind: 'serve_workspace',
    result: {
      path: '/serve/agent-1/abc123/',
      url: 'http://localhost:5000/serve/agent-1/abc123/',
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
      url: 'http://localhost:5000/dev/agent-1/xyz789/',
      expires_at: '2099-01-01T00:00:00Z',
      command: 'npm start',
      port: 3000,
    },
    warmupTimeoutSeconds: 10,
    ...overrides,
  } as IframePreviewProps
}

function setLocation(overrides: Partial<Location> = {}) {
  Object.defineProperty(window, 'location', {
    value: {
      hostname: 'localhost',
      protocol: 'http:',
      origin: 'http://localhost:5000',
      port: '5000',
      ...overrides,
    },
    writable: true,
  })
}

// ── Core: link, not iframe (US-9 / FR-016) ───────────────────────────────────

describe('IframePreview — renders a link, never an embedded iframe (US-9 / FR-016)', () => {
  beforeEach(() => setLocation())

  it('renders a clickable link for a ready static (serve_workspace) result', () => {
    render(<IframePreview {...makeReadyProps()} />)

    // No iframe anywhere in the DOM.
    expect(document.querySelectorAll('iframe').length).toBe(0)

    const link = screen.getByTestId('preview-link')
    expect(link.tagName).toBe('A')
    expect(link).toHaveAttribute('href', 'http://localhost:5000/serve/agent-1/abc123/')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  })

  it('differentiation: two different result paths produce two different link hrefs', () => {
    const { unmount } = render(<IframePreview {...makeReadyProps()} />)
    const first = screen.getByTestId('preview-link').getAttribute('href')
    unmount()

    render(
      <IframePreview
        {...makeReadyProps({
          result: {
            path: '/serve/agent-2/def456/',
            url: 'http://localhost:5000/serve/agent-2/def456/',
            expires_at: '2099-01-01T00:00:00Z',
          },
        })}
      />
    )
    const second = screen.getByTestId('preview-link').getAttribute('href')

    expect(first).toContain('abc123')
    expect(second).toContain('def456')
    expect(first).not.toBe(second)
  })

  it('grep guard: no <iframe> element for a dev-mode ready result either', () => {
    render(<IframePreview {...makeWarmupProps()} />)
    expect(document.querySelectorAll('iframe').length).toBe(0)
  })
})

// ── Open in new tab ───────────────────────────────────────────────────────────

describe('IframePreview — open in new tab', () => {
  let windowOpenSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    setLocation()
    windowOpenSpy = vi.spyOn(window, 'open').mockReturnValue(null)
  })

  afterEach(() => {
    windowOpenSpy.mockRestore()
  })

  it('clicking "Open preview in new tab" calls window.open with noopener,noreferrer', () => {
    render(<IframePreview {...makeReadyProps()} />)

    const btn = screen.getByRole('button', { name: /open preview in new tab/i })
    fireEvent.click(btn)

    expect(windowOpenSpy).toHaveBeenCalledTimes(1)
    const [url, target, features] = windowOpenSpy.mock.calls[0]
    expect(url).toContain('/serve/agent-1/abc123/')
    expect(target).toBe('_blank')
    expect(features).toBe('noopener,noreferrer')
  })
})

// ── Copy link toast ───────────────────────────────────────────────────────────

describe('IframePreview — copy link toast', () => {
  beforeEach(() => {
    setLocation()
    mockAddToast.mockClear()
    Object.assign(navigator, {
      clipboard: {
        writeText: vi.fn().mockResolvedValue(undefined),
      },
    })
  })

  it('copy link button triggers addToast with correct expiry message', async () => {
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

// ── Invalid path → link-only fallback ────────────────────────────────────────

describe('IframePreview — invalid path link-only fallback', () => {
  beforeEach(() => setLocation())

  it('renders link-only fallback using the raw legacy url when path is invalid', () => {
    render(
      <IframePreview
        {...makeReadyProps({
          result: {
            path: 'javascript:xss',
            url: 'http://localhost:5000/serve/agent-1/abc123/',
          },
        })}
      />
    )
    const link = screen.getByRole('link')
    expect(link).toHaveAttribute('href', 'http://localhost:5000/serve/agent-1/abc123/')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    expect(document.querySelectorAll('iframe').length).toBe(0)
  })

  it('renders "cannot render link" message when the legacy url has an unsafe scheme', () => {
    render(
      <IframePreview
        {...makeReadyProps({
          result: {
            path: undefined,
            url: 'not-a-valid-url',
            expires_at: '2099-01-01T00:00:00Z',
          },
        })}
      />
    )
    expect(document.querySelectorAll('a').length).toBe(0)
    expect(screen.getByText(/cannot render link/i)).toBeInTheDocument()
    expect(document.querySelectorAll('iframe').length).toBe(0)
  })

  it('renders "Preview URL unavailable" when neither path nor url resolve', () => {
    render(
      <IframePreview
        {...makeReadyProps({
          result: {
            path: 'javascript:xss',
            url: '',
            expires_at: '2099-01-01T00:00:00Z',
          },
        })}
      />
    )
    expect(screen.getByText(/preview url unavailable/i)).toBeInTheDocument()
  })
})

// ── Legacy URL replay (relative url / absolute url without path) ─────────────

describe('IframePreview — legacy URL replay', () => {
  beforeEach(() => setLocation())

  it('resolves a link when result has no path but url is a relative path', () => {
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
    const link = screen.getByTestId('preview-link')
    expect(link).toHaveAttribute('href', 'http://localhost:5000/preview/jim/tok/')
  })

  it('resolves a link when result has an absolute legacy 0.0.0.0 url', () => {
    render(
      <IframePreview
        {...makeReadyProps({
          result: {
            path: undefined,
            url: 'http://0.0.0.0:5000/preview/jim/tok/',
            expires_at: '2099-01-01T00:00:00Z',
          },
        })}
      />
    )
    const link = screen.getByTestId('preview-link')
    expect(link).toHaveAttribute('href', 'http://localhost:5000/preview/jim/tok/')
  })
})

// ── null result (tool still running) ─────────────────────────────────────────

describe('IframePreview — null result waiting state', () => {
  beforeEach(() => setLocation())

  it('shows waiting message when result is null (tool running)', () => {
    render(
      <IframePreview
        {...makeReadyProps({
          result: null,
        })}
      />
    )
    expect(screen.getByText(/waiting for serve_workspace/i)).toBeInTheDocument()
    expect(document.querySelectorAll('iframe').length).toBe(0)
    expect(document.querySelectorAll('a').length).toBe(0)
  })
})

// ── Chrome bar a11y (aria-labels) ─────────────────────────────────────────────

describe('IframePreview — link block accessibility', () => {
  beforeEach(() => {
    setLocation()
    vi.spyOn(window, 'open').mockReturnValue(null)
  })

  afterEach(() => {
    vi.restoreAllMocks()
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

// ── Warmup — same-origin HEAD-poll (no iframe) ───────────────────────────────

describe('IframePreview — dev-mode warmup polling (no probe iframe)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setLocation()
    mockAddToast.mockClear()
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('shows warmup placeholder while probing, with no iframe in the DOM', () => {
    vi.spyOn(global, 'fetch').mockResolvedValue(new Response(null, { status: 503 }))
    render(<IframePreview {...makeWarmupProps()} />)

    act(() => { vi.advanceTimersByTime(0) })

    expect(screen.getByText(/starting dev server/i)).toBeInTheDocument()
    expect(document.querySelectorAll('iframe').length).toBe(0)
  })

  it('transitions from warmup to a ready link once the HEAD probe succeeds', async () => {
    const fetchSpy = vi.spyOn(global, 'fetch').mockResolvedValue(new Response(null, { status: 200 }))

    render(<IframePreview {...makeWarmupProps()} />)

    act(() => { vi.advanceTimersByTime(0) })
    expect(screen.getByText(/starting dev server/i)).toBeInTheDocument()

    // Flush the fetch().then() microtask queued by the immediate probe.
    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(screen.queryByText(/starting dev server/i)).toBeNull()
    const link = screen.getByTestId('preview-link')
    expect(link).toHaveAttribute('href', 'http://localhost:5000/dev/agent-1/xyz789/')
    expect(document.querySelectorAll('iframe').length).toBe(0)

    // fetch is same-origin — no CORS mode is required.
    expect(fetchSpy).toHaveBeenCalledWith('http://localhost:5000/dev/agent-1/xyz789/', { method: 'HEAD' })
  })

  it('times out after maxProbes cycles and shows a retry link', async () => {
    vi.spyOn(global, 'fetch').mockRejectedValue(new Error('connection refused'))

    // warmupTimeoutSeconds=4 → maxProbes=floor(4/2)=2 → 2×2s=4s to timeout
    render(<IframePreview {...makeWarmupProps({ warmupTimeoutSeconds: 4 })} />)

    act(() => { vi.advanceTimersByTime(0) })
    await act(async () => { await Promise.resolve() })

    act(() => { vi.advanceTimersByTime(4000) })
    await act(async () => { await Promise.resolve() })

    expect(screen.getByRole('button', { name: /retry warmup/i })).toBeInTheDocument()
    expect(screen.getByText(/did not respond in time/i)).toBeInTheDocument()
    expect(document.querySelectorAll('iframe').length).toBe(0)
  })

  it('retry button restarts the warmup polling state machine', async () => {
    vi.spyOn(global, 'fetch').mockRejectedValue(new Error('connection refused'))

    render(<IframePreview {...makeWarmupProps({ warmupTimeoutSeconds: 4 })} />)
    act(() => { vi.advanceTimersByTime(0) })
    await act(async () => { await Promise.resolve() })
    act(() => { vi.advanceTimersByTime(4000) })
    await act(async () => { await Promise.resolve() })

    expect(screen.getByRole('button', { name: /retry warmup/i })).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /retry warmup/i }))
    act(() => { vi.advanceTimersByTime(0) })

    expect(screen.getByText(/starting dev server/i)).toBeInTheDocument()
  })

  it('static (serve_workspace) mode never enters a warmup phase — link renders immediately', () => {
    render(<IframePreview {...makeReadyProps()} />)
    expect(screen.queryByText(/starting/i)).toBeNull()
    expect(screen.getByTestId('preview-link')).toBeInTheDocument()
  })
})
