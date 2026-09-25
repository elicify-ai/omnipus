// ActivityPanel — slide-out detail view tests.
//
// Fixture types come from src/hooks/useRunningActivity.ts (ActivityItem,
// AgentActivityItem, BashActivityItem) — this component is purely prop-driven
// except for the ADR-091 additions below (the approval queue store, and
// navigation for the open control).
//
// ADR-091 D7/D10: the nested per-step detail (SubagentBlock's steps,
// ToolCallBadge surface="panel") is gone from this file along with it — a
// child's own tool calls carry the child's own session_id (I-4) and never
// arrive in this bucket any more, so `AgentActivityItem` no longer carries a
// `steps` field. This file's step-list-specific describe blocks ("expandable
// native row", "panel-only step visibility policy", "delegated browser call,
// the partial fallback") are removed with it; final result / interrupt
// reason (subagent_end's own fields, untouched by that deletion) stay
// covered. New coverage: the row's status line, the "queued" state, the
// "awaiting approval: <tool>" override, and the open control.

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { act } from 'react'
import { ActivityPanel } from './ActivityPanel'
import type { AgentActivityItem, BashActivityItem } from '@/hooks/useRunningActivity'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { useToolApprovalStore } from '@/store/toolApproval'

const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

beforeEach(() => {
  mockNavigate.mockClear()
  act(() => {
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
    useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
  })
})

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
    finalResult: overrides.finalResult,
    interruptReason: overrides.interruptReason,
    statusLine: overrides.statusLine,
    lifecycleState: overrides.lifecycleState,
    childSessionId: overrides.childSessionId,
    lastUpdateAt: overrides.lastUpdateAt,
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

// ── Fix 2 (2026-07-16): panel carries the final result / interrupt reason ──
// SubagentBlock's thread card (now deleted, ADR-091 D7/D10) was the only
// surface showing span.finalResult and a human-readable interrupt reason;
// useRunningActivity.ts carries both onto AgentActivityItem so the panel —
// the durable surface for this detail now — can render them.

describe('ActivityPanel — final result / interrupt reason (Fix 2)', () => {
  it('an expanded finished item shows its final result in a labeled block', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[
          makeAgentItem({
            status: 'success',
            taskLabel: 'summarize logs',
            finalResult: 'Found 3 errors in the last hour.',
          }),
        ]}
      />,
    )
    const toggle = screen.getByRole('button', { expanded: false })
    expect(toggle).not.toBeDisabled()
    fireEvent.click(toggle)
    expect(screen.getByText('Final result')).toBeInTheDocument()
    expect(screen.getByText('Found 3 errors in the last hour.')).toBeInTheDocument()
  })

  it('an expanded interrupted item appends the human-readable reason to the status text', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[
          makeAgentItem({
            status: 'interrupted',
            taskLabel: 'long task',
            finalResult: 'partial output',
            interruptReason: 'parent_timeout',
          }),
        ]}
      />,
    )
    expect(screen.getByText('interrupted')).toBeInTheDocument()
    expect(screen.getByText('(parent timed out)')).toBeInTheDocument()
  })

  it('does not render a "Final result" block when the finished item has none, and the row is not expandable', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[makeAgentItem({ status: 'success', taskLabel: 'no result' })]}
      />,
    )
    const toggle = screen.getByText('no result').closest('button')
    expect(toggle).toBeDisabled()
    expect(screen.queryByText('Final result')).not.toBeInTheDocument()
  })
})

