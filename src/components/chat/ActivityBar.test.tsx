// ActivityBar — persistent activity strip tests.
//
// Covers: idle (0 running), 1 running, N running (span + bash), the count
// text, clicking the bar opens the ActivityPanel slide-out, and (Fix 1,
// 2026-07-16) the revised mount matrix: an open panel survives running→0,
// a retained failure keeps the pill mounted in its failed-state variant,
// a purely-successful idle history still unmounts, and the spinner only
// ever appears while something is running.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ActivityBar } from './ActivityBar'
import { useChatStore } from '@/store/chat'
import type { ChatMessage, SubagentSpan } from '@/store/chat'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      { id: 'ray', name: 'Ray', type: 'Subagent', locked: false, status: 'active', color: '#4488ff', icon: 'compass' },
      { id: 'ext-1', name: 'ClaudeCode', type: 'subagent_3p', locked: false, status: 'active' },
    ]),
  }
})

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderBar() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <ActivityBar />
    </QueryClientProvider>,
  )
}

function makeAssistantMessage(spans: SubagentSpan[]): ChatMessage {
  return {
    id: 'msg_1',
    role: 'assistant',
    content: '',
    timestamp: new Date().toISOString(),
    status: 'done',
    spans,
  } as ChatMessage
}

function runningSpan(overrides: Partial<SubagentSpan> = {}): SubagentSpan {
  return {
    spanId: 's1',
    parentCallId: 'c1',
    taskLabel: 'digging into logs',
    steps: [],
    status: 'running',
    ...overrides,
  } as SubagentSpan
}

/** A terminal (non-running) span — status defaults to 'error' since most Fix-1 tests need a failure. */
function finishedSpan(overrides: Partial<SubagentSpan> = {}): SubagentSpan {
  return {
    spanId: 's1',
    parentCallId: 'c1',
    taskLabel: 'digging into logs',
    steps: [],
    status: 'error',
    durationMs: 1200,
    ...overrides,
  } as SubagentSpan
}

beforeEach(() => {
  act(() => {
    useChatStore.setState({ messages: [], toolCalls: {} })
  })
})

afterEach(() => {
  vi.clearAllMocks()
})

describe('ActivityBar — idle (0 running)', () => {
  // Regression test for a /visual-qa finding: the bar previously always
  // rendered, including an always-visible "No active background work" idle
  // state stretched full-width above the composer — noise, not signal, and
  // inconsistent with RateLimitIndicator's own precedent of conditional
  // mounting. The bar must now render nothing at all when idle.
  it('renders nothing when there is no running activity', () => {
    renderBar()
    expect(screen.queryByTestId('activity-bar')).not.toBeInTheDocument()
    expect(screen.queryByText('No active background work')).not.toBeInTheDocument()
  })
})

describe('ActivityBar — 1 running', () => {
  it('renders "1 running" for a single running agent span', async () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([runningSpan({ agentId: 'ray' })])],
      })
    })
    renderBar()
    await waitFor(() => {
      expect(screen.getByText('1 running')).toBeInTheDocument()
    })
  })

  // Fix 3 (2026-07-16): the pill was missing any in-progress signal beyond
  // the static avatar stack — the spinning ArrowsClockwise (same running-icon
  // vocabulary as toolStatusConfig's getToolBadgeStatusConfig/getSpanStatusDot
  // 'running' case) now sits before the "N running" text, decorative
  // (aria-hidden) since the text already carries the label for assistive tech.
  it('shows a spinning indicator (.animate-spin) in the pill while something is running', async () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([runningSpan({ agentId: 'ray' })])],
      })
    })
    renderBar()
    await waitFor(() => {
      expect(screen.getByText('1 running')).toBeInTheDocument()
    })
    const bar = screen.getByTestId('activity-bar')
    const spinner = bar.querySelector('.animate-spin')
    expect(spinner).not.toBeNull()
    expect(spinner).toHaveAttribute('aria-hidden', 'true')
  })
})

