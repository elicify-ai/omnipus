/**
 * WebServeUI (WebServeBlock) edge-case render tests (Phase 5, Agent B)
 *
 * Covers degenerate-but-valid inputs to WebServeBlock to ensure no edge-case
 * payload crashes the component. Uses generated types from asyncapi-types.ts
 * where applicable; WebServeResult is a hand-written type in WebServeUI.tsx
 * (flagged in report — see deliverables).
 *
 * ADR-044 (preview-on-main-listener): IframePreview no longer reads
 * /api/v1/about (no separate preview origin/port to resolve), so no
 * useQuery/fetchAboutInfo mock is needed here anymore.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { WebServeBlock } from './WebServeUI'
import type { ToolCallStartFrame } from '@/lib/api/generated/asyncapi-types'

// ── Mocks ─────────────────────────────────────────────────────────────────────

vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: vi.fn() }),
}))

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    value: { hostname: 'localhost', protocol: 'http:', origin: 'http://localhost:5000', port: '5000' },
    writable: true,
  })
})

// ── Valid base result shape ────────────────────────────────────────────────────

const validStaticResult = {
  kind: 'static' as const,
  url: '/preview/agent/tok/',
  expires_at: '2099-01-01T00:00:00Z',
}

const validDevResult = {
  kind: 'dev' as const,
  url: '/preview/agent/tok/',
  command: 'vite dev',
  port: 3000,
  expires_at: '2099-01-01T00:00:00Z',
}

// ── result = null/undefined (tool in-progress) ────────────────────────────────

describe.each([
  ['null result, running', null, true],
  ['null result, not running', null, false],
  ['undefined result, running', undefined, true],
  ['undefined result, not running', undefined, false],
] as Array<[string, null | undefined, boolean]>)(
  'WebServeBlock renders "%s" without throwing',
  (_label, result, isRunning) => {
    it('renders', () => {
      expect(() =>
        render(
          <WebServeBlock
            args={{}}
            result={result}
            isRunning={isRunning}
            toolName="web_serve"
          />
        )
      ).not.toThrow()
    })
  }
)

// ── Malformed / unexpected result shapes ──────────────────────────────────────

describe.each([
  ['empty object result', {}],
  ['result missing url', { kind: 'static', expires_at: '2099-01-01T00:00:00Z' }],
  ['result missing expires_at', { kind: 'static', url: '/preview/x/tok/' }],
  ['string result', 'not-an-object'],
  ['number result', 42],
  ['boolean result', true],
  ['array result', []],
  ['array with items result', [1, 2, 3]],
  ['null nested field', { kind: null, url: '/preview/x/tok/', expires_at: '2099-01-01T00:00:00Z' }],
] as Array<[string, unknown]>)(
  'WebServeBlock renders malformed "%s" without throwing',
  (_label, result) => {
    it('renders', () => {
      expect(() =>
        render(
          <WebServeBlock
            args={{}}
            result={result}
            isRunning={false}
            toolName="web_serve"
          />
        )
      ).not.toThrow()
    })
  }
)

// ── Valid static result edge cases ────────────────────────────────────────────

describe.each([
  ['static result with empty path', { ...validStaticResult, path: '' }],
  ['static result with very long path', { ...validStaticResult, path: '/preview/' + 'a'.repeat(2_000) + '/' }],
  ['static result with unicode path', { ...validStaticResult, path: '/preview/\u{1F680}/tok/' }],
  ['static result with no path field', validStaticResult],
  ['static result with path and url identical', { ...validStaticResult, path: validStaticResult.url }],
  ['static result with expired timestamp', { ...validStaticResult, expires_at: '2000-01-01T00:00:00Z' }],
  ['static result with malformed timestamp', { ...validStaticResult, expires_at: 'not-a-date' }],
] as Array<[string, typeof validStaticResult & { path?: string }]>)(
  'WebServeBlock renders static "%s" without throwing',
  (_label, result) => {
    it('renders', () => {
      expect(() =>
        render(
          <WebServeBlock
            args={{}}
            result={result}
            isRunning={false}
            toolName="web_serve"
          />
        )
      ).not.toThrow()
    })
  }
)

// ── Valid dev result edge cases ────────────────────────────────────────────────

describe.each([
  ['dev result with empty command', { ...validDevResult, command: '' }],
  ['dev result with very long command', { ...validDevResult, command: 'a'.repeat(5_000) }],
  ['dev result with port 0', { ...validDevResult, port: 0 }],
  ['dev result with port 65535', { ...validDevResult, port: 65535 }],
  ['dev result with no command', { kind: 'dev' as const, url: '/preview/x/tok/', port: 3000, expires_at: '2099-01-01T00:00:00Z' }],
  ['dev result with no port', { kind: 'dev' as const, url: '/preview/x/tok/', command: 'vite', expires_at: '2099-01-01T00:00:00Z' }],
  ['dev result with unicode command', { ...validDevResult, command: 'npm run start \u{1F4BB}' }],
] as Array<[string, object]>)(
  'WebServeBlock renders dev "%s" without throwing',
  (_label, result) => {
    it('renders', () => {
      expect(() =>
        render(
          <WebServeBlock
            args={{}}
            result={result}
            isRunning={false}
            toolName="web_serve"
          />
        )
      ).not.toThrow()
    })
  }
)

// ── Args edge cases ───────────────────────────────────────────────────────────

describe.each([
  ['empty args', {}],
  ['args with empty command', { command: '' }],
  ['args with very long command', { command: 'a'.repeat(5_000) }],
  ['args with null path', { path: undefined }],
  ['args with all fields', { path: '/workspace', command: 'vite dev', port: 3000, duration_seconds: 300 }],
  ['args with zero port', { port: 0 }],
  ['args with negative port (degenerate)', { port: -1 }],
] as Array<[string, object]>)(
  'WebServeBlock renders args "%s" without throwing',
  (_label, args) => {
    it('renders', () => {
      expect(() =>
        render(
          <WebServeBlock
            args={args}
            result={null}
            isRunning={true}
            toolName="web_serve"
          />
        )
      ).not.toThrow()
    })
  }
)

// ── ToolCallStartFrame.params as args (using generated type) ──────────────────
// WebServeUI args are a subset of ToolCallStartFrame.params; verify that
// params from a generated-typed frame do not crash the block.

describe.each([
  [
    'params from generated ToolCallStartFrame',
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'web_serve',
      call_id: 'call-1',
      params: { path: '/workspace', duration_seconds: 300 },
    } satisfies ToolCallStartFrame,
  ],
  [
    'params from generated ToolCallStartFrame for dev mode',
    {
      type: 'tool_call_start' as const,
      session_id: 'sess-1',
      tool: 'web_serve',
      call_id: 'call-2',
      params: { command: 'vite dev', port: 5173 },
    } satisfies ToolCallStartFrame,
  ],
] as Array<[string, ToolCallStartFrame]>)(
  'WebServeBlock renders ToolCallStartFrame params "%s" without throwing',
  (_label, frame) => {
    it('renders', () => {
      expect(() =>
        render(
          <WebServeBlock
            args={frame.params as Record<string, unknown>}
            result={null}
            isRunning={true}
            toolName={frame.tool}
          />
        )
      ).not.toThrow()
    })
  }
)

// ── Tool name aliases ─────────────────────────────────────────────────────────

describe.each([
  ['web_serve', 'web_serve'],
  ['serve_workspace (back-compat)', 'serve_workspace'],
  ['run_in_workspace (back-compat)', 'run_in_workspace'],
  ['empty tool name', ''],
  ['unknown tool name', 'unknown_tool'],
] as Array<[string, string]>)(
  'WebServeBlock renders tool name "%s" without throwing',
  (_label, toolName) => {
    it('renders', () => {
      expect(() =>
        render(
          <WebServeBlock
            args={{}}
            result={validStaticResult}
            isRunning={false}
            toolName={toolName}
          />
        )
      ).not.toThrow()
    })
  }
)

// ── Positive invariants ───────────────────────────────────────────────────────
//
// A silent null render (blank DOM) would pass .not.toThrow() but break the UX.
// These assertions verify the user sees real content.

it('renders a visible tool header for a static result', () => {
  render(
    <WebServeBlock
      args={{}}
      result={validStaticResult}
      isRunning={false}
      toolName="web_serve"
    />
  )
  // The webserve-tool-header data-testid is always present when the component renders.
  expect(screen.getByTestId('webserve-tool-header')).toBeInTheDocument()
})

it('renders a non-empty DOM for null result (tool in-progress)', () => {
  render(
    <WebServeBlock
      args={{}}
      result={null}
      isRunning={true}
      toolName="web_serve"
    />
  )
  // A non-empty DOM means the user sees something (not a blank card).
  expect(document.querySelector('body')?.textContent?.trim().length).toBeGreaterThan(0)
})
