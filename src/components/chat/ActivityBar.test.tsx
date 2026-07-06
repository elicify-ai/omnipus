// ActivityBar — persistent activity strip tests.
//
// Covers: idle (0 running), 1 running, N running (span + bash), the count
// text, and clicking the bar opens the ActivityPanel slide-out.

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

  // Deliberate, accepted tradeoff (see ActivityBar.tsx's header comment): the
  // bar is a pure "something is happening right now" glance, not a
  // permanent history browser — at rest there's nothing to click, by design.
  it('has no clickable entry point at rest — the panel is only reachable while something is running', () => {
    renderBar()
    expect(screen.queryByTestId('activity-bar')).not.toBeInTheDocument()
    expect(screen.queryByText('Running now')).not.toBeInTheDocument()
  })
})