describe('ActivityBar — N running', () => {
  it('renders "2 running" for one agent span plus one background bash call', async () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([runningSpan({ agentId: 'ray' })])],
        toolCalls: {
          call_1: {
            id: 'call_1',
            call_id: 'call_1',
            tool: 'bash',
            params: { command: 'npm test', run_in_background: true },
            status: 'success',
            result: '{"sessionId":"call_1","status":"running"}',
          },
        },
      })
    })
    renderBar()
    await waitFor(() => {
      expect(screen.getByText('2 running')).toBeInTheDocument()
    })
  })
})

describe('ActivityBar — avatar stack cap', () => {
  it('caps the rendered avatar stack at MAX_STACK_AVATARS (4) while the count label shows the true total of 5', async () => {
    act(() => {
      useChatStore.setState({
        toolCalls: {
          call_1: {
            id: 'call_1',
            call_id: 'call_1',
            tool: 'bash',
            params: { command: 'npm run build', run_in_background: true },
            status: 'success',
            result: '{"sessionId":"call_1","status":"running"}',
          },
          call_2: {
            id: 'call_2',
            call_id: 'call_2',
            tool: 'bash',
            params: { command: 'npm run lint', run_in_background: true },
            status: 'success',
            result: '{"sessionId":"call_2","status":"running"}',
          },
          call_3: {
            id: 'call_3',
            call_id: 'call_3',
            tool: 'bash',
            params: { command: 'npm run test', run_in_background: true },
            status: 'success',
            result: '{"sessionId":"call_3","status":"running"}',
          },
          call_4: {
            id: 'call_4',
            call_id: 'call_4',
            tool: 'bash',
            params: { command: 'npm run typecheck', run_in_background: true },
            status: 'success',
            result: '{"sessionId":"call_4","status":"running"}',
          },
          call_5: {
            id: 'call_5',
            call_id: 'call_5',
            tool: 'bash',
            params: { command: 'npm run e2e', run_in_background: true },
            status: 'success',
            result: '{"sessionId":"call_5","status":"running"}',
          },
        },
      })
    })
    renderBar()

    // Count label must reflect the TRUE total (5), not the capped stack size.
    await waitFor(() => {
      expect(screen.getByText('5 running')).toBeInTheDocument()
    })
    expect(screen.getByTestId('activity-bar')).toHaveAttribute('aria-label', 'Activity — 5 running')

    // Stack itself is capped at MAX_STACK_AVATARS (4) — each stacked avatar is
    // wrapped in a `ring-2` div in ActivityBar.tsx's stackItems.map render.
    const bar = screen.getByTestId('activity-bar')
    const stackedAvatarWrappers = bar.querySelectorAll('.ring-2')
    expect(stackedAvatarWrappers.length).toBe(4)
  })
})

describe('ActivityBar — opens the panel on click', () => {
  it('reveals "Running now" in the slide-out after clicking the bar', async () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([runningSpan({ agentId: 'ray' })])],
      })
    })
    renderBar()
    await waitFor(() => {
      expect(screen.getByText('1 running')).toBeInTheDocument()
    })

    expect(screen.queryByText('Running now')).not.toBeInTheDocument()
    fireEvent.click(screen.getByTestId('activity-bar'))

    await waitFor(() => {
      expect(screen.getByText('Running now')).toBeInTheDocument()
    })
    expect(screen.getByText('digging into logs')).toBeInTheDocument()
  })

  // Deliberate, accepted tradeoff (see ActivityBar.tsx's header comment): a
  // FRESH mount with no running/finished activity at all has nothing to
  // click, by design — this is distinct from Fix 1's later matrix (a panel
  // that WAS opened, or a failure that WAS retained, keeps the pill
  // mounted); this test only covers the true "nothing has ever happened"
  // starting state.
  it('has no clickable entry point at rest — nothing has ever run or finished', () => {
    renderBar()
    expect(screen.queryByTestId('activity-bar')).not.toBeInTheDocument()
    expect(screen.queryByText('Running now')).not.toBeInTheDocument()
  })
})

// ── Fix 1 (2026-07-16): revised mount matrix ────────────────────────────────
// The bar previously unmounted unconditionally the instant runningCount hit
// 0 (`if (runningCount === 0) return null`), which killed an OPEN panel
// mid-inspection and made a retained failure unreachable at idle — the
// panel is now the designated failure-transparency surface for delegation/
// background-bash outcomes hidden from the thread by default
// (toolVisibility.ts). This block pins the corrected matrix.

