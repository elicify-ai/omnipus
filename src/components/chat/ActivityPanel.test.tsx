// ActivityPanel — slide-out detail view tests.
//
// Fixture types come from src/hooks/useRunningActivity.ts (ActivityItem,
// AgentActivityItem, BashActivityItem) — this component is purely prop-driven,
// no store/query mocking required.

import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ActivityPanel } from './ActivityPanel'
import type { AgentActivityItem, BashActivityItem } from '@/hooks/useRunningActivity'

function makeAgentItem(overrides: Partial<AgentActivityItem> = {}): AgentActivityItem {
  return {
    kind: 'agent',
    key: overrides.key ?? 'span_1',
    agentId: overrides.agentId ?? 'ray',
    agentName: overrides.agentName ?? 'Ray',
    agentType: overrides.agentType ?? 'native',
    agentColor: overrides.agentColor,
    agentIcon: overrides.agentIcon,
    taskLabel: overrides.taskLabel ?? 'audit files',
    status: overrides.status ?? 'running',
    durationMs: overrides.durationMs,
    steps: overrides.steps ?? [],
  }
}

function makeBashItem(overrides: Partial<BashActivityItem> = {}): BashActivityItem {
  return {
    kind: 'bash',
    key: overrides.key ?? 'call_1',
    command: overrides.command ?? 'npm test',
    status: overrides.status ?? 'running',
    durationMs: overrides.durationMs,
  }
}

describe('ActivityPanel — empty state', () => {
  it('shows a quiet empty message and hides both section headings', () => {
    render(<ActivityPanel open onOpenChange={() => {}} running={[]} recentlyFinished={[]} />)
    expect(screen.getByText('No background activity yet.')).toBeInTheDocument()
    expect(screen.queryByText('Running now')).not.toBeInTheDocument()
    expect(screen.queryByText('Recently finished')).not.toBeInTheDocument()
  })
})

describe('ActivityPanel — running section', () => {
  it('renders a running item under "Running now"', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', taskLabel: 'digging into logs' })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByText('Running now')).toBeInTheDocument()
    expect(screen.getByText('digging into logs')).toBeInTheDocument()
    expect(screen.getByText('running')).toBeInTheDocument()
    expect(screen.queryByText('Recently finished')).not.toBeInTheDocument()
  })
})

describe('ActivityPanel — recently finished section', () => {
  it('renders a finished error item with distinct, first-class error styling', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[makeAgentItem({ status: 'error', taskLabel: 'broken task', durationMs: 500 })]}
      />,
    )
    expect(screen.getByText('Recently finished')).toBeInTheDocument()
    const row = screen.getByTestId('activity-row')
    expect(row).toHaveAttribute('data-status', 'error')
    expect(screen.getByText('failed')).toBeInTheDocument()
    expect(screen.getByText('500ms')).toBeInTheDocument()
  })

  it('renders a finished success item distinctly from an error item', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[makeAgentItem({ status: 'success', taskLabel: 'finished task' })]}
      />,
    )
    const row = screen.getByTestId('activity-row')
    expect(row).toHaveAttribute('data-status', 'success')
    expect(screen.getByText('done')).toBeInTheDocument()
  })
})

describe('ActivityPanel — expandable native row', () => {
  it('toggles open on click, revealing its ToolCallBadge steps', () => {
    const toolStep = {
      kind: 'tool' as const,
      tool: {
        id: 's1',
        call_id: 's1',
        tool: 'fs.list',
        params: {},
        status: 'success' as const,
        result: 'a.txt',
      },
    }
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', steps: [toolStep] })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.queryByTestId('tool-call-badge')).not.toBeInTheDocument()
    const toggle = screen.getByRole('button', { expanded: false })
    // A row that CAN expand must stay enabled (unlike the disabled gate on
    // non-expandable bash/3p rows below).
    expect(toggle).not.toBeDisabled()
    fireEvent.click(toggle)
    expect(screen.getByTestId('tool-call-badge')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { expanded: true }))
    expect(screen.queryByTestId('tool-call-badge')).not.toBeInTheDocument()
  })
})

describe('ActivityPanel — 3rd-party agent row', () => {
  it('shows the static "no live step detail" notice instead of an expand affordance', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[
          makeAgentItem({ status: 'running', agentType: '3p', agentName: 'ClaudeCode', steps: [] }),
        ]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByText('No live step detail yet')).toBeInTheDocument()
    // No expand affordance for 3p rows even when (hypothetically) steps exist client-side.
    const button = screen.getByText('audit files').closest('button')
    expect(button).not.toHaveAttribute('aria-expanded')
    // Inert-focusable fix: a non-expandable row's header button must be
    // genuinely disabled (dropped from the tab order, Enter/Space can't
    // no-op on it), not just missing aria-expanded — mirrors
    // GenericToolCall.tsx's `disabled={!hasDetail}` gate.
    expect(button).toBeDisabled()
    fireEvent.click(button as HTMLButtonElement)
    expect(screen.queryByTestId('tool-call-badge')).not.toBeInTheDocument()
  })
})

