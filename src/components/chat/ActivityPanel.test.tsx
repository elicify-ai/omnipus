// ActivityPanel — slide-out detail view tests.
//
// Fixture types come from src/hooks/useRunningActivity.ts (ActivityItem,
// AgentActivityItem, BashActivityItem) — this component is purely prop-driven,
// no store/query mocking required.

import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { ActivityPanel } from './ActivityPanel'
import type { AgentActivityItem, BashActivityItem } from '@/hooks/useRunningActivity'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { shouldRenderToolCallInPanel } from '@/lib/toolVisibility'

beforeEach(() => {
  act(() => {
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
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
    steps: overrides.steps ?? [],
    finalResult: overrides.finalResult,
    interruptReason: overrides.interruptReason,
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

// ── Fix 2 (2026-07-16): panel carries the final result / interrupt reason ──
// SubagentBlock's thread card (now hidden from the thread by default) was
// the only surface showing span.finalResult and a human-readable interrupt
// reason. useRunningActivity.ts now carries both onto AgentActivityItem so
// the panel — the durable default-visible surface for this detail — can
// render them too.

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

  it('a finished item with zero steps but a final result is still expandable', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[
          makeAgentItem({ status: 'success', steps: [], finalResult: 'done quickly' }),
        ]}
      />,
    )
    const toggle = screen.getByRole('button', { expanded: false })
    expect(toggle).not.toBeDisabled()
    fireEvent.click(toggle)
    expect(screen.getByText('done quickly')).toBeInTheDocument()
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
            steps: [],
            finalResult: 'partial output',
            interruptReason: 'parent_timeout',
          }),
        ]}
      />,
    )
    expect(screen.getByText('interrupted')).toBeInTheDocument()
    expect(screen.getByText('(parent timed out)')).toBeInTheDocument()
  })

  // (item 8g, 2026-07-16 fix wave): expansion-with-steps was only ever
  // exercised for RUNNING items (see "ActivityPanel — expandable native
  // row" above) — this pins the same behavior for a FINISHED
  // (recentlyFinished) item, whose steps are just as reachable.
  it('an expanded finished item shows its (non-ToolSearch) steps', () => {
    const visibleStep = {
      kind: 'tool' as const,
      tool: { id: 'fin1', call_id: 'fin1', tool: 'fs.list', params: { path: '/tmp' }, status: 'success' as const, result: 'a.txt' },
    }
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[makeAgentItem({ status: 'success', taskLabel: 'listed files', steps: [visibleStep] })]}
      />,
    )
    const toggle = screen.getByRole('button', { expanded: false })
    expect(toggle).not.toBeDisabled()
    fireEvent.click(toggle)
    const badge = screen.getByTestId('tool-call-badge')
    expect(badge).toHaveAttribute('data-tool', 'fs.list')
  })

  it('an expanded finished item HIDES a ToolSearch step by default (non-verbose) but shows other steps', () => {
    const loadToolStep = {
      kind: 'tool' as const,
      tool: { id: 'fin2', call_id: 'fin2', tool: 'ToolSearch', params: { name: 'web_search' }, status: 'success' as const, result: 'ok' },
    }
    const visibleStep = {
      kind: 'tool' as const,
      tool: { id: 'fin3', call_id: 'fin3', tool: 'fs.list', params: {}, status: 'success' as const, result: 'a.txt' },
    }
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[makeAgentItem({ status: 'success', steps: [loadToolStep, visibleStep] })]}
      />,
    )
    fireEvent.click(screen.getByRole('button', { expanded: false }))
    const badges = screen.getAllByTestId('tool-call-badge')
    expect(badges).toHaveLength(1)
    expect(badges[0]).toHaveAttribute('data-tool', 'fs.list')
  })

  it('does not render a "Final result" block when the finished item has none', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[
          makeAgentItem({
            status: 'success',
            steps: [{ kind: 'tool', tool: { id: 's1', call_id: 's1', tool: 'fs.list', params: {}, status: 'success', result: 'a.txt' } }],
          }),
        ]}
      />,
    )
    fireEvent.click(screen.getByRole('button', { expanded: false }))
    expect(screen.queryByText('Final result')).not.toBeInTheDocument()
  })
})

// ── Fix 2 (2026-07-16, revised same day): panel-only step visibility ───────
// The panel is the designated transparency surface for exactly the detail
// the thread hides by default — its default INVERTS to show everything
// except `ToolSearch` (shouldRenderToolCallInPanel, toolVisibility.ts),
// rather than applying the thread's shouldRenderToolCall hidden-set (which
// would hide a bash poll/read step, a background delegate dispatch, etc.).
// ToolCallBadge is told to use this policy via surface="panel" — see
// ActivityPanel.tsx's step-mapping.