describe('ActivityPanel — 3rd-party agent row', () => {
  it('shows the static "no live step detail" notice instead of an expand affordance', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[
          makeAgentItem({ status: 'running', agentType: '3p', agentName: 'ClaudeCode' }),
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
  it('renders a cancelled item with a cancelled-specific dot color and label, distinct from error/success', () => {
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

    // Flat-dot design (getSpanStatusDot): the status color lives on the 8px
    // dot indicator (the label's immediately preceding sibling), not on the
    // label text itself, which is always muted.
    const dot = screen.getByText('cancelled').previousElementSibling
    expect(dot?.getAttribute('class')).toContain('bg-[var(--color-cancelled)]')
    // Distinct from every other status's dot color.
    expect(dot?.getAttribute('class')).not.toContain('--color-error')
    expect(dot?.getAttribute('class')).not.toContain('--color-success')
    expect(dot?.getAttribute('class')).not.toContain('--color-accent')
  })

  it('renders an interrupted item with a muted dot color and its own label', () => {
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

    const dot = screen.getByText('interrupted').previousElementSibling
    expect(dot?.getAttribute('class')).toContain('bg-[var(--color-muted)]')
    expect(dot?.getAttribute('class')).not.toContain('--color-cancelled')
    // Distinct label from every other muted-colored status (timeout).
    expect(screen.queryByText('timed out')).not.toBeInTheDocument()
  })

  it('renders a timeout item with its own label, sharing the exact same muted dot as interrupted (label is the sole discriminator under the flat-dot design)', () => {
    // getSpanStatusDot (src/lib/toolStatusConfig.tsx) collapses the old
    // pill family's per-status Clock/Prohibit icon distinction into a single
    // shared muted dot for both 'interrupted' and 'timeout' — see that
    // file's own unit tests. This test now asserts that documented parity
    // instead of an icon-glyph difference that no longer exists.
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

    const timeoutDot = screen.getByText('timed out').previousElementSibling
    expect(timeoutDot?.getAttribute('class')).toContain('bg-[var(--color-muted)]')
    // Label is the discriminator vs. interrupted.
    expect(screen.queryByText('interrupted')).not.toBeInTheDocument()
    const timeoutDotClass = timeoutDot?.getAttribute('class')

    unmount()

    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[makeAgentItem({ status: 'interrupted', taskLabel: 'ran too long' })]}
      />,
    )
    const interruptedDot = screen.getByText('interrupted').previousElementSibling

    // Same dot class for both — no icon/color distinction remains between
    // interrupted and timeout, only the label text differs.
    expect(interruptedDot?.getAttribute('class')).toBe(timeoutDotClass)
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

    const dot = screen.getByText('failed').previousElementSibling
    expect(dot?.getAttribute('class')).toContain('bg-[var(--color-error)]')
    // Same error-distinct dot color an agent error row gets (kind is irrelevant to status styling) —
    // this is the case that surfaces background-bash failures that are otherwise hidden from inline chat.
    expect(dot?.getAttribute('class')).not.toContain('--color-success')
    expect(dot?.getAttribute('class')).not.toContain('--color-muted')
    expect(dot?.getAttribute('class')).not.toContain('--color-cancelled')
  })
})

// ── ADR-091 D7/FR-E-004: the row's status line ──────────────────────────────

describe('ActivityPanel — status line (ADR-091 FR-E-004)', () => {
  it('shows the last subagent_message.text as the status line', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', statusLine: 'checking the checkout page' })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByTestId('activity-row-status-line')).toHaveTextContent('checking the checkout page')
  })

  it('falls back to "last update N s ago" before any subagent_message has arrived', () => {
    const tenSecondsAgo = new Date(Date.now() - 10_000).toISOString()
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', statusLine: undefined, lastUpdateAt: tenSecondsAgo })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByTestId('activity-row-status-line')).toHaveTextContent(/last update \d+ s ago/)
  })

  it('renders no status line when neither statusLine nor lastUpdateAt is present (a pre-delivery transcript, edge case)', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', statusLine: undefined, lastUpdateAt: undefined })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.queryByTestId('activity-row-status-line')).not.toBeInTheDocument()
  })

  it('a bash item never shows a status line — the field only exists on agent items', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeBashItem({ status: 'running' })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.queryByTestId('activity-row-status-line')).not.toBeInTheDocument()
  })
})

// ── ADR-091 D7/FR-E-004: "queued" state ─────────────────────────────────────

describe('ActivityPanel — queued state (ADR-091 FR-E-004)', () => {
  it('reads "queued" when lifecycleState is queued, even though the span itself is status: running', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', lifecycleState: 'queued', statusLine: 'ignored while queued' })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByText('queued')).toBeInTheDocument()
    expect(screen.queryByText('running')).not.toBeInTheDocument()
    // No fabricated status line while queued — nothing has happened yet.
    expect(screen.queryByTestId('activity-row-status-line')).not.toBeInTheDocument()
  })

  it('reads "running" once the child starts (lifecycleState transitions away from queued)', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', lifecycleState: 'running' })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByText('running')).toBeInTheDocument()
    expect(screen.queryByText('queued')).not.toBeInTheDocument()
  })
})

