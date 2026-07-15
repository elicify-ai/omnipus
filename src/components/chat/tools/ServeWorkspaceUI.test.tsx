/**
 * ServeWorkspaceUI tests — FR-008 / FR-019 / FR-015
 * Also: docs/internal/specs/preview-on-main-listener-spec.md FR-016/FR-017 (US-9)
 *
 * Focus: verify that:
 *   - ServeWorkspaceUI is exported as an AssistantUI tool component
 *   - kind='serve_workspace' means no warmup — renders a preview LINK immediately
 *     (never an embedded iframe, per ADR-044)
 *   - result with both path and url (FR-008) renders correctly
 *   - legacy result with only url field (FR-019 replay) renders correctly
 *   - null result (running state) shows waiting message
 *   - two different paths produce two different link hrefs (differentiation)
 *
 * IframePreview internals are tested in IframePreview.test.tsx.
 * We test the no-warmup path by rendering IframePreview directly with
 * kind='serve_workspace' (matching what ServeWorkspaceBlock passes).
 *
 * IframePreview no longer reads /api/v1/about (ADR-044 — no separate preview
 * origin/port to resolve), so there is no useQuery/fetchAboutInfo mock needed.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { IframePreview } from '../IframePreview'

// ── Top-level mocks ───────────────────────────────────────────────────────────

vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: vi.fn() }),
}))

// ── Helpers ───────────────────────────────────────────────────────────────────

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    value: { hostname: 'localhost', protocol: 'http:', origin: 'http://localhost:5000', port: '5000' },
    writable: true,
  })
})

// ── Module structure ──────────────────────────────────────────────────────────

describe('ServeWorkspaceUI — module structure', () => {
  it('exports ServeWorkspaceUI as a defined value (AssistantUI tool component)', async () => {
    const mod = await import('./ServeWorkspaceUI')
    expect(mod.ServeWorkspaceUI).toBeDefined()
  })
})

// ── kind='serve_workspace' — renders a link immediately (no warmup, no iframe) ─

describe('ServeWorkspaceUI — kind=serve_workspace (FR-013, US-9)', () => {
  // serve_workspace does NOT require warmup. IframePreview resolves the link
  // at phase 'ready' with no warmup delay AND without ever mounting an iframe
  // (US-9 / FR-016 — /preview/ shares the SPA's own origin post-ADR-044).
  // We render IframePreview directly with kind='serve_workspace' (as
  // ServeWorkspaceBlock does).

  it('renders a preview link immediately without warmup placeholder or iframe', () => {
    render(
      <IframePreview
        kind="serve_workspace"
        result={{
          path: '/serve/agent-1/abc123/',
          url: 'http://localhost:5000/serve/agent-1/abc123/',
          expires_at: '2099-01-01T00:00:00Z',
        }}
        warmupTimeoutSeconds={60}
      />
    )

    // No warmup placeholder — link renders directly
    expect(screen.queryByText(/starting preview/i)).toBeNull()
    expect(screen.queryByText(/starting dev server/i)).toBeNull()

    // Never an iframe.
    expect(document.querySelectorAll('iframe').length).toBe(0)

    const link = screen.getByTestId('preview-link')
    expect(link).toHaveAttribute('href', 'http://localhost:5000/serve/agent-1/abc123/')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  })

  it('result with both path and url (FR-008): uses the absolute url for the link href', () => {
    render(
      <IframePreview
        kind="serve_workspace"
        result={{
          path: '/serve/agent-1/abc123/',
          url: 'http://localhost:5000/serve/agent-1/abc123/',
          expires_at: '2099-01-01T00:00:00Z',
        }}
      />
    )
    const link = screen.getByTestId('preview-link')
    expect(link).toHaveAttribute('href', 'http://localhost:5000/serve/agent-1/abc123/')
  })

  it('null result shows waiting message (tool still running)', () => {
    render(
      <IframePreview
        kind="serve_workspace"
        result={null}
      />
    )
    expect(screen.getByText(/waiting for serve_workspace/i)).toBeInTheDocument()
    expect(document.querySelectorAll('iframe').length).toBe(0)
    expect(document.querySelectorAll('a').length).toBe(0)
  })

  it('legacy URL replay: renders a link from the url field when path is absent (FR-019)', () => {
    // Old transcripts only have the url field; resolvePreviewHref validates
    // the URL's own pathname when `path` is empty/absent.
    render(
      <IframePreview
        kind="serve_workspace"
        result={{
          // Legacy transcript: only url, path is an empty string (legacy shape)
          path: '',
          url: 'http://localhost:5000/serve/agent-1/abc123/',
          expires_at: '2099-01-01T00:00:00Z',
        }}
      />
    )
    const link = screen.getByTestId('preview-link')
    expect(link).toHaveAttribute('href', 'http://localhost:5000/serve/agent-1/abc123/')
  })

  it('differentiation: two different serve paths produce two different link hrefs', () => {
    // Anti-shortcut: link href is computed from the result, not hardcoded.
    const { unmount } = render(
      <IframePreview
        kind="serve_workspace"
        result={{
          path: '/serve/agent-a/tok1/',
          url: 'http://localhost:5000/serve/agent-a/tok1/',
          expires_at: '2099-01-01T00:00:00Z',
        }}
      />
    )
    const href1 = screen.getByTestId('preview-link').getAttribute('href') ?? ''
    unmount()

    render(
      <IframePreview
        kind="serve_workspace"
        result={{
          path: '/serve/agent-b/tok2/',
          url: 'http://localhost:5000/serve/agent-b/tok2/',
          expires_at: '2099-01-01T00:00:00Z',
        }}
      />
    )
    const href2 = screen.getByTestId('preview-link').getAttribute('href') ?? ''

    expect(href1).toContain('tok1')
    expect(href2).toContain('tok2')
    expect(href1).not.toBe(href2)
  })
})
