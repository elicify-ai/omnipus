// WorkspaceTabBar.wave3.test.tsx — wave-3 join pack for
// side-panel-shell-spec.md Wave 3 (SP-40, amending FR-007/US-5; tasks/calendar/
// team panel registration per §10 Wave 3, SP-6):
//
//   "Chat is NEVER rendered as a tab-strip or compact-dropdown entry (it is
//   the base route, not a togglable panel); the compact dropdown's trigger is
//   a menu ICON, not a text label." (FR-007 as amended, SP-40)
//
//   "...with no Chat entry left, the compact trigger no longer needs a text
//   label at all: it's icon-only now... Tasks / Calendar / Library / Team are
//   the only entries, all toggle buttons (aria-pressed)." (wireframe §6)
//
// Wave 3 registers Tasks ('board' segment → 'tasks' panel), Calendar
// ('calendar') and Team ('team') as workspace panels, so their strip entries
// are the Library-style toggle model (US-5 AS-1/AS-2: click opens the panel
// over chat with no navigation and the URL gains ?panel=<id> via the shared
// deep-link sync; second click closes).
//
// JOIN STATUS (2026-10-05, work/side-panel-wave3-join-20261005): the five
// production slices are merged. This pack was the 8773803cf RED pack written
// to fail on base fe0b68fb0; its assertions are UNCHANGED — they already
// express the SP-40 end state and the joined WorkspaceTabBar delivers it
// (WORKSPACE_TABS holds board/calendar/media/team; PANEL_TOGGLE_SEGMENTS maps
// every segment; the compact trigger is the icon-only "Open panels menu"
// button). Only this header and the file's RED commentary were updated.
//
// Oracle sources (never the implementation):
//   - docs/internal/specs/side-panel-shell-spec.md §10 Wave 3 table
//   - §13 decision table SP-40 (+SP-6), FR-007 as amended
//   - amendment header items (SP-38/SP-40 corrections)
//   - US-5 AS-1..AS-5 (toggle semantics carry to the compact dropdown)
//   - coordination/wireframes/side-panel-wave3.html §6
//
// NOTE (wave-3 join, confirmed by the squad lead): the 8773803cf pack's
// "exactly Tasks, Calendar, Library, Team" inventory was written against a
// pre-Mail base — the approved spec supersedes it, not the implementation.
// FR-007 as amended removes ONLY Chat; Mail (registered in wave 2) keeps its
// toggle entry. This pack therefore pins the EXACT five-entry inventory on
// both the full strip and the compact dropdown — RED against the joined bar
// (which dropped the mail entry: production gap, frontend-lead restores it),
// and green exactly when the restore lands. The mail entry's TOGGLE
// semantics stay pinned by the wave-2 pack (WorkspaceTabBar.toggle.test.tsx).

import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { act } from 'react'
import { useUiStore } from '@/store/ui'

let mockPathname = '/workspaces/ws-1/chat'
const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: mockPathname }),
  useNavigate: () => mockNavigate,
  Link: ({
    children,
    to,
    params,
    'data-testid': testId,
    'aria-label': ariaLabel,
  }: {
    children: React.ReactNode
    to: string
    params?: Record<string, string>
    'data-testid'?: string
    'aria-label'?: string
  }) => {
    const href = params ? to.replace('$workspaceId', params.workspaceId) : to
    return (
      <a href={href} data-testid={testId} aria-label={ariaLabel}>
        {children}
      </a>
    )
  },
}))

vi.mock('framer-motion', () => ({
  motion: {
    div: ({ children, ...rest }: React.HTMLAttributes<HTMLDivElement>) => <div {...rest}>{children}</div>,
  },
}))

