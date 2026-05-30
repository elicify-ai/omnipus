/**
 * GenericToolCall edge-case render tests (Phase 5, Agent B)
 *
 * Uses generated types from src/lib/api/generated/asyncapi-types.ts.
 * Each describe.each block covers degenerate-but-valid inputs to ensure no
 * edge-case payload crashes the component.
 */

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { GenericToolCall } from './GenericToolCall'
import type { MessagePartStatus } from '@assistant-ui/react'
import type {
  ToolCallStartFrame,
  ToolCallResultFrame,
  TruncatedResult,
  MarshalErrorResult,
} from '@/lib/api/generated/asyncapi-types'

// ── Base props ────────────────────────────────────────────────────────────────

const COMPLETE_STATUS: MessagePartStatus = { type: 'complete' }
const RUNNING_STATUS: MessagePartStatus = { type: 'running' }
const ERROR_STATUS: MessagePartStatus = { type: 'incomplete', reason: 'error' }

// Minimal valid ToolCallStartFrame (from generated spec)
const validStartFrame: ToolCallStartFrame = {
  type: 'tool_call_start',
  session_id: 'sess-001',
  tool: 'exec',
  call_id: 'call-001',
  params: {},
}

// Minimal valid ToolCallResultFrame (from generated spec)
const validResultFrame: ToolCallResultFrame = {
  type: 'tool_call_result',
  session_id: 'sess-001',
  tool: 'exec',
  call_id: 'call-001',
  result: 'ok',
  status: 'success',
}

// ── result edge cases ─────────────────────────────────────────────────────────

describe.each([
  ['empty string result', { result: '' }],
  ['very long string result', { result: 'x'.repeat(50_000) }],
  ['unicode result', { result: '\u{1F680}\u{1F480}\u{1F389}⚡' }],
  ['null result', { result: null }],
  ['multiline result', { result: 'line1\nline2\nline3' }],
  ['JSON-like string result', { result: '{"foo":"bar"}' }],
  ['number result', { result: 42 }],
  ['boolean result', { result: false }],
  ['array result', { result: [1, 2, 3] }],
  ['deeply nested result', { result: { a: { b: { c: { d: 'deep' } } } } }],
  ['result with special chars', { result: '<script>alert(1)</script>' }],
  ['result with null bytes', { result: 'before\x00after' }],
] as Array<[string, Partial<{ result: unknown; error: string | undefined }>]>)(
  'GenericToolCall renders "%s" without throwing',
  (_label, overrides) => {
    it('renders', () => {
      expect(() =>
        render(
          <GenericToolCall
            toolName={validStartFrame.tool}
            args={validStartFrame.params}
            result={overrides.result}
            status={COMPLETE_STATUS}
          />
        )
      ).not.toThrow()
    })
  }
)

// ── params / args edge cases ──────────────────────────────────────────────────

describe.each([
  ['empty params object', {}],
  ['params with null values', { x: null }],
  ['params with nested arrays', { list: [[1, 2], [3, 4]] }],
  ['params with undefined values', { x: undefined }],
  ['params with very long string', { cmd: 'a'.repeat(10_000) }],
  ['params with special chars', { key: '<script>alert(1)</script>' }],
  ['params with unicode', { key: '\u{1F680}' }],
  ['params with numeric keys', { 0: 'a', 1: 'b' }],
  ['params with deeply nested', { a: { b: { c: { d: { e: 1 } } } } }],
] as Array<[string, Record<string, unknown>]>)(
  'GenericToolCall renders params "%s" without throwing',
  (_label, params) => {
    it('renders', () => {
      expect(() =>
        render(
          <GenericToolCall
            toolName="exec"
            args={params}
            result={undefined}
            status={RUNNING_STATUS}
          />
        )
      ).not.toThrow()
    })
  }
)

// ── status variants ───────────────────────────────────────────────────────────

describe.each([
  ['in-progress (running)', RUNNING_STATUS, undefined],
  ['complete status', COMPLETE_STATUS, undefined],
  ['error status (incomplete)', ERROR_STATUS, undefined],
  ['error status with error string', ERROR_STATUS, 'tool execution failed'],
  ['complete with error string', COMPLETE_STATUS, 'unexpected error'],
] as Array<[string, MessagePartStatus, string | undefined]>)(
  'GenericToolCall renders status "%s" without throwing',
  (_label, status, error) => {
    it('renders', () => {
      expect(() =>
        render(
          <GenericToolCall
            toolName="exec"
            args={{}}
            result={undefined}
            status={status}
            error={error}
          />
        )
      ).not.toThrow()
    })
  }
)

// ── sentinel result types (from generated asyncapi-types) ─────────────────────

