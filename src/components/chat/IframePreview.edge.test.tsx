/**
 * IframePreview — edge-case render tests (Phase 5 agent C)
 *
 * Goal: no degenerate-but-valid payload should crash IframePreview.
 *
 * These tests are ADDITIVE to IframePreview.test.tsx. They focus on
 * degenerate input shapes (edge-case paths, urls, kind variants, null
 * result for every kind) that were not covered by the existing spec test.
 *
 * Fixture types come exclusively from src/lib/api.ts:
 *   ServeWorkspaceResult, RunInWorkspaceResult
 * and IframePreview's discriminated union IframePreviewProps.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { IframePreview, type IframePreviewProps } from './IframePreview'
import type { ServeWorkspaceResult, RunInWorkspaceResult } from '@/lib/api'

// ── Mocks (mirror setup from IframePreview.test.tsx) ─────────────────────────

vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: vi.fn() }),
}))

const mockUseQuery = vi.fn().mockReturnValue({
  data: {
    preview_port: 5001,
    preview_listener_enabled: true,
    warmup_timeout_seconds: 10,
  },
  isLoading: false,
  isError: false,
  refetch: vi.fn(),
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

beforeEach(() => {
  mockUseQuery.mockReturnValue({
    data: {
      preview_port: 5001,
      preview_listener_enabled: true,
      warmup_timeout_seconds: 10,
    },
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  })
  Object.defineProperty(window, 'location', {
    value: { hostname: 'localhost', protocol: 'http:', origin: 'http://localhost:5000' },
    writable: true,
  })
})

// ── Fixture builders ──────────────────────────────────────────────────────────

function baseServe(overrides: Partial<ServeWorkspaceResult> = {}): ServeWorkspaceResult {
  return {
    path: '/preview/agent-1/tok123/',
    url: 'http://localhost:5001/preview/agent-1/tok123/',
    expires_at: '2099-01-01T00:00:00Z',
    ...overrides,
  }
}

function baseRun(overrides: Partial<RunInWorkspaceResult> = {}): RunInWorkspaceResult {
  return {
    path: '/preview/agent-1/devtok/',
    url: 'http://localhost:5001/preview/agent-1/devtok/',
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
    })
  }
)

// ── describe.each: url edge cases (no path, legacy url replay) ───────────────

describe.each([
  ['url only (no path), absolute URL', { url: 'http://localhost:5001/preview/agent-1/tok/', path: undefined as unknown as string, expires_at: '2099-01-01T00:00:00Z' }],
  ['url with data: scheme', { url: 'data:text/html,<h1>hi</h1>', path: undefined as unknown as string, expires_at: '2099-01-01T00:00:00Z' }],
  ['url with javascript: scheme', { url: 'javascript:alert(1)', path: undefined as unknown as string, expires_at: '2099-01-01T00:00:00Z' }],
  ['url that is empty string', { url: '', path: undefined as unknown as string, expires_at: '2099-01-01T00:00:00Z' }],
  ['url that is relative path', { url: '/preview/tok/', path: undefined as unknown as string, expires_at: '2099-01-01T00:00:00Z' }],
  ['url with host 0.0.0.0 (legacy)', { url: 'http://0.0.0.0:5001/preview/tok/', path: undefined as unknown as string, expires_at: '2099-01-01T00:00:00Z' }],
])(
  'IframePreview renders serve_workspace with %s without throwing',
  (_label: string, result: ServeWorkspaceResult) => {
    it('renders without throwing', () => {
      expect(() =>
        render(<IframePreview kind="serve_workspace" result={result} />)
      ).not.toThrow()
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
    })
  }
)

// ── describe.each: aboutInfo query states ────────────────────────────────────

describe.each([
  ['aboutInfo isLoading=true', { data: undefined, isLoading: true, isError: false, refetch: vi.fn() }],
  ['aboutInfo isError=true', { data: undefined, isLoading: false, isError: true, refetch: vi.fn() }],
  ['aboutInfo preview_port=null', { data: { preview_port: null, preview_listener_enabled: true, warmup_timeout_seconds: 10 }, isLoading: false, isError: false, refetch: vi.fn() }],
  ['aboutInfo warmup_timeout_seconds=0', { data: { preview_port: 5001, preview_listener_enabled: true, warmup_timeout_seconds: 0 }, isLoading: false, isError: false, refetch: vi.fn() }],
  ['aboutInfo warmup_timeout_seconds very large', { data: { preview_port: 5001, preview_listener_enabled: true, warmup_timeout_seconds: 999_999 }, isLoading: false, isError: false, refetch: vi.fn() }],
])(
  'IframePreview renders with %s without throwing',
  (_label: string, queryReturn: Record<string, unknown>) => {
    it('renders without throwing', () => {
      mockUseQuery.mockReturnValueOnce(queryReturn)
      expect(() =>
        render(<IframePreview kind="serve_workspace" result={baseServe()} />)
      ).not.toThrow()
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
    })
  }
)

// ── Positive invariant: null result renders a "Waiting for" placeholder ───────
//
// A silent null render (blank DOM) would pass .not.toThrow() but break the UX.
// Assert that the user actually sees an informative placeholder.

it('null result for serve_workspace renders a "Waiting for" placeholder', () => {
  render(<IframePreview kind="serve_workspace" result={null} />)
  // The component renders "Waiting for {toolName}…" when result is null.
  expect(screen.getByText(/Waiting for/i)).toBeInTheDocument()
})

it('null result for web_serve renders a "Waiting for" placeholder', () => {
  render(<IframePreview kind="web_serve" result={null} />)
  expect(screen.getByText(/Waiting for/i)).toBeInTheDocument()
})

it('null result for run_in_workspace renders a warming-up placeholder', () => {
  render(<IframePreview kind="run_in_workspace" result={null} />)
  // run_in_workspace with null result kicks off warmup polling UI.
  // The component must render something visible — it must not be blank.
  const container = document.querySelector('body')
  expect(container?.textContent?.trim().length).toBeGreaterThan(0)
})

it('isLoading state renders a "Loading preview" placeholder', () => {
  mockUseQuery.mockReturnValueOnce({ data: undefined, isLoading: true, isError: false, refetch: vi.fn() })
  render(<IframePreview kind="serve_workspace" result={baseServe()} />)
  expect(screen.getByText(/Loading preview/i)).toBeInTheDocument()
})
