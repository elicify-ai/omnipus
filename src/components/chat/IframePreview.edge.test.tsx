/**
 * IframePreview — edge-case render tests (Phase 5 agent C; updated for ADR-044)
 *
 * Goal: no degenerate-but-valid payload should crash IframePreview, and no
 * payload should ever cause an <iframe> to be mounted (US-9 / FR-016 —
 * preview-on-main-listener-spec.md).
 *
 * These tests are ADDITIVE to IframePreview.test.tsx. They focus on
 * degenerate input shapes (edge-case paths, urls, kind variants, null
 * result for every kind) that were not covered by the existing spec test.
 *
 * IframePreview no longer reads /api/v1/about (ADR-044 — `/preview/` shares
 * the SPA's own origin, no separate preview port/origin to resolve), so
 * there is no useQuery/fetchAboutInfo mock needed here anymore.
 *
 * Fixture types come exclusively from src/lib/api.ts:
 *   ServeWorkspaceResult, RunInWorkspaceResult
 * and IframePreview's discriminated union IframePreviewProps.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { IframePreview, type IframePreviewProps } from './IframePreview'
import type { ServeWorkspaceResult, RunInWorkspaceResult } from '@/lib/api'

// ── Mocks (mirror setup from IframePreview.test.tsx) ─────────────────────────

vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: vi.fn() }),
}))

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    value: { hostname: 'localhost', protocol: 'http:', origin: 'http://localhost:5000', port: '5000' },
    writable: true,
  })
  // Dev-mode fixtures kick off warmup polling via fetch(); default to a
  // rejected promise (server not reachable in jsdom) so tests that don't
  // care about the warmup outcome don't leave it unmocked.
  vi.spyOn(global, 'fetch').mockRejectedValue(new Error('not reachable in test env'))
})

afterEach(() => {
  vi.restoreAllMocks()
})

// ── Fixture builders ──────────────────────────────────────────────────────────

function baseServe(overrides: Partial<ServeWorkspaceResult> = {}): ServeWorkspaceResult {
  return {
    path: '/preview/agent-1/tok123/',
    url: 'http://localhost:5000/preview/agent-1/tok123/',
    expires_at: '2099-01-01T00:00:00Z',
    ...overrides,
  }
}

function baseRun(overrides: Partial<RunInWorkspaceResult> = {}): RunInWorkspaceResult {
  return {
    path: '/preview/agent-1/devtok/',
    url: 'http://localhost:5000/preview/agent-1/devtok/',
    expires_at: '2099-01-01T00:00:00Z',
    command: 'npm run dev',
    port: 3000,
    ...overrides,
  }
}

// ── describe.each: null result for each kind ──────────────────────────────────

describe.each([
  ['kind=serve_workspace, result=null', { kind: 'serve_workspace' as const, result: null }],
  ['kind=web_serve, result=null', { kind: 'web_serve' as const, result: null }],
  ['kind=run_in_workspace, result=null', { kind: 'run_in_workspace' as const, result: null }],
])(
  'IframePreview renders %s without throwing',
  (_label: string, props: { kind: IframePreviewProps['kind']; result: null }) => {
    it('renders without throwing', () => {
      expect(() =>
        render(<IframePreview kind={props.kind} result={props.result} />)
      ).not.toThrow()
    })
  }
)

// ── describe.each: path edge cases (serve_workspace kind) ────────────────────

describe.each([
  ['empty path string', baseServe({ path: '' })],
  ['path with spaces', baseServe({ path: '/preview/agent 1/tok 123/' })],
  ['path with unicode', baseServe({ path: '/preview/agent-1/tok-🚀/' })],
  ['path with javascript: scheme (invalid-path branch)', baseServe({ path: 'javascript:alert(1)' })],
  ['path with data: scheme', baseServe({ path: 'data:text/html,<h1>hi</h1>' })],
  ['very long path (1KB)', baseServe({ path: '/' + 'a'.repeat(1000) + '/' })],
  ['path with query string', baseServe({ path: '/preview/agent-1/tok/?x=1&y=2' })],
  ['path with fragment', baseServe({ path: '/preview/agent-1/tok/#section' })],
])(
  'IframePreview renders serve_workspace with %s without throwing',
  (_label: string, result: ServeWorkspaceResult) => {
    it('renders without throwing', () => {
      expect(() =>
        render(<IframePreview kind="serve_workspace" result={result} />)
      ).not.toThrow()
      // Never an iframe, regardless of how malformed the path is.
      expect(document.querySelectorAll('iframe').length).toBe(0)
    })
  }
)

// ── describe.each: url edge cases (no path, legacy url replay) ───────────────

describe.each([
  ['url only (no path), absolute URL', { url: 'http://localhost:5000/preview/agent-1/tok/', path: undefined as unknown as string, expires_at: '2099-01-01T00:00:00Z' }],
  ['url with data: scheme', { url: 'data:text/html,<h1>hi</h1>', path: undefined as unknown as string, expires_at: '2099-01-01T00:00:00Z' }],
  ['url with javascript: scheme', { url: 'javascript:alert(1)', path: undefined as unknown as string, expires_at: '2099-01-01T00:00:00Z' }],
  ['url that is empty string', { url: '', path: undefined as unknown as string, expires_at: '2099-01-01T00:00:00Z' }],
  ['url that is relative path', { url: '/preview/tok/', path: undefined as unknown as string, expires_at: '2099-01-01T00:00:00Z' }],
  ['url with host 0.0.0.0 (legacy)', { url: 'http://0.0.0.0:5000/preview/tok/', path: undefined as unknown as string, expires_at: '2099-01-01T00:00:00Z' }],
])(
  'IframePreview renders serve_workspace with %s without throwing',
  (_label: string, result: ServeWorkspaceResult) => {
    it('renders without throwing', () => {
      expect(() =>
        render(<IframePreview kind="serve_workspace" result={result} />)
      ).not.toThrow()
      expect(document.querySelectorAll('iframe').length).toBe(0)
    })
  }
)

// ── describe.each: web_serve kind edge cases ─────────────────────────────────

describe.each([
  ['web_serve with valid result', { kind: 'web_serve' as const, result: baseServe() }],
  ['web_serve with null result', { kind: 'web_serve' as const, result: null }],
  ['web_serve with empty path', { kind: 'web_serve' as const, result: baseServe({ path: '' }) }],
])(
  'IframePreview renders %s without throwing',
  (_label: string, props: { kind: 'web_serve'; result: ServeWorkspaceResult | null }) => {
    it('renders without throwing', () => {
      expect(() =>
        render(<IframePreview kind={props.kind} result={props.result} />)
      ).not.toThrow()
      expect(document.querySelectorAll('iframe').length).toBe(0)
    })
  }
)

// ── describe.each: run_in_workspace kind edge cases ───────────────────────────

describe.each([
  ['run_in_workspace with valid result', { kind: 'run_in_workspace' as const, result: baseRun() }],
  ['run_in_workspace with port=0', { kind: 'run_in_workspace' as const, result: baseRun({ port: 0 }) }],
  ['run_in_workspace with empty command', { kind: 'run_in_workspace' as const, result: baseRun({ command: '' }) }],
  ['run_in_workspace with very long command', { kind: 'run_in_workspace' as const, result: baseRun({ command: 'npm'.repeat(500) }) }],
  ['run_in_workspace null result', { kind: 'run_in_workspace' as const, result: null }],
])(
  'IframePreview renders %s without throwing',
  (_label: string, props: { kind: 'run_in_workspace'; result: RunInWorkspaceResult | null }) => {
    it('renders without throwing', () => {
      // run_in_workspace starts warmup; just ensure mount doesn't crash
      expect(() =>
        render(<IframePreview kind={props.kind} result={props.result} />)
      ).not.toThrow()
      expect(document.querySelectorAll('iframe').length).toBe(0)
    })
  }
)

// ── warmupTimeoutSeconds prop edge cases ─────────────────────────────────────

describe.each([
  ['warmupTimeoutSeconds=0', 0],
  ['warmupTimeoutSeconds=1', 1],
  ['warmupTimeoutSeconds=3600', 3600],
  ['warmupTimeoutSeconds=undefined', undefined],
])(
  'IframePreview renders run_in_workspace with %s without throwing',
  (_label: string, warmupTimeoutSeconds: number | undefined) => {
    it('renders without throwing', () => {
      expect(() =>
        render(
          <IframePreview
            kind="run_in_workspace"
            result={baseRun()}
            warmupTimeoutSeconds={warmupTimeoutSeconds}
          />
        )
      ).not.toThrow()
      expect(document.querySelectorAll('iframe').length).toBe(0)
    })
  }
)

// ── Positive invariant: null result renders a "Waiting for" placeholder ───────
//
// A silent null render (blank DOM) would pass .not.toThrow() but break the UX.
// Assert that the user actually sees an informative placeholder.

it('null result for serve_workspace renders a "Waiting for" placeholder', () => {
  render(<IframePreview kind="serve_workspace" result={null} />)
  expect(screen.getByText(/Waiting for/i)).toBeInTheDocument()
})

it('null result for web_serve renders a "Waiting for" placeholder', () => {
  render(<IframePreview kind="web_serve" result={null} />)
  expect(screen.getByText(/Waiting for/i)).toBeInTheDocument()
})

it('null result for run_in_workspace renders a warming-up placeholder', () => {
  render(<IframePreview kind="run_in_workspace" result={null} />)
  // run_in_workspace with null result shows the "waiting for the tool" state
  // (there's no href to warm up yet since there's no result). The component
  // must render something visible — it must not be blank.
  const container = document.querySelector('body')
  expect(container?.textContent?.trim().length).toBeGreaterThan(0)
})