describe('ActivityPanel — panel-only step visibility policy (shows all but ToolSearch)', () => {
  function makeToolStep(overrides: Partial<import('@/lib/api').ToolCall & { call_id: string }> = {}) {
    return {
      kind: 'tool' as const,
      tool: {
        id: overrides.id ?? 'step_1',
        call_id: overrides.call_id ?? overrides.id ?? 'step_1',
        tool: overrides.tool ?? 'bash',
        params: overrides.params ?? {},
        status: overrides.status ?? 'success' as const,
        result: overrides.result,
      },
    }
  }

  it('SHOWS a bash {action:"poll"} step — hidden in the thread, but visible here', () => {
    const pollStep = makeToolStep({ id: 'poll1', call_id: 'poll1', tool: 'bash', params: { action: 'poll' } })
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', steps: [pollStep] })]}
        recentlyFinished={[]}
      />,
    )
    fireEvent.click(screen.getByRole('button', { expanded: false }))
    const badge = screen.getByTestId('tool-call-badge')
    expect(badge).toBeInTheDocument()
    expect(badge).toHaveAttribute('data-tool', 'bash')
  })

  it('HIDES a ToolSearch step by default (non-verbose)', () => {
    const loadToolStep = makeToolStep({ id: 'lt1', call_id: 'lt1', tool: 'ToolSearch' })
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', steps: [loadToolStep] })]}
        recentlyFinished={[]}
      />,
    )
    fireEvent.click(screen.getByRole('button', { expanded: false }))
    expect(screen.queryByTestId('tool-call-badge')).not.toBeInTheDocument()
  })

  it('SHOWS a ToolSearch step once verbose chat is enabled', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    const loadToolStep = makeToolStep({ id: 'lt1', call_id: 'lt1', tool: 'ToolSearch' })
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', steps: [loadToolStep] })]}
        recentlyFinished={[]}
      />,
    )
    fireEvent.click(screen.getByRole('button', { expanded: false }))
    const badge = screen.getByTestId('tool-call-badge')
    expect(badge).toBeInTheDocument()
    expect(badge).toHaveAttribute('data-tool', 'ToolSearch')
  })

  it('SHOWS a failed (error-status) delegate step — the panel is the transparency surface for exactly what the thread hides on failure', () => {
    const failedDelegateStep = makeToolStep({
      id: 'd1',
      call_id: 'd1',
      tool: 'delegate',
      params: {},
      status: 'error',
      result: { error: 'delegation_denied', reason: 'nope', policy: 'mode', tool: 'delegate' },
    })
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeAgentItem({ status: 'running', steps: [failedDelegateStep] })]}
        recentlyFinished={[]}
      />,
    )
    fireEvent.click(screen.getByRole('button', { expanded: false }))
    const badge = screen.getByTestId('tool-call-badge')
    expect(badge).toBeInTheDocument()
    expect(badge).toHaveAttribute('data-tool', 'delegate')
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

// ── ADR-057: the ADR-053 FE-5 Agent-View session list (lifecycle badge +
// peek/reply/steer/stop affordances) has been removed as dead code — its
// sole data source, mid-span `subagent_message`/`subagent_state` frames, has
// zero Go emitters and can never be populated. See ActivityPanel.tsx's own
// header comment. This describe block covered that removed surface and is
// gone with it; makeAgentItem above no longer accepts the now-deleted
// sessionMessages/lifecycleState/lifecycleSessionId/steeringReceipt fields.

// ── browser-agent-capability-spec FR-039 / S-63, panel half ──────────────
//
// The chat thread does NOT show a delegated browser call at the default
// (asserted in src/lib/toolVisibility.test.ts). The ActivityPanel is the only
// partial fallback, and "partial" is the load-bearing word — the panel is
// narrower than "fully transparent" and these tests assert what it actually
// does, not what it is assumed to do:
//
//   - it aggregates ONLY subagent delegation spans, background bash sessions
//     and judge verdicts (useRunningActivity.ts's own header). A PARENT-turn
//     tool call is none of those, so it never appears here at all — which is
//     why ADR-075 D2.11's "a browser_snapshot never appears there" is right
//     for the parent case and wrong for the delegated one.
//   - a span must still be running, or inside RECENTLY_FINISHED_CAP = 8
//     (useRunningActivity.ts:148), to be listed.
//   - the operator must open the panel AND expand that span.
//   - an external-CLI (3p) sub-agent's steps are dropped upstream at
//     useRunningActivity.ts:549 (`steps: resolved.agentType === '3p' ? [] :
//     span.steps`), so nothing of its browsing reaches any surface.
//
// TESTS ONLY — no SPA source changes.

