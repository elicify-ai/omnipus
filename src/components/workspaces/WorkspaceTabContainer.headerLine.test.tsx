// Wiring contract between the workspace header row and the strip's measurement
// (#1449 D13, founder 2026-10-28: "the workspace strip ALWAYS keeps the header
// line; the chat slot shrinks and truncates first"; R44, founder 2026-10-07).
//
// useWorkspaceHeaderMode reads the header line from the DOM: the strip must be a
// direct child of the element marked `data-header-line`, and the chat slot another
// direct child of it. Lose the marker and the strip silently goes back to measuring
// its own flex slot — the F1 regression (1440 px, sidebar closed, Tasks List open:
// the chat slot takes its width first and the strip steps down to icons although the
// full labelled row fits). Only a browser sees that, because jsdom has no layout
// (tests/e2e/workspace-header-standalone.spec.ts, case F1), so the marker and the
// parent/child shape it relies on are pinned here. The container itself is real; only
// the router, the stores and the query layer around it are stubbed, as in
// WorkspaceTabContainer.test.tsx.

import React from 'react'
import { describe, it, expect, vi } from 'vitest'
import { act, render, screen } from '@testing-library/react'

vi.mock('@tanstack/react-router', () => ({
  Outlet: () => <div data-testid="outlet" />,
  useNavigate: () => vi.fn(),
  useLocation: () => ({ pathname: '/workspaces/ws-1/chat', search: {} }),
}))

vi.mock('framer-motion', () => ({
  motion: {
    div: ({ children, ...rest }: React.HTMLAttributes<HTMLDivElement>) => <div {...rest}>{children}</div>,
  },
}))

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: vi.fn().mockImplementation(({ queryKey }: { queryKey: unknown[] }) => {
      const key = JSON.stringify(queryKey)
      if (key.includes('app-state')) {
        return { data: { onboarding_complete: true, dev_mode_bypass: false }, isLoading: false, isError: false }
      }
      if (key.includes('archived')) return { data: [], isLoading: false, isError: false }
      return {
        data: [{ id: 'ws-1', name: 'My Workspace', is_default: true, core_team: [] }],
        isLoading: false,
        isError: false,
        refetch: vi.fn(),
      }
    }),
  }
})

vi.mock('@/store/sidebar', () => ({
  useSidebarStore: (selector: ((s: { open: () => void; pin: () => void; isOpen: boolean; isPinned: boolean }) => unknown) | undefined) => {
    const state = { open: vi.fn(), pin: vi.fn(), isOpen: false, isPinned: false }
    return selector ? selector(state) : state
  },
  SIDEBAR_PIN_BREAKPOINT: 1024,
}))

vi.mock('@/store/session', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/store/session')>()),
  useSessionStore: (selector: ((s: { enterWorkspaceChat: (id: string) => void }) => unknown) | undefined) => {
    const state = { enterWorkspaceChat: vi.fn() }
    return selector ? selector(state) : state
  },
}))

vi.mock('@/store/workspacesStore', () => ({
  useWorkspacesStore: (selector?: ((s: Record<string, unknown>) => unknown)) => {
    const state = {
      activeWorkspaceId: null,
      setActiveWorkspaceId: vi.fn(),
      activePlanId: null,
      setActivePlanId: vi.fn(),
      boardAltitude: 'top-level',
      setBoardAltitude: vi.fn(),
    }
    return selector ? selector(state) : state
  },
}))

vi.mock('@/components/chat/ChatControls', () => ({
  ChatControls: () => <div data-testid="chat-controls-mock">ChatControls</div>,
}))

import { WorkspaceTabContainer } from './WorkspaceTabContainer'

describe('WorkspaceTabContainer — the header line the strip measures against', () => {
  it('marks the banner as the header line and makes the strip a direct child of it', async () => {
    await act(async () => { render(<WorkspaceTabContainer workspaceId="ws-1" />) })
    const topBar = screen.getByTestId('workspace-top-bar')
    expect(topBar).toHaveAttribute('data-header-line')
    expect(screen.getByTestId('workspace-header-entries').parentElement).toBe(topBar)
  })

  it('puts the chat slot and the chat controls on the same line, as direct children of the marked banner', async () => {
    await act(async () => { render(<WorkspaceTabContainer workspaceId="ws-1" />) })
    const topBar = screen.getByTestId('workspace-top-bar')
    expect(screen.getByTestId('chat-header-slot').parentElement).toBe(topBar)
    expect(screen.getByTestId('workspace-chat-controls').parentElement).toBe(topBar)
  })

  it('marks no other element as a header line (the strip would measure against the wrong ancestor)', async () => {
    let view: ReturnType<typeof render> | undefined
    await act(async () => { view = render(<WorkspaceTabContainer workspaceId="ws-1" />) })
    expect([...(view?.container.querySelectorAll('[data-header-line]') ?? [])]).toEqual([screen.getByTestId('workspace-top-bar')])
  })
})
