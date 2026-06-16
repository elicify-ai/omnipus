// SubagentBlock component tests — TDD rows 13-19
// Traces to: sprint-h-subagent-block-spec.md BDD Scenarios 1-6, 11-14

import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import axe from 'axe-core'
import { SubagentBlock } from './SubagentBlock'
import type { SubagentSpan, SubagentSpanTerminal, SpanStep } from '@/store/chat'
import type { ToolCall } from '@/lib/api'
import { useUiStore } from '@/store/ui'

// Reset the shared expandedSpans state so each test starts with all blocks
// collapsed. Expansion state moved out of component-local useState into
// useUiStore so the component survives the live→historical-render-tree swap
// (when streaming ends and the virtualizer takes over) without losing state.
beforeEach(() => {
  useUiStore.setState({ expandedSpans: {} })
})

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeStep(overrides: Partial<ToolCall & { call_id: string }> = {}): SpanStep {
  const tool: ToolCall & { call_id: string } = {
    id: overrides.id ?? 'step_1',
    call_id: overrides.call_id ?? overrides.id ?? 'step_1',
    tool: overrides.tool ?? 'fs.list',
    params: overrides.params ?? { path: '/tmp' },
    status: overrides.status ?? 'success',
    result: overrides.result ?? 'file1.txt\nfile2.txt',
    duration_ms: overrides.duration_ms ?? 120,
    error: overrides.error,
  }
  return { kind: 'tool', tool }
}

function makeSpan(overrides?: { spanId?: string; parentCallId?: string; taskLabel?: string; status?: SubagentSpan['status']; steps?: SpanStep[] } & Partial<Omit<SubagentSpanTerminal, 'status' | 'steps'>>): SubagentSpan {
  const status = overrides?.status ?? 'running'
  const base = {
    spanId: overrides?.spanId ?? 'span_c1',
    parentCallId: overrides?.parentCallId ?? 'c1',
    taskLabel: overrides?.taskLabel ?? 'audit go files',
    steps: overrides?.steps ?? [],
  }
  if (status === 'running') {
    return { ...base, status: 'running' }
  }
  return {
    ...base,
    status: status as SubagentSpanTerminal['status'],
    durationMs: (overrides as Partial<SubagentSpanTerminal>)?.durationMs ?? 0,
    finalResult: (overrides as Partial<SubagentSpanTerminal>)?.finalResult,
    reason: (overrides as Partial<SubagentSpanTerminal>)?.reason,
  }
}

// ── TDD row 13: SubagentBlock_Collapsed_LiveStepCounter ──────────────────────

describe('SubagentBlock_Collapsed_LiveStepCounter', () => {
  it('shows step count that reflects props', () => {
    const span = makeSpan({ steps: [] })
    const { rerender } = render(<SubagentBlock span={span} />)

    // 0 steps while no tool_call_start yet
    const header = screen.getByTestId('subagent-collapsed')
    expect(header).toHaveTextContent('0 steps')

    // After 1 step arrives
    rerender(<SubagentBlock span={makeSpan({ steps: [makeStep({ id: 's1', call_id: 's1' })] })} />)
    expect(screen.getByTestId('subagent-collapsed')).toHaveTextContent('1 step')

    // After 2 steps
    rerender(
      <SubagentBlock
        span={makeSpan({
          steps: [makeStep({ id: 's1', call_id: 's1' }), makeStep({ id: 's2', call_id: 's2' })],
        })}
      />,
    )
    expect(screen.getByTestId('subagent-collapsed')).toHaveTextContent('2 steps')
  })

  it('shows spinner while running', () => {
    render(<SubagentBlock span={makeSpan({ status: 'running' })} />)
    // The ArrowsClockwise icon has animate-spin class when running
    const header = screen.getByTestId('subagent-collapsed')
    const spinner = header.querySelector('.animate-spin')
    expect(spinner).not.toBeNull()
  })

  it('task label appears in header', () => {
    render(<SubagentBlock span={makeSpan({ taskLabel: 'research repo structure' })} />)
    expect(screen.getByTestId('subagent-collapsed')).toHaveTextContent('research repo structure')
  })
})

// ── TDD row 14: SubagentBlock_Expanded_NestedToolCallsInOrder ────────────────