describe('ActivityPanel — delegated browser call, the partial fallback (FR-039, S-63)', () => {
  function makeBrowserSnapshotStep() {
    return {
      kind: 'tool' as const,
      tool: {
        id: 'snap1',
        call_id: 'snap1',
        tool: 'browser_snapshot',
        params: {},
        status: 'success' as const,
        result: 'textbox "Card number" value="4111 1111 1111 1111"',
      },
    }
  }

  it('direction 3: a NATIVE sub-agent span carries the browser_snapshot call in its expanded steps', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[
          makeAgentItem({
            status: 'running',
            agentType: 'native',
            taskLabel: 'check the checkout form',
            steps: [makeBrowserSnapshotStep()],
          }),
        ]}
        recentlyFinished={[]}
      />,
    )
    // The expand is a real precondition, not a formality — this is limit (3).
    // Before the click the badge must not be present.
    expect(screen.queryByTestId('tool-call-badge')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { expanded: false }))
    const badge = screen.getByTestId('tool-call-badge')
    expect(badge).toBeInTheDocument()
    expect(badge).toHaveAttribute('data-tool', 'browser_snapshot')
  })

  it('the same is true for a finished native span, which is the case an operator actually reads after the fact', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[
          makeAgentItem({
            status: 'success',
            agentType: 'native',
            durationMs: 1200,
            steps: [makeBrowserSnapshotStep()],
          }),
        ]}
      />,
    )
    fireEvent.click(screen.getByRole('button', { expanded: false }))
    expect(screen.getByTestId('tool-call-badge')).toHaveAttribute('data-tool', 'browser_snapshot')
  })

  // Direction 4 — and this is the assertion that stops direction 3's green
  // from being read as "the panel covers delegated browsing". It does not
  // cover the external-CLI population at all.
  //
  // Asserted at the PANEL rather than at the hook on purpose: the upstream
  // mapping (useRunningActivity.ts:549) already empties `steps` for a 3p
  // agent, so handing the panel a 3p item WITH steps is the stronger test —
  // it proves the panel itself refuses to render them, so the guarantee does
  // not rest on that one upstream ternary staying correct.
  it('direction 4: an external-CLI (3p) span renders NO browser step, even when handed one', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[
          makeAgentItem({
            status: 'running',
            agentType: '3p',
            agentName: 'ClaudeCode',
            taskLabel: 'check the checkout form',
            steps: [makeBrowserSnapshotStep()],
          }),
        ]}
        recentlyFinished={[]}
      />,
    )
    // The 3p row is not expandable at all — it shows a static notice instead.
    expect(screen.getByText('No live step detail yet')).toBeInTheDocument()
    const header = screen.getByText('check the checkout form').closest('button')
    expect(header).toBeDisabled()
    fireEvent.click(header as HTMLButtonElement)
    expect(screen.queryByTestId('tool-call-badge')).not.toBeInTheDocument()
    // And the captured value never reaches the DOM on any path.
    expect(screen.queryByText(/4111 1111 1111 1111/)).not.toBeInTheDocument()
  })

  // The panel's OTHER real limit, asserted rather than assumed: it renders
  // only what it is handed. A parent-turn browser call is not a span, not a
  // background bash session and not a judge verdict, so useRunningActivity
  // never produces an item for it — and with no items the panel shows its
  // empty state. This is why the panel is not a fallback for the parent case
  // and the chat thread is the only surface there.
  it('a parent-turn browser call is not something the panel can show — with no spans it is empty', () => {
    render(<ActivityPanel open onOpenChange={() => {}} running={[]} recentlyFinished={[]} />)
    expect(screen.getByText('No background activity yet.')).toBeInTheDocument()
    expect(screen.queryByTestId('tool-call-badge')).not.toBeInTheDocument()
  })

  // The panel's step policy is the INVERSE of the thread's, and a browser tool
  // must be on the visible side of it. Pinned separately from the render
  // assertions above so a regression in the predicate is distinguishable from
  // a regression in the component.
  it('shouldRenderToolCallInPanel admits every one of the six new browser tools', () => {
    for (const tool of [
      'browser_select_option',
      'browser_press_key',
      'browser_hover',
      'browser_upload_file',
      'browser_handle_dialog',
      'browser_snapshot',
    ]) {
      expect(shouldRenderToolCallInPanel(tool, false)).toBe(true)
    }
  })
})