describe('ActivityPanel — bash row', () => {
  it('renders a bash item without an expand affordance, and the header button is disabled', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeBashItem({ status: 'running', command: 'npm run build' })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByText('npm run build')).toBeInTheDocument()
    const button = screen.getByText('npm run build').closest('button')
    expect(button).not.toHaveAttribute('aria-expanded')
    expect(button).toBeDisabled()
  })
})

describe('ActivityPanel — remaining status coverage (cancelled/interrupted/timeout)', () => {
  it('renders a cancelled item with cancelled-specific pill color and label, distinct from error/success', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[makeAgentItem({ status: 'cancelled', taskLabel: 'stopped task' })]}
      />,
    )
    const row = screen.getByTestId('activity-row')
    expect(row).toHaveAttribute('data-status', 'cancelled')
    expect(screen.getByText('cancelled')).toBeInTheDocument()

    const pill = screen.getByText('cancelled').parentElement
    expect(pill?.className).toContain('--color-cancelled')
    // Distinct from every other status's pill color.
    expect(pill?.className).not.toContain('--color-error')
    expect(pill?.className).not.toContain('--color-success')
    expect(pill?.className).not.toContain('--color-accent')
  })

  it('renders an interrupted item with muted pill color, its own label, and a Prohibit icon', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[makeAgentItem({ status: 'interrupted', taskLabel: 'stopped mid-flight' })]}
      />,
    )
    const row = screen.getByTestId('activity-row')
    expect(row).toHaveAttribute('data-status', 'interrupted')
    expect(screen.getByText('interrupted')).toBeInTheDocument()

    const pill = screen.getByText('interrupted').parentElement
    expect(pill?.className).toContain('--color-muted')
    expect(pill?.className).not.toContain('--color-cancelled')
    // Distinct label from every other muted-colored status (timeout).
    expect(screen.queryByText('timed out')).not.toBeInTheDocument()
  })

  it('renders a timeout item with its own label and icon, distinguishable from interrupted despite sharing muted pill color', () => {
    const { unmount } = render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[makeAgentItem({ status: 'timeout', taskLabel: 'ran too long' })]}
      />,
    )
    const row = screen.getByTestId('activity-row')
    expect(row).toHaveAttribute('data-status', 'timeout')
    expect(screen.getByText('timed out')).toBeInTheDocument()

    const pill = screen.getByText('timed out').parentElement
    expect(pill?.className).toContain('--color-muted')
    // Label is the discriminator vs. interrupted (both use muted color/Prohibit-family treatment).
    expect(screen.queryByText('interrupted')).not.toBeInTheDocument()
    const timeoutIconSvg = pill?.querySelector('svg')?.outerHTML

    unmount()

    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[makeAgentItem({ status: 'interrupted', taskLabel: 'ran too long' })]}
      />,
    )
    const interruptedPill = screen.getByText('interrupted').parentElement
    const interruptedIconSvg = interruptedPill?.querySelector('svg')?.outerHTML

    // Icon glyph itself (Clock vs Prohibit) differs even though color class matches.
    expect(timeoutIconSvg).toBeTruthy()
    expect(interruptedIconSvg).toBeTruthy()
    expect(timeoutIconSvg).not.toBe(interruptedIconSvg)
  })
})

describe('ActivityPanel — bash-kind error state', () => {
  it('renders a failed background bash item with the same error-distinct treatment as an agent error row', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[makeBashItem({ status: 'error', command: 'npm run lint', durationMs: 2300 })]}
      />,
    )
    expect(screen.getByText('Recently finished')).toBeInTheDocument()
    const row = screen.getByTestId('activity-row')
    expect(row).toHaveAttribute('data-status', 'error')
    expect(screen.getByText('npm run lint')).toBeInTheDocument()
    expect(screen.getByText('failed')).toBeInTheDocument()
    expect(screen.getByText('2.3s')).toBeInTheDocument()

    const pill = screen.getByText('failed').parentElement
    expect(pill?.className).toContain('--color-error')
    // Same error-distinct pill color an agent error row gets (kind is irrelevant to status styling) —
    // this is the case that surfaces background-bash failures that are otherwise hidden from inline chat.
    expect(pill?.className).not.toContain('--color-success')
    expect(pill?.className).not.toContain('--color-muted')
    expect(pill?.className).not.toContain('--color-cancelled')
  })
})
