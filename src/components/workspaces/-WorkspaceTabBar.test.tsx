import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'

// Mock TanStack Router primitives the tab bar uses (Link, useLocation, useNavigate).
// mockNavigate is shared (not a fresh vi.fn() per call) so tests can assert on it —
// the "mock" prefix is required for Vitest's vi.mock hoisting to see it.
let mockPathname = '/workspaces/ws-1/chat'
const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: mockPathname }),
  useNavigate: () => mockNavigate,
  Link: ({
    children,
    to,
    params,
    'aria-current': ariaCurrent,
    'data-testid': testId,
    'aria-label': ariaLabel,
  }: {
    children: React.ReactNode
    to: string
    params?: Record<string, string>
    'aria-current'?: React.AriaAttributes['aria-current']
    'data-testid'?: string
    'aria-label'?: string
  }) => {
    const href = params ? to.replace('$workspaceId', params.workspaceId) : to
    return (
      <a href={href} aria-current={ariaCurrent} data-testid={testId} aria-label={ariaLabel}>
        {children}
      </a>
    )
  },
}))

// Framer Motion — render motion.div as a plain div (no animation in tests).
vi.mock('framer-motion', () => ({
  motion: {
    div: ({ children, ...rest }: React.HTMLAttributes<HTMLDivElement>) => (
      <div {...rest}>{children}</div>
    ),
  },
}))

// DropdownMenu — minimal stub so the view-switcher trigger renders without Radix internals.
vi.mock('@/components/ui/dropdown-menu', () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) =>
    asChild ? <>{children}</> : <div>{children}</div>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="view-switcher-menu">{children}</div>
  ),
  DropdownMenuItem: ({
    children,
    onClick,
    className,
    ...rest
  }: {
    children: React.ReactNode
    onClick?: () => void
    className?: string
  } & Record<string, unknown>) => (
    <button type="button" onClick={onClick} className={className} {...rest}>
      {children}
    </button>
  ),
}))

import { WorkspaceTabBar, WORKSPACE_TABS, resolveActiveSegment } from './WorkspaceTabBar'

beforeEach(() => {
  mockNavigate.mockClear()
})

describe('WorkspaceTabBar — full strip (hidden @6xl:flex)', () => {
  it('renders the strip entries — registered panels are toggle buttons, unregistered entries are links (MAJ-007; wave 2 per side-panel-shell-spec.md §15 item 1: "Mail joins in wave 2")', () => {
    mockPathname = '/workspaces/ws-1/chat'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)

    for (const tab of WORKSPACE_TABS) {
      const els = screen.getAllByTestId(`workspace-tab-${tab.segment}`)
      expect(els.length).toBe(1)
      if (tab.segment === 'media' || tab.segment === 'mail') {
        // Registered panels (US-5): toggle buttons — no href, aria-pressed.
        // WAVE 2: mail joins media as a registered toggle (§15 item 1 —
        // "Tasks / Calendar / Library / Team entries toggle panels (Mail
        // joins in wave 2)"); at wave 1 it was a plain link.
        expect(els[0].tagName).toBe('BUTTON')
        expect(els[0].getAttribute('href')).toBeNull()
        expect(els[0].getAttribute('aria-pressed')).toBe('false')
      } else if (tab.segment === 'chat') {
        // The panel-host page entry: a button that navigates (US-5), so it
        // carries the page-activeness attribute itself.
        expect(els[0].tagName).toBe('BUTTON')
        expect(els[0].getAttribute('href')).toBeNull()
        expect(els[0].getAttribute('aria-current')).toBe('page')
      } else {
        expect(els[0].getAttribute('href')).toBe(`/workspaces/ws-1/${tab.segment}`)
      }
    }
  })

  it('marks the entry matching the current route with aria-current="page" (MAJ-007)', () => {
    mockPathname = '/workspaces/ws-1/board'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)

    // Unregistered-panel links keep navigation semantics: the active one
    // carries aria-current="page" (WCAG 1.3.1), never aria-selected — the
    // strip is not a tablist any more.
    const boardTabs = screen.getAllByTestId('workspace-tab-board')
    expect(boardTabs[0].getAttribute('aria-current')).toBe('page')

    // Chat is the panel-host page entry; on this route board is the page,
    // so chat carries no aria-current.
    const chatTab = screen.getByTestId('workspace-tab-chat')
    expect(chatTab.getAttribute('aria-current')).toBeNull()
  })

  it('full strip has the expected container-query classes (hidden @6xl:flex)', () => {
    mockPathname = '/workspaces/ws-1/chat'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    // The strip div should have the responsive classes. MAJ-007: it is not a
    // role="tablist" any more — a mixed set of links and toggles.
    const strip = screen.getByTestId('workspace-tab-strip')
    expect(strip.className).toContain('hidden')
    expect(strip.className).toContain('@6xl:flex')
  })

  it('tab order matches the canonical WORKSPACE_TABS order', () => {
    // 'mail' sits between media and team in the canonical strip (email spec
    // §16 route row: the mail segment landed as the Mail tab's redirect-stub
    // route, commit 5524ef853) — the pin below previously omitted it and was
    // stale-red against the production array.
    expect(WORKSPACE_TABS.map((t) => t.segment)).toEqual([
      'chat',
      'board',
      'calendar',
      'media',
      'mail',
      'team',
    ])
  })

  it('the board segment is labelled "Tasks" (ADR-051 D1 — Board/List/Graph collapse into one screen)', () => {
    const boardTab = WORKSPACE_TABS.find((t) => t.segment === 'board')
    expect(boardTab?.label).toBe('Tasks')
  })
})