vi.mock('@/components/ui/dropdown-menu', async () => {
  const { Button } = await vi.importActual<typeof import('@/components/ui/button')>('@/components/ui/button')
  return {
    DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    DropdownMenuTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) =>
      asChild ? <>{children}</> : <div>{children}</div>,
    DropdownMenuContent: ({ children }: { children: React.ReactNode }) => (
      <div data-testid="view-switcher-menu">{children}</div>
    ),
    DropdownMenuItem: ({ children, onClick, className, ...rest }: {
      children: React.ReactNode
      onClick?: () => void
      className?: string
    } & Record<string, unknown>) => {
      void className
      return <Button variant="ghost" onClick={onClick} {...rest}>{children}</Button>
    },
  }
})

import { WorkspaceTabBar } from './WorkspaceTabBar'

function activePanel(): { id: string; context?: { workspaceId?: string } } | null {
  const state = useUiStore.getState() as unknown as {
    activePanel?: { id: string; context?: { workspaceId?: string } } | null
  }
  return state.activePanel ?? null
}

beforeEach(() => {
  mockNavigate.mockClear()
  mockPathname = '/workspaces/ws-1/chat'
  act(() => {
    useUiStore.setState({
      libraryPanel: null,
      browserPanel: null,
      activePanel: null,
    } as never)
  })
})

describe('WorkspaceTabBar wave 3 — Chat entry removed (SP-40, FR-007 amended)', () => {
  it('the full strip has NO Chat entry', () => {
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    expect(screen.queryByTestId('workspace-tab-chat')).not.toBeInTheDocument()
  })

  it('the compact dropdown has NO Chat entry', () => {
    // Queried by the entry's label, not a test id: only REGISTERED toggle
    // entries carry `workspace-view-switcher-<segment>` test ids, so a
    // test-id query would pass vacuously while Chat still renders in the menu.
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const menu = screen.getByTestId('view-switcher-menu')
    expect(within(menu).queryByText('Chat')).not.toBeInTheDocument()
  })

  it('the strip entries are exactly Tasks, Calendar, Library, Mail, Team — in that order', () => {
    // Oracle: FR-007 as amended — Chat is the ONLY entry ever removed
    // (SP-40); every REGISTERED panel keeps its toggle entry, and Mail has
    // been one since wave 2. The 8773803cf pack's four-entry inventory was
    // written against a pre-Mail base and is superseded on this point by the
    // approved spec, not by the implementation: the exact inventory (deep
    // equality, not a containment check) must include Mail. RED today — the
    // joined bar dropped the mail entry; production gap, frontend-lead owns
    // the restore.
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const strip = screen.getByTestId('workspace-tab-strip')
    const segments = within(strip)
      .queryAllByTestId(/^workspace-tab-/)
      .map((el) => el.getAttribute('data-testid'))
    expect(segments).toEqual([
      'workspace-tab-board',
      'workspace-tab-calendar',
      'workspace-tab-media',
      'workspace-tab-mail',
      'workspace-tab-team',
    ])
  })

  it('the strip labels read Tasks, Calendar, Library, Mail, Team', () => {
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const strip = screen.getByTestId('workspace-tab-strip')
    expect(within(strip).getByRole('button', { name: 'Tasks' })).toBeInTheDocument()
    expect(within(strip).getByRole('button', { name: 'Calendar' })).toBeInTheDocument()
    expect(within(strip).getByRole('button', { name: 'Library' })).toBeInTheDocument()
    expect(within(strip).getByRole('button', { name: 'Mail' })).toBeInTheDocument()
    expect(within(strip).getByRole('button', { name: 'Team' })).toBeInTheDocument()
  })

  it('the compact dropdown carries the exact same inventory: Settings plus the five panel toggles', () => {
    // US-5 AS-5 parity: the dropdown mirrors the strip (MAJ-007's ARIA model
    // carries to compact). Exact deep-equal inventory — settings (narrow
    // viewports' only settings entry here) then the five toggles in strip
    // order, nothing else.
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const menu = screen.getByTestId('view-switcher-menu')
    const entries = within(menu)
      .queryAllByTestId(/^workspace-view-switcher-/)
      .map((el) => el.getAttribute('data-testid'))
    expect(entries).toEqual([
      'workspace-view-switcher-settings',
      'workspace-view-switcher-board',
      'workspace-view-switcher-calendar',
      'workspace-view-switcher-media',
      'workspace-view-switcher-mail',
      'workspace-view-switcher-team',
    ])
  })
})