describe('ActivityBar — Fix 1: an open panel survives running -> 0', () => {
  it('keeps the panel mounted (and open) after the last running item finishes while the panel is open', async () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([runningSpan({ agentId: 'ray' })])],
      })
    })
    renderBar()
    await waitFor(() => {
      expect(screen.getByText('1 running')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByTestId('activity-bar'))
    await waitFor(() => {
      expect(screen.getByText('Running now')).toBeInTheDocument()
    })

    // The running item finishes (successfully) — runningCount drops to 0.
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage([
            finishedSpan({ agentId: 'ray', status: 'success' }),
          ]),
        ],
      })
    })

    // The panel (and its "Recently finished" section) must still be visible
    // — the whole component must NOT have unmounted out from under it.
    await waitFor(() => {
      expect(screen.getByText('Recently finished')).toBeInTheDocument()
    })
    expect(screen.getByTestId('activity-bar')).toBeInTheDocument()
  })
})

describe('ActivityBar — Fix 1: a retained failure keeps the pill mounted at idle', () => {
  it('shows a red "1 failed" pill (no spinner) when idle with an error item in recentlyFinished', async () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([finishedSpan({ agentId: 'ray', status: 'error' })])],
      })
    })
    renderBar()

    const bar = await screen.findByTestId('activity-bar')
    expect(screen.getByText('1 failed')).toBeInTheDocument()
    // No spinner while idle — spinner is reserved for runningCount > 0.
    expect(bar.querySelector('.animate-spin')).toBeNull()
    // Failed-state variant uses the error-toned dot, not the muted/idle one.
    const dot = bar.querySelector('[class*="rounded-full"]')
    expect(dot?.getAttribute('class')).toContain('bg-[var(--color-error)]')
  })

  it('also stays mounted for an interrupted or timeout item (not just error)', async () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([finishedSpan({ agentId: 'ray', status: 'interrupted' })])],
      })
    })
    renderBar()
    expect(await screen.findByText('1 failed')).toBeInTheDocument()
  })

  it('does NOT count a cancelled item as a failure (cancelled is a deliberate stop, not a failure)', () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([finishedSpan({ agentId: 'ray', status: 'cancelled' })])],
      })
    })
    renderBar()
    expect(screen.queryByTestId('activity-bar')).not.toBeInTheDocument()
  })

  it('opening the panel from the failed-state pill still reveals the failed row', async () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([finishedSpan({ agentId: 'ray', status: 'error', taskLabel: 'broken task' })])],
      })
    })
    renderBar()
    const bar = await screen.findByTestId('activity-bar')
    fireEvent.click(bar)
    await waitFor(() => {
      expect(screen.getByText('broken task')).toBeInTheDocument()
    })
  })
})

describe('ActivityBar — Fix 1: a purely-successful idle history still unmounts', () => {
  it('renders nothing when recentlyFinished contains only success items and nothing is running', () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([finishedSpan({ agentId: 'ray', status: 'success' })])],
      })
    })
    renderBar()
    expect(screen.queryByTestId('activity-bar')).not.toBeInTheDocument()
  })
})

describe('ActivityBar — Fix 1: the spinner only ever shows while running', () => {
  it('does not render .animate-spin when idle-but-mounted via a retained failure', async () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([finishedSpan({ agentId: 'ray', status: 'timeout' })])],
      })
    })
    renderBar()
    const bar = await screen.findByTestId('activity-bar')
    expect(bar.querySelector('.animate-spin')).toBeNull()
  })

  it('renders .animate-spin once something is running again, even with a failure also retained', async () => {
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage([
            finishedSpan({ spanId: 's_failed', parentCallId: 'c_failed', agentId: 'ray', status: 'error' }),
            runningSpan({ spanId: 's_running', parentCallId: 'c_running', agentId: 'ray' }),
          ]),
        ],
      })
    })
    renderBar()
    const bar = await screen.findByTestId('activity-bar')
    expect(bar.querySelector('.animate-spin')).not.toBeNull()
    expect(screen.getByText('1 running')).toBeInTheDocument()
  })
})
