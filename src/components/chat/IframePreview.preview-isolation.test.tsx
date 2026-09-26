/**
 * IframePreview.preview-isolation.test.tsx — RED tests, ADR-094 TDD Plan
 * orders 22 (TestPreviewCardEngineSelection) and 23 (TestPreviewWarmupTarget).
 *
 * Traces to: docs/internal/specs/adr-094-preview-isolation-spec.md
 *   Order 22 (S-8.1, S-8.2, S-8.3; FR-024): userAgentData => Mode 1; Firefox
 *   UA token => Mode 1; WebKit/unknown => Mode 2; NEVER both links.
 *   Order 23 (S-8.4; FR-025): the warmup HEAD probe targets the Mode 2 URL
 *   even when the rendered link is Mode 1 (round-1 MAJ-009).
 *
 * ORACLE INDEPENDENCE: expectations are FR-024's engine rule and S-8.x,
 * transcribed from the spec — not from running the component.
 *
 * RED (current failure mode): IframePreview has no engine-gated dual-URL
 * selection — isolated_url is dropped, so every Mode 1-expecting row fails on
 * behaviour (card renders the Mode 2 link). Mode 2/fallback and probe-target
 * rows are PINS (green today) that must survive GREEN unchanged.
 */

import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { IframePreview } from './IframePreview'

// -- Engine stubbing (FR-024) --------------------------------------------------

type EngineKind = 'chromium' | 'firefox' | 'webkit' | 'unknown'

const ORIGINAL_UA = navigator.userAgent
const ORIGINAL_UAD = Object.getOwnPropertyDescriptor(
  Object.getPrototypeOf(navigator),
  'userAgentData',
)

function stubEngine(kind: EngineKind): void {
  if (kind === 'chromium') {
    Object.defineProperty(navigator, 'userAgentData', {
      configurable: true,
      value: {
        brands: [
          { brand: 'Not.A/Brand', version: '99' },
          { brand: 'Chromium', version: '130' },
        ],
        mobile: false,
        platform: 'macOS',
      },
    })
  } else {
    Object.defineProperty(navigator, 'userAgentData', {
      configurable: true,
      get: () => undefined,
    })
    Object.defineProperty(navigator, 'userAgent', {
      configurable: true,
      value:
        kind === 'firefox'
          ? 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:141.0) Gecko/20100101 Firefox/141.0'
          : kind === 'webkit'
            ? 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15'
            : ORIGINAL_UA,
    })
  }
}

afterEach(() => {
  if (ORIGINAL_UAD) {
    Object.defineProperty(
      Object.getPrototypeOf(navigator),
      'userAgentData',
      ORIGINAL_UAD,
    )
  } else {
    Reflect.deleteProperty(navigator, 'userAgentData')
    Reflect.deleteProperty(Object.getPrototypeOf(navigator), 'userAgentData')
  }
  Object.defineProperty(navigator, 'userAgent', {
    configurable: true,
    value: ORIGINAL_UA,
  })
  cleanup()
})

// -- SPA-origin fixture --------------------------------------------------------

const SPA_PORT = '5000'
const MODE2_PATH = '/preview/mia/tok-abc123/'
const MODE2_URL = 'http://localhost:5000' + MODE2_PATH
const ISOLATED_URL = 'http://myapp.localhost:5000/'
const EXPIRES = new Date(Date.now() + 3600_000).toISOString()

beforeEach(() => {
  // IframePreview reads window.location at render (resolvePreviewHref args).
  Object.defineProperty(window, 'location', {
    value: {
      hostname: 'localhost',
      protocol: 'http:',
      origin: 'http://localhost:5000',
      port: SPA_PORT,
    },
    writable: true,
  })
})

describe('IframePreview engine selection (order 22, FR-024 / S-8.1-S-8.3)', () => {
  it('S-8.1: Chromium engine + valid isolated_url => exactly one link, the Mode 1 href', () => {
    stubEngine('chromium')
    render(
      <IframePreview
        kind="serve_workspace"
        result={{
          path: MODE2_PATH,
          url: MODE2_URL,
          expires_at: EXPIRES,
          isolated_url: ISOLATED_URL,
        }}
      />,
    )
    const links = screen.getAllByTestId('preview-link')
    expect(links).toHaveLength(1) // S-8.1: NEVER both links
    expect(links[0]).toHaveAttribute('href', ISOLATED_URL)
  })

  it('S-8.2: Firefox UA token + valid isolated_url => the Mode 1 link', () => {
    stubEngine('firefox')
    render(
      <IframePreview
        kind="serve_workspace"
        result={{
          path: MODE2_PATH,
          url: MODE2_URL,
          expires_at: EXPIRES,
          isolated_url: ISOLATED_URL,
        }}
      />,
    )
    const links = screen.getAllByTestId('preview-link')
    expect(links).toHaveLength(1)
    expect(links[0]).toHaveAttribute('href', ISOLATED_URL)
  })
})

