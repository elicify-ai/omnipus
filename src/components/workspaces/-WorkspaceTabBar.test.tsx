import React from 'react'
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'

// Mock TanStack Router primitives the tab bar uses (Link, useLocation).
let mockPathname = '/workspaces/ws-1/chat'
vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: mockPathname }),
  Link: ({
    children,
    to,
    params,
    role,
    'aria-selected': ariaSelected,
    'data-testid': testId,
  }: {
    children: React.ReactNode
    to: string
    params?: Record<string, string>
    role?: string
    'aria-selected'?: boolean
    'data-testid'?: string
  }) => {
    const href = params ? to.replace('$workspaceId', params.workspaceId) : to
    return (
      <a href={href} role={role} aria-selected={ariaSelected} data-testid={testId}>
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

import { WorkspaceTabBar, WORKSPACE_TABS, resolveActiveSegment } from './WorkspaceTabBar'

describe('WorkspaceTabBar', () => {
  it('renders all seven workspace tabs as deep-linkable tabs', () => {
    mockPathname = '/workspaces/ws-1/chat'
    render(<WorkspaceTabBar workspaceId="ws-1" />)

    // All 7 tabs present, each linking to its sub-route under the workspace.
    for (const tab of WORKSPACE_TABS) {
      const el = screen.getByTestId(`workspace-tab-${tab.segment}`)
      expect(el).toBeTruthy()
      expect(el.getAttribute('href')).toBe(`/workspaces/ws-1/${tab.segment}`)
    }
    // Exactly the expected seven tabs in the expected order.
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

  it('marks the tab matching the current route as selected', () => {
    mockPathname = '/workspaces/ws-1/board'
    render(<WorkspaceTabBar workspaceId="ws-1" />)

    expect(screen.getByTestId('workspace-tab-board').getAttribute('aria-selected')).toBe('true')
    expect(screen.getByTestId('workspace-tab-chat').getAttribute('aria-selected')).toBe('false')
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