// ── Cross-family review finding 21: lifecycleState drives the row's DOT,
// not just the 'queued' label text. Every case below sets `status: 'running'`
// — the parent's own "span still open" flag, true for a real child from
// subagent_start until subagent_end — while `lifecycleState` carries a
// DIFFERENT value, exactly the race the finding describes (a subagent_state
// announcing e.g. 'completed' can arrive before the matching subagent_end).
// Before the fix, every one of these rendered the spinning 'running'
// indicator regardless of lifecycleState; after the fix, the dot and label
// come from lifecycleState whenever it is present, span status only as a
// legacy fallback (last test in this block).
describe('ActivityPanel — lifecycle state drives the row dot, not span status (ADR-091 cross-family review finding 21)', () => {
  it('needs_input renders a distinct warning dot and label, not "running"', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', lifecycleState: 'needs_input' })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByText('needs input')).toBeInTheDocument()
    expect(screen.queryByText('running')).not.toBeInTheDocument()
    const dot = screen.getByText('needs input').previousElementSibling
    expect(dot?.getAttribute('class')).toContain('bg-[var(--color-warning)]')
  })

  it('paused renders a distinct warning dot and label, not "running"', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', lifecycleState: 'paused' })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByText('paused')).toBeInTheDocument()
    expect(screen.queryByText('running')).not.toBeInTheDocument()
    const dot = screen.getByText('paused').previousElementSibling
    expect(dot?.getAttribute('class')).toContain('bg-[var(--color-warning)]')
  })

  it('completed renders a success dot and "done" label even while the span itself is still status: running (subagent_end has not arrived yet)', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', lifecycleState: 'completed' })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByText('done')).toBeInTheDocument()
    expect(screen.queryByText('running')).not.toBeInTheDocument()
    const dot = screen.getByText('done').previousElementSibling
    expect(dot?.getAttribute('class')).toContain('bg-[var(--color-success)]')
  })

  it('failed renders an error dot and "failed" label even while the span itself is still status: running', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', lifecycleState: 'failed' })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByText('failed')).toBeInTheDocument()
    expect(screen.queryByText('running')).not.toBeInTheDocument()
    const dot = screen.getByText('failed').previousElementSibling
    expect(dot?.getAttribute('class')).toContain('bg-[var(--color-error)]')
  })

  it('cancelled renders a cancelled dot and label even while the span itself is still status: running', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', lifecycleState: 'cancelled' })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByText('cancelled')).toBeInTheDocument()
    expect(screen.queryByText('running')).not.toBeInTheDocument()
    const dot = screen.getByText('cancelled').previousElementSibling
    expect(dot?.getAttribute('class')).toContain('bg-[var(--color-cancelled)]')
  })

  it('timed_out renders a muted dot and "timed out" label even while the span itself is still status: running', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', lifecycleState: 'timed_out' })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByText('timed out')).toBeInTheDocument()
    expect(screen.queryByText('running')).not.toBeInTheDocument()
    const dot = screen.getByText('timed out').previousElementSibling
    expect(dot?.getAttribute('class')).toContain('bg-[var(--color-muted)]')
  })

  it('queued renders a distinct (non-spinning) dot, not the running spinner — the label override alone was the old, incomplete fix', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', lifecycleState: 'queued' })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByText('queued')).toBeInTheDocument()
    // The old bug: the dot stayed the spinning 'running' indicator (an
    // ArrowsClockwise icon, not a `span` dot) even though the label read
    // "queued". A real dot element must be present instead.
    const dot = screen.getByText('queued').previousElementSibling
    expect(dot?.tagName).toBe('SPAN')
    expect(dot?.getAttribute('class')).toContain('bg-[var(--color-muted)]')
  })

  it('running (lifecycleState) renders the spinning indicator and "running" label', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', lifecycleState: 'running' })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByText('running')).toBeInTheDocument()
    // The spinning indicator is an SVG icon (ArrowsClockwise), not a `span` dot.
    const indicator = screen.getByText('running').previousElementSibling
    expect(indicator?.tagName).toBe('svg')
    expect(indicator?.getAttribute('class')).toContain('animate-spin')
  })

  it('falls back to span status when lifecycleState is absent (legacy transcript, or before the first subagent_state arrives)', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[makeAgentItem({ status: 'error', lifecycleState: undefined })]}
      />,
    )
    expect(screen.getByText('failed')).toBeInTheDocument()
    const dot = screen.getByText('failed').previousElementSibling
    expect(dot?.getAttribute('class')).toContain('bg-[var(--color-error)]')
  })
})

// ── ADR-091 D7/FR-E-009: awaiting approval ──────────────────────────────────

describe('ActivityPanel — awaiting approval (ADR-091 FR-E-009)', () => {
  function pendingApproval(sessionId: string, toolName: string) {
    return {
      approvalId: 'appr_1',
      toolCallId: 'call_1',
      toolName,
      args: {},
      agentId: 'agent-child',
      sessionId,
      turnId: 'turn_1',
      expiresAt: Date.now() + 60_000,
    }
  }

  it('reads "awaiting approval: bash" while the approval queue holds a pending approval for the child session', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [pendingApproval('child-sess-1', 'bash')] })
    })
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[
          makeAgentItem({ status: 'running', childSessionId: 'child-sess-1', statusLine: 'was doing something' }),
        ]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByTestId('activity-row-status-line')).toHaveTextContent('awaiting approval: bash')
  })

  it('returns to its own status line once the approval is resolved (removed from the queue)', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [pendingApproval('child-sess-2', 'bash')] })
    })
    const { rerender } = render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[
          makeAgentItem({ status: 'running', childSessionId: 'child-sess-2', statusLine: 'checking logs' }),
        ]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByTestId('activity-row-status-line')).toHaveTextContent('awaiting approval: bash')

    act(() => {
      useToolApprovalStore.setState({ queue: [] })
    })
    rerender(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[
          makeAgentItem({ status: 'running', childSessionId: 'child-sess-2', statusLine: 'checking logs' }),
        ]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByTestId('activity-row-status-line')).toHaveTextContent('checking logs')
  })

  it('an approval pending for a DIFFERENT session does not affect this row', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [pendingApproval('some-other-session', 'bash')] })
    })
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[
          makeAgentItem({ status: 'running', childSessionId: 'child-sess-3', statusLine: 'my own status' }),
        ]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByTestId('activity-row-status-line')).toHaveTextContent('my own status')
  })
})