describe('WorkspaceTabBar — view-switcher (flex @6xl:hidden)', () => {
  it('renders the view-switcher trigger button', () => {
    mockPathname = '/workspaces/ws-1/chat'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const switcher = screen.getByTestId('workspace-view-switcher')
    expect(switcher).toBeInTheDocument()
  })

  it('view-switcher shows the active view label', () => {
    mockPathname = '/workspaces/ws-1/board'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const switcher = screen.getByTestId('workspace-view-switcher')
    expect(switcher.textContent).toContain('Tasks')
  })

  it('view-switcher aria-label references the active view', () => {
    mockPathname = '/workspaces/ws-1/calendar'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const switcher = screen.getByTestId('workspace-view-switcher')
    expect(switcher.getAttribute('aria-label')).toContain('Calendar')
  })

  it('view-switcher is wrapped in a div with the container-query responsive classes (flex @6xl:hidden)', () => {
    mockPathname = '/workspaces/ws-1/chat'
    const { container } = render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    // Walk up from the trigger to find any ancestor div with @6xl:hidden
    // (the Radix mock inserts an extra wrapper div between the trigger and our wrapper)
    const switcher = screen.getByTestId('workspace-view-switcher')
    let el: Element | null = switcher.parentElement
    let found = false
    while (el) {
      if (el.className.includes('@6xl:hidden') && el.className.includes('flex')) {
        found = true
        break
      }
      el = el.parentElement
    }
    expect(found, `No ancestor div with 'flex @6xl:hidden' found. Container HTML:\n${container.innerHTML}`).toBe(true)
  })

  it('view-switcher menu renders all four view tab options', () => {
    mockPathname = '/workspaces/ws-1/chat'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    // The DropdownMenuContent stub renders with data-testid="view-switcher-menu"
    const menu = screen.getByTestId('view-switcher-menu')
    expect(menu).toBeInTheDocument()
    // All four view-tab labels should appear in the menu (plus the settings
    // entry, covered separately below).
    for (const tab of WORKSPACE_TABS) {
      expect(menu.textContent).toContain(tab.label)
    }
  })

  it('compact dropdown contains a settings entry that navigates to /workspaces/$id/settings', () => {
    // Narrow viewports (<1152px, e.g. phones) hide the full-strip name
    // button entirely — the compact dropdown is the ONLY settings entry
    // point in the header at those widths, so it must carry one too.
    mockPathname = '/workspaces/ws-1/chat'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const menu = screen.getByTestId('view-switcher-menu')
    const settingsEntry = screen.getByTestId('workspace-view-switcher-settings')
    expect(menu.contains(settingsEntry)).toBe(true)
    expect(settingsEntry.textContent).toContain('Settings')
    // Not the active entry here (chat is active) — no aria-current.
    expect(settingsEntry).not.toHaveAttribute('aria-current')

    fireEvent.click(settingsEntry)
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/workspaces/$workspaceId/settings',
      params: { workspaceId: 'ws-1' },
    })
  })

  it('compact dropdown marks the settings entry selected on the settings route', () => {
    mockPathname = '/workspaces/ws-1/settings'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const settingsEntry = screen.getByTestId('workspace-view-switcher-settings')
    expect(settingsEntry.className).toContain('text-[var(--color-accent)]')
    // WCAG 1.3.1 — activeness must be conveyed programmatically (aria-current),
    // not only via accent colour + the aria-hidden "●" dot.
    expect(settingsEntry).toHaveAttribute('aria-current', 'page')
    // The switcher trigger itself should also read "Settings" while active.
    const switcher = screen.getByTestId('workspace-view-switcher')
    expect(switcher.textContent).toContain('Settings')
  })

  it('compact dropdown marks the active view tab item with aria-current="page", and every other tab item with none', () => {
    // WCAG 1.3.1 — the mapped WORKSPACE_TABS items in the compact dropdown
    // must also carry aria-current, not just the settings entry.
    mockPathname = '/workspaces/ws-1/board'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const menu = screen.getByTestId('view-switcher-menu')

    for (const tab of WORKSPACE_TABS) {
      const item = Array.from(menu.querySelectorAll('button')).find((el) =>
        el.textContent?.includes(tab.label),
      )
      expect(item, `no dropdown item found for ${tab.segment}`).toBeTruthy()
      if (tab.segment === 'board') {
        expect(item).toHaveAttribute('aria-current', 'page')
      } else {
        expect(item).not.toHaveAttribute('aria-current')
      }
    }
    // The settings entry is also not active on this route.
    expect(screen.getByTestId('workspace-view-switcher-settings')).not.toHaveAttribute(
      'aria-current',
    )
  })
})

