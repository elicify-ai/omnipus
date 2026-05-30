/**
 * SubagentBlock — edge-case render tests (Phase 5 agent C)
 *
 * Goal: no degenerate-but-valid payload should crash SubagentBlock.
 *
 * Fixture types come exclusively from:
 *   - src/store/chat.ts  (SubagentSpan, SubagentSpanTerminal, SpanStep)
 *   - src/lib/api.ts     (ToolCall)
 * No hand-written wire types are used.
 */

import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { SubagentBlock } from './SubagentBlock'
import type { SubagentSpan, SubagentSpanTerminal, SpanStep } from '@/store/chat'
import type { ToolCall } from '@/lib/api'

// ── Fixture helpers ───────────────────────────────────────────────────────────

function makeToolStep(overrides: Partial<ToolCall & { call_id: string }> = {}): SpanStep {
  const tool: ToolCall & { call_id: string } = {
    id: overrides.id ?? 'step_e1',
    call_id: overrides.call_id ?? overrides.id ?? 'step_e1',
    tool: overrides.tool ?? 'fs.list',
    params: overrides.params ?? {},
    status: overrides.status ?? 'success',
    result: overrides.result,
    duration_ms: overrides.duration_ms,
    error: overrides.error,
  }
  return { kind: 'tool', tool }
}

function makeTextStep(text: string, ts = 0): SpanStep {
  return { kind: 'text', text, ts }
}

function makeRunningSpan(overrides: {
  spanId?: string
  parentCallId?: string
  taskLabel?: string
  steps?: SpanStep[]
} = {}): SubagentSpan {
  return {
    spanId: overrides.spanId ?? 'span_e1',
    parentCallId: overrides.parentCallId ?? 'c_e1',
    taskLabel: overrides.taskLabel ?? 'edge task',
    steps: overrides.steps ?? [],
    status: 'running',
  }
}

function makeTerminalSpan(
  status: SubagentSpanTerminal['status'],
  overrides: Partial<Omit<SubagentSpanTerminal, 'status'>> = {}
): SubagentSpan {
  return {
    spanId: overrides.spanId ?? 'span_e1',
    parentCallId: overrides.parentCallId ?? 'c_e1',
    taskLabel: overrides.taskLabel ?? 'edge task',
    steps: overrides.steps ?? [],
    status,
    durationMs: overrides.durationMs ?? 0,
    finalResult: overrides.finalResult,
    reason: overrides.reason,
  }
}

// ── describe.each: taskLabel edge cases ──────────────────────────────────────

describe.each([
  ['empty taskLabel', ''],
  ['single space taskLabel', ' '],
  ['taskLabel of exactly 60 chars', 'a'.repeat(60)],
  ['taskLabel of 61 chars (triggers truncation)', 'b'.repeat(61)],
  ['taskLabel of 200 chars', 'c'.repeat(200)],
  ['unicode-heavy taskLabel (100 emojis)', '🎉'.repeat(100)],
  ['taskLabel with HTML entities', '<b>bold</b> &amp; <script>bad</script>'],
  ['taskLabel with newlines', 'line1\nline2\nline3'],
  ['taskLabel with mixed RTL/LTR', 'english הברית english'],
])(
  'SubagentBlock renders with %s without throwing',
  (_label: string, taskLabel: string) => {
    it('renders without throwing', () => {
      const span = makeRunningSpan({ taskLabel })
      expect(() => render(<SubagentBlock span={span} />)).not.toThrow()
    })
  }
)

// ── describe.each: status edge cases ─────────────────────────────────────────

describe.each([
  ['running status, zero steps', makeRunningSpan({ steps: [] })],
  ['success with durationMs=0', makeTerminalSpan('success', { durationMs: 0 })],
  ['success with durationMs very large', makeTerminalSpan('success', { durationMs: 999_999_999 })],
  ['error with no finalResult', makeTerminalSpan('error')],
  ['error with finalResult', makeTerminalSpan('error', { finalResult: 'Error: something went wrong' })],
  ['cancelled with no reason', makeTerminalSpan('cancelled')],
  ['interrupted with reason=parent_timeout', makeTerminalSpan('interrupted', { reason: 'parent_timeout' })],
  ['interrupted with reason=parent_cancelled', makeTerminalSpan('interrupted', { reason: 'parent_cancelled' })],
  ['interrupted with reason=parent_done_early', makeTerminalSpan('interrupted', { reason: 'parent_done_early' })],
  ['interrupted with reason=unknown', makeTerminalSpan('interrupted', { reason: 'unknown' })],
  ['timeout status', makeTerminalSpan('timeout')],
])(
  'SubagentBlock renders with %s without throwing',
  (_label: string, span: SubagentSpan) => {
    it('renders without throwing', () => {
      expect(() => render(<SubagentBlock span={span} />)).not.toThrow()
    })
  }
)

