import React from 'react'
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'

// Mock TanStack Router primitives the tab bar uses (Link, useLocation, useNavigate).
let mockPathname = '/workspaces/ws-1/chat'
vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: mockPathname }),
  useNavigate: () => vi.fn(),
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
  }: {
    children: React.ReactNode
    onClick?: () => void
    className?: string
  }) => (
    <button type="button" onClick={onClick} className={className}>
      {children}
    </button>
  ),
}))

import { WorkspaceTabBar, WORKSPACE_TABS, resolveActiveSegment } from './WorkspaceTabBar'

describe('WorkspaceTabBar — full strip (hidden @6xl:flex)', () => {
  it('renders all seven workspace tab links in the full strip with correct test ids and hrefs', () => {
    mockPathname = '/workspaces/ws-1/chat'
    render(<WorkspaceTabBar workspaceId="ws-1" />)

    // All 7 tabs present — each segment appears at least once (full strip + sr-only strip).
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
    render(<WorkspaceTabBar workspaceId="ws-1" />)

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
    render(<WorkspaceTabBar workspaceId="ws-1" />)
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
      'settings',
    ])
  })
})

describe('WorkspaceTabBar — view-switcher (flex @6xl:hidden)', () => {
  it('renders the view-switcher trigger button', () => {
    mockPathname = '/workspaces/ws-1/chat'
    render(<WorkspaceTabBar workspaceId="ws-1" />)
    const switcher = screen.getByTestId('workspace-view-switcher')
    expect(switcher).toBeInTheDocument()
  })

  it('view-switcher shows the active view label', () => {
    mockPathname = '/workspaces/ws-1/board'
    render(<WorkspaceTabBar workspaceId="ws-1" />)
    const switcher = screen.getByTestId('workspace-view-switcher')
    expect(switcher.textContent).toContain('Board')
  })

  it('view-switcher aria-label references the active view', () => {
    mockPathname = '/workspaces/ws-1/calendar'
    render(<WorkspaceTabBar workspaceId="ws-1" />)
    const switcher = screen.getByTestId('workspace-view-switcher')
    expect(switcher.getAttribute('aria-label')).toContain('Calendar')
  })

  it('view-switcher is wrapped in a div with the container-query responsive classes (flex @6xl:hidden)', () => {
    mockPathname = '/workspaces/ws-1/chat'
    const { container } = render(<WorkspaceTabBar workspaceId="ws-1" />)
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

  it('view-switcher menu renders all 7 view options', () => {
    mockPathname = '/workspaces/ws-1/chat'
    render(<WorkspaceTabBar workspaceId="ws-1" />)
    // The DropdownMenuContent stub renders with data-testid="view-switcher-menu"
    const menu = screen.getByTestId('view-switcher-menu')
    expect(menu).toBeInTheDocument()
    // All 7 view labels should appear in the menu
    for (const tab of WORKSPACE_TABS) {
      expect(menu.textContent).toContain(tab.label)
    }
  })
})

describe('WorkspaceTabBar — tab test ids', () => {
  it('exposes each workspace-tab-<segment> test id exactly once (unique for e2e)', () => {
    mockPathname = '/workspaces/ws-1/chat'
    render(<WorkspaceTabBar workspaceId="ws-1" />)
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
})
