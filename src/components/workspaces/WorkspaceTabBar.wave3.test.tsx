// WorkspaceTabBar.wave3.test.tsx — RED pack for side-panel-shell-spec.md
// Wave 3 (SP-40, amending FR-007/US-5; tasks/calendar/team panel registration
// per §10 Wave 3, SP-6):
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
// move from MAJ-012 mixed-mode links to the Library-style toggle model
// (US-5 AS-1/AS-2: click opens the panel over chat with no navigation and
// the URL gains ?panel=<id> via the shared deep-link sync; second click
// closes).
//
// Oracle sources (never the implementation):
//   - docs/internal/specs/side-panel-shell-spec.md §10 Wave 3 table
//   - §13 decision table SP-40 (+SP-6), FR-007 as amended
//   - amendment header items (SP-38/SP-40 corrections)
//   - US-5 AS-1..AS-5 (toggle semantics carry to the compact dropdown)
//   - coordination/wireframes/side-panel-wave3.html §6
//
// RED evidence (2026-10-04, read src/components/workspaces/WorkspaceTabBar.tsx
// in full): WORKSPACE_TABS still carries the `chat` entry; PANEL_TOGGLE_SEGMENTS
// still maps only `media → library`; the compact trigger still renders the
// active segment's text label (`SEGMENT_LABELS[activeSegment]`) plus a caret.
// Every assertion below that expects Chat absent, tasks/calendar/team toggles,
// or an icon-only trigger fails against this code.
//
// This is a NEW file on purpose: the existing wave-1 packs
// (WorkspaceTabBar.toggle.test.tsx / .compactParity.test.tsx) pin TODAY's
// mixed-mode model and stay authoritative for it until GREEN reworks them
// (spec §12: wave-3 strip changes update those specs in that wave, never
// silently).

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
    // Queried by the entry's label, not a test id: today only REGISTERED
    // toggle entries carry `workspace-view-switcher-<segment>` test ids, so a
    // test-id query would pass vacuously while Chat still renders in the menu.
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const menu = screen.getByTestId('view-switcher-menu')
    expect(within(menu).queryByText('Chat')).not.toBeInTheDocument()
  })

  it('the strip entries are exactly Tasks, Calendar, Library, Team — in that order', () => {
    // Oracle: wireframe §6 — "Tasks / Calendar / Library / Team are the only
    // entries", order per WORKSPACE_TABS minus the removed chat row.
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const strip = screen.getByTestId('workspace-tab-strip')
    const segments = within(strip)
      .queryAllByTestId(/^workspace-tab-/)
      .map((el) => el.getAttribute('data-testid'))
    expect(segments).toEqual([
      'workspace-tab-board',
      'workspace-tab-calendar',
      'workspace-tab-media',
      'workspace-tab-team',
    ])
  })

  it('the strip labels read Tasks, Calendar, Library, Team', () => {
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const strip = screen.getByTestId('workspace-tab-strip')
    expect(within(strip).getByRole('button', { name: 'Tasks' })).toBeInTheDocument()
    expect(within(strip).getByRole('button', { name: 'Calendar' })).toBeInTheDocument()
    expect(within(strip).getByRole('button', { name: 'Library' })).toBeInTheDocument()
    expect(within(strip).getByRole('button', { name: 'Team' })).toBeInTheDocument()
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
