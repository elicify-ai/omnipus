// FeedKindLabel roster-unavailable tests (combined-review R2-S4-03).
//
// When the shared ['agents'] roster FAILS, the above-feed line falls back to
// the agent id ("jim - Main Session") — the ruling's accepted stand-in — but
// until now nothing marked that fallback, so a rendered id read as an actual
// agent name. The strip now carries the same honest signal the search modal
// shows for the identical trigger ("agent names unavailable"), and ONLY in
// the real fallback case:
//   - a merely-loading roster keeps the line absent (existing behaviour);
//   - a failed REFETCH whose retained data still names the agent keeps the
//     plain name — nothing fell back, so there is nothing to disclaim.
//
// Harness copied from FeedKindLabel.main-title.test.tsx.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { fetchAgents } from '@/lib/api'
import type { Agent } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn(),
  }
})

import { FeedKindLabel } from './FeedKindLabel'

const WORKSPACE_ID = 'ws_roster_unavailable_test'

function agentFixture(overrides: Partial<Agent>): Agent {
  return {
    id: 'jim',
    name: 'Jim',
    type: 'core',
    locked: true,
    figure: {} as Agent['figure'],
    role: {} as Agent['role'],
    revision: {} as Agent['revision'],
    ...overrides,
  } as Agent
}

function setDescriptor(): void {
  useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE_ID })
  useSessionStore.setState({
    activeSessionId: 'sid-main',
    attachedSessionType: null,
    attachedTaskTitle: null,
    sessionByWorkspace: {
      [WORKSPACE_ID]: {
        id: 'sid-main',
        type: 'main',
        title: 'Jim - Main Session',
        agentId: 'jim',
      },
    },
  })
}

function renderLabel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = render(
    <QueryClientProvider client={qc}>
      <FeedKindLabel />
    </QueryClientProvider>,
  )
  return { qc, view }
}

function label(): HTMLElement | null {
  return document.querySelector('[data-testid="feed-kind-label"]')
}

beforeEach(() => {
  vi.mocked(fetchAgents).mockReset()
  setDescriptor()
})

describe('FeedKindLabel — an id-fallback line is marked as such (R2-S4-03)', () => {
  it('when the roster has FAILED and the line shows the agent id, the strip says names are unavailable', async () => {
    vi.mocked(fetchAgents).mockRejectedValue(new Error('roster down'))
    renderLabel()
    // R3-QA-03: strict equality — the marker is the line's SUFFIX, not a
    // fragment that may appear anywhere in it.
    await waitFor(() => {
      expect(label()?.textContent).toBe('jim - Main Session — agent names unavailable')
    })
  })

  it('while the roster is merely LOADING the line is absent — no signal, existing behaviour', () => {
    vi.mocked(fetchAgents).mockReturnValue(new Promise(() => {})) // never settles
    renderLabel()
    expect(label()).toBeNull()
  })

  it('a failed REFETCH with retained data that still names the agent keeps the plain name', async () => {
    vi.mocked(fetchAgents)
      .mockResolvedValueOnce([agentFixture({})])
      .mockRejectedValueOnce(new Error('roster down'))
    const { qc } = renderLabel()
    await waitFor(() => {
      expect(label()?.textContent).toBe('Jim - Main Session')
    })
    // Refetch fails; React Query retains the last successful roster.
    await qc.refetchQueries({ queryKey: ['agents'] })
    await waitFor(() => {
      expect(qc.getQueryState(['agents'])?.status).toBe('error')
    })
    expect(label()?.textContent).toBe('Jim - Main Session')
    expect(label()?.textContent).not.toContain('unavailable')
  })

  it('when the roster loaded fine the line stays plain', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([agentFixture({})])
    renderLabel()
    await waitFor(() => {
      expect(label()?.textContent).toBe('Jim - Main Session')
    })
    expect(label()?.textContent).not.toContain('unavailable')
  })
})

// The warning's EXPLANATION has to reach a sighted keyboard user (review
// round 2, C6/R2-1). The compact glyph stays compact — the explanation is
// the catalogued Tooltip's job (design-system rule 14): a NAMED, focusable
// trigger that reveals on focus and on a tap, and that Escape dismisses.
//
// The words a screen reader gets (the status region's own accessible name)
// are unchanged by any of this — the trigger is a SIBLING of the labelled
// warning text, never a child of it, so the region's name cannot repeat
// itself.
describe('FeedKindLabel — the compact warning explains itself (review round 2)', () => {
  const EXPLANATION = 'Agent names could not be loaded. Reload the page to try again.'

  async function renderFailedRoster(): Promise<HTMLElement> {
    vi.mocked(fetchAgents).mockRejectedValue(new Error('roster down'))
    renderLabel()
    await waitFor(() => {
      expect(label()?.textContent).toBe('jim - Main Session — agent names unavailable')
    })
    return screen.getByTestId('feed-kind-warning')
  }

  it('keyboard focus reveals the explanation on a named, focusable trigger', async () => {
    const trigger = await renderFailedRoster()
    // Named and focusable: the keyboard can reach the explanation at all.
    expect(trigger).toHaveAttribute('aria-label', 'Agent names unavailable')
    expect(trigger.tabIndex).toBe(0)
    // Nothing is revealed before the user asks for it.
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()

    fireEvent.focus(trigger)
    expect(screen.getByRole('tooltip')).toHaveTextContent(EXPLANATION)
  })

  it('a tap reveals the explanation', async () => {
    const trigger = await renderFailedRoster()
    fireEvent.click(trigger)
    expect(screen.getByRole('tooltip')).toHaveTextContent(EXPLANATION)
  })

  it('Escape dismisses the explanation again', async () => {
    const trigger = await renderFailedRoster()
    trigger.focus()
    fireEvent.focus(trigger)
    expect(screen.getByRole('tooltip')).toBeInTheDocument()

    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
    // The operator's keyboard position does not change — only the bubble goes.
    expect(document.activeElement).toBe(trigger)
  })

  it('the status region still names the identity AND the warning, once', async () => {
    await renderFailedRoster()
    const region = label()
    const [identityId, warningId] = (region?.getAttribute('aria-labelledby') ?? '').split(' ')
    const identity = document.getElementById(identityId ?? '')
    const warning = document.getElementById(warningId ?? '')
    expect(identity).not.toBeNull()
    expect(warning).not.toBeNull()
    // The trigger is NOT inside the region's name source: a named child there
    // would repeat itself in the accessible name.
    expect(warning?.contains(screen.getByTestId('feed-kind-warning'))).toBe(false)
    // The region's name is the identity plus the warning — the words the
    // screen reader reads, unchanged by the tooltip.
    expect(identity?.textContent).toBe('jim - Main Session')
    expect(warning?.textContent).toContain('agent names unavailable')
  })
})