describe('SubagentBlock_Expanded_NestedToolCallsInOrder', () => {
  it('shows 2 ToolCallBadges in stored order when expanded', async () => {
    const span = makeSpan({
      status: 'success',
      steps: [
        makeStep({ id: 't1', call_id: 't1', tool: 'fs.list' }),
        makeStep({ id: 't2', call_id: 't2', tool: 'shell' }),
      ],
    })
    render(<SubagentBlock span={span} />)

    // Click header to expand
    fireEvent.click(screen.getByTestId('subagent-collapsed'))

    const expanded = screen.getByTestId('subagent-expanded')
    const badges = within(expanded).getAllByRole('button')
    // Each ToolCallBadge renders a button (the header row)
    expect(badges.length).toBe(2)
    // Verify order: fs.list first, shell second. Collapsed chips show the
    // humanized label (humanizeToolName: 'fs.list' -> 'List', 'shell' -> 'Shell');
    // the raw id is preserved in the expanded chip view.
    expect(badges[0]).toHaveTextContent('List')
    expect(badges[1]).toHaveTextContent('Shell')
  })
})

// ── TDD row 15: SubagentBlock_Expanded_FinalResult ───────────────────────────

describe('SubagentBlock_Expanded_FinalResult', () => {
  it('renders final result in a dedicated section at the bottom', () => {
    const span = makeSpan({
      status: 'success',
      steps: [makeStep({ id: 't1', call_id: 't1' })],
      finalResult: 'Found 12 Go files',
    })
    render(<SubagentBlock span={span} />)
    fireEvent.click(screen.getByTestId('subagent-collapsed'))

    const expanded = screen.getByTestId('subagent-expanded')
    expect(within(expanded).getByText('Final result')).toBeInTheDocument()
    expect(within(expanded).getByText('Found 12 Go files')).toBeInTheDocument()

    // Final result section must come AFTER the tool call badges
    const children = Array.from(expanded.children)
    const badgeIdx = children.findIndex((el) => el.querySelector('button'))
    const resultIdx = children.findIndex((el) => el.textContent?.includes('Final result'))
    expect(badgeIdx).toBeGreaterThanOrEqual(0)
    expect(resultIdx).toBeGreaterThan(badgeIdx)
  })

  it('does not render final result section when finalResult is absent', () => {
    const span = makeSpan({ status: 'success', steps: [] })
    render(<SubagentBlock span={span} />)
    fireEvent.click(screen.getByTestId('subagent-collapsed'))
    expect(screen.queryByText('Final result')).toBeNull()
  })
})

// ── TDD row 16: SubagentBlock_TerminalStatuses ───────────────────────────────

describe('SubagentBlock_TerminalStatuses', () => {
  const terminalCases: Array<{ status: SubagentSpan['status']; labelMatch: string }> = [
    { status: 'success', labelMatch: 'done' },
    { status: 'error', labelMatch: 'failed' },
    { status: 'interrupted', labelMatch: 'interrupted' },
    { status: 'cancelled', labelMatch: 'cancelled' },
  ]

  for (const { status, labelMatch } of terminalCases) {
    it(`status="${status}" renders correct label and block is expandable`, () => {
      const span = makeSpan({ status, steps: [makeStep({ id: 't1', call_id: 't1' })] })
      render(<SubagentBlock span={span} />)
      const header = screen.getByTestId('subagent-collapsed')
      expect(header).toHaveTextContent(labelMatch)

      // Should be expandable regardless of terminal status
      fireEvent.click(header)
      expect(screen.getByTestId('subagent-expanded')).toBeInTheDocument()
    })
  }

  it('running state shows spinner and "working" label', () => {
    render(<SubagentBlock span={makeSpan({ status: 'running' })} />)
    expect(screen.getByTestId('subagent-collapsed')).toHaveTextContent('working')
  })
})

// ── TDD row 17: SubagentBlock_a11y_WCAG21_AA ─────────────────────────────────

describe('SubagentBlock_a11y_WCAG21_AA', () => {
  async function runAxe(container: HTMLElement) {
    const results = await axe.run(container, {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'] },
    })
    return results.violations.filter((v) =>
      // Only check violations on elements within [data-testid^="subagent-"]
      v.nodes.some((n) =>
        n.target.some((t) => typeof t === 'string' && t.includes('subagent-'))
      )
    )
  }

  it('collapsed state has zero WCAG 2.1 AA violations on subagent elements', async () => {
    const span = makeSpan({ status: 'running', steps: [] })
    const { container } = render(<SubagentBlock span={span} />)
    const violations = await runAxe(container)
    expect(violations).toHaveLength(0)
  })

  it('expanded state has zero WCAG 2.1 AA violations on subagent elements', async () => {
    const span = makeSpan({
      status: 'success',
      steps: [makeStep({ id: 't1', call_id: 't1' })],
      finalResult: 'Result text',
    })
    const { container } = render(<SubagentBlock span={span} />)
    fireEvent.click(screen.getByTestId('subagent-collapsed'))
    const violations = await runAxe(container)
    expect(violations).toHaveLength(0)
  })

  it('header button has aria-expanded attribute', () => {
    render(<SubagentBlock span={makeSpan()} />)
    const btn = screen.getByTestId('subagent-collapsed')
    expect(btn).toHaveAttribute('aria-expanded', 'false')
    fireEvent.click(btn)
    expect(btn).toHaveAttribute('aria-expanded', 'true')
  })
})