describe('WorkspaceTabBar wave 3 — compact trigger is icon-only (SP-40)', () => {
  it('the compact trigger renders NO text label — icon only', () => {
    // Oracle: FR-007 amended — "the compact dropdown's trigger is a menu
    // ICON, not a text label"; wireframe §6 — "a plain menu icon, no
    // 'Panels ▾' text". An icon-only button has no text content at all.
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const trigger = screen.getByTestId('workspace-view-switcher')
    expect(trigger.textContent?.trim()).toBe('')
  })

  it('the icon-only trigger still exposes an accessible name', () => {
    // GUARD (green by construction today — the current trigger already has an
    // aria-label): it pins the a11y invariant across the SP-40 change, so
    // dropping the text label must not orphan the control. Not a RED test.
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const trigger = screen.getByTestId('workspace-view-switcher')
    const name = trigger.getAttribute('aria-label') ?? ''
    expect(name.length).toBeGreaterThan(0)
  })
})

describe.each([
  { segment: 'board', panelId: 'tasks', label: 'Tasks' },
  { segment: 'calendar', panelId: 'calendar', label: 'Calendar' },
  { segment: 'team', panelId: 'team', label: 'Team' },
] as const)('WorkspaceTabBar wave 3 — $label registers as a panel toggle (SP-6)', ({ segment, panelId, label }) => {
  it(`the ${label} entry is a toggle button with aria-pressed=false when closed (not a link)`, () => {
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const entry = screen.getByTestId(`workspace-tab-${segment}`)
    expect(entry.tagName).toBe('BUTTON')
    expect(entry.getAttribute('aria-pressed')).toBe('false')
    expect(entry.getAttribute('data-panel-trigger')).toBe(panelId)
  })

  it(`clicking ${label} opens the ${panelId} panel scoped to this workspace WITHOUT navigating (US-5 AS-1)`, () => {
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    fireEvent.click(screen.getByTestId(`workspace-tab-${segment}`))
    expect(activePanel()).toEqual({ id: panelId, context: { workspaceId: 'ws-1' } })
    expect(mockNavigate).not.toHaveBeenCalled()
  })

  it(`a second click on ${label} closes the panel and clears aria-pressed (US-5 AS-2)`, () => {
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const entry = screen.getByTestId(`workspace-tab-${segment}`)
    fireEvent.click(entry)
    // Intermediate assertion — without it a no-op click would leave
    // activePanel null before AND after, passing vacuously (same guard the
    // wave-1 toggle pack uses).
    expect(activePanel()).toEqual({ id: panelId, context: { workspaceId: 'ws-1' } })
    expect(entry.getAttribute('aria-pressed')).toBe('true')

    fireEvent.click(entry)
    expect(activePanel()).toBeNull()
    expect(entry.getAttribute('aria-pressed')).toBe('false')
  })

  it(`the compact dropdown's ${label} entry carries the same toggle semantics (US-5 AS-5)`, () => {
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const item = screen.getByTestId(`workspace-view-switcher-${segment}`)
    expect(item.getAttribute('data-panel-trigger')).toBe(panelId)
    expect(item.getAttribute('aria-pressed')).toBe('false')
    fireEvent.click(item)
    expect(activePanel()).toEqual({ id: panelId, context: { workspaceId: 'ws-1' } })
    expect(item.getAttribute('aria-pressed')).toBe('true')
  })
})

describe('WorkspaceTabBar wave 3 — panel switching (US-5 AS-3)', () => {
  it('opening Tasks while Library is open replaces it with Tasks', () => {
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    fireEvent.click(screen.getByTestId('workspace-tab-media'))
    expect(activePanel()).toEqual({ id: 'library', context: { workspaceId: 'ws-1' } })

    fireEvent.click(screen.getByTestId('workspace-tab-board'))
    expect(activePanel()).toEqual({ id: 'tasks', context: { workspaceId: 'ws-1' } })
  })
})
