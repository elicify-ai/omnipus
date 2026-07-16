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
    role,
    'aria-selected': ariaSelected,
    'data-testid': testId,
    'aria-label': ariaLabel,
  }: {
    children: React.ReactNode
    to: string
    params?: Record<string, string>
    role?: string
    'aria-selected'?: boolean
    'data-testid'?: string
    'aria-label'?: string
  }) => {
    const href = params ? to.replace('$workspaceId', params.workspaceId) : to
    return (
      <a href={href} role={role} aria-selected={ariaSelected} data-testid={testId} aria-label={ariaLabel}>
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
  it('renders all six workspace tab links in the full strip with correct test ids and hrefs', () => {
    mockPathname = '/workspaces/ws-1/chat'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)

    // All six tabs present — each segment appears at least once (full strip + sr-only strip).
    for (const tab of WORKSPACE_TABS) {
      const els = screen.getAllByTestId(`workspace-tab-${tab.segment}`)
      expect(els.length).toBeGreaterThan(0)
      // All instances share the correct href
      els.forEach((el) => {
        expect(el.getAttribute('href')).toBe(`/workspaces/ws-1/${tab.segment}`)
      })
    }
  })

  it('marks the tab matching the current route as selected', () => {
    mockPathname = '/workspaces/ws-1/board'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)

    // aria-selected=true on board tabs, false on chat tabs (may be multiple from sr-only strip)
    const boardTabs = screen.getAllByTestId('workspace-tab-board')
    boardTabs.forEach((el) => {
      expect(el.getAttribute('aria-selected')).toBe('true')
    })
    const chatTabs = screen.getAllByTestId('workspace-tab-chat')
    chatTabs.forEach((el) => {
      expect(el.getAttribute('aria-selected')).toBe('false')
    })
  })

  it('full strip has the expected container-query classes (hidden @6xl:flex)', () => {
    mockPathname = '/workspaces/ws-1/chat'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    // The tablist div should have the responsive classes
    const tablist = screen.getByRole('tablist', { name: 'Workspace views' })
    expect(tablist.className).toContain('hidden')
    expect(tablist.className).toContain('@6xl:flex')
  })

  it('tab order matches the canonical WORKSPACE_TABS order', () => {
    expect(WORKSPACE_TABS.map((t) => t.segment)).toEqual([
      'chat',
      'board',
      'list',
      'graph',
      'calendar',
      'team',
    ])
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
    expect(switcher.textContent).toContain('Board')
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

  it('view-switcher menu renders all six view tab options', () => {
    mockPathname = '/workspaces/ws-1/chat'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    // The DropdownMenuContent stub renders with data-testid="view-switcher-menu"
    const menu = screen.getByTestId('view-switcher-menu')
    expect(menu).toBeInTheDocument()
    // All six view-tab labels should appear in the menu (plus the settings
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
    expect(resolveActiveSegment('/workspaces/ws-1/graph', 'ws-1')).toBe('graph')
    expect(resolveActiveSegment('/workspaces/ws-1/team', 'ws-1')).toBe('team')
  })

  it('defaults to chat for the bare container path (index redirect target)', () => {
    expect(resolveActiveSegment('/workspaces/ws-1', 'ws-1')).toBe('chat')
  })

  it('defaults to chat for an unrelated path or unknown segment', () => {
    expect(resolveActiveSegment('/agents', 'ws-1')).toBe('chat')
    expect(resolveActiveSegment('/workspaces/ws-1/bogus', 'ws-1')).toBe('chat')
  })

  it('resolves the settings segment for the workspace settings route', () => {
    // Regression test: 'settings' is a real segment (reached via the
    // workspace-name button / compact dropdown, not a WORKSPACE_TABS entry)
    // and previously had no coverage — every other branch was tested.
    expect(resolveActiveSegment('/workspaces/ws-1/settings', 'ws-1')).toBe('settings')
  })
})

describe('WorkspaceTabBar — workspace-name button (settings entry, full strip)', () => {
  it('renders as role="tab", first inside the tablist', () => {
    mockPathname = '/workspaces/ws-1/chat'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const tablist = screen.getByRole('tablist', { name: 'Workspace views' })
    const nameButton = screen.getByTestId('workspace-name-button')

    expect(nameButton).toBeInTheDocument()
    expect(nameButton.getAttribute('role')).toBe('tab')
    // Inside the tablist (not a stray sibling) — and first in DOM order,
    // ahead of all six view tabs.
    expect(tablist.contains(nameButton)).toBe(true)
    expect(tablist.children[0]).toBe(nameButton)
  })

  it('is aria-selected on the settings route', () => {
    mockPathname = '/workspaces/ws-1/settings'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const nameButton = screen.getByTestId('workspace-name-button')
    expect(nameButton.getAttribute('aria-selected')).toBe('true')
  })

  it('is not aria-selected on a non-settings route', () => {
    mockPathname = '/workspaces/ws-1/chat'
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const nameButton = screen.getByTestId('workspace-name-button')
    expect(nameButton.getAttribute('aria-selected')).toBe('false')
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