// ── describe.each: steps edge cases ──────────────────────────────────────────

describe.each([
  ['zero steps (running)', makeRunningSpan({ steps: [] })],
  [
    'one text step',
    makeRunningSpan({ steps: [makeTextStep('some output text')] }),
  ],
  [
    'one text step with empty text',
    makeRunningSpan({ steps: [makeTextStep('')] }),
  ],
  [
    'text step with very long text (10KB)',
    makeRunningSpan({ steps: [makeTextStep('t'.repeat(10_000))] }),
  ],
  [
    'text step with HTML entities',
    makeRunningSpan({ steps: [makeTextStep('<script>xss</script>')] }),
  ],
  [
    'mixed text and tool steps',
    makeRunningSpan({
      steps: [
        makeTextStep('Starting task'),
        makeToolStep({ id: 's1', call_id: 's1', tool: 'shell' }),
        makeTextStep('Done'),
      ],
    }),
  ],
  [
    'tool step with empty params',
    makeRunningSpan({
      steps: [makeToolStep({ id: 's1', call_id: 's1', params: {} })],
    }),
  ],
  [
    'tool step with error status',
    makeRunningSpan({
      steps: [
        makeToolStep({ id: 's1', call_id: 's1', status: 'error', error: 'command not found' }),
      ],
    }),
  ],
  [
    'tool step with very long result',
    makeRunningSpan({
      steps: [
        makeToolStep({ id: 's1', call_id: 's1', result: 'r'.repeat(10_000) }),
      ],
    }),
  ],
  [
    'duplicate step call_ids (should not crash)',
    makeRunningSpan({
      steps: [
        makeToolStep({ id: 'dupe', call_id: 'dupe', tool: 'fs.read' }),
        makeToolStep({ id: 'dupe', call_id: 'dupe', tool: 'fs.read' }),
      ],
    }),
  ],
])(
  'SubagentBlock renders with %s without throwing',
  (_label: string, span: SubagentSpan) => {
    it('renders without throwing', () => {
      expect(() => render(<SubagentBlock span={span} />)).not.toThrow()
    })
  }
)

// ── describe.each: finalResult edge cases ────────────────────────────────────

describe.each([
  ['finalResult empty string', makeTerminalSpan('success', { finalResult: '' })],
  ['finalResult with 50KB text', makeTerminalSpan('success', { finalResult: 'f'.repeat(50_000) })],
  ['finalResult with unicode', makeTerminalSpan('success', { finalResult: '日本語テキスト'.repeat(100) })],
  ['finalResult with HTML', makeTerminalSpan('success', { finalResult: '<script>alert(1)</script>' })],
  ['finalResult with JSON', makeTerminalSpan('success', { finalResult: JSON.stringify({ ok: true, count: 42 }) })],
  ['finalResult undefined', makeTerminalSpan('success', { finalResult: undefined })],
])(
  'SubagentBlock renders with finalResult=%s without throwing',
  (_label: string, span: SubagentSpan) => {
    it('renders without throwing', () => {
      // Expand the block so the finalResult section renders
      const { container } = render(<SubagentBlock span={span} />)
      const btn = container.querySelector('[data-testid="subagent-collapsed"]')
      expect(btn).not.toBeNull()
      // Click to expand
      btn!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      // Should not have thrown at any point
    })
  }
)

// ── No <script> element rendered for any text content ────────────────────────

it('text step with script tag does not produce a <script> DOM element', () => {
  const span = makeRunningSpan({
    steps: [makeTextStep('<script>window.__xss = true</script>')],
  })
  render(<SubagentBlock span={span} />)
  // Expand to ensure body renders
  const btn = document.querySelector('[data-testid="subagent-collapsed"]')
  btn?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  expect(document.querySelectorAll('script')).toHaveLength(0)
})

it('finalResult with script tag does not produce a <script> DOM element', () => {
  const span = makeTerminalSpan('success', {
    finalResult: '<script>window.__xss = true</script>',
  })
  render(<SubagentBlock span={span} />)
  const btn = document.querySelector('[data-testid="subagent-collapsed"]')
  btn?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  expect(document.querySelectorAll('script')).toHaveLength(0)
})