// ── TDD row 18: SubagentBlock_Keyboard_EnterAndSpace ─────────────────────────

describe('SubagentBlock_Keyboard_EnterAndSpace', () => {
  it('Enter key toggles expansion', async () => {
    const user = userEvent.setup()
    render(<SubagentBlock span={makeSpan({ status: 'success', steps: [] })} />)
    const btn = screen.getByTestId('subagent-collapsed')

    btn.focus()
    expect(btn).toHaveAttribute('aria-expanded', 'false')

    await user.keyboard('{Enter}')
    expect(btn).toHaveAttribute('aria-expanded', 'true')

    await user.keyboard('{Enter}')
    expect(btn).toHaveAttribute('aria-expanded', 'false')
  })

  it('Space key toggles expansion', async () => {
    const user = userEvent.setup()
    render(<SubagentBlock span={makeSpan({ status: 'success', steps: [] })} />)
    const btn = screen.getByTestId('subagent-collapsed')

    btn.focus()
    expect(btn).toHaveAttribute('aria-expanded', 'false')

    await user.keyboard(' ')
    expect(btn).toHaveAttribute('aria-expanded', 'true')

    await user.keyboard(' ')
    expect(btn).toHaveAttribute('aria-expanded', 'false')
  })
})

// ── TDD row 19: SubagentBlock_LabelTruncation ────────────────────────────────

describe('SubagentBlock_LabelTruncation', () => {
  it('60-char task label renders in full with no ellipsis', () => {
    const label = 'a'.repeat(60)
    render(<SubagentBlock span={makeSpan({ taskLabel: label })} />)
    const header = screen.getByTestId('subagent-collapsed')
    expect(header.textContent).toContain(label)
    expect(header.textContent).not.toContain('\u2026')
  })

  it('61-char task label is truncated to first 60 chars + ellipsis', () => {
    const label = 'b'.repeat(61)
    render(<SubagentBlock span={makeSpan({ taskLabel: label })} />)
    const header = screen.getByTestId('subagent-collapsed')
    expect(header.textContent).toContain('b'.repeat(60) + '\u2026')
    expect(header.textContent).not.toContain('b'.repeat(61))
  })

  it('emoji label counts grapheme clusters, not bytes', () => {
    // Each emoji is 1 grapheme cluster. 60 emojis → no truncation. 61 → truncated.
    const sixtyEmojis = '\uD83C\uDF89'.repeat(60) // 60 party-popper emoji (each = 2 code units, 1 cluster)
    const { rerender } = render(<SubagentBlock span={makeSpan({ taskLabel: sixtyEmojis })} />)
    expect(screen.getByTestId('subagent-collapsed').textContent).not.toContain('\u2026')

    const sixtyOneEmojis = '\uD83C\uDF89'.repeat(61)
    rerender(<SubagentBlock span={makeSpan({ taskLabel: sixtyOneEmojis })} />)
    expect(screen.getByTestId('subagent-collapsed').textContent).toContain('\u2026')
  })

  it('label=dataset D7: label wins over task', () => {
    // If taskLabel is set (populated from span.taskLabel which comes from spawn label),
    // it is used directly regardless of length
    render(<SubagentBlock span={makeSpan({ taskLabel: 'X' })} />)
    expect(screen.getByTestId('subagent-collapsed')).toHaveTextContent('X')
  })
})

// ── Scenario 13: two sibling spans expand independently ──────────────────────

describe('SubagentBlock sibling blocks', () => {
  it('two blocks expand independently', () => {
    const span1 = makeSpan({ spanId: 'span_c1', parentCallId: 'c1', taskLabel: 'First task', status: 'success' })
    const span2 = makeSpan({ spanId: 'span_c2', parentCallId: 'c2', taskLabel: 'Second task', status: 'success' })

    render(
      <>
        <SubagentBlock span={span1} />
        <SubagentBlock span={span2} />
      </>,
    )

    const [header1, header2] = screen.getAllByTestId('subagent-collapsed')

    // Expand block 1
    fireEvent.click(header1)
    expect(header1).toHaveAttribute('aria-expanded', 'true')
    expect(header2).toHaveAttribute('aria-expanded', 'false')

    // Expand block 2
    fireEvent.click(header2)
    expect(header1).toHaveAttribute('aria-expanded', 'true')
    expect(header2).toHaveAttribute('aria-expanded', 'true')

    // Collapse block 1
    fireEvent.click(header1)
    expect(header1).toHaveAttribute('aria-expanded', 'false')
    expect(header2).toHaveAttribute('aria-expanded', 'true')
  })
})