// ── ADR-091 D7/FR-E-004: the open control ───────────────────────────────────

describe('ActivityPanel — open control (ADR-091 FR-E-004)', () => {
  it('renders an open control when childSessionId is present, and navigates to the child session on click', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', childSessionId: 'child-sess-open-1' })]}
        recentlyFinished={[]}
      />,
    )
    const openControl = screen.getByTestId('activity-row-open')
    expect(openControl).toBeInTheDocument()
    fireEvent.click(openControl)
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/sessions/$sessionId',
      params: { sessionId: 'child-sess-open-1' },
    })
  })

  it('renders no open control when childSessionId is absent (a transcript written before this delivery, edge case)', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', childSessionId: undefined })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.queryByTestId('activity-row-open')).not.toBeInTheDocument()
  })

  it('a bash item never shows an open control', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeBashItem({ status: 'running' })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.queryByTestId('activity-row-open')).not.toBeInTheDocument()
  })
})

describe('ActivityPanel — Queued section and background commands (AC-3, AC-4)', () => {
  it('AC-4: numbers queued children in list order under Queued, and does not repeat them under Running now', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[
          makeAgentItem({ key: 'run', taskLabel: 'already going', lifecycleState: 'running' }),
          makeAgentItem({ key: 'q-zeta', taskLabel: 'zeta first', lifecycleState: 'queued' }),
          makeAgentItem({ key: 'q-alpha', taskLabel: 'alpha second', lifecycleState: 'queued' }),
        ]}
        recentlyFinished={[]}
      />,
    )
    const queued = screen.getByTestId('activity-section-queued')
    expect(within(queued).getByRole('heading', { name: 'Queued' })).toBeInTheDocument()
    expect(within(queued).getAllByTestId('activity-queue-position').map((el) => el.textContent)).toEqual(['1', '2'])
    const queuedText = queued.textContent ?? ''
    expect(queuedText.indexOf('zeta first')).toBeLessThan(queuedText.indexOf('alpha second'))
    expect(within(queued).queryByText('already going')).not.toBeInTheDocument()
    const runningNow = screen.getByTestId('activity-section-running')
    expect(within(runningNow).getByText('already going')).toBeInTheDocument()
    expect(within(runningNow).queryByText('zeta first')).not.toBeInTheDocument()
    expect(screen.getByText('zeta first')).toBeInTheDocument()
  })

  it('puts a running background command in the background-commands section, not under Running now', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[
          makeAgentItem({ taskLabel: 'digging into logs', lifecycleState: 'running' }),
          makeBashItem({ command: 'npm test' }),
        ]}
        recentlyFinished={[]}
      />,
    )
    const commands = screen.getByTestId('activity-section-commands')
    expect(within(commands).getByRole('heading', { name: 'Background commands' })).toBeInTheDocument()
    expect(within(commands).getByText('npm test')).toBeInTheDocument()
    expect(within(screen.getByTestId('activity-section-running')).queryByText('npm test')).not.toBeInTheDocument()
  })

  it('scrolls to a retained failed command when the live commands section is absent', async () => {
    Element.prototype.scrollIntoView ??= () => {}
    const scrollIntoView = vi.spyOn(Element.prototype, 'scrollIntoView').mockImplementation(() => {})
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        scrollRequest={{ section: 'commands', nonce: 1 }}
        running={[]}
        recentlyFinished={[makeBashItem({ status: 'error', command: 'npm test', durationMs: 400 })]}
      />,
    )

    // The commands section lists only commands that are still running.
    expect(screen.queryByTestId('activity-section-commands')).not.toBeInTheDocument()
    const command = screen.getByText('npm test')
    expect(screen.getByText('Recently finished')).toBeInTheDocument()
    expect(command).toBeInTheDocument()

    await waitFor(() => {
      const scrolledToCommand = scrollIntoView.mock.instances.some(
        (node) => node instanceof Element && node.contains(command),
      )
      expect(scrolledToCommand).toBe(true)
    })
    scrollIntoView.mockRestore()
  })
})