describe('WorkspaceTabBar — tab test ids', () => {
  it('exposes each workspace-tab-<segment> test id exactly once (unique for e2e)', () => {
    mockPathname = '/workspaces/ws-1/chat'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    // Exactly one element per segment (the full strip). No duplicate test ids,
    // so Playwright getByTestId('workspace-tab-board') stays unambiguous.
    for (const tab of WORKSPACE_TABS) {
      const els = screen.getAllByTestId(`workspace-tab-${tab.segment}`)
      expect(els.length).toBe(1)
    }
  })
})

describe('resolveActiveSegment', () => {
  it('resolves the active segment from a tab pathname', () => {
    expect(resolveActiveSegment('/workspaces/ws-1/board', 'ws-1')).toBe('board')
    expect(resolveActiveSegment('/workspaces/ws-1/team', 'ws-1')).toBe('team')
  })

  it('defaults to chat for the bare container path (index redirect target)', () => {
    expect(resolveActiveSegment('/workspaces/ws-1', 'ws-1')).toBe('chat')
  })

  it('defaults to chat for an unrelated path or unknown segment', () => {
    expect(resolveActiveSegment('/agents', 'ws-1')).toBe('chat')
    expect(resolveActiveSegment('/workspaces/ws-1/bogus', 'ws-1')).toBe('chat')
  })

  it('defaults to chat for the retired list/graph segments (ADR-051 D1 — no longer WORKSPACE_TABS entries; their routes redirect to board before this would ever be read live)', () => {
    expect(resolveActiveSegment('/workspaces/ws-1/list', 'ws-1')).toBe('chat')
    expect(resolveActiveSegment('/workspaces/ws-1/graph', 'ws-1')).toBe('chat')
  })

  it('resolves the settings segment for the workspace settings route', () => {
    // Regression test: 'settings' is a real segment (reached via the
    // workspace-name button / compact dropdown, not a WORKSPACE_TABS entry)
    // and previously had no coverage — every other branch was tested.
    expect(resolveActiveSegment('/workspaces/ws-1/settings', 'ws-1')).toBe('settings')
  })
})

describe('WorkspaceTabBar — workspace-name entry (settings, full strip)', () => {
  it('renders as a plain settings button, first child of the strip (MAJ-007 — no role="tab")', () => {
    mockPathname = '/workspaces/ws-1/chat'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const strip = screen.getByTestId('workspace-tab-strip')
    const nameButton = screen.getByTestId('workspace-name-button')

    expect(nameButton).toBeInTheDocument()
    expect(nameButton.getAttribute('role')).not.toBe('tab')
    // Inside the strip (not a stray sibling) — and first in DOM order,
    // ahead of all five entries.
    expect(strip.contains(nameButton)).toBe(true)
    expect(strip.children[0]).toBe(nameButton)
  })

  it('is aria-current="page" on the settings route', () => {
    mockPathname = '/workspaces/ws-1/settings'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const nameButton = screen.getByTestId('workspace-name-button')
    expect(nameButton.getAttribute('aria-current')).toBe('page')
  })

  it('carries no aria-current on a non-settings route', () => {
    mockPathname = '/workspaces/ws-1/chat'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const nameButton = screen.getByTestId('workspace-name-button')
    expect(nameButton.getAttribute('aria-current')).toBeNull()
  })

  it('navigates to the workspace settings route when clicked', () => {
    mockPathname = '/workspaces/ws-1/chat'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const nameButton = screen.getByTestId('workspace-name-button')

    fireEvent.click(nameButton)

    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/workspaces/$workspaceId/settings',
      params: { workspaceId: 'ws-1' },
    })
  })
})
