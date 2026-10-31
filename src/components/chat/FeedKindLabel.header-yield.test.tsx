/**
 * The chat label's title is the part of the header line the chat gives back to
 * the workspace strip (#1449 D13, founder 2026-10-28: "the workspace strip
 * ALWAYS keeps the header line; the chat slot shrinks and truncates first";
 * R44: the strip steps down only when the full row no longer fits).
 *
 * Two facts make that true in the browser and are all jsdom can see (it has
 * no layout engine; the geometry itself is measured in Chromium by
 * tests/e2e/workspace-header-standalone.spec.ts, case F1):
 *
 *   1. the identity span is declared `data-header-yield`, so the strip's
 *      measurement counts its width as free room — and ONLY the identity:
 *      the failed-roster warning is a control that is never given away;
 *   2. the label root is a grid whose identity track is `minmax(0, 1fr)`, so
 *      the label's minimum width is the warning (or nothing), not the whole
 *      text. A flex root with a nowrap text kept the full text width as the
 *      slot's automatic minimum, so the slot never shrank under pressure
 *      (measured in Chromium before this change: slot 146 px, no yield).
 *
 * Tests 2 and 4 pin that class contract; they are characterization of the
 * CSS the browser case depends on, not a substitute for it.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { fetchAgents } from '@/lib/api'
import type { Agent } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAgents: vi.fn() }
})

import { FeedKindLabel, TaskKindLabel } from './FeedKindLabel'

const WORKSPACE_ID = 'ws_header_yield_test'

function agentFixture(): Agent {
  return {
    id: 'jim', name: 'Jim', type: 'core', locked: true,
    figure: {} as Agent['figure'], role: {} as Agent['role'], revision: {} as Agent['revision'],
  } as Agent
}

function seedMain(): void {
  useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE_ID })
  useSessionStore.setState({
    activeSessionId: 'sid-main',
    attachedSessionType: null,
    attachedTaskTitle: null,
    sessionByWorkspace: { [WORKSPACE_ID]: { id: 'sid-main', type: 'main', title: 'Jim - Main Session', agentId: 'jim' } },
  })
}

function renderLabel(element: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{element}</QueryClientProvider>)
}

const tokens = (element: Element) => (element.getAttribute('class') ?? '').split(/\s+/).filter(Boolean)

beforeEach(() => {
  vi.mocked(fetchAgents).mockReset()
  seedMain()
})
afterEach(() => cleanup())

describe('FeedKindLabel — the identity is room the chat gives back to the strip', () => {
  it('declares the identity span as the width the header line may take back', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([agentFixture()])
    renderLabel(<FeedKindLabel />)
    const root = await screen.findByTestId('feed-kind-label')
    const identity = document.getElementById((root.getAttribute('aria-labelledby') ?? '').split(' ')[0] ?? '')
    expect(identity).not.toBeNull()
    expect(identity).toHaveTextContent('Jim - Main Session')
    expect(identity).toHaveAttribute('data-header-yield')
    expect(identity?.parentElement).toBe(root)
    // The yield is the identity ALONE: the label root itself is not declared.
    expect(root).not.toHaveAttribute('data-header-yield')
  })

  it('lays the label out as a grid whose identity track can reach zero width', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([agentFixture()])
    renderLabel(<FeedKindLabel />)
    const root = await screen.findByTestId('feed-kind-label')
    expect(tokens(root)).toEqual(expect.arrayContaining(['grid', 'min-w-0', 'grid-flow-col', 'grid-cols-[minmax(0,1fr)]']))
    // A flex root would give the slot its text width as an automatic minimum.
    expect(tokens(root)).not.toContain('flex')
  })

  it('never gives the failed-roster warning away: the control is outside the declared yield', async () => {
    vi.mocked(fetchAgents).mockRejectedValue(new Error('roster down'))
    renderLabel(<FeedKindLabel />)
    const warning = await screen.findByTestId('feed-kind-warning')
    const root = screen.getByTestId('feed-kind-label')
    expect(warning.closest('[data-header-yield]')).toBeNull()
    // The identity is still the one declared yield, and the warning's wrapper
    // is the label's second grid item (an auto-sized track).
    expect(root.querySelectorAll('[data-header-yield]')).toHaveLength(1)
    await waitFor(() => expect(root.textContent).toContain('agent names unavailable'))
  })
})

describe('TaskKindLabel — the task identity gives width back the same way', () => {
  it('declares its identity span as the yield and keeps the icon as the fixed first track', () => {
    useSessionStore.setState({ attachedSessionType: 'task', attachedTaskTitle: 'Deploy the thing' })
    const { container } = renderLabel(<TaskKindLabel />)
    const identity = container.querySelector('span[title]')
    expect(identity).toHaveTextContent('Task: Deploy the thing')
    expect(identity).toHaveAttribute('data-header-yield')
    const root = identity?.parentElement as HTMLElement
    expect(tokens(root)).toEqual(expect.arrayContaining(['grid', 'min-w-0', 'grid-cols-[auto_minmax(0,1fr)]']))
    expect(tokens(root)).not.toContain('flex')
    // The glyph precedes the identity in the first (auto) track.
    expect(root.firstElementChild?.tagName.toLowerCase()).toBe('svg')
  })

  it('is what FeedKindLabel renders for a task transcript with no matching descriptor', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: null })
    useSessionStore.setState({ activeSessionId: null, sessionByWorkspace: {}, attachedSessionType: 'task', attachedTaskTitle: 'Nightly report' })
    const { container } = renderLabel(<FeedKindLabel />)
    expect(container.querySelector('span[data-header-yield]')).toHaveTextContent('Task: Nightly report')
  })
})