describe.each([
  [
    'TruncatedResult sentinel — zero preview',
    {
      _truncated: true as const,
      original_size_bytes: 0,
      preview: '',
    } satisfies TruncatedResult,
  ],
  [
    'TruncatedResult sentinel — 1 byte',
    {
      _truncated: true as const,
      original_size_bytes: 1,
      preview: 'x',
    } satisfies TruncatedResult,
  ],
  [
    'TruncatedResult sentinel — large size',
    {
      _truncated: true as const,
      original_size_bytes: 100 * 1024 * 1024, // 100 MiB
      preview: 'preview text',
    } satisfies TruncatedResult,
  ],
  [
    'MarshalErrorResult sentinel — empty message',
    {
      _marshal_error: '',
    } satisfies MarshalErrorResult,
  ],
  [
    'MarshalErrorResult sentinel — long message',
    {
      _marshal_error: 'json: unsupported type: ' + 'x'.repeat(1_000),
    } satisfies MarshalErrorResult,
  ],
  [
    'MarshalErrorResult sentinel — unicode error',
    {
      _marshal_error: 'error: \u{1F4A5} unexpected type',
    } satisfies MarshalErrorResult,
  ],
] as Array<[string, TruncatedResult | MarshalErrorResult]>)(
  'GenericToolCall renders sentinel "%s" without throwing',
  (_label, sentinelResult) => {
    it('renders', () => {
      expect(() =>
        render(
          <GenericToolCall
            toolName="exec"
            args={{}}
            result={sentinelResult}
            status={COMPLETE_STATUS}
          />
        )
      ).not.toThrow()
    })
  }
)

// ── ToolCallResultFrame-shaped result (using generated type) ──────────────────

describe.each([
  [
    'result frame with success status',
    { ...validResultFrame, result: 'stdout output\n', status: 'success' as const },
  ],
  [
    'result frame with error status',
    {
      ...validResultFrame,
      result: null,
      status: 'error' as const,
      error: 'exit code 1',
    },
  ],
  [
    'result frame with duration',
    {
      ...validResultFrame,
      result: 'ok',
      status: 'success' as const,
      duration_ms: 42,
    },
  ],
  [
    'result frame with very large duration',
    {
      ...validResultFrame,
      result: 'ok',
      status: 'success' as const,
      duration_ms: 999_999,
    },
  ],
] as Array<[string, ToolCallResultFrame]>)(
  'GenericToolCall renders ToolCallResultFrame "%s" without throwing',
  (_label, frame) => {
    it('renders', () => {
      expect(() =>
        render(
          <GenericToolCall
            toolName={frame.tool}
            args={{}}
            result={frame.result}
            status={
              frame.status === 'error' ? ERROR_STATUS : COMPLETE_STATUS
            }
            error={frame.error}
            durationMs={frame.duration_ms}
          />
        )
      ).not.toThrow()
    })
  }
)

// ── Tool name edge cases ──────────────────────────────────────────────────────

describe.each([
  ['empty tool name', ''],
  ['very long tool name', 'tool_'.repeat(200)],
  ['tool name with dots', 'browser.navigate.click'],
  ['tool name with underscores', 'workspace_shell_bg'],
  ['tool name with special chars', 'tool<script>'],
  ['tool name with unicode', 'tool_\u{1F680}'],
] as Array<[string, string]>)(
  'GenericToolCall renders tool name "%s" without throwing',
  (_label, toolName) => {
    it('renders', () => {
      expect(() =>
        render(
          <GenericToolCall
            toolName={toolName}
            args={{}}
            result={undefined}
            status={RUNNING_STATUS}
          />
        )
      ).not.toThrow()
    })
  }
)

// ── Positive invariants ───────────────────────────────────────────────────────
//
// data-testid="tool-call-badge" is always rendered by GenericToolCall.
// Asserting its presence proves the user sees a real rendered card, not a blank div.

it('always renders the tool-call-badge container', () => {
  render(
    <GenericToolCall
      toolName="exec"
      args={{ command: 'ls' }}
      result={undefined}
      status={RUNNING_STATUS}
    />
  )
  expect(screen.getByTestId('tool-call-badge')).toBeInTheDocument()
})

it('renders tool-call-badge with the correct data-tool attribute', () => {
  render(
    <GenericToolCall
      toolName="fs.read"
      args={{ path: '/workspace/file.ts' }}
      result={undefined}
      status={COMPLETE_STATUS}
    />
  )
  const badge = screen.getByTestId('tool-call-badge')
  expect(badge).toBeInTheDocument()
  expect(badge.getAttribute('data-tool')).toBe('fs.read')
})