describe('IframePreview engine selection — fallback pins (order 22, FR-024 / S-8.5 / S-8.3)', () => {
  it('S-8.1: WebKit engine => exactly one link, the Mode 2 fallback href', () => {
    stubEngine('webkit')
    render(
      <IframePreview
        kind="serve_workspace"
        result={{
          path: MODE2_PATH,
          url: MODE2_URL,
          expires_at: EXPIRES,
          isolated_url: ISOLATED_URL,
        }}
      />,
    )
    const links = screen.getAllByTestId('preview-link')
    expect(links).toHaveLength(1) // never both links
    expect(links[0]).toHaveAttribute('href', MODE2_URL)
  })

  it('FR-024: unknown engine (no userAgentData, no Firefox UA token) => Mode 2 link', () => {
    stubEngine('unknown')
    render(
      <IframePreview
        kind="serve_workspace"
        result={{
          path: MODE2_PATH,
          url: MODE2_URL,
          expires_at: EXPIRES,
          isolated_url: ISOLATED_URL,
        }}
      />,
    )
    const links = screen.getAllByTestId('preview-link')
    expect(links).toHaveLength(1)
    expect(links[0]).toHaveAttribute('href', MODE2_URL)
  })

  it('S-8.5 (card level): Chromium + tampered isolated_url (second dot) => Mode 2 link', () => {
    stubEngine('chromium')
    render(
      <IframePreview
        kind="serve_workspace"
        result={{
          path: MODE2_PATH,
          url: MODE2_URL,
          expires_at: EXPIRES,
          isolated_url: 'http://myapp.evil.localhost:5000/',
        }}
      />,
    )
    const links = screen.getAllByTestId('preview-link')
    expect(links).toHaveLength(1)
    expect(links[0]).toHaveAttribute('href', MODE2_URL)
  })

  it('S-8.3: Chromium + absent isolated_url (old transcript) => Mode 2 link, exactly as today', () => {
    stubEngine('chromium')
    render(
      <IframePreview
        kind="serve_workspace"
        result={{ path: MODE2_PATH, url: MODE2_URL, expires_at: EXPIRES }}
      />,
    )
    const links = screen.getAllByTestId('preview-link')
    expect(links).toHaveLength(1)
    expect(links[0]).toHaveAttribute('href', MODE2_URL)
  })
})

describe('IframePreview warmup target (order 23, FR-025 / S-8.4)', () => {
  it('S-8.4: warmup HEAD probe targets the Mode 2 URL — never the label host', async () => {
    stubEngine('chromium')
    const fetchSpy = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(new Response(null, { status: 200 }))
    render(
      <IframePreview
        kind="run_in_workspace"
        result={{
          path: MODE2_PATH,
          url: MODE2_URL,
          expires_at: EXPIRES,
          command: 'vite dev',
          port: 5173,
          isolated_url: ISOLATED_URL,
        }}
      />,
    )
    await waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled()
    })
    const probed = fetchSpy.mock.calls.map((c) =>
      typeof c[0] === 'string' ? c[0] : c[0].url,
    )
    const mode2Probe = probed.find((u) => u === MODE2_URL)
    // S-8.4: the probe targets the Mode 2 URL (same-origin, permitted by the
    // SPA's own connect-src) — exact equality, not substring.
    expect(mode2Probe).toBe(MODE2_URL)
    expect(
      probed.some((u) => u.includes('myapp.localhost')),
      'no probe may target the cross-origin label host (FR-025)',
    ).toBe(false)
    const mode2ProbeCall = fetchSpy.mock.calls.find(
      (c) => (typeof c[0] === 'string' ? c[0] : c[0].url) === MODE2_URL,
    )
    expect(mode2ProbeCall?.[1]?.method).toBe('HEAD')
    fetchSpy.mockRestore()
  })

  it('S-8.4: Mode 1 link shows Ready when the Mode 2 probe succeeds', async () => {
    stubEngine('chromium')
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(null, { status: 200 }),
    )
    render(
      <IframePreview
        kind="run_in_workspace"
        result={{
          path: MODE2_PATH,
          url: MODE2_URL,
          expires_at: EXPIRES,
          command: 'vite dev',
          port: 5173,
          isolated_url: ISOLATED_URL,
        }}
      />,
    )
    await waitFor(
      () => {
        expect(screen.getByTestId('preview-link')).toHaveAttribute(
          'href',
          ISOLATED_URL,
        )
      },
      { timeout: 5000 },
    )
    vi.restoreAllMocks()
  })
})
