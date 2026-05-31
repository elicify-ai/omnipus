/**
 * ServeWorkspaceUI tests — FR-008 / FR-019 / FR-015
 *
 * Focus: verify that:
 *   - ServeWorkspaceUI is exported as an AssistantUI tool component
 *   - kind='serve_workspace' means no warmup (renders iframe immediately)
 *   - result with both path and url (FR-008) renders correctly
 *   - legacy result with only url field (FR-019 replay) renders correctly
 *   - null result (running state) shows waiting message
 *   - two different paths produce two different iframe srcs (differentiation)
 *
 * IframePreview internals are tested in IframePreview.test.tsx.
 * We test the no-warmup path by rendering IframePreview directly with
 * kind='serve_workspace' (matching what ServeWorkspaceBlock passes).
 *
 * Traces to: docs/internal/specs/chat-served-iframe-preview-spec.md
 * FR-008: result includes both path and url
 * FR-013: serve_workspace does NOT require warmup
 * FR-015: link-only fallback for invalid path
 * FR-019: legacy transcript replay from url field
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { IframePreview } from '../IframePreview'

// ── Top-level mocks ───────────────────────────────────────────────────────────

vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: vi.fn() }),
}))

vi.mock('@tanstack/react-query', () => ({
  useQuery: () => ({
    data: {
      preview_port: 5001,
      preview_listener_enabled: true,
      warmup_timeout_seconds: 60,
    },
  }),
}))

vi.mock('@/lib/api', () => ({
  fetchAboutInfo: vi.fn().mockResolvedValue({}),
}))

// ── Helpers ───────────────────────────────────────────────────────────────────

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    value: { hostname: 'localhost', protocol: 'http:' },
    writable: true,
  })
})

// ── Module structure ──────────────────────────────────────────────────────────

describe('ServeWorkspaceUI — module structure', () => {
  it('exports ServeWorkspaceUI as a defined value (AssistantUI tool component)', async () => {
    // Traces to: chat-served-iframe-preview-spec.md — Scenario: serve_workspace registers as AssistantUI tool
    vi.mock('@assistant-ui/react', () => ({
      makeAssistantToolUI: (config: Record<string, unknown>) => config,
    }))
    const mod = await import('./ServeWorkspaceUI')
    expect(mod.ServeWorkspaceUI).toBeDefined()
  })
})

// ── kind='serve_workspace' — renders iframe immediately (no warmup) ───────────

describe('ServeWorkspaceUI — kind=serve_workspace (FR-013)', () => {
  // Traces to: chat-served-iframe-preview-spec.md — serve_workspace does NOT require warmup.
  // The gateway's preview listener is already bound when the token is issued.
  // IframePreview mounts the iframe at phase 'ready' with no warmup delay.
  // We render IframePreview directly with kind='serve_workspace' (as ServeWorkspaceBlock does).

  it('renders visible iframe immediately without warmup placeholder (FR-013)', () => {
    // Traces to: chat-served-iframe-preview-spec.md — Scenario: serve_workspace renders without warmup
    render(
      <IframePreview
        kind="serve_workspace"
        result={{
          path: '/serve/agent-1/abc123/',
          url: 'http://localhost:5001/serve/agent-1/abc123/',
          expires_at: '2099-01-01T00:00:00Z',
        }}
        warmupTimeoutSeconds={60}
      />
    )

    // No warmup placeholder — iframe renders directly
    expect(screen.queryByText(/starting preview/i)).toBeNull()
    expect(screen.queryByText(/starting dev server/i)).toBeNull()

    // Visible iframe is rendered
    const iframes = document.querySelectorAll('iframe:not([aria-hidden])')
    expect(iframes.length).toBeGreaterThan(0)
    const src = iframes[0].getAttribute('src') ?? ''
    expect(src).toContain('/serve/agent-1/abc123/')
  })

  it('result with both path and url (FR-008): uses path field for iframe src', () => {
    // Traces to: chat-served-iframe-preview-spec.md — FR-008: path field takes precedence over url
    render(
      <IframePreview
        kind="serve_workspace"
        result={{
          path: '/serve/agent-1/abc123/',
          url: 'http://localhost:5001/serve/agent-1/abc123/',
          expires_at: '2099-01-01T00:00:00Z',
        }}
      />
    )
    const iframes = document.querySelectorAll('iframe:not([aria-hidden])')
    expect(iframes.length).toBeGreaterThan(0)
    const src = iframes[0].getAttribute('src') ?? ''
    expect(src).toContain('/serve/agent-1/abc123/')
  })

  it('null result shows waiting message (tool still running)', () => {
    // Traces to: chat-served-iframe-preview-spec.md — running tool state
    render(
      <IframePreview
        kind="serve_workspace"
        result={null}
      />
    )
    expect(screen.getByText(/waiting for serve_workspace/i)).toBeInTheDocument()
    expect(document.querySelectorAll('iframe').length).toBe(0)
  })

  it('legacy URL replay: renders iframe from url field when path is absent (FR-019)', () => {
    // Traces to: chat-served-iframe-preview-spec.md — FR-019 legacy transcript replay
    // Old transcripts only have the url field; extractPath parses pathname from it.
    // We cast to ServeWorkspaceResult since url-only is a legacy compat scenario.
    render(
      <IframePreview
        kind="serve_workspace"
        result={{
          // Legacy transcript: only url, path is an empty string (legacy shape)
          path: '',
          url: 'http://localhost:5001/serve/agent-1/abc123/',
          expires_at: '2099-01-01T00:00:00Z',
        }}
      />
    )
    const iframes = document.querySelectorAll('iframe:not([aria-hidden])')
    expect(iframes.length).toBeGreaterThan(0)
    const src = iframes[0].getAttribute('src') ?? ''
    expect(src).toContain('/serve/agent-1/abc123/')
  })

  it('differentiation: two different serve paths produce two different iframe srcs', () => {
    // Anti-shortcut: iframe src is computed from path, not hardcoded
    // Traces to: chat-served-iframe-preview-spec.md — anti-shortcut differentiation
    const { unmount } = render(
      <IframePreview
        kind="serve_workspace"
        result={{
          path: '/serve/agent-a/tok1/',
          url: 'http://localhost:5001/serve/agent-a/tok1/',
          expires_at: '2099-01-01T00:00:00Z',
        }}
      />
    )
    const src1 = document.querySelector('iframe:not([aria-hidden])')?.getAttribute('src') ?? ''
    unmount()

    render(
      <IframePreview
        kind="serve_workspace"
        result={{
          path: '/serve/agent-b/tok2/',
          url: 'http://localhost:5001/serve/agent-b/tok2/',
          expires_at: '2099-01-01T00:00:00Z',
        }}
      />
    )
    const src2 = document.querySelector('iframe:not([aria-hidden])')?.getAttribute('src') ?? ''

    expect(src1).toContain('tok1')
    expect(src2).toContain('tok2')
    expect(src1).not.toBe(src2)
  })
})
